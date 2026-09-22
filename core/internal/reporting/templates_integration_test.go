//go:build integration

package reporting

// templates_integration_test.go, THE ACCEPTANCE SPINE. One named test per report template, each
// proving that template's NUMBERS against a fixture whose arithmetic is written down in the test.
//
// The rule every test here follows: the expected figures are computed BY HAND in the test body, from
// the fixture the test just wrote, and compared against what the report returned. Not "the report
// returns something", not "the report returns the same as a second query", the number a person would
// get with a calculator. A report suite that checks a query against another query proves the two agree
// and nothing about whether either is right.
//
// Run:
//
//	DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:5432/swarm6_b?sslmode=disable' \
//	  go test -tags=integration ./internal/reporting/... -count=1 -shuffle=on

import (
	"context"
	"testing"
)

// -- SALES --------------------------------------------------------------------

// 1/22 · sales_by_day, one row per invoiced day, and DRAFT/VOID excluded.
func TestIntegration_Report_SalesByDay(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	from, to := window()
	day1, day2 := from, addDays(from, 1)

	cust := f.customer("acme")
	// day 1: two invoices, 100.00 + 5.00 tax, and 250.00 + 12.50 tax.
	f.invoicePaid(cust, trentonBranch, "", day1, 10_000, 500)
	f.invoicePaid(cust, trentonBranch, "", day1, 25_000, 1_250)
	// day 2: one invoice, 40.00 + 2.00 tax.
	f.invoicePaid(cust, trentonBranch, "", day2, 4_000, 200)
	// A VOID and a DRAFT on day 1 that must NOT appear anywhere in the numbers.
	f.invoice(cust, trentonBranch, "", day1, 999_999, 0, "VOID", 0)
	f.invoice(cust, trentonBranch, "", day1, 888_888, 0, "DRAFT", 0)

	res := run(t, svc, "sales_by_day", map[string]string{"from_date": from, "to_date": to})
	if len(res.Rows) != 2 {
		t.Fatalf("sales_by_day returned %d rows for a two-day fixture: %v", len(res.Rows), res.Rows)
	}
	wantText(t, res, 0, "day", day1)
	wantInt(t, res, 0, "invoice_count", 2)
	wantCents(t, res, 0, "subtotal_cents", 35_000)
	wantCents(t, res, 0, "tax_cents", 1_750)
	wantCents(t, res, 0, "total_cents", 36_750)

	wantText(t, res, 1, "day", day2)
	wantInt(t, res, 1, "invoice_count", 1)
	wantCents(t, res, 1, "total_cents", 4_200)

	wantTotalCents(t, res, "total_cents", 40_950)
	wantTotalCents(t, res, "invoice_count", 3)
}

// 2/22 · sales_by_branch, the same money split by branch, and the branch NAME resolved from locations.
func TestIntegration_Report_SalesByBranch(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	from, to := window()

	cust := f.customer("acme")
	f.invoicePaid(cust, trentonBranch, "", from, 10_000, 500)   // TRT
	f.invoicePaid(cust, trentonBranch, "", from, 20_000, 1_000) // TRT
	f.invoicePaid(cust, kingstonBranch, "", from, 7_500, 375)   // KGN

	res := run(t, svc, "sales_by_branch", map[string]string{"from_date": from, "to_date": to})
	if len(res.Rows) != 2 {
		t.Fatalf("sales_by_branch returned %d rows, want 2 (KGN, TRT): %v", len(res.Rows), res.Rows)
	}
	kgn := findRow(res, "branch_code", "KGN")
	trt := findRow(res, "branch_code", "TRT")
	if kgn < 0 || trt < 0 {
		t.Fatalf("expected both branch codes, got %v", res.Rows)
	}
	wantInt(t, res, trt, "invoice_count", 2)
	wantCents(t, res, trt, "subtotal_cents", 30_000)
	wantCents(t, res, trt, "total_cents", 31_500)
	wantInt(t, res, kgn, "invoice_count", 1)
	wantCents(t, res, kgn, "total_cents", 7_875)
	wantTotalCents(t, res, "total_cents", 39_375)
}

