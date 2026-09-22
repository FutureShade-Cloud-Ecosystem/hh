package inventory

// dib470_onorder_test.go, the replenishment worklist grades with the SEAM's on-order number.
//
// The defect this pins: the worklist read `on_order` from the 0011 purchase_orders rows, which have
// been receiving documents since 0120 and are therefore never open, so the leg was a constant zero
// and the list re-ordered stock that was already bought. These tests are DB-free; the same claim is
// made against real rows in dib470_onorder_integration_test.go.

import (
	"context"
	"errors"
	"testing"
)

// - doubles -

// fakeShortfallStore is the worklist's persistence seam. It stands in for the SQL, so it does what
// the SQL does: it applies the on-order quantities it is HANDED, re-derives the position, and lists
// only the lines that are still short.
//
// It deliberately does not compute an on-order figure of its own. A double that invented one could
// not tell the difference between a service that passed the seam's answer through and a service that
// dropped it, which is the exact regression this file exists to catch.
type fakeShortfallStore struct {
	*fakeStore

	rows       []ShortfallRow // raw components: on hand, reserved, in transit, the policy
	gotOnOrder []OnOrderQty
	gotBranch  string
	gotLimit   int
	err        error
}

func (f *fakeShortfallStore) ShortfallWorklist(_ context.Context, branchID string, limit int, onOrder []OnOrderQty) ([]ShortfallRow, error) {
	f.gotOnOrder, f.gotBranch, f.gotLimit = onOrder, branchID, limit
	if f.err != nil {
		return nil, f.err
	}
	var out []ShortfallRow
	for _, r := range f.rows {
		if branchID != "" && r.BranchID != branchID {
			continue
		}
		r.OnOrderBase = onOrderBaseFor(onOrder, r.ProductID, r.BranchID)
		r.Derive()
		if r.ProjectedBase >= r.ReorderPointBase {
			continue // not short, the SQL's WHERE clause
		}
		out = append(out, r)
	}
	return out, nil
}

// fakeOnOrder is the purchasing side of the seam.
type fakeOnOrder struct {
	rows        []OnOrderQty
	err         error
	gotBranch   string
	gotProducts []string
	calls       int
}

func (f *fakeOnOrder) OutstandingOnOrderBase(_ context.Context, branchID string, productIDs []string) ([]OnOrderQty, error) {
	f.calls++
	f.gotBranch, f.gotProducts = branchID, productIDs
	return f.rows, f.err
}

func newWorklistSvc(rows []ShortfallRow) (*Service, *fakeShortfallStore) {
	st := &fakeShortfallStore{fakeStore: newFakeStore(), rows: rows}
	return NewService(nil).withStore(st), st
}

// - the fixture from the ticket -

const (
	trt470 = "5d1b3f6e-0000-4000-9000-000000000001"
	blu60  = "a7000000-0000-4000-9000-000000000001"
	aberdn = "a7000000-0000-4000-9000-000000000002"
)

// blu60Line is the measured live row: 800 on hand at Trenton, nothing held, nothing on a truck,
// against a reorder point of 900, short by 100 until the 160 PC on order is counted.
func blu60Line() ShortfallRow {
	return ShortfallRow{
		ProductID: blu60, SKU: "DBS-TB-BLU60", ProductName: "Techo-Bloc Blu 60 Slab",
		BranchID: trt470, BranchCode: "TRT", UOM: "PC",
		OnHandBase: 800, ReservedBase: 0, InTransitBase: 0,
		ReorderPointBase: 900, ReorderQtyBase: 120, OrderMultipleBase: 60,
		PreferredVendorID: "v-alcli", AvgCostCents: 1250,
	}
}

