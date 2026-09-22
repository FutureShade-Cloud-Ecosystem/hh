package pricing

// uomplan_test.go, SCOPE M2-uom. The SF → PLT → TON chain, the scope precedence, and the thing
// this feature actually exists for: ROUND-UP AT PICK.
//
// The round-up assertions are the mutation target named in REPORT-W4-A.md. They are written so
// that math.Ceil → math.Round and math.Ceil → math.Floor BOTH fail on the quantity, not on some
// downstream total, a pick ticket that is one pallet short is the failure, and the test has to say
// so in those words.

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
)

// - DB-free conversion fake -

type fakeConvStore struct {
	rows []UOMConversion
}

func (f *fakeConvStore) ConversionsFor(_ context.Context, productID, categoryID string) ([]UOMConversion, error) {
	var out []UOMConversion
	for _, c := range f.rows {
		switch {
		case c.ProductID != "":
			if c.ProductID == productID {
				out = append(out, c)
			}
		case c.CategoryID != "":
			if c.CategoryID == categoryID {
				out = append(out, c)
			}
		default:
			out = append(out, c)
		}
	}
	return out, nil
}
func (f *fakeConvStore) ListConversions(_ context.Context, productID string, _ bool) ([]UOMConversion, error) {
	if productID == "" {
		return f.rows, nil
	}
	var out []UOMConversion
	for _, c := range f.rows {
		if c.ProductID == productID {
			out = append(out, c)
		}
	}
	return out, nil
}
func (f *fakeConvStore) UpsertConversion(_ context.Context, c UOMConversion) (UOMConversion, error) {
	c.ID, c.Version, c.IsActive = "conv-"+c.FromUOM+"-"+c.ToUOM, 1, true
	f.rows = append(f.rows, c)
	return c, nil
}
func (f *fakeConvStore) DeleteConversion(context.Context, string) error { return nil }

// brussels is the seeded shape from migration 0113 for a real SKU: SF→PLT derived from the
// catalogue factors (4.10 base per SF ÷ 50 base per PLT = 0.082 PLT per SF) and a placeholder
// PLT→T weight. Every number here mirrors a row the migration actually writes.
func brussels() *fakeConvStore {
	return &fakeConvStore{rows: []UOMConversion{
		{ID: "c1", ProductID: "brussels", FromUOM: "SF", ToUOM: "PLT", Factor: 0.082, RoundUpAtPick: true, Source: ConvSeededDefault, IsActive: true},
		{ID: "c2", ProductID: "brussels", FromUOM: "PLT", ToUOM: "T", Factor: 1.10, Source: ConvSeededDefault, IsActive: true},
	}}
}

func convSvc(cs *fakeConvStore) *Service {
	return newSvc(&fakeStore{}, "").WithConversionStore(cs)
}

func plan(t *testing.T, s *Service, in UOMPlanInput) UOMPlan {
	t.Helper()
	p, err := s.PlanUOM(context.Background(), in)
	if err != nil {
		t.Fatalf("PlanUOM: %v", err)
	}
	return p
}

// - ROUND-UP AT PICK -

func TestPlanUOM_RoundsUpToAWholePallet(t *testing.T) {
	// 77 SF × 0.082 PLT/SF = 6.314 pallets. The yard pulls SEVEN.
	//
	// This single assertion is the whole feature. math.Round(6.314) = 6 sends the customer away a
	// pallet short; math.Floor is the same failure more obviously. The ONLY direction a pick
	// quantity can move is up.
	p := plan(t, convSvc(brussels()), UOMPlanInput{ProductID: "brussels", QtyEntered: 77, UOMEntered: "SF"})

	if p.PickUOM != "PLT" {
		t.Fatalf("the pick unit must be discovered from the round_up edge, got %q", p.PickUOM)
	}
	if math.Abs(p.PickQtyExact-6.314) > 1e-9 {
		t.Fatalf("exact pick qty = 77 × 0.082 = 6.314, got %v", p.PickQtyExact)
	}
	if p.PickQty != 7 {
		t.Fatalf("a banded pallet cannot be split: 6.314 pallets is SEVEN picks, got %v", p.PickQty)
	}
	if !p.RoundedUp {
		t.Fatalf("RoundedUp must flag that the pick differs from the order")
	}
	// The overage is reported, not hidden: 0.686 of a pallet leaves the yard and somebody has to
	// decide who pays for it. A plan that returned only the rounded number would make that
	// decision invisible.
	if math.Abs(p.OverageQty-0.686) > 1e-9 {
		t.Fatalf("overage = 7 − 6.314 = 0.686 PLT, got %v", p.OverageQty)
	}
}

