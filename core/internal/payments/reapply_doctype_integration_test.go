//go:build integration

package payments

// reapply_doctype_integration_test.go, ROUND 9, M3-P2-5's SECOND DOOR:
// ReapplyHeld's named rows (M3's leg G, the reapply side).
//
// ReapplyHeld locks the named invoice rows in ascending id order with no
// doc_type filter, so a request may name a CREDIT_MEMO beside an invoice: the
// door takes the memo's row first (the lower id) and keeps it held while it
// wants the invoice's row. The credit apply's own order for those two rows is
// the TARGET invoice row first and the memo row second
// (billing/credit_apply.go's THE LOCKS), so the two doors' lock orders CROSS:
// each can hold what the other needs, and the answer is a raw SQLSTATE 40P01
// where a refusal in words belongs.
//
// The rule this leg pins: ReapplyHeld's named rows must be doc_type INVOICE
// documents, and a memo named beside an invoice is refused in words (the same
// no such invoice an unknown id answers) before any row is locked.
//
// The staging is deterministic, M3's leg G's shape with the doors swapped: one
// transaction of our own takes the apply's FIRST lock (the target invoice row,
// exactly the lock billing's door takes first), the real ReapplyHeld holds the
// memo row and parks on the invoice row, and then our transaction asks for the
// row ReapplyHeld is holding. That is a lock cycle, and on the unfixed door
// Postgres answers SQLSTATE 40P01 somewhere in it.

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestIntegration_FW1_12b_ReapplyRefusesACreditMemoNamedBesideAnInvoice pins
// the reapply door. After the fix the door refuses the memo in words while the
// invoice row is still held by our transaction, which is itself the proof it
// never took the memo's row: it answered before the row it would have needed
// was free, and no cycle can close.
func TestIntegration_FW1_12b_ReapplyRefusesACreditMemoNamedBesideAnInvoice(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	n := t.Name() + "-" + time.Now().Format("150405.000000000")

	var branchID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type = 'BRANCH' ORDER BY id LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no seeded BRANCH: %v", err)
	}

	var customerID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ('PAY-'||$1, 'Reapply DocType '||$1, 'CONTRACTOR', $2::uuid, 0) RETURNING id::text`,
		n, branchID).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE payload->>'customer_id' = $1::uuid OR payload->>'reverses_payment_id' IN (SELECT id::text FROM payments WHERE customer_id = $1::uuid)`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM payment_reapplications WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `UPDATE payments SET reverses_payment_id = NULL WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM payments WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, customerID)
	})

	// The memo and the invoice, seeded until the memo's id sorts below the
	// invoice's: the reapply walks its named rows ascending, so the memo must
	// be the row it takes FIRST for the two doors' orders to cross.
	//
	// The memo's holdback is NEGATIVE on purpose. The door's per row ceiling is
	// the balance less the holdback, the database's own doc sign check binds a
	// memo's balance at or below zero, and no check binds the holdback's sign,
	// so a memo with a negative holdback is the one row the database accepts on
	// which the unfixed door gets past the memo's own ceiling and reaches for
	// the invoice's row while still holding the memo's. That is the shape the
	// finding names; no production writer writes a negative holdback, so the
	// reach is the same dormant but one writer away shape the round's report
	// carries.
	var memoID, invoiceID string
	for i := 0; i < 40; i++ {
		var memo, inv string
		if err := pool.QueryRow(ctx, `
			INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
			                      subtotal_cents, tax_cents, total_cents, balance_cents, holdback_cents,
			                      finalized_at, doc_type)
			VALUES ('R9-M-'||$1::text||'-'||$2::int, $3::uuid, $4::uuid, 'FINALIZED', CURRENT_DATE, CURRENT_DATE,
			        -10000, 0, -10000, -10000, -11000, now(), 'CREDIT_MEMO')
			RETURNING id::text`, n, i, customerID, branchID).Scan(&memo); err != nil {
			t.Fatalf("seed the memo: %v", err)
		}
		if err := pool.QueryRow(ctx, `
			INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
			                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
			VALUES ('R9-I-'||$1::text||'-'||$2::int, $3::uuid, $4::uuid, 'FINALIZED', CURRENT_DATE, CURRENT_DATE,
			        10000, 0, 10000, 10000, now(), 'INVOICE')
			RETURNING id::text`, n, i, customerID, branchID).Scan(&inv); err != nil {
			t.Fatalf("seed the invoice: %v", err)
		}
		if memo < inv {
			memoID, invoiceID = memo, inv
			break
		}
		_, _ = pool.Exec(ctx, `DELETE FROM invoices WHERE id = $1::uuid OR id = $2::uuid`, memo, inv)
	}
	if memoID == "" {
		t.Fatal("could not seed a memo whose id sorts below the invoice's in 40 tries")
	}

	// The reversed payment: 10000 received, then reversed, so it holds 10000 in 2310.
	var paymentID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, branch_id)
		 VALUES ('R9-P-'||$1, $2::uuid, 'CHEQUE', 10000, 'RECEIVED', $3::uuid)
		 RETURNING id::text`, n, customerID, branchID).Scan(&paymentID); err != nil {
		t.Fatalf("seed the payment: %v", err)
	}
	if _, err := repo.ReversePayment(ctx, ReverseInput{
		PaymentID: paymentID, Reason: "round 9: the crossing", IsManager: true,
	}, time.Now()); err != nil {
		t.Fatalf("reverse the payment: %v", err)
	}

	// The apply's FIRST lock, taken and held by a transaction of our own: the
	// apply would take the target invoice row exactly so, before it wants the
	// memo's row.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM invoices WHERE id = $1::uuid FOR NO KEY UPDATE`, invoiceID); err != nil {
		t.Fatalf("hold the invoice row: %v", err)
	}

	// The real re-application, naming the memo beside the invoice. The memo
	// carries a POSITIVE balance on purpose: it is the only shape that gets
	// past the door's own per row ceiling on the memo, which is what lets the
	// door reach past the row it holds to the invoice row we hold. Nothing
	// here can move money whichever way the door answers: the cycle is closed
	// by our own transaction, and a deadlocked door writes nothing.
	reapplyErr := make(chan error, 1)
	go func() {
		_, err := repo.ReapplyHeld(ctx, ReapplyInput{
			PaymentID: paymentID, Reason: "round 9: the crossing", IsManager: true,
			IdempotencyKey: n + "-key-1",
			Allocations: []Allocation{
				{InvoiceID: memoID, AmountCents: 100},
				{InvoiceID: invoiceID, AmountCents: 100},
			},
		}, time.Now())
		reapplyErr <- err
	}()

	// Wait until the door either answers (the fixed door refuses at once,
	// before any row beyond the payment's) or parks on a lock (the unfixed
	// door has taken the memo's row and wants the invoice's row we hold), so
	// the leg is deterministic and not a sleep race. The answer, when it has
	// landed, is kept here: a second receive on the channel would wait for an
	// answer that has already been taken.
	answered := false
	var reapplyAnswer error
	deadline := time.Now().Add(15 * time.Second)
	for !answered {
		select {
		case reapplyAnswer = <-reapplyErr:
			answered = true
		default:
		}
		if answered {
			break
		}
		var waiting int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM pg_stat_activity
			 WHERE datname = current_database() AND wait_event_type = 'Lock' AND pid <> pg_backend_pid()`).Scan(&waiting); err != nil {
			t.Fatalf("read the lock waiters: %v", err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the re-application neither answered nor reached a lock wait")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Close the cycle only when the door is parked: ask for the row it holds.
	// On the fixed door this never runs with the door holding anything (it
	// answered instead), and the ask cannot deadlock.
	ourErr := error(nil)
	if !answered {
		if _, ourErr = tx.Exec(ctx, `SELECT 1 FROM invoices WHERE id = $1::uuid FOR NO KEY UPDATE`, memoID); ourErr != nil {
			t.Logf("our transaction asking for the held memo row: err=%v", ourErr)
		}
	}

	if !answered {
		select {
		case reapplyAnswer = <-reapplyErr:
		case <-time.After(30 * time.Second):
			t.Fatal("the re-application never answered")
		}
	}

	if ourErr != nil {
		t.Fatalf("the two doors' lock orders still cross: asking for the row the reapply holds answered %v, want no cycle at all", ourErr)
	}
	var noSuch NoSuchInvoiceError
	if !errors.As(reapplyAnswer, &noSuch) {
		t.Fatalf("the re-application answered %v, want the no such invoice refusal in words for a memo named beside an invoice", reapplyAnswer)
	}
}
