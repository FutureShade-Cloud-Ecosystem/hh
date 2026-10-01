//go:build integration

// counter_till_break_integration_test.go, W4's leg of the ruling over the same fixture world the
// counter till suites use: the scratch database named by HH_CORE_TEST_DATABASE_URL, the frozen
// migration set applied, the kit's world built, and (this file alone) a volume break planted on
// the fixture Holland paver stand-in carrying the demoseed's QB1 shape (a 0 bps band below the
// threshold, then 500 bps at 500, then 1000 bps at 2000, thresholds in the line's own unit).
//
// What is proven here, at Trenton, as Sales, across the break: the D1 replace rule holds on the
// till's path. C01's line of 600 SF crosses the 500 SF threshold, and the line rings at the
// contractor rule price (39934, the best single price) instead of the stacked 37937 the old
// adjustment-on-top model answered; a walk-in line through the counter resolve at the same
// quantity pays the break's own price off the list anchor (46265 replaces 48700) and below the
// threshold pays list unchanged; a pricing order for the crossing line is created at the rule
// price. Run with the variable set:
//
//	HH_CORE_TEST_DATABASE_URL=... go test -tags integration -count=1 ./internal/pos/ -run TestCounterTillBreak
//
// Row hygiene: the planted rows carry fixed ids in the kit's 3ef1 block and are deleted by this
// file alone (the break items die with their header by cascade); the ring is replay-covered by
// its fixed idempotency key, so a re-run against the same scratch database replays the committed
// sale instead of writing a second one, and the committed sale and its audit and event history
// stay, as history does.
package pos

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The break's own fixture ids, fixed and patterned like the kit's, deleted by this file alone.
const (
	fxTillBreakHeaderID   = "3ef10000-0000-4000-8000-0000000001d1"
	fxTillBreakTierLowID  = "3ef10000-0000-4000-8000-0000000001d2"
	fxTillBreakTierMidID  = "3ef10000-0000-4000-8000-0000000001d3"
	fxTillBreakTierTopID  = "3ef10000-0000-4000-8000-0000000001d4"
	fxTillBreakSaleKey    = "3ef10000-0000-4000-8000-0000000001c2" // this ring's fixed replay key
	fxTillBreakHeaderCode = "FX-TILL-QB1"
)

// The ruled numbers across the break: the contractor rule price stands at the threshold (the
// break's 500 bps off the 48700 anchor is 46265, the 1800 bps rule beats it), the walk-in pays
// the break price there, and stacking would have rung the account line at 37937.
const (
	fxBreakRuleUnitCents    int64 = 39934
	fxBreakRuleLineCents    int64 = 23960400 // 600 SF at the rule price
	fxBreakStackedUnitCents int64 = 37937    // the old stacking answer, asserted against and refused
	fxBreakWalkInUnitCents  int64 = 46265    // 48700 less 500 bps, the break replacing list
)

