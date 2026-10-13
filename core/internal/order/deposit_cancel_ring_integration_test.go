//go:build integration

package order

// deposit_cancel_ring_integration_test.go, the cancel's lock order against the
// POS ring (fix round 2, the Opus read's P2-1): the deposit settlement runs
// inside the order cancel's transaction and draws the invoices feed counter,
// and the release hook writes the order's stock rows AFTER it, while the POS
// collect now ring takes a stock_balances row FIRST and draws that same
// counter LATER (LOCK-ORDER's ring row). A cancel that drew the counter before
// its stock rows could meet the ring head on: the cancel holds the counter and
// waits on the stock row, the ring holds the stock row and waits on the
// counter, and Postgres aborts one side whole with a 40P01.
//
// Two legs, one choreography: the ring's own two statements on a transaction
// of their own (the real UpsertBalanceDelta on the held product's balance row,
// then the counter drawing statement the ring's invoice insert prepends)
// against a cancel side that parks on the stock row. The first leg drives the
// reader's shape, the cancel's transaction hand assembled in the route's own
// statement order around the REAL settlement and the REAL release, so the 40P01
// surfaces raw; the second drives the REAL cancel route with the reservation
// lane wired the way the deploy wires it, so the shipped path is the one
// proved. The park is where the two sides of the fix differ: before it the
// cancel parks at the release, AFTER the settlement drew the counter, and the
// ring's counter draw closes the cycle; after it the cancel parks on the stock
// rows the settlement's caller now takes BEFORE any counter draw, the ring's
// counter draw is free, and the two only queue.
//
//	Run: DATABASE_URL='postgres://.../db?sslmode=disable' go test -tags=integration -count=1 \
//	     -run 'DepositCancel_(TheSettledCancelAgainstTheRing|TheCancelTakesItsStockRowsBeforeTheInvoicesCounter)' ./internal/order/
import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// newDepositCancelWorldWithStock is newDepositCancelWorld with the reservation
// lane wired the way the deploy wires it: the real inventory service behind the
// seam, through the same adapter shape main.go binds (w5aInvReserver). With it
// the cancel routes through the transactional status writer and its release
// hook, the shipped shape; without it the laneless path holds no release and no
// stock rows at all.
func newDepositCancelWorldWithStock(t *testing.T) *depositCancelWorld {
	t.Helper()
	w := newDepositCancelWorld(t)
	w.svc.WithStockReserver(w5aInvReserver{svc: inventory.NewService(inventory.NewRepository(w.pool))})
	if !w.svc.StockSeamWired() {
		t.Fatal("the stock seam did not bind, the cancel would take the laneless path")
	}
	return w
}

