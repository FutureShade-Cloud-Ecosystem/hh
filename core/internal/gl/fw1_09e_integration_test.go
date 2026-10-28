//go:build integration

package gl

// fw1_09e_integration_test.go, FW1-09e: the close pre flight's STOCK tie reads both sides at the
// same instant, so a month that ties at its end is never refused because a LATER month moved the
// stock.
//
// THE DEFECT (FROM-PRINCIPAL 326, the principal checked it at source on main): runClosePreFlight
// tied StockValueCents (TODAY's stock) against AccountBalanceAsOf("1200", nextMonth) (the month's
// END). Closing October on the third of November therefore failed after the first November sale or
// receipt moved the stock ledger and 1200 together: the stock side read November's figure and the
// GL side read October's, and the difference was the movement.
//
// THE LEGS.
//
//  1. The red: the month ties at its end, a NEXT month's movement moves the stock ledger AND 1200
//     together through the production seams, and the close of the earlier month on the next month's
//     third day must seal it.
//  2. The honest guard: a real out of tie (1200 moved with no stock movement) is still refused. That
//     leg is green today and must stay green: it goes red only if the tie check stops refusing.
//  3. The wording: the refusal says what it compares.
//
// THE WORLD. The gate database carries the seeded world's stock with no inventory opening journal
// (the month walk and FW1-09d's leg post that opening themselves), so the leg posts it too, through
// the GL's own posting path, dated in the month it closes. The leg's own product carries ten units
// at a thousand cents a unit, so the November movement moves the stock ledger and 1200 by exactly
// two thousand cents, to the cent.
//
//	Run: DATABASE_URL='postgres://.../integration_gate?sslmode=disable' go test -tags integration -count=1 -run FW1_09e ./internal/gl/

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

const (
	// fw109eYear is the leg's fiscal year: outside every other leg's band in this package, and below
	// the 3074 scrub the round 4 legs run.
	fw109eYear = 2125
	// fw109eMonth is the month the leg closes.
	fw109eMonth = 10
	// fw109eUnits is the leg's own product's on hand, fw109eUnitCost its weighted average cost, and
	// fw109eSold how many units the next month's movement sells.
	fw109eUnits    = 10
	fw109eUnitCost = 1000
	fw109eSold     = 2
)

// fw109eInstants are the leg's four instants, in the dealer's own zone (the store's BusinessZone,
// never a hard coded default): the opening balance lands in the middle of the month the leg closes,
// the movement lands in the NEXT month on its third, the close runs on that same third, and the
// month's end is the first instant of the next month, the instant the AR tie reads.
func fw109eInstants(t *testing.T, pool *pgxpool.Pool) (openAt, moveAt, closeAt, monthEnd time.Time) {
	t.Helper()
	zone, err := NewRepository(pool).BusinessZone(context.Background())
	if err != nil {
		t.Fatalf("the dealer's business zone: %v", err)
	}
	loc := zone.Location()
	monthStart := time.Date(fw109eYear, fw109eMonth, 1, 0, 0, 0, 0, loc)
	return time.Date(fw109eYear, fw109eMonth, 15, 12, 0, 0, 0, loc),
		time.Date(fw109eYear, fw109eMonth+1, 3, 9, 0, 0, 0, loc),
		time.Date(fw109eYear, fw109eMonth+1, 3, 12, 0, 0, 0, loc),
		monthStart.AddDate(0, 1, 0)
}

// fw109eCaller is the dealer wide caller the close runs under.
func fw109eCaller(user string) identity.StaffContext {
	return identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}
}

// fw109eStockReader is the close pre flight's stock seam over inventory (the production read the
// composition root wires): the company's stock value, weighted average, in integer cents.
type fw109eStockReader struct{ pool *pgxpool.Pool }

func (r fw109eStockReader) StockValueCents(ctx context.Context, branchID string) (int64, error) {
	return inventory.NewService(inventory.NewRepository(r.pool)).StockValueCents(ctx, branchID)
}

// fw109eStockValue reads the company's stock value through that same production read.
func fw109eStockValue(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	return fw109eStockReader{pool: pool}.StockValueCents(ctx, "")
}