// 3/22 · sales_by_rep, the W5-D resolution order: order override beats the customer default, and an
// unassigned sale lands in an explicit house row rather than vanishing.
func TestIntegration_Report_SalesByRep(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	from, to := window()

	repA, repB := twoUsers(t, pool)
	custAssigned := f.customer("assigned")
	custHouse := f.customer("house")

	// customer-level default → repA
	f.repAssignment(custAssigned, "", repA)
	ordDefault := f.order(custAssigned, trentonBranch, from)
	f.invoicePaid(custAssigned, trentonBranch, ordDefault, from, 10_000, 0)

	// split book: an order override on the SAME customer → repB
	ordOverride := f.order(custAssigned, trentonBranch, from)
	f.repAssignment(custAssigned, ordOverride, repB)
	f.invoicePaid(custAssigned, trentonBranch, ordOverride, from, 30_000, 0)

	// nobody assigned → house queue
	ordHouse := f.order(custHouse, trentonBranch, from)
	f.invoicePaid(custHouse, trentonBranch, ordHouse, from, 5_000, 0)

	res := run(t, svc, "sales_by_rep", map[string]string{"from_date": from, "to_date": to})
	a := findRow(res, "rep_user_id", repA)
	b := findRow(res, "rep_user_id", repB)
	house := findRow(res, "rep_name", "Unassigned (house queue)")
	if a < 0 || b < 0 || house < 0 {
		t.Fatalf("expected rows for repA, repB and the house queue; got %v (cols %v)", res.Rows, columnNames(res))
	}
	wantCents(t, res, a, "total_cents", 10_000)
	wantCents(t, res, b, "total_cents", 30_000) // the ORDER OVERRIDE won over the customer default
	wantCents(t, res, house, "total_cents", 5_000)
	wantTotalCents(t, res, "total_cents", 45_000) // and the three add back to sales_by_day's figure
}

// 4/22 · sales_by_category, line revenue rolled to category, with uncategorised lines named rather
// than dropped.
func TestIntegration_Report_SalesByCategory(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	from, to := window()

	catA := f.category("pavers")
	prodA := f.product("PAV1", "Paver", catA)
	prodNone := f.product("MISC1", "Uncategorised", "")
	cust := f.customer("acme")

	inv := f.invoicePaid(cust, trentonBranch, "", from, 30_000, 0)
	f.invoiceLine(inv, prodA, 1, 10, 2_000, 20_000)
	f.invoiceLine(inv, prodNone, 2, 5, 2_000, 10_000)

	res := run(t, svc, "sales_by_category", map[string]string{"from_date": from, "to_date": to})
	if len(res.Rows) != 2 {
		t.Fatalf("sales_by_category returned %d rows, want 2: %v", len(res.Rows), res.Rows)
	}
	pav := findRow(res, "category_code", f.tag+"-pavers")
	un := findRow(res, "category_code", "(uncategorised)")
	if pav < 0 || un < 0 {
		t.Fatalf("expected the fixture category AND an uncategorised row; got %v", res.Rows)
	}
	wantCents(t, res, pav, "revenue_cents", 20_000)
	wantQty(t, res, pav, "qty_base", 10)
	wantCents(t, res, un, "revenue_cents", 10_000)
	wantTotalCents(t, res, "revenue_cents", 30_000)
}

// -- MARGIN -------------------------------------------------------------------

// 5/22 · margin_by_product, revenue, weighted-average cost, margin and margin_bps per SKU.
//
// The arithmetic, written out: 10 units received at $12.00 each is a $12.00 weighted average. Selling
// 10 at $20.00 is $200.00 revenue against $120.00 cost, $80.00 margin, 4000 bps.
func TestIntegration_Report_MarginByProduct(t *testing.T) {
	pool := intPool(t)
	dib396LeaseShared(t, pool, dib396StockDriftLease) // this fixture writes stock
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	from, to := window()

	cat := f.category("pavers")
	prod := f.product("PAV1", "Paver", cat)
	loc := f.branchLocation(trentonBranch)
	f.stockReceipt(prod, trentonBranch, loc, from+"T00:00:00Z", 10, 1_200)

	cust := f.customer("acme")
	inv := f.invoicePaid(cust, trentonBranch, "", from, 20_000, 0)
	f.invoiceLine(inv, prod, 1, 10, 2_000, 20_000)

	res := run(t, svc, "margin_by_product", map[string]string{
		"from_date": from, "to_date": to, "branch_id": trentonBranch})
	row := findRow(res, "sku", f.tag+"-PAV1")
	if row < 0 {
		t.Fatalf("the fixture SKU is missing from margin_by_product: %v", res.Rows)
	}
	wantCents(t, res, row, "revenue_cents", 20_000)
	wantCents(t, res, row, "cost_cents", 12_000)
	wantCents(t, res, row, "margin_cents", 8_000)
	wantInt(t, res, row, "margin_bps", 4_000) // 40.00%
	wantQty(t, res, row, "qty_base", 10)
}

