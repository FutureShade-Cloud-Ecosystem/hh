//go:build integration

// incoming_integration_test.go, DIB-460's on-order read and its expected-date write, against a real
// database. What the unit file cannot prove lives here: that the SQL selects the right lines, orders
// them the way a person reads them, and that the branch predicate the reach renders actually filters.
package purchasing

import (
	"context"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// draftPOAt drafts (and registers cleanup for) a one-line PO at a branch with an optional date.
func (h *harness) draftPOAt(t *testing.T, branchID, productID, expected string, qty float64) PODetail {
	t.Helper()
	po, err := h.svc.CreatePO(h.ctx, CreatePORequest{
		VendorID: h.vendor, BranchID: branchID, ExpectedAt: expected,
		Lines: []AddLineParams{{ProductID: productID, QtyOrderedBase: qty, UOM: "EA", UnitCostCents: 1000}},
	})
	if err != nil {
		t.Fatalf("draft PO at %s: %v", branchID, err)
	}
	h.cleanupPO(t, po.ID)
	return po
}

func lineFor(rows []IncomingPOLine, poNo string) (IncomingPOLine, bool) {
	for _, l := range rows {
		if l.PONo == poNo {
			return l, true
		}
	}
	return IncomingPOLine{}, false
}

// TestIntegration_Incoming_OutstandingLinesWithTheirExpectedDate is the ticket, end to end: the
// question Ryan asked ("when is it showing up") answered from the product, without asking anybody.
func TestIntegration_Incoming_OutstandingLinesWithTheirExpectedDate(t *testing.T) {
	h := newHarness(t)
	product := h.newProduct(t, "inc-open")

	po := h.draftPOAt(t, branchTRT, product, "2026-08-19", 120)
	if _, err := h.svc.Transition(h.ctx, po.ID, StatusOrdered); err != nil {
		t.Fatalf("order it: %v", err)
	}

	rows, err := h.svc.IncomingForProduct(h.ctx, product, "")
	if err != nil {
		t.Fatalf("incoming: %v", err)
	}
	got, ok := lineFor(rows, po.PONo)
	if !ok {
		t.Fatalf("the open PO %s is not in the answer: %+v", po.PONo, rows)
	}
	if got.ExpectedAt != "2026-08-19" {
		t.Errorf("expected_at = %q, want 2026-08-19, the date is the whole ticket", got.ExpectedAt)
	}
	if got.QtyOutstanding != 120 || got.QtyOrderedBase != 120 || got.QtyReceivedBase != 0 {
		t.Errorf("qty ordered/received/outstanding = %v/%v/%v, want 120/0/120",
			got.QtyOrderedBase, got.QtyReceivedBase, got.QtyOutstanding)
	}
	if got.Status != StatusOrdered {
		t.Errorf("status = %q, want ORDERED, the surface says 'ordered' or 'draft' and must not guess", got.Status)
	}
	if got.UOM != "EA" {
		t.Errorf("uom = %q, a quantity without its unit is not an answer", got.UOM)
	}
	if got.VendorName == "" {
		t.Error("vendor_name is empty, it is enriched through the VendorDirectory seam, not JOINed")
	}
}

// TestIntegration_Incoming_APartiallyReceivedLineReportsWhatIsSTILLComing, the number that matters
// after a truck has been part-unloaded is the REMAINDER, and the surface must be able to say
// "40 of 120 landed" rather than repeating the original order quantity.
func TestIntegration_Incoming_APartiallyReceivedLineReportsWhatIsSTILLComing(t *testing.T) {
	h := newHarness(t)
	product := h.newProduct(t, "inc-partial")

	po := h.draftPOAt(t, branchTRT, product, "2026-08-19", 120)
	if _, err := h.svc.Transition(h.ctx, po.ID, StatusOrdered); err != nil {
		t.Fatalf("order it: %v", err)
	}
	full, err := h.svc.GetPO(h.ctx, po.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: full.Lines[0].ID, QtyBase: 40}},
	}); err != nil {
		t.Fatalf("receive 40: %v", err)
	}

	rows, err := h.svc.IncomingForProduct(h.ctx, product, "")
	if err != nil {
		t.Fatalf("incoming: %v", err)
	}
	got, ok := lineFor(rows, po.PONo)
	if !ok {
		t.Fatalf("a PARTIAL PO still has stock coming and must stay on the read: %+v", rows)
	}
	if got.QtyOutstanding != 80 {
		t.Errorf("outstanding = %v, want 80, reporting the ordered 120 would double-count what already landed", got.QtyOutstanding)
	}
	if got.QtyReceivedBase != 40 {
		t.Errorf("received = %v, want 40", got.QtyReceivedBase)
	}
	if got.Status != StatusPartial {
		t.Errorf("status = %q, want PARTIAL", got.Status)
	}
}

