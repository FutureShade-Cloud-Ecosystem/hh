//go:build integration

// delivery_method_integration_test.go, DIB-572's correction path against a real database, ported
// from the delivery-method half of the ERP's dib572_dib573_integration_test.go. What the unit file
// cannot prove lives here: that a normalized value is what the column stores (an un-normalized
// write would meet the 0219 CHECK as a 500), that a finished order's refusal is the 409 conflict
// classification, and that the branch fence holds on this path. The worklist legs of the ERP file
// are not ported: core's worklist does not render the column (the DIB-573 half is a recorded
// residual), so the read-back here is GetPO.
package purchasing

import (
	"errors"
	"net/http"
	"testing"
)

// TestIntegration_SetDeliveryMethod_NormalizesPersistsAndReadsBack is the write path the unit
// suite cannot reach: the value that lands in the column is the normalized one, the read agrees
// with the write, and UNKNOWN is a value you may SET, not only one you may leave.
func TestIntegration_SetDeliveryMethod_NormalizesPersistsAndReadsBack(t *testing.T) {
	h := newHarness(t)
	product := h.newProduct(t, "dib572set")

	// Raised with no arrival recorded, the only shape core's create path can produce: the column
	// default is UNKNOWN, and a default is not an arrival.
	po := h.draftPOAt(t, branchTRT, product, "", 10)
	if po.DeliveryMethod != DeliveryUnknown {
		t.Fatalf("fixture: a fresh draft reports %q, want %q, the column default must not invent an arrival",
			po.DeliveryMethod, DeliveryUnknown)
	}
	if _, err := h.svc.Transition(h.ctx, po.ID, StatusOrdered); err != nil {
		t.Fatalf("order it: %v", err)
	}

	readBack := func() string {
		t.Helper()
		full, err := h.svc.GetPO(h.ctx, po.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		return full.DeliveryMethod
	}

	// The leg the mutation hole hid behind: a padded, lowercase answer must LAND as the canonical
	// word. Written raw, "  pickup  " would meet the column CHECK and surface as a 500.
	set, err := h.svc.SetDeliveryMethod(h.ctx, po.ID, "  pickup  ")
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if set.DeliveryMethod != DeliveryPickup {
		t.Errorf("the write reports %q, want %q", set.DeliveryMethod, DeliveryPickup)
	}
	if got := readBack(); got != DeliveryPickup {
		t.Fatalf("the column holds %q after a padded lowercase write, want %q", got, DeliveryPickup)
	}

	// The correction: the yard learns the supplier is bringing it after all.
	fixed, err := h.svc.SetDeliveryMethod(h.ctx, po.ID, DeliveryDelivered)
	if err != nil {
		t.Fatalf("correct the arrival: %v", err)
	}
	if fixed.DeliveryMethod != DeliveryDelivered {
		t.Errorf("the corrected order reports %q, want %q", fixed.DeliveryMethod, DeliveryDelivered)
	}
	if got := readBack(); got != DeliveryDelivered {
		t.Fatalf("the correction did not reach the column: %q", got)
	}

	// A buyer who marked an order and then learns nobody actually knows must be able to say so.
	back, err := h.svc.SetDeliveryMethod(h.ctx, po.ID, DeliveryUnknown)
	if err != nil {
		t.Fatalf("un-record the arrival: %v", err)
	}
	if back.DeliveryMethod != DeliveryUnknown {
		t.Errorf("un-recording reports %q, want %q", back.DeliveryMethod, DeliveryUnknown)
	}
	if got := readBack(); got != DeliveryUnknown {
		t.Fatalf("un-recording did not reach the column: %q", got)
	}

	// The refusals, against the real column: an empty body is a malformed correction rather than
	// an unrecord, a word outside the three is refused here rather than by the CHECK as a 500, and
	// a missing po id is refused before anything is read.
	if _, err := h.svc.SetDeliveryMethod(h.ctx, po.ID, "   "); err == nil {
		t.Error("an empty delivery_method was accepted on the edit path")
	}
	if _, err := h.svc.SetDeliveryMethod(h.ctx, po.ID, "PICK UP"); err == nil {
		t.Error("a value outside the three was accepted; the CHECK would then refuse it as a 500")
	}
	if _, err := h.svc.SetDeliveryMethod(h.ctx, "", DeliveryPickup); err == nil {
		t.Error("a missing po id was accepted")
	}
	if got := readBack(); got != DeliveryUnknown {
		t.Fatalf("a refused request still moved the column: %q", got)
	}
}

// TestIntegration_SetDeliveryMethod_RefusedOnAFinishedPOAndOutsideTheFence: a finished order's
// arrival is a historical fact nobody is waiting on, and the refusal is the 409 conflict
// classification, the same one the expected-date sibling returns; and a caller granted only
// another yard cannot correct this yard's arrival.
func TestIntegration_SetDeliveryMethod_RefusedOnAFinishedPOAndOutsideTheFence(t *testing.T) {
	h := newHarness(t)
	product := h.newProduct(t, "dib572no")

	po := h.draftPOAt(t, branchTRT, product, "", 10)
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

	err = func() error { _, err := h.svc.SetDeliveryMethod(h.ctx, po.ID, DeliveryPickup); return err }()
	if err == nil {
		t.Error("the arrival of a RECEIVED purchase order was editable, that is rewriting history")
	} else {
		var ce clientError
		if !errors.As(err, &ce) || ce.status != http.StatusConflict {
			t.Errorf("a finished order's refusal = %v, want the 409 conflict classification", err)
		}
	}

	// A Kingston-only caller cannot re-route a Trenton order's arrival, and the refusal is the 403
	// classification, the fence and not some incidental failure.
	other := h.draftPOAt(t, branchTRT, product, "", 10)
	scoped := NewService(NewRepository(h.pool)).
		WithVendors(itVendors{pool: h.pool}).
		withCaller(scopedCaller(h.user, branchKGN))
	err = func() error { _, err := scoped.SetDeliveryMethod(h.ctx, other.ID, DeliveryPickup); return err }()
	if err == nil {
		t.Error("a caller with no grant on the PO's branch changed its arrival")
	} else {
		var ce clientError
		if !errors.As(err, &ce) || ce.status != http.StatusForbidden {
			t.Errorf("a cross-branch refusal = %v, want the 403 fence classification", err)
		}
	}
}
