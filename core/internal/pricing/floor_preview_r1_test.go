package pricing

// floor_preview_r1_test.go, the review round's cases for the floor preview: the band that
// starts at zero, the customer's own type, a failure that is not "could not be checked",
// the bounds on a break's work, and the refusals the write also gives.
import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func isBadRequest(err error) bool {
	var b badRequestError
	return errors.As(err, &b)
}

type fakeCustTypes map[string]string

func (f fakeCustTypes) CustomerType(_ context.Context, id string) (string, error) {
	t, ok := f[id]
	if !ok {
		return "", ErrFloorCustomerNotFound
	}
	return t, nil
}

// a store whose rules read fails, the stand-in for a table the resolver cannot read.
type unreadableRules struct{ *fakeStore }

func (unreadableRules) MatchingRules(context.Context, ResolveInput) ([]Rule, error) {
	return nil, errors.New("relation pricing_rules is unreadable")
}

func TestPreviewFloor_AFromZeroBandIsGraded(t *testing.T) {
	s := previewSvc(&fakeStore{})
	h := &QuantityBreakHeader{Code: "B0", Name: "Ninety off from zero", CategoryID: "pavers", ThresholdUOM: "PLT",
		Items: []QuantityBreakItem{{FromQty: 0, ValueType: BreakPctOff, DiscountBps: p64(9000), PriceUOM: "SF"}}}
	out, err := s.PreviewFloor(context.Background(), FloorChange{Kind: "quantity_break", Break: h})
	if err != nil {
		t.Fatalf("PreviewFloor: %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].FinalCents != 1000 || out.Evaluated != 1 {
		t.Fatalf("a 90 percent band from zero prices the product at 1000, under the floor, got %+v", out)
	}
}

func TestPreviewFloor_ACustomerChangeIsGradedAtTheCustomersOwnType(t *testing.T) {
	// the CONTRACTOR tier already prices Pavers at 8200 today (18 percent off)
	tier := tierRule(1800)
	tier.ID, tier.IsActive = "tier18", true
	s := previewSvc(&fakeStore{rules: []Rule{*tier}}).WithFloorCustomerType(fakeCustTypes{"cust1": "CONTRACTOR"})
	out, err := s.PreviewFloor(context.Background(), FloorChange{Kind: "override", Override: &Override{
		CustomerID: "cust1", CategoryID: "pavers", CalcType: CalcPctOffList, DiscountBps: p64(5000), PriceUOM: "SF"}})
	if err != nil {
		t.Fatalf("PreviewFloor: %v", err)
	}
	if len(out.Items) != 1 {
		t.Fatalf("one product under the floor, got %+v", out)
	}
	if it := out.Items[0]; it.PreviousFinalCents != 8200 || it.AlreadyUnderFloor {
		t.Errorf("the customer pays 8200 today through the contractor tier, got was %d under %v", it.PreviousFinalCents, it.AlreadyUnderFloor)
	}
}

func TestPreviewFloor_AStoredCustomerRuleAlreadyUnderIsMarkedForThatCustomerType(t *testing.T) {
	tier := tierRule(7000)
	tier.ID, tier.IsActive = "tier70", true
	s := previewSvc(&fakeStore{rules: []Rule{*tier}}).WithFloorCustomerType(fakeCustTypes{"cust1": "CONTRACTOR"})
	cr := &Rule{Scope: ScopeCustomerCategory, CustomerID: "cust1", CategoryID: "pavers", CalcType: CalcPctOffList, DiscountBps: p64(7500), PriceUOM: "SF", Priority: 5}
	out, err := s.PreviewFloor(context.Background(), FloorChange{Kind: "rule", Rule: cr})
	if err != nil {
		t.Fatalf("PreviewFloor: %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].PreviousFinalCents != 3000 || !out.Items[0].AlreadyUnderFloor {
		t.Fatalf("the customer's price today is 3000 through the tier and already under the floor, got %+v", out)
	}
}

func TestPreviewFloor_AnUnknownCustomerIsNotFoundAndAnUnwiredSeamRefuses(t *testing.T) {
	ov := &Override{CustomerID: "ghost", CategoryID: "pavers", CalcType: CalcPctOffList, DiscountBps: p64(5000), PriceUOM: "SF"}
	s := previewSvc(&fakeStore{}).WithFloorCustomerType(fakeCustTypes{})
	if _, err := s.PreviewFloor(context.Background(), FloorChange{Kind: "override", Override: ov}); !errors.Is(err, errNotFound) {
		t.Errorf("an unknown customer is not found, got %v", err)
	}
	unwired := newSvc(&fakeStore{listPrice: &ListPrice{ProductID: "blu60", ListPriceCents: 10000, PriceUOM: "SF"}}, "pavers").WithCandidateSource(fakeCands{})
	if _, err := unwired.PreviewFloor(context.Background(), FloorChange{Kind: "override", Override: ov}); !errors.Is(err, errNoDB) {
		t.Errorf("a customer change with no customer seam must refuse, not grade at no type: %v", err)
	}
}

func TestPreviewFloor_AFailureWhileGradingIsAnErrorNotACountOfUnchecked(t *testing.T) {
	st := &fakeStore{listPrice: &ListPrice{ProductID: "blu60", ListPriceCents: 10000, PriceUOM: "SF"}}
	s := newSvc(nil, "pavers")
	s.store = unreadableRules{st}
	s.WithCandidateSource(fakeCands{[]CandidateProduct{{ProductID: "blu60", CategoryID: "pavers", SKU: "BLU-60", Name: "Blu 60", BaseUOM: "SF", CostCents: 7000}}})
	out, err := s.PreviewFloor(context.Background(), FloorChange{Kind: "rule", Rule: tierRule(4000)})
	if err == nil {
		t.Fatalf("a table the resolver cannot read is an error answer, got %+v", out)
	}
}

func TestPreviewFloor_OnlyKnownPerProductCasesAreNotGraded(t *testing.T) {
	// no list price and a cost: the derived anchor grades it; with an ABSOLUTE-less tier and no cost it is "no cost"
	st := &fakeStore{listPrice: nil}
	st.listPrice = &ListPrice{ProductID: "x", ListPriceCents: 10000, PriceUOM: "SF"}
	s := previewSvc(st, CandidateProduct{ProductID: "blu60", CategoryID: "pavers", SKU: "A", Name: "A", BaseUOM: "SF", CostCents: 7000},
		CandidateProduct{ProductID: "nocost", CategoryID: "pavers", SKU: "B", Name: "B", BaseUOM: "SF"})
	out, err := s.PreviewFloor(context.Background(), FloorChange{Kind: "rule", Rule: tierRule(1000)})
	if err != nil {
		t.Fatalf("PreviewFloor: %v", err)
	}
	if out.NoCost != 1 || out.NotGraded != 0 || out.Evaluated != 1 {
		t.Fatalf("no cost on file is counted apart from cannot price: %+v", out)
	}
}

func TestPreviewFloor_AProductThatCannotBePricedIsCountedApartFromNoCost(t *testing.T) {
	// no list price row and no conversion for the entered unit: the resolver says so, the preview counts it
	st := &fakeStore{listPrice: &ListPrice{ProductID: "blu60", ListPriceCents: 10000, PriceUOM: "SF"}}
	s := newSvc(st, "pavers").WithCandidateSource(fakeCands{[]CandidateProduct{{ProductID: "blu60", CategoryID: "pavers", SKU: "A", Name: "A", BaseUOM: "SF", CostCents: 7000}}})
	h := &QuantityBreakHeader{Code: "B", Name: "N", ProductID: "blu60", ThresholdUOM: "BOX",
		Items: []QuantityBreakItem{{FromQty: 1, ValueType: BreakPctOff, DiscountBps: p64(5000), PriceUOM: "SF"}}}
	out, err := s.PreviewFloor(context.Background(), FloorChange{Kind: "quantity_break", Break: h})
	if err != nil {
		t.Fatalf("a missing conversion is a per product case, not an error: %v", err)
	}
	if out.NotGraded != 1 || out.NoCost != 0 || out.Evaluated != 0 {
		t.Fatalf("cannot price is its own count: %+v", out)
	}
}

func TestValidateQuantityBreak_TheTierCountIsCapped(t *testing.T) {
	h := QuantityBreakHeader{Code: "B", Name: "N", ProductID: "blu60", ThresholdUOM: "PLT"}
	for i := 1; i <= MaxBreakTiers+1; i++ {
		h.Items = append(h.Items, QuantityBreakItem{FromQty: float64(i), ValueType: BreakPctOff, DiscountBps: p64(100), PriceUOM: "SF"})
	}
	err := validateQuantityBreak(h)
	if err == nil || !isBadRequest(err) || !strings.Contains(err.Error(), fmt.Sprint(MaxBreakTiers)) {
		t.Fatalf("more than %d tiers must be refused naming the cap, got %v", MaxBreakTiers, err)
	}
	h.Items = h.Items[:MaxBreakTiers]
	if err := validateQuantityBreak(h); err != nil {
		t.Fatalf("exactly %d tiers is allowed: %v", MaxBreakTiers, err)
	}
}

func TestPreviewFloor_TheTotalWorkIsCappedAndSaid(t *testing.T) {
	var prods []CandidateProduct
	for i := 0; i < PreviewProductCap; i++ {
		prods = append(prods, CandidateProduct{ProductID: fmt.Sprintf("p%03d", i), CategoryID: "pavers", SKU: fmt.Sprintf("S%03d", i), Name: "N", BaseUOM: "SF", CostCents: 7000})
	}
	st := &fakeStore{listPrice: &ListPrice{ProductID: "x", ListPriceCents: 10000, PriceUOM: "SF"}}
	s := previewSvc(st, prods...)
	h := &QuantityBreakHeader{Code: "B", Name: "N", CategoryID: "pavers", ThresholdUOM: "PLT"}
	for i := 1; i <= MaxBreakTiers; i++ {
		h.Items = append(h.Items, QuantityBreakItem{FromQty: float64(i), ValueType: BreakPctOff, DiscountBps: p64(100), PriceUOM: "SF"})
	}
	out, err := s.PreviewFloor(context.Background(), FloorChange{Kind: "quantity_break", Break: h})
	if err != nil {
		t.Fatalf("PreviewFloor: %v", err)
	}
	if !out.Truncated || out.Evaluated == 0 || out.Evaluated >= PreviewProductCap {
		t.Fatalf("the work cap must stop grading early and say so: %+v", out)
	}
	if got := out.Evaluated * len(previewCustomerTypes) * MaxBreakTiers; got > MaxPreviewGrades {
		t.Fatalf("graded %d combinations over the %d cap", got, MaxPreviewGrades)
	}
}

func TestPreviewFloor_AnOverrideNeedsACustomerACategoryAndAUnit(t *testing.T) {
	s := previewSvc(&fakeStore{}).WithFloorCustomerType(fakeCustTypes{"cust1": "RETAIL"})
	base := Override{CustomerID: "cust1", CategoryID: "pavers", CalcType: CalcPctOffList, DiscountBps: p64(500), PriceUOM: "SF"}
	for name, mut := range map[string]func(*Override){
		"no customer": func(o *Override) { o.CustomerID = "" },
		"no category": func(o *Override) { o.CategoryID = "" },
		"no unit":     func(o *Override) { o.PriceUOM = "" },
	} {
		o := base
		mut(&o)
		_, err := s.PreviewFloor(context.Background(), FloorChange{Kind: "override", Override: &o})
		if err == nil || !isBadRequest(err) {
			t.Errorf("%s: want a 400 as the write gives, got %v", name, err)
		}
	}
}

func TestRecordFloorConfirmation_TheRowSaysWhatTheEditorDidNotWhatTheServerKnows(t *testing.T) {
	st := &fakeStore{byID: map[string]Rule{"r1": {ID: "r1"}}}
	s := previewSvc(st)
	if err := s.RecordFloorConfirmation(context.Background(), FloorConfirmation{
		EntityType: "pricing_rule", EntityID: "r1", ProductIDs: []string{"a", "b", "c"}}, "u"); err != nil {
		t.Fatalf("RecordFloorConfirmation: %v", err)
	}
	if v := st.audits[0].NewValue; !strings.HasPrefix(v, "the editor confirmed a floor warning naming 3 products") {
		t.Errorf("the row must say the editor confirmed a warning naming N products, got %q", v)
	}
}

func TestRecordFloorConfirmation_AReferenceOutsideTheEnumIsRefused(t *testing.T) {
	st := &fakeStore{byID: map[string]Rule{"r1": {ID: "r1"}}}
	s := previewSvc(st)
	for name, c := range map[string]FloorConfirmation{
		"type outside the enum": {EntityType: "pricing_rule", EntityID: "r1", ProductIDs: []string{"a"}, RefType: "warehouse", RefID: "x"},
		"type with no id":       {EntityType: "pricing_rule", EntityID: "r1", ProductIDs: []string{"a"}, RefType: "product"},
		"id with no type":       {EntityType: "pricing_rule", EntityID: "r1", ProductIDs: []string{"a"}, RefID: "x"},
	} {
		if err := s.RecordFloorConfirmation(context.Background(), c, "u"); err == nil || !isBadRequest(err) {
			t.Errorf("%s: want a 400, got %v", name, err)
		}
	}
	if len(st.audits) != 0 {
		t.Errorf("a refusal writes nothing, got %+v", st.audits)
	}
}
