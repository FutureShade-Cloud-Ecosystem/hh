//go:build integration

// w5a_reservation_integration_test.go, W4 seam 3 LANDING, proved end to end against the live
// database through the SAME production code paths cmd/server/main.go wires.
//
// These are the four claims the acceptance criteria name, and every one of them is a claim a unit
// fake CANNOT make, because the whole property under test is "the hold and the status share ONE
// transaction":
//
//	confirm → a HARD reservation exists AND stock_balances.qty_reserved moved
//	fulfil  → the hold closes CONSUMED and the projection unwinds
//	cancel  → the hold closes RELEASED and the projection unwinds
//	a transition that fails AFTER the reserve leaves NO hold behind (the atomicity claim)
//
// The reserver here is the REAL *inventory.Service through the SAME adapter shape main.go binds, so
// what is exercised is production's arithmetic, production's policy resolution and production's
// projection maintenance, not a stand-in.
//
// DIB-396: these fixtures write stock_reservations and move qty_reserved on a SEEDED product that
// other packages read as availability, so they take the stock lease in SHARE mode for the duration
// and restore what they touch on cleanup.
package order

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// w5aProduct is a seeded catalogue product with real stock at Trenton (0003/0005 opening moves).
const w5aProduct = "a7000000-0000-4000-9000-000000000019" // DBS-AGG-HPB

// w5aUser is a REAL seeded users row. stock_reservations.created_by carries an FK to users (0115),
// so the actor a reserve stamps must exist, which it always does in production, where the id comes
// from the guard-stamped StaffContext. Using a fabricated uuid here (as the DIB-191 timeline tests
// legitimately do, because audit_log.actor_id has NO FK) would fail on that constraint and prove
// nothing about the code.
const w5aUser = "40000061-0000-4000-9000-000000000001" // the seeded staff user linked to the reservation

// - the production reserver, through the production adapter shape -

// w5aInvReserver is the SAME translation cmd/server/main.go's w5aStockReserver performs. It is
// duplicated here rather than imported because main is package main, the shape is asserted
// identical by the fact that both compile against order.StockReserver.
type w5aInvReserver struct{ svc *inventory.Service }

func (a w5aInvReserver) ReserveTx(ctx context.Context, tx pgx.Tx, in StockReserveInput) (StockReserveResult, error) {
	res, err := a.svc.ReserveTx(ctx, tx, inventory.ReserveInput{
		ProductID: in.ProductID, BranchID: in.BranchID, LocationID: in.LocationID,
		ResType: in.ResType, QtyBase: in.QtyBase, SourceType: in.SourceType,
		SourceRefID: in.SourceRefID, Reason: in.Reason, CreatedBy: in.CreatedBy,
		EnforceAvailable: in.EnforceAvailable,
	})
	if err != nil {
		return StockReserveResult{}, err
	}
	return StockReserveResult{
		ReservationID: res.Reservation.ID, HoldMode: res.Reservation.HoldMode, Applied: res.Applied,
	}, nil
}

func (a w5aInvReserver) ReleaseBySourceTx(ctx context.Context, tx pgx.Tx, sourceType, sourceRefID, status string) (int, error) {
	return a.svc.ReleaseBySourceTx(ctx, tx, sourceType, sourceRefID, status)
}

// - fixtures -

