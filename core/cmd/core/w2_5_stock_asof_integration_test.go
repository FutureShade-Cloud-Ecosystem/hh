//go:build integration

package main

// w2_5_stock_asof_integration_test.go, W2-5 (finance wave 2): the close pre
// flight's stock tie reads the stock ledger AS OF THE MONTH'S END, through the
// production wiring, so a month that tied at its end seals no matter what the
// next month did to the stock, and a month that was out of tie at its end is
// refused no matter what the next month did to correct it.
//
// THE DEFECT (S5's read of PR 659, the three P3s): FW1-09e made the tie read
// BOTH sides at one instant, the service clock's now, which pins nothing at
// the month end: a later 1200 only entry, or a waiting current month
// inventory event, refuses a month that tied at its end; a later correction
// passes a month that was out of tie at its end. W2-5 gives inventory an as of
// read over its own movement ledger and the pre flight reads both sides at the
// first instant of the next month, the same basis the AR tie reads.
//
// THE LEGS (each on its own fresh migrated and seeded database, the served
// routes over the real money spine, wireMoney, wireOperations and wireTops,
// with no test double on the money path):
//
//  1. The red: the month ties at its end, the NEXT month's movement moves the
//     stock ledger and 1200 together through the production seams, and the
//     close of the earlier month seals it. Today the close is refused with a
//     false variance, because the stock side reads the figure after the
//     movement and the GL side reads the instant before it.
//  2. S5's P3 2: the month was out of tie at its end (a book only write down
//     inside the month) and the next month corrected it; the close of the
//     earlier month is still refused, naming the month's own discrepancy.
//  3. S5's P3 3: a book only movement in the NEXT month belongs to the next
//     month: the close of the earlier month passes, and the close of the next
//     month is refused, naming the book only figure.
//
// THE WORLD. Every leg lives in a year of its own (far ahead of the seeded
// world's clocks) so the fiction is reachable only through document dates and
// the movement ledger's own instants: the opening lands inside the month the
// leg closes, the movement and the corrections land in the next month, and
// the movement's move rows carry the next month's instant the way a real next
// month movement would (the production fulfilment stamps the wall clock,
// which cannot reach the leg's year, so the leg aligns the rows its own
// movement wrote, the same fixture adjustment the GL side needs none of,
// because the journal posts with the document date through the GL's own
// provider seam).
//
//	Run: DATABASE_URL='postgres://.../postgres?sslmode=disable' \
//	     HH_CORE_TEST_DATABASE_URL='postgres://.../integration_core_gate?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run W25 -count=1 -v

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/config"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	coreid "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

const (
	// w25Year and w25Month are the legs' fiscal frame, a year no other leg
	// trades in. w25Units and w25UnitCost are the leg product's on hand and
	// weighted average cost, w25Sold how many units the next month's movement
	// sells.
	w25Year      = 2127
	w25Month     = 10
	w25Units     = 10
	w25UnitCost  = 1000
	w25Sold      = 2
	w25WriteDown = 2000
)

// w25Instants are the legs' instants in the dealer's own zone: the opening
// lands in the middle of the month the leg closes, the movement and the
// corrections land in the NEXT month on its third, and the month's end is the
// first instant of the next month, the instant both sides of the tie read.
func w25Instants(t *testing.T, pool *pgxpool.Pool) (openAt, moveAt, monthEnd time.Time) {
	t.Helper()
	zone, err := gl.NewRepository(pool).BusinessZone(context.Background())
	if err != nil {
		t.Fatalf("the dealer's business zone: %v", err)
	}
	loc := zone.Location()
	monthStart := time.Date(w25Year, time.Month(w25Month), 1, 0, 0, 0, 0, loc)
	return time.Date(w25Year, w25Month, 15, 12, 0, 0, 0, loc),
		time.Date(w25Year, w25Month+1, 3, 9, 0, 0, 0, loc),
		monthStart.AddDate(0, 1, 0)
}

