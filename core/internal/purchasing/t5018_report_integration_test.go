//go:build integration

// t5018_report_integration_test.go, the year's T5018 report against a real database: what a CRA
// Form T5018 is built from. The report's own date rule is proved as a pure function in
// t5018_report_test.go; this file proves the money it reads.
package purchasing

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// t5018Vendor creates a vendor, marks it a subcontractor with the business number given, and
// answers its id. An empty number marks it with no number on file.
func t5018Vendor(t *testing.T, h *harness, orgID, name, number string) string {
	t.Helper()
	v, err := h.svc.CreateVendor(h.ctx, orgID, CreateVendorRequest{Name: name})
	if err != nil {
		t.Fatalf("create vendor %s: %v", name, err)
	}
	yes := true
	in := SetVendorT5018Request{IsSubcontractor: &yes}
	if number != "" {
		in.BusinessNumber = &number
	}
	if _, err := h.svc.SetVendorT5018(h.ctx, orgID, v.ID, in); err != nil {
		t.Fatalf("mark vendor %s: %v", name, err)
	}
	return v.ID
}

// t5018PayOne raises a received purchase order against one vendor, bills it and pays it at the
// instant given. It answers the amount paid.
func t5018PayOne(t *testing.T, h *harness, vendorID string, unitCostCents int64, paidAt time.Time, tag string) int64 {
	t.Helper()
	prod := h.newProduct(t, tag)
	po, err := h.svc.CreatePO(h.ctx, CreatePORequest{
		VendorID: vendorID, BranchID: branchTRT, ExpectedAt: "2026-09-01",
		Lines: []AddLineParams{{ProductID: prod, QtyOrderedBase: 1, UOM: "EA", UnitCostCents: unitCostCents}},
	})
	if err != nil {
		t.Fatalf("create PO (%s): %v", tag, err)
	}
	h.cleanupPO(t, po.ID)
	placed, err := h.svc.Transition(h.ctx, po.ID, StatusOrdered)
	if err != nil {
		t.Fatalf("order PO (%s): %v", tag, err)
	}
	if _, err := h.svc.Receive(h.ctx, placed.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: placed.Lines[0].ID, QtyBase: 1}},
	}); err != nil {
		t.Fatalf("receive PO (%s): %v", tag, err)
	}
	day := paidAt.Format("2006-01-02")
	bill, err := h.svc.CreateBill(h.ctx, CreateBillRequest{
		BillNo:   "T5018-" + tag,
		POID:     placed.ID,
		BillDate: day,
		DueDate:  day,
		Lines: []BillLineParams{
			{Description: "subcontractor work", QtyBilledBase: 1, UnitCostCents: unitCostCents, SupplyKind: SupplyKindServices},
		},
	})
	if err != nil {
		t.Fatalf("create bill (%s): %v", tag, err)
	}
	posted, err := h.svc.PostBill(h.ctx, bill.ID, PostBillRequest{AcceptVariance: true, Note: "t5018 test"})
	if err != nil {
		t.Fatalf("post bill (%s): %v", tag, err)
	}
	key := "t5018-pay-" + tag
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = h.pool.Exec(bg, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE idempotency_key = $1)`, "purchasing.bill.payment:"+key)
		_, _ = h.pool.Exec(bg, `DELETE FROM gl_journals WHERE idempotency_key = $1`, "purchasing.bill.payment:"+key)
	})
	if _, err := h.svc.PayBill(h.ctx, posted.ID, PayBillRequest{
		AmountCents:    unitCostCents,
		PaidAt:         paidAt,
		Note:           "t5018 test payment",
		IdempotencyKey: key,
	}); err != nil {
		t.Fatalf("pay bill (%s): %v", tag, err)
	}
	return unitCostCents
}

// t5018Row finds a vendor's row in the report.
func t5018Row(report T5018Report, vendorID string) (T5018Row, bool) {
	for _, row := range report.Items {
		if row.VendorID == vendorID {
			return row, true
		}
	}
	return T5018Row{}, false
}

// TestIntegration_T5018Report_TheTotalIsTheYearsPaymentsToTheMarkedVendors: the report's total
// is the year's PayBill payments to the marked vendors, a vendor that was paid and is not marked
// is not on the form, and a contractor with no business number on file says so.
func TestIntegration_T5018Report_TheTotalIsTheYearsPaymentsToTheMarkedVendors(t *testing.T) {
	rig := newAPAgingRig(t)
	h := rig.harness
	org := h.orgID(t)
	year := time.Now().In(rig.zone).Year()

	// The payment the boundary is about: 22:00 on the 31st of December, house time. In UTC it is
	// already the next year, so a report that filed it under UTC would answer the CRA a different
	// year than the one the dealer paid in.
	paidAt := time.Date(year, time.December, 31, 22, 0, 0, 0, rig.zone)
	if paidAt.UTC().Year() == year {
		t.Fatalf("the payment at %s is not the UTC year boundary the rule is about", paidAt)
	}

	markedWith := t5018Vendor(t, h, org, "T5018 Marked With Number (demo)", "123456789")
	markedWithout := t5018Vendor(t, h, org, "T5018 Marked Without Number (demo)", "")
	unmarked := t5018Vendor(t, h, org, "T5018 Not Marked (demo)", "")
	// The unmarked one is not marked: it is a plain vendor that was paid, so the report must
	// leave it off the form.
	no := false
	if _, err := h.svc.SetVendorT5018(h.ctx, org, unmarked, SetVendorT5018Request{IsSubcontractor: &no}); err != nil {
		t.Fatalf("clear the mark on the unmarked vendor: %v", err)
	}

	markedPaid := t5018PayOne(t, h, markedWith, 125000, paidAt, fmt.Sprintf("marked-%d", year))
	t5018PayOne(t, h, h.vendor, 45000, paidAt, fmt.Sprintf("seeded-%d", year))
	unmarkedPaid := t5018PayOne(t, h, unmarked, 33000, paidAt, fmt.Sprintf("unmarked-%d", year))

	report, err := h.svc.T5018ReportForCaller(h.ctx, org, fmt.Sprintf("%d", year), "")
	if err != nil {
		t.Fatalf("the T5018 report: %v", err)
	}
	if report.Year != year {
		t.Fatalf("the report reads year %d, want %d", report.Year, year)
	}

	row, found := t5018Row(report, markedWith)
	if !found {
		t.Fatalf("the report omits the marked vendor:\n%+v", report.Items)
	}
	if row.PaidCents != markedPaid {
		t.Fatalf("the marked vendor reads %d paid cents, want %d", row.PaidCents, markedPaid)
	}
	if row.BusinessNumber != "123456789" || row.BusinessNumberMissing {
		t.Fatalf("the marked vendor reads business number %q missing %v, want 123456789 and not missing",
			row.BusinessNumber, row.BusinessNumberMissing)
	}

	// A marked vendor with no number on file says so: the mark, not a blank cell.
	row, found = t5018Row(report, markedWithout)
	if !found {
		t.Fatalf("the report omits the marked vendor with no number:\n%+v", report.Items)
	}
	if !row.BusinessNumberMissing || row.BusinessNumber != "" {
		t.Fatalf("the marked vendor reads business number %q missing %v, want a blank and the mark",
			row.BusinessNumber, row.BusinessNumberMissing)
	}
	if row.PaidCents != 0 {
		t.Fatalf("the marked vendor nobody was paid reads %d paid cents, want 0", row.PaidCents)
	}

	// A vendor that was paid and is not marked is not on the form.
	if _, found := t5018Row(report, unmarked); found {
		t.Fatalf("the report carries a vendor that was not marked:\n%+v", report.Items)
	}
	if report.TotalCents != markedPaid {
		t.Fatalf("the report reads %d total cents, want %d", report.TotalCents, markedPaid)
	}
	_ = unmarkedPaid

	// The next year answers for itself: the payment was made in this year, so the next year's
	// report carries the marked vendor with nothing paid.
	next, err := h.svc.T5018ReportForCaller(h.ctx, org, fmt.Sprintf("%d", year+1), "")
	if err != nil {
		t.Fatalf("the next year's T5018 report: %v", err)
	}
	row, found = t5018Row(next, markedWith)
	if !found {
		t.Fatalf("the next year's report omits the marked vendor:\n%+v", next.Items)
	}
	if row.PaidCents != 0 {
		t.Fatalf("the next year's report reads %d paid cents for the marked vendor, want 0", row.PaidCents)
	}
	if next.TotalCents != 0 {
		t.Fatalf("the next year's report reads %d total cents, want 0", next.TotalCents)
	}
}

// TestIntegration_T5018Report_TheCSVIsTheReportInItsFileForm: the file carries the rows, the
// missing mark and the total, so the file a bookkeeper hands the contractor's book says what the
// screen says.
func TestIntegration_T5018Report_TheCSVIsTheReportInItsFileForm(t *testing.T) {
	rig := newAPAgingRig(t)
	h := rig.harness
	org := h.orgID(t)
	year := time.Now().In(rig.zone).Year()
	paidAt := time.Date(year, time.December, 31, 22, 0, 0, 0, rig.zone)

	marked := t5018Vendor(t, h, org, "T5018 CSV Marked (demo)", "")
	without := t5018Vendor(t, h, org, "T5018 CSV Without Number (demo)", "")
	paid := t5018PayOne(t, h, marked, 25000, paidAt, fmt.Sprintf("csv-%d", year))
	_ = without

	report, err := h.svc.T5018ReportForCaller(h.ctx, org, fmt.Sprintf("%d", year), "")
	if err != nil {
		t.Fatalf("the T5018 report: %v", err)
	}
	body, err := EncodeT5018ReportCSV(report)
	if err != nil {
		t.Fatalf("encode the report: %v", err)
	}
	text := string(body)
	if !strings.HasPrefix(text, T5018ConstructionIncomeNote+"\n") {
		t.Fatalf("the file has no construction income header line:\n%s", text)
	}
	if !strings.Contains(text, "vendor_no,vendor_name,business_number,paid_cents,box_22_cents,services_subtotal_cents,under_threshold,threshold_note,unmarked_line_count,unmarked_lines_note") {
		t.Fatalf("the file has no CRA column head:\n%s", text)
	}
	if !strings.Contains(text, T5018MissingNumber) {
		t.Fatalf("the file carries no missing mark:\n%s", text)
	}
	if !strings.Contains(text, fmt.Sprintf("%d", paid)) {
		t.Fatalf("the file carries no paid cents:\n%s", text)
	}
	if !strings.Contains(text, T5018UnderThresholdNote) {
		t.Fatalf("the file carries no under-threshold mark:\n%s", text)
	}
}

// t5018BillAndPay creates a PO for vendorID, orders and receives it, enters a bill with the given
// lines and taxCents, posts it, and pays it in full at paidAt.
func t5018BillAndPay(t *testing.T, h *harness, vendorID string, lines []BillLineParams, taxCents int64, paidAt time.Time, tag string) (poLineID string) {
	t.Helper()
	prod := h.newProduct(t, tag)
	po, err := h.svc.CreatePO(h.ctx, CreatePORequest{
		VendorID: vendorID, BranchID: branchTRT, ExpectedAt: "2026-09-01",
		Lines: []AddLineParams{{ProductID: prod, QtyOrderedBase: 1, UOM: "EA", UnitCostCents: 10000}},
	})
	if err != nil {
		t.Fatalf("create PO (%s): %v", tag, err)
	}
	h.cleanupPO(t, po.ID)
	placed, err := h.svc.Transition(h.ctx, po.ID, StatusOrdered)
	if err != nil {
		t.Fatalf("order PO (%s): %v", tag, err)
	}
	if _, err := h.svc.Receive(h.ctx, placed.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: placed.Lines[0].ID, QtyBase: 1}},
	}); err != nil {
		t.Fatalf("receive PO (%s): %v", tag, err)
	}
	poLineID = placed.Lines[0].ID
	resolved := make([]BillLineParams, len(lines))
	var subtotal int64
	for i, l := range lines {
		if l.POLineID == "PO" {
			l.POLineID = poLineID
		}
		resolved[i] = l
		subtotal += int64(l.QtyBilledBase) * l.UnitCostCents
	}
	day := paidAt.Format("2006-01-02")
	bill, err := h.svc.CreateBill(h.ctx, CreateBillRequest{
		BillNo:   "T5018-CRA-" + tag,
		POID:     placed.ID,
		BillDate: day,
		DueDate:  day,
		TaxCents: taxCents,
		Lines:    resolved,
	})
	if err != nil {
		t.Fatalf("create bill (%s): %v", tag, err)
	}
	posted, err := h.svc.PostBill(h.ctx, bill.ID, PostBillRequest{AcceptVariance: true, Note: "t5018 cra test"})
	if err != nil {
		t.Fatalf("post bill (%s): %v", tag, err)
	}
	key := "t5018-cra-pay-" + tag
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = h.pool.Exec(bg, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE idempotency_key = $1)`, "purchasing.bill.payment:"+key)
		_, _ = h.pool.Exec(bg, `DELETE FROM gl_journals WHERE idempotency_key = $1`, "purchasing.bill.payment:"+key)
	})
	if _, err := h.svc.PayBill(h.ctx, posted.ID, PayBillRequest{
		AmountCents:    subtotal + taxCents,
		PaidAt:         paidAt,
		Note:           "t5018 cra test payment",
		IdempotencyKey: key,
	}); err != nil {
		t.Fatalf("pay bill (%s): %v", tag, err)
	}
	return poLineID
}

