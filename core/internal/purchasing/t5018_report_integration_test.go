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
		Lines:    []BillLineParams{{POLineID: placed.Lines[0].ID, QtyBilledBase: 1, UnitCostCents: unitCostCents}},
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
	if !strings.Contains(text, "vendor_no,vendor_name,business_number,paid_cents") {
		t.Fatalf("the file has no head:\n%s", text)
	}
	if !strings.Contains(text, T5018MissingNumber) {
		t.Fatalf("the file carries no missing mark:\n%s", text)
	}
	if !strings.Contains(text, fmt.Sprintf("%d", paid)) {
		t.Fatalf("the file carries no paid cents:\n%s", text)
	}
}
