//go:build integration

package main

// w2_5b_landed_cost_integration_test.go, W2-5B (finance wave 2): the close pre
// flight's stock tie reads the stock ledger AS OF THE MONTH'S END, and a landed
// cost that lands in a LATER month must not refuse the earlier month that tied
// at its end.
//
// THE DEFECT (W2-5, the stock tie's own read): W2-5 gave inventory an as of
// read over its movement ledger, so the tie unwinds every movement that
// happened AFTER the month's end. A landed cost capitalization is not a
// movement: it raises the weighted average cost, and with it the stock
// ledger's value, and it writes NO stock move row of its own. The stock side
// of the tie therefore read the month's end as if the capitalization had
// already been inside it, while 1200 (the GL side) read the month as it
// stood before the journal the capitalization posted. A landed cost applied
// in the NEXT month refused the earlier month by the absorbed amount, and a
// landed cost VOIDED in the next month refused it by the restored amount.
//
// THE FIX (W2-5B): inventory, which owns stock_moves, records a VALUE ONLY
// move for each capitalization, retro capitalization and restore, carrying the
// exact cents the GL posting books for that event, at the SAME instant the GL
// journal for that event carries (the landed cost's own clock, pinned through
// the service's own WithClock setter). The as of read then unwinds it, so the
// stock side and 1200 cut the event together.
//
// THE LEGS (each on its own fresh migrated and seeded database, the served
// routes over the real money spine, wireMoney, wireOperations and wireTops,
// with no test double on the money path):
//
//  1. The control: the landed cost lands in the month the leg closes; the
//     month ties at its end and the close seals it. It ties today and must
//     still tie.
//  2. The red: the landed cost lands in the NEXT month; the close of the
//     earlier month is refused today, naming the absorbed amount.
//  3. The red: the landed cost is applied in the month and VOIDED in the next;
//     the close of the earlier month is refused today, naming the restored
//     amount.
//  4. The red: a retro capitalization lands in the next month; the close of
//     the earlier month is refused today, naming the absorbed amount.
//  5. The addendum's red: the month is out of tie at its end (the stock
//     ledger 500 BELOW 1200) by exactly the later apply's amount, and an
//     apply lands in the next month. The as of read answers the stock
//     ledger's CURRENT value, so the next month's capitalization is inside
//     the earlier month's end reading, the two sides agree, and the close of
//     the earlier month PASSES today. It must refuse, naming the month's own
//     variance.
//  6. The addendum's red, the sign reversed: the month is out of tie at its
//     end (the stock ledger 500 ABOVE 1200) and a void lands in the next
//     month. The as of read swallows the next month's reversal, the two
//     sides agree, and the close of the earlier month PASSES today. It must
//     refuse, naming the month's own variance.
//
// THE WORLD. Every leg lives in a year of its own (far ahead of the seeded
// world's clocks), so the fiction is reachable only through document dates and
// the landed cost's own clock: the receipt lands before the month the leg
// closes, the landed cost lands in the month or in the next one, and the
// month's end is the first instant of the next month, the instant both sides
// of the tie read.
//
//	Run: DATABASE_URL='postgres://.../postgres?sslmode=disable' \
//	     HH_CORE_TEST_DATABASE_URL='postgres://.../integration_core_gate?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run W25B -count=1 -v

import (
	"context"
	"encoding/json"
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
	// w25bYear and w25bMonth are the legs' fiscal frame, a year no other leg
	// trades in. w25bUnits and w25bUnitCost are the leg product's received
	// on hand and its unit cost, w25bFreight the landed cost the PO carries.
	w25bYear     = 2127
	w25bMonth    = 10
	w25bUnits    = 10
	w25bUnitCost = 1000
	w25bFreight  = 500
	// w25bSold is how many units the retro leg sells before the late freight
	// lands, so the retro has a sold through portion to expense.
	w25bSold = 4
)

// w25bInstants are the legs' instants in the dealer's own zone: the opening
// lands in the middle of the month the leg closes, the landed cost lands
// inside that month or in the NEXT one on its third, and the month's end is
// the first instant of the next month, the instant both sides of the tie read.
func w25bInstants(t *testing.T, pool *pgxpool.Pool) (openAt, inMonth, nextMonth, monthEnd time.Time) {
	t.Helper()
	zone, err := gl.NewRepository(pool).BusinessZone(context.Background())
	if err != nil {
		t.Fatalf("the dealer's business zone: %v", err)
	}
	loc := zone.Location()
	monthStart := time.Date(w25bYear, time.Month(w25bMonth), 1, 0, 0, 0, 0, loc)
	return time.Date(w25bYear, w25bMonth, 15, 12, 0, 0, 0, loc),
		time.Date(w25bYear, w25bMonth, 20, 9, 0, 0, 0, loc),
		time.Date(w25bYear, w25bMonth+1, 3, 9, 0, 0, 0, loc),
		monthStart.AddDate(0, 1, 0)
}

// w25bLeg is one leg's world: the wiring, the branch, the leg's own product
// and the PO it arrived on, the controller's token, and the figure the stock
// tie compares.
type w25bLeg struct {
	suffix     string
	pool       *pgxpool.Pool
	mux        *http.ServeMux
	money      *moneySpine
	ops        *operationsSpine
	orgSlug    string
	closeToken string
	branchID   string
	productID  string
	poID       string
	poLineID   string
	stockCents int64
}