// w25Leg is one leg's world: the wiring, the branch, the leg's own product,
// the controller's token, and the figure the stock tie compares.
type w25Leg struct {
	suffix     string
	pool       *pgxpool.Pool
	mux        *http.ServeMux
	money      *moneySpine
	orgSlug    string
	closeToken string
	branchID   string
	productID  string
	stockCents int64
}

// w25Setup builds the leg's world on a fresh migrated and seeded database:
// the production wiring over one mux, the posting queue drained, the leg's own
// product carrying its stock, and nothing else touched.
func w25Setup(t *testing.T, suffix string) *w25Leg {
	t.Helper()
	ctx := context.Background()
	leg := &w25Leg{suffix: suffix}
	leg.pool = freshMigratedDB(t, "w25"+suffix)
	body, err := seedFS.ReadFile("dibbits_seed.sql")
	if err != nil {
		t.Fatalf("reading the embedded seed: %v", err)
	}
	if _, err := leg.pool.Exec(ctx, string(body)); err != nil {
		t.Fatalf("applying the seed: %v", err)
	}
	orgID := "88a20000-0000-4000-8000-0000000000e5"
	leg.orgSlug = "w2-5-stock-asof-" + suffix
	mustExec(t, ctx, leg.pool, `INSERT INTO org (id, slug, name) VALUES ($1, $2, 'W2 5 Stock As Of Org') ON CONFLICT (id) DO NOTHING`, orgID, leg.orgSlug)
	mustExec(t, ctx, leg.pool, `UPDATE locations SET org_id = $1 WHERE loc_type = 'BRANCH'`, orgID)
	mustExec(t, ctx, leg.pool, `UPDATE users SET platform_user_id = $1 WHERE id = 'd7000000-0000-4000-9000-000000000006'`, testkit.SubSalesManager)

	sg := testkit.NewStaffGuards(t)
	sg.Harness.Membership.SetMember(testkit.SubSalesManager, leg.orgSlug, true)
	sg.Harness.Resolver.Set(leg.orgSlug, orgID)
	sg.SetAuthority(coreid.StaffAuthority{RoleID: "financial_controller", Tier: coreid.TierLeadership, Scope: coreid.ScopeGlobal})

	log := slog.New(slog.NewTextHandler(fw109dWriter{t}, nil))
	vaultKey := dashVaultKey(t)
	vault, err := payment.NewVault(vaultKey)
	if err != nil {
		t.Fatalf("the leg's vault: %v", err)
	}
	if err := (payment.NewStore(leg.pool, vault)).Configure(ctx, "run_payments", "MID-W25"+strings.ToUpper(suffix), "CAD", "", 0, false,
		map[string]string{"api_key": "w2-5-not-a-secret"}, ""); err != nil {
		t.Fatalf("configuring the leg's card rail: %v", err)
	}
	leg.mux = http.NewServeMux()
	belt := buildSmsBelt(leg.pool, log)
	leg.money = wireMoney(leg.mux, leg.pool, sg.Guards, log, "", mustRoleConfigForWire())
	ops := wireOperations(leg.mux, leg.pool, sg.Guards, log, leg.money, "w2-5-fx-ticket", belt.outbox)
	wireTops(leg.mux, leg.pool, sg.Guards, log, leg.money, ops, config.Config{PaymentVaultKey: vaultKey}, mustRoleConfigForWire(), belt)
	leg.closeToken = sg.Harness.Minter.StaffToken(t, testkit.SubSalesManager)
	w25Drain(t, leg)

	if err := leg.pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type = 'BRANCH' ORDER BY id LIMIT 1`).Scan(&leg.branchID); err != nil {
		t.Fatalf("the seeded branch: %v", err)
	}
	if err := leg.pool.QueryRow(ctx, `
		INSERT INTO products (sku, name, base_uom, price_uom, weight_per_unit_kg)
		VALUES ('W2-5-' || $1 || '-' || substr(md5(random()::text), 1, 8), 'W2-5 leg product', 'EA', 'EA', 2.0)
		RETURNING id::text`, suffix).Scan(&leg.productID); err != nil {
		t.Fatalf("the leg's product: %v", err)
	}
	mustExec(t, ctx, leg.pool, `
		INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base)
		VALUES ($1, $2, $2, $3)`, leg.productID, leg.branchID, w25Units)
	mustExec(t, ctx, leg.pool, `
		INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents)
		VALUES ($1, $2, $3)`, leg.productID, leg.branchID, w25UnitCost)
	return leg
}

// w25Drain runs the posting queue's own consumers, registered exactly as
// worker.go registers them, until nothing waits.
func w25Drain(t *testing.T, leg *w25Leg) {
	t.Helper()
	d := notify.NewDispatcher(leg.money.outboxStore, slog.New(slog.NewTextHandler(fw109dWriter{t}, nil)))
	leg.money.glSvc.RegisterPostingHandlers(d)
	leg.money.inventorySvc.RegisterPostingHandlers(d)
	for i := 0; i < 30; i++ {
		if d.DrainOnce(context.Background()) == 0 {
			return
		}
	}
	t.Fatalf("the posting queue never drained")
}

// w25Post posts one journal through the GL's own provider path.
func w25Post(t *testing.T, leg *w25Leg, j gl.ExternalJournal) string {
	t.Helper()
	id, err := leg.money.glSvc.PostExternal(context.Background(), j)
	if err != nil {
		t.Fatalf("posting %s: %v", j.IdempotencyKey, err)
	}
	return id
}

// w25TieTheAR posts the seeded open invoices that carry no journal of their
// own against 1100, through the GL's own provider path, the way FW1-09d's leg
// posts it, dated at the instant the leg names (a zero instant rides the
// clock, so the entry belongs to the running month and no earlier year).
func w25TieTheAR(t *testing.T, leg *w25Leg, at time.Time) {
	t.Helper()
	ctx := context.Background()
	arRows := fw109dBranchAmounts(t, ctx, leg.pool, `
		SELECT i.branch_id::text, COALESCE(SUM(i.balance_cents), 0)
		  FROM invoices i
		 WHERE i.status IN ('FINALIZED','PARTIALLY_PAID') AND i.balance_cents > 0
		   AND NOT EXISTS (SELECT 1 FROM gl_journals j
		                    WHERE j.source = 'INVOICE' AND j.source_ref_id = i.id AND j.status = 'POSTED')
		 GROUP BY i.branch_id ORDER BY i.branch_id`)
	for _, o := range arRows {
		w25Post(t, leg, gl.ExternalJournal{
			Source: "MANUAL", IdempotencyKey: "w2-5:opening:ar:" + leg.suffix + ":" + o.branch,
			Memo: "opening accounts receivable", BranchID: o.branch, DocumentDate: at,
			Lines: []gl.ExternalLine{
				{AccountCode: "1100", DebitCents: o.cents, Memo: "opening AR"},
				{AccountCode: "3000", CreditCents: o.cents, Memo: "opening equity"},
			},
		})
	}
}

// w25TieTheMonthAtItsEnd posts the opening journal that makes the closing
// month tie at its end: the company's stock value, read through the production
// seam, against the company's 1200 at the month's end, through the GL's own
// provider path, dated inside the month.
func w25TieTheMonthAtItsEnd(t *testing.T, leg *w25Leg, openAt, monthEnd time.Time) {
	t.Helper()
	ctx := context.Background()
	stock, err := leg.money.inventorySvc.StockValueCents(ctx, "")
	if err != nil {
		t.Fatalf("the company's stock value: %v", err)
	}
	gl1200, _, err := gl.NewRepository(leg.pool).AccountBalanceAsOf(ctx, "1200", monthEnd, "")
	if err != nil {
		t.Fatalf("the company's 1200 at the month's end: %v", err)
	}
	leg.stockCents = stock
	if delta := stock - gl1200; delta != 0 {
		amount := delta
		lines := []gl.ExternalLine{
			{AccountCode: "1200", DebitCents: amount, Memo: "opening stock"},
			{AccountCode: "3000", CreditCents: amount, Memo: "opening equity"},
		}
		if delta < 0 {
			amount = -amount
			lines = []gl.ExternalLine{
				{AccountCode: "1200", CreditCents: amount, Memo: "opening stock"},
				{AccountCode: "3000", DebitCents: amount, Memo: "opening equity"},
			}
		}
		w25Post(t, leg, gl.ExternalJournal{
			Source: "MANUAL", IdempotencyKey: "w2-5:opening:" + leg.suffix,
			Memo: "opening stock", BranchID: leg.branchID, DocumentDate: openAt,
			Lines: lines,
		})
	}
}

// w25BookOnly writes 1200 with no stock movement, through the GL's own
// provider path, dated at the instant the leg names. crediting1200 false
// writes the compensating entry, the correction leg.
func w25BookOnly(t *testing.T, leg *w25Leg, at time.Time, key, memo string, crediting1200 bool) {
	t.Helper()
	lines := []gl.ExternalLine{
		{AccountCode: "5200", DebitCents: w25WriteDown, Memo: memo},
		{AccountCode: "1200", CreditCents: w25WriteDown, Memo: memo},
	}
	if !crediting1200 {
		lines = []gl.ExternalLine{
			{AccountCode: "1200", DebitCents: w25WriteDown, Memo: memo},
			{AccountCode: "5200", CreditCents: w25WriteDown, Memo: memo},
		}
	}
	w25Post(t, leg, gl.ExternalJournal{
		Source: "MANUAL", IdempotencyKey: key, Memo: memo, BranchID: leg.branchID, DocumentDate: at,
		Lines: lines,
	})
}

// w25Move is the NEXT month's movement through the production seams: the
// order's fulfilment writes the stock ledger (the SHIP move) and emits
// order.fulfilled, and the GL's own provider path posts the COGS that
// relieves 1200, dated in the next month. The move rows the fulfilment wrote
// carry the wall clock, which cannot reach the leg's year, so the leg stamps
// them with the movement's own instant; the journal rides the document date,
// so both sides of the tie cut the movement at the same instant.
func w25Move(t *testing.T, leg *w25Leg, moveAt time.Time) {
	t.Helper()
	ctx := context.Background()
	var customerID string
	if err := leg.pool.QueryRow(ctx, `SELECT id::text FROM customers ORDER BY id LIMIT 1`).Scan(&customerID); err != nil {
		t.Fatalf("a seeded customer: %v", err)
	}
	var orderID string
	if err := leg.pool.QueryRow(ctx, `
		INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		VALUES ('W2-5-' || $1 || '-' || substr(md5(random()::text), 1, 8), $2, $3, 'FULFILLED', 'PICKUP', $4, $4)
		RETURNING id::text`, leg.suffix, customerID, leg.branchID, w25Sold*w25UnitCost).Scan(&orderID); err != nil {
		t.Fatalf("the sale: %v", err)
	}
	tx, err := leg.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the fulfilment: %v", err)
	}
	if err := leg.money.inventorySvc.FulfilOrderLinesTx(ctx, tx, orderID, leg.branchID, []string{leg.productID}, []float64{w25Sold}); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("the fulfilment: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the fulfilment: %v", err)
	}
	var moved int64
	if err := leg.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(value_cents), 0) FROM stock_moves WHERE source_ref_id = $1::uuid`, orderID).Scan(&moved); err != nil {
		t.Fatalf("the movement's own value: %v", err)
	}
	if moved != w25Sold*w25UnitCost {
		t.Fatalf("the movement moved %d cents, want %d", moved, w25Sold*w25UnitCost)
	}
	// The ledger is append-only (0037: a posted move is never mutated), and the
	// wall clock cannot reach the leg's year, so the leg re-inserts the move
	// rows its own fulfilment wrote with the movement's own instant: a DELETE
	// (allowed for fixture hygiene) and an INSERT of the same rows, occurred_at
	// apart. The journal rides the document date, so both sides of the tie cut
	// the movement at the same instant.
	w25RedateTheMovement(t, leg, orderID, moveAt)
	// The COGS journal the order.fulfilled consumer posts, through the GL's own
	// provider path, under the consumer's own idempotency key: the drain below
	// then acks the event as the duplicate it is.
	w25Post(t, leg, gl.ExternalJournal{
		Source: "MANUAL", IdempotencyKey: "order.fulfilled:" + orderID,
		Memo: "COGS on fulfillment", BranchID: leg.branchID, DocumentDate: moveAt,
		Lines: []gl.ExternalLine{
			{AccountCode: "5000", DebitCents: moved, Memo: "COGS"},
			{AccountCode: "1200", CreditCents: moved, Memo: "inventory relief"},
		},
	})
	w25Drain(t, leg)
}

