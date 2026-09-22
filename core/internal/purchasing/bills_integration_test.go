//go:build integration

// bills_integration_test.go, the acceptance evidence for item 2 (AP bills + three-way match + GL posting)
// and item 3 (the landed-cost tie-in), against the REAL gl and landedcost services.
package purchasing

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// billNo returns a per-run vendor invoice number (bill_no is UNIQUE per vendor, the duplicate-payment control).
func billNo(tag string) string { return fmt.Sprintf("INV-%d-%s", time.Now().UnixNano(), tag) }

// journalLegs reads back the posted journal as account_code → (debit, credit).
func (h *harness) journalLegs(t *testing.T, journalID string) map[string][2]int64 {
	t.Helper()
	rows, err := h.pool.Query(h.ctx, `
		SELECT a.code::text, COALESCE(l.debit_cents,0), COALESCE(l.credit_cents,0)
		  FROM gl_journal_lines l JOIN gl_accounts a ON a.id = l.account_id
		 WHERE l.journal_id = $1 ORDER BY l.line_no`, journalID)
	if err != nil {
		t.Fatalf("read journal legs: %v", err)
	}
	defer rows.Close()
	out := map[string][2]int64{}
	for rows.Next() {
		var code string
		var dr, cr int64
		if err := rows.Scan(&code, &dr, &cr); err != nil {
			t.Fatal(err)
		}
		cur := out[code]
		out[code] = [2]int64{cur[0] + dr, cur[1] + cr}
	}
	return out
}

// TestIntegration_Bill_MatchedPostsCleanGRNIRelief, the ordinary case. A bill that agrees with the PO and
// the receipt relieves GRNI exactly and books no variance at all.
func TestIntegration_Bill_MatchedPostsCleanGRNIRelief(t *testing.T) {
	h := newHarness(t)
	product := h.newProduct(t, "billmatch")
	po := h.orderedPO(t, product, 10, 2500)
	lineID := po.Lines[0].ID

	if _, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: lineID, QtyBase: 10}},
	}); err != nil {
		t.Fatalf("receive: %v", err)
	}

	bill, err := h.svc.CreateBill(h.ctx, CreateBillRequest{
		BillNo: billNo("match"), POID: po.ID, BillDate: "2026-08-01", DueDate: "2026-08-31",
		Lines: []BillLineParams{{POLineID: lineID, QtyBilledBase: 10, UnitCostCents: 2500}},
	})
	if err != nil {
		t.Fatalf("create bill: %v", err)
	}
	if bill.MatchState != MatchMatched {
		t.Fatalf("match_state = %s, want MATCHED", bill.MatchState)
	}
	if bill.TotalCents != 25000 {
		t.Fatalf("total = %d, want 25000", bill.TotalCents)
	}

	posted, err := h.svc.PostBill(h.ctx, bill.ID, PostBillRequest{})
	if err != nil {
		t.Fatalf("post a MATCHED bill must not need an acceptance: %v", err)
	}
	if posted.Status != BillPosted || posted.GLJournalID == "" {
		t.Fatalf("posted bill = status %s journal %q", posted.Status, posted.GLJournalID)
	}
	if posted.VarianceByUser != "" {
		t.Fatalf("a MATCHED bill must record no variance acceptance, got %q", posted.VarianceByUser)
	}

	legs := h.journalLegs(t, posted.GLJournalID)
	if legs["2150"][0] != 25000 {
		t.Fatalf("DR 2150 = %d, want 25000 (the whole accrual relieved)", legs["2150"][0])
	}
	if legs["2100"][1] != 25000 {
		t.Fatalf("CR 2100 = %d, want 25000", legs["2100"][1])
	}
	if _, present := legs["5250"]; present {
		t.Fatalf("a MATCHED bill must book no variance leg, got %v", legs["5250"])
	}

	// The PO line's billed rollup advances, so a second bill for the same delivery cannot look clean.
	detail, err := h.svc.GetPO(h.ctx, po.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Lines[0].QtyBilledBase != 10 || detail.Lines[0].BilledCents != 25000 {
		t.Fatalf("billed rollup = %v / %d, want 10 / 25000",
			detail.Lines[0].QtyBilledBase, detail.Lines[0].BilledCents)
	}
	// Re-posting is refused.
	if _, err := h.svc.PostBill(h.ctx, bill.ID, PostBillRequest{}); err == nil {
		t.Fatal("posting a POSTED bill again must be refused")
	}
}