// fw109eWorld is the leg's world: the branch it trades at, its own product, and the figures the
// stock tie compares.
type fw109eWorld struct {
	branchID   string
	productID  string
	stockCents int64
	gl1200     int64
}

// fw109eSetup builds the leg's world and ties the books at the month's end: the company's stock
// value against the company's 1200. The seeded world carries stock with no inventory opening
// journal, so the leg posts that opening itself, through the GL's own posting path, dated in the
// month it closes (the month walk and FW1-09d's leg post the same opening).
func fw109eSetup(t *testing.T, pool *pgxpool.Pool, openAt, monthEnd time.Time) *fw109eWorld {
	t.Helper()
	ctx := context.Background()
	w := &fw109eWorld{}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type = 'BRANCH' ORDER BY id LIMIT 1`).Scan(&w.branchID); err != nil {
		t.Fatalf("the seeded branch: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO products (sku, name, base_uom, price_uom, weight_per_unit_kg)
		VALUES ('FW1-09E-' || substr(md5(random()::text), 1, 8), 'FW1-09e leg product', 'EA', 'EA', 2.0)
		RETURNING id::text`).Scan(&w.productID); err != nil {
		t.Fatalf("the leg's product: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM stock_moves WHERE product_id = $1`, w.productID)
		_, _ = pool.Exec(c, `DELETE FROM stock_balances WHERE product_id = $1`, w.productID)
		_, _ = pool.Exec(c, `DELETE FROM product_branch_costs WHERE product_id = $1`, w.productID)
		_, _ = pool.Exec(c, `DELETE FROM products WHERE id = $1`, w.productID)
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base)
		VALUES ($1, $2, $2, $3)`, w.productID, w.branchID, fw109eUnits); err != nil {
		t.Fatalf("the leg's stock: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents)
		VALUES ($1, $2, $3)`, w.productID, w.branchID, fw109eUnitCost); err != nil {
		t.Fatalf("the leg's branch cost: %v", err)
	}
	// The two figures the tie compares, read through the production reads: the company's stock value
	// (inventory's consolidated weighted average, the read the close pre flight's stock seam is
	// wired to) and the company's 1200 (the GL's own as of read).
	stock, err := fw109eStockValue(ctx, pool)
	if err != nil {
		t.Fatalf("the company's stock value: %v", err)
	}
	gl, _, err := NewRepository(pool).AccountBalanceAsOf(ctx, "1200", monthEnd, "")
	if err != nil {
		t.Fatalf("the company's 1200 at the month's end: %v", err)
	}
	w.stockCents, w.gl1200 = stock, gl
	// The opening journal: the company's 1200 against the stock the shelves hold. Its size is
	// whatever the seeded world's stock is worth less the 1200 the books already carry, so the month
	// ties at its end.
	if delta := stock - gl; delta != 0 {
		amount := delta
		if amount < 0 {
			amount = -amount
		}
		lines := []ExternalLine{
			{AccountCode: "1200", DebitCents: amount, Memo: "opening stock"},
			{AccountCode: "3000", CreditCents: amount, Memo: "opening equity"},
		}
		if delta < 0 {
			lines = []ExternalLine{
				{AccountCode: "1200", CreditCents: amount, Memo: "opening stock"},
				{AccountCode: "3000", DebitCents: amount, Memo: "opening equity"},
			}
		}
		svc := svcAt(pool, fw109eCaller(aUser(t, pool)), openAt)
		if _, err := svc.PostExternal(ctx, ExternalJournal{
			Source:         SourceAdjustment,
			IdempotencyKey: "fw1-09e:opening:" + w.productID,
			Memo:           "opening stock",
			Lines:          lines,
		}); err != nil {
			t.Fatalf("the opening stock journal: %v", err)
		}
	}
	return w
}