// 6/22 · margin_by_category, the same, rolled up, with the ratio computed from the SUMMED figures
// rather than averaged from per-line percentages.
//
// Two SKUs in one category: 10 @ $20.00 costing $12.00 (40% margin) and 1 @ $10.00 costing $9.00 (10%).
// Revenue $210.00, cost $129.00, margin $81.00 → 3857 bps. Averaging the two percentages would give
// 2500 bps, which is the wrong answer this test exists to prevent.
func TestIntegration_Report_MarginByCategory(t *testing.T) {
	pool := intPool(t)
	dib396LeaseShared(t, pool, dib396StockDriftLease)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	from, to := window()

	cat := f.category("pavers")
	big := f.product("PAV1", "Paver", cat)
	small := f.product("PAV2", "Edger", cat)
	loc := f.branchLocation(trentonBranch)
	f.stockReceipt(big, trentonBranch, loc, from+"T00:00:00Z", 10, 1_200)
	f.stockReceipt(small, trentonBranch, loc, from+"T00:00:00Z", 1, 900)

	cust := f.customer("acme")
	inv := f.invoicePaid(cust, trentonBranch, "", from, 21_000, 0)
	f.invoiceLine(inv, big, 1, 10, 2_000, 20_000)
	f.invoiceLine(inv, small, 2, 1, 1_000, 1_000)

	res := run(t, svc, "margin_by_category", map[string]string{
		"from_date": from, "to_date": to, "branch_id": trentonBranch})
	row := findRow(res, "category_code", f.tag+"-pavers")
	if row < 0 {
		t.Fatalf("the fixture category is missing from margin_by_category: %v", res.Rows)
	}
	wantCents(t, res, row, "revenue_cents", 21_000)
	wantCents(t, res, row, "cost_cents", 12_900)
	wantCents(t, res, row, "margin_cents", 8_100)
	wantInt(t, res, row, "margin_bps", 3_857) // NOT 2500, the ratio is of the totals
}

// -- AR -----------------------------------------------------------------------

// 7/22 · ar_aging_detail, one row per open item, aged from the due date.
func TestIntegration_Report_ARAgingDetail(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	from, _ := window()

	cust := f.customer("debtor")
	// FINALIZED with an open balance is what puts an invoice in the aged-debt view.
	f.invoice(cust, trentonBranch, "", from, 10_000, 0, "FINALIZED", 10_000)
	f.invoice(cust, trentonBranch, "", from, 2_500, 0, "FINALIZED", 2_500)
	// A PAID invoice must NOT appear: it is not debt.
	f.invoicePaid(cust, trentonBranch, "", from, 99_999, 0)

	res := run(t, svc, "ar_aging_detail", map[string]string{"customer_id": cust})
	if len(res.Rows) != 2 {
		t.Fatalf("ar_aging_detail returned %d rows for two open invoices (a PAID one must not appear): %v",
			len(res.Rows), res.Rows)
	}
	for i := range res.Rows {
		wantText(t, res, i, "bucket", "90_PLUS") // a 1970s due date is comfortably past 90 days
		wantText(t, res, i, "branch_code", "TRT")
	}
	wantTotalCents(t, res, "amount_cents", 12_500)
}

// 8/22 · ar_aging_summary, the same money pivoted into buckets, one row per customer, and the bucket
// columns must add to the total column.
func TestIntegration_Report_ARAgingSummary(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	from, _ := window()

	cust := f.customer("debtor")
	f.invoice(cust, trentonBranch, "", from, 10_000, 0, "FINALIZED", 10_000)
	f.invoice(cust, trentonBranch, "", from, 2_500, 0, "FINALIZED", 2_500)

	res := run(t, svc, "ar_aging_summary", map[string]string{"customer_id": cust})
	if len(res.Rows) != 1 {
		t.Fatalf("ar_aging_summary returned %d rows for one customer: %v", len(res.Rows), res.Rows)
	}
	wantCents(t, res, 0, "days_90_plus_cents", 12_500)
	wantCents(t, res, 0, "current_cents", 0)
	wantCents(t, res, 0, "total_cents", 12_500)

	// The pivot must be exhaustive: the five buckets add to the total, or money has fallen off the report.
	var sum int64
	for _, name := range []string{"current_cents", "days_1_30_cents", "days_31_60_cents",
		"days_61_90_cents", "days_90_plus_cents"} {
		v, _ := cell(t, res, 0, name).(int64)
		sum += v
	}
	total, _ := cell(t, res, 0, "total_cents").(int64)
	if sum != total {
		t.Errorf("buckets sum to %d but the total column says %d, an item is in no bucket", sum, total)
	}
}

