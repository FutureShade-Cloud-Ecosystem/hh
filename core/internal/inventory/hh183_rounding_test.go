package inventory

// hh183_rounding_test.go, HH-183: exact half-cent products must round UP (half away from zero), the
// way SQL ROUND() does on the same quantity, not with the last bit of a float64 product. The float
// product of a DECIMAL(19,4) quantity and integer cents can land below an exact half
// (0.29 x 750 is exactly 217.5 but float arithmetic evaluates 217.49999999999997), and the float
// round() then stores 217 where the SQL leg of the same receipt stores ROUND(0.2900 * 750) = 218.
// Every shape in this file pins the money arithmetic at an exact half so a regression to computing
// money through a float product fails here rather than at a counter.

import (
	"context"
	"math"
	"testing"
)

// recvHalfCent runs one full receipt through the fake store and returns the move it wrote.
func recvHalfCent(t *testing.T, qty float64, unitCost int64) moveParams {
	t.Helper()
	f := newFakeStore()
	f.hdr = poHeader{ID: "po-1", PONo: "PO-1", BranchID: "br-1", Status: "SUBMITTED", Currency: "CAD", FxRate: 1}
	f.lines = []poLine{{ID: "l1", LineNo: 1, ProductID: "p", UOM: "EA", QtyBase: qty, UnitCostCents: unitCost}}
	s := svcWith(f)
	res, err := s.applyReceipt(context.Background(), nil, "po-1", nil, "")
	if err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if len(f.moves) != 1 {
		t.Fatalf("want 1 move, got %d", len(f.moves))
	}
	if res.ValueCadCents != f.moves[0].ValueCents {
		t.Fatalf("GRNI total %d disagrees with the move value %d", res.ValueCadCents, f.moves[0].ValueCents)
	}
	return f.moves[0]
}

// AC: 0.29 x 750 cents is exactly 217.5 and must store 218 (the float product is
// 217.49999999999997; SQL ROUND(0.2900 * 750) on the same line is 218).
func TestReceive_HalfCentProduct_RoundsUp(t *testing.T) {
	m := recvHalfCent(t, 0.29, 750)
	if m.UnitCostCents != 750 {
		t.Fatalf("CAD unit cost must pass through untouched, got %d", m.UnitCostCents)
	}
	if m.ValueCents != 218 {
		t.Fatalf("0.29 x 750 = 217.5 must value at 218, got %d", m.ValueCents)
	}
}

// AC: a second exact half from the same family (two-decimal qty, cents within the audit's sweep)
// also rounds up; 0.29 x 850 = 246.5 must store 247.
func TestReceive_HalfCentProduct_AuditShape_RoundsUp(t *testing.T) {
	if got := recvHalfCent(t, 0.29, 850).ValueCents; got != 247 {
		t.Fatalf("0.29 x 850 = 246.5 must value at 247, got %d", got)
	}
}

// AC: the receipt's WAC re-derivation divides the exact value by the qty, so after receiving
// 0.29 at 218 cents the branch average is round(218 / 0.29) = 752, not round(217 / 0.29).
func TestReceive_HalfCentProduct_WeightedAverage(t *testing.T) {
	f := newFakeStore()
	f.hdr = poHeader{ID: "po-1", PONo: "PO-1", BranchID: "br-1", Status: "SUBMITTED", Currency: "CAD", FxRate: 1}
	f.lines = []poLine{{ID: "l1", LineNo: 1, ProductID: "p", UOM: "EA", QtyBase: 0.29, UnitCostCents: 750}}
	s := svcWith(f)
	if _, err := s.applyReceipt(context.Background(), nil, "po-1", nil, ""); err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if got := f.cost[key("p", "br-1")]; got != 752 {
		t.Fatalf("round(218 / 0.29) = 752 must be the branch average, got %d", got)
	}
}

// AC: the USD leg converts through the exact fx rate, 75 cents at 1.38 is exactly 103.5 CAD and
// must become 104 (the float product is 103.49999999999999), then the move values 2 x 104 = 208.
func TestReceive_HalfCentFXProduct_RoundsUp(t *testing.T) {
	f := newFakeStore()
	f.hdr = poHeader{ID: "po-1", PONo: "PO-USD", BranchID: "br-1", Status: "SUBMITTED", Currency: "USD", FxRate: 1.38}
	f.lines = []poLine{{ID: "l1", LineNo: 1, ProductID: "p", UOM: "EA", QtyBase: 2, UnitCostCents: 75}}
	s := svcWith(f)
	if _, err := s.applyReceipt(context.Background(), nil, "po-1", nil, ""); err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if len(f.moves) != 1 {
		t.Fatalf("want 1 move, got %d", len(f.moves))
	}
	if got := f.moves[0].UnitCostCents; got != 104 {
		t.Fatalf("75 x 1.38 = 103.5 must convert to 104 CAD cents, got %d", got)
	}
	if got := f.moves[0].ValueCents; got != 208 {
		t.Fatalf("2 x 104 must value at 208, got %d", got)
	}
}

// AC: the CostBasis seam values 0.29 at a 750-cent average at 218.
func TestUnitCost_HalfCentProduct_RoundsUp(t *testing.T) {
	f := newFakeStore()
	f.productCost["p"] = 750
	s := svcWith(f)
	cost, found, err := s.UnitCostCents(context.Background(), "p", 0.29)
	if err != nil || !found {
		t.Fatalf("UnitCostCents: (%v, %v)", found, err)
	}
	if cost != 218 {
		t.Fatalf("0.29 x 750 = 217.5 must value at 218, got %d", cost)
	}
}