// TestIntegration_Bill_VarianceRefusedThenAcceptedAndPosted is the acceptance flow the Chromium walkthrough
// mirrors: a bill that disagrees with the PO cannot post until a named person accepts it, and when it does
// post the disagreement lands on its own account rather than disappearing into COGS.
func TestIntegration_Bill_VarianceRefusedThenAcceptedAndPosted(t *testing.T) {
	h := newHarness(t)
	product := h.newProduct(t, "billvar")
	po := h.orderedPO(t, product, 10, 2500)
	lineID := po.Lines[0].ID

	if _, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: lineID, QtyBase: 10}},
	}); err != nil {
		t.Fatalf("receive: %v", err)
	}

	// The vendor charged 2800¢/unit against a PO at 2500¢, 3000¢ over, well past the 2% / 500¢ tolerance.
	bill, err := h.svc.CreateBill(h.ctx, CreateBillRequest{
		BillNo: billNo("var"), POID: po.ID,
		Lines: []BillLineParams{{POLineID: lineID, QtyBilledBase: 10, UnitCostCents: 2800}},
	})
	if err != nil {
		t.Fatalf("create bill: %v", err)
	}
	if bill.MatchState != MatchVariance {
		t.Fatalf("match_state = %s, want VARIANCE", bill.MatchState)
	}
	if bill.Lines[0].PriceVarianceCents != 3000 {
		t.Fatalf("price variance = %d, want 3000", bill.Lines[0].PriceVarianceCents)
	}

	_, err = h.svc.PostBill(h.ctx, bill.ID, PostBillRequest{})
	if err == nil {
		t.Fatal("posting a VARIANCE bill without an acceptance must be refused")
	}
	if got := statusOf(err); got != 409 {
		t.Fatalf("status = %d, want 409", got)
	}
	// The refusal names the amount, so the person deciding knows what they are accepting.
	if !strings.Contains(err.Error(), "3000") || !strings.Contains(err.Error(), "accept_variance") {
		t.Fatalf("the refusal must name the variance and the way through: %q", err)
	}
	// Still DRAFT, still unposted.
	mid, err := h.svc.GetBill(h.ctx, bill.ID)
	if err != nil {
		t.Fatal(err)
	}
	if mid.Status != BillDraft || mid.GLJournalID != "" {
		t.Fatalf("a refused post left the bill as %s / journal %q", mid.Status, mid.GLJournalID)
	}

	posted, err := h.svc.PostBill(h.ctx, bill.ID, PostBillRequest{AcceptVariance: true, Note: "vendor confirmed a fuel surcharge"})
	if err != nil {
		t.Fatalf("post with acceptance: %v", err)
	}
	if posted.Status != BillPosted {
		t.Fatalf("status = %s, want POSTED", posted.Status)
	}
	if posted.VarianceByUser != h.user {
		t.Fatalf("the acceptance must record WHO: got %q, want %q", posted.VarianceByUser, h.user)
	}
	if posted.VarianceNote == "" {
		t.Fatal("the acceptance note must be recorded")
	}

	legs := h.journalLegs(t, posted.GLJournalID)
	if legs["2150"][0] != 25000 {
		t.Fatalf("DR 2150 = %d, want 25000, GRNI is relieved at the PO price, not the bill's", legs["2150"][0])
	}
	if legs["5250"][0] != 3000 {
		t.Fatalf("DR 5250 = %d, want 3000, the overcharge must land on the variance account", legs["5250"][0])
	}
	if legs["2100"][1] != 28000 {
		t.Fatalf("CR 2100 = %d, want 28000", legs["2100"][1])
	}
	assertBalanced(t, legs)
}

// TestIntegration_Bill_ShortBilledPostsWithoutAcceptance, a short bill costs nobody anything, so it is not
// gated. It leaves the un-billed accrual sitting in 2150, which is exactly what SHORT_BILLED means.
func TestIntegration_Bill_ShortBilledPostsWithoutAcceptance(t *testing.T) {
	h := newHarness(t)
	product := h.newProduct(t, "shortbill")
	po := h.orderedPO(t, product, 10, 2500)
	lineID := po.Lines[0].ID

	if _, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: lineID, QtyBase: 10}},
	}); err != nil {
		t.Fatalf("receive: %v", err)
	}
	bill, err := h.svc.CreateBill(h.ctx, CreateBillRequest{
		BillNo: billNo("short"), POID: po.ID,
		Lines: []BillLineParams{{POLineID: lineID, QtyBilledBase: 7, UnitCostCents: 2500}},
	})
	if err != nil {
		t.Fatalf("create bill: %v", err)
	}
	if bill.MatchState != MatchShortBilled {
		t.Fatalf("match_state = %s, want SHORT_BILLED", bill.MatchState)
	}
	posted, err := h.svc.PostBill(h.ctx, bill.ID, PostBillRequest{})
	if err != nil {
		t.Fatalf("a SHORT_BILLED bill must post without an acceptance: %v", err)
	}
	legs := h.journalLegs(t, posted.GLJournalID)
	if legs["2150"][0] != 17500 {
		t.Fatalf("DR 2150 = %d, want 17500, only the billed units are relieved", legs["2150"][0])
	}
	if legs["2100"][1] != 17500 {
		t.Fatalf("CR 2100 = %d, want 17500", legs["2100"][1])
	}
	if _, present := legs["5250"]; present {
		t.Fatal("a short bill at the agreed price has no price variance")
	}
	assertBalanced(t, legs)
	// 7500¢ of accrual is deliberately left in 2150, the trace of goods received and not yet invoiced.
}