// w25bSetup builds the leg's world on a fresh migrated and seeded database:
// the production wiring over one mux, the posting queue drained, the leg's own
// product received against its own PO through the production receiving seam,
// and nothing else touched.
func w25bSetup(t *testing.T, suffix string) *w25bLeg {
	t.Helper()
	ctx := context.Background()
	leg := &w25bLeg{suffix: suffix}
	leg.pool = freshMigratedDB(t, "w25b"+suffix)
	body, err := seedFS.ReadFile("dibbits_seed.sql")
	if err != nil {
		t.Fatalf("reading the embedded seed: %v", err)
	}
	if _, err := leg.pool.Exec(ctx, string(body)); err != nil {
		t.Fatalf("applying the seed: %v", err)
	}
	orgID := "88a20000-0000-4000-8000-0000000000b5"
	leg.orgSlug = "w2-5b-landed-cost-" + suffix
	mustExec(t, ctx, leg.pool, `INSERT INTO org (id, slug, name) VALUES ($1, $2, 'W2 5B Landed Cost Org') ON CONFLICT (id) DO NOTHING`, orgID, leg.orgSlug)
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
	if err := (payment.NewStore(leg.pool, vault)).Configure(ctx, "run_payments", "MID-W25B"+strings.ToUpper(suffix), "CAD", "", 0, false,
		map[string]string{"api_key": "w2-5b-not-a-secret"}, ""); err != nil {
		t.Fatalf("configuring the leg's card rail: %v", err)
	}
	leg.mux = http.NewServeMux()
	belt := buildSmsBelt(leg.pool, log)
	leg.money = wireMoney(leg.mux, leg.pool, sg.Guards, log, "", mustRoleConfigForWire())
	leg.ops = wireOperations(leg.mux, leg.pool, sg.Guards, log, leg.money, "w2-5b-fx-ticket", belt.outbox)
	wireTops(leg.mux, leg.pool, sg.Guards, log, leg.money, leg.ops, config.Config{PaymentVaultKey: vaultKey}, mustRoleConfigForWire(), belt)
	leg.closeToken = sg.Harness.Minter.StaffToken(t, testkit.SubSalesManager)

	// The leg's fiscal frame, planted BEFORE anything posts, so every journal
	// the leg writes files into a period of its own year.
	mustExec(t, ctx, leg.pool, `
		INSERT INTO fiscal_periods (year, month) SELECT $1, m FROM generate_series(1,12) AS m
		 ON CONFLICT (year, month) DO NOTHING`, w25bYear)
	w25bDrain(t, leg)

	if err := leg.pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type = 'BRANCH' ORDER BY id LIMIT 1`).Scan(&leg.branchID); err != nil {
		t.Fatalf("the seeded branch: %v", err)
	}
	if err := leg.pool.QueryRow(ctx, `
		INSERT INTO products (sku, name, base_uom, price_uom, weight_per_unit_kg)
		VALUES ('W2-5B-' || $1 || '-' || substr(md5(random()::text), 1, 8), 'W2-5B leg product', 'EA', 'EA', 2.0)
		RETURNING id::text`, suffix).Scan(&leg.productID); err != nil {
		t.Fatalf("the leg's product: %v", err)
	}
	w25bOpenThePO(t, leg)
	return leg
}

// w25bOpenThePO opens the leg's PO and RECEIVES the leg's product against it
// through the production receiving seam (the wired inventory service's own
// ReceivePOTx), so the PO's received line, the stock ledger's receipt move,
// the balance and the weighted average cost are all the production path's own
// writes. The receipt's move row carries the wall clock, which is before the
// leg's month's end, so the month's end reads it on both sides of the tie.
func w25bOpenThePO(t *testing.T, leg *w25bLeg) {
	t.Helper()
	ctx := context.Background()
	var vendorID string
	if err := leg.pool.QueryRow(ctx, `SELECT id::text FROM vendors ORDER BY id LIMIT 1`).Scan(&vendorID); err != nil {
		t.Fatalf("a seeded vendor: %v", err)
	}
	if err := leg.pool.QueryRow(ctx, `
		INSERT INTO purchase_orders (po_no, vendor_id, branch_id, status, currency, fx_rate)
		VALUES ('W2-5B-' || $1 || '-' || substr(md5(random()::text), 1, 8), $2, $3, 'SUBMITTED', 'CAD', 1.0)
		RETURNING id::text`, leg.suffix, vendorID, leg.branchID).Scan(&leg.poID); err != nil {
		t.Fatalf("the leg's PO: %v", err)
	}
	if err := leg.pool.QueryRow(ctx, `
		INSERT INTO po_lines (po_id, line_no, product_id, qty_entered, uom_entered, qty_base, unit_cost_cents, cost_uom)
		VALUES ($1, 1, $2, $3, 'EA', $3, $4, 'EA')
		RETURNING id::text`, leg.poID, leg.productID, w25bUnits, w25bUnitCost).Scan(&leg.poLineID); err != nil {
		t.Fatalf("the leg's PO line: %v", err)
	}
	tx, err := leg.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the receipt: %v", err)
	}
	if err := leg.money.inventorySvc.ReceivePOTx(ctx, tx, leg.poID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("the receipt: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the receipt: %v", err)
	}
	w25bDrain(t, leg)
}

// w25bDrain runs the posting queue's own consumers, registered exactly as
// worker.go registers them, until nothing waits.
func w25bDrain(t *testing.T, leg *w25bLeg) {
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

// w25bPost posts one journal through the GL's own provider path.
func w25bPost(t *testing.T, leg *w25bLeg, j gl.ExternalJournal) string {
	t.Helper()
	id, err := leg.money.glSvc.PostExternal(context.Background(), j)
	if err != nil {
		t.Fatalf("posting %s: %v", j.IdempotencyKey, err)
	}
	return id
}

// w25bTieTheAR posts the seeded open invoices that carry no journal of their
// own against 1100, through the GL's own provider path, dated at the instant
// the leg names.
func w25bTieTheAR(t *testing.T, leg *w25bLeg, at time.Time) {
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
		w25bPost(t, leg, gl.ExternalJournal{
			Source: "MANUAL", IdempotencyKey: "w2-5b:opening:ar:" + leg.suffix + ":" + o.branch,
			Memo: "opening accounts receivable", BranchID: o.branch, DocumentDate: at,
			Lines: []gl.ExternalLine{
				{AccountCode: "1100", DebitCents: o.cents, Memo: "opening AR"},
				{AccountCode: "3000", CreditCents: o.cents, Memo: "opening equity"},
			},
		})
	}
}

// w25bTieTheMonthAtItsEnd posts the opening journal that makes the closing
// month tie at its end: the company's stock value, read through the production
// seam, against the company's 1200 at the month's end, through the GL's own
// provider path, dated inside the month.
func w25bTieTheMonthAtItsEnd(t *testing.T, leg *w25bLeg, openAt, monthEnd time.Time) {
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
		w25bPost(t, leg, gl.ExternalJournal{
			Source: "MANUAL", IdempotencyKey: "w2-5b:opening:" + leg.suffix,
			Memo: "opening stock", BranchID: leg.branchID, DocumentDate: openAt,
			Lines: lines,
		})
	}
}

// w25bPlantTheMonthEndVariance plants the month end discrepancy the addendum
// describes: a month that does NOT tie at its end, by exactly the variance the
// leg names, written to 1200 with no stock movement at all, dated inside the
// month the leg closes, through the GL's own provider path.
//
// The variance is the figure the close pre flight reports: the stock ledger's
// value at the month's end MINUS 1200's balance at the month's end. A
// positive variance means the stock ledger is ABOVE 1200, so 1200 comes down
// by it; a negative one means 1200 is above the stock ledger, so 1200 goes up
// by it.
//
// The world is planted, not accidental: a world that ties cannot be out of
// tie, and these legs are about what a close does with a month that does not
// tie. The journal moves no stock, so the stock side of the tie reads the
// figure it read before it, and the month's own variance is the only thing at
// stake.
func w25bPlantTheMonthEndVariance(t *testing.T, leg *w25bLeg, at time.Time, variance int64, key string) {
	t.Helper()
	amount := variance
	if amount < 0 {
		amount = -amount
	}
	lines := []gl.ExternalLine{
		{AccountCode: "1200", DebitCents: amount, Memo: "the month end variance"},
		{AccountCode: "3000", CreditCents: amount, Memo: "the month end variance"},
	}
	if variance > 0 {
		// The stock ledger is ABOVE 1200 by the variance, so 1200 comes down.
		lines = []gl.ExternalLine{
			{AccountCode: "3000", DebitCents: amount, Memo: "the month end variance"},
			{AccountCode: "1200", CreditCents: amount, Memo: "the month end variance"},
		}
	}
	w25bPost(t, leg, gl.ExternalJournal{
		Source: "MANUAL", IdempotencyKey: key, Memo: "the month end variance",
		BranchID: leg.branchID, DocumentDate: at, Lines: lines,
	})
}

// w25bPostJSON posts one body to a served route as the controller and answers
// the status code and the body.
func w25bPostJSON(t *testing.T, leg *w25bLeg, path, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+leg.closeToken)
	req.Header.Set(authz.HeaderXOrg, leg.orgSlug)
	req.Header.Set("Content-Type", "application/json")
	rsp := httptest.NewRecorder()
	leg.mux.ServeHTTP(rsp, req)
	return rsp.Code, rsp.Body.String()
}

// w25bCreateLandedCost opens the leg's landed cost over the served route and
// answers its id. The freight is the leg's own: w25bFreight cents, allocated
// across the PO's received line by value.
func w25bCreateLandedCost(t *testing.T, leg *w25bLeg, freight int64) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"po_id":            leg.poID,
		"branch_id":        leg.branchID,
		"currency":         "CAD",
		"freight_cents":    freight,
		"allocation_basis": "VALUE",
	})
	if err != nil {
		t.Fatalf("the landed cost body: %v", err)
	}
	code, rsp := w25bPostJSON(t, leg, "/api/v1/landed-costs", string(body))
	if code != http.StatusCreated {
		t.Fatalf("opening the landed cost answered %d, want 201: %s", code, rsp)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(rsp), &out); err != nil {
		t.Fatalf("the landed cost answer: %v", err)
	}
	return out.ID
}

// w25bPinClock pins the landed cost's own clock to the instant the leg names,
// through the service's own WithClock setter (the month walk pins the till and
// the payments clocks the same way). The instant is the one the landed cost's
// stock ledger value event AND its GL journal carry, so the two sides of the
// stock tie cut the event together.
func w25bPinClock(t *testing.T, leg *w25bLeg, at time.Time) {
	t.Helper()
	leg.ops.landedcostSvc.WithClock(func() time.Time { return at })
}

// w25bClose closes one month through the served route, as the Financial
// Controller, and answers the status code and the body.
func w25bClose(t *testing.T, leg *w25bLeg, year, month int) (int, string) {
	t.Helper()
	var periodID string
	if err := leg.pool.QueryRow(context.Background(), `SELECT id::text FROM fiscal_periods WHERE year = $1 AND month = $2`, year, month).Scan(&periodID); err != nil {
		t.Fatalf("the %04d-%02d period: %v", year, month, err)
	}
	return w25bPostJSON(t, leg, "/api/v1/gl/periods/"+periodID+"/close", `{"reason":"W2-5B month close"}`)
}

// w25bPeriodStatus answers one period's status.
func w25bPeriodStatus(t *testing.T, leg *w25bLeg, year, month int) string {
	t.Helper()
	var status string
	if err := leg.pool.QueryRow(context.Background(), `SELECT status FROM fiscal_periods WHERE year = $1 AND month = $2`, year, month).Scan(&status); err != nil {
		t.Fatalf("the %04d-%02d period status: %v", year, month, err)
	}
	return status
}

// w25bStockValueAt reads the company's stock value as of an instant, through
// the production seam (the same read the close pre flight makes).
func w25bStockValueAt(t *testing.T, leg *w25bLeg, at time.Time) int64 {
	t.Helper()
	v, err := leg.money.inventorySvc.StockValueAsOfCents(context.Background(), at, "")
	if err != nil {
		t.Fatalf("the company's stock value as of %s: %v", at, err)
	}
	return v
}

// w25bGL1200At reads the company's 1200 as of an instant, through the GL's own
// repository (the same read the close pre flight makes).
func w25bGL1200At(t *testing.T, leg *w25bLeg, at time.Time) int64 {
	t.Helper()
	return w25bGLAccountAt(t, leg, "1200", at)
}

// w25bGLAccountAt reads one company's account balance as of an instant, through
// the GL's own repository.
func w25bGLAccountAt(t *testing.T, leg *w25bLeg, account string, at time.Time) int64 {
	t.Helper()
	v, _, err := gl.NewRepository(leg.pool).AccountBalanceAsOf(context.Background(), account, at, "")
	if err != nil {
		t.Fatalf("the company's %s as of %s: %v", account, at, err)
	}
	return v
}

// w25bSell sells part of the leg's stock through the production fulfilment
// seam (the wired inventory service's own FulfilOrderLinesTx), so the retro
// leg has a genuine sold through portion to expense. The sale's move row
// carries the wall clock, which is before the leg's month's end, and the COGS
// the fulfilment's consumer posts is dated the same way, so both sides of the
// tie read the sale together.
func w25bSell(t *testing.T, leg *w25bLeg, units float64) {
	t.Helper()
	ctx := context.Background()
	var customerID string
	if err := leg.pool.QueryRow(ctx, `SELECT id::text FROM customers ORDER BY id LIMIT 1`).Scan(&customerID); err != nil {
		t.Fatalf("a seeded customer: %v", err)
	}
	var orderID string
	if err := leg.pool.QueryRow(ctx, `
		INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		VALUES ('W2-5B-' || $1 || '-' || substr(md5(random()::text), 1, 8), $2, $3, 'FULFILLED', 'PICKUP', $4, $4)
		RETURNING id::text`, leg.suffix, customerID, leg.branchID, int64(units)*w25bUnitCost).Scan(&orderID); err != nil {
		t.Fatalf("the sale: %v", err)
	}
	tx, err := leg.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the fulfilment: %v", err)
	}
	if err := leg.money.inventorySvc.FulfilOrderLinesTx(ctx, tx, orderID, leg.branchID, []string{leg.productID}, []float64{units}); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("the fulfilment: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the fulfilment: %v", err)
	}
	w25bDrain(t, leg)
}

// TestW25BLandedCostInTheMonthClosesTheMonthIsTheControl is the control: the
// landed cost lands in the month the leg closes, the month ties at its end and
// the close seals it. It ties today and must still tie: the value event the
// fix adds is inside the month, so the month's end reads it on both sides.
func TestW25BLandedCostInTheMonthClosesTheMonthIsTheControl(t *testing.T) {
	leg := w25bSetup(t, "control")
	openAt, inMonth, _, monthEnd := w25bInstants(t, leg.pool)
	w25bTieTheAR(t, leg, openAt)
	w25bTieTheMonthAtItsEnd(t, leg, openAt, monthEnd)

	// The landed cost lands INSIDE the month the leg closes.
	lcID := w25bCreateLandedCost(t, leg, w25bFreight)
	w25bPinClock(t, leg, inMonth)
	code, body := w25bPostJSON(t, leg, "/api/v1/landed-costs/"+lcID+"/apply", "{}")
	if code != http.StatusOK {
		t.Fatalf("applying the landed cost answered %d, want 200: %s", code, body)
	}

	// The capitalization raised the stock ledger and 1200 together, inside the
	// month: both sides read the raised figure at the month's end.
	stock := w25bStockValueAt(t, leg, monthEnd)
	gl1200 := w25bGL1200At(t, leg, monthEnd)
	if stock != gl1200 {
		t.Fatalf("the month does not tie at its end: the stock ledger holds %d and 1200 holds %d", stock, gl1200)
	}
	if want := leg.stockCents + w25bFreight; stock != want {
		t.Fatalf("the stock ledger holds %d after the capitalization, want %d", stock, want)
	}

	// The close of the month the landed cost landed in seals it.
	code, body = w25bClose(t, leg, w25bYear, w25bMonth)
	if code != http.StatusOK {
		t.Fatalf("the close of the month the landed cost landed in was refused: %d: %s", code, body)
	}
	if status := w25bPeriodStatus(t, leg, w25bYear, w25bMonth); status != "CLOSED" {
		t.Fatalf("the period reads %s after the close, want CLOSED", status)
	}
}

// TestW25BLandedCostInTheNextMonthDoesNotRefuseTheEarlierMonth is the red: the
// landed cost lands in the NEXT month, so it belongs to that month and to no
// earlier one. The close of the earlier month is refused today, naming the
// absorbed amount: the stock side reads the month's end as if the
// capitalization had already been inside it, and 1200 reads the month as it
// stood before the capitalization's journal.
func TestW25BLandedCostInTheNextMonthDoesNotRefuseTheEarlierMonth(t *testing.T) {
	leg := w25bSetup(t, "red")
	openAt, _, nextMonth, monthEnd := w25bInstants(t, leg.pool)
	w25bTieTheAR(t, leg, openAt)
	w25bTieTheMonthAtItsEnd(t, leg, openAt, monthEnd)

	// The landed cost lands in the NEXT month.
	lcID := w25bCreateLandedCost(t, leg, w25bFreight)
	w25bPinClock(t, leg, nextMonth)
	code, body := w25bPostJSON(t, leg, "/api/v1/landed-costs/"+lcID+"/apply", "{}")
	if code != http.StatusOK {
		t.Fatalf("applying the landed cost answered %d, want 200: %s", code, body)
	}

	// The capitalization raised the stock ledger and 1200 together, in the
	// NEXT month: the month's end must read neither.
	stock := w25bStockValueAt(t, leg, monthEnd)
	gl1200 := w25bGL1200At(t, leg, monthEnd)
	if stock != gl1200 {
		t.Fatalf("the month does not tie at its end: the stock ledger holds %d and 1200 holds %d", stock, gl1200)
	}
	if want := leg.stockCents; stock != want {
		t.Fatalf("the stock ledger holds %d at the month's end, want %d (the next month's capitalization is not in this month)", stock, want)
	}

	// The close of the earlier month: the next month's landed cost is no
	// reason to refuse it.
	code, body = w25bClose(t, leg, w25bYear, w25bMonth)
	if code != http.StatusOK {
		t.Fatalf("the close of the month the next month's landed cost belongs to was refused: %d: %s", code, body)
	}
	if status := w25bPeriodStatus(t, leg, w25bYear, w25bMonth); status != "CLOSED" {
		t.Fatalf("the period reads %s after the close, want CLOSED", status)
	}
}

// TestW25BLandedCostVoidedInTheNextMonthDoesNotRefuseTheEarlierMonth is the
// red's other face: the landed cost landed in the month the leg closes and was
// VOIDED in the next one. The earlier month carries the capitalization and
// not the reversal, so its stock side and 1200 must both read the raised
// figure; today the stock side reads the reversal (the weighted average cost
// is back where it was) while 1200 reads the month before the reversal's
// journal, and the close is refused by the restored amount.
func TestW25BLandedCostVoidedInTheNextMonthDoesNotRefuseTheEarlierMonth(t *testing.T) {
	leg := w25bSetup(t, "void")
	openAt, inMonth, nextMonth, monthEnd := w25bInstants(t, leg.pool)
	w25bTieTheAR(t, leg, openAt)
	w25bTieTheMonthAtItsEnd(t, leg, openAt, monthEnd)

	// The landed cost lands in the month the leg closes.
	lcID := w25bCreateLandedCost(t, leg, w25bFreight)
	w25bPinClock(t, leg, inMonth)
	code, body := w25bPostJSON(t, leg, "/api/v1/landed-costs/"+lcID+"/apply", "{}")
	if code != http.StatusOK {
		t.Fatalf("applying the landed cost answered %d, want 200: %s", code, body)
	}
	raised := leg.stockCents + w25bFreight

	// The void lands in the NEXT month.
	w25bPinClock(t, leg, nextMonth)
	code, body = w25bPostJSON(t, leg, "/api/v1/landed-costs/"+lcID+"/void", "{}")
	if code != http.StatusOK {
		t.Fatalf("voiding the landed cost answered %d, want 200: %s", code, body)
	}

	// The apply/void round trip: the weighted average cost and the stock
	// ledger are back where they were, and 1200 with them.
	now, err := leg.money.inventorySvc.StockValueCents(context.Background(), "")
	if err != nil {
		t.Fatalf("the company's stock value after the void: %v", err)
	}
	if now != leg.stockCents {
		t.Fatalf("the stock ledger holds %d after the void, want %d (the round trip is cent exact)", now, leg.stockCents)
	}
	if glNow := w25bGL1200At(t, leg, nextMonth); glNow != now {
		t.Fatalf("the void moved the stock ledger to %d and 1200 to %d; they must move together", now, glNow)
	}

	// The earlier month carries the capitalization and not the reversal: both
	// sides read the raised figure at its end.
	stock := w25bStockValueAt(t, leg, monthEnd)
	gl1200 := w25bGL1200At(t, leg, monthEnd)
	if stock != gl1200 {
		t.Fatalf("the month does not tie at its end: the stock ledger holds %d and 1200 holds %d", stock, gl1200)
	}
	if stock != raised {
		t.Fatalf("the stock ledger holds %d at the month's end, want %d (the month carries the capitalization, not the next month's void)", stock, raised)
	}

	// The as of value equals GL 1200 as of that instant at EVERY instant: before
	// the capitalization, between the capitalization and the reversal, and after
	// the reversal. The ledger's value events and the 1200 legs are the same pair
	// of events at the same instants, so the two sides never come apart.
	for _, at := range []time.Time{openAt, inMonth.Add(time.Hour), nextMonth.Add(48 * time.Hour)} {
		atStock := w25bStockValueAt(t, leg, at)
		atGL := w25bGL1200At(t, leg, at)
		if atStock != atGL {
			t.Fatalf("the as of value and 1200 come apart at %s: the stock ledger holds %d and 1200 holds %d", at, atStock, atGL)
		}
	}

	// The close of the earlier month: the next month's void is no reason to
	// refuse it.
	code, body = w25bClose(t, leg, w25bYear, w25bMonth)
	if code != http.StatusOK {
		t.Fatalf("the close of the month the next month's void belongs to was refused: %d: %s", code, body)
	}
	if status := w25bPeriodStatus(t, leg, w25bYear, w25bMonth); status != "CLOSED" {
		t.Fatalf("the period reads %s after the close, want CLOSED", status)
	}
}

// TestW25BRetroCapitalizationInTheNextMonthDoesNotRefuseTheEarlierMonth is
// the red's third face: the late freight lands in the NEXT month as a retro
// capitalization. Only the still on hand portion capitalizes into the stock
// ledger; the sold through portion is expensed to COGS and never moves the
// stock value. The earlier month must read neither.
func TestW25BRetroCapitalizationInTheNextMonthDoesNotRefuseTheEarlierMonth(t *testing.T) {
	leg := w25bSetup(t, "retro")
	ctx := context.Background()
	openAt, _, nextMonth, monthEnd := w25bInstants(t, leg.pool)
	w25bTieTheAR(t, leg, openAt)
	w25bTieTheMonthAtItsEnd(t, leg, openAt, monthEnd)

	// The sale that leaves the retro a sold through portion.
	w25bSell(t, leg, w25bSold)
	afterSale := leg.stockCents - w25bSold*w25bUnitCost

	// The late freight: a retro doc over the same PO, capturing the stock
	// that is still on hand.
	lcID := w25bCreateLandedCost(t, leg, w25bFreight)
	body, err := json.Marshal(map[string]any{
		"po_id":            leg.poID,
		"branch_id":        leg.branchID,
		"currency":         "CAD",
		"freight_cents":    w25bFreight,
		"allocation_basis": "VALUE",
		"retro_of_id":      lcID,
		"on_hand": []map[string]any{{
			"product_id":   leg.productID,
			"po_line_id":   leg.poLineID,
			"on_hand_base": w25bUnits - w25bSold,
		}},
	})
	if err != nil {
		t.Fatalf("the retro landed cost body: %v", err)
	}
	code, rsp := w25bPostJSON(t, leg, "/api/v1/landed-costs", string(body))
	if code != http.StatusCreated {
		t.Fatalf("opening the retro landed cost answered %d, want 201: %s", code, rsp)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(rsp), &out); err != nil {
		t.Fatalf("the retro landed cost answer: %v", err)
	}
	retroID := out.ID

	// The retro lands in the NEXT month.
	w25bPinClock(t, leg, nextMonth)
	code, rsp = w25bPostJSON(t, leg, "/api/v1/landed-costs/"+retroID+"/retro", "{}")
	if code != http.StatusOK {
		t.Fatalf("retro-adjusting the landed cost answered %d, want 200: %s", code, rsp)
	}

	// The retro's split: the still on hand portion capitalized into the stock
	// ledger, the sold through portion expensed to COGS.
	onHandPortion := int64(w25bFreight) * int64(w25bUnits-w25bSold) / int64(w25bUnits)
	soldThrough := int64(w25bFreight) - onHandPortion
	if soldThrough == 0 {
		t.Fatalf("the retro had no sold through portion to expense")
	}
	now, err := leg.money.inventorySvc.StockValueCents(ctx, "")
	if err != nil {
		t.Fatalf("the company's stock value after the retro: %v", err)
	}
	if want := afterSale + onHandPortion; now != want {
		t.Fatalf("the stock ledger holds %d after the retro, want %d (the sold through portion never moves the stock value)", now, want)
	}
	// The sold through portion went to COGS and the still on hand portion to
	// inventory: the two legs say which is which, and the stock value moved by
	// the inventory leg alone.
	after := nextMonth.Add(48 * time.Hour)
	if moved := w25bGL1200At(t, leg, after) - w25bGL1200At(t, leg, nextMonth.Add(-time.Hour)); moved != onHandPortion {
		t.Fatalf("the retro moved 1200 by %d, want %d (the still on hand portion alone)", moved, onHandPortion)
	}
	if moved := w25bGLAccountAt(t, leg, "5000", after) - w25bGLAccountAt(t, leg, "5000", nextMonth.Add(-time.Hour)); moved != soldThrough {
		t.Fatalf("the retro moved COGS by %d, want %d (the sold through portion alone)", moved, soldThrough)
	}

	// The earlier month reads neither: the retro belongs to the next month.
	stock := w25bStockValueAt(t, leg, monthEnd)
	gl1200 := w25bGL1200At(t, leg, monthEnd)
	if stock != gl1200 {
		t.Fatalf("the month does not tie at its end: the stock ledger holds %d and 1200 holds %d", stock, gl1200)
	}
	if stock != afterSale {
		t.Fatalf("the stock ledger holds %d at the month's end, want %d (the next month's retro is not in this month)", stock, afterSale)
	}

	// The close of the earlier month: the next month's retro is no reason to
	// refuse it.
	code, rsp = w25bClose(t, leg, w25bYear, w25bMonth)
	if code != http.StatusOK {
		t.Fatalf("the close of the month the next month's retro belongs to was refused: %d: %s", code, rsp)
	}
	if status := w25bPeriodStatus(t, leg, w25bYear, w25bMonth); status != "CLOSED" {
		t.Fatalf("the period reads %s after the close, want CLOSED", status)
	}
}

// TestW25BALaterApplyDoesNotMaskAnEarlierMonthsVariance is the addendum's
// first leg, K3's first P1 on 661: the gap is not only that a month that TIED
// at its end is refused by a later landed cost. A later apply moves the stock
// ledger AND 1200 together, so it can CANCEL a discrepancy the month already
// carried and pass a month that did not tie at its end.
//
// The world: the stock ledger sits 500 BELOW 1200 at month 1's end, by
// exactly the amount the later apply is worth, and the apply lands in month
// 2. The as of read answers the stock ledger's CURRENT value, so the next
// month's capitalization is inside month 1's end reading, the two sides
// agree, and the close of month 1 PASSES today. It must refuse, naming the
// month's own variance.
func TestW25BALaterApplyDoesNotMaskAnEarlierMonthsVariance(t *testing.T) {
	leg := w25bSetup(t, "maskapply")
	openAt, inMonth, nextMonth, monthEnd := w25bInstants(t, leg.pool)
	w25bTieTheAR(t, leg, openAt)
	w25bTieTheMonthAtItsEnd(t, leg, openAt, monthEnd)

	// The month ends OUT OF TIE: the stock ledger sits 500 BELOW 1200, the
	// discrepancy the later apply is worth.
	w25bPlantTheMonthEndVariance(t, leg, inMonth, -w25bFreight, "w2-5b:maskapply:variance")

	// The landed cost lands in the NEXT month, for exactly that amount.
	lcID := w25bCreateLandedCost(t, leg, w25bFreight)
	w25bPinClock(t, leg, nextMonth)
	code, body := w25bPostJSON(t, leg, "/api/v1/landed-costs/"+lcID+"/apply", "{}")
	if code != http.StatusOK {
		t.Fatalf("applying the landed cost answered %d, want 200: %s", code, body)
	}

	// The month's own stock value: the receipt's, and nothing else. The next
	// month's capitalization belongs to the next month, so the month's end
	// reading must not carry it.
	stock := w25bStockValueAt(t, leg, monthEnd)
	if want := leg.stockCents; stock != want {
		t.Errorf("the stock ledger holds %d at the month's end, want %d (the next month's capitalization is not in this month's stock)", stock, want)
	}
	gl1200 := w25bGL1200At(t, leg, monthEnd)
	if want := leg.stockCents + w25bFreight; gl1200 != want {
		t.Errorf("1200 holds %d at the month's end, want %d (the planted variance)", gl1200, want)
	}
	if variance := stock - gl1200; variance != -w25bFreight {
		t.Errorf("the month's end variance is %d, want %d (the stock ledger is 500 below 1200)", variance, -w25bFreight)
	}

	// The variance persists into the present: nothing later corrected it, so
	// the month really did end out of tie.
	after := nextMonth.Add(48 * time.Hour)
	afterStock := w25bStockValueAt(t, leg, after)
	afterGL := w25bGL1200At(t, leg, after)
	if variance := afterStock - afterGL; variance != -w25bFreight {
		t.Errorf("the present's variance is %d, want %d (the stock ledger holds %d and 1200 holds %d)", variance, -w25bFreight, afterStock, afterGL)
	}

	// The close of the month that was out of tie at its end: refused, naming
	// the month's OWN variance, not the figure the later apply made agree.
	code, body = w25bClose(t, leg, w25bYear, w25bMonth)
	if code != http.StatusConflict {
		t.Fatalf("the close of the month that was out of tie at its end answered %d, want 409: the period reads %s", code, w25bPeriodStatus(t, leg, w25bYear, w25bMonth))
	}
	for _, want := range []string{"stock is out of tie with 1200 by -$5.00"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the refusal %q does not name %q", body, want)
		}
	}
	if status := w25bPeriodStatus(t, leg, w25bYear, w25bMonth); status != "OPEN" {
		t.Fatalf("the refused close left the period %s, want OPEN", status)
	}
}

// TestW25BALaterVoidDoesNotMaskAnEarlierMonthsVariance is the addendum's
// second leg: the same, for a later VOID, with the sign reversed.
//
// The world: the landed cost lands inside month 1 and the stock ledger sits
// 500 ABOVE 1200 at month 1's end, by exactly the amount the later void is
// worth, and the void lands in month 2. The as of read answers the stock
// ledger's CURRENT value, so the next month's reversal is inside month 1's
// end reading, the two sides agree, and the close of month 1 PASSES today.
// It must refuse, naming the month's own variance.
func TestW25BALaterVoidDoesNotMaskAnEarlierMonthsVariance(t *testing.T) {
	leg := w25bSetup(t, "maskvoid")
	openAt, inMonth, nextMonth, monthEnd := w25bInstants(t, leg.pool)
	w25bTieTheAR(t, leg, openAt)
	w25bTieTheMonthAtItsEnd(t, leg, openAt, monthEnd)

	// The landed cost lands INSIDE the month the leg closes.
	lcID := w25bCreateLandedCost(t, leg, w25bFreight)
	w25bPinClock(t, leg, inMonth)
	code, body := w25bPostJSON(t, leg, "/api/v1/landed-costs/"+lcID+"/apply", "{}")
	if code != http.StatusOK {
		t.Fatalf("applying the landed cost answered %d, want 200: %s", code, body)
	}
	raised := leg.stockCents + w25bFreight

	// The month ends OUT OF TIE, the sign reversed from the apply leg: the
	// stock ledger sits 500 ABOVE 1200, by exactly the amount the later void
	// is worth.
	w25bPlantTheMonthEndVariance(t, leg, inMonth.Add(time.Hour), w25bFreight, "w2-5b:maskvoid:variance")

	// The void lands in the NEXT month.
	w25bPinClock(t, leg, nextMonth)
	code, body = w25bPostJSON(t, leg, "/api/v1/landed-costs/"+lcID+"/void", "{}")
	if code != http.StatusOK {
		t.Fatalf("voiding the landed cost answered %d, want 200: %s", code, body)
	}

	// The month's own stock value: the capitalization's, and not the reversal
	// the next month books.
	stock := w25bStockValueAt(t, leg, monthEnd)
	if want := raised; stock != want {
		t.Errorf("the stock ledger holds %d at the month's end, want %d (the next month's reversal is not in this month's stock)", stock, want)
	}
	gl1200 := w25bGL1200At(t, leg, monthEnd)
	if want := leg.stockCents; gl1200 != want {
		t.Errorf("1200 holds %d at the month's end, want %d (the planted variance)", gl1200, want)
	}
	if variance := stock - gl1200; variance != w25bFreight {
		t.Errorf("the month's end variance is %d, want %d (the stock ledger is 500 above 1200)", variance, w25bFreight)
	}

	// The variance persists into the present: nothing later corrected it, so
	// the month really did end out of tie.
	after := nextMonth.Add(48 * time.Hour)
	afterStock := w25bStockValueAt(t, leg, after)
	afterGL := w25bGL1200At(t, leg, after)
	if variance := afterStock - afterGL; variance != w25bFreight {
		t.Errorf("the present's variance is %d, want %d (the stock ledger holds %d and 1200 holds %d)", variance, w25bFreight, afterStock, afterGL)
	}

	// The close of the month that was out of tie at its end: refused, naming
	// the month's OWN variance, not the figure the later void made agree.
	code, body = w25bClose(t, leg, w25bYear, w25bMonth)
	if code != http.StatusConflict {
		t.Fatalf("the close of the month that was out of tie at its end answered %d, want 409: the period reads %s", code, w25bPeriodStatus(t, leg, w25bYear, w25bMonth))
	}
	for _, want := range []string{"stock is out of tie with 1200 by $5.00"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the refusal %q does not name %q", body, want)
		}
	}
	if status := w25bPeriodStatus(t, leg, w25bYear, w25bMonth); status != "OPEN" {
		t.Fatalf("the refused close left the period %s, want OPEN", status)
	}
}