func TestPlanUOM_AnExactPalletDoesNotRound(t *testing.T) {
	// The control. Without it, `PickQty = PickQtyExact + 1` would pass the test above.
	// 100 SF × 0.082 = 8.2 → 9; 500 SF × 0.082 = 41.0 → 41 exactly, and RoundedUp must be false.
	cs := &fakeConvStore{rows: []UOMConversion{
		{ID: "c1", ProductID: "p", FromUOM: "SF", ToUOM: "PLT", Factor: 0.1, RoundUpAtPick: true, Source: ConvMeasured, IsActive: true},
	}}
	p := plan(t, convSvc(cs), UOMPlanInput{ProductID: "p", QtyEntered: 50, UOMEntered: "SF"})
	if p.PickQty != 5 || p.RoundedUp || p.OverageQty != 0 {
		t.Fatalf("50 SF at 0.1 PLT/SF is exactly 5 pallets with no overage, got qty=%v rounded=%v over=%v",
			p.PickQty, p.RoundedUp, p.OverageQty)
	}
}

func TestPlanUOM_AHairOverAPalletStillCostsAWholeOne(t *testing.T) {
	// The nastiest real case: 5.0001 pallets. Rounding would say 5 and the truck leaves 0.0001 of a
	// pallet short, which in the yard means the last customer of the day gets nothing.
	cs := &fakeConvStore{rows: []UOMConversion{
		{ID: "c1", ProductID: "p", FromUOM: "SF", ToUOM: "PLT", Factor: 0.1, RoundUpAtPick: true, Source: ConvMeasured, IsActive: true},
	}}
	p := plan(t, convSvc(cs), UOMPlanInput{ProductID: "p", QtyEntered: 50.001, UOMEntered: "SF"})
	if p.PickQty != 6 {
		t.Fatalf("5.0001 pallets rounds UP to 6, got %v", p.PickQty)
	}
}

func TestPlanUOM_ADivisibleUnitIsNotRounded(t *testing.T) {
	// round_up_at_pick is per EDGE, not per unit: bagged goods on a pallet CAN be split, and a
	// blanket "pallets are indivisible" rule would over-pick every one of those lines.
	cs := &fakeConvStore{rows: []UOMConversion{
		{ID: "c1", ProductID: "bags", FromUOM: "BAG", ToUOM: "PLT", Factor: 0.02, RoundUpAtPick: false, Source: ConvMeasured, IsActive: true},
	}}
	p := plan(t, convSvc(cs), UOMPlanInput{ProductID: "bags", QtyEntered: 77, UOMEntered: "BAG", PickUOM: "PLT"})
	if p.RoundedUp || math.Abs(p.PickQty-1.54) > 1e-9 {
		t.Fatalf("a divisible handling unit keeps the exact quantity, got %v (rounded=%v)", p.PickQty, p.RoundedUp)
	}
}

// - THE CHAIN: SF → PLT → TON -