// TestIntegration_T5018Report_CRAServicesShareThresholdTaxAndUnmarkedLines proves the five CRA
// T5018 legs and the construction-income sentence against the real database:
// 1. A goods-only bill is excluded.
// 2. A mixed bill counts only its services part (pro rata, half to even, in integer cents).
// 3. $480.00 of services plus 13% HST ($542.40) is under the $500.00 pre-tax threshold.
// 4. Box 22 carries the tax ($542.40, not $480.00).
// 5. An unmarked line counts as services, with the per-vendor words.
func TestIntegration_T5018Report_CRAServicesShareThresholdTaxAndUnmarkedLines(t *testing.T) {
	rig := newAPAgingRig(t)
	h := rig.harness
	org := h.orgID(t)
	year := time.Now().In(rig.zone).Year()
	paidAt := time.Date(year, time.June, 15, 14, 0, 0, 0, rig.zone)

	vGoods := t5018Vendor(t, h, org, "T5018 Goods Only (demo)", "111222333")
	vMixed := t5018Vendor(t, h, org, "T5018 Mixed Bill (demo)", "222333444")
	vUnder := t5018Vendor(t, h, org, "T5018 Under Threshold With HST (demo)", "333444555")
	vUnmarked := t5018Vendor(t, h, org, "T5018 Unmarked Line (demo)", "444555666")

	// Leg 1: goods-only bill ($800.00 goods + $104.00 HST = $904.00) contributes nothing.
	t5018BillAndPay(t, h, vGoods, []BillLineParams{
		{POLineID: "PO", QtyBilledBase: 1, UnitCostCents: 80000},
	}, 10400, paidAt, fmt.Sprintf("goods-%d", year))

	// Leg 2: mixed bill ($600.00 services + $400.00 goods = $1,000.00 subtotal, + $130.00 HST = $1,130.00).
	// Services share is 60%: pre-tax services subtotal = 60000 ($600.00), Box 22 = 67800 ($678.00).
	t5018BillAndPay(t, h, vMixed, []BillLineParams{
		{POLineID: "PO", QtyBilledBase: 1, UnitCostCents: 40000},
		{Description: "retaining wall installation", QtyBilledBase: 1, UnitCostCents: 60000, SupplyKind: SupplyKindServices},
	}, 13000, paidAt, fmt.Sprintf("mixed-%d", year))

	// Legs 3 & 4: $480.00 services + 13% HST ($62.40) = $542.40 paid.
	// Pre-tax services subtotal is 48000 (< 50000), so UnderThreshold is true with the note,
	// while Box 22 carries the tax (54240 cents = $542.40).
	t5018BillAndPay(t, h, vUnder, []BillLineParams{
		{Description: "site grading", QtyBilledBase: 1, UnitCostCents: 48000, SupplyKind: SupplyKindServices},
	}, 6240, paidAt, fmt.Sprintf("under-%d", year))

	// Leg 5: unmarked line (no PO line, no supply_kind) for $700.00 + 13% HST ($91.00) = $791.00.
	// Counts as services (ServicesSubtotalCents = 70000, Box22Cents = 79100) and carries the words.
	t5018BillAndPay(t, h, vUnmarked, []BillLineParams{
		{Description: "excavation charge", QtyBilledBase: 1, UnitCostCents: 70000},
	}, 9100, paidAt, fmt.Sprintf("unmarked-%d", year))

	report, err := h.svc.T5018ReportForCaller(h.ctx, org, fmt.Sprintf("%d", year), "")
	if err != nil {
		t.Fatalf("T5018 report: %v", err)
	}
	if report.RuleNote != T5018ConstructionIncomeNote {
		t.Fatalf("rule_note = %q, want %q", report.RuleNote, T5018ConstructionIncomeNote)
	}

	// Leg 1 assertions: goods-only bill excluded.
	rGoods, ok := t5018Row(report, vGoods)
	if !ok {
		t.Fatal("goods-only vendor missing from report")
	}
	if rGoods.Box22Cents != 0 || rGoods.PaidCents != 0 || rGoods.ServicesSubtotalCents != 0 {
		t.Fatalf("goods-only vendor = %+v, want 0 Box22Cents, 0 PaidCents, 0 ServicesSubtotalCents", rGoods)
	}
	if !rGoods.UnderThreshold || rGoods.ThresholdNote != T5018UnderThresholdNote {
		t.Fatalf("goods-only vendor threshold = %v / %q, want true / %q",
			rGoods.UnderThreshold, rGoods.ThresholdNote, T5018UnderThresholdNote)
	}

	// Leg 2 assertions: mixed bill counts its services part.
	rMixed, ok := t5018Row(report, vMixed)
	if !ok {
		t.Fatal("mixed vendor missing from report")
	}
	if rMixed.ServicesSubtotalCents != 60000 {
		t.Fatalf("mixed vendor services_subtotal_cents = %d, want 60000", rMixed.ServicesSubtotalCents)
	}
	if rMixed.Box22Cents != 67800 || rMixed.PaidCents != 67800 {
		t.Fatalf("mixed vendor box_22_cents = %d (paid_cents = %d), want 67800", rMixed.Box22Cents, rMixed.PaidCents)
	}
	if rMixed.UnderThreshold || rMixed.ThresholdNote != "" {
		t.Fatalf("mixed vendor threshold = %v / %q, want false / empty", rMixed.UnderThreshold, rMixed.ThresholdNote)
	}

	// Legs 3 & 4 assertions: $480.00 + 13% HST ($542.40) is under threshold, and Box 22 carries the tax.
	rUnder, ok := t5018Row(report, vUnder)
	if !ok {
		t.Fatal("under-threshold vendor missing from report")
	}
	if rUnder.ServicesSubtotalCents != 48000 {
		t.Fatalf("under-threshold vendor services_subtotal_cents = %d, want 48000", rUnder.ServicesSubtotalCents)
	}
	if !rUnder.UnderThreshold || rUnder.ThresholdNote != T5018UnderThresholdNote {
		t.Fatalf("under-threshold vendor = %v / %q, want true / %q",
			rUnder.UnderThreshold, rUnder.ThresholdNote, T5018UnderThresholdNote)
	}
	if rUnder.Box22Cents != 54240 || rUnder.PaidCents != 54240 {
		t.Fatalf("under-threshold vendor box_22_cents = %d (paid_cents = %d), want 54240 (tax included)",
			rUnder.Box22Cents, rUnder.PaidCents)
	}

	// Leg 5 assertions: unmarked line counts as services, with the words.
	rUnmarked, ok := t5018Row(report, vUnmarked)
	if !ok {
		t.Fatal("unmarked-line vendor missing from report")
	}
	if rUnmarked.ServicesSubtotalCents != 70000 || rUnmarked.Box22Cents != 79100 {
		t.Fatalf("unmarked-line vendor services = %d, box_22 = %d, want 70000 and 79100",
			rUnmarked.ServicesSubtotalCents, rUnmarked.Box22Cents)
	}
	wantUnmarkedNote := "1 bill lines are not marked goods or services; they are counted as services until marked"
	if rUnmarked.UnmarkedLineCount != 1 || rUnmarked.UnmarkedLinesNote != wantUnmarkedNote {
		t.Fatalf("unmarked-line vendor count = %d note = %q, want 1 and %q",
			rUnmarked.UnmarkedLineCount, rUnmarked.UnmarkedLinesNote, wantUnmarkedNote)
	}
}
