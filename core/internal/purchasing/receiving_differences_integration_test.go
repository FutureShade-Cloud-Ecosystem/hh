//go:build integration

package purchasing

import (
	"context"
	"testing"
)

// TestIntegration_ReceivingDifferences_CountsShortReceiptsInsideTheReach is the yard manager's
// "receipts that landed short" source (0381): purchase orders left PARTIAL, counted by the module
// that owns them, fenced to the reach, and dropped the moment the rest lands or the order is closed.
// The harness database is shared, so every assertion is a delta against a baseline read first.
func TestIntegration_ReceivingDifferences_CountsShortReceiptsInsideTheReach(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	trt := BranchReach{BranchIDs: []string{branchTRT}}
	global := BranchReach{Global: true}

	baseTRT, err := h.svc.ReceivingDifferences(ctx, trt)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	kgn := BranchReach{BranchIDs: []string{branchKGN}}
	baseKGN, err := h.svc.ReceivingDifferences(ctx, kgn)
	if err != nil {
		t.Fatalf("baseline kingston: %v", err)
	}
	baseAll, err := h.svc.ReceivingDifferences(ctx, global)
	if err != nil {
		t.Fatalf("baseline global: %v", err)
	}

	product := h.newProduct(t, "recv-diff")
	po := h.draftPOAt(t, branchTRT, product, "", 100)
	if _, err := h.svc.Transition(h.ctx, po.ID, StatusOrdered); err != nil {
		t.Fatalf("order it: %v", err)
	}
	full, err := h.svc.GetPO(h.ctx, po.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	// An ordered PO with nothing received is on order, not a difference.
	if got, _ := h.svc.ReceivingDifferences(ctx, trt); got.Count != baseTRT.Count {
		t.Fatalf("an untouched order counted as a receiving difference: %d -> %d", baseTRT.Count, got.Count)
	}

	if _, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: full.Lines[0].ID, QtyBase: 40}},
	}); err != nil {
		t.Fatalf("receive 40 of 100: %v", err)
	}
	got, err := h.svc.ReceivingDifferences(ctx, trt)
	if err != nil {
		t.Fatalf("after a short receipt: %v", err)
	}
	if got.Count != baseTRT.Count+1 {
		t.Fatalf("a PARTIAL PO counts %d -> %d, want one more", baseTRT.Count, got.Count)
	}
	if got.Oldest.IsZero() {
		t.Error("the summary carries no age; a short receipt has a landing time")
	}
	if all, _ := h.svc.ReceivingDifferences(ctx, global); all.Count != baseAll.Count+1 {
		t.Errorf("global %d -> %d, want one more", baseAll.Count, all.Count)
	}

	// The fence: another branch's grant, and no grants at all, both count nothing of this PO.
	if o, err := h.svc.ReceivingDifferences(ctx, kgn); err != nil || o.Count != baseKGN.Count {
		t.Errorf("the Kingston reach moved %d -> %d (%v) on a Trenton order", baseKGN.Count, o.Count, err)
	}
	if none, err := h.svc.ReceivingDifferences(ctx, BranchReach{}); err != nil || none.Count != 0 {
		t.Fatalf("a caller with no grants counted %d (%v), want 0", none.Count, err)
	}

	// The rest lands: the order is whole, the difference is gone.
	if _, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: full.Lines[0].ID, QtyBase: 60}},
	}); err != nil {
		t.Fatalf("receive the rest: %v", err)
	}
	if after, _ := h.svc.ReceivingDifferences(ctx, trt); after.Count != baseTRT.Count {
		t.Fatalf("a fully received PO still counts: %d, want %d", after.Count, baseTRT.Count)
	}
}
