package pricing

// floor_preview_test.go, the editors' floor warning (CP0-6 A): the preview grades a
// proposed price against the same resolver and the same gate a quote line meets, with
// the candidate laid over the stored rules, and the confirmation lands in the pricing
// log. Every case runs on the DB-free fakes of service_test.go.
import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeCands struct{ prods []CandidateProduct }

func (f fakeCands) CandidateProducts(_ context.Context, categoryID, productID, _ string, limit int) ([]CandidateProduct, error) {
	var out []CandidateProduct
	for _, p := range f.prods {
		if productID != "" && p.ProductID != productID {
			continue
		}
		if categoryID != "" && p.CategoryID != categoryID {
			continue
		}
		out = append(out, p)
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// previewSvc: one product at 10000 list per SF, cost 7000, so list margin is 3000 bps
// against the 1200 default floor.
func previewSvc(st *fakeStore, prods ...CandidateProduct) *Service {
	if st.listPrice == nil {
		st.listPrice = &ListPrice{ProductID: "blu60", ListPriceCents: 10000, PriceUOM: "SF"}
	}
	if len(prods) == 0 {
		prods = []CandidateProduct{{ProductID: "blu60", CategoryID: "pavers", SKU: "BLU-60", Name: "Blu 60", BaseUOM: "SF", CostCents: 7000}}
	}
	return newSvc(st, "pavers").WithCandidateSource(fakeCands{prods})
}

func tierRule(pct int64) *Rule {
	return &Rule{Scope: ScopeTierCategory, CustomerType: "CONTRACTOR", CategoryID: "pavers",
		CalcType: CalcPctOffList, DiscountBps: p64(pct), PriceUOM: "SF", Priority: 10}
}

func TestPreviewFloor_ARulePricedBelowCostNamesTheProduct(t *testing.T) {
	s := previewSvc(&fakeStore{})
	out, err := s.PreviewFloor(context.Background(), FloorChange{Kind: "rule", Rule: tierRule(4000)})
	if err != nil {
		t.Fatalf("PreviewFloor: %v", err)
	}
	if len(out.Items) != 1 {
		t.Fatalf("a 40 percent rule on a 30 percent margin product must name it, got %+v", out)
	}
	it := out.Items[0]
	if it.ProductID != "blu60" || it.SKU != "BLU-60" || it.FinalCents != 6000 || it.PreviousFinalCents != 10000 {
		t.Errorf("wrong item: %+v", it)
	}
	if it.MarginBps >= it.FloorBps || it.FloorBps != 1200 || it.AlreadyUnderFloor {
		t.Errorf("the margin must sit under the 1200 floor and be newly under, got %+v", it)
	}
	if out.Evaluated != 1 || out.NotGraded != 0 || out.Truncated {
		t.Errorf("counts: %+v", out)
	}
}

func TestPreviewFloor_ARuleAboveTheFloorWarnsOfNothing(t *testing.T) {
	s := previewSvc(&fakeStore{})
	out, err := s.PreviewFloor(context.Background(), FloorChange{Kind: "rule", Rule: tierRule(1000)})
	if err != nil {
		t.Fatalf("PreviewFloor: %v", err)
	}
	if len(out.Items) != 0 || out.Evaluated != 1 {
		t.Fatalf("10 percent off leaves 2222 bps, above the floor: %+v", out)
	}
}

func TestPreviewFloor_EditingARuleReplacesTheStoredOne(t *testing.T) {
	stored := tierRule(4000)
	stored.ID, stored.IsActive = "r1", true
	st := &fakeStore{rules: []Rule{*stored}}
	s := previewSvc(st)
	// The stored 40 percent rule already prices the product under the floor.
	same := tierRule(4000)
	out, err := s.PreviewFloor(context.Background(), FloorChange{Kind: "rule", ID: "r1", Rule: same})
	if err != nil {
		t.Fatalf("PreviewFloor: %v", err)
	}
	if len(out.Items) != 1 || !out.Items[0].AlreadyUnderFloor {
		t.Fatalf("an unchanged price on a product already under the floor is marked already under: %+v", out)
	}
	// Edited down to 10 percent: the stored rule is gone from the picture, nothing is under.
	out, err = s.PreviewFloor(context.Background(), FloorChange{Kind: "rule", ID: "r1", Rule: tierRule(1000)})
	if err != nil {
		t.Fatalf("PreviewFloor: %v", err)
	}
	if len(out.Items) != 0 {
		t.Fatalf("the edited rule replaces the stored one, got %+v", out)
	}
}

func TestPreviewFloor_ACustomerPriceGradesThatCustomer(t *testing.T) {
	s := previewSvc(&fakeStore{})
	out, err := s.PreviewFloor(context.Background(), FloorChange{Kind: "override", Override: &Override{
		CustomerID: "cust1", CategoryID: "pavers", CalcType: CalcPctOffList, DiscountBps: p64(5000), PriceUOM: "SF"}})
	if err != nil {
		t.Fatalf("PreviewFloor: %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].FinalCents != 5000 {
		t.Fatalf("50 percent off must name the product at 5000, got %+v", out)
	}
}

func TestPreviewFloor_ARaisedFloorNamesTheProductsThatFallUnderIt(t *testing.T) {
	s := previewSvc(&fakeStore{})
	out, err := s.PreviewFloor(context.Background(), FloorChange{Kind: "category_policy",
		Policy: &CategoryPolicy{CategoryID: "pavers", MarginTargetBps: 5000, MarginFloorBps: 4000}})
	if err != nil {
		t.Fatalf("PreviewFloor: %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].FloorBps != 4000 || out.Items[0].AlreadyUnderFloor {
		t.Fatalf("a 4000 floor puts the 3000 bps list margin under it, newly: %+v", out)
	}
}

func TestPreviewFloor_ADisabledEnforcementWarnsOfNothing(t *testing.T) {
	s := previewSvc(&fakeStore{})
	out, err := s.PreviewFloor(context.Background(), FloorChange{Kind: "category_policy",
		Policy: &CategoryPolicy{CategoryID: "pavers", MarginTargetBps: 5000, MarginFloorBps: 4000, EnforcementDisabled: true}})
	if err != nil {
		t.Fatalf("PreviewFloor: %v", err)
	}
	if len(out.Items) != 0 {
		t.Fatalf("with the floor switched off nothing is gated: %+v", out)
	}
}

func TestPreviewFloor_ABreakGradesItsDiscountTiersAndNeverItsSurcharges(t *testing.T) {
	s := previewSvc(&fakeStore{})
	disc := &QuantityBreakHeader{Code: "B1", Name: "Half off a skid", ProductID: "blu60", ThresholdUOM: "PLT",
		Items: []QuantityBreakItem{{FromQty: 1, ValueType: BreakPctOff, DiscountBps: p64(5000), PriceUOM: "SF"}}}
	out, err := s.PreviewFloor(context.Background(), FloorChange{Kind: "quantity_break", Break: disc})
	if err != nil {
		t.Fatalf("PreviewFloor: %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].FinalCents != 5000 {
		t.Fatalf("half off at a skid prices under the floor, got %+v", out)
	}
	// The surcharge tier never changes a price under the replace rule (promo.go): nothing to warn of.
	sur := &QuantityBreakHeader{Code: "B2", Name: "Partial skid", ProductID: "blu60", ThresholdUOM: "PLT",
		Items: []QuantityBreakItem{{FromQty: 1, ValueType: BreakSurchargePct, SurchargeBps: p64(1000), PriceUOM: "SF"}}}
	out, err = s.PreviewFloor(context.Background(), FloorChange{Kind: "quantity_break", Break: sur})
	if err != nil {
		t.Fatalf("PreviewFloor: %v", err)
	}
	if len(out.Items) != 0 || out.Evaluated != 0 {
		t.Fatalf("a surcharge only break grades nothing, got %+v", out)
	}
}

func TestPreviewFloor_AProductWithNoCostIsCountedNotGuessed(t *testing.T) {
	s := previewSvc(&fakeStore{}, CandidateProduct{ProductID: "blu60", CategoryID: "pavers", SKU: "BLU-60", Name: "Blu 60", BaseUOM: "SF"})
	out, err := s.PreviewFloor(context.Background(), FloorChange{Kind: "rule", Rule: tierRule(9000)})
	if err != nil {
		t.Fatalf("PreviewFloor: %v", err)
	}
	if len(out.Items) != 0 || out.NotGraded != 1 || out.Evaluated != 0 {
		t.Fatalf("no cost on file means not graded, never a confident zero: %+v", out)
	}
}

func TestPreviewFloor_TheCandidateCapIsSaid(t *testing.T) {
	var prods []CandidateProduct
	for i := 0; i < PreviewProductCap+5; i++ {
		prods = append(prods, CandidateProduct{ProductID: "p" + string(rune('a'+i%26)) + string(rune('a'+i/26)), CategoryID: "pavers", SKU: "S", Name: "N", BaseUOM: "SF", CostCents: 7000})
	}
	s := previewSvc(&fakeStore{}, prods...)
	out, err := s.PreviewFloor(context.Background(), FloorChange{Kind: "rule", Rule: tierRule(1000)})
	if err != nil {
		t.Fatalf("PreviewFloor: %v", err)
	}
	if !out.Truncated || out.Evaluated != PreviewProductCap {
		t.Fatalf("the cap must show as truncated with %d evaluated, got %+v", PreviewProductCap, out)
	}
}

func TestPreviewFloor_RefusesWhatTheWriteWouldRefuse(t *testing.T) {
	s := previewSvc(&fakeStore{})
	cases := map[string]FloorChange{
		"unknown kind":       {Kind: "promo"},
		"rule missing":       {Kind: "rule"},
		"absolute on tier":   {Kind: "rule", Rule: &Rule{Scope: ScopeTierDefault, CustomerType: "CONTRACTOR", CalcType: CalcAbsolute, PriceCents: p64(100), PriceUOM: "SF"}},
		"floor over target":  {Kind: "category_policy", Policy: &CategoryPolicy{CategoryID: "pavers", MarginTargetBps: 1000, MarginFloorBps: 2000}},
		"break with no tier": {Kind: "quantity_break", Break: &QuantityBreakHeader{Code: "B", Name: "N", ProductID: "blu60", ThresholdUOM: "PLT"}},
	}
	for name, ch := range cases {
		if _, err := s.PreviewFloor(context.Background(), ch); err == nil {
			t.Errorf("%s: want a refusal, got none", name)
		}
	}
}

func TestPreviewFloor_WithoutAProductSourceSaysSoNotNothing(t *testing.T) {
	s := newSvc(&fakeStore{listPrice: &ListPrice{ProductID: "blu60", ListPriceCents: 10000, PriceUOM: "SF"}}, "pavers")
	_, err := s.PreviewFloor(context.Background(), FloorChange{Kind: "rule", Rule: tierRule(1000)})
	if !errors.Is(err, errNoDB) {
		t.Fatalf("an unwired product source must refuse, not answer an empty clean list: %v", err)
	}
}

func TestPreviewFloor_WritesNothing(t *testing.T) {
	st := &fakeStore{catPolicy: &CategoryPolicy{CategoryID: "pavers", MarginFloorBps: 1200, EnforcementDisabled: true}}
	s := previewSvc(st)
	if _, err := s.PreviewFloor(context.Background(), FloorChange{Kind: "rule", Rule: tierRule(1000)}); err != nil {
		t.Fatalf("PreviewFloor: %v", err)
	}
	if len(st.audits) != 0 {
		t.Fatalf("a preview must not write the pricing log (not even a BYPASS row), got %+v", st.audits)
	}
}

func TestRecordFloorConfirmation_LogsTheEditorsYesInThePricingLog(t *testing.T) {
	st := &fakeStore{byID: map[string]Rule{"r1": {ID: "r1"}}}
	s := previewSvc(st)
	err := s.RecordFloorConfirmation(context.Background(), FloorConfirmation{
		EntityType: "pricing_rule", EntityID: "r1", ProductIDs: []string{"blu60", "blu61"},
		RefType: "category", RefID: "pavers"}, "user-1")
	if err != nil {
		t.Fatalf("RecordFloorConfirmation: %v", err)
	}
	if len(st.audits) != 1 {
		t.Fatalf("one log row, got %+v", st.audits)
	}
	a := st.audits[0]
	if a.EntityType != "pricing_rule" || a.EntityID != "r1" || a.Action != "UPDATE" || a.UserID != "user-1" ||
		a.FieldName != "floor_warning_confirmed" || !strings.Contains(a.NewValue, "2 products") || !strings.Contains(a.NewValue, "blu60") {
		t.Errorf("wrong row: %+v", a)
	}
}

func TestRecordFloorConfirmation_RefusesAnUnknownRecordOrNoProducts(t *testing.T) {
	st := &fakeStore{byID: map[string]Rule{"r1": {ID: "r1"}}}
	s := previewSvc(st)
	ctx := context.Background()
	if err := s.RecordFloorConfirmation(ctx, FloorConfirmation{EntityType: "pricing_rule", EntityID: "nope", ProductIDs: []string{"p"}}, "u"); !errors.Is(err, errNotFound) {
		t.Errorf("a confirmation for a record that does not exist is a not found, got %v", err)
	}
	if err := s.RecordFloorConfirmation(ctx, FloorConfirmation{EntityType: "pricing_rule", EntityID: "r1"}, "u"); err == nil {
		t.Errorf("a confirmation of nothing is refused")
	}
	if err := s.RecordFloorConfirmation(ctx, FloorConfirmation{EntityType: "promotion", EntityID: "r1", ProductIDs: []string{"p"}}, "u"); err == nil {
		t.Errorf("an entity type outside the four is refused")
	}
	if len(st.audits) != 0 {
		t.Errorf("refusals write nothing, got %+v", st.audits)
	}
}

// A product can be under the floor for one kind of customer already and newly under it for
// another. The warning names the newly under case: reporting the old problem alone would
// hide what this change does.
func TestPreviewFloor_ANewBreachIsNotHiddenBehindAnOldOne(t *testing.T) {
	old := tierRule(4000)
	old.ID, old.IsActive = "r-old", true // CONTRACTOR already prices under the 1200 floor
	st := &fakeStore{rules: []Rule{*old}}
	s := previewSvc(st)
	out, err := s.PreviewFloor(context.Background(), FloorChange{Kind: "category_policy",
		Policy: &CategoryPolicy{CategoryID: "pavers", MarginTargetBps: 5000, MarginFloorBps: 4000}})
	if err != nil {
		t.Fatalf("PreviewFloor: %v", err)
	}
	if len(out.Items) != 1 {
		t.Fatalf("one product, one item: %+v", out)
	}
	if out.Items[0].AlreadyUnderFloor {
		t.Fatalf("the item must be the newly under case (a customer at list price), not the contractor who was already under: %+v", out.Items[0])
	}
}
