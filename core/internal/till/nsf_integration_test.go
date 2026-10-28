//go:build integration

// nsf_integration_test.go, W5 A-05 integration tests against the migrated + seeded Postgres (`make reset`).
// These exercise the REAL atomic NSF reversal + credit-hold placement + declined-attempt log against live
// tables (0035 + 0009 + 0010), and assert the invariants the unit fakes cannot: the invoice actually
// reopens, the AR tie-out stays exact, NO tax_decision_snapshot row is written, and the events land in the
// outbox in the same tx. Run: make reset && go test -tags integration ./internal/till/...
package till

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedNSFCase inserts a customer + a FINALIZED-then-PAID invoice + a RECEIVED cheque payment fully
// allocated to it, so MarkNSF has a real bounce to reverse. Returns the ids + a cleanup.
func seedNSFCase(t *testing.T, pool *pgxpool.Pool, branchID string, totalCents int64) (paymentID, invoiceID, customerID string, cleanup func()) {
	t.Helper()
	ctx := context.Background()
	suffix := t.Name()

	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ('NSF-'||$1, 'NSF Test Co '||$1, 'CONTRACTOR', $2::uuid, 1000000) RETURNING id::text`,
		suffix, branchID).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	// Invoice starts life PAID with a zero balance (the cheque cleared it).
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date, subtotal_cents, tax_cents, total_cents, balance_cents)
		 VALUES ('INV-NSF-'||$1, $2::uuid, $3::uuid, 'PAID', (now() AT TIME ZONE 'America/Toronto')::date, (now() AT TIME ZONE 'America/Toronto')::date + 30, $4, 0, $4, 0) RETURNING id::text`,
		suffix, customerID, branchID, totalCents).Scan(&invoiceID); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, branch_id, gateway_ref)
		 VALUES ('PMT-NSF-'||$1, $2::uuid, 'CHEQUE', $4, 'RECEIVED', $3::uuid, 'chq-'||$1) RETURNING id::text`,
		suffix, customerID, branchID, totalCents).Scan(&paymentID); err != nil {
		t.Fatalf("seed payment: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO payment_allocations (payment_id, invoice_id, amount_cents) VALUES ($1::uuid, $2::uuid, $3)`,
		paymentID, invoiceID, totalCents); err != nil {
		t.Fatalf("seed allocation: %v", err)
	}
	cleanup = func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE payload->>'payment_id' = $1`, paymentID)
		_, _ = pool.Exec(c, `DELETE FROM credit_holds WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM payment_nsf WHERE payment_id = $1::uuid`, paymentID)
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM payment_allocations WHERE payment_id = $1::uuid`, paymentID)
		_, _ = pool.Exec(c, `DELETE FROM payments WHERE id = $1::uuid`, paymentID)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE id = $1::uuid`, invoiceID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, customerID)
	}
	return paymentID, invoiceID, customerID, cleanup
}

// IN1 (AC2 + AC3), the full atomic reversal: BOUNCED, invoice reopened, the allocation kept and the
// undo written beside it as a reversal row (0450), NSF + fee + reversal subledger rows written,
// credit hold placed, payment.bounced emitted once, and, the keystone -
// ZERO tax_decision_snapshot rows written. The AR tie-out (invoice + fee) stays exact.
func TestIntegration_NSF_FullReversal(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	svc := realSvc(pool)
	cctx := globalCtx(userID)

	pid, invID, custID, cleanup := seedNSFCase(t, pool, branchID, 50000)
	t.Cleanup(cleanup)

	var snapBefore int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM tax_decision_snapshot`).Scan(&snapBefore)

	res, err := svc.MarkNSF(cctx, NSFInput{PaymentID: pid, Reason: "cheque returned NSF"})
	if err != nil {
		t.Fatalf("MarkNSF: %v", err)
	}
	if res.Status != "BOUNCED" || res.ReversedCents != 50000 || res.ReopenedInvoices != 1 || !res.HoldPlaced {
		t.Fatalf("unexpected result: %+v", res)
	}

	// Payment flipped BOUNCED.
	var pStatus string
	_ = pool.QueryRow(ctx, `SELECT status FROM payments WHERE id = $1`, pid).Scan(&pStatus)
	if pStatus != "BOUNCED" {
		t.Fatalf("payment status = %s, want BOUNCED", pStatus)
	}
	// Invoice reopened (balance restored, status FINALIZED).
	var invBalance int64
	var invStatus string
	_ = pool.QueryRow(ctx, `SELECT balance_cents, status FROM invoices WHERE id = $1`, invID).Scan(&invBalance, &invStatus)
	if invBalance != 50000 || invStatus != "FINALIZED" {
		t.Fatalf("invoice not reopened: balance=%d status=%s", invBalance, invStatus)
	}
	// Allocation reversed: the application is KEPT and the undo is written beside it as a reversal
	// row (0450), the same payment and the same invoice, the exact negative. The two net to zero, so
	// the figures the bounce moves are the figures it moved before.
	var nAlloc int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM payment_allocations WHERE payment_id = $1`, pid).Scan(&nAlloc)
	if nAlloc != 2 {
		t.Fatalf("the bounce left %d allocation rows, want 2 (the application and the reversal beside it)", nAlloc)
	}
	var allocNet int64
	_ = pool.QueryRow(ctx, `SELECT COALESCE(SUM(amount_cents), 0) FROM payment_allocations WHERE payment_id = $1`, pid).Scan(&allocNet)
	if allocNet != 0 {
		t.Fatalf("the bounced payment's allocations net %d, want 0", allocNet)
	}
	// NSF record captured the policy fee ($45 placeholder).
	var feeCents int64
	if err := pool.QueryRow(ctx, `SELECT fee_cents FROM payment_nsf WHERE payment_id = $1`, pid).Scan(&feeCents); err != nil {
		t.Fatalf("no payment_nsf row: %v", err)
	}
	if feeCents != 4500 {
		t.Fatalf("nsf fee = %d, want 4500", feeCents)
	}
	// Subledger: a reversal NSF row (excluded from AR) + a fee ADJUSTMENT row (counted).
	var nReversal, nFee int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM customer_transactions WHERE customer_id = $1 AND txn_type='NSF' AND source_type='invoice'`, custID).Scan(&nReversal)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM customer_transactions WHERE customer_id = $1 AND txn_type='ADJUSTMENT' AND source_type='payment_nsf'`, custID).Scan(&nFee)
	if nReversal != 1 || nFee != 1 {
		t.Fatalf("subledger rows wrong: reversal=%d fee=%d", nReversal, nFee)
	}
	// Credit hold ACTIVE (exactly one).
	var nHold int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM credit_holds WHERE customer_id = $1 AND status='ACTIVE'`, custID).Scan(&nHold)
	if nHold != 1 {
		t.Fatalf("active holds = %d, want 1", nHold)
	}
	// payment.bounced emitted exactly once, same tx.
	var nEvent int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM events_outbox WHERE topic='payment.bounced' AND payload->>'payment_id' = $1`, pid).Scan(&nEvent)
	if nEvent != 1 {
		t.Fatalf("payment.bounced events = %d, want 1", nEvent)
	}
	// ZERO tax_decision_snapshot writes (the sale stands; only the cash failed).
	var snapAfter int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM tax_decision_snapshot`).Scan(&snapAfter)
	if snapAfter != snapBefore {
		t.Fatalf("tax_decision_snapshot changed: before=%d after=%d (NSF must not touch snapshots)", snapBefore, snapAfter)
	}

	// AR tie-out: ARBalanceCents-equivalent (invoice + non-invoice) == v_ar_aging total == 54500, and NOT
	// double-counted (would be 104500 if the reversal row leaked into the union).
	arBalance := arBalanceFor(t, pool, custID)
	if arBalance != 54500 {
		t.Fatalf("AR balance = %d, want 54500 (invoice 50000 + fee 4500)", arBalance)
	}
	agingTotal := agingTotalFor(t, pool, custID)
	if agingTotal != arBalance {
		t.Fatalf("AR aging total %d != AR balance %d (tie-out broken)", agingTotal, arBalance)
	}
}