// TestDIB470_BLU60_160PCOutstandingStopsTheSuggestion is the ticket, as a test.
//
// DBS-TB-BLU60 at Trenton with two open purchase orders totalling 160 PC outstanding. The projected
// position is 800 + 160 = 960, which clears the reorder point of 900, so the product is NOT short
// and the worklist must not suggest buying any more of it.
//
// Before the fix this row was listed with `on order 0`, `projected 800` and a suggestion of 120 PC -
// 120 pieces of a product with 160 pieces already on the way.
func TestDIB470_BLU60_160PCOutstandingStopsTheSuggestion(t *testing.T) {
	svc, _ := newWorklistSvc([]ShortfallRow{blu60Line()})
	svc.WithOnOrderReader(&fakeOnOrder{rows: []OnOrderQty{
		{ProductID: blu60, BranchID: trt470, QtyBase: 100}, // #PO-TRT-2
		{ProductID: blu60, BranchID: trt470, QtyBase: 60},  // #PO-TRT-3
	}})

	wl, err := svc.ShortfallWorklist(context.Background(), trt470, 200)
	if err != nil {
		t.Fatalf("worklist: %v", err)
	}
	if len(wl.Rows) != 0 {
		t.Fatalf("DBS-TB-BLU60 is still suggested with 160 PC on order: %+v", wl.Rows)
	}
	if wl.OnOrderDegraded {
		t.Error("the seam answered, so the on-order figure is not degraded")
	}
}

// TestDIB470_OnOrderIsSummedAcrossOpenOrders, two purchase orders for one product at one branch are
// ONE position. Summing rather than taking the first (or the largest) is what makes 100 + 60 = 160.
func TestDIB470_OnOrderIsSummedAcrossOpenOrders(t *testing.T) {
	line := blu60Line()
	line.ReorderPointBase = 1000 // still short at 960, so the row stays and its number is readable
	svc, _ := newWorklistSvc([]ShortfallRow{line})
	svc.WithOnOrderReader(&fakeOnOrder{rows: []OnOrderQty{
		{ProductID: blu60, BranchID: trt470, QtyBase: 100},
		{ProductID: blu60, BranchID: trt470, QtyBase: 60},
	}})

	wl, err := svc.ShortfallWorklist(context.Background(), trt470, 200)
	if err != nil || len(wl.Rows) != 1 {
		t.Fatalf("want one short row, got %d (err %v)", len(wl.Rows), err)
	}
	got := wl.Rows[0]
	if got.OnOrderBase != 160 {
		t.Fatalf("on order = %v, want 160 (100 + 60, two orders for one product are one position)", got.OnOrderBase)
	}
	if got.ProjectedBase != 960 {
		t.Fatalf("projected = %v, want 960 (800 available + 160 on order)", got.ProjectedBase)
	}
	if got.ShortBase != 40 {
		t.Fatalf("short = %v, want 40 (point 1000 − projected 960)", got.ShortBase)
	}
	// The gap is 40, the standard order is 120, the skid multiple is 60 → 120.
	if got.SuggestedQtyBase != 120 {
		t.Fatalf("suggested = %v, want 120", got.SuggestedQtyBase)
	}
}

// TestDIB470_QuantitiesAreBaseUnitsNotSellUnits, the seam speaks BASE units and the worklist adds
// them to a position counted in base units.
//
// 160 PC of a product sold by the skid is 160, not 160 skids and not 160/60 skids. A quantity
// converted on the way in would land as a plausible-looking number (2.67, or 9600) that nobody can
// reproduce by hand, and the position it produces would be wrong in the direction of not ordering.
func TestDIB470_QuantitiesAreBaseUnitsNotSellUnits(t *testing.T) {
	line := blu60Line()
	line.ReorderPointBase = 1000
	svc, _ := newWorklistSvc([]ShortfallRow{line})
	svc.WithOnOrderReader(&fakeOnOrder{rows: []OnOrderQty{{ProductID: blu60, BranchID: trt470, QtyBase: 160}}})

	wl, err := svc.ShortfallWorklist(context.Background(), trt470, 200)
	if err != nil || len(wl.Rows) != 1 {
		t.Fatalf("want one row, got %d (err %v)", len(wl.Rows), err)
	}
	// 160 PIECES on a 60-piece skid multiple: if the seam's number were read as skids the position
	// would be 800 + 2.67 = 802.67, and if it were multiplied by the skid it would be 10400.
	if got := wl.Rows[0].ProjectedBase; got != 960 {
		t.Fatalf("projected = %v, want 960, the on-order quantity was not added in BASE units", got)
	}
	if got := wl.Rows[0].UOM; got != "PC" {
		t.Fatalf("the row's unit is %q, the quantities above are only readable in the product's base unit", got)
	}
}