func TestPlanUOM_WeightResolvesThroughTheTwoHopChain(t *testing.T) {
	// Neither SF→T nor its factor is authored anywhere. The graph composes it from SF→PLT and
	// PLT→T, which is the whole reason the table holds edges instead of pairs.
	p := plan(t, convSvc(brussels()), UOMPlanInput{
		ProductID: "brussels", QtyEntered: 77, UOMEntered: "SF", WeightUOM: "T",
	})
	if p.WeightUOM != "T" {
		t.Fatalf("weight unit must be reported, got %q", p.WeightUOM)
	}
	// WEIGHT IS OF WHAT LEAVES THE YARD: 7 picked pallets × 1.10 t = 7.7 t, NOT the 6.314 pallets
	// the customer ordered (6.945 t). Load weight is a legal limit on a public road, so
	// under-stating it by understating the pick is the one direction that must not happen.
	if math.Abs(p.WeightQty-7.7) > 1e-9 {
		t.Fatalf("weight must follow the ROUNDED pick (7 × 1.10 = 7.7 t), got %v", p.WeightQty)
	}
}

func TestPlanUOM_ReverseEdgesAreUsableAtTheInverseFactor(t *testing.T) {
	// Only SF→PLT is authored. PLT→SF is its mathematical inverse, so a line entered in pallets
	// still plans, without anybody authoring a second row that could drift from the first.
	cs := &fakeConvStore{rows: []UOMConversion{
		{ID: "c1", ProductID: "p", FromUOM: "SF", ToUOM: "PLT", Factor: 0.1, RoundUpAtPick: true, Source: ConvMeasured, IsActive: true},
	}}
	p := plan(t, convSvc(cs), UOMPlanInput{ProductID: "p", QtyEntered: 3, UOMEntered: "PLT", PickUOM: "SF"})
	if math.Abs(p.PickQtyExact-30) > 1e-9 {
		t.Fatalf("3 PLT at 0.1 PLT/SF is 30 SF, got %v", p.PickQtyExact)
	}
	// AND the reverse edge must NOT inherit round_up: SF is not indivisible just because PLT is.
	if p.RoundedUp {
		t.Fatalf("the reverse of an indivisible edge is not itself indivisible")
	}
}

// - scope precedence, per edge -

func TestPlanUOM_ProductOverridesCategoryPerEdge(t *testing.T) {
	// The product overrides ONLY its weight edge. It must still inherit the category's pallet edge
	//, a per-LOOKUP precedence would have the single product row silently disable that inheritance
	// and leave the line unplannable.
	cs := &fakeConvStore{rows: []UOMConversion{
		{ID: "cat-sf", CategoryID: "pavers", FromUOM: "SF", ToUOM: "PLT", Factor: 0.1, RoundUpAtPick: true, Source: ConvVendorSheet, IsActive: true},
		{ID: "cat-wt", CategoryID: "pavers", FromUOM: "PLT", ToUOM: "T", Factor: 1.0, Source: ConvSeededDefault, IsActive: true},
		{ID: "prod-wt", ProductID: "heavy", FromUOM: "PLT", ToUOM: "T", Factor: 1.8, Source: ConvMeasured, IsActive: true},
	}}
	p := plan(t, convSvc(cs), UOMPlanInput{
		ProductID: "heavy", CategoryID: "pavers", QtyEntered: 100, UOMEntered: "SF", WeightUOM: "T",
	})
	if p.PickQty != 10 {
		t.Fatalf("the CATEGORY pallet edge must still be inherited: 100 × 0.1 = 10, got %v", p.PickQty)
	}
	if math.Abs(p.WeightQty-18) > 1e-9 {
		t.Fatalf("the PRODUCT weight edge must win: 10 × 1.8 = 18 t, got %v", p.WeightQty)
	}
}

