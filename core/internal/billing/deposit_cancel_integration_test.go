//go:build integration

package billing

// deposit_cancel_integration_test.go, the cancelled special order's supplier
// deposit against the real schema: the settlement a manual cancel carries
// through billing. Credit back clears the open deposit document (or leaves a
// credit on the account when it was paid); kept settles the document's
// keeps only what was paid (the unpaid rest is closed) and touches no account
// row.

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
)

// seedDepositOrderAt plants a CONFIRMED order with one live deposit document
// whose balance the caller sets (0 plants the paid shape the settle path
// leaves: PAID with a zero balance).
func seedDepositOrderAt(t *testing.T, pool *pgxpool.Pool, tag string, balance int64) (cust, orderID, dep string) {
	t.Helper()
	ctx := context.Background()
	cust = seedCustomer(t, pool, tag)
	if err := pool.QueryRow(ctx,
		`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment)
		 VALUES ('ORD-DC-'||$1, $2::uuid, $3::uuid, 'CONFIRMED', 'DELIVERY') RETURNING id::text`,
		tag, cust, trentonBranch).Scan(&orderID); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	dep = seedSupplierDepositInvoice(t, pool, cust, "#INV-"+tag+"-DEP", 4_000)
	if balance == 0 {
		if _, err := pool.Exec(ctx,
			`UPDATE invoices SET order_id = $1::uuid, balance_cents = 0, status = 'PAID' WHERE id = $2::uuid`,
			orderID, dep); err != nil {
			t.Fatalf("set the paid deposit shape: %v", err)
		}
	} else if _, err := pool.Exec(ctx, `UPDATE invoices SET order_id = $1::uuid, balance_cents = $2 WHERE id = $3::uuid`,
		orderID, balance, dep); err != nil {
		t.Fatalf("set the deposit balance: %v", err)
	}
	// The paid part carries the dated act it owes, the receipt the desk would
	// have taken for it: the as of rebuild (the aging and the 1100 tie figure)
	// reconstructs a document's balance from the acts that moved it, so a hand
	// set balance with no act behind it reads as still owed. The same reason
	// the seed plants the receipt that settled the paid demo invoice.
	if paid := 4_000 - balance; paid > 0 {
		var payID string
		if err := pool.QueryRow(ctx,
			`INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, branch_id)
			 VALUES (nextval('payment_no_seq')::text, $1::uuid, 'CHEQUE', $2, 'RECEIVED', $3::uuid)
			 RETURNING id::text`, cust, paid, trentonBranch).Scan(&payID); err != nil {
			t.Fatalf("seed the paid part's receipt: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO payment_allocations (payment_id, invoice_id, amount_cents, created_at)
			 VALUES ($1::uuid, $2::uuid, $3, now())`, payID, dep, paid); err != nil {
			t.Fatalf("seed the paid part's allocation: %v", err)
		}
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM payment_allocations WHERE invoice_id::text = $1`, dep)
		_, _ = pool.Exec(c, `DELETE FROM payments WHERE customer_id::text = $1`, cust)
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE source_ref_id::text = $1`, dep)
		_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE (payload->>'invoice_id')::uuid = $1::uuid`, dep)
		_, _ = pool.Exec(c, `DELETE FROM audit_log WHERE entity_id::text = $1`, dep)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE id::uuid = $1::uuid`, dep)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE id::uuid = $1::uuid`, orderID)
	})
	return cust, orderID, dep
}