// TestIntegration_Incoming_AFullyReceivedPOIsNotComing, the read must drop a line the moment it is
// complete, or the card promises stock that is already in the yard and gets counted twice.
func TestIntegration_Incoming_AFullyReceivedPOIsNotComing(t *testing.T) {
	h := newHarness(t)
	product := h.newProduct(t, "inc-done")

	po := h.draftPOAt(t, branchTRT, product, "2026-08-19", 10)
	if _, err := h.svc.Transition(h.ctx, po.ID, StatusOrdered); err != nil {
		t.Fatalf("order it: %v", err)
	}
	full, err := h.svc.GetPO(h.ctx, po.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: full.Lines[0].ID, QtyBase: 10}},
	}); err != nil {
		t.Fatalf("receive everything: %v", err)
	}

	rows, err := h.svc.IncomingForProduct(h.ctx, product, "")
	if err != nil {
		t.Fatalf("incoming: %v", err)
	}
	if _, ok := lineFor(rows, po.PONo); ok {
		t.Fatalf("a fully RECEIVED purchase order is still reported as on order: %+v", rows)
	}
}

// TestIntegration_Incoming_ACancelledPOIsNotComing, the other end of the same rule.
func TestIntegration_Incoming_ACancelledPOIsNotComing(t *testing.T) {
	h := newHarness(t)
	product := h.newProduct(t, "inc-cancel")

	po := h.draftPOAt(t, branchTRT, product, "2026-08-19", 10)
	if _, err := h.svc.Transition(h.ctx, po.ID, StatusCancelled); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	rows, err := h.svc.IncomingForProduct(h.ctx, product, "")
	if err != nil {
		t.Fatalf("incoming: %v", err)
	}
	if _, ok := lineFor(rows, po.PONo); ok {
		t.Fatalf("a CANCELLED purchase order is reported as on order: %+v", rows)
	}
}

// TestIntegration_Incoming_SoonestFirstAndUndatedLast is the ORDER, which is the difference between
// an answer and a list. The person asking wants the next truck; an order with no promised date
// answers nothing and belongs at the bottom.
func TestIntegration_Incoming_SoonestFirstAndUndatedLast(t *testing.T) {
	h := newHarness(t)
	product := h.newProduct(t, "inc-order")

	late := h.draftPOAt(t, branchTRT, product, "2026-12-01", 5)
	undated := h.draftPOAt(t, branchTRT, product, "", 5)
	soon := h.draftPOAt(t, branchTRT, product, "2026-08-19", 5)

	rows, err := h.svc.IncomingForProduct(h.ctx, product, "")
	if err != nil {
		t.Fatalf("incoming: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d lines, want 3: %+v", len(rows), rows)
	}
	if rows[0].PONo != soon.PONo {
		t.Errorf("first = %s, want the soonest (%s)", rows[0].PONo, soon.PONo)
	}
	if rows[1].PONo != late.PONo {
		t.Errorf("second = %s, want the later dated one (%s)", rows[1].PONo, late.PONo)
	}
	if rows[2].PONo != undated.PONo {
		t.Errorf("last = %s, want the UNDATED one (%s), NULLS LAST", rows[2].PONo, undated.PONo)
	}
	if rows[2].ExpectedAt != "" {
		t.Errorf("expected_at = %q for a PO raised without a date; it must stay EMPTY and never be guessed", rows[2].ExpectedAt)
	}
}

