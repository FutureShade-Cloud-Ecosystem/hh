//go:build integration

// counter_till_break_integration_test.go, W4's leg of the ruling over the same fixture world the
// counter till suites use: the scratch database named by HH_CORE_TEST_DATABASE_URL, the frozen
// migration set applied, the kit's world built, and (this file alone) a volume break planted on
// the fixture Holland paver stand-in carrying the demoseed's QB1 shape (a 0 bps band below the
// threshold, then 500 bps at 500, then 1000 bps at 2000, thresholds in the line's own unit), plus
// a retail customer whose lines no fixture rule reaches, so the break can win.
//
// What is proven here, at Trenton, as Sales: the D1 replace rule holds on the till's path, both
// ways round. C01's line of 600 SF crosses the 500 SF threshold and rings at the contractor rule
// price (39934, the best single price) instead of the stacked 37937 the old adjustment-on-top
// model answered; the retail customer's line at the same quantity rings at the break's own price
// off the list anchor (46265 replaces 48700); a pricing order for the crossing line is created at
// the rule price. The price assertions on a ring are made on the FIRST RUN ONLY (fresh key
// detection below): on a database that already holds the committed sale the ring replays, and the
// fresh engine comparison beside it is what measures the resolver. Run with the variable set:
//
//	HH_CORE_TEST_DATABASE_URL=... go test -tags integration -count=1 ./internal/pos/ -run TestCounterTillBreak
//
// Row hygiene: the planted rows carry fixed ids in the kit's 3ef1 block and are deleted by this
// file alone (the break items die with their header by cascade; the retail customer's delete is
// best-effort, a committed sale keeps it alive by reference); the rings are replay-covered by
// their fixed idempotency keys, so a re-run against the same scratch database replays the
// committed sales instead of writing second ones, and the committed sales and their audit and
// event history stay, as history does.
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
	fxTillBreakHeaderID    = "3ef10000-0000-4000-8000-0000000001d1"
	fxTillBreakTierLowID   = "3ef10000-0000-4000-8000-0000000001d2"
	fxTillBreakTierMidID   = "3ef10000-0000-4000-8000-0000000001d3"
	fxTillBreakTierTopID   = "3ef10000-0000-4000-8000-0000000001d4"
	fxTillBreakSaleKey     = "3ef10000-0000-4000-8000-0000000001c2" // the rule-price ring's fixed replay key
	fxTillBreakWinSaleKey  = "3ef10000-0000-4000-8000-0000000001c3" // the break-win ring's fixed replay key
	fxTillRetailCustomerID = "3ef10000-0000-4000-8000-0000000001a5"
	fxTillBreakHeaderCode  = "FX-TILL-QB1"
)

// The ruled numbers across the break: the contractor rule price stands at the threshold (the
// break's 500 bps off the 48700 anchor is 46265, the 1800 bps rule beats it), the retail
// customer pays the break price there, and stacking would have rung the account line at 37937.
const (
	fxBreakRuleUnitCents    int64 = 39934
	fxBreakRuleLineCents    int64 = 23960400 // 600 SF at the rule price
	fxBreakStackedUnitCents int64 = 37937    // the old stacking answer, asserted against and refused
	fxBreakWalkInUnitCents  int64 = 46265    // 48700 less 500 bps, the break replacing list
	fxBreakWinLineCents     int64 = 27759000 // 600 SF at the break price
)

// counterTillBreakFixture is counterTillFixture plus this file's planted rows.
func counterTillBreakFixture(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := counterTillFixture(t)
	plantTillBreakRows(t, pool)
	return pool
}

// plantTillBreakRows deletes this file's soft rows and plants its fixed rows again, so a re-run
// against a database the file has filled before is a clean re-plant. The break header dies with
// nothing referencing it; the retail customer's delete is best-effort, a committed sale's order
// keeps it alive by reference, and the insert below re-plants it unchanged by its fixed id.
func plantTillBreakRows(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`DELETE FROM quantity_break_headers WHERE id = '`+fxTillBreakHeaderID+`'`); err != nil {
		t.Fatalf("break re-plant delete: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM customers WHERE id = '`+fxTillRetailCustomerID+`'`); err != nil {
		t.Fatalf("break re-plant delete from customers: %v", err)
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
		// The walk-in account at Trenton: RETAIL, so no fixture rule reaches its lines and the
		// break alone prices them.
		`INSERT INTO customers (id, account_no, name, customer_type, home_branch_id, org_id) VALUES
			('` + fxTillRetailCustomerID + `', 'FXACC-NORTH-4', 'Fixture Retail Walk-in', 'RETAIL', '` + testkit.BranchNorthAID + `', '` + testkit.OrgNorthID + `')
			ON CONFLICT DO NOTHING`,
	}
	for _, q := range plants {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("break plant: %v (%s)", err, q)
		}
	}
	// No cleanup here: counterTillFixture's leg sweep deletes these rows (items before header,
	// customer after its orders) and fails the leg on a refused delete.
}