// fw109eMove is the NEXT month's movement, through the production seams: inventory's fulfilment
// writes the stock ledger and emits order.fulfilled, and the GL's own posting consumer takes that
// event and posts the COGS that relieves 1200. The event goes to the consumer directly rather than
// through a shared outbox drain, because the gate database carries pending rows from other
// packages (the seed's own invoice events) and this leg's clock would date those rows into the
// leg's year.
func fw109eMove(t *testing.T, pool *pgxpool.Pool, w *fw109eWorld, moveAt time.Time) {
	t.Helper()
	ctx := context.Background()
	// The sale's own document.
	var customerID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM customers ORDER BY id LIMIT 1`).Scan(&customerID); err != nil {
		t.Fatalf("a seeded customer: %v", err)
	}
	var orderID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		VALUES ('FW1-09E-' || substr(md5(random()::text), 1, 8), $1, $2, 'FULFILLED', 'PICKUP', $3, $3)
		RETURNING id::text`, customerID, w.branchID, fw109eSold*fw109eUnitCost).Scan(&orderID); err != nil {
		t.Fatalf("the sale: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM orders WHERE id = $1`, orderID)
	})
	// Inventory's fulfilment: the stock ledger moves and the event goes out.
	inv := inventory.NewService(inventory.NewRepository(pool))
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the fulfilment: %v", err)
	}
	if err := inv.FulfilOrderLinesTx(ctx, tx, orderID, w.branchID, []string{w.productID}, []float64{fw109eSold}); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("the fulfilment: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the fulfilment: %v", err)
	}
	// The GL's own posting consumer takes the event the movement emitted.
	var eventID string
	var payload []byte
	if err := pool.QueryRow(ctx, `
		SELECT event_id::text, payload FROM events_outbox
		 WHERE topic = 'order.fulfilled' AND payload->>'order_id' = $1`, orderID).Scan(&eventID, &payload); err != nil {
		t.Fatalf("the movement's event: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM events_outbox WHERE event_id = $1::uuid`, eventID)
	})
	gl := NewService(NewRepository(pool)).
		withClock(func() time.Time { return moveAt }).
		WithCostBasis(inv)
	if err := gl.postEvent(ctx, notify.Event{EventID: eventID, Topic: "order.fulfilled", Payload: payload}); err != nil {
		t.Fatalf("the GL's posting consumer: %v", err)
	}
}

// fw109eBookOnlyMovement is a real out of tie: 1200 moves, the shelves do not. It lands in the month
// the leg closes, so both the month's end read and the same instant read see it.
func fw109eBookOnlyMovement(t *testing.T, pool *pgxpool.Pool, w *fw109eWorld, openAt time.Time) {
	t.Helper()
	svc := svcAt(pool, fw109eCaller(aUser(t, pool)), openAt)
	if _, err := svc.PostExternal(context.Background(), ExternalJournal{
		Source:         SourceAdjustment,
		IdempotencyKey: "fw1-09e:out-of-tie:" + w.productID,
		Memo:           "a book only stock write down",
		Lines: []ExternalLine{
			{AccountCode: "5000", DebitCents: fw109eSold * fw109eUnitCost, Memo: "stock write down"},
			{AccountCode: "1200", CreditCents: fw109eSold * fw109eUnitCost, Memo: "stock write down"},
		},
	}); err != nil {
		t.Fatalf("the book only movement: %v", err)
	}
}