// TestDIB470_TheSeamIsAskedAboutTheBranchTheWorklistIsShowing, the question carries the scope.
func TestDIB470_TheSeamIsAskedAboutTheBranchTheWorklistIsShowing(t *testing.T) {
	svc, _ := newWorklistSvc([]ShortfallRow{blu60Line()})
	seam := &fakeOnOrder{}
	svc.WithOnOrderReader(seam)

	if _, err := svc.ShortfallWorklist(context.Background(), trt470, 200); err != nil {
		t.Fatalf("worklist: %v", err)
	}
	if seam.gotBranch != trt470 {
		t.Fatalf("the seam was asked about branch %q, the worklist is showing %q", seam.gotBranch, trt470)
	}
	if seam.gotProducts != nil {
		t.Errorf("the worklist grades every managed line, so it must not narrow the seam to %v", seam.gotProducts)
	}

	// And the cross-branch worklist asks a cross-branch question.
	if _, err := svc.ShortfallWorklist(context.Background(), "", 200); err != nil {
		t.Fatalf("worklist: %v", err)
	}
	if seam.gotBranch != "" {
		t.Fatalf("an all-branch worklist asked the seam about branch %q", seam.gotBranch)
	}
	if seam.calls != 2 {
		t.Fatalf("the seam was called %d times for two worklists, one read, one question", seam.calls)
	}
}

// TestDIB470_TheStoreIsHandedTheClampedLimit, the on-order read does not change the paging
// contract: the service still clamps, and the store still sees the clamped number.
func TestDIB470_TheStoreIsHandedTheClampedLimit(t *testing.T) {
	svc, st := newWorklistSvc(nil)
	svc.WithOnOrderReader(&fakeOnOrder{})
	if _, err := svc.ShortfallWorklist(context.Background(), trt470, 0); err != nil {
		t.Fatalf("worklist: %v", err)
	}
	if st.gotLimit != 200 {
		t.Fatalf("limit = %d, want the 200 default", st.gotLimit)
	}
	if _, err := svc.ShortfallWorklist(context.Background(), trt470, 9000); err != nil {
		t.Fatalf("worklist: %v", err)
	}
	if st.gotLimit != 200 {
		t.Fatalf("limit = %d, want an over-large request clamped to 200", st.gotLimit)
	}
}

// TestDIB470_OnOrderIsMatchedPerBranch, 160 PC on order at Trenton is not 160 PC at Kingston.
func TestDIB470_OnOrderIsMatchedPerBranch(t *testing.T) {
	kgn := "5d1b3f6e-0000-4000-9000-000000000002"
	trtLine, kgnLine := blu60Line(), blu60Line()
	kgnLine.BranchID, kgnLine.BranchCode = kgn, "KGN"
	svc, _ := newWorklistSvc([]ShortfallRow{trtLine, kgnLine})
	// Everything is on order at TRENTON; Kingston has nothing coming.
	svc.WithOnOrderReader(&fakeOnOrder{rows: []OnOrderQty{{ProductID: blu60, BranchID: trt470, QtyBase: 160}}})

	wl, err := svc.ShortfallWorklist(context.Background(), "", 200)
	if err != nil {
		t.Fatalf("worklist: %v", err)
	}
	if len(wl.Rows) != 1 {
		t.Fatalf("want only the Kingston row to be short, got %d rows", len(wl.Rows))
	}
	if wl.Rows[0].BranchID != kgn {
		t.Fatalf("the short row is at %q, Trenton's purchase order was credited to the wrong yard", wl.Rows[0].BranchID)
	}
	if wl.Rows[0].OnOrderBase != 0 {
		t.Fatalf("Kingston shows %v on order and has no purchase order", wl.Rows[0].OnOrderBase)
	}
}