// saleAlreadyRung reports whether this file's fixed replay key has already committed a sale on
// this database, which makes the ring below a replay: the replay returns the committed sale, and
// only the first run's assertions measure the resolver through the ring.
func saleAlreadyRung(t *testing.T, pool *pgxpool.Pool, key string) bool {
	t.Helper()
	var rung bool
	if err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM pos_sale WHERE idempotency_key = $1)`, key).Scan(&rung); err != nil {
		t.Fatalf("replay check: %v", err)
	}
	return rung
}

// As Sales at Trenton: C01's line crosses the 500 SF threshold and rings at the contractor rule
// price, the best single price under D1, never the stacked figure; the committed line equals the
// engine's own counter resolve for the same line, which is priced fresh on every run.
func TestCounterTillBreak_SaleAcrossTheBreakRingsTheRulePrice(t *testing.T) {
	pool := counterTillBreakFixture(t)
	svc := realPosSvc(t, pool, stubCharger{})
	firstRun := !saleAlreadyRung(t, pool, fxTillBreakSaleKey)
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
	if firstRun {
		// The ring happened on this run, so its numbers measure the resolver.
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
	} else {
		t.Log("replay run: the ring replays a committed sale, the engine comparison below measures the resolver")
	}
	// The sale's lines equal the engine's price: the same crossing line through the same counter
	// resolve, fresh arithmetic on every run, replay or not.
	lp, err := counterTillPricing(pool).DeriveCounterLinePrice(context.Background(), pricing.ResolveInput{
		CustomerID: testkit.TenantAlphaID, CustomerType: "CONTRACTOR", ProductID: fxTillProductID,
		BranchID: testkit.BranchNorthAID, QtyEntered: 600, UOMEntered: "SF",
	})
	if err != nil {
		t.Fatalf("counter resolve across the break: %v", err)
	}
	if lp.UnitPriceCents != fxBreakRuleUnitCents || lp.LineTotalCents != fxBreakRuleLineCents {
		t.Fatalf("the engine prices the crossing line %d/%d, want the D1 price %d/%d",
			lp.UnitPriceCents, lp.LineTotalCents, fxBreakRuleUnitCents, fxBreakRuleLineCents)
	}
	if lp.Breakdown.Source != pricing.SourceTierCategory {
		t.Fatalf("the rule holds the line against the weaker break, got source %s", lp.Breakdown.Source)
	}
}

// The break WINS through a committed ring: the retail customer's line crosses the threshold, no
// rule reaches it, and the sale commits at the break's price off the list anchor.
func TestCounterTillBreak_SaleWhereTheBreakWins(t *testing.T) {
	pool := counterTillBreakFixture(t)
	svc := realPosSvc(t, pool, stubCharger{})
	firstRun := !saleAlreadyRung(t, pool, fxTillBreakWinSaleKey)
	out, err := svc.Sale(tillSalesCtx(), SaleInput{
		IdempotencyKey: fxTillBreakWinSaleKey, BranchID: testkit.BranchNorthAID,
		CustomerID: fxTillRetailCustomerID, Tender: TenderCash, SaleType: SaleCollectNow,
		TillSessionID: openTill(t, pool, testkit.BranchNorthAID),
		Lines:         []LineInput{{ProductID: fxTillProductID, QtyEntered: 600, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("the till rings the walk-in crossing line: %v", err)
	}
	if out.Status != "COMMITTED" {
		t.Fatalf("the walk-in crossing line commits: %+v", out)
	}
	if firstRun {
		if out.SubtotalCents != fxBreakWinLineCents {
			t.Fatalf("subtotal at the break price %d (list would be %d), got %d",
				fxBreakWinLineCents, fxHollandListCents*600, out.SubtotalCents)
		}
		var unit, total int64
		if err := pool.QueryRow(context.Background(),
			`SELECT unit_price_cents, line_total_cents FROM order_lines WHERE order_id=$1 AND line_no=1`, out.OrderID).
			Scan(&unit, &total); err != nil {
			t.Fatalf("read the committed line: %v", err)
		}
		if unit != fxBreakWalkInUnitCents || total != fxBreakWinLineCents {
			t.Fatalf("committed line %d/%d, want the break price %d/%d", unit, total, fxBreakWalkInUnitCents, fxBreakWinLineCents)
		}
	}
	// The engine says why the line is cheap: the break won, source QUANTITY_BREAK, at the same
	// price the sale committed, fresh on every run.
	lp, err := counterTillPricing(pool).DeriveCounterLinePrice(context.Background(), pricing.ResolveInput{
		CustomerID: fxTillRetailCustomerID, CustomerType: "RETAIL", ProductID: fxTillProductID,
		BranchID: testkit.BranchNorthAID, QtyEntered: 600, UOMEntered: "SF",
	})
	if err != nil {
		t.Fatalf("counter resolve for the walk-in: %v", err)
	}
	if lp.Breakdown.Source != pricing.SourceQuantityBreak || lp.UnitPriceCents != fxBreakWalkInUnitCents {
		t.Fatalf("the break wins the walk-in's line: want QUANTITY_BREAK/%d, got %s/%d",
			fxBreakWalkInUnitCents, lp.Breakdown.Source, lp.UnitPriceCents)
	}
	if firstRun && lp.UnitPriceCents*600 != fxBreakWinLineCents {
		t.Fatalf("the committed line is the break's line: engine %d x 600 vs committed %d",
			lp.UnitPriceCents, fxBreakWinLineCents)
	}
}

// The walk-in's price through the counter resolve: the break REPLACES list at the threshold and
// leaves list alone below it.
func TestCounterTillBreak_WalkInTakesTheBreakPriceAtTheThreshold(t *testing.T) {
	pool := counterTillBreakFixture(t)
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
	pool := counterTillBreakFixture(t)
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
