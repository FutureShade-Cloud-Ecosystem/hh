//go:build integration

package billing

// deposit_cancel_apply_race_integration_test.go, the settlement beside the
// REAL received payment apply route (the 304 shape, payments.Repository.
// ApplyReceived), both orders, and the walk order the settlement owes that
// route. The racing payment leg in deposit_cancel_integration_test.go
// simulates the payment with hand written SQL; these legs drive the route
// itself, so the lock the settlement takes is judged against the statement
// the money actually runs, not a stand in for it.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
)

// THE CANCEL HOLDS THE ROW, THE APPLY IS REFUSED: the settlement runs on a
// transaction the test holds open (the order cancel's own shape, the caller's
// transaction), so the deposit row stays locked with the settlement's writes
// uncommitted; the real apply for 2500 parks on the row, the settlement
// commits, and the apply re judges the document under its lock and refuses
// the now VOID document in the route's own words, having written nothing.
func TestIntegration_CancelDeposits_CancelFirstThenApplyIsRefused(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	cust, orderID, dep := seedDepositOrderAt(t, pool, tag, 4_000)
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM payment_allocations WHERE invoice_id::uuid = $1`, dep)
		_, _ = pool.Exec(c, `DELETE FROM payments WHERE customer_id::uuid = $1`, cust)
	})

	held, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Rollback(ctx) }()
	if _, err := NewInvoiceWriter(pool).CancelOrderDeposits(ctx, held, orderID, "CREDIT_BACK", "the settlement holds the row"); err != nil {
		t.Fatalf("settle on the held transaction: %v", err)
	}

	type applyResult struct {
		err error
	}
	done := make(chan applyResult, 1)
	go func() {
		_, err := payments.NewRepository(pool).ApplyReceived(ctx, payments.ApplyRequest{
			CustomerID: cust, Method: "CHEQUE", Reference: "DCX-" + tag,
			IdempotencyKey: "dcx-cancel-first-" + tag, InvoiceIDs: []string{dep}, AmountCents: 2_500,
		})
		done <- applyResult{err}
	}()

	deadline := time.Now().Add(15 * time.Second)
	for {
		var waiting int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM pg_stat_activity
			 WHERE datname = current_database() AND wait_event_type = 'Lock' AND pid <> pg_backend_pid()`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		select {
		case r := <-done:
			t.Fatalf("the apply answered (%v) without parking on the settlement's row lock", r.err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the apply never reached a lock wait")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := held.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	r := <-done
	if r.err == nil || !strings.Contains(r.err.Error(), "is not open") {
		t.Fatalf("the apply against the settled document = %v, want the not open refusal", r.err)
	}

	// The refusal wrote nothing: no payment row, no allocation, no credit,
	// and the document stands as the settlement left it.
	var pays int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payments WHERE customer_id = $1::uuid`, cust).Scan(&pays); err != nil {
		t.Fatal(err)
	}
	if pays != 0 {
		t.Fatalf("the refused apply wrote %d payment rows, want none", pays)
	}
	var credits int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM customer_transactions WHERE customer_id = $1::uuid AND source_type = 'deposit_cancel'`, cust).
		Scan(&credits); err != nil {
		t.Fatal(err)
	}
	if credits != 0 {
		t.Fatalf("the unpaid deposit's settlement wrote %d credit rows, want none", credits)
	}
	var status string
	var balance int64
	if err := pool.QueryRow(ctx,
		`SELECT status, balance_cents FROM invoices WHERE id = $1::uuid`, dep).Scan(&status, &balance); err != nil {
		t.Fatal(err)
	}
	if status != "VOID" || balance != 0 {
		t.Fatalf("the deposit = %s/%d, want VOID/0", status, balance)
	}
}

// THE APPLY HOLDS THE ROW, THE CANCEL CREDITS THE PAYMENT: the real apply
// parks inside its own transaction right after its allocation insert (a test
// trigger waiting on an advisory lock the leg holds), the settlement starts
// and parks on the deposit row, then the lock is dropped: the apply commits,
// the settlement re reads the moved balance and credits the paid 2500,
// closing the other 1500.
func TestIntegration_CancelDeposits_ApplyFirstThenCancelCreditsThePayment(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	cust, orderID, dep := seedDepositOrderAt(t, pool, tag, 4_000)
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM payment_allocations WHERE invoice_id::uuid = $1`, dep)
		_, _ = pool.Exec(c, `DELETE FROM payments WHERE customer_id::uuid = $1`, cust)
	})

	const parkKey = 872_721_451
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE OR REPLACE FUNCTION deposit_cancel_apply_race_park() RETURNS trigger AS $fn$
		BEGIN PERFORM pg_advisory_lock(%d); RETURN NEW; END $fn$ LANGUAGE plpgsql`, parkKey)); err != nil {
		t.Fatalf("create the park function: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE TRIGGER deposit_cancel_apply_race_park AFTER INSERT ON payment_allocations
		FOR EACH ROW EXECUTE FUNCTION deposit_cancel_apply_race_park()`); err != nil {
		t.Fatalf("create the park trigger: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DROP TRIGGER IF EXISTS deposit_cancel_apply_race_park ON payment_allocations`)
		_, _ = pool.Exec(c, `DROP FUNCTION IF EXISTS deposit_cancel_apply_race_park()`)
	})

	// The session that holds the advisory lock the trigger waits on.
	park, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = park.Rollback(ctx) }()
	if _, err := park.Exec(ctx, `SELECT pg_advisory_lock($1)`, parkKey); err != nil {
		t.Fatalf("take the park lock: %v", err)
	}

	type applyResult struct {
		err error
	}
	applied := make(chan applyResult, 1)
	go func() {
		_, err := payments.NewRepository(pool).ApplyReceived(ctx, payments.ApplyRequest{
			CustomerID: cust, Method: "CHEQUE", Reference: "DCX-" + tag,
			IdempotencyKey: "dcx-apply-first-" + tag, InvoiceIDs: []string{dep}, AmountCents: 2_500,
		})
		applied <- applyResult{err}
	}()

	// Wait until the apply is parked inside the trigger, then start the
	// settlement: it parks on the deposit row the apply's transaction holds.
	deadline := time.Now().Add(15 * time.Second)
	for {
		var waiting int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM pg_stat_activity
			 WHERE datname = current_database() AND wait_event_type = 'Lock' AND pid <> pg_backend_pid()`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		select {
		case r := <-applied:
			t.Fatalf("the apply answered (%v) without parking in its trigger", r.err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the apply never reached its park")
		}
		time.Sleep(20 * time.Millisecond)
	}
	settled := make(chan error, 1)
	go func() {
		_, err := NewInvoiceWriter(pool).CancelOrderDeposits(ctx, nil, orderID, "CREDIT_BACK", "racing the apply")
		settled <- err
	}()
	for {
		var waiting int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM pg_stat_activity
			 WHERE datname = current_database() AND wait_event_type = 'Lock' AND pid <> pg_backend_pid()`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 1 {
			break
		}
		select {
		case err := <-settled:
			t.Fatalf("the settlement finished (%v) without parking on the apply's row lock", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the settlement never reached a lock wait")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Drop the park: the apply commits, the settlement reads its payment.
	if _, err := park.Exec(ctx, `SELECT pg_advisory_unlock($1)`, parkKey); err != nil {
		t.Fatalf("drop the park lock: %v", err)
	}
	if err := park.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if r := <-applied; r.err != nil {
		t.Fatalf("the apply: %v", r.err)
	}
	if err := <-settled; err != nil {
		t.Fatalf("settle: %v", err)
	}

	var credit int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_cents),0) FROM customer_transactions
		 WHERE customer_id = $1::uuid AND source_type = 'deposit_cancel'`, cust).Scan(&credit); err != nil {
		t.Fatal(err)
	}
	if credit != -2_500 {
		t.Fatalf("the account credit = %d, want the raced payment's -2500", credit)
	}
	var pays int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM payments p JOIN payment_allocations a ON a.payment_id = p.id
		  WHERE p.customer_id = $1::uuid AND a.invoice_id = $2::uuid AND a.amount_cents = 2500`, cust, dep).Scan(&pays); err != nil {
		t.Fatal(err)
	}
	if pays != 1 {
		t.Fatalf("the raced payment's allocation = %d rows, want the one", pays)
	}
	if _, paid, closed := depositCancelEvent(t, pool, dep); paid != 2_500 || closed != 1_500 {
		t.Fatalf("the event paid %d closed %d, want 2500 and 1500", paid, closed)
	}
}

