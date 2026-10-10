package inventory

import (
	"context"
	"encoding/json"
	"testing"
)

// P3-3 of the Opus review: with drafts out of the on order leg, the quick reorder suggests the full
// quantity again while a draft for the product sits unplaced, so a second press makes a second
// draft. That is the ruling's meaning (a draft never covers), so the press still drafts, but the
// answer now names the draft that was already open, in the same pointer words the worklist row
// carries, and a product with no open draft carries no such keys.
func TestReorderPO_NamesTheDraftThatWasAlreadyOpen(t *testing.T) {
	line := ShortfallRow{
		ProductID: blu60, SKU: "DBS-TB-BLU60", BranchID: trt470, UOM: "PC",
		OnHandBase: 800, ReorderPointBase: 1000, ReorderQtyBase: 120, OrderMultipleBase: 60,
		PreferredVendorID: "v-alcli", AvgCostCents: 1250,
	}
	read := func(drafts *fakeDraftReader) map[string]any {
		t.Helper()
		svc := newIncomingSvc(&fakeIncoming{lineFound: true, line: line})
		svc.WithOnOrderReader(&fakeOnOrder{})
		if drafts != nil {
			svc.WithDraftPOReader(drafts)
		}
		// The purchasing seam is unbound: the stub's 503 rides with the draft, which is all this reads.
		res, _ := svc.CreateReorderPO(context.Background(),
			BranchReach{BranchIDs: []string{trt470}}, ReorderRequest{ProductID: blu60, BranchID: trt470})
		raw, _ := json.Marshal(res)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		return m
	}

	open := &fakeDraftReader{rows: []DraftPO{{ProductID: blu60, BranchID: trt470, POID: "po-open-1", PONo: "#PO-TRT-7"}}}
	m := read(open)
	if m["existing_draft_po_id"] != "po-open-1" || m["existing_draft_po_no"] != "#PO-TRT-7" {
		t.Errorf("the answer names draft %v (%v), want the open draft po-open-1 (#PO-TRT-7)", m["existing_draft_po_id"], m["existing_draft_po_no"])
	}
	if open.gotBranch != trt470 || len(open.gotProducts) != 1 || open.gotProducts[0] != blu60 {
		t.Errorf("the draft read asked for branch %q products %v, want this product at this branch only", open.gotBranch, open.gotProducts)
	}
	for _, m := range []map[string]any{read(&fakeDraftReader{}), read(nil)} {
		if _, ok := m["existing_draft_po_id"]; ok {
			t.Errorf("a product with no open draft carries existing_draft_po_id: %v", m)
		}
	}
}
