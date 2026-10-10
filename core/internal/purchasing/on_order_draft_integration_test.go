//go:build integration

// on_order_draft_integration_test.go, the principal's ruling that a draft never covers a shortfall:
// only a PLACED order (ORDERED, PARTIAL) is supply. A draft is a buyer's note to self, not a
// promise to the vendor, so the aggregate the replenishment worklist grades with leaves it out,
// while the product incoming list keeps showing it, carrying its DRAFT status, so a screen can say
// "drafted, not placed" rather than hide the work.
package purchasing

import (
	"testing"
)

func TestIntegration_ADraftIsNotOnOrderUntilItIsPlaced(t *testing.T) {
	h := newHarness(t)
	prod := h.newProduct(t, "draft-cover")
	po, err := h.svc.CreatePO(h.ctx, CreatePORequest{
		VendorID: h.vendor, BranchID: branchTRT, Notes: "draft never covers",
		Lines: []AddLineParams{{ProductID: prod, QtyOrderedBase: 40, UOM: "PC", UnitCostCents: 125}},
	})
	if err != nil {
		t.Fatalf("draft the PO: %v", err)
	}
	t.Cleanup(func() { h.cleanupPO(t, po.ID) })

	onOrder := func() float64 {
		rows, err := h.svc.OutstandingOnOrderBase(h.ctx, branchTRT, []string{prod})
		if err != nil {
			t.Fatalf("on order: %v", err)
		}
		var total float64
		for _, r := range rows {
			total += r.QtyBase
		}
		return total
	}
	if got := onOrder(); got != 0 {
		t.Errorf("a draft counts %v as on order, want 0: a draft never covers a shortfall", got)
	}
	// The incoming list is the other reader of the same rows: it keeps the draft, as a draft.
	lines, err := h.svc.IncomingForProduct(h.ctx, prod, branchTRT)
	if err != nil {
		t.Fatalf("incoming: %v", err)
	}
	if len(lines) != 1 || lines[0].Status != StatusDraft {
		t.Errorf("the incoming list reads %+v, want the one line carrying its DRAFT status", lines)
	}

	// The draft pointer: the draft's id and number for this product at this branch, then none once
	// the draft is placed (placing takes the row off the worklist, so nothing points at a draft).
	drafts, err := h.svc.OpenDraftsFor(h.ctx, branchTRT, []string{prod})
	if err != nil {
		t.Fatalf("open drafts: %v", err)
	}
	if len(drafts) != 1 || drafts[0].POID != po.ID || drafts[0].PONo != po.PONo || drafts[0].ProductID != prod {
		t.Errorf("the draft pointer reads %+v, want the one draft %s (%s)", drafts, po.ID, po.PONo)
	}
	if other, _ := h.svc.OpenDraftsFor(h.ctx, branchKGN, []string{prod}); len(other) != 0 {
		t.Errorf("the draft pointer at the other branch reads %+v, want none", other)
	}

	if _, err := h.svc.Transition(h.ctx, po.ID, StatusOrdered); err != nil {
		t.Fatalf("place the PO: %v", err)
	}
	if got := onOrder(); got != 40 {
		t.Errorf("a placed order counts %v as on order, want 40", got)
	}
	if drafts, _ := h.svc.OpenDraftsFor(h.ctx, branchTRT, []string{prod}); len(drafts) != 0 {
		t.Errorf("the draft pointer after placing reads %+v, want none", drafts)
	}
}