// w25RedateTheMovement gives the movement's own ledger rows the movement's own instant.
//
// The ledger is append-only (0037: a posted move is never mutated) and the wall clock cannot
// reach a leg's year, so a leg re-inserts the rows its own movement wrote with the instant it
// names: a DELETE (allowed for fixture hygiene) and an INSERT of the same rows, occurred_at
// apart. Every leg that needs a movement in its own fiscal frame uses this one helper, so the
// fixture's shape is written down once.
func w25RedateTheMovement(t *testing.T, leg *w25Leg, sourceRefID string, at time.Time) {
	t.Helper()
	mustExec(t, context.Background(), leg.pool, `
		WITH gone AS (
			DELETE FROM stock_moves WHERE source_ref_id = $1::uuid RETURNING *
		)
		INSERT INTO stock_moves (id, product_id, from_location_id, to_location_id, qty_base, qty_entered,
			uom_entered, move_type, unit_cost_cents, value_cents, weight_kg_total, source_type, source_ref_id,
			reason, actor_id, branch_id, metadata, occurred_at, created_at, bin_code)
		SELECT id, product_id, from_location_id, to_location_id, qty_base, qty_entered,
			uom_entered, move_type, unit_cost_cents, value_cents, weight_kg_total, source_type, source_ref_id,
			reason, actor_id, branch_id, metadata, $2, created_at, bin_code
		FROM gone`, sourceRefID, at)
}