// 9/22 · statement_run_history, the month-end audit trail.
func TestIntegration_Report_StatementRunHistory(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	from, to := window()

	f.statementRun("MASS", trentonBranch, from, "COMPLETED", 12, 7)
	f.statementRun("INTERIM", "", addDays(from, 1), "FAILED", 0, 0)

	res := run(t, svc, "statement_run_history", map[string]string{"from_date": from, "to_date": to})
	if len(res.Rows) != 2 {
		t.Fatalf("statement_run_history returned %d rows, want 2: %v", len(res.Rows), res.Rows)
	}
	mass := findRow(res, "mode", "MASS")
	interim := findRow(res, "mode", "INTERIM")
	if mass < 0 || interim < 0 {
		t.Fatalf("expected one MASS and one INTERIM run: %v", res.Rows)
	}
	wantInt(t, res, mass, "generated_count", 12)
	wantInt(t, res, mass, "queued_count", 7)
	wantText(t, res, mass, "branch_code", "TRT")
	wantText(t, res, mass, "status", "COMPLETED")
	// A run with no branch is company-wide and says so, rather than showing a blank cell.
	wantText(t, res, interim, "branch_code", "(all)")
}

// -- INVENTORY ----------------------------------------------------------------

// 10/22 · inventory_valuation, on-hand times weighted-average cost, aggregated per product per BRANCH
// (not per location: a SKU sitting in three bays must not be counted three times).
func TestIntegration_Report_InventoryValuation(t *testing.T) {
	pool := intPool(t)
	dib396LeaseShared(t, pool, dib396StockDriftLease)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	from, _ := window()

	cat := f.category("pavers")
	prod := f.product("PAV1", "Paver", cat)
	loc := f.branchLocation(trentonBranch)
	// Two receipts at different costs: (10×1200 + 10×1400) / 20 = 1300 weighted average.
	f.stockReceipt(prod, trentonBranch, loc, from+"T00:00:00Z", 10, 1_200)
	f.stockReceipt(prod, trentonBranch, loc, from+"T01:00:00Z", 10, 1_400)

	res := run(t, svc, "inventory_valuation", map[string]string{"branch_id": trentonBranch})
	row := findRow(res, "sku", f.tag+"-PAV1")
	if row < 0 {
		t.Fatalf("the fixture SKU is missing from inventory_valuation")
	}
	wantQty(t, res, row, "on_hand_base", 20)
	wantCents(t, res, row, "unit_cost_cents", 1_300)
	wantCents(t, res, row, "value_cents", 26_000)
	wantText(t, res, row, "branch_code", "TRT")
}

// 11/22 · inventory_movement, moves per day per SKU, and the move_type filter narrows without
// changing the query text.
func TestIntegration_Report_InventoryMovement(t *testing.T) {
	pool := intPool(t)
	dib396LeaseShared(t, pool, dib396StockDriftLease)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	from, to := window()

	prod := f.product("PAV1", "Paver", f.category("pavers"))
	loc := f.branchLocation(trentonBranch)
	f.stockReceipt(prod, trentonBranch, loc, from+"T09:00:00Z", 10, 1_200)
	f.stockReceipt(prod, trentonBranch, loc, from+"T15:00:00Z", 5, 1_200)
	// An outbound ADJUST of 2. stock_moves stores magnitude + direction, so this is qty 2 leaving a
	// location, not -2 arriving at one.
	f.stockMove(prod, trentonBranch, loc, addDays(from, 1)+"T09:00:00Z", "ADJUST", 2, 1_200, true)

	all := run(t, svc, "inventory_movement", map[string]string{
		"from_date": from, "to_date": to, "branch_id": trentonBranch})
	day1 := findRow(all, "day", from)
	if day1 < 0 {
		t.Fatalf("inventory_movement has no row for the receipt day: %v", all.Rows)
	}
	wantInt(t, all, day1, "move_count", 2)
	wantQty(t, all, day1, "qty_base", 15)
	wantCents(t, all, day1, "value_cents", 18_000)

	// Narrowed to RECEIPT, the ADJUST row disappears and nothing else changes.
	receiptsOnly := run(t, svc, "inventory_movement", map[string]string{
		"from_date": from, "to_date": to, "branch_id": trentonBranch, "move_type": "RECEIPT"})
	if findRow(receiptsOnly, "move_type", "ADJUST") >= 0 {
		t.Error("move_type=RECEIPT still returned an ADJUST row")
	}
	if r := findRow(receiptsOnly, "day", from); r < 0 {
		t.Error("move_type=RECEIPT dropped the receipt day")
	} else {
		wantQty(t, receiptsOnly, r, "qty_base", 15)
	}
}