// TestIntegration_Bill_MatchIsRecomputedAtPostTime, a bill entered before the goods landed says VARIANCE at
// entry; by the time it is posted the rest has arrived and it is MATCHED. Money moves at POST, so the verdict
// that gates it must describe the moment it moves.
func TestIntegration_Bill_MatchIsRecomputedAtPostTime(t *testing.T) {
	h := newHarness(t)
	product := h.newProduct(t, "recompute")
	po := h.orderedPO(t, product, 10, 1000)
	lineID := po.Lines[0].ID

	// Only 4 have landed when the invoice arrives for all 10.
	if _, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: lineID, QtyBase: 4}},
	}); err != nil {
		t.Fatalf("first receive: %v", err)
	}
	bill, err := h.svc.CreateBill(h.ctx, CreateBillRequest{
		BillNo: billNo("recompute"), POID: po.ID,
		Lines: []BillLineParams{{POLineID: lineID, QtyBilledBase: 10, UnitCostCents: 1000}},
	})
	if err != nil {
		t.Fatalf("create bill: %v", err)
	}
	if bill.MatchState != MatchVariance {
		t.Fatalf("at entry the bill is %s, want VARIANCE (10 billed against 4 received)", bill.MatchState)
	}

	// The rest arrives.
	if _, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: lineID, QtyBase: 6}},
	}); err != nil {
		t.Fatalf("second receive: %v", err)
	}

	posted, err := h.svc.PostBill(h.ctx, bill.ID, PostBillRequest{})
	if err != nil {
		t.Fatalf("once the goods landed the bill matches and needs no acceptance: %v", err)
	}
	if posted.MatchState != MatchMatched {
		t.Fatalf("match_state at post = %s, want MATCHED", posted.MatchState)
	}
	legs := h.journalLegs(t, posted.GLJournalID)
	if legs["2150"][0] != 10000 {
		t.Fatalf("DR 2150 = %d, want the full 10000 accrual", legs["2150"][0])
	}
	if _, present := legs["5250"]; present {
		t.Fatal("no variance leg once the quantities agree")
	}
}

// TestIntegration_Bill_TaxAndInstalments, a bill with input tax posts the tax leg, and its instalment
// schedule must add up to the total.
func TestIntegration_Bill_TaxAndInstalments(t *testing.T) {
	h := newHarness(t)
	product := h.newProduct(t, "instal")
	po := h.orderedPO(t, product, 10, 10000)
	lineID := po.Lines[0].ID

	if _, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: lineID, QtyBase: 10}},
	}); err != nil {
		t.Fatalf("receive: %v", err)
	}

	// 100000 subtotal + 13000 HST = 113000. A schedule that does not add up is refused.
	_, err := h.svc.CreateBill(h.ctx, CreateBillRequest{
		BillNo: billNo("badinstal"), POID: po.ID, TaxCents: 13000,
		Lines: []BillLineParams{{POLineID: lineID, QtyBilledBase: 10, UnitCostCents: 10000}},
		Instalments: []InstalmentParams{
			{DueDate: "2026-09-01", AmountCents: 50000},
			{DueDate: "2026-10-01", AmountCents: 50000},
		},
	})
	if err == nil {
		t.Fatal("an instalment schedule that does not add up to the bill must be refused")
	}
	if !strings.Contains(err.Error(), "out by") {
		t.Fatalf("the refusal must name the shortfall: %q", err)
	}

	bill, err := h.svc.CreateBill(h.ctx, CreateBillRequest{
		BillNo: billNo("instal"), POID: po.ID, TaxCents: 13000,
		Lines: []BillLineParams{{POLineID: lineID, QtyBilledBase: 10, UnitCostCents: 10000}},
		Instalments: []InstalmentParams{
			{DueDate: "2026-09-01", AmountCents: 56500, Note: "deposit"},
			{DueDate: "2026-10-01", AmountCents: 56500, Note: "balance"},
		},
	})
	if err != nil {
		t.Fatalf("create bill: %v", err)
	}
	if len(bill.Instalments) != 2 {
		t.Fatalf("bill holds %d instalments, want 2", len(bill.Instalments))
	}
	if bill.TotalCents != 113000 {
		t.Fatalf("total = %d, want 113000", bill.TotalCents)
	}

	posted, err := h.svc.PostBill(h.ctx, bill.ID, PostBillRequest{})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	legs := h.journalLegs(t, posted.GLJournalID)
	if legs["2200"][0] != 13000 {
		t.Fatalf("DR 2200 = %d, want 13000 (recoverable input tax)", legs["2200"][0])
	}
	if legs["2100"][1] != 113000 {
		t.Fatalf("CR 2100 = %d, want 113000 (subtotal + tax)", legs["2100"][1])
	}
	assertBalanced(t, legs)
}