// THE WALK ORDER: with two live deposit documents on one order whose created
// order differs from their id order, the settlement must walk them in ID
// order, the order the apply's multi invoice walk locks the same rows in, or
// a cancel and an apply naming both can meet head on. The feed seq each
// clearing draws makes the walk observable: the document settled first carries
// the smaller seq.
func TestIntegration_CancelDeposits_TheWalkTakesTheDocumentsInIdOrder(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	cust := seedCustomer(t, pool, tag)
	var orderID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment)
		 VALUES ('ORD-DCW-'||$1, $2::uuid, $3::uuid, 'CONFIRMED', 'DELIVERY') RETURNING id::text`,
		tag, cust, trentonBranch).Scan(&orderID); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	// createdFirst carries the HIGHER id; createdLast the LOWER, so the two
	// orders disagree and the leg can tell them apart.
	const createdFirst = "00000000-0000-4000-8000-0000000000bb"
	const createdLast = "00000000-0000-4000-8000-0000000000aa"
	for _, d := range []struct {
		id       string
		older    bool
	}{
		{createdFirst, true},
		{createdLast, false},
	} {
		age := "now()"
		if d.older {
			age = "now() - interval '1 hour'"
		}
		if _, err := pool.Exec(ctx, fmt.Sprintf(`
			INSERT INTO invoices (id, invoice_no, customer_id, order_id, branch_id, status, due_date,
			                      subtotal_cents, tax_cents, total_cents, balance_cents,
			                      source_kind, finalized_at, created_at)
			VALUES ($1::uuid, 'INV-DCW-'||$2||$3, $4::uuid, $5::uuid, $6::uuid, 'FINALIZED', CURRENT_DATE,
			        4000, 0, 4000, 4000, 'SUPPLIER_DEPOSIT', now(), %s)`, age),
			d.id, tag, d.id[len(d.id)-2:], cust, orderID, trentonBranch); err != nil {
			t.Fatalf("seed deposit document %s: %v", d.id, err)
		}
	}

	n, err := NewInvoiceWriter(pool).CancelOrderDeposits(ctx, nil, orderID, "CREDIT_BACK", "order cancelled")
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if n != 2 {
		t.Fatalf("settled %d documents, want the two", n)
	}
	var seqFirstByCreated, seqLastByCreated int64
	if err := pool.QueryRow(ctx,
		`SELECT seq FROM invoices WHERE id = $1::uuid`, createdFirst).Scan(&seqFirstByCreated); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT seq FROM invoices WHERE id = $1::uuid`, createdLast).Scan(&seqLastByCreated); err != nil {
		t.Fatal(err)
	}
	if seqFirstByCreated < seqLastByCreated {
		t.Fatalf("the walk took the documents in created order (seq %d before %d); the id order the apply locks in wants the lower id first",
			seqFirstByCreated, seqLastByCreated)
	}
}