// The credit back on an UNPAID deposit: the document's balance clears and the
// document leaves the live set (neither the release paths nor the register
// read it as money attached any more), and no credit lands on the account
// (nothing was paid, the credit exactly clears the open document).
func TestIntegration_CancelDeposits_CreditBackUnpaidClearsTheDocument(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	cust, orderID, dep := seedDepositOrderAt(t, pool, tag, 4_000)

	n, err := NewInvoiceWriter(pool).CancelOrderDeposits(ctx, nil, orderID, "CREDIT_BACK", "customer walked away")
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if n != 1 {
		t.Fatalf("settled %d documents, want the one", n)
	}
	var status string
	var balance int64
	if err := pool.QueryRow(ctx,
		`SELECT status, balance_cents FROM invoices WHERE id = $1::uuid`, dep).Scan(&status, &balance); err != nil {
		t.Fatal(err)
	}
	if status != "VOID" || balance != 0 {
		t.Fatalf("the credited deposit document = %s/%d, want VOID/0", status, balance)
	}
	var credits int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM customer_transactions WHERE customer_id = $1::uuid AND source_type = 'deposit_cancel'`, cust).
		Scan(&credits); err != nil {
		t.Fatal(err)
	}
	if credits != 0 {
		t.Fatalf("an unpaid deposit credited the account %d times, want none (the credit clears the document)", credits)
	}
}

// The credit back on a PAID deposit: the document already sits at PAID with a
// zero balance, and the paid amount lands on the account as a credit (an
// ADJUSTMENT row off the settlement, never a credit memo document).
func TestIntegration_CancelDeposits_CreditBackPaidLeavesAnAccountCredit(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	cust, orderID, dep := seedDepositOrderAt(t, pool, tag, 0)

	n, err := NewInvoiceWriter(pool).CancelOrderDeposits(ctx, nil, orderID, "CREDIT_BACK", "order cancelled")
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if n != 1 {
		t.Fatalf("settled %d documents, want the one", n)
	}
	var status string
	var balance int64
	if err := pool.QueryRow(ctx,
		`SELECT status, balance_cents FROM invoices WHERE id = $1::uuid`, dep).Scan(&status, &balance); err != nil {
		t.Fatal(err)
	}
	if status != "PAID" || balance != 0 {
		t.Fatalf("a paid deposit document must keep its paid shape, got %s/%d", status, balance)
	}
	var amt int64
	if err := pool.QueryRow(ctx, `
		SELECT amount_cents FROM customer_transactions
		 WHERE customer_id = $1::uuid AND source_type = 'deposit_cancel' AND txn_type = 'ADJUSTMENT'`, cust).
		Scan(&amt); err != nil {
		t.Fatalf("the paid deposit's credit back wrote no account credit: %v", err)
	}
	if amt != -4_000 {
		t.Fatalf("the account credit = %d, want the deposit's -4000", amt)
	}
	var memos int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM invoices WHERE customer_id = $1::uuid AND doc_type = 'CREDIT_MEMO'`, cust).Scan(&memos); err != nil {
		t.Fatal(err)
	}
	if memos != 0 {
		t.Fatalf("the deposit cancel minted %d credit memo documents, the credit memo path is forbidden", memos)
	}
}

// The partial case: the paid part becomes the account credit, the open part
// clears, in one settlement.
func TestIntegration_CancelDeposits_CreditBackPartial(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	cust, orderID, dep := seedDepositOrderAt(t, pool, tag, 1_500)

	if _, err := NewInvoiceWriter(pool).CancelOrderDeposits(ctx, nil, orderID, "CREDIT_BACK", "order cancelled"); err != nil {
		t.Fatalf("settle: %v", err)
	}
	var status string
	var balance int64
	if err := pool.QueryRow(ctx,
		`SELECT status, balance_cents FROM invoices WHERE id = $1::uuid`, dep).Scan(&status, &balance); err != nil {
		t.Fatal(err)
	}
	if status != "VOID" || balance != 0 {
		t.Fatalf("the partly paid deposit = %s/%d, want VOID/0", status, balance)
	}
	var amt int64
	if err := pool.QueryRow(ctx, `
		SELECT amount_cents FROM customer_transactions
		 WHERE customer_id = $1::uuid AND source_type = 'deposit_cancel'`, cust).Scan(&amt); err != nil {
		t.Fatalf("the paid part wrote no account credit: %v", err)
	}
	if amt != -2_500 {
		t.Fatalf("the account credit = %d, want the paid part's -2500", amt)
	}
}