// w25Close closes one month through the served route, as the Financial
// Controller, and answers the status code and the body.
func w25Close(t *testing.T, leg *w25Leg, year, month int) (int, string) {
	t.Helper()
	var periodID string
	if err := leg.pool.QueryRow(context.Background(), `SELECT id::text FROM fiscal_periods WHERE year = $1 AND month = $2`, year, month).Scan(&periodID); err != nil {
		t.Fatalf("the %04d-%02d period: %v", year, month, err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/gl/periods/"+periodID+"/close",
		strings.NewReader(`{"reason":"W2-5 month close"}`))
	req.Header.Set("Authorization", "Bearer "+leg.closeToken)
	req.Header.Set(authz.HeaderXOrg, leg.orgSlug)
	req.Header.Set("Content-Type", "application/json")
	rsp := httptest.NewRecorder()
	leg.mux.ServeHTTP(rsp, req)
	return rsp.Code, rsp.Body.String()
}

// w25PeriodStatus answers one period's status.
func w25PeriodStatus(t *testing.T, leg *w25Leg, year, month int) string {
	t.Helper()
	var status string
	if err := leg.pool.QueryRow(context.Background(), `SELECT status FROM fiscal_periods WHERE year = $1 AND month = $2`, year, month).Scan(&status); err != nil {
		t.Fatalf("the %04d-%02d period status: %v", year, month, err)
	}
	return status
}

// TestW25StockTieReadsTheStockLedgerAsOfTheMonthEnd is the red: the month ties
// at its end, the NEXT month's movement moves the stock ledger and 1200
// together through the production seams, and the close of the earlier month
// seals it. On the code before this item the close is refused: the stock side
// reads the figure after the movement while the GL side reads the instant
// before it, and the refusal names a false variance.
func TestW25StockTieReadsTheStockLedgerAsOfTheMonthEnd(t *testing.T) {
	leg := w25Setup(t, "red")
	ctx := context.Background()
	mustExec(t, ctx, leg.pool, `
		INSERT INTO fiscal_periods (year, month) SELECT $1, m FROM generate_series(1,12) AS m
		 ON CONFLICT (year, month) DO NOTHING`, w25Year)
	openAt, moveAt, monthEnd := w25Instants(t, leg.pool)
	w25TieTheAR(t, leg, openAt)
	w25TieTheMonthAtItsEnd(t, leg, openAt, monthEnd)

	// The month ties at its end: the company's stock value against 1200 at the
	// month's end, both through the production reads.
	stock, err := leg.money.inventorySvc.StockValueCents(ctx, "")
	if err != nil {
		t.Fatalf("the company's stock value: %v", err)
	}
	if stock != leg.stockCents {
		t.Fatalf("the company's stock value moved from %d to %d before the leg's movement", leg.stockCents, stock)
	}
	gl1200, _, err := gl.NewRepository(leg.pool).AccountBalanceAsOf(ctx, "1200", monthEnd, "")
	if err != nil {
		t.Fatalf("the company's 1200 at the month's end: %v", err)
	}
	if gl1200 != leg.stockCents {
		t.Fatalf("the month does not tie at its end: the stock ledger holds %d and 1200 holds %d", leg.stockCents, gl1200)
	}

	// The NEXT month's movement: the stock ledger and 1200 move together.
	w25Move(t, leg, moveAt)
	after, err := leg.money.inventorySvc.StockValueCents(ctx, "")
	if err != nil {
		t.Fatalf("the company's stock value after the movement: %v", err)
	}
	if want := leg.stockCents - w25Sold*w25UnitCost; after != want {
		t.Fatalf("the stock ledger holds %d after the movement, want %d (the shelves lost the sale)", after, want)
	}
	glNow, _, err := gl.NewRepository(leg.pool).AccountBalanceAsOf(ctx, "1200", moveAt, "")
	if err != nil {
		t.Fatalf("the company's 1200 after the movement: %v", err)
	}
	if glNow != after {
		t.Fatalf("the movement moved the stock ledger to %d and 1200 to %d; they must move together", after, glNow)
	}

	// The close of the earlier month, after the next month's movement.
	code, body := w25Close(t, leg, w25Year, w25Month)
	if code != http.StatusOK {
		t.Fatalf("the close of the month that tied at its end was refused after the NEXT month's movement moved the stock ledger and 1200 together: %d: %s", code, body)
	}
	if status := w25PeriodStatus(t, leg, w25Year, w25Month); status != "CLOSED" {
		t.Fatalf("the period reads %s after the close, want CLOSED", status)
	}
}

// TestW25AMonthOutOfTieAtItsEndStaysRefusedDespiteALaterCorrection is S5's
// P3 2: the month was out of tie at its end, the next month corrected the
// difference, and the close of the earlier month is still refused, naming the
// discrepancy the month itself carries.
func TestW25AMonthOutOfTieAtItsEndStaysRefusedDespiteALaterCorrection(t *testing.T) {
	leg := w25Setup(t, "refuse")
	ctx := context.Background()
	mustExec(t, ctx, leg.pool, `
		INSERT INTO fiscal_periods (year, month) SELECT $1, m FROM generate_series(1,12) AS m
		 ON CONFLICT (year, month) DO NOTHING`, w25Year)
	openAt, moveAt, monthEnd := w25Instants(t, leg.pool)
	w25TieTheAR(t, leg, openAt)
	w25TieTheMonthAtItsEnd(t, leg, openAt, monthEnd)

	// The month is out of tie at its end: 1200 moves with no stock movement,
	// inside the month the leg closes.
	w25BookOnly(t, leg, openAt, "w2-5:refuse:write-down", "a book only stock write down", true)
	// The next month corrects the difference, so the book ties again today.
	w25BookOnly(t, leg, moveAt, "w2-5:refuse:correction", "the correction the next month posts", false)
	glNow, _, err := gl.NewRepository(leg.pool).AccountBalanceAsOf(ctx, "1200", moveAt, "")
	if err != nil {
		t.Fatalf("the company's 1200 after the correction: %v", err)
	}
	if glNow != leg.stockCents {
		t.Fatalf("the book does not tie after the correction: 1200 holds %d, the stock ledger holds %d", glNow, leg.stockCents)
	}

	// The close of the month that was out of tie at its end: refused, naming
	// the month's own discrepancy, not the corrected present.
	code, body := w25Close(t, leg, w25Year, w25Month)
	if code != http.StatusConflict {
		t.Fatalf("the close of the month that was out of tie at its end answered %d, want 409: %s", code, body)
	}
	for _, want := range []string{"stock is out of tie with 1200", "$20.00"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the refusal %q does not name %q", body, want)
		}
	}
	if status := w25PeriodStatus(t, leg, w25Year, w25Month); status != "OPEN" {
		t.Fatalf("the refused close left the period %s, want OPEN", status)
	}
}