// TestIntegration_Bill_DuplicateInvoiceNumberRefused, the duplicate-payment control.
func TestIntegration_Bill_DuplicateInvoiceNumberRefused(t *testing.T) {
	h := newHarness(t)
	product := h.newProduct(t, "dupbill")
	po := h.orderedPO(t, product, 10, 1000)
	lineID := po.Lines[0].ID
	if _, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: lineID, QtyBase: 10}},
	}); err != nil {
		t.Fatalf("receive: %v", err)
	}
	no := billNo("dup")
	req := CreateBillRequest{BillNo: no, POID: po.ID,
		Lines: []BillLineParams{{POLineID: lineID, QtyBilledBase: 5, UnitCostCents: 1000}}}
	if _, err := h.svc.CreateBill(h.ctx, req); err != nil {
		t.Fatalf("first entry: %v", err)
	}
	_, err := h.svc.CreateBill(h.ctx, req)
	if err == nil {
		t.Fatal("the same vendor invoice number must not be enterable twice")
	}
	if got := statusOf(err); got != 409 {
		t.Fatalf("status = %d, want 409", got)
	}
}

// TestIntegration_LandedCost_CapitalizesThroughTheExistingPath is item 3: attaching a freight invoice to a
// receipt raises the weighted-average cost of exactly the products on that receipt, entirely through
// internal/landedcost's existing Create+Apply path. Purchasing computes nothing.
func TestIntegration_LandedCost_CapitalizesThroughTheExistingPath(t *testing.T) {
	h := newHarness(t)
	product := h.newProduct(t, "landed")
	po := h.orderedPO(t, product, 10, 2500)
	lineID := po.Lines[0].ID

	rc, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: lineID, QtyBase: 10}},
	})
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	avgBefore := h.avgCost(t, product, branchTRT)
	if avgBefore != 2500 {
		t.Fatalf("weighted average after receipt = %d, want 2500", avgBefore)
	}

	// $50.00 of freight over 10 units = 500¢/unit.
	withLanded, err := h.svc.AttachLandedCost(h.ctx, rc.ID, LandedCostRequest{
		FreightCents: 5000, Basis: "VALUE",
	})
	if err != nil {
		t.Fatalf("attach landed cost: %v", err)
	}
	if withLanded.LandedCostID == "" {
		t.Fatal("the receipt must record which landed-cost document was applied to it")
	}
	if got := h.avgCost(t, product, branchTRT); got != 3000 {
		t.Fatalf("weighted average after 5000¢ of freight over 10 units = %d, want 3000", got)
	}

	// The landed cost is attached to the RECEIPT's receiving document, not the purchase order, a freight
	// invoice covers one truckload.
	var lcPOID string
	if err := h.pool.QueryRow(h.ctx, `SELECT po_id::text FROM landed_costs WHERE id = $1`, withLanded.LandedCostID).Scan(&lcPOID); err != nil {
		t.Fatalf("read landed cost: %v", err)
	}
	if lcPOID != rc.StockDocID {
		t.Fatalf("landed cost is attached to %s, want the receiving document %s", lcPOID, rc.StockDocID)
	}

	// One landed cost per receipt; a second freight invoice is a retro-adjustment, not a second apply.
	if _, err := h.svc.AttachLandedCost(h.ctx, rc.ID, LandedCostRequest{FreightCents: 100}); err == nil {
		t.Fatal("a second landed cost on the same receipt must be refused")
	}
}