// seedDepositOrderWithHold plants the world the two ring legs share: a live
// deposit document on a CONFIRMED order, and one ACTIVE HARD hold of a real
// seeded product with a real balance row at Trenton, the projection bumped by
// the held qty. It answers the product's on hand and reserved figures as they
// stood BEFORE the hold was planted, the baseline the outcome assertion
// compares against. DIB-396: the fixture moves qty_reserved on a seeded
// product, so it takes the stock lease in SHARE mode and restores what it
// touched.
func seedDepositOrderWithHold(t *testing.T, w *depositCancelWorld, tag string) (ord, dep string, onHandBefore, reservedBefore float64) {
	t.Helper()
	ctx := context.Background()
	_, ord, dep = w.seedDepositOrder(t, tag, false)
	dib396LeaseShared(t, w.pool, dib396StockDriftLease)
	if err := w.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(on_hand_base),0), COALESCE(SUM(qty_reserved),0)
		   FROM stock_balances WHERE product_id = $1::uuid AND branch_id = $2::uuid`,
		w5aProduct, trentonBranch).Scan(&onHandBefore, &reservedBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := w.pool.Exec(ctx, `
		INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base, qty_reserved, updated_at)
		VALUES ($1::uuid, $2::uuid, $2::uuid, 0, 5, now())
		ON CONFLICT (product_id, location_id)
		DO UPDATE SET qty_reserved = stock_balances.qty_reserved + 5, updated_at = now()`,
		w5aProduct, trentonBranch); err != nil {
		t.Fatalf("seed the balance row's reserved side: %v", err)
	}
	if _, err := w.pool.Exec(ctx, `
		INSERT INTO stock_reservations
			(product_id, branch_id, location_id, res_type, hold_mode, qty_base,
			 source_type, source_ref_id, reason, created_by)
		VALUES ($1::uuid, $2::uuid, $2::uuid, 'ORDER', 'HARD', 5, 'order', $3::uuid,
		        'the ring leg hold', $4::uuid)`,
		w5aProduct, trentonBranch, ord, w5aUser); err != nil {
		t.Fatalf("seed the order's hard hold: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = w.pool.Exec(c, `
			UPDATE stock_balances sb
			   SET qty_reserved = GREATEST(sb.qty_reserved - h.held, 0)
			  FROM (SELECT product_id, location_id, SUM(qty_base) AS held
			          FROM stock_reservations
			         WHERE source_ref_id = $1 AND status = 'ACTIVE' AND hold_mode = 'HARD'
			         GROUP BY product_id, location_id) h
			 WHERE sb.product_id = h.product_id AND sb.location_id = h.location_id`, ord)
		_, _ = w.pool.Exec(c, `DELETE FROM stock_reservations WHERE source_ref_id = $1`, ord)
		_, _ = w.pool.Exec(c, `
			UPDATE stock_balances SET on_hand_base = $3, updated_at = now()
			 WHERE product_id = $1::uuid AND location_id = $2::uuid`, w5aProduct, trentonBranch, onHandBefore)
		_, _ = w.pool.Exec(c, `DELETE FROM events_outbox WHERE (payload->>'invoice_id')::uuid = $1::uuid`, dep)
	})
	return ord, dep, onHandBefore, reservedBefore
}

// ringTakesTheStockRow opens the ring's transaction and takes its first
// statement: the real UpsertBalanceDelta on the held product's balance row, the
// statement the POS collect now ring decrements stock with. The transaction is
// handed back open, held by the caller until the cancel has parked on the row.
func ringTakesTheStockRow(t *testing.T, pool *pgxpool.Pool, product, branch string) pgx.Tx {
	t.Helper()
	ctx := context.Background()
	ring, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := inventory.NewRepository(pool).UpsertBalanceDelta(ctx, ring, product, branch, branch, -1); err != nil {
		t.Fatalf("the ring's stock row: %v", err)
	}
	return ring
}

// theRingDrawsTheCounterAndCommits runs the ring's second statement, the
// invoices counter, with the exact CTE the ring's invoice insert prepends, then
// commits. Called with the cancel parked on the stock row, this is the
// statement that either closes the cycle (before the fix, the cancel already
// holds the counter) or runs free (after it, the cancel has drawn no counter).
func theRingDrawsTheCounterAndCommits(t *testing.T, ring pgx.Tx) {
	t.Helper()
	ctx := context.Background()
	defer func() { _ = ring.Rollback(ctx) }()
	if _, err := ring.Exec(ctx, `
		WITH iseq AS (INSERT INTO scope_sequences (scope) VALUES ('invoices')
		              ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
		              RETURNING next_value)
		SELECT next_value FROM iseq`); err != nil {
		t.Fatalf("the ring's invoices counter: %v (a 40P01 here is the ring meeting the cancel's counter head on)", err)
	}
	if err := ring.Commit(ctx); err != nil {
		t.Fatalf("the ring's collect now half: %v (a 40P01 here is the ring meeting the cancel's counter head on)", err)
	}
}

// parkBehindTheRingsStockRow blocks until some other backend waits on a lock
// (the cancel parked on the ring's stock row), failing early if the cancel
// answered first. peekAnswered only consumes its answer on the failing path.
// The waiter count is database wide, which holds under -p 1, the same posture
// the apply race legs take.
func parkBehindTheRingsStockRow(t *testing.T, pool *pgxpool.Pool, peekAnswered func() (string, bool)) {
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
			t.Fatalf("the cancel answered (%s) without queueing behind the ring's stock row", msg)
		}
		if time.Now().After(deadline) {
			t.Fatal("the cancel never reached the ring's stock row")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// assertTheCancelledOrderLeftTheWorldTheRingExpects is the outcome both legs
// share: the order CANCELLED, the deposit document settled to VOID at zero, the
// hold released with the projection unwound, and the ring's own move committed
// beside it.
func assertTheCancelledOrderLeftTheWorldTheRingExpects(t *testing.T, w *depositCancelWorld, ord, dep string, reservedBefore, onHandBefore float64) {
	t.Helper()
	ctx := context.Background()
	var status string
	if err := w.pool.QueryRow(ctx, `SELECT status FROM orders WHERE id = $1::uuid`, ord).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "CANCELLED" {
		t.Fatalf("the order = %s, want CANCELLED", status)
	}
	var docStatus string
	var docBalance int64
	if err := w.pool.QueryRow(ctx,
		`SELECT status, balance_cents FROM invoices WHERE id = $1::uuid`, dep).Scan(&docStatus, &docBalance); err != nil {
		t.Fatal(err)
	}
	if docStatus != "VOID" || docBalance != 0 {
		t.Fatalf("the deposit document = %s/%d, want VOID/0", docStatus, docBalance)
	}
	if s := w5aHoldStatuses(t, w.pool, ord); len(s) != 1 || s[0] != "RELEASED" {
		t.Fatalf("the hold's terminal statuses = %v, want the one RELEASED", s)
	}
	var onHandAfter, reservedAfter float64
	if err := w.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(on_hand_base),0), COALESCE(SUM(qty_reserved),0)
		   FROM stock_balances WHERE product_id = $1::uuid AND branch_id = $2::uuid`,
		w5aProduct, trentonBranch).Scan(&onHandAfter, &reservedAfter); err != nil {
		t.Fatal(err)
	}
	if reservedAfter != reservedBefore {
		t.Fatalf("qty_reserved = %v after the cancel, want the seeded %v back (the release must unwind the hold)", reservedAfter, reservedBefore)
	}
	if onHandAfter != onHandBefore-1 {
		t.Fatalf("on_hand = %v after the ring, want the seeded %v less the ring's one", onHandAfter, onHandBefore)
	}
}