// AC: the COLLECT_NOW decrement values 0.29 at a 750-cent average at 218.
func TestDecrement_HalfCentProduct_RoundsUp(t *testing.T) {
	f := newFakeStore()
	f.cost[key("p", "br-1")] = 750
	s := svcWith(f)
	err := s.DecrementForSaleTx(context.Background(), nil, DecrementInput{
		OrderID: "o1", BranchID: "br-1", SaleType: SaleCollectNow,
		Lines: []SaleLine{{ProductID: "p", QtyBase: 0.29}},
	})
	if err != nil {
		t.Fatalf("decrement: %v", err)
	}
	if len(f.moves) != 1 || f.moves[0].ValueCents != 218 {
		t.Fatalf("0.29 x 750 = 217.5 must value at 218, got %+v", f.moves)
	}
}

// AC: the POS return restock values 0.29 back in at the branch average, 218.
func TestRestock_HalfCentProduct_RoundsUp(t *testing.T) {
	f := newFakeStore()
	f.cost[key("p", "br-1")] = 750
	s := svcWith(f)
	err := s.RestockForReturnTx(context.Background(), nil, RestockInput{
		ReturnID: "r1", ReturnNo: "POS-R-1", BranchID: "br-1",
		Lines: []SaleLine{{ProductID: "p", QtyBase: 0.29}},
	})
	if err != nil {
		t.Fatalf("restock: %v", err)
	}
	if len(f.moves) != 1 || f.moves[0].ValueCents != 218 {
		t.Fatalf("0.29 x 750 = 217.5 must value at 218, got %+v", f.moves)
	}
}

// AC: a counted variance of 0.29 at a 750-cent average books the COUNT move at 218.
func TestCountAdjustment_HalfCentProduct_RoundsUp(t *testing.T) {
	f := newFakeStore()
	s := svcWith(f)
	if _, err := s.PostCountAdjustmentTx(context.Background(), nil, "p", "br-1", "br-1", 0.29, 750, "count sheet"); err != nil {
		t.Fatalf("count adjustment: %v", err)
	}
	if len(f.moves) != 1 || f.moves[0].ValueCents != 218 {
		t.Fatalf("0.29 x 750 = 217.5 must value at 218, got %+v", f.moves)
	}
}

// AC: landed-cost capitalization reads the on-hand value exactly, round(0.29 x 750) = 218, so
// capitalizing 100 more over 0.29 on hand sets the average to round(318 / 0.29) = 1097 and absorbs
// round(1097 x 0.29) - 218 = 100 exactly.
func TestCapitalizeLanded_HalfCentProduct_RoundsUp(t *testing.T) {
	const product, branch = "p", "b1"
	f := newFakeStore()
	f.onhand[key(product, branch)] = 0.29
	f.cost[key(product, branch)] = 750
	s := NewService(nil).withStore(f)

	absorbed, err := s.capitalizeLanded(context.Background(), nil, product, branch, 100)
	if err != nil {
		t.Fatalf("capitalizeLanded: %v", err)
	}
	if absorbed != 100 {
		t.Fatalf("the whole 100 must absorb, got %d", absorbed)
	}
	if got := f.cost[key(product, branch)]; got != 1097 {
		t.Fatalf("round(218 + 100 over 0.29) = 1097 must be the average, got %d", got)
	}
}

// AC: the landed-cost void reads the current value exactly, round(0.29 x 750) = 218, so removing an
// absorbed 218 drains the full 218 (delta -218) and recognizes the drift-0 restore path
// (218 - 218 == round(0 x 0.29)), rather than draining the float's 217.
func TestRestoreLandedCost_HalfCentProduct_RoundsUp(t *testing.T) {
	const product, branch = "p", "b1"
	f := newFakeStore()
	f.onhand[key(product, branch)] = 0.29
	f.cost[key(product, branch)] = 750
	s := NewService(nil).withStore(f)

	delta, err := s.restoreLandedCost(context.Background(), nil, product, branch, 0, 218)
	if err != nil {
		t.Fatalf("restoreLandedCost: %v", err)
	}
	if delta != -218 {
		t.Fatalf("draining an absorbed 218 over a 218-cent on-hand must delta -218, got %d", delta)
	}
	if got := f.cost[key(product, branch)]; got != 0 {
		t.Fatalf("the snapshot average 0 must be restored, got %d", got)
	}
}

// AC: the replenishment suggestion values 0.29 suggested at a 750-cent average at 218.
func TestShortfallDerive_HalfCentProduct_RoundsUp(t *testing.T) {
	r := ShortfallRow{
		OnHandBase: 10, ReorderPointBase: 10.29, ReorderQtyBase: 0, OrderMultipleBase: 0, AvgCostCents: 750,
	}
	r.Derive()
	if math.Abs(r.SuggestedQtyBase-0.29) > qtyEpsilon {
		t.Fatalf("want a 0.29 suggestion (reorder point 10.29 over 10 on hand), got %v", r.SuggestedQtyBase)
	}
	if r.SuggestedValueCents != 218 {
		t.Fatalf("0.29 x 750 = 217.5 must value at 218, got %d", r.SuggestedValueCents)
	}
}