// plantTillBreakRows deletes this file's soft rows and plants its fixed rows again, so a re-run
// against a database the file has filled before is a clean re-plant.
func plantTillBreakRows(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`DELETE FROM quantity_break_headers WHERE id = '`+fxTillBreakHeaderID+`'`); err != nil {
		t.Fatalf("break re-plant delete: %v", err)
	}
	plants := []string{
		// QB1's shape on the fixture stand-in: no adjustment below 500, 500 bps at 500, 1000 bps
		// at 2000, thresholds in the product's own unit.
		`INSERT INTO quantity_break_headers (id, code, name, product_id, threshold_uom, is_active) VALUES
			('` + fxTillBreakHeaderID + `', '` + fxTillBreakHeaderCode + `', 'Fixture Holland Paver volume breaks (demo)', '` + fxTillProductID + `', 'SF', TRUE)
			ON CONFLICT DO NOTHING`,
		`INSERT INTO quantity_break_items (id, header_id, from_qty, to_qty, value_type, discount_bps, price_uom) VALUES
			('` + fxTillBreakTierLowID + `', '` + fxTillBreakHeaderID + `', 0, 500, 'PCT_OFF', 0, 'SF')
			ON CONFLICT DO NOTHING`,
		`INSERT INTO quantity_break_items (id, header_id, from_qty, to_qty, value_type, discount_bps, price_uom) VALUES
			('` + fxTillBreakTierMidID + `', '` + fxTillBreakHeaderID + `', 500, 2000, 'PCT_OFF', 500, 'SF')
			ON CONFLICT DO NOTHING`,
		`INSERT INTO quantity_break_items (id, header_id, from_qty, to_qty, value_type, discount_bps, price_uom) VALUES
			('` + fxTillBreakTierTopID + `', '` + fxTillBreakHeaderID + `', 2000, NULL, 'PCT_OFF', 1000, 'SF')
			ON CONFLICT DO NOTHING`,
	}
	for _, q := range plants {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("break plant: %v (%s)", err, q)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM quantity_break_headers WHERE id = '`+fxTillBreakHeaderID+`'`)
	})
}

// As Sales at Trenton: C01's line crosses the 500 SF threshold and rings at the contractor rule
// price, the best single price under D1, never the stacked figure; the committed line equals the
// engine's own counter resolve for the same line.
func TestCounterTillBreak_SaleAcrossTheBreakRingsTheRulePrice(t *testing.T) {
	pool := counterTillFixture(t)
	plantTillBreakRows(t, pool)
	svc := realPosSvc(t, pool, stubCharger{})
	out, err := svc.Sale(tillSalesCtx(), SaleInput{
		IdempotencyKey: fxTillBreakSaleKey, BranchID: testkit.BranchNorthAID,
		CustomerID: testkit.TenantAlphaID, Tender: TenderCash, SaleType: SaleCollectNow,
		TillSessionID: openTill(t, pool, testkit.BranchNorthAID),
		Lines:         []LineInput{{ProductID: fxTillProductID, QtyEntered: 600, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("the till rings the crossing line: %v", err)
	}
	if out.Status != "COMMITTED" {
		t.Fatalf("the crossing line commits: %+v", out)
	}
	if out.SubtotalCents != fxBreakRuleLineCents {
		t.Fatalf("subtotal at the D1 price %d (stacking would ring %d), got %d",
			fxBreakRuleLineCents, fxBreakStackedUnitCents*600, out.SubtotalCents)
	}
	var unit, total int64
	if err := pool.QueryRow(context.Background(),
		`SELECT unit_price_cents, line_total_cents FROM order_lines WHERE order_id=$1 AND line_no=1`, out.OrderID).
		Scan(&unit, &total); err != nil {
		t.Fatalf("read the committed line: %v", err)
	}
	if unit != fxBreakRuleUnitCents || total != fxBreakRuleLineCents {
		t.Fatalf("committed line %d/%d, want %d/%d", unit, total, fxBreakRuleUnitCents, fxBreakRuleLineCents)
	}
	// The sale's lines equal the engine's price: the same crossing line through the same counter
	// resolve, which prices the break's contest the way the ring just did.
	lp, err := counterTillPricing(pool).DeriveCounterLinePrice(context.Background(), pricing.ResolveInput{
		CustomerID: testkit.TenantAlphaID, CustomerType: "CONTRACTOR", ProductID: fxTillProductID,
		BranchID: testkit.BranchNorthAID, QtyEntered: 600, UOMEntered: "SF",
	})
	if err != nil {
		t.Fatalf("counter resolve across the break: %v", err)
	}
	if lp.UnitPriceCents != unit || lp.LineTotalCents != total {
		t.Fatalf("the engine prices the crossing line %d/%d, the sale rang %d/%d",
			lp.UnitPriceCents, lp.LineTotalCents, unit, total)
	}
	if lp.Breakdown.Source != pricing.SourceTierCategory {
		t.Fatalf("the rule holds the line against the weaker break, got source %s", lp.Breakdown.Source)
	}
}

// The walk-in's price through the counter resolve: the break REPLACES list at the threshold and
// leaves list alone below it.
func TestCounterTillBreak_WalkInTakesTheBreakPriceAtTheThreshold(t *testing.T) {
	pool := counterTillFixture(t)
	plantTillBreakRows(t, pool)
	psvc := counterTillPricing(pool)

	above, err := psvc.DeriveCounterLinePrice(context.Background(), pricing.ResolveInput{
		CustomerType: "RETAIL", ProductID: fxTillProductID,
		BranchID: testkit.BranchNorthAID, QtyEntered: 600, UOMEntered: "SF",
	})
	if err != nil {
		t.Fatalf("counter resolve at 600 SF: %v", err)
	}
	if above.UnitPriceCents != fxBreakWalkInUnitCents || above.Breakdown.Source != pricing.SourceQuantityBreak {
		t.Fatalf("the break price 46265 replaces list at the threshold, got %d/%s",
			above.UnitPriceCents, above.Breakdown.Source)
	}
	below, err := psvc.DeriveCounterLinePrice(context.Background(), pricing.ResolveInput{
		CustomerType: "RETAIL", ProductID: fxTillProductID,
		BranchID: testkit.BranchNorthAID, QtyEntered: 400, UOMEntered: "SF",
	})
	if err != nil {
		t.Fatalf("counter resolve at 400 SF: %v", err)
	}
	if below.UnitPriceCents != fxHollandListCents || below.Breakdown.Source != pricing.SourceList {
		t.Fatalf("below the threshold the walk-in pays list unchanged, got %d/%s",
			below.UnitPriceCents, below.Breakdown.Source)
	}
}

// A pricing order for the crossing line is created at the rule price against the 48700 list.
func TestCounterTillBreak_PricingOrder_CreatedAtTheRulePrice(t *testing.T) {
	pool := counterTillFixture(t)
	plantTillBreakRows(t, pool)
	svc := realPosSvc(t, pool, stubCharger{})
	po, err := svc.CreatePricingOrder(tillSalesCtx(), PricingOrderInput{
		BranchID: testkit.BranchNorthAID, CustomerID: testkit.TenantAlphaID, CustomerName: "Fixture C01",
		Lines: []PricingOrderLineInput{{ProductID: fxTillProductID, QtyEntered: 600, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("pricing order created: %v", err)
	}
	if po.DocNo == "" || len(po.Lines) != 1 {
		t.Fatalf("the pricing order carries its line: %+v", po)
	}
	ln := po.Lines[0]
	if ln.UnitPriceCents != fxBreakRuleUnitCents || ln.ListPriceCents != fxHollandListCents || ln.LineTotalCents != fxBreakRuleLineCents {
		t.Fatalf("list-vs-offered at the rule price across the break: %+v", ln)
	}
	if po.SubtotalCents != fxBreakRuleLineCents {
		t.Fatalf("subtotal %d, want %d", po.SubtotalCents, fxBreakRuleLineCents)
	}
}