// depositCancelEvent reads back the one deposit.cancelled event a settlement
// emitted for the document: the amounts the booking will post.
func depositCancelEvent(t *testing.T, pool *pgxpool.Pool, dep string) (disposition string, paid, closed int64) {
	t.Helper()
	if err := pool.QueryRow(context.Background(), `
		SELECT payload->>'disposition', COALESCE((payload->>'paid_cents')::bigint, -1), COALESCE((payload->>'closed_cents')::bigint, -1)
		  FROM events_outbox
		 WHERE topic = 'deposit.cancelled' AND payload->>'invoice_id' = $1`, dep).Scan(&disposition, &paid, &closed); err != nil {
		t.Fatalf("the settlement's event: %v", err)
	}
	return disposition, paid, closed
}

// Kept on a PART PAID deposit (4000 total, 2500 paid, 1500 owed), the keep
// ruling (row 1228, Colton's A): only what the customer PAID is the dealer's.
// The unpaid rest is closed so nothing more is owed on the cancelled order, the
// document reads PAID with a zero balance (what was paid is all there is), no
// account row lands, and the event carries both amounts for the booking.
func TestIntegration_CancelDeposits_KeptPartPaidKeepsThePaidAndClosesTheRest(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	cust, orderID, dep := seedDepositOrderAt(t, pool, tag, 1_500)

	if _, err := NewInvoiceWriter(pool).WithAudit(audit.NewService(audit.NewRepository(pool), nil)).CancelOrderDeposits(ctx, nil, orderID, "KEPT", "the supplier already ordered it"); err != nil {
		t.Fatalf("settle: %v", err)
	}
	var status string
	var balance int64
	if err := pool.QueryRow(ctx,
		`SELECT status, balance_cents FROM invoices WHERE id = $1::uuid`, dep).Scan(&status, &balance); err != nil {
		t.Fatal(err)
	}
	if status != "PAID" || balance != 0 {
		t.Fatalf("a kept part paid deposit must close with nothing owed, got %s/%d", status, balance)
	}
	var rows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM customer_transactions WHERE customer_id = $1::uuid AND source_type = 'deposit_cancel'`, cust).
		Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("a kept deposit wrote %d account rows, want none", rows)
	}
	if d, paid, closed := depositCancelEvent(t, pool, dep); d != "KEPT" || paid != 2_500 || closed != 1_500 {
		t.Fatalf("the event = %s paid %d closed %d, want KEPT 2500 1500", d, paid, closed)
	}
	var keptAmt, closedAmt int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE((changes->>'paid_kept_cents')::bigint, -1), COALESCE((changes->>'closed_cents')::bigint, -1)
		  FROM audit_log WHERE action = 'DEPOSIT_CANCELLED' AND entity_id::text = $1`, dep).Scan(&keptAmt, &closedAmt); err != nil {
		t.Fatalf("the audit row: %v", err)
	}
	if keptAmt != 2_500 || closedAmt != 1_500 {
		t.Fatalf("the audit row records kept %d closed %d, want 2500 and 1500", keptAmt, closedAmt)
	}
}

