//go:build integration

package order

// pos_return_lock_order_integration_test.go, fix round 3 (the Opus read's P2-1): the POS return's
// own lock order against the two stock first writers. The return's account credit rail draws the
// invoices counter (ApplyCreditToInvoice) and its restock writes the returned goods' stock_balances
// row AFTER it, while the POS ring takes that stock row FIRST and draws the same counter later, and
// the order cancel's settlement now takes its stock rows before its counter too (round 2). So the
// return could meet either head on: the return holds the counter and waits on the stock row, the
// other side holds the stock row and waits on the counter, and Postgres aborts a side whole with a
// 40P01 (against the cancel, the route answers its 500). The return now takes the stock rows its
// restock will touch through inventory's own seam (LockStockTx, sorted) BEFORE its first counter
// draw, so against both writers the two only queue.
//
// Both legs drive the REAL return body, the account credit rail with the goods back on the shelf,
// through the real pos service with its stock seams wired the way the deploy wires them (the real
// restocker and the real stock pre-lock over the inventory service). The first leg parks the return
// behind the ring's own stock row (the real UpsertBalanceDelta, then the counter drawing statement
// the ring's invoice insert prepends, on a transaction of their own); after the fix the return
// queues there before it has drawn any counter, the ring's counter draw is free, and both commit.
// The second leg parks the return INSIDE its invoices counter statement (a one shot window on the
// counter row, the house's widened window shape), then runs the REAL cancel handler as a Trenton
// manager with the reservation lane wired the way round 2's route leg wires it: before the fix the
// cancel takes the stock row and meets the parked return's counter head on; after it the return
// already holds the stock row when it parks, the cancel queues behind the return, and both answer.
//
//	Run: DATABASE_URL='postgres://.../db?sslmode=disable' go test -tags=integration -count=1 \
//	     -run 'POSReturn_The(Ring|Cancel)AgainstTheReturnToAccountCredit' ./internal/order/
import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// rloRestocker adapts the inventory service to the pos restock seam, the deploy's posRestockAdapter
// shape: the return's restock books its RETURN moves and balance upserts through inventory itself.
type rloRestocker struct{ svc *inventory.Service }

func (a rloRestocker) RestockForReturnTx(ctx context.Context, tx pgx.Tx, in pos.RestockInput) error {
	lines := make([]inventory.SaleLine, len(in.Lines))
	for i, l := range in.Lines {
		lines[i] = inventory.SaleLine{ProductID: l.ProductID, QtyBase: l.QtyBase}
	}
	return a.svc.RestockForReturnTx(ctx, tx, inventory.RestockInput{
		ReturnID: in.ReturnID, OrderID: in.OrderID, BranchID: in.BranchID, ReturnNo: in.ReturnNo, Lines: lines,
	})
}

// rloStockLocks adapts the inventory service to the pos StockLocker seam, the deploy's
// posStockAdapter shape: the return's stock pre-lock is the same sorted pass the swap already takes.
type rloStockLocks struct{ svc *inventory.Service }

func (a rloStockLocks) LockStockTx(ctx context.Context, tx pgx.Tx, branchID string, productIDs []string) error {
	return a.svc.LockStockTx(ctx, tx, branchID, productIDs)
}

// rloReturnService builds the real POS return service both legs drive: the real store over the
// pool, the real restocker and stock pre-lock over the inventory service (the deploy's wiring), and
// the default role configuration (no refund approval limit, the seeded dealer's posture: every
// return commits without an approver, so the legs exercise the lock order, not the gate).
func rloReturnService(t *testing.T, pool *pgxpool.Pool) *pos.Service {
	t.Helper()
	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatalf("roleconfig.Default: %v", err)
	}
	inv := inventory.NewService(inventory.NewRepository(pool))
	return pos.NewService(pos.NewRepository(pool)).
		WithRoleConfig(cfg).
		WithRestocker(rloRestocker{svc: inv}).
		WithStockLocks(rloStockLocks{svc: inv})
}

// rloReturnResult carries the return's own answer out of its goroutine.
type rloReturnResult struct {
	ret pos.Return
	err error
}