// TestDIB470_UnboundSeamGradesWithZeroAndSaysSo, the honest unwired posture.
//
// Not the legacy count: it has been structurally zero since 0120, so falling back to it would
// produce the SAME NUMBER while claiming to have measured something. Zero, and the worklist reports
// the leg as degraded so the surface can say the position is missing a piece.
func TestDIB470_UnboundSeamGradesWithZeroAndSaysSo(t *testing.T) {
	svc, st := newWorklistSvc([]ShortfallRow{blu60Line()})

	wl, err := svc.ShortfallWorklist(context.Background(), trt470, 200)
	if err != nil {
		t.Fatalf("worklist: %v", err)
	}
	if !wl.OnOrderDegraded {
		t.Fatal("no seam is bound and the worklist presented its zeros as measured")
	}
	if len(wl.Rows) != 1 || wl.Rows[0].OnOrderBase != 0 {
		t.Fatalf("an unbound seam must contribute zero, got %+v", wl.Rows)
	}
	if st.gotOnOrder != nil {
		t.Errorf("the store was handed %v for an unbound seam", st.gotOnOrder)
	}
	// The list still works. Degrading one leg must not take away the other four.
	if wl.Rows[0].AvailableBase != 800 || wl.Rows[0].SuggestedQtyBase != 120 {
		t.Errorf("the degraded worklist stopped grading: %+v", wl.Rows[0])
	}
}

// TestDIB470_ABoundSeamThatFailsIsAnError, a seam that answered "I am broken" must not be laundered
// into a silent zero, which is the state the defect looked like from the outside.
func TestDIB470_ABoundSeamThatFailsIsAnError(t *testing.T) {
	svc, _ := newWorklistSvc([]ShortfallRow{blu60Line()})
	svc.WithOnOrderReader(&fakeOnOrder{err: errors.New("purchasing: connection refused")})

	if _, err := svc.ShortfallWorklist(context.Background(), trt470, 200); err == nil {
		t.Fatal("a failing on-order seam produced a worklist instead of an error")
	}
}

// TestDIB470_CreatePOFromShortfallUsesTheSameGradedRows, the button and the list agree.
//
// The selection is graded through the same seam, so a product that is no longer short because of
// what is on order cannot be ordered from the worklist "because the row was there a moment ago".
func TestDIB470_CreatePOFromShortfallUsesTheSameGradedRows(t *testing.T) {
	svc, _ := newWorklistSvc([]ShortfallRow{blu60Line()})
	svc.WithOnOrderReader(&fakeOnOrder{rows: []OnOrderQty{{ProductID: blu60, BranchID: trt470, QtyBase: 160}}})

	_, _, _, err := svc.CreatePOFromShortfall(context.Background(), trt470, []string{blu60})
	if err == nil {
		t.Fatal("a product that is no longer short was drafted onto a purchase order anyway")
	}
	if !errors.Is(err, err) || err.Error() == "" {
		t.Fatalf("unhelpful refusal: %v", err)
	}
	if got := err.Error(); got != "inventory: none of the selected products is currently short" {
		t.Fatalf("refusal = %q, want the not-short sentence", got)
	}
}