// Kept on an entirely UNPAID deposit: nothing was paid, so nothing is the
// dealer's; the whole document is closed (VOID, zero balance) and the event
// carries paid 0 and closed 4000, so nothing books to other income.
func TestIntegration_CancelDeposits_KeptUnpaidClosesTheWholeDocument(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	cust, orderID, dep := seedDepositOrderAt(t, pool, tag, 4_000)

	if _, err := NewInvoiceWriter(pool).CancelOrderDeposits(ctx, nil, orderID, "KEPT", "the supplier already ordered it"); err != nil {
		t.Fatalf("settle: %v", err)
	}
	var status string
	var balance int64
	if err := pool.QueryRow(ctx,
		`SELECT status, balance_cents FROM invoices WHERE id = $1::uuid`, dep).Scan(&status, &balance); err != nil {
		t.Fatal(err)
	}
	if status != "VOID" || balance != 0 {
		t.Fatalf("a kept unpaid deposit must be closed, got %s/%d", status, balance)
	}
	var rows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM customer_transactions WHERE customer_id = $1::uuid AND source_type = 'deposit_cancel'`, cust).
		Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("a kept deposit wrote %d account rows, want none", rows)
	}
	if d, paid, closed := depositCancelEvent(t, pool, dep); d != "KEPT" || paid != 0 || closed != 4_000 {
		t.Fatalf("the event = %s paid %d closed %d, want KEPT 0 4000", d, paid, closed)
	}
}

// Kept on a fully PAID deposit: the whole deposit is the dealer's, the
// document stands PAID, nothing is closed.
func TestIntegration_CancelDeposits_KeptPaidKeepsTheWholeDeposit(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	_, orderID, dep := seedDepositOrderAt(t, pool, tag, 0)

	if _, err := NewInvoiceWriter(pool).CancelOrderDeposits(ctx, nil, orderID, "KEPT", "the supplier already ordered it"); err != nil {
		t.Fatalf("settle: %v", err)
	}
	var status string
	var balance int64
	if err := pool.QueryRow(ctx,
		`SELECT status, balance_cents FROM invoices WHERE id = $1::uuid`, dep).Scan(&status, &balance); err != nil {
		t.Fatal(err)
	}
	if status != "PAID" || balance != 0 {
		t.Fatalf("a kept paid deposit must stand PAID, got %s/%d", status, balance)
	}
	if d, paid, closed := depositCancelEvent(t, pool, dep); d != "KEPT" || paid != 4_000 || closed != 0 {
		t.Fatalf("the event = %s paid %d closed %d, want KEPT 4000 0", d, paid, closed)
	}
}

// An unknown disposition is refused loudly and writes nothing.
func TestIntegration_CancelDeposits_UnknownDispositionRefused(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	_, orderID, dep := seedDepositOrderAt(t, pool, tag, 4_000)

	if _, err := NewInvoiceWriter(pool).CancelOrderDeposits(ctx, nil, orderID, "DONATED", "nope"); err == nil {
		t.Fatal("an unknown disposition must be refused")
	}
	var balance int64
	if err := pool.QueryRow(ctx, `SELECT balance_cents FROM invoices WHERE id = $1::uuid`, dep).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if balance != 4_000 {
		t.Fatalf("the refused settlement moved the document to %d", balance)
	}
}

// A second settlement over the same order writes nothing: the documents are
// no longer live, so the settlement is a no-op.
func TestIntegration_CancelDeposits_SecondSettlementWritesNothing(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	cust, orderID, dep := seedDepositOrderAt(t, pool, tag, 4_000)

	if _, err := NewInvoiceWriter(pool).CancelOrderDeposits(ctx, nil, orderID, "CREDIT_BACK", "once"); err != nil {
		t.Fatalf("settle: %v", err)
	}
	n, err := NewInvoiceWriter(pool).CancelOrderDeposits(ctx, nil, orderID, "CREDIT_BACK", "again")
	if err != nil {
		t.Fatalf("the second settlement must be a clean no-op: %v", err)
	}
	if n != 0 {
		t.Fatalf("the second settlement touched %d documents", n)
	}
	var credits int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM customer_transactions WHERE customer_id = $1::uuid AND source_type = 'deposit_cancel'`, cust).
		Scan(&credits); err != nil {
		t.Fatal(err)
	}
	if credits != 0 {
		t.Fatalf("the unpaid document's second settlement wrote %d account rows", credits)
	}
	_ = dep
}

// The clearing update stamps the invoices feed seq: the invoices sync scope
// (syncscope_invoices.go) is how the counter and the payer's app see an
// invoice change, and it reads i.seq, so a balance and status move that
// leaves the seq standing is a money move no pull can see, the settle's own
// words. Both shapes that touch the document must move it: the VOID the
// credit back (or an unpaid keep) leaves, and the PAID a kept part paid
// document reads; a fully paid document is untouched and its seq stands.
func TestIntegration_CancelDeposits_ClearingStampsTheInvoicesFeedSeq(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name         string
		balance      int64
		disposition  string
		wantStatus   string
		wantSeqMoves bool
	}{
		{"credit back voids the document", 4_000, "CREDIT_BACK", "VOID", true},
		{"kept part paid reads PAID", 1_500, "KEPT", "PAID", true},
		{"a fully paid document is untouched", 0, "CREDIT_BACK", "PAID", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tag := ns(t)
			_, orderID, dep := seedDepositOrderAt(t, pool, tag, tc.balance)
			if _, err := pool.Exec(ctx, `UPDATE invoices SET seq = 7 WHERE id = $1::uuid`, dep); err != nil {
				t.Fatalf("plant the feed seq: %v", err)
			}
			if _, err := NewInvoiceWriter(pool).CancelOrderDeposits(ctx, nil, orderID, tc.disposition, "order cancelled"); err != nil {
				t.Fatalf("settle: %v", err)
			}
			var status string
			var balance, seq int64
			if err := pool.QueryRow(ctx,
				`SELECT status, balance_cents, seq FROM invoices WHERE id = $1::uuid`, dep).Scan(&status, &balance, &seq); err != nil {
				t.Fatal(err)
			}
			if status != tc.wantStatus || balance != 0 {
				t.Fatalf("the deposit document = %s/%d, want %s/0", status, balance, tc.wantStatus)
			}
			if tc.wantSeqMoves && seq <= 7 {
				t.Fatalf("the cancel left the invoices feed seq at %d (was 7): the till and the payer's app can never see the deposit leave", seq)
			}
			if !tc.wantSeqMoves && seq != 7 {
				t.Fatalf("an untouched document's feed seq moved to %d, want it standing at 7", seq)
			}
		})
	}
}

