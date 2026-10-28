//go:build integration

// history_integration_test.go, Q-044 payment attribution against the real schema. Proves:
//   - TakeTender writes the explicit PAYMENT_RECEIVED audit row (who/method/amount/payment_no);
//   - Allocate stamps allocated_by AND writes the PAYMENT_ALLOCATED audit row;
//   - the read side answers both directions with names: PaymentHistory (who took it, what it paid)
//     and InvoicePayments (which payments settled this invoice, by whom, when).
//
// Run: make reset && go test -tags integration ./internal/till/...
package till

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedCustomerInvoice inserts a customer + FINALIZED invoice for attribution reads (cleaned up).
func seedCustomerInvoice(t *testing.T, pool *pgxpool.Pool, branchID string, totalCents int64) (customerID, invoiceID string) {
	t.Helper()
	ctx := context.Background()
	suffix := t.Name()
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ('Q44-'||$1, 'Q044 Test '||$1, 'CONTRACTOR', $2::uuid, 1000000) RETURNING id::text`,
		suffix, branchID).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date, subtotal_cents, tax_cents, total_cents, balance_cents)
		 VALUES ('INV-Q44-'||$1, $2::uuid, $3::uuid, 'FINALIZED', (now() AT TIME ZONE 'America/Toronto')::date, (now() AT TIME ZONE 'America/Toronto')::date + 30, $4, 0, $4, $4) RETURNING id::text`,
		suffix, customerID, branchID, totalCents).Scan(&invoiceID); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		// Order matters (M3): the customer's payments (and NSF/hold side-effects) must go BEFORE the
		// customer row, payments.customer_id FK-rejects the customer delete otherwise, and the
		// swallowed error used to leave an orphan that collides on account_no on every later run.
		// Errors are REPORTED so a broken cleanup can never poison the DB silently again.
		steps := []struct{ q, id string }{
			{`DELETE FROM payment_allocations WHERE invoice_id = $1::uuid`, invoiceID},
			{`DELETE FROM payment_allocations WHERE payment_id IN (SELECT id FROM payments WHERE customer_id = $1::uuid)`, customerID},
			{`DELETE FROM credit_holds WHERE customer_id = $1::uuid`, customerID},
			{`DELETE FROM payment_nsf WHERE payment_id IN (SELECT id FROM payments WHERE customer_id = $1::uuid)`, customerID},
			{`DELETE FROM events_outbox WHERE payload->>'customer_id' = $1`, customerID},
			{`DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, customerID},
			{`DELETE FROM payments WHERE customer_id = $1::uuid`, customerID},
			{`DELETE FROM invoices WHERE id = $1::uuid`, invoiceID},
			{`DELETE FROM customers WHERE id = $1::uuid`, customerID},
		}
		for _, s := range steps {
			if _, err := pool.Exec(c, s.q, s.id); err != nil {
				t.Errorf("seedCustomerInvoice cleanup failed (%s): %v", s.q, err)
			}
		}
	})
	return customerID, invoiceID
}

// Q-044 end to end: take a tender, allocate it, then read both attribution projections and the
// explicit audit records back.
func TestIntegration_Q044_AttributionAndAuditTrail(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)

	auditSvc := audit.NewService(audit.NewRepository(pool), nil)
	svc := realSvc(pool).WithAudit(auditSvc)
	cctx := globalCtx(userID)

	sid, err := svc.OpenTill(cctx, OpenTillInput{BranchID: branchID, Station: "ITEST-Q044", FloatCents: 0})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { cleanupSession(pool, sid) })

	customerID, invoiceID := seedCustomerInvoice(t, pool, branchID, 4500)

	// Take the tender + allocate it inside one tx (the A-19 shape).
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	res, err := svc.TakeTender(cctx, tx, TenderInput{
		TillSessionID: sid, Method: MethodCash, AmountCents: 4500,
		CustomerID: customerID, IdempotencyKey: "q044-" + sid,
	})
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("TakeTender: %v", err)
	}
	if err := svc.Allocate(cctx, tx, res.PaymentID, invoiceID, 4500); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("Allocate: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	// M4: PAYMENT_RECEIVED is recorded via the explicit post-commit hook (never mid-tx).
	svc.RecordPaymentReceived(cctx, res)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_log WHERE entity_type = 'payment' AND entity_id = $1`, res.PaymentID)
	})

	// The allocation row carries WHO (0043 allocated_by) and WHEN (created_at).
	var allocatedBy string
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(allocated_by::text, '') FROM payment_allocations WHERE payment_id = $1::uuid AND invoice_id = $2::uuid`,
		res.PaymentID, invoiceID).Scan(&allocatedBy); err != nil {
		t.Fatalf("read allocation: %v", err)
	}
	if allocatedBy != userID {
		t.Fatalf("allocated_by = %q, want %q", allocatedBy, userID)
	}

	// Explicit business-semantic audit rows exist for the payment (received + allocated).
	for _, action := range []string{audit.ActPaymentReceived, audit.ActPaymentAllocated} {
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM audit_log WHERE action = $1 AND entity_type = 'payment' AND entity_id = $2 AND actor_id = $3::uuid`,
			action, res.PaymentID, userID).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", action, err)
		}
		if n != 1 {
			t.Fatalf("audit rows for %s = %d, want 1", action, n)
		}
	}

	// PaymentHistory: who took it, when, how, and the application history with names.
	h, err := svc.PaymentHistory(cctx, res.PaymentID)
	if err != nil {
		t.Fatalf("PaymentHistory: %v", err)
	}
	if h.ReceivedBy != userID || h.ReceivedByName == "" {
		t.Fatalf("payment header attribution missing: received_by=%q name=%q", h.ReceivedBy, h.ReceivedByName)
	}
	if h.PaymentNo == "" || h.Method != MethodCash || h.AmountCents != 4500 {
		t.Fatalf("unexpected payment header: %+v", h)
	}
	if len(h.Allocations) != 1 {
		t.Fatalf("allocations = %d, want 1", len(h.Allocations))
	}
	al := h.Allocations[0]
	if al.InvoiceID != invoiceID || al.AmountCents != 4500 || al.AllocatedBy != userID || al.AllocatedByName == "" || al.AllocatedAt.IsZero() {
		t.Fatalf("allocation attribution missing: %+v", al)
	}
	if al.InvoiceNo == "" {
		t.Fatalf("allocation must carry the invoice_no: %+v", al)
	}

	// InvoicePayments: the invoice-side view names the same payment, taker, and matcher.
	ip, err := svc.InvoicePayments(cctx, invoiceID)
	if err != nil {
		t.Fatalf("InvoicePayments: %v", err)
	}
	if ip.InvoiceNo == "" || len(ip.Items) != 1 {
		t.Fatalf("unexpected invoice payments: %+v", ip)
	}
	it := ip.Items[0]
	if it.PaymentID != res.PaymentID || it.AmountCents != 4500 ||
		it.AllocatedBy != userID || it.AllocatedByName == "" ||
		it.ReceivedBy != userID || it.ReceivedByName == "" || it.AllocatedAt.IsZero() {
		t.Fatalf("invoice payment attribution missing: %+v", it)
	}
}