// rloGoReturn runs the REAL return body, the whole of the return path exactly as the counter's
// screen reaches it: the service's own transaction, the per-sale advisory lock, the invoice locks,
// the guard, the account credit rail and the restock, attributing the caller the guards would
// stamp. Nothing in it touches t, so a parked or aborted side reports through the channel.
func rloGoReturn(done chan<- rloReturnResult, svc *pos.Service, sc identity.StaffContext, orderID, lineID string, qty float64) {
	ret, err := svc.Return(identity.ContextWithStaff(context.Background(), sc), pos.ReturnInput{
		IdempotencyKey: uuid.NewString(), OrderID: orderID,
		RefundMethod: pos.RefundAccountCredit, Reason: "lock order leg", Restock: true,
		Lines: []pos.ReturnLineInput{{OrderLineID: lineID, QtyBase: qty}},
	})
	done <- rloReturnResult{ret: ret, err: err}
}

// rloSeedReturnableSale plants the sale both legs return: one committed COLLECT_NOW cash ring of the
// held product at Trenton for a terms customer, its invoice FINALIZED and owed (the account credit
// rail's ApplyCreditToInvoice offsets it and draws the invoices counter), planted in the ring's own
// tables' shape: a pos_sale facet, one order line that left the shelf whole. It answers the order
// id, its line, and the product's on hand at Trenton before anything the leg does to it.
func rloSeedReturnableSale(t *testing.T, pool *pgxpool.Pool, tag string) (ord, lineID string, qty, onHandBefore float64) {
	t.Helper()
	ctx := context.Background()
	var cust string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		 VALUES ($1, 'POS return lock order', 'CONTRACTOR', 30, 5000000) RETURNING id::text`,
		fmt.Sprintf("RLO-%s", tag)).Scan(&cust); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		 VALUES ($1, $2, $3::uuid, 'FULFILLED', 'PICKUP', 5000, 5000) RETURNING id::text`,
		fmt.Sprintf("#RLO-%s", tag), cust, trentonBranch).Scan(&ord); err != nil {
		t.Fatalf("seed the sale's order: %v", err)
	}
	var inv string
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, status, due_date,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at)
		 VALUES ($1, $2, $3::uuid, $4::uuid, 'FINALIZED', CURRENT_DATE, 5000, 0, 5000, 5000, now())
		 RETURNING id::text`,
		fmt.Sprintf("INV-RLO-%s", tag), cust, ord, trentonBranch).Scan(&inv); err != nil {
		t.Fatalf("seed the sale's invoice: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO pos_sale (idempotency_key, order_id, invoice_id, branch_id, sale_type, cash_sale, tender, created_by)
		 VALUES ($1, $2::uuid, $3::uuid, $4::uuid, 'COLLECT_NOW', TRUE, 'CASH', $5::uuid)`,
		uuid.NewString(), ord, inv, trentonBranch, w5aUser); err != nil {
		t.Fatalf("seed the pos_sale facet: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered,
		                          qty_base, unit_price_cents, price_uom, line_total_cents, tax_cents)
		 SELECT $1::uuid, 1, p.id, p.name, 2, p.base_uom, 2, 2500, p.base_uom, 5000, 0
		   FROM products p WHERE p.id = $2::uuid RETURNING id::text, qty_base`,
		ord, w5aProduct).Scan(&lineID, &qty); err != nil {
		t.Fatalf("seed the sale's line: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(on_hand_base),0) FROM stock_balances WHERE product_id = $1::uuid AND branch_id = $2::uuid`,
		w5aProduct, trentonBranch).Scan(&onHandBefore); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE payload->>'order_id' = $1`, ord)
		_, _ = pool.Exec(c, `DELETE FROM stock_moves WHERE source_type = 'pos_return' AND source_ref_id IN
			(SELECT id FROM pos_returns WHERE order_id = $1::uuid)`, ord)
		_, _ = pool.Exec(c, `DELETE FROM pos_return_lines WHERE return_id IN
			(SELECT id FROM pos_returns WHERE order_id = $1::uuid)`, ord)
		_, _ = pool.Exec(c, `DELETE FROM pos_returns WHERE order_id = $1::uuid`, ord)
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE source_ref_id::text IN
			(SELECT id::text FROM invoices WHERE order_id = $1::uuid)`, ord)
		_, _ = pool.Exec(c, `DELETE FROM pos_sale WHERE order_id = $1::uuid`, ord)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE order_id = $1::uuid`, ord)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1::uuid`, ord)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE account_no = $1`, fmt.Sprintf("RLO-%s", tag))
	})
	return ord, lineID, qty, onHandBefore
}

// rloAssertTheReturnCommitted is the return side's outcome both legs share: one committed
// ACCOUNT_CREDIT return of the whole line, the sale's invoice credited to zero and PAID, and the
// goods back on the shelf (onHandWant is the leg's own arithmetic around the restock's qty).
func rloAssertTheReturnCommitted(t *testing.T, pool *pgxpool.Pool, ord string, onHandWant float64) {
	t.Helper()
	ctx := context.Background()
	var returns int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pos_returns WHERE order_id = $1::uuid AND refund_method = 'ACCOUNT_CREDIT'`, ord).Scan(&returns); err != nil {
		t.Fatal(err)
	}
	if returns != 1 {
		t.Fatalf("the return committed, found %d pos_returns rows", returns)
	}
	var status string
	var balance int64
	if err := pool.QueryRow(ctx,
		`SELECT status, balance_cents FROM invoices WHERE order_id = $1::uuid`, ord).Scan(&status, &balance); err != nil {
		t.Fatal(err)
	}
	if status != "PAID" || balance != 0 {
		t.Fatalf("the sale's invoice = %s/%d, want PAID/0 (the rail's credit landed)", status, balance)
	}
	var onHand float64
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(on_hand_base),0) FROM stock_balances WHERE product_id = $1::uuid AND branch_id = $2::uuid`,
		w5aProduct, trentonBranch).Scan(&onHand); err != nil {
		t.Fatal(err)
	}
	if onHand != onHandWant {
		t.Fatalf("on_hand = %v, want %v (the restock put the goods back)", onHand, onHandWant)
	}
}

// rloWaitUntilSomeoneWaitsOnALock blocks until some other backend waits on a row lock (the side
// queuing behind the parked return), failing early if that side answered first. peekAnswered only
// consumes its answer on the failing path. The waiter count is database wide, which holds under
// -p 1, the same posture the ring legs take.
func rloWaitUntilSomeoneWaitsOnALock(t *testing.T, pool *pgxpool.Pool, peekAnswered func() (string, bool)) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(20 * time.Second)
	for {
		var waiting int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM pg_stat_activity
			 WHERE datname = current_database() AND wait_event_type = 'Lock' AND pid <> pg_backend_pid()`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			return
		}
		if msg, answered := peekAnswered(); answered {
			t.Fatalf("the cancel answered (%s) without queueing behind the return", msg)
		}
		if time.Now().After(deadline) {
			t.Fatal("the cancel never reached the row it queues on")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// rloWait blocks until `want` sessions of the gate database match cond, or fails the leg. The
// observer reads pg_stat_activity; nothing sleeps on a guess.
func rloWait(t *testing.T, pool *pgxpool.Pool, what string, want int, cond string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var n int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_stat_activity
			  WHERE datname = current_database() AND pid <> pg_backend_pid() AND (`+cond+`)`).Scan(&n); err != nil {
			t.Fatalf("observer: %v", err)
		}
		if n >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("barrier never reached: %s (want %d sessions where %s)", what, want, cond)
		}
		time.Sleep(15 * time.Millisecond)
	}
}

// rloParkedInInvoicesCounter matches the session parked inside the one shot counter window.
func rloParkedInInvoicesCounter() string {
	return `wait_event = 'PgSleep' AND query LIKE '%scope_sequences%'`
}

// rloArmInvoicesCounterWindow parks the NEXT writer to UPDATE the invoices counter row inside its
// statement, once: the writer sleeps 6 seconds holding the counter row and nothing its statement
// takes later, which is how the cancel leg makes the return the side that is parked (the house's
// widened window shape, a one shot flag and a fixed sleep, the other side's choreography running
// inside the window). The invoices counter row is seeded first because the window rides an AFTER
// UPDATE trigger and no migration plants the row: a first ever draw would take the INSERT arm and
// never park.
func rloArmInvoicesCounterWindow(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO scope_sequences (scope, next_value) VALUES ('invoices', 0) ON CONFLICT (scope) DO NOTHING`,
		`CREATE TABLE IF NOT EXISTS rlo_flag (id int PRIMARY KEY)`,
		`CREATE OR REPLACE FUNCTION rlo_park_iseq() RETURNS trigger LANGUAGE plpgsql AS $f$
		 BEGIN IF NEW.scope = 'invoices' THEN DELETE FROM rlo_flag WHERE id = 1; IF FOUND THEN PERFORM pg_sleep(6); END IF; END IF; RETURN NEW; END $f$`,
		`DROP TRIGGER IF EXISTS zz_rlo_park_iseq ON scope_sequences`,
		`CREATE TRIGGER zz_rlo_park_iseq AFTER UPDATE ON scope_sequences FOR EACH ROW EXECUTE FUNCTION rlo_park_iseq()`,
		`INSERT INTO rlo_flag VALUES (1) ON CONFLICT DO NOTHING`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("arm the invoices counter window: %v", err)
		}
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DROP TRIGGER IF EXISTS zz_rlo_park_iseq ON scope_sequences`)
		_, _ = pool.Exec(c, `DROP TABLE IF EXISTS rlo_flag`)
	})
}

// rloAssertTheCancelSettled is the cancel side's outcome the cancel leg asserts: the order
// CANCELLED, the deposit document settled to VOID at zero, the hold released with the projection
// unwound (the stock row's own on hand is the return leg's business, asserted beside it).
func rloAssertTheCancelSettled(t *testing.T, pool *pgxpool.Pool, ord, dep string, reservedBefore float64) {
	t.Helper()
	ctx := context.Background()
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM orders WHERE id = $1::uuid`, ord).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "CANCELLED" {
		t.Fatalf("the order = %s, want CANCELLED", status)
	}
	var docStatus string
	var docBalance int64
	if err := pool.QueryRow(ctx,
		`SELECT status, balance_cents FROM invoices WHERE id = $1::uuid`, dep).Scan(&docStatus, &docBalance); err != nil {
		t.Fatal(err)
	}
	if docStatus != "VOID" || docBalance != 0 {
		t.Fatalf("the deposit document = %s/%d, want VOID/0", docStatus, docBalance)
	}
	if s := w5aHoldStatuses(t, pool, ord); len(s) != 1 || s[0] != "RELEASED" {
		t.Fatalf("the hold's terminal statuses = %v, want the one RELEASED", s)
	}
	var reservedAfter float64
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(qty_reserved),0) FROM stock_balances WHERE product_id = $1::uuid AND branch_id = $2::uuid`,
		w5aProduct, trentonBranch).Scan(&reservedAfter); err != nil {
		t.Fatal(err)
	}
	if reservedAfter != reservedBefore {
		t.Fatalf("qty_reserved = %v after the cancel, want the seeded %v back (the release must unwind the hold)", reservedAfter, reservedBefore)
	}
}

// TestIntegration_POSReturn_TheRingAgainstTheReturnToAccountCredit: the ring's stock row then
// counter against the return's rail then restock. The ring's transaction takes the product's
// balance row (the real UpsertBalanceDelta) and holds it; the REAL return runs beside it and, on
// the old order, draws the invoices counter through its rail and then parks on the ring's stock row
// at its restock: the ring's counter draw closes the cycle and Postgres aborts a side with a 40P01.
// With the return taking its restock's stock rows BEFORE its first counter draw, the return queues
// on the ring's row having drawn no counter, the ring's counter draw is free, the ring commits, and
// the return answers behind it: both sides must commit, the sale credited and the goods back.
func TestIntegration_POSReturn_TheRingAgainstTheReturnToAccountCredit(t *testing.T) {
	pool := intPool(t)
	svc := rloReturnService(t, pool)
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	ord, lineID, qty, onHandBefore := rloSeedReturnableSale(t, pool, tag)

	ring := ringTakesTheStockRow(t, pool, w5aProduct, trentonBranch)

	done := make(chan rloReturnResult, 1)
	go rloGoReturn(done, svc, managerAt(w5aUser, trentonBranch), ord, lineID, qty)
	peek := func() (string, bool) {
		select {
		case r := <-done:
			return fmt.Sprintf("%v", r.err), true
		default:
			return "", false
		}
	}
	parkBehindTheRingsStockRow(t, pool, peek)
	theRingDrawsTheCounterAndCommits(t, ring)

	r := <-done
	if r.err != nil {
		t.Fatalf("the return (rail then restock): %v (a 40P01 here is the return holding the invoices counter its rail drew while its restock waits on the ring's stock row)", r.err)
	}
	rloAssertTheReturnCommitted(t, pool, ord, onHandBefore-1+qty)
}

// TestIntegration_POSReturn_TheCancelAgainstTheReturnToAccountCredit: the real cancel route against
// the return's rail then restock. The REAL return parks inside its invoices counter statement (the
// one shot window), holding the counter; the REAL cancel handler runs as a Trenton manager with the
// reservation lane wired the way the deploy wires it, so its one transaction takes the cancelled
// order's stock rows before its settlement draws the same counter. On the old order the cancel
// takes the stock row free and parks on the return's counter: the window ending has the return's
// restock park on the cancel's stock row, the cycle closes, and Postgres aborts a side (the cancel
// answers its route 500). With the return taking its restock's stock rows BEFORE its first counter
// draw, the return already holds the stock row when it parks, the cancel queues behind the return
// from its lock pass, and both answer: the cancel 200 with the deposit settled and the hold
// released, the return committed beside it.
func TestIntegration_POSReturn_TheCancelAgainstTheReturnToAccountCredit(t *testing.T) {
	w := newDepositCancelWorldWithStock(t)
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	ord, lineID, qty, _ := rloSeedReturnableSale(t, w.pool, tag)
	depOrd, dep, onHandBefore, reservedBefore := seedDepositOrderWithHold(t, w, tag+"d")
	svc := rloReturnService(t, w.pool)

	rloArmInvoicesCounterWindow(t, w.pool)
	done := make(chan rloReturnResult, 1)
	go rloGoReturn(done, svc, managerAt(w5aUser, trentonBranch), ord, lineID, qty)
	rloWait(t, w.pool, "the return parked inside its invoices counter draw", 1, rloParkedInInvoicesCounter())

	type cancelResult struct {
		code int
		body string
	}
	cdone := make(chan cancelResult, 1)
	go func() {
		rr := invoke(t, w.h.cancel, managerAt(intManager, trentonBranch), depOrd, `{"reason":"customer walked away"}`)
		cdone <- cancelResult{rr.Code, rr.Body.String()}
	}()
	cpeek := func() (string, bool) {
		select {
		case r := <-cdone:
			return fmt.Sprintf("code %d body %s", r.code, r.body), true
		default:
			return "", false
		}
	}
	rloWaitUntilSomeoneWaitsOnALock(t, w.pool, cpeek)

	cr := <-cdone
	if cr.code != http.StatusOK {
		t.Fatalf("the cancel: code %d body %s (a 500 here with the return's rail drawn is the cancel aborted as the deadlock's victim, holding the stock rows its lock pass took while its settlement waits on the return's invoices counter)", cr.code, cr.body)
	}
	r := <-done
	if r.err != nil {
		t.Fatalf("the return (rail then restock): %v (a 40P01 here is the return holding the invoices counter its rail drew while its restock waits on the cancel's stock rows)", r.err)
	}

	rloAssertTheCancelSettled(t, w.pool, depOrd, dep, reservedBefore)
	rloAssertTheReturnCommitted(t, w.pool, ord, onHandBefore+qty)
}