// 12/22 · below_reorder, only products actually below their point, with the shortfall arithmetic.
func TestIntegration_Report_BelowReorder(t *testing.T) {
	pool := intPool(t)
	dib396LeaseShared(t, pool, dib396StockDriftLease)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	from, _ := window()

	cat := f.category("pavers")
	short := f.product("PAV1", "Short", cat)
	fine := f.product("PAV2", "Fine", cat)
	loc := f.branchLocation(trentonBranch)
	vend := f.vendor("stoneco")

	f.stockReceipt(short, trentonBranch, loc, from+"T00:00:00Z", 4, 1_000)
	f.stockReceipt(fine, trentonBranch, loc, from+"T00:00:00Z", 50, 1_000)
	f.reorderPoint(short, trentonBranch, 10, 40, vend)
	f.reorderPoint(fine, trentonBranch, 10, 40, vend)

	res := run(t, svc, "below_reorder", map[string]string{"branch_id": trentonBranch})
	row := findRow(res, "sku", f.tag+"-PAV1")
	if row < 0 {
		t.Fatalf("the short SKU is missing from below_reorder: %v", res.Rows)
	}
	if findRow(res, "sku", f.tag+"-PAV2") >= 0 {
		t.Error("a product ABOVE its reorder point appeared on the reorder report")
	}
	wantQty(t, res, row, "on_hand_base", 4)
	wantQty(t, res, row, "reorder_point_base", 10)
	wantQty(t, res, row, "shortfall_base", 6)
	wantQty(t, res, row, "reorder_qty_base", 40)
	wantText(t, res, row, "preferred_vendor", f.tag+" stoneco")
}

// -- PURCHASING ---------------------------------------------------------------

// 13/22 · po_status, ordered vs received quantity and committed value, rolled from the lines.
func TestIntegration_Report_POStatus(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)

	vend := f.vendor("stoneco")
	prod := f.product("PAV1", "Paver", f.category("pavers"))
	po := f.purchaseOrder(vend, trentonBranch, "PARTIAL")
	f.poLine(po, 1, prod, 100, 1_000, 60) // 100 ordered @ $10.00, 60 received
	f.poLine(po, 2, prod, 20, 2_500, 0)   // 20 ordered @ $25.00, none received

	res := run(t, svc, "po_status", map[string]string{"vendor_id": vend})
	if len(res.Rows) != 1 {
		t.Fatalf("po_status returned %d rows for one PO: %v", len(res.Rows), res.Rows)
	}
	wantText(t, res, 0, "status", "PARTIAL")
	wantText(t, res, 0, "vendor_name", f.tag+" stoneco")
	wantQty(t, res, 0, "qty_ordered_base", 120)
	wantQty(t, res, 0, "qty_received_base", 60)
	wantCents(t, res, 0, "ordered_cents", 150_000) // 100×1000 + 20×2500
}

// 14/22 · receiving_history, one row per GRN with its quantity and landed value.
func TestIntegration_Report_ReceivingHistory(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	from, to := window()

	vend := f.vendor("stoneco")
	prod := f.product("PAV1", "Paver", f.category("pavers"))
	po := f.purchaseOrder(vend, trentonBranch, "PARTIAL")
	line := f.poLine(po, 1, prod, 100, 1_000, 60)
	rcpt := f.receipt(po, 1, from+"T10:00:00Z")
	f.receiptLine(rcpt, line, 40, 1_000, 40_000)
	f.receiptLine(rcpt, line, 20, 1_100, 22_000)

	res := run(t, svc, "receiving_history", map[string]string{"from_date": from, "to_date": to})
	if len(res.Rows) != 1 {
		t.Fatalf("receiving_history returned %d rows for one GRN: %v", len(res.Rows), res.Rows)
	}
	wantText(t, res, 0, "received_date", from)
	wantText(t, res, 0, "vendor_name", f.tag+" stoneco")
	wantQty(t, res, 0, "qty_base", 60)
	wantCents(t, res, 0, "value_cents", 62_000)
}