// M5, a BOUNCED payment keeps its application history: allocate → NSF-bounce (0450 keeps the live
// payment_allocations and writes the undo beside it as a reversal row) → PaymentHistory reads both
// rows, with the header marked reversed.
func TestIntegration_Q044_BouncedPaymentHistory_ShowsReversedAllocations(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)

	auditSvc := audit.NewService(audit.NewRepository(pool), nil)
	svc := realSvc(pool).WithAudit(auditSvc).WithAuditTrail(auditSvc)
	cctx := globalCtx(userID)

	sid, err := svc.OpenTill(cctx, OpenTillInput{BranchID: branchID, Station: "ITEST-Q044-NSF", FloatCents: 0})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { cleanupSession(pool, sid) })

	customerID, invoiceID := seedCustomerInvoice(t, pool, branchID, 7700)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	res, err := svc.TakeTender(cctx, tx, TenderInput{
		TillSessionID: sid, Method: MethodCheque, AmountCents: 7700,
		CustomerID: customerID, IdempotencyKey: "q044-nsf-" + sid,
	})
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("TakeTender: %v", err)
	}
	if err := svc.Allocate(cctx, tx, res.PaymentID, invoiceID, 7700); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("Allocate: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	svc.RecordPaymentReceived(cctx, res)
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM audit_log WHERE entity_type = 'payment' AND entity_id = $1`, res.PaymentID)
		_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE payload->>'payment_id' = $1`, res.PaymentID)
	})

	// The cheque bounces: the NSF reversal keeps the allocation and writes the undo beside it.
	if _, err := svc.MarkNSF(cctx, NSFInput{PaymentID: res.PaymentID, Reason: "cheque returned NSF"}); err != nil {
		t.Fatalf("MarkNSF: %v", err)
	}
	var nAlloc int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM payment_allocations WHERE payment_id = $1::uuid`, res.PaymentID).Scan(&nAlloc)
	if nAlloc != 2 {
		t.Fatalf("NSF must keep the allocation and write the reversal beside it, %d rows stand", nAlloc)
	}

	// The forensic read still answers "what did this cheque used to pay?", from the live rows now.
	h, err := svc.PaymentHistory(cctx, res.PaymentID)
	if err != nil {
		t.Fatalf("PaymentHistory: %v", err)
	}
	if h.Status != "BOUNCED" || !h.Reversed {
		t.Fatalf("bounced payment must be marked reversed: status=%s reversed=%v", h.Status, h.Reversed)
	}
	if len(h.Allocations) != 2 {
		t.Fatalf("want the application and the reversal beside it, got %d: %+v", len(h.Allocations), h.Allocations)
	}
	al := h.Allocations[0]
	if al.ReversedByNSF {
		t.Fatalf("the live application row must not be flagged reversed_by_nsf: that flag marks a trail reconstruction: %+v", al)
	}
	if al.InvoiceID != invoiceID || al.AmountCents != 7700 {
		t.Fatalf("the application row facts are wrong: %+v", al)
	}
	if al.AllocatedBy != userID {
		t.Fatalf("the application row must carry the audit actor: %+v", al)
	}
	rev := h.Allocations[1]
	if rev.InvoiceID != invoiceID || rev.AmountCents != -7700 {
		t.Fatalf("the reversal row facts are wrong: %+v", rev)
	}
}
