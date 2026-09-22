//go:build integration

// dib421_currency_integration_test.go, DIB-421's acceptance evidence: a USD purchase order, received,
// billed and posted, with the CAD landing checked in the ledger inventory actually wrote.
//
// The unit half proves toCAD agrees with inventory's arithmetic. This half proves the conversion happens at
// the RIGHT BOUNDARIES, that nothing is converted twice, nothing is left foreign, and the number in
// stock_moves is the number purchasing believes it recorded.
package purchasing

import (
	"strings"
	"testing"
)

// usdRate is a real-looking rate that is not exactly representable in binary, so the test exercises the
// same rounding path a production rate would.
const usdRate = 1.37

// TestIntegration_DIB421_USDPurchaseOrderReceivesInCAD is the DIB-421 named test.
//
// A USD PO at $25.00/unit and fx 1.37 → $34.25 CAD per unit (2500 × 1.37 = 3425 exactly). Four units land.
// EVERY ledger downstream must read 3425 and 13700, and the PO itself must still read 2500 USD, because the
// buyer reconciles that against the vendor's order confirmation.
func TestIntegration_DIB421_USDPurchaseOrderReceivesInCAD(t *testing.T) {
	h := newHarness(t)
	product := h.newProduct(t, "usd")

	po, err := h.svc.CreatePO(h.ctx, CreatePORequest{
		VendorID: h.vendor, BranchID: branchTRT, Notes: "DIB-421 USD order",
		Currency: "USD", FXRate: usdRate,
		Lines: []AddLineParams{{ProductID: product, QtyOrderedBase: 10, UOM: "EA", UnitCostCents: 2500}},
	})
	if err != nil {
		t.Fatalf("create USD PO: %v", err)
	}
	h.cleanupPO(t, po.ID)

	if po.Currency != "USD" || po.FXRate != usdRate {
		t.Fatalf("the PO must carry its own currency + frozen rate, got %q / %v", po.Currency, po.FXRate)
	}
	// The LINE stays in the vendor's money. A buyer holding the order confirmation must see the number
	// the vendor wrote, not a converted one.
	if po.Lines[0].UnitCostCents != 2500 {
		t.Fatalf("the PO line must stay in the document currency, got %d, want 2500 USD cents", po.Lines[0].UnitCostCents)
	}

	if _, err := h.svc.Transition(h.ctx, po.ID, StatusOrdered); err != nil {
		t.Fatalf("place PO: %v", err)
	}
	before := h.onHand(t, product, branchTRT)

	rc, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: po.Lines[0].ID, QtyBase: 4}}, Notes: "USD truck",
	})
	if err != nil {
		t.Fatalf("receive: %v", err)
	}

	const wantUnitCad = 3425   // 2500 × 1.37
	const wantValueCad = 13700 // 4 × 3425

	// 1. Purchasing's own record.
	if rc.ValueCadCents != wantValueCad {
		t.Fatalf("receipt value = %d, want %d CAD cents (4 × 2500 USD × 1.37)", rc.ValueCadCents, wantValueCad)
	}
	if len(rc.Lines) != 1 || rc.Lines[0].UnitCostCents != wantUnitCad || rc.Lines[0].ValueCents != wantValueCad {
		t.Fatalf("receipt line must be CAD (%d/%d), got %+v", wantUnitCad, wantValueCad, rc.Lines)
	}

	// 2. THE STOCK LEDGER, the number inventory actually wrote, through the seam, in this transaction.
	//    This is the assertion the whole item exists for: a USD PO's received cost lands in CAD.
	var moveUnit, moveValue int64
	if err := h.pool.QueryRow(h.ctx, `
		SELECT unit_cost_cents, value_cents FROM stock_moves
		 WHERE product_id = $1 AND move_type = 'RECEIPT' ORDER BY created_at DESC LIMIT 1`,
		product).Scan(&moveUnit, &moveValue); err != nil {
		t.Fatalf("no RECEIPT stock move for the USD PO: %v", err)
	}
	if moveUnit != wantUnitCad || moveValue != wantValueCad {
		t.Fatalf("the stock move is %d/%d, want %d/%d CAD, a USD cost reached the stock ledger unconverted "+
			"(or was converted twice)", moveUnit, moveValue, wantUnitCad, wantValueCad)
	}

	// 3. The weighted average inventory maintains. If purchasing had handed over a USD document AND
	//    inventory had converted it too, this would read 4692 (3425 × 1.37) and nothing would say so.
	var avg int64
	if err := h.pool.QueryRow(h.ctx,
		`SELECT avg_unit_cost_cents FROM product_branch_costs WHERE product_id = $1 AND branch_id = $2`,
		product, branchTRT).Scan(&avg); err != nil {
		t.Fatalf("no weighted-average row: %v", err)
	}
	if avg != wantUnitCad {
		t.Fatalf("weighted average = %d, want %d CAD (a double conversion would read 4692)", avg, wantUnitCad)
	}

	// 4. Quantities are unaffected by any of this, a rate must never move a count.
	if got := h.onHand(t, product, branchTRT); got != before+4 {
		t.Fatalf("on-hand = %v, want %v", got, before+4)
	}

	// 5. The receiving document itself is written CAD / fx 1.0: purchasing has ALREADY converted, so the
	//    document must not invite a second conversion by anyone reading it later.
	var docCur string
	var docFX float64
	if err := h.pool.QueryRow(h.ctx,
		`SELECT currency::text, fx_rate::float8 FROM purchase_orders WHERE id = $1`, rc.StockDocID).
		Scan(&docCur, &docFX); err != nil {
		t.Fatalf("receiving document missing: %v", err)
	}
	if !strings.EqualFold(docCur, "CAD") || docFX != 1.0 {
		t.Fatalf("the receiving document must be CAD/1.0 (already converted), got %s/%v", docCur, docFX)
	}
}