// TestW25ABookOnlyMovementInTheNextMonthBelongsToTheNextMonth is S5's P3 3: a
// book only movement in the NEXT month refuses no earlier month, and the next
// month's own close is refused by it.
func TestW25ABookOnlyMovementInTheNextMonthBelongsToTheNextMonth(t *testing.T) {
	leg := w25Setup(t, "nextmo")
	ctx := context.Background()
	mustExec(t, ctx, leg.pool, `
		INSERT INTO fiscal_periods (year, month) SELECT $1, m FROM generate_series(1,12) AS m
		 ON CONFLICT (year, month) DO NOTHING`, w25Year)
	openAt, moveAt, monthEnd := w25Instants(t, leg.pool)
	w25TieTheAR(t, leg, openAt)
	w25TieTheMonthAtItsEnd(t, leg, openAt, monthEnd)

	// The book only movement lands in the NEXT month.
	w25BookOnly(t, leg, moveAt, "w2-5:nextmo:write-down", "a book only stock write down in the next month", true)

	// The earlier month tied at its end: the next month's book only movement
	// is no reason to refuse it.
	code, body := w25Close(t, leg, w25Year, w25Month)
	if code != http.StatusOK {
		t.Fatalf("the close of the month that tied at its end was refused by the NEXT month's book only movement: %d: %s", code, body)
	}
	if status := w25PeriodStatus(t, leg, w25Year, w25Month); status != "CLOSED" {
		t.Fatalf("the period reads %s after the close, want CLOSED", status)
	}

	// The next month's own close is refused by the book only movement it
	// carries.
	code, body = w25Close(t, leg, w25Year, w25Month+1)
	if code != http.StatusConflict {
		t.Fatalf("the close of the month that carries the book only movement answered %d, want 409: %s", code, body)
	}
	for _, want := range []string{"stock is out of tie with 1200", "$20.00"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the refusal %q does not name %q", body, want)
		}
	}
	if status := w25PeriodStatus(t, leg, w25Year, w25Month+1); status != "OPEN" {
		t.Fatalf("the refused close left the period %s, want OPEN", status)
	}
}