// TestIntegration_Incoming_TheBranchFenceActuallyFilters, the reach is not decoration.
//
// A caller granted only Trenton must not see the Kingston order for the same product. This is the
// half the unit test cannot reach: there the fake ignores the reach by construction, so only a real
// query can prove the predicate does what the parameter says.
func TestIntegration_Incoming_TheBranchFenceActuallyFilters(t *testing.T) {
	h := newHarness(t)
	product := h.newProduct(t, "inc-fence")

	trt := h.draftPOAt(t, branchTRT, product, "2026-08-19", 10)
	kgn := h.draftPOAt(t, branchKGN, product, "2026-08-20", 10)

	// A GLOBAL caller sees both.
	rows, err := h.svc.IncomingForProduct(h.ctx, product, "")
	if err != nil {
		t.Fatalf("incoming (global): %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("a GLOBAL caller sees %d of 2 branches: %+v", len(rows), rows)
	}

	// The same read, by a caller granted Trenton only.
	scoped := NewService(NewRepository(h.pool)).
		WithVendors(itVendors{pool: h.pool}).
		withCaller(scopedCaller(h.user, branchTRT))
	rows, err = scoped.IncomingForProduct(h.ctx, product, "")
	if err != nil {
		t.Fatalf("incoming (scoped): %v", err)
	}
	if len(rows) != 1 || rows[0].PONo != trt.PONo {
		t.Fatalf("a Trenton-only caller saw %+v; want exactly %s and NOT %s", rows, trt.PONo, kgn.PONo)
	}

	// And a caller with no grants at all sees nothing, the inversion case, against real rows.
	none := NewService(NewRepository(h.pool)).
		WithVendors(itVendors{pool: h.pool}).
		withCaller(func(context.Context) (identity.StaffContext, bool) {
			return identity.StaffContext{UserID: h.user, RoleCode: "INSIDE_SALES",
				Tier: identity.TierFrontline, Scope: identity.ScopeBranch, Status: "ACTIVE"}, true
		})
	rows, err = none.IncomingForProduct(h.ctx, product, "")
	if err != nil {
		t.Fatalf("incoming (no grants): %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("a caller with NO branch grants was handed %d purchase orders: %+v", len(rows), rows)
	}
}

// TestIntegration_SetExpectedDate_SetsChangesAndClears, the edit path, including the clear.
func TestIntegration_SetExpectedDate_SetsChangesAndClears(t *testing.T) {
	h := newHarness(t)
	product := h.newProduct(t, "inc-date")

	// Raised with NO date, which is what every PO the replenishment worklist creates looks like.
	po := h.draftPOAt(t, branchTRT, product, "", 10)
	if po.ExpectedAt != "" {
		t.Fatalf("fixture: expected_at = %q, want empty", po.ExpectedAt)
	}

	set, err := h.svc.SetExpectedDate(h.ctx, po.ID, "2026-08-19")
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if set.ExpectedAt != "2026-08-19" {
		t.Fatalf("after set, expected_at = %q", set.ExpectedAt)
	}

	// The vendor moves it, the ordinary case, and the reason a create-time-only column was useless.
	moved, err := h.svc.SetExpectedDate(h.ctx, po.ID, "2026-08-26")
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if moved.ExpectedAt != "2026-08-26" {
		t.Fatalf("after move, expected_at = %q", moved.ExpectedAt)
	}

	// The read agrees with the write.
	rows, err := h.svc.IncomingForProduct(h.ctx, product, "")
	if err != nil {
		t.Fatalf("incoming: %v", err)
	}
	got, _ := lineFor(rows, po.PONo)
	if got.ExpectedAt != "2026-08-26" {
		t.Fatalf("the read reports %q after the write said 2026-08-26", got.ExpectedAt)
	}

	// The vendor withdraws the promise: back to no date, not to a stale one.
	cleared, err := h.svc.SetExpectedDate(h.ctx, po.ID, "")
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if cleared.ExpectedAt != "" {
		t.Fatalf("after clear, expected_at = %q, want empty", cleared.ExpectedAt)
	}
}

// TestIntegration_SetExpectedDate_RefusedOnAFinishedPOAndOutsideTheFence.
func TestIntegration_SetExpectedDate_RefusedOnAFinishedPOAndOutsideTheFence(t *testing.T) {
	h := newHarness(t)
	product := h.newProduct(t, "inc-date-no")

	po := h.draftPOAt(t, branchTRT, product, "2026-08-19", 10)
	if _, err := h.svc.Transition(h.ctx, po.ID, StatusOrdered); err != nil {
		t.Fatalf("order it: %v", err)
	}
	full, err := h.svc.GetPO(h.ctx, po.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: full.Lines[0].ID, QtyBase: 10}},
	}); err != nil {
		t.Fatalf("receive: %v", err)
	}
	if _, err := h.svc.SetExpectedDate(h.ctx, po.ID, "2026-09-01"); err == nil {
		t.Error("the promised date of a RECEIVED purchase order was editable, that is rewriting history")
	}

	// A Kingston-only caller cannot re-date a Trenton order.
	other := h.draftPOAt(t, branchTRT, product, "2026-08-19", 10)
	scoped := NewService(NewRepository(h.pool)).
		WithVendors(itVendors{pool: h.pool}).
		withCaller(scopedCaller(h.user, branchKGN))
	if _, err := scoped.SetExpectedDate(h.ctx, other.ID, "2026-09-01"); err == nil {
		t.Error("a caller with no grant on the PO's branch changed its expected date")
	}
}

// TestIntegration_CreatePO_AnOmittedUOMResolvesToTheProductBaseUnit, the walkthrough's blocker,
// pinned against a real products row.
func TestIntegration_CreatePO_AnOmittedUOMResolvesToTheProductBaseUnit(t *testing.T) {
	h := newHarness(t)
	product := h.newProduct(t, "uom-default") // created with base_uom 'EA'

	po, err := h.svc.CreatePO(h.ctx, CreatePORequest{
		VendorID: h.vendor, BranchID: branchTRT,
		// No UOM, exactly what the DIB-72 create-PO adapter sends.
		Lines: []AddLineParams{{ProductID: product, QtyOrderedBase: 120, UnitCostCents: 1850}},
	})
	if err != nil {
		t.Fatalf("a PO from the replenishment seam must not be refused for a missing unit: %v", err)
	}
	h.cleanupPO(t, po.ID)
	if len(po.Lines) != 1 || po.Lines[0].UOM != "EA" {
		t.Fatalf("line uom = %q, want the product's base unit EA, a line must never be written unitless", po.Lines[0].UOM)
	}
}