func TestPlanUOM_GlobalEdgeIsTheFallback(t *testing.T) {
	cs := &fakeConvStore{rows: []UOMConversion{
		{ID: "g", FromUOM: "KG", ToUOM: "T", Factor: 0.001, Source: ConvMeasured, IsActive: true},
	}}
	p := plan(t, convSvc(cs), UOMPlanInput{ProductID: "anything", QtyEntered: 2500, UOMEntered: "KG", WeightUOM: "T"})
	if math.Abs(p.WeightQty-2.5) > 1e-9 {
		t.Fatalf("2500 kg = 2.5 t through the global edge, got %v", p.WeightQty)
	}
}

// - the provenance flag rides on the payload -

func TestPlanUOM_SeededDefaultsAreFlaggedUnconfirmed(t *testing.T) {
	// The open client question (0113) has to travel WITH the number to every surface that renders
	// it, not be annotated on the one screen someone remembered. A plan built on placeholder
	// factors says so in its own payload.
	p := plan(t, convSvc(brussels()), UOMPlanInput{
		ProductID: "brussels", QtyEntered: 77, UOMEntered: "SF", WeightUOM: "T",
	})
	if !p.Unconfirmed {
		t.Fatalf("a plan resting on SEEDED_DEFAULT factors must declare itself unconfirmed")
	}
	if len(p.Notes) == 0 || !strings.Contains(strings.Join(p.Notes, " "), "SEEDED DEFAULT") {
		t.Fatalf("the notes must name which hop is unconfirmed, got %v", p.Notes)
	}
}

func TestPlanUOM_ConfirmedFactorsAreNotFlagged(t *testing.T) {
	cs := &fakeConvStore{rows: []UOMConversion{
		{ID: "c1", ProductID: "p", FromUOM: "SF", ToUOM: "PLT", Factor: 0.1, RoundUpAtPick: true, Source: ConvClientConfirm, IsActive: true},
	}}
	p := plan(t, convSvc(cs), UOMPlanInput{ProductID: "p", QtyEntered: 50, UOMEntered: "SF"})
	if p.Unconfirmed || len(p.Notes) != 0 {
		t.Fatalf("a fully client-confirmed chain must not be flagged, got %+v", p)
	}
}

// - refusals -

func TestPlanUOM_AMissingChainIsAnErrorNotAZero(t *testing.T) {
	// Same ruling the resolver already makes for a missing price factor: a misconfigured conversion
	// must FAIL LOUD. A pick ticket that says 0 pallets is worse than a screen that says "no
	// conversion configured", because the zero looks like an answer.
	cs := &fakeConvStore{rows: []UOMConversion{
		{ID: "c1", ProductID: "p", FromUOM: "SF", ToUOM: "PLT", Factor: 0.1, RoundUpAtPick: true, Source: ConvMeasured, IsActive: true},
	}}
	_, err := convSvc(cs).PlanUOM(context.Background(), UOMPlanInput{
		ProductID: "p", QtyEntered: 10, UOMEntered: "SF", WeightUOM: "T",
	})
	if !errors.Is(err, errNoConversion) {
		t.Fatalf("an unreachable weight unit must surface errNoConversion, got %v", err)
	}
}

func TestPlanUOM_NoPickEdgeIsNotAnError(t *testing.T) {
	// A product with no indivisible handling unit (bulk aggregate sold by the tonne) has no pick
	// rounding to do. That is a normal line, not a misconfiguration, so it plans quietly with an
	// empty PickUOM rather than 422-ing every aggregate sale.
	cs := &fakeConvStore{rows: []UOMConversion{
		{ID: "g", FromUOM: "KG", ToUOM: "T", Factor: 0.001, Source: ConvMeasured, IsActive: true},
	}}
	p := plan(t, convSvc(cs), UOMPlanInput{ProductID: "agg", QtyEntered: 1000, UOMEntered: "KG"})
	if p.PickUOM != "" || p.RoundedUp {
		t.Fatalf("no round-up edge means no pick plan, got %+v", p)
	}
}