// 15/22 · three_way_variance, the AP exception list. MATCHED lines must be absent; that is what makes
// it a worklist rather than a listing.
func TestIntegration_Report_ThreeWayVariance(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	from, to := window()

	vend := f.vendor("stoneco")
	bill := f.bill(vend, trentonBranch, from, "VARIANCE")
	f.billLine(bill, 1, "over-billed pallet", "VARIANCE", 2, 1_500, 30_000)
	f.billLine(bill, 2, "short shipment", "SHORT_BILLED", -1, -1_000, 5_000)
	f.billLine(bill, 3, "clean line", "MATCHED", 0, 0, 10_000)

	res := run(t, svc, "three_way_variance", map[string]string{"from_date": from, "to_date": to})
	if len(res.Rows) != 2 {
		t.Fatalf("three_way_variance returned %d rows, want the 2 non-MATCHED lines: %v", len(res.Rows), res.Rows)
	}
	if findRow(res, "match_state", "MATCHED") >= 0 {
		t.Error("a MATCHED line appeared on the variance exception report")
	}
	v := findRow(res, "match_state", "VARIANCE")
	s := findRow(res, "match_state", "SHORT_BILLED")
	wantQty(t, res, v, "qty_variance_base", 2)
	wantCents(t, res, v, "price_variance_cents", 1_500)
	wantCents(t, res, s, "price_variance_cents", -1_000)
	wantTotalCents(t, res, "price_variance_cents", 500) // a negative variance must OFFSET, not accumulate
}

// -- TAX ----------------------------------------------------------------------

// 16/22 · tax_collected, the invoice's OWN tax, per day per branch. A VOID invoice's tax was never
// collected and must not be remitted.
func TestIntegration_Report_TaxCollected(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	from, to := window()

	cust := f.customer("acme")
	f.invoicePaid(cust, trentonBranch, "", from, 100_000, 13_000) // 13% HST
	f.invoicePaid(cust, trentonBranch, "", from, 50_000, 6_500)
	f.invoice(cust, trentonBranch, "", from, 999_999, 130_000, "VOID", 0)

	res := run(t, svc, "tax_collected", map[string]string{
		"from_date": from, "to_date": to, "branch_id": trentonBranch})
	if len(res.Rows) != 1 {
		t.Fatalf("tax_collected returned %d rows for one day at one branch: %v", len(res.Rows), res.Rows)
	}
	wantInt(t, res, 0, "invoice_count", 2)
	wantCents(t, res, 0, "taxable_base_cents", 150_000)
	wantCents(t, res, 0, "tax_cents", 19_500)
}

// -- PAYMENTS -----------------------------------------------------------------

// 17/22 · payments_by_method, RECEIVED and DEPOSITED only. A BOUNCED cheque is not a cash receipt, and
// counting it is how a bank reconciliation becomes impossible.
func TestIntegration_Report_PaymentsByMethod(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	from, to := window()

	cust := f.customer("acme")
	f.payment(cust, trentonBranch, "CASH", "RECEIVED", from+"T09:00:00Z", 10_000, 0)
	f.payment(cust, trentonBranch, "CASH", "DEPOSITED", from+"T10:00:00Z", 5_000, 0)
	f.payment(cust, trentonBranch, "CARD", "RECEIVED", from+"T11:00:00Z", 20_000, 480)
	f.payment(cust, trentonBranch, "CHEQUE", "BOUNCED", from+"T12:00:00Z", 99_999, 0)

	res := run(t, svc, "payments_by_method", map[string]string{
		"from_date": from, "to_date": to, "branch_id": trentonBranch})
	if len(res.Rows) != 2 {
		t.Fatalf("payments_by_method returned %d rows, want CASH and CARD (never the BOUNCED cheque): %v",
			len(res.Rows), res.Rows)
	}
	cash := findRow(res, "method", "CASH")
	card := findRow(res, "method", "CARD")
	wantInt(t, res, cash, "payment_count", 2)
	wantCents(t, res, cash, "amount_cents", 15_000)
	wantCents(t, res, card, "amount_cents", 20_000)
	wantCents(t, res, card, "surcharge_cents", 480)
	wantTotalCents(t, res, "amount_cents", 35_000)
}