// The settlement lock (PR272-GLM-REVIEW P2-1): a counter payment allocating
// against the deposit invoice while the manager's cancel settles it. The
// payment's transaction takes the invoice row FOR NO KEY UPDATE, the lock the
// payments path takes (lockAndTotal), and moves the balance by 2500 of 4000;
// the cancel starts while that transaction is open and is seen waiting on the
// row; then the payment commits. The settlement's re-read must wait for the
// payment and see its balance, so the 2500 lands as the account credit. Read
// without the lock, the cancel computed the credit from the balance it saw
// before the payment (4000, nothing paid), the overwrite voided the document,
// and the 2500 vanished: the account held the payment row and no credit, so
// the AR tie out broke by the payment's amount.
func TestIntegration_CancelDeposits_RacingPaymentIsNotSwallowed(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	cust, orderID, dep := seedDepositOrderAt(t, pool, tag, 4_000)

	pay, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pay.Rollback(ctx) }()
	var bal int64
	if err := pay.QueryRow(ctx,
		`SELECT balance_cents FROM invoices WHERE id = $1::uuid AND customer_id = $2::uuid FOR NO KEY UPDATE`,
		dep, cust).Scan(&bal); err != nil || bal != 4_000 {
		t.Fatalf("payment lock read = %d, %v", bal, err)
	}
	if _, err := pay.Exec(ctx,
		`UPDATE invoices SET balance_cents = balance_cents - 2500, status = 'PARTIALLY_PAID', updated_at = now() WHERE id = $1::uuid`,
		dep); err != nil {
		t.Fatal(err)
	}
	if _, err := pay.Exec(ctx, `
		INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id)
		VALUES ($1::uuid, 'PAYMENT', -2500, 1500, 'payment', $2::uuid, $3::uuid)`,
		cust, dep, trentonBranch); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := NewInvoiceWriter(pool).CancelOrderDeposits(ctx, nil, orderID, "CREDIT_BACK", "racing a payment")
		done <- err
	}()

	// Wait until the cancel is parked on a lock (the settlement's read with
	// the fix, its overwrite without it) before the payment commits.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM pg_stat_activity
			 WHERE datname = current_database() AND wait_event_type = 'Lock' AND pid <> pg_backend_pid()
			   AND query LIKE '%invoices%'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("the cancel finished (%v) without waiting on the payment's row lock", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the cancel never reached a lock wait")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := pay.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("settle: %v", err)
	}

	var credit int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_cents),0) FROM customer_transactions
		 WHERE customer_id = $1::uuid AND source_type = 'deposit_cancel'`, cust).Scan(&credit); err != nil {
		t.Fatal(err)
	}
	if credit != -2_500 {
		t.Fatalf("the account credit = %d, want the racing payment's -2500 (the payment was swallowed)", credit)
	}
	if _, paid, closed := depositCancelEvent(t, pool, dep); paid != 2_500 || closed != 1_500 {
		t.Fatalf("the event paid %d closed %d, want 2500 and 1500", paid, closed)
	}
}