// TestIntegration_FW1_09e_TheStockTieReadsBothSidesAtTheSameInstant is the red: a month that ties
// at its end, a NEXT month's movement that moves the stock ledger and 1200 together, and the close
// of the earlier month on the next month's third day. Today the close is refused with the stock out
// of tie, because the stock side reads TODAY's figure while the GL side reads the month's end.
func TestIntegration_FW1_09e_TheStockTieReadsBothSidesAtTheSameInstant(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, fw109eYear)
	openAt, moveAt, closeAt, monthEnd := fw109eInstants(t, pool)
	w := fw109eSetup(t, pool, openAt, monthEnd)

	// The month ties at its end: the company's stock against the company's 1200.
	stock, err := fw109eStockValue(ctx, pool)
	if err != nil {
		t.Fatalf("the company's stock value: %v", err)
	}
	if stock != w.stockCents {
		t.Fatalf("the company's stock value moved from %d to %d before the leg's movement", w.stockCents, stock)
	}
	gl1200, _, err := NewRepository(pool).AccountBalanceAsOf(ctx, "1200", monthEnd, "")
	if err != nil {
		t.Fatalf("the company's 1200 at the month's end: %v", err)
	}
	if gl1200 != w.stockCents {
		t.Fatalf("the month does not tie at its end: the stock ledger holds %d and 1200 holds %d", w.stockCents, gl1200)
	}

	// The NEXT month's movement: the stock ledger and 1200 move together.
	fw109eMove(t, pool, w, moveAt)
	after, err := fw109eStockValue(ctx, pool)
	if err != nil {
		t.Fatalf("the company's stock value after the movement: %v", err)
	}
	if want := w.stockCents - fw109eSold*fw109eUnitCost; after != want {
		t.Fatalf("the stock ledger holds %d after the movement, want %d (the shelves lost the sale)", after, want)
	}
	glNow, _, err := NewRepository(pool).AccountBalanceAsOf(ctx, "1200", moveAt, "")
	if err != nil {
		t.Fatalf("the company's 1200 after the movement: %v", err)
	}
	if glNow != after {
		t.Fatalf("the movement moved the stock ledger to %d and 1200 to %d; they must move together", after, glNow)
	}

	// The close of the earlier month, on the next month's third day.
	closer := svcAt(pool, fw109eCaller(user), closeAt).WithStockReader(fw109eStockReader{pool: pool})
	if _, err := closer.ClosePeriod(ctx, periodID(t, pool, fw109eYear, fw109eMonth), "FW1-09e close"); err != nil {
		t.Fatalf("the close of the month that ties at its end was refused after the NEXT month's movement moved the stock ledger and 1200 together: %v", err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM fiscal_periods WHERE year = $1 AND month = $2`, fw109eYear, fw109eMonth).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "CLOSED" {
		t.Fatalf("the period reads %s after the close, want CLOSED", status)
	}
}

// TestIntegration_FW1_09e_ARealOutOfTieIsStillRefused is the honest guard: a real out of tie (1200
// moved with no stock movement) is still refused by the tie. This leg is green today and must stay
// green: it goes red only if the tie check stops refusing.
func TestIntegration_FW1_09e_ARealOutOfTieIsStillRefused(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, fw109eYear)
	openAt, _, closeAt, monthEnd := fw109eInstants(t, pool)
	w := fw109eSetup(t, pool, openAt, monthEnd)
	fw109eBookOnlyMovement(t, pool, w, openAt)

	closer := svcAt(pool, fw109eCaller(user), closeAt).WithStockReader(fw109eStockReader{pool: pool})
	_, err := closer.ClosePeriod(ctx, periodID(t, pool, fw109eYear, fw109eMonth), "FW1-09e out of tie proof")
	if err == nil {
		t.Fatalf("the month closed although 1200 moved with no stock movement; the tie must refuse a real discrepancy")
	}
	msg := err.Error()
	for _, want := range []string{
		"stock is out of tie with 1200",
		"$20.00",
		"the stock ledger holds",
		"1200 holds",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the refusal %q does not name %q", msg, want)
		}
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM fiscal_periods WHERE year = $1 AND month = $2`, fw109eYear, fw109eMonth).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "OPEN" {
		t.Fatalf("the refused close left the period %s, want OPEN", status)
	}
}

// TestIntegration_FW1_09e_TheRefusalSaysWhatItCompares is the wording: the refusal says what it
// compares and says when it read both sides.
func TestIntegration_FW1_09e_TheRefusalSaysWhatItCompares(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, fw109eYear)
	openAt, _, closeAt, monthEnd := fw109eInstants(t, pool)
	w := fw109eSetup(t, pool, openAt, monthEnd)
	fw109eBookOnlyMovement(t, pool, w, openAt)

	closer := svcAt(pool, fw109eCaller(user), closeAt).WithStockReader(fw109eStockReader{pool: pool})
	_, err := closer.ClosePeriod(ctx, periodID(t, pool, fw109eYear, fw109eMonth), "FW1-09e wording proof")
	if err == nil {
		t.Fatalf("the month closed although 1200 moved with no stock movement; the tie must refuse a real discrepancy")
	}
	msg := err.Error()
	for _, want := range []string{
		"stock is out of tie with 1200",
		"both read now",
		"the same instant",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the refusal %q does not say %q", msg, want)
		}
	}
}
