//go:build integration

// po_line_edit_integration_test.go, the draft PO's line editor against a real database: add and
// remove existed (POST/DELETE .../lines, draft only), the missing leg was the CORRECTION, change
// the quantity or the cost of a line already on the order (the roadmap's "no PO line edit",
// section 1.5). A buyer who cannot correct a mistyped quantity removes the line and retypes it,
// losing the description and the line number, or places a wrong order; this file is the red first
// half of the route that closes that.
package purchasing

import (
	"errors"
	"net/http"
	"testing"
)

// draftWithLine plants a one line draft PO and answers (po id, line id).
func draftWithLine(t *testing.T, h *harness, qty float64, cost int64) (string, string) {
	t.Helper()
	po, err := h.svc.CreatePO(h.ctx, CreatePORequest{
		VendorID: h.vendor, BranchID: branchTRT, Notes: "line edit fixture",
		Lines: []AddLineParams{{ProductID: h.prodAny(t), Description: "Original", QtyOrderedBase: qty, UOM: "PC", UnitCostCents: cost}},
	})
	if err != nil {
		t.Fatalf("create draft PO: %v", err)
	}
	t.Cleanup(func() { _, _ = h.pool.Exec(h.ctx, `DELETE FROM purchasing_pos WHERE id = $1`, po.ID) })
	if len(po.Lines) != 1 {
		t.Fatalf("the draft holds %d lines, want 1", len(po.Lines))
	}
	return po.ID, po.Lines[0].ID
}

func TestIntegration_POLineEdit_TheDraftsEditor(t *testing.T) {
	h := newHarness(t)
	poID, lineID := draftWithLine(t, h, 40, 125)

	// The correction: change the quantity, then the cost, then both, and read each back.
	po, err := h.svc.UpdateLine(h.ctx, poID, lineID, UpdateLineParams{QtyOrderedBase: ptr(60.0)})
	if err != nil {
		t.Fatalf("change the quantity: %v", err)
	}
	if got := po.Lines[0].QtyOrderedBase; got != 60 {
		t.Errorf("the line's quantity reads %v, want 60", got)
	}
	if got := po.Lines[0].UnitCostCents; got != 125 {
		t.Errorf("an unnamed cost must not move, reads %d, want 125", got)
	}
	po, err = h.svc.UpdateLine(h.ctx, poID, lineID, UpdateLineParams{UnitCostCents: ptr(int64(140))})
	if err != nil {
		t.Fatalf("change the cost: %v", err)
	}
	if po.Lines[0].UnitCostCents != 140 || po.Lines[0].QtyOrderedBase != 60 {
		t.Errorf("the line reads %v at %d, want 60 at 140", po.Lines[0].QtyOrderedBase, po.Lines[0].UnitCostCents)
	}
	if po.Lines[0].Description != "Original" || po.Lines[0].LineNo != 1 {
		t.Errorf("the correction moved what it should not: %+v", po.Lines[0])
	}

	// The malformed: a negative quantity or cost is refused, and a body naming nothing is refused
	// rather than silently bumping the header.
	if _, err := h.svc.UpdateLine(h.ctx, poID, lineID, UpdateLineParams{QtyOrderedBase: ptr(-5.0)}); err == nil {
		t.Errorf("a negative quantity was accepted, want a refusal")
	}
	if _, err := h.svc.UpdateLine(h.ctx, poID, lineID, UpdateLineParams{UnitCostCents: ptr(int64(-1))}); err == nil {
		t.Errorf("a negative cost was accepted, want a refusal")
	}
	if _, err := h.svc.UpdateLine(h.ctx, poID, lineID, UpdateLineParams{}); err == nil {
		t.Errorf("a body naming nothing was accepted, want a refusal")
	}
	// A line that is not on this order is a 404, never a silent no-op.
	if _, err := h.svc.UpdateLine(h.ctx, poID, "00000000-0000-0000-0000-000000000000", UpdateLineParams{QtyOrderedBase: ptr(1.0)}); !errors.Is(err, errNotFound) {
		t.Errorf("edit a line that is not on the order: got %v, want not found", err)
	}
}

func TestIntegration_POLineEdit_RefusedOncePlaced(t *testing.T) {
	h := newHarness(t)

	placed := func() (string, string) {
		t.Helper()
		poID, _ := draftWithLine(t, h, 10, 90)
		po, err := h.svc.Transition(h.ctx, poID, StatusOrdered)
		if err != nil {
			t.Fatalf("place the draft: %v", err)
		}
		return po.ID, po.Lines[0].ID
	}

	poID, lineID := placed()
	if _, err := h.svc.UpdateLine(h.ctx, poID, lineID, UpdateLineParams{QtyOrderedBase: ptr(99.0)}); err == nil {
		t.Errorf("a placed order's line was edited, want the refusal")
	} else {
		var ce clientError
		if errors.As(err, &ce) && ce.status != http.StatusConflict {
			t.Errorf("the placed refusal reads status %d, want the 409 the lifecycle's refusals carry", ce.status)
		}
	}
	// The add and the remove carry the same freeze (the existing pair, proved in the same shape).
	if _, err := h.svc.AddLine(h.ctx, poID, AddLineParams{ProductID: h.prodAny(t), QtyOrderedBase: 5, UOM: "PC", UnitCostCents: 90}); err == nil {
		t.Errorf("a line was added to a placed order, want the refusal")
	}
	if _, err := h.svc.RemoveLine(h.ctx, poID, lineID); err == nil {
		t.Errorf("a line was removed from a placed order, want the refusal")
	}
	// And the placed line is byte for byte what it was: no partial write behind the refusal.
	po, err := h.svc.GetPO(h.ctx, poID)
	if err != nil {
		t.Fatalf("read the placed order back: %v", err)
	}
	if po.Lines[0].QtyOrderedBase != 10 || po.Lines[0].UnitCostCents != 90 {
		t.Errorf("the placed line reads %v at %d behind the refusal, want 10 at 90", po.Lines[0].QtyOrderedBase, po.Lines[0].UnitCostCents)
	}
}