// TestIntegration_DepositCancel_TheSettledCancelAgainstTheRing: the reader's
// shape, the leg the round's brief names first. The cancel's transaction is
// hand assembled in the route's own statement order (UpdateStatusFromTx at
// source): the order row lock, the money predicate and the settlement (the
// precheck's two halves, the REAL CancelOrderDeposits with the stock rows the
// fixed caller takes before it), the status flip, then the REAL release. The
// raw error of whichever side Postgres aborts surfaces in the leg itself.
func TestIntegration_DepositCancel_TheSettledCancelAgainstTheRing(t *testing.T) {
	w := newDepositCancelWorldWithStock(t)
	ctx := context.Background()
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	ord, dep, onHandBefore, reservedBefore := seedDepositOrderWithHold(t, w, tag)

	ring := ringTakesTheStockRow(t, w.pool, w5aProduct, trentonBranch)

	done := make(chan error, 1)
	go func() {
		tx, err := w.pool.Begin(ctx)
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()
		var oldStatus, branchID string
		if err := tx.QueryRow(ctx,
			`SELECT status, branch_id::text FROM orders WHERE id = $1 FOR UPDATE`, ord).
			Scan(&oldStatus, &branchID); err != nil {
			done <- err
			return
		}
		pctx := withPrecheckTx(ctx, tx)
		if err := w.svc.assertCancellable(pctx, ord); err != nil {
			done <- err
			return
		}
		if err := w.svc.settleDepositsOnCancel(pctx, ord, DepositDispositionCreditBack, "order cancelled against the ring"); err != nil {
			done <- err
			return
		}
		if _, err := tx.Exec(ctx,
			`UPDATE orders SET status = 'CANCELLED', updated_at = now() WHERE id = $1`, ord); err != nil {
			done <- err
			return
		}
		if _, err := w.svc.stock.ReleaseBySourceTx(ctx, tx, ResSourceOrder, ord, ResReleased); err != nil {
			done <- err
			return
		}
		done <- tx.Commit(ctx)
	}()
	peek := func() (string, bool) {
		select {
		case err := <-done:
			return fmt.Sprintf("%v", err), true
		default:
			return "", false
		}
	}
	parkBehindTheRingsStockRow(t, w.pool, peek)
	theRingDrawsTheCounterAndCommits(t, ring)

	if err := <-done; err != nil {
		t.Fatalf("the cancel's transaction: %v (a 40P01 here is the cancel holding the invoices counter the settlement drew while its release waits on the ring's stock row)", err)
	}
	assertTheCancelledOrderLeftTheWorldTheRingExpects(t, w, ord, dep, reservedBefore, onHandBefore)
}

// TestIntegration_DepositCancel_TheCancelTakesItsStockRowsBeforeTheInvoicesCounter:
// the real route, the shipped path. The cancel runs through the real handler
// as a Trenton manager with the reservation lane wired, so its one transaction
// is the deploy's: the order row, the precheck (predicate, stock rows,
// settlement), the status flip and the release. The handler maps an aborted
// transaction to a 500, so this leg reads the deadlock from the two sides'
// answers rather than the raw error text the first leg carries.
func TestIntegration_DepositCancel_TheCancelTakesItsStockRowsBeforeTheInvoicesCounter(t *testing.T) {
	w := newDepositCancelWorldWithStock(t)
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	ord, dep, onHandBefore, reservedBefore := seedDepositOrderWithHold(t, w, tag)

	ring := ringTakesTheStockRow(t, w.pool, w5aProduct, trentonBranch)

	type cancelResult struct {
		code int
		body string
	}
	done := make(chan cancelResult, 1)
	sc := managerAt(intManager, trentonBranch)
	go func() {
		rr := invoke(t, w.h.cancel, sc, ord, `{"reason":"customer walked away"}`)
		done <- cancelResult{rr.Code, rr.Body.String()}
	}()
	peek := func() (string, bool) {
		select {
		case r := <-done:
			return fmt.Sprintf("code %d body %s", r.code, r.body), true
		default:
			return "", false
		}
	}
	parkBehindTheRingsStockRow(t, w.pool, peek)
	theRingDrawsTheCounterAndCommits(t, ring)

	r := <-done
	if r.code != http.StatusOK {
		t.Fatalf("the cancel: code %d body %s (a 500 here with the ring committed beside it is the cancel aborted as the deadlock's victim, the counter held while its release waited on the ring's stock row)", r.code, r.body)
	}
	assertTheCancelledOrderLeftTheWorldTheRingExpects(t, w, ord, dep, reservedBefore, onHandBefore)
}