// TestIntegration_DIB421_MatchComparesBothSidesInCAD is the second half of the boundary claim: the
// three-way match must never compare a CAD bill against a USD PO price. A vendor invoicing a USD order in
// CAD at the agreed price is a MATCH, not a 37% price variance.
func TestIntegration_DIB421_MatchComparesBothSidesInCAD(t *testing.T) {
	h := newHarness(t)
	product := h.newProduct(t, "usdmatch")

	po, err := h.svc.CreatePO(h.ctx, CreatePORequest{
		VendorID: h.vendor, BranchID: branchTRT, Currency: "USD", FXRate: usdRate,
		Lines: []AddLineParams{{ProductID: product, QtyOrderedBase: 4, UOM: "EA", UnitCostCents: 2500}},
	})
	if err != nil {
		t.Fatalf("create USD PO: %v", err)
	}
	h.cleanupPO(t, po.ID)
	if _, err := h.svc.Transition(h.ctx, po.ID, StatusOrdered); err != nil {
		t.Fatalf("place: %v", err)
	}
	if _, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: po.Lines[0].ID, QtyBase: 4}},
	}); err != nil {
		t.Fatalf("receive: %v", err)
	}

	// The vendor invoices the SAME goods in CAD, at the CAD equivalent of the agreed USD price.
	bill, err := h.svc.CreateBill(h.ctx, CreateBillRequest{
		BillNo: "DIB421-CAD-" + po.PONo, POID: po.ID, Currency: "CAD",
		Lines: []BillLineParams{{POLineID: po.Lines[0].ID, QtyBilledBase: 4, UnitCostCents: 3425}},
	})
	if err != nil {
		t.Fatalf("create CAD bill against a USD PO: %v", err)
	}

	if bill.MatchState != MatchMatched {
		t.Fatalf("a CAD bill at the agreed USD price must MATCH, got %s, the match compared two currencies",
			bill.MatchState)
	}
	if bill.Lines[0].PriceVarianceCents != 0 {
		t.Fatalf("price variance = %d, want 0 (both sides converted at their own document's rate)",
			bill.Lines[0].PriceVarianceCents)
	}
	if bill.TotalCents != 13700 {
		t.Fatalf("bill total = %d, want 13700 CAD", bill.TotalCents)
	}
}