// 18/22 · payments_by_day, the same money by day, and the two payment reports must agree.
func TestIntegration_Report_PaymentsByDay(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	from, to := window()
	day2 := addDays(from, 1)

	cust := f.customer("acme")
	f.payment(cust, trentonBranch, "CASH", "RECEIVED", from+"T09:00:00Z", 10_000, 0)
	f.payment(cust, trentonBranch, "CARD", "RECEIVED", from+"T10:00:00Z", 20_000, 480)
	f.payment(cust, trentonBranch, "EFT", "RECEIVED", day2+"T09:00:00Z", 7_500, 0)

	res := run(t, svc, "payments_by_day", map[string]string{
		"from_date": from, "to_date": to, "branch_id": trentonBranch})
	if len(res.Rows) != 2 {
		t.Fatalf("payments_by_day returned %d rows, want 2: %v", len(res.Rows), res.Rows)
	}
	wantText(t, res, 0, "day", from)
	wantInt(t, res, 0, "payment_count", 2)
	wantCents(t, res, 0, "amount_cents", 30_000)
	wantCents(t, res, 0, "surcharge_cents", 480)
	wantText(t, res, 1, "day", day2)
	wantCents(t, res, 1, "amount_cents", 7_500)

	// Cross-report tie-out: by-day and by-method are two cuts of one figure and must agree.
	byMethod := run(t, svc, "payments_by_method", map[string]string{
		"from_date": from, "to_date": to, "branch_id": trentonBranch})
	if res.Totals["amount_cents"] != byMethod.Totals["amount_cents"] {
		t.Errorf("payments_by_day totals %v but payments_by_method totals %v, two cuts of one number disagree",
			res.Totals["amount_cents"], byMethod.Totals["amount_cents"])
	}
}

// -- CREDIT -------------------------------------------------------------------

// 19/22 · credit_hold_history, who was stopped, why, by whom, and whether it was automatic.
func TestIntegration_Report_CreditHoldHistory(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	from, to := window()

	cust := f.customer("slowpayer")
	actor := fixtureActor(t, pool)
	f.creditHold(cust, "NSF", "ACTIVE", "cheque returned NSF", from+"T09:00:00Z", true, actor)
	f.creditHold(cust, "MANUAL", "RELEASED", "manager stop", addDays(from, 1)+"T09:00:00Z", false, actor)

	res := run(t, svc, "credit_hold_history", map[string]string{"from_date": from, "to_date": to})
	if len(res.Rows) != 2 {
		t.Fatalf("credit_hold_history returned %d rows, want 2: %v", len(res.Rows), res.Rows)
	}
	nsf := findRow(res, "hold_kind", "NSF")
	man := findRow(res, "hold_kind", "MANUAL")
	wantText(t, res, nsf, "status", "ACTIVE")
	wantText(t, res, nsf, "reason", "cheque returned NSF")
	wantText(t, res, nsf, "placed_date", from)
	if auto, _ := cell(t, res, nsf, "auto_placed").(bool); !auto {
		t.Error("an automatic NSF hold is reported as manual, the distinction is who to ask about it")
	}
	if auto, _ := cell(t, res, man, "auto_placed").(bool); auto {
		t.Error("a manual hold is reported as automatic")
	}

	// The status filter narrows to one.
	active := run(t, svc, "credit_hold_history", map[string]string{
		"from_date": from, "to_date": to, "status": "ACTIVE"})
	if len(active.Rows) != 1 || findRow(active, "hold_kind", "NSF") < 0 {
		t.Errorf("status=ACTIVE returned %d rows, want only the NSF hold: %v", len(active.Rows), active.Rows)
	}
}

// -- DELIVERY -----------------------------------------------------------------

// 20/22 · delivery_performance, stops, delivered, failed, and on-time (delivered on or before the
// scheduled day).
func TestIntegration_Report_DeliveryPerformance(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	from, to := window()

	cust := f.customer("acme")
	onTime := f.order(cust, trentonBranch, from)
	late := f.order(cust, trentonBranch, from)
	failed := f.order(cust, trentonBranch, from)
	waiting := f.order(cust, trentonBranch, from)

	f.deliveryStop(trentonBranch, onTime, from, "DELIVERED", from+"T14:00:00Z", "")
	f.deliveryStop(trentonBranch, late, from, "DELIVERED", addDays(from, 2)+"T14:00:00Z", "")
	f.deliveryStop(trentonBranch, failed, from, "FAILED", "", "nobody on site")
	f.deliveryStop(trentonBranch, waiting, from, "READY", "", "")

	res := run(t, svc, "delivery_performance", map[string]string{
		"from_date": from, "to_date": to, "branch_id": trentonBranch})
	if len(res.Rows) != 1 {
		t.Fatalf("delivery_performance returned %d rows for one branch-day: %v", len(res.Rows), res.Rows)
	}
	wantInt(t, res, 0, "stop_count", 4)
	wantInt(t, res, 0, "delivered_count", 2)
	wantInt(t, res, 0, "failed_count", 1)
	wantInt(t, res, 0, "on_time_count", 1) // the two-days-late delivery counts as delivered, NOT on time
}