// w5aWidePool is intPool with an explicit, generous MaxConns. The default is max(4, NumCPU), which
// is not enough for a test that runs several transactions at once when each of them also needs a
// second connection mid-flight (see TestIntegration_W5A_ConcurrentApplyLandsExactlyOnce).
func w5aWidePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		if set {
			t.Fatalf("parse DATABASE_URL: %v", err)
		}
		t.Skipf("parse DATABASE_URL: %v", err)
	}
	cfg.MaxConns = 16
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil || pool.Ping(context.Background()) != nil {
		if set {
			t.Fatalf("integration DB unavailable: %v", err)
		}
		t.Skipf("integration DB unavailable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// w5aService builds the order service exactly as main.go does for the reservation lane: the real
// pgx repository, the real audit spine, the real inventory service behind the seam.
func w5aService(t *testing.T, pool *pgxpool.Pool) *Service {
	t.Helper()
	auditSvc := audit.NewService(audit.NewRepository(pool), nil)
	svc := NewService(NewRepository(pool)).WithAudit(auditSvc).WithAuditTrail(auditSvc)
	svc.WithStockReserver(w5aInvReserver{svc: inventory.NewService(inventory.NewRepository(pool))})
	if !svc.StockSeamWired() {
		t.Fatal("the stock seam did not bind, the whole reservation lane would be inert")
	}
	return svc
}

// w5aSeedOrderWithLine inserts a customer + one order at `status` with one STOCKED line, and
// registers full cleanup INCLUDING the reservations and the qty_reserved projection this test will
// move. Restoring what a fixture touches is what keeps the shared DB usable in parallel.
func w5aSeedOrderWithLine(t *testing.T, pool *pgxpool.Pool, status string, qty float64) (cust, ord string) {
	return w5aSeedOrderWithPricedLine(t, pool, status, qty, 1000)
}

// w5aSeedOrderWithPricedLine is the same fixture with the line money stated explicitly, so a
// change-order test can construct a delta of an EXACT size: the seeded line total must agree with
// what the fixture pricer will quote, or the $500 boundary assertion measures the fixture rather
// than the rule.
func w5aSeedOrderWithPricedLine(t *testing.T, pool *pgxpool.Pool, status string, qty float64, unitCents int64) (cust, ord string) {
	t.Helper()
	ctx := context.Background()
	dib396LeaseShared(t, pool, dib396StockDriftLease)

	stamp := time.Now().UnixNano()
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name) VALUES ($1, 'W5A Reservation Tenant') RETURNING id::text`,
		fmt.Sprintf("W5A-%d", stamp)).Scan(&cust); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		 VALUES ($1, $2, $3, $4, 'PICKUP', $5, $5) RETURNING id::text`,
		fmt.Sprintf("#W5A-%d", stamp), cust, trentonBranch, status, int64(qty)*unitCents).Scan(&ord); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered,
		                         qty_base, unit_price_cents, price_uom, line_total_cents)
		SELECT $1::uuid, 1, $2::uuid, 'W5A stocked line', $3, p.base_uom, $3, $4, p.base_uom, $5
		  FROM products p WHERE p.id = $2::uuid`, ord, w5aProduct, qty, unitCents, int64(qty)*unitCents); err != nil {
		t.Fatalf("seed order line: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		// Unwind the projection this order's holds contributed, then delete them, then the order.
		// Order matters: deleting the reservation first would strand qty_reserved.
		_, _ = pool.Exec(c, `
			UPDATE stock_balances sb
			   SET qty_reserved = GREATEST(sb.qty_reserved - h.held, 0)
			  FROM (SELECT product_id, location_id, SUM(qty_base) AS held
			          FROM stock_reservations
			         WHERE source_ref_id = $1 AND status = 'ACTIVE' AND hold_mode = 'HARD'
			         GROUP BY product_id, location_id) h
			 WHERE sb.product_id = h.product_id AND sb.location_id = h.location_id`, ord)
		_, _ = pool.Exec(c, `DELETE FROM stock_reservations WHERE source_ref_id = $1`, ord)
		_, _ = pool.Exec(c, `DELETE FROM change_orders WHERE order_id = $1`, ord)
		_, _ = pool.Exec(c, `DELETE FROM shipment_groups WHERE order_id = $1`, ord)
		_, _ = pool.Exec(c, `DELETE FROM audit_log WHERE entity_id = $1`, ord)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1`, ord)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, cust)
	})
	return cust, ord
}

// w5aStaff builds a staff context at Trenton. manager=true is Tier 2 (the cancel/apply authority).
func w5aStaff(manager bool) identity.StaffContext {
	tier := identity.Tier(4)
	if manager {
		tier = identity.TierManager
	}
	return identity.StaffContext{
		UserID: w5aUser, Status: identity.StatusActive, Tier: tier,
		Scope: identity.ScopeBranch, Branches: map[string]bool{trentonBranch: true},
	}
}