// TestIntegration_DIB421_USDBillConvertsAtItsOwnRate is the mirror: the vendor invoices in USD. Every
// amount stored on the bill, subtotal, tax and total, is CAD, because AP, the four journal legs and the
// GL never handle a foreign amount, while currency + fx_rate record what the vendor actually wrote.
func TestIntegration_DIB421_USDBillConvertsAtItsOwnRate(t *testing.T) {
	h := newHarness(t)
	product := h.newProduct(t, "usdbill")

	po, err := h.svc.CreatePO(h.ctx, CreatePORequest{
		VendorID: h.vendor, BranchID: branchTRT, Currency: "USD", FXRate: usdRate,
		Lines: []AddLineParams{{ProductID: product, QtyOrderedBase: 4, UOM: "EA", UnitCostCents: 2500}},
	})
	if err != nil {
		t.Fatalf("create USD PO: %v", err)
	}
	h.cleanupPO(t, po.ID)
	if _, err := h.svc.Transition(h.ctx, po.ID, StatusOrdered); err != nil {
		t.Fatalf("place: %v", err)
	}
	if _, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: po.Lines[0].ID, QtyBase: 4}},
	}); err != nil {
		t.Fatalf("receive: %v", err)
	}

	// USD bill at the agreed USD price, plus $10.00 USD of tax.
	bill, err := h.svc.CreateBill(h.ctx, CreateBillRequest{
		BillNo: "DIB421-USD-" + po.PONo, POID: po.ID, Currency: "USD", FXRate: usdRate, TaxCents: 1000,
		Lines: []BillLineParams{{POLineID: po.Lines[0].ID, QtyBilledBase: 4, UnitCostCents: 2500}},
	})
	if err != nil {
		t.Fatalf("create USD bill: %v", err)
	}

	if bill.Currency != "USD" || bill.FXRate != usdRate {
		t.Fatalf("the bill must record what the vendor wrote, got %q / %v", bill.Currency, bill.FXRate)
	}
	if bill.MatchState != MatchMatched {
		t.Fatalf("a USD bill at the agreed USD price must MATCH, got %s", bill.MatchState)
	}
	if bill.SubtotalCents != 13700 {
		t.Fatalf("subtotal = %d, want 13700 CAD (4 × 2500 × 1.37)", bill.SubtotalCents)
	}
	if bill.TaxCents != 1370 {
		t.Fatalf("tax = %d, want 1370 CAD (1000 USD × 1.37), tax is on the vendor's invoice in the "+
			"vendor's money and converts with everything else", bill.TaxCents)
	}
	if bill.TotalCents != 15070 {
		t.Fatalf("total = %d, want 15070 CAD", bill.TotalCents)
	}

	// And the journal it posts is CAD to the cent: AP owes 15070, not 11000.
	posted, err := h.svc.PostBill(h.ctx, bill.ID, PostBillRequest{})
	if err != nil {
		t.Fatalf("post USD bill: %v", err)
	}
	if posted.GLJournalID == "" {
		t.Fatal("posting must produce a journal")
	}
	var apCents int64
	if err := h.pool.QueryRow(h.ctx, `
		SELECT COALESCE(sum(l.credit_cents),0) FROM gl_journal_lines l
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE l.journal_id = $1 AND a.code = '2100'`, posted.GLJournalID).Scan(&apCents); err != nil {
		t.Fatalf("read AP leg: %v", err)
	}
	if apCents != 15070 {
		t.Fatalf("AP credited %d, want 15070 CAD, a foreign amount reached the GL", apCents)
	}
}