// TestDIB470_OrderNowGradesWithTheSameSeamNumber, DIB-461's single-line read is the worklist's row
// for one product, and it must be handed the same on-order figure or the two screens suggest
// different quantities for the same product on the same day.
func TestDIB470_OrderNowGradesWithTheSameSeamNumber(t *testing.T) {
	f := &fakeIncoming{
		lineFound: true,
		line: ShortfallRow{
			ProductID: blu60, SKU: "DBS-TB-BLU60", BranchID: trt470, UOM: "PC",
			OnHandBase: 800, ReorderPointBase: 1000, ReorderQtyBase: 120, OrderMultipleBase: 60,
			PreferredVendorID: "v-alcli", AvgCostCents: 1250,
		},
	}
	svc := newIncomingSvc(f)
	seam := &fakeOnOrder{rows: []OnOrderQty{{ProductID: blu60, BranchID: trt470, QtyBase: 160}}}
	svc.WithOnOrderReader(seam)

	res, err := svc.CreateReorderPO(context.Background(),
		BranchReach{BranchIDs: []string{trt470}}, ReorderRequest{ProductID: blu60, BranchID: trt470})
	// The purchasing seam is unbound, so the draft comes back with the stub's 503, what this test
	// is about is the QUANTITY on that draft.
	if err == nil {
		t.Fatal("expected the unwired-purchasing refusal, which carries the draft")
	}
	if f.gotOnOrder != 160 {
		t.Fatalf("the single-line read was handed %v on order, the seam said 160", f.gotOnOrder)
	}
	if seam.gotProducts == nil || len(seam.gotProducts) != 1 || seam.gotProducts[0] != blu60 {
		t.Fatalf("a one-product read asked the seam about %v, it should name the product", seam.gotProducts)
	}
	// Position 800 + 160 = 960 against a point of 1000: the gap is 40, floored at the standard 120.
	if res.Suggested != 120 {
		t.Fatalf("suggested = %v, want 120", res.Suggested)
	}
	if len(res.Draft.Lines) != 1 || res.Draft.Lines[0].QtyBase != 120 {
		t.Fatalf("draft = %+v, want one line of 120", res.Draft.Lines)
	}
}

// TestDIB470_UnnestOnOrderKeepsTheThreeArraysAligned, the parameter arrays the SQL unnests are
// positional, so a dropped row must drop from all three or the query pairs a product with somebody
// else's quantity.
func TestDIB470_UnnestOnOrderKeepsTheThreeArraysAligned(t *testing.T) {
	products, branches, qty := unnestOnOrder([]OnOrderQty{
		{ProductID: blu60, BranchID: trt470, QtyBase: 100},
		{ProductID: "", BranchID: trt470, QtyBase: 999},    // no product, unmatchable, and a uuid cast error
		{ProductID: aberdn, BranchID: "", QtyBase: 999},    // no branch, same
		{ProductID: aberdn, BranchID: trt470, QtyBase: 60}, // kept
	})
	if len(products) != 2 || len(branches) != 2 || len(qty) != 2 {
		t.Fatalf("arrays are ragged: %v / %v / %v", products, branches, qty)
	}
	if products[1] != aberdn || branches[1] != trt470 || qty[1] != 60 {
		t.Fatalf("row 1 is %s / %s / %v, the arrays drifted out of step", products[1], branches[1], qty[1])
	}
}

// TestDIB470_OnOrderBaseForSumsAndScopes pins the picker the single-line path uses.
func TestDIB470_OnOrderBaseForSumsAndScopes(t *testing.T) {
	rows := []OnOrderQty{
		{ProductID: blu60, BranchID: trt470, QtyBase: 100},
		{ProductID: blu60, BranchID: trt470, QtyBase: 60},
		{ProductID: blu60, BranchID: "kgn", QtyBase: 500},
		{ProductID: aberdn, BranchID: trt470, QtyBase: 40},
	}
	if got := onOrderBaseFor(rows, blu60, trt470); got != 160 {
		t.Fatalf("got %v, want 160, two orders for one product at one branch are one position", got)
	}
	if got := onOrderBaseFor(rows, aberdn, trt470); got != 40 {
		t.Fatalf("got %v, want 40", got)
	}
	if got := onOrderBaseFor(rows, "unknown", trt470); got != 0 {
		t.Fatalf("got %v, want 0 for a product with nothing on order", got)
	}
}