// IN2 (AC6), a second NSF on the already-BOUNCED payment is a 409 and writes no second event/hold.
func TestIntegration_NSF_ReplayGuard(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	svc := realSvc(pool)
	cctx := globalCtx(userID)

	pid, _, custID, cleanup := seedNSFCase(t, pool, branchID, 20000)
	t.Cleanup(cleanup)

	if _, err := svc.MarkNSF(cctx, NSFInput{PaymentID: pid, Reason: "nsf"}); err != nil {
		t.Fatalf("first MarkNSF: %v", err)
	}
	_, err := svc.MarkNSF(cctx, NSFInput{PaymentID: pid, Reason: "nsf again"})
	wantStatus(t, err, 409)

	var nEvent, nHold int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM events_outbox WHERE topic='payment.bounced' AND payload->>'payment_id' = $1`, pid).Scan(&nEvent)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM credit_holds WHERE customer_id = $1 AND status='ACTIVE'`, custID).Scan(&nHold)
	if nEvent != 1 || nHold != 1 {
		t.Fatalf("replay caused duplicates: events=%d holds=%d", nEvent, nHold)
	}
}

// IN3 (AC5), HasActiveHold reflects the real credit_holds table, and a Tier-1 release clears it.
func TestIntegration_NSF_HoldLifecycle(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	svc := realSvc(pool).WithAudit(noopRecorder{})
	cctx := globalCtx(userID)

	pid, _, custID, cleanup := seedNSFCase(t, pool, branchID, 30000)
	t.Cleanup(cleanup)

	if _, err := svc.MarkNSF(cctx, NSFInput{PaymentID: pid, Reason: "nsf"}); err != nil {
		t.Fatalf("MarkNSF: %v", err)
	}
	held, err := svc.store.HasActiveHold(ctx, custID)
	if err != nil || !held {
		t.Fatalf("HasActiveHold = %v,%v want true,nil", held, err)
	}
	var holdID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM credit_holds WHERE customer_id = $1 AND status='ACTIVE'`, custID).Scan(&holdID); err != nil {
		t.Fatalf("read hold: %v", err)
	}
	if err := svc.ReleaseHold(cctx, ReleaseHoldInput{HoldID: holdID, Reason: "customer settled by wire"}); err != nil {
		t.Fatalf("ReleaseHold: %v", err)
	}
	held, _ = svc.store.HasActiveHold(ctx, custID)
	if held {
		t.Fatalf("customer still on hold after release")
	}
	// A second release of the same hold is a 404 (already RELEASED).
	wantStatus(t, svc.ReleaseHold(cctx, ReleaseHoldInput{HoldID: holdID, Reason: "again"}), 404)
}

// IN4 (AC7), the declined-attempt log persists a DECLINED attempt and dedupes on the key.
func TestIntegration_NSF_PaymentAttempts(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	svc := realSvc(pool)
	cctx := globalCtx(userID)

	key := "itest-attempt-" + t.Name()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM payment_attempts WHERE idempotency_key = $1`, key)
	})

	att := PaymentAttempt{IdempotencyKey: key, AmountCents: 8000, Method: MethodCard, Processor: "run_payments", Result: "DECLINED", Detail: "insufficient funds", BranchID: branchID}
	if err := svc.RecordPaymentAttempt(cctx, att); err != nil {
		t.Fatalf("record attempt: %v", err)
	}
	if err := svc.RecordPaymentAttempt(cctx, att); err != nil {
		t.Fatalf("replay attempt: %v", err)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM payment_attempts WHERE idempotency_key = $1`, key).Scan(&n)
	if n != 1 {
		t.Fatalf("payment_attempts rows = %d, want 1 (idempotent)", n)
	}
}

// - integration helpers -

// arBalanceFor mirrors billing.ARBalanceCents (open invoices + non-invoice AR txns) for a customer.
func arBalanceFor(t *testing.T, pool *pgxpool.Pool, customerID string) int64 {
	t.Helper()
	var sum int64
	if err := pool.QueryRow(context.Background(),
		`SELECT
		   (SELECT COALESCE(SUM(balance_cents),0) FROM invoices
		     WHERE customer_id = $1 AND status IN ('FINALIZED','PARTIALLY_PAID') AND balance_cents <> 0)
		 + (SELECT COALESCE(SUM(amount_cents),0) FROM customer_transactions
		     WHERE customer_id = $1 AND txn_type IN ('NSF','ADJUSTMENT','CREDIT_MEMO','WRITE_OFF') AND source_type <> 'invoice')`,
		customerID).Scan(&sum); err != nil {
		t.Fatalf("ar balance: %v", err)
	}
	return sum
}

// agingTotalFor sums the 0034 v_ar_aging view for one customer (the tie-out target).
func agingTotalFor(t *testing.T, pool *pgxpool.Pool, customerID string) int64 {
	t.Helper()
	var sum int64
	if err := pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(amount_cents),0) FROM v_ar_aging WHERE customer_id = $1`, customerID).Scan(&sum); err != nil {
		t.Fatalf("aging total: %v", err)
	}
	return sum
}

// noopRecorder satisfies audit.Recorder for the release-audit path in integration.
type noopRecorder struct{}

func (noopRecorder) Record(context.Context, audit.SensitiveAccess) error { return nil }
