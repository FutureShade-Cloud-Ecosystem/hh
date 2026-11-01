//go:build integration

// ap_subledger_integration_test.go, the AP aging's as-of boundary: a posted vendor bill enters the
// open AP at its posting journal's effective instant, so the reads bracket the test's own bill and
// no other writer's state can move the assertion.
package purchasing

import (
	"testing"
	"time"
)

func TestIntegration_APAgingCountsThePostedBill(t *testing.T) {
	h := newHarness(t)
	product := h.newProduct(t, "apread")
	po := h.orderedPO(t, product, 10, 2500)
	lineID := po.Lines[0].ID

	if _, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: lineID, QtyBase: 10}},
	}); err != nil {
		t.Fatalf("receive: %v", err)
	}

	stamp := time.Now()
	// The reads bracket the test's own bill, so every other open obligation in the window cancels.
	// The harness wires the GL journal clock as production does, so the bill counts from its
	// posting journal's effective instant: its posted_at, or, filed late, the end of the month its
	// document date names. Both sit past the before read.
	before, err := h.svc.APAging(h.ctx, stamp.Add(time.Minute), BranchReach{Global: true})
	if err != nil {
		t.Fatalf("aging before the bill: %v", err)
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

	// After the post the bill is in the balance. The harness also wires the landed cost payables,
	// but this test plants none, so the total moves by the bill alone.
	after, err := h.svc.APAging(h.ctx, stamp.Add(time.Minute), BranchReach{Global: true})
	if err != nil {
		t.Fatalf("aging after the post: %v", err)
	}
	if after.TotalCents != before.TotalCents+25000 {
		t.Fatalf("aging moved %d, want exactly the bill's 25000", after.TotalCents-before.TotalCents)
	}

	// Before the bill's document date the read does not count it: the 15th of July sits before the
	// bill's own 2026-08-01, and so before every instant the bill's journal can count at, posted
	// now or filed late.
	early, err := h.svc.APAging(h.ctx, time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC), BranchReach{Global: true})
	if err != nil {
		t.Fatalf("aging before the bill date: %v", err)
	}
	if early.TotalCents != after.TotalCents-25000 {
		t.Fatalf("aging before the bill date is %d over the earlier read, want exactly 25000 less", early.TotalCents-after.TotalCents+25000)
	}
}