// TestIntegration_LandedCost_PerReceiptNotPerPO, two receipts against one PO, freight on the first only.
// Only the first truckload's units carry the cost, which is the whole reason the tie-in is per receipt.
func TestIntegration_LandedCost_PerReceiptNotPerPO(t *testing.T) {
	h := newHarness(t)
	p1 := h.newProduct(t, "lc1")
	p2 := h.newProduct(t, "lc2")

	po, err := h.svc.CreatePO(h.ctx, CreatePORequest{
		VendorID: h.vendor, BranchID: branchTRT,
		Lines: []AddLineParams{
			{ProductID: p1, QtyOrderedBase: 10, UOM: "EA", UnitCostCents: 1000},
			{ProductID: p2, QtyOrderedBase: 10, UOM: "EA", UnitCostCents: 1000},
		},
	})
	if err != nil {
		t.Fatalf("create PO: %v", err)
	}
	h.cleanupPO(t, po.ID)
	if _, err := h.svc.Transition(h.ctx, po.ID, StatusOrdered); err != nil {
		t.Fatalf("order: %v", err)
	}

	// Truck one carries p1 only.
	rc1, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{Lines: []ReceiveLineParams{{POLineID: po.Lines[0].ID, QtyBase: 10}}})
	if err != nil {
		t.Fatalf("receive 1: %v", err)
	}
	// Truck two carries p2.
	if _, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{Lines: []ReceiveLineParams{{POLineID: po.Lines[1].ID, QtyBase: 10}}}); err != nil {
		t.Fatalf("receive 2: %v", err)
	}

	if _, err := h.svc.AttachLandedCost(h.ctx, rc1.ID, LandedCostRequest{FreightCents: 2000, Basis: "QUANTITY"}); err != nil {
		t.Fatalf("attach landed cost to the first truck: %v", err)
	}
	if got := h.avgCost(t, p1, branchTRT); got != 1200 {
		t.Fatalf("p1 (on the freighted truck) average = %d, want 1200", got)
	}
	if got := h.avgCost(t, p2, branchTRT); got != 1000 {
		t.Fatalf("p2 (on the other truck) average = %d, want 1000, freight must not spread across receipts", got)
	}
}

// TestIntegration_Bill_GLSeamUnwired_Refuses, a payable that books no journal is an invisible liability.
func TestIntegration_Bill_GLSeamUnwired_Refuses(t *testing.T) {
	h := newHarness(t)
	product := h.newProduct(t, "noGL")
	po := h.orderedPO(t, product, 4, 1000)
	lineID := po.Lines[0].ID
	if _, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: lineID, QtyBase: 4}},
	}); err != nil {
		t.Fatalf("receive: %v", err)
	}
	bill, err := h.svc.CreateBill(h.ctx, CreateBillRequest{
		BillNo: billNo("nogl"), POID: po.ID,
		Lines: []BillLineParams{{POLineID: lineID, QtyBilledBase: 4, UnitCostCents: 1000}},
	})
	if err != nil {
		t.Fatalf("create bill: %v", err)
	}

	noGL := NewService(NewRepository(h.pool)).
		WithVendors(itVendors{pool: h.pool}).
		withCaller(ctxCaller(h.user))
	if _, err := noGL.PostBill(h.ctx, bill.ID, PostBillRequest{}); err == nil {
		t.Fatal("posting a bill with no GL seam must be refused")
	} else if statusOf(err) != 503 {
		t.Fatalf("status = %d, want 503", statusOf(err))
	}

	after, err := h.svc.GetBill(h.ctx, bill.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != BillDraft || after.GLJournalID != "" {
		t.Fatalf("the refused post left the bill as %s / journal %q", after.Status, after.GLJournalID)
	}
}

func (h *harness) avgCost(t *testing.T, productID, branchID string) int64 {
	t.Helper()
	var c int64
	if err := h.pool.QueryRow(h.ctx,
		`SELECT avg_unit_cost_cents FROM product_branch_costs WHERE product_id = $1 AND branch_id = $2`,
		productID, branchID).Scan(&c); err != nil {
		t.Fatalf("weighted-average cost: %v", err)
	}
	return c
}

func assertBalanced(t *testing.T, legs map[string][2]int64) {
	t.Helper()
	var dr, cr int64
	for _, v := range legs {
		dr += v[0]
		cr += v[1]
	}
	if dr != cr {
		t.Fatalf("journal does not balance: DR %d ≠ CR %d (%v)", dr, cr, legs)
	}
	if dr == 0 {
		t.Fatal("journal has no legs")
	}
}
