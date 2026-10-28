//go:build integration

// ap_subledger_integration_test.go, the seam read the month walk's AP tie consumes: the posted bill
// total as of a moment, with the as-of boundary held against the test's own bill so no other
// writer's state can move the assertion.
package purchasing

import (
	"testing"
	"time"
)

func TestIntegration_PostedBillTotalCentsAsOf(t *testing.T) {
	h := newHarness(t)
	product := h.newProduct(t, "apread")
	po := h.orderedPO(t, product, 10, 2500)
	lineID := po.Lines[0].ID

	if _, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: lineID, QtyBase: 10}},
	}); err != nil {
		t.Fatalf("receive: %v", err)
	}

	// The reads bracket the test's own bill, so every other bill in the window cancels.
	before, err := h.svc.PostedBillTotalCentsAsOf(h.ctx, time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("posted bill total before the bill: %v", err)
	}

	bill, err := h.svc.CreateBill(h.ctx, CreateBillRequest{
		BillNo: billNo("apread"), POID: po.ID, BillDate: "2026-08-01", DueDate: "2026-08-31",
		Lines: []BillLineParams{{POLineID: lineID, QtyBilledBase: 10, UnitCostCents: 2500}},
	})
	if err != nil {
		t.Fatalf("create bill: %v", err)
	}
	if bill.TotalCents != 25000 {
		t.Fatalf("bill total = %d, want 25000", bill.TotalCents)
	}
	if _, err := h.svc.PostBill(h.ctx, bill.ID, PostBillRequest{}); err != nil {
		t.Fatalf("post bill: %v", err)
	}

	// After the post the bill is in the balance.
	after, err := h.svc.PostedBillTotalCentsAsOf(h.ctx, time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("posted bill total as of the 15th: %v", err)
	}
	if after != before+25000 {
		t.Fatalf("posted bill total moved %d, want exactly the bill's 25000", after-before)
	}

	// Before the bill's date the read does not count it.
	early, err := h.svc.PostedBillTotalCentsAsOf(h.ctx, time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("posted bill total before the bill date: %v", err)
	}
	if early != after-25000 {
		t.Fatalf("posted bill total before the bill date is %d over the earlier read, want exactly 25000 less", early-after+25000)
	}
}
