package pricing

import (
	"context"
	"testing"
)

// The counter resolve's ruling tests (D48 row 481, D49): the same line prices two ways, the
// till's way and the quote flow's way, and only the quote flow's way meets the floor.

// The pairing, arm one: a counter resolve below the floor is NOT blocked and writes no BYPASS
// row. The store carries the same category floor the gated path blocks on; the counter resolve
// returns before the gate step, so the ruled price stands and the audit log stays untouched.
func TestResolveCounter_BelowFloor_NotBlocked_NoBypassRow(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 10000, "EA"},
		catPolicy: &CategoryPolicy{CategoryID: "pavers", MarginTargetBps: 3000, MarginFloorBps: 2000},
	}
	lp, err := newSvc(st, "pavers").DeriveCounterLinePrice(context.Background(),
		base(ResolveInput{CategoryID: "pavers", UOMEntered: "EA", CostCents: 8500}))
	if err != nil {
		t.Fatalf("counter resolve: %v", err)
	}
	if lp.Breakdown.Blocked || lp.Breakdown.BlockPolicy != "" {
		t.Fatalf("the till's line must ring below the floor unblocked, got %+v", lp.Breakdown)
	}
	if lp.UnitPriceCents != 10000 || lp.LineTotalCents != 10000 {
		t.Fatalf("the ruled price is the waterfall price 10000, got %+v", lp)
	}
	if lp.Breakdown.BypassReason != "" || lp.Breakdown.FloorBps != 0 || lp.Breakdown.MarginBps != 0 {
		t.Fatalf("the counter resolve must answer no gate facts at all, got %+v", lp.Breakdown)
	}
	for _, a := range st.audits {
		if a.Action == "BYPASS" {
			t.Fatalf("a till line must never write a BYPASS row, got %+v", st.audits)
		}
	}
}

// The pairing, arm two: the SAME line through the gated resolve (what POST
// /api/v1/pricing/resolve and the quote path call) is blocked exactly as today. The floor
// question lives there alone.
func TestResolveCounter_SameLineThroughGatedResolve_Blocked(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 10000, "EA"},
		catPolicy: &CategoryPolicy{CategoryID: "pavers", MarginTargetBps: 3000, MarginFloorBps: 2000},
	}
	bd := resolve(t, newSvc(st, "pavers"), base(ResolveInput{CategoryID: "pavers", UOMEntered: "EA", CostCents: 8500}))
	if !bd.Blocked || bd.BlockPolicy != BlockMarginBelowMinimum || bd.FloorBps != 2000 {
		t.Fatalf("the gated resolve of the same line must block as today, got %+v", bd)
	}
}

// The enforcement-disabled arm: the gated resolve of a disabled item records a BYPASS row (no
// silent bypass); the counter resolve of the same item writes NOTHING, it never reaches the
// step that writes.
func TestResolveCounter_EnforcementDisabled_WritesNothing(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 10000, "EA"},
		catPolicy: &CategoryPolicy{CategoryID: "aggregates", MarginTargetBps: 1500, MarginFloorBps: 1000, EnforcementDisabled: true},
	}
	lp, err := newSvc(st, "aggregates").DeriveCounterLinePrice(context.Background(),
		base(ResolveInput{CategoryID: "aggregates", UOMEntered: "EA", CostCents: 9800, UserID: "ryan"}))
	if err != nil {
		t.Fatalf("counter resolve: %v", err)
	}
	if lp.Breakdown.Blocked || lp.Breakdown.BypassReason != "" {
		t.Fatalf("the till's line must carry no bypass reason, got %+v", lp.Breakdown)
	}
	if len(st.audits) != 0 {
		t.Fatalf("a till line must write no audit rows at all, got %+v", st.audits)
	}
	// And the gated resolve of the same line still records, unchanged.
	bd := resolve(t, newSvc(st, "aggregates"), base(ResolveInput{CategoryID: "aggregates", UOMEntered: "EA", CostCents: 9800, UserID: "ryan"}))
	if bd.BypassReason == "" {
		t.Fatalf("the gated resolve must keep the disabled bypass reason, got %+v", bd)
	}
}

// The rule price is the SAME number both ways: the counter resolve differs from the gated
// resolve in the floor question only, never in the price the waterfall resolves.
func TestResolveCounter_PriceIdenticalToGatedResolve(t *testing.T) {
	vf := fakeUOM{stdFactors()}
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 48700, "SF"},
		rules:     []Rule{{ID: "r-tc", Scope: ScopeTierCategory, CustomerType: "CONTRACTOR", CategoryID: "pavers", CalcType: CalcPctOffList, DiscountBps: p64(1800), PriceUOM: "SF", Priority: 40, IsActive: true}},
	}
	svc := NewService(st).WithUOMConverter(vf).WithMarginGate(NewIdentityMarginGate()).WithCategoryResolver(fakeCat{cat: "pavers"})
	counter, err := svc.ResolveCounterPrice(context.Background(), base(ResolveInput{CategoryID: "pavers", QtyEntered: 100, UOMEntered: "SF"}))
	if err != nil {
		t.Fatalf("counter resolve: %v", err)
	}
	gated := resolve(t, svc, base(ResolveInput{CategoryID: "pavers", QtyEntered: 100, UOMEntered: "SF"}))
	if counter.FinalCents != gated.FinalCents || counter.LineTotalCents != gated.LineTotalCents || counter.Source != gated.Source {
		t.Fatalf("one waterfall, one price: counter %+v gated %+v", counter, gated)
	}
	if counter.FinalCents != 39934 { // 48700 less the 1800 bps contractor pavers rule
		t.Fatalf("the rule price is 39934, got %d", counter.FinalCents)
	}
}