// -- WARRANTY -----------------------------------------------------------------

// 21/22 · warranty_by_vendor, claims and money per manufacturer, with a "(no vendor)" row rather than
// a silent drop for a claim nobody has attributed yet.
func TestIntegration_Report_WarrantyByVendor(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	from, to := window()

	cust := f.customer("acme")
	vend := f.vendor("stoneco")
	f.warrantyClaim(cust, trentonBranch, vend, "CLOSED", "VENDOR_CREDIT", from+"T09:00:00Z", 50_000, 45_000)
	f.warrantyClaim(cust, trentonBranch, vend, "REJECTED", "", from+"T10:00:00Z", 20_000, 0)
	f.warrantyClaim(cust, trentonBranch, "", "TRIAGE", "", from+"T11:00:00Z", 10_000, 0)

	res := run(t, svc, "warranty_by_vendor", map[string]string{"from_date": from, "to_date": to})
	v := findRow(res, "vendor_name", f.tag+" stoneco")
	none := findRow(res, "vendor_name", "(no vendor)")
	if v < 0 || none < 0 {
		t.Fatalf("expected a vendor row AND a (no vendor) row: %v", res.Rows)
	}
	wantInt(t, res, v, "claim_count", 2)
	wantCents(t, res, v, "expected_cents", 70_000)
	wantCents(t, res, v, "realized_cents", 45_000)
	wantInt(t, res, v, "closed_count", 1)
	wantInt(t, res, v, "rejected_count", 1)
	wantInt(t, res, none, "claim_count", 1)
}

// 22/22 · warranty_by_status, the lifecycle cut. An open claim has no outcome yet and shows an empty
// one rather than being excluded.
func TestIntegration_Report_WarrantyByStatus(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	from, to := window()

	cust := f.customer("acme")
	vend := f.vendor("stoneco")
	f.warrantyClaim(cust, trentonBranch, vend, "CLOSED", "VENDOR_CREDIT", from+"T09:00:00Z", 50_000, 45_000)
	f.warrantyClaim(cust, trentonBranch, vend, "CLOSED", "WRITE_OFF", from+"T10:00:00Z", 30_000, 0)
	f.warrantyClaim(cust, trentonBranch, vend, "TRIAGE", "", from+"T11:00:00Z", 10_000, 0)

	res := run(t, svc, "warranty_by_status", map[string]string{
		"from_date": from, "to_date": to, "branch_id": trentonBranch})
	if len(res.Rows) != 3 {
		t.Fatalf("warranty_by_status returned %d rows, want 3 (two CLOSED outcomes + one open): %v",
			len(res.Rows), res.Rows)
	}
	credit := findRow(res, "outcome", "VENDOR_CREDIT")
	writeOff := findRow(res, "outcome", "WRITE_OFF")
	open := findRow(res, "outcome", "")
	if credit < 0 || writeOff < 0 || open < 0 {
		t.Fatalf("expected VENDOR_CREDIT, WRITE_OFF and an outcome-less open claim: %v", res.Rows)
	}
	wantCents(t, res, credit, "realized_cents", 45_000)
	wantCents(t, res, writeOff, "realized_cents", 0)
	wantText(t, res, open, "phase", "TRIAGE")
	wantTotalCents(t, res, "expected_cents", 90_000)
}

// -- column-allowlist enforcement, proven at runtime --------------------------

// TestIntegration_ColumnAllowlistIsCheckedAgainstTheDatabase drives the drift path for real: a template
// whose declared columns no longer match what Postgres returns must FAIL, not leak.
func TestIntegration_ColumnAllowlistIsCheckedAgainstTheDatabase(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	tmpl, err := repo.GetTemplate(context.Background(), "sales_by_day")
	if err != nil {
		t.Fatal(err)
	}
	// Same query, one fewer declared column: exactly the shape of "someone edited the SQL and forgot
	// the allowlist", inverted so the test can build it without editing the catalog.
	drifted := tmpl
	drifted.Columns = tmpl.Columns[:len(tmpl.Columns)-1]
	from, to := window()
	args, _, err := bindParams(tmpl.Params, map[string]string{"from_date": from, "to_date": to})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runTemplate(context.Background(), pool, drifted, args); err == nil {
		t.Fatal("a template whose allowlist disagrees with its query RAN, the drift check is not enforcing")
	} else if !isColumnDrift(err) {
		t.Errorf("drift produced %v, want ErrColumnDrift so the handler answers 500 with the report named", err)
	}
}