// w5aHolds reads the ACTIVE hold rows this order owns, straight from Postgres.
func w5aHolds(t *testing.T, pool *pgxpool.Pool, orderID string) (n int, qty float64, mode string) {
	t.Helper()
	err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*), COALESCE(SUM(qty_base),0), COALESCE(MAX(hold_mode),'')
		  FROM stock_reservations
		 WHERE source_type = 'order' AND source_ref_id = $1 AND status = 'ACTIVE'`, orderID).
		Scan(&n, &qty, &mode)
	if err != nil {
		t.Fatalf("read holds: %v", err)
	}
	return n, qty, mode
}

// w5aHoldStatuses returns the terminal statuses of this order's CLOSED holds.
func w5aHoldStatuses(t *testing.T, pool *pgxpool.Pool, orderID string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT status FROM stock_reservations WHERE source_ref_id = $1 AND status <> 'ACTIVE' ORDER BY status`,
		orderID)
	if err != nil {
		t.Fatalf("read closed holds: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

// w5aReserved reads the qty_reserved PROJECTION for the fixture product at Trenton. This is the
// number every availability read in the tree subtracts, so it is the one that actually matters.
func w5aReserved(t *testing.T, pool *pgxpool.Pool) float64 {
	t.Helper()
	var q float64
	if err := pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(qty_reserved),0) FROM stock_balances WHERE product_id = $1 AND branch_id = $2`,
		w5aProduct, trentonBranch).Scan(&q); err != nil {
		t.Fatalf("read qty_reserved: %v", err)
	}
	return q
}

// - the four lifecycle claims -

// TestIntegration_W5A_ConfirmHardReserves, DRAFT→CONFIRMED through the production transition path
// writes a HARD hold AND moves the projection, in one transaction.
func TestIntegration_W5A_ConfirmHardReserves(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithLine(t, pool, "DRAFT", 7)
	svc := w5aService(t, pool)

	before := w5aReserved(t, pool)
	ctx := identity.ContextWithStaff(context.Background(), w5aStaff(false))
	if _, err := svc.TransitionManual(ctx, ord, ActionConfirm, ""); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	n, qty, mode := w5aHolds(t, pool, ord)
	if n != 1 {
		t.Fatalf("ACTIVE holds after confirm = %d, want 1, confirm did not hard-reserve the line", n)
	}
	if mode != "HARD" {
		t.Fatalf("hold mode = %q, want HARD (a SOFT hold on a confirmed order does not gate anything)", mode)
	}
	if qty != 7 {
		t.Fatalf("held qty = %v, want 7", qty)
	}
	if got := w5aReserved(t, pool) - before; got != 7 {
		t.Fatalf("qty_reserved moved by %v, want 7, the hold row exists but availability did not change, "+
			"which is the projection drift 0115 exists to prevent", got)
	}
	if liveStatus(t, pool, ord) != "CONFIRMED" {
		t.Fatal("the order did not actually reach CONFIRMED")
	}
}

// TestIntegration_W5A_FulfilConsumes, the event driver's advance to FULFILLED closes the holds as
// CONSUMED (not RELEASED: the stock LEFT on this order) and unwinds the projection.
func TestIntegration_W5A_FulfilConsumes(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithLine(t, pool, "DRAFT", 5)
	svc := w5aService(t, pool)
	ctx := identity.ContextWithStaff(context.Background(), w5aStaff(false))

	before := w5aReserved(t, pool)
	if _, err := svc.TransitionManual(ctx, ord, ActionConfirm, ""); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if n, _, _ := w5aHolds(t, pool, ord); n != 1 {
		t.Fatalf("precondition: expected one ACTIVE hold, got %d", n)
	}

	// The production event path, the same call the LifecycleConsumer makes on order.fulfilled.
	if err := svc.AdvanceFromEvent(context.Background(), ord, "FULFILLED", "w5a-test-event"); err != nil {
		t.Fatalf("advance to FULFILLED: %v", err)
	}

	if n, _, _ := w5aHolds(t, pool, ord); n != 0 {
		t.Fatalf("ACTIVE holds after fulfilment = %d, want 0, a hold that outlives its order is a "+
			"phantom shortage nobody can clear", n)
	}
	got := w5aHoldStatuses(t, pool, ord)
	if len(got) != 1 || got[0] != ResConsumed {
		t.Fatalf("closed hold statuses = %v, want [CONSUMED], RELEASED would say the order never took "+
			"the stock, which is the opposite of what happened", got)
	}
	if now := w5aReserved(t, pool); now != before {
		t.Fatalf("qty_reserved = %v after fulfilment, want it back to %v", now, before)
	}
}

// TestIntegration_W5A_CancelReleases, a manager cancel closes the holds as RELEASED and the stock
// returns to available.
func TestIntegration_W5A_CancelReleases(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithLine(t, pool, "DRAFT", 9)
	svc := w5aService(t, pool)
	// The cancel money predicate must be able to EVALUATE (it fails closed when unwired), so bind the
	// same two readers main.go binds. Both answer "no money attached" for this fresh order.
	svc.WithCancelGuards(w5aNoInvoices{}, w5aNoSales{})
	ctx := identity.ContextWithStaff(context.Background(), w5aStaff(true))

	before := w5aReserved(t, pool)
	if _, err := svc.TransitionManual(ctx, ord, ActionConfirm, ""); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if _, err := svc.TransitionManual(ctx, ord, ActionCancel, "customer changed their mind"); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	if n, _, _ := w5aHolds(t, pool, ord); n != 0 {
		t.Fatalf("ACTIVE holds after cancel = %d, want 0, cancelled stock that stays held is stock "+
			"nobody can sell and nobody can find", n)
	}
	got := w5aHoldStatuses(t, pool, ord)
	if len(got) != 1 || got[0] != ResReleased {
		t.Fatalf("closed hold statuses = %v, want [RELEASED]", got)
	}
	if now := w5aReserved(t, pool); now != before {
		t.Fatalf("qty_reserved = %v after cancel, want it back to %v", now, before)
	}
}

// w5aNoInvoices / w5aNoSales satisfy the DIB-191 cancel-predicate seams with "no money attached".
type w5aNoInvoices struct{}

func (w5aNoInvoices) OrderHasFinalizedInvoice(context.Context, string) (bool, error) {
	return false, nil
}

type w5aNoSales struct{}

func (w5aNoSales) OrderHasCommittedPOSSale(context.Context, string) (bool, error) { return false, nil }

// TestIntegration_W5A_ReserveAndStatusShareOneTransaction is the ATOMICITY claim, and it is the one
// that justifies the whole tx-scoped writer: when the reserve fails, the STATUS must not move
// either. A hold written in a second transaction could not make this promise.
//
// The failure is injected at the seam (a reserver that returns an error), which is the only way to
// exercise the rollback path without corrupting the database to provoke it.
func TestIntegration_W5A_ReserveAndStatusShareOneTransaction(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithLine(t, pool, "DRAFT", 4)

	auditSvc := audit.NewService(audit.NewRepository(pool), nil)
	svc := NewService(NewRepository(pool)).WithAudit(auditSvc)
	svc.WithStockReserver(&fakeReserver{err: errors.New("inventory is down")})

	ctx := identity.ContextWithStaff(context.Background(), w5aStaff(false))
	if _, err := svc.TransitionManual(ctx, ord, ActionConfirm, ""); err == nil {
		t.Fatal("confirm succeeded despite the reserve failing, the two are not sharing a transaction")
	}
	if got := liveStatus(t, pool, ord); got != "DRAFT" {
		t.Fatalf("order status = %q after a failed reserve, want DRAFT, the status write was NOT rolled "+
			"back, so the order is confirmed while holding nothing", got)
	}
	if n, _, _ := w5aHolds(t, pool, ord); n != 0 {
		t.Fatalf("holds after a failed confirm = %d, want 0", n)
	}
}

// TestIntegration_W5A_ResyncMakesHoldsMatchTheLines, the repair/apply primitive. After a line
// changes underneath a confirmed order, resync leaves the holds equal to the lines, with no stale
// row surviving.
func TestIntegration_W5A_ResyncMakesHoldsMatchTheLines(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithLine(t, pool, "DRAFT", 6)
	svc := w5aService(t, pool)
	ctx := identity.ContextWithStaff(context.Background(), w5aStaff(false))

	before := w5aReserved(t, pool)
	if _, err := svc.TransitionManual(ctx, ord, ActionConfirm, ""); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	// Move the line behind the service's back, exactly the drift the resync exists to repair.
	if _, err := pool.Exec(context.Background(),
		`UPDATE order_lines SET qty_base = 11, qty_entered = 11 WHERE order_id = $1 AND line_no = 1`, ord); err != nil {
		t.Fatalf("drift the line: %v", err)
	}

	if _, err := svc.ResyncReservations(context.Background(), ord, trentonBranch, w5aUser); err != nil {
		t.Fatalf("resync: %v", err)
	}
	n, qty, _ := w5aHolds(t, pool, ord)
	if n != 1 || qty != 11 {
		t.Fatalf("after resync: %d hold(s) totalling %v, want exactly 1 for 11, the old 6-unit hold "+
			"survived, which is precisely what release-then-reserve exists to prevent", n, qty)
	}
	if got := w5aReserved(t, pool) - before; got != 11 {
		t.Fatalf("qty_reserved moved by %v, want 11 (not 17, the stale hold must not still be counted)", got)
	}
}