func TestPlanUOM_InputRefusals(t *testing.T) {
	svc := convSvc(brussels())
	cases := []struct {
		name string
		in   UOMPlanInput
	}{
		{"no product", UOMPlanInput{QtyEntered: 1, UOMEntered: "SF"}},
		{"zero qty", UOMPlanInput{ProductID: "brussels", QtyEntered: 0, UOMEntered: "SF"}},
		{"negative qty", UOMPlanInput{ProductID: "brussels", QtyEntered: -5, UOMEntered: "SF"}},
		{"no entered uom", UOMPlanInput{ProductID: "brussels", QtyEntered: 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := svc.PlanUOM(context.Background(), c.in); err == nil {
				t.Fatalf("expected a refusal")
			}
		})
	}
}

func TestPlanUOM_WithoutTheSeamIsUnavailable(t *testing.T) {
	_, err := newSvc(&fakeStore{}, "").PlanUOM(context.Background(), UOMPlanInput{
		ProductID: "p", QtyEntered: 1, UOMEntered: "SF",
	})
	if !errors.Is(err, errNoDB) {
		t.Fatalf("an unwired conversion seam must refuse with errNoDB, got %v", err)
	}
}

// - graph internals -

func TestConvGraph_UOMCodesAreCaseInsensitive(t *testing.T) {
	// uoms.code is CITEXT. A hand-typed lowercase unit that silently has no conversions is the
	// exact bug shape this normalisation prevents.
	cs := &fakeConvStore{rows: []UOMConversion{
		{ID: "c1", ProductID: "p", FromUOM: "sf", ToUOM: "plt", Factor: 0.1, RoundUpAtPick: true, Source: ConvMeasured, IsActive: true},
	}}
	p := plan(t, convSvc(cs), UOMPlanInput{ProductID: "p", QtyEntered: 50, UOMEntered: "SF", PickUOM: "PLT"})
	if p.PickQty != 5 {
		t.Fatalf("'sf' and 'SF' are the same unit, got %v", p.PickQty)
	}
}

func TestConvGraph_ADirectEdgeBeatsATwoHopRoute(t *testing.T) {
	// Breadth-first matters here: a direct, MEASURED SF→T must not be bypassed in favour of
	// composing two SEEDED_DEFAULT hops that happen to reach the same unit.
	g := buildGraph([]UOMConversion{
		{FromUOM: "SF", ToUOM: "PLT", Factor: 0.1, Source: ConvSeededDefault, IsActive: true},
		{FromUOM: "PLT", ToUOM: "T", Factor: 1.0, Source: ConvSeededDefault, IsActive: true},
		{FromUOM: "SF", ToUOM: "T", Factor: 0.25, Source: ConvMeasured, IsActive: true},
	})
	steps, ok := g.chain("SF", "T")
	if !ok || len(steps) != 1 {
		t.Fatalf("want the single direct hop, got %d steps (ok=%v)", len(steps), ok)
	}
	if steps[0].Factor != 0.25 || steps[0].Source != ConvMeasured {
		t.Fatalf("the direct MEASURED edge must be the one used, got %+v", steps[0])
	}
}

func TestConvGraph_InactiveAndNonPositiveRowsAreExcluded(t *testing.T) {
	// A zero or negative factor would produce a zero or negative pick quantity, worse than an
	// error, because it looks like a number.
	g := buildGraph([]UOMConversion{
		{FromUOM: "SF", ToUOM: "PLT", Factor: 0.1, IsActive: false},
		{FromUOM: "PLT", ToUOM: "T", Factor: 0, IsActive: true},
		{FromUOM: "T", ToUOM: "KG", Factor: -1000, IsActive: true},
	})
	if _, ok := g.chain("SF", "PLT"); ok {
		t.Fatalf("a retired edge must not be walkable")
	}
	if _, ok := g.chain("PLT", "T"); ok {
		t.Fatalf("a zero-factor edge must not be walkable")
	}
	if _, ok := g.chain("T", "KG"); ok {
		t.Fatalf("a negative-factor edge must not be walkable")
	}
}
