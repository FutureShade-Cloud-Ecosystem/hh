package pricing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
)

// - DB-free fakes (the internal/product test pattern) -

type fakeStore struct {
	rules      []Rule
	breaks     []QuantityBreak
	override   *Override
	listPrice  *ListPrice
	catPolicy  *CategoryPolicy
	prodPolicy *ProductPolicy
	audits     []AuditEntry
	byID       map[string]Rule
	// admin-editor (A-07 Tier-B) fakes
	catPolicies   map[string]CategoryPolicy
	overridesByID map[string]Override
	breakHeaders  map[string]QuantityBreakHeader
	auditLog      []AuditLogRow
	nextID        int
}

func (f *fakeStore) MatchingRules(_ context.Context, _ ResolveInput) ([]Rule, error) {
	return f.rules, nil
}
func (f *fakeStore) QuantityBreaksFor(_ context.Context, _, _ string) ([]QuantityBreak, error) {
	return f.breaks, nil
}
func (f *fakeStore) OverrideFor(_ context.Context, customerID, categoryID string) (*Override, error) {
	if f.override != nil && f.override.CustomerID == customerID && f.override.CategoryID == categoryID {
		return f.override, nil
	}
	return nil, nil
}
func (f *fakeStore) ListPrice(_ context.Context, _ string) (*ListPrice, error) {
	return f.listPrice, nil
}
func (f *fakeStore) CategoryPolicy(_ context.Context, categoryID string) (*CategoryPolicy, error) {
	// Read-after-write for the upsert tests; fall back to the single resolver-test policy.
	if f.catPolicies != nil {
		if p, ok := f.catPolicies[categoryID]; ok {
			return &p, nil
		}
	}
	return f.catPolicy, nil
}
func (f *fakeStore) ProductPolicy(_ context.Context, _ string) (*ProductPolicy, error) {
	return f.prodPolicy, nil
}
func (f *fakeStore) AppendAudit(_ context.Context, e AuditEntry) error {
	f.audits = append(f.audits, e)
	f.nextID++
	f.auditLog = append(f.auditLog, AuditLogRow{
		ID: fmt.Sprintf("audit-%d", f.nextID), EntityType: e.EntityType, EntityID: e.EntityID,
		Action: e.Action, FieldName: e.FieldName, OldValue: e.OldValue, NewValue: e.NewValue,
		UserID: e.UserID, CreatedAt: time.Now(),
	})
	return nil
}

// - admin-editor store methods -

func (f *fakeStore) ListCategoryPolicies(_ context.Context) ([]CategoryPolicy, error) {
	var out []CategoryPolicy
	for _, p := range f.catPolicies {
		out = append(out, p)
	}
	return out, nil
}
func (f *fakeStore) UpsertCategoryPolicy(_ context.Context, p CategoryPolicy) (CategoryPolicy, error) {
	if f.catPolicies == nil {
		f.catPolicies = map[string]CategoryPolicy{}
	}
	f.catPolicies[p.CategoryID] = p
	return p, nil
}
func (f *fakeStore) ListOverrides(_ context.Context, customerID string) ([]Override, error) {
	var out []Override
	for _, o := range f.overridesByID {
		if o.CustomerID == customerID {
			out = append(out, o)
		}
	}
	return out, nil
}
func (f *fakeStore) GetOverride(_ context.Context, id string) (Override, error) {
	if o, ok := f.overridesByID[id]; ok {
		return o, nil
	}
	return Override{}, errNotFound
}
func (f *fakeStore) CreateOverride(_ context.Context, o Override) (Override, error) {
	if f.overridesByID == nil {
		f.overridesByID = map[string]Override{}
	}
	// Emulate the (customer_id, category_id) UNIQUE constraint (0023:158).
	for _, e := range f.overridesByID {
		if e.CustomerID == o.CustomerID && e.CategoryID == o.CategoryID {
			return Override{}, errOverrideTaken
		}
	}
	f.nextID++
	o.ID = fmt.Sprintf("ovr-%d", f.nextID)
	f.overridesByID[o.ID] = o
	return o, nil
}

// SaveOverride emulates the repo's ATOMIC version guard: it writes only when the stored row's
// version still equals expectedVersion (errConflict on mismatch, errNotFound when absent).
func (f *fakeStore) SaveOverride(_ context.Context, o Override, expectedVersion int64) (Override, error) {
	existing, ok := f.overridesByID[o.ID]
	if !ok {
		return Override{}, errNotFound
	}
	if existing.Version != expectedVersion {
		return Override{}, errConflict
	}
	f.overridesByID[o.ID] = o
	return o, nil
}

// DeleteOverride emulates the repo's ATOMIC version guard (finding #4).
func (f *fakeStore) DeleteOverride(_ context.Context, id string, expectedVersion int64) error {
	existing, ok := f.overridesByID[id]
	if !ok {
		return errNotFound
	}
	if existing.Version != expectedVersion {
		return errConflict
	}
	delete(f.overridesByID, id)
	return nil
}
func (f *fakeStore) ListAudit(_ context.Context, flt AuditFilter) ([]AuditLogRow, error) {
	var out []AuditLogRow
	for i := len(f.auditLog) - 1; i >= 0; i-- { // newest first
		a := f.auditLog[i]
		if flt.EntityType != "" && a.EntityType != flt.EntityType {
			continue
		}
		if flt.EntityID != "" && a.EntityID != flt.EntityID {
			continue
		}
		if flt.Action != "" && a.Action != flt.Action {
			continue
		}
		out = append(out, a)
		if flt.Limit > 0 && len(out) >= flt.Limit {
			break
		}
	}
	return out, nil
}

// - quantity-break admin store methods (A-07 Tier-B) -
func (f *fakeStore) ListQuantityBreakHeaders(_ context.Context) ([]QuantityBreakHeader, error) {
	var out []QuantityBreakHeader
	for _, h := range f.breakHeaders {
		out = append(out, h)
	}
	return out, nil
}
func (f *fakeStore) GetQuantityBreakHeader(_ context.Context, id string) (QuantityBreakHeader, error) {
	if h, ok := f.breakHeaders[id]; ok {
		return h, nil
	}
	return QuantityBreakHeader{}, errNotFound
}
func (f *fakeStore) CreateQuantityBreakHeader(_ context.Context, h QuantityBreakHeader) (QuantityBreakHeader, error) {
	if f.breakHeaders == nil {
		f.breakHeaders = map[string]QuantityBreakHeader{}
	}
	for _, e := range f.breakHeaders { // emulate the code UNIQUE constraint
		if e.Code == h.Code {
			return QuantityBreakHeader{}, errBreakCodeTaken
		}
	}
	f.nextID++
	h.ID = fmt.Sprintf("qbh-%d", f.nextID)
	f.breakHeaders[h.ID] = h
	return h, nil
}
func (f *fakeStore) SaveQuantityBreakHeader(_ context.Context, h QuantityBreakHeader, expectedVersion int64) (QuantityBreakHeader, error) {
	existing, ok := f.breakHeaders[h.ID]
	if !ok {
		return QuantityBreakHeader{}, errNotFound
	}
	if existing.Version != expectedVersion {
		return QuantityBreakHeader{}, errConflict
	}
	f.breakHeaders[h.ID] = h
	return h, nil
}
func (f *fakeStore) DeleteQuantityBreakHeader(_ context.Context, id string, expectedVersion int64) error {
	existing, ok := f.breakHeaders[id]
	if !ok {
		return errNotFound
	}
	if existing.Version != expectedVersion {
		return errConflict
	}
	existing.IsActive = false
	existing.Version++
	f.breakHeaders[id] = existing
	return nil
}

func (f *fakeStore) CreateRule(_ context.Context, r Rule) (Rule, error) {
	if f.byID == nil {
		f.byID = map[string]Rule{}
	}
	if r.ID == "" {
		r.ID = "gen-" + r.Scope
	}
	f.byID[r.ID] = r
	return r, nil
}
func (f *fakeStore) GetRule(_ context.Context, id string) (Rule, error) {
	if r, ok := f.byID[id]; ok {
		return r, nil
	}
	return Rule{}, errNotFound
}

// SaveRule emulates the repo's ATOMIC optimistic-lock guard (finding #3): the write lands only
// when the stored row's version still equals expectedVersion, a second concurrent save that
// read the same stale version gets errConflict, and a missing row gets errNotFound.
func (f *fakeStore) SaveRule(_ context.Context, r Rule, expectedVersion int64) (Rule, error) {
	existing, ok := f.byID[r.ID]
	if !ok {
		return Rule{}, errNotFound
	}
	if existing.Version != expectedVersion {
		return Rule{}, errConflict
	}
	f.byID[r.ID] = r
	return r, nil
}
func (f *fakeStore) ListRules(_ context.Context, _ RuleFilter) ([]Rule, error) {
	var out []Rule
	for _, r := range f.byID {
		out = append(out, r)
	}
	return out, nil
}

type fakeCat struct {
	cat     string
	baseUOM string
}

func (f fakeCat) GetCategory(_ context.Context, _ string) (string, error) { return f.cat, nil }

// GetBaseUOM returns the configured base UOM (defaulting to a factor-1 UOM present in
// stdFactors, so a derived anchor denominates without changing the unit-test math).
func (f fakeCat) GetBaseUOM(_ context.Context, _ string) (string, error) {
	if f.baseUOM != "" {
		return f.baseUOM, nil
	}
	return "EA", nil
}

// errCat is a CategoryResolver that always reports the product's not-found sentinel.
type errCat struct{ err error }

func (c errCat) GetCategory(_ context.Context, _ string) (string, error) { return "", c.err }
func (c errCat) GetBaseUOM(_ context.Context, _ string) (string, error)  { return "", c.err }

type fakeUOM struct{ factors map[string]float64 }

func (f fakeUOM) ConvertUOM(_ context.Context, _, uom string) (float64, error) {
	if v, ok := f.factors[uom]; ok {
		return v, nil
	}
	return 0, errNoConversion
}

func p64(v int64) *int64      { return &v }
func f64p(v float64) *float64 { return &v }

// stdFactors covers the UOMs used across the suite.
func stdFactors() map[string]float64 {
	return map[string]float64{"EA": 1, "SF": 1, "PLT": 120, "T": 1, "KG": 0.001}
}

func newSvc(st *fakeStore, cat string) *Service {
	s := NewService(st).WithUOMConverter(fakeUOM{stdFactors()}).WithMarginGate(NewIdentityMarginGate())
	if cat != "" {
		s = s.WithCategoryResolver(fakeCat{cat: cat})
	}
	return s
}

func resolve(t *testing.T, s *Service, in ResolveInput) Breakdown {
	t.Helper()
	bd, err := s.ResolvePrice(context.Background(), in)
	if err != nil {
		t.Fatalf("ResolvePrice: %v", err)
	}
	return bd
}

func base(in ResolveInput) ResolveInput {
	if in.QtyEntered == 0 {
		in.QtyEntered = 1
	}
	if in.UOMEntered == "" {
		in.UOMEntered = "SF"
	}
	if in.CustomerType == "" {
		in.CustomerType = "CONTRACTOR"
	}
	in.ProductID = "blu60"
	in.CustomerID = "abc"
	return in
}

// - U1: LIST / retail no rule -
func TestResolve_List_RetailNoRule(t *testing.T) {
	st := &fakeStore{listPrice: &ListPrice{ProductID: "blu60", ListPriceCents: 48700, PriceUOM: "SF"}}
	bd := resolve(t, newSvc(st, "pavers"), base(ResolveInput{CustomerType: "RETAIL", QtyEntered: 10, UOMEntered: "SF"}))
	if bd.Source != SourceList || bd.FinalCents != 48700 || bd.FinalCents != bd.OriginalCents {
		t.Fatalf("walk-in retail should be LIST 48700==original, got %+v", bd)
	}
}

// - U2: TIER_DEFAULT (the ~99% path) -
func TestResolve_TierDefault(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 10000, "EA"},
		rules:     []Rule{{ID: "r-def", Scope: ScopeTierDefault, CustomerType: "CONTRACTOR", CalcType: CalcPctOffList, DiscountBps: p64(1500), PriceUOM: "EA", Priority: 10, IsActive: true, DetailText: "Contractor, default: 15% off"}},
	}
	bd := resolve(t, newSvc(st, ""), base(ResolveInput{QtyEntered: 5, UOMEntered: "EA"}))
	if bd.Source != SourceTierDefault || bd.FinalCents != 8500 || bd.LineTotalCents != 42500 {
		t.Fatalf("tier default 15%% off 10000 → 8500 ×5 = 42500, got %+v", bd)
	}
}

// - U3: TIER_CATEGORY beats TIER_DEFAULT -
func TestResolve_TierCategory_BeatsDefault(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 48700, "SF"},
		rules: []Rule{
			{ID: "r-def", Scope: ScopeTierDefault, CustomerType: "CONTRACTOR", CalcType: CalcPctOffList, DiscountBps: p64(1500), PriceUOM: "EA", Priority: 10, IsActive: true},
			{ID: "r-cat", Scope: ScopeTierCategory, CustomerType: "CONTRACTOR", CategoryID: "pavers", CalcType: CalcPctOffList, DiscountBps: p64(1800), PriceUOM: "SF", Priority: 40, IsActive: true, DetailText: "Contractor, Pavers: 18% off"},
		},
	}
	bd := resolve(t, newSvc(st, "pavers"), base(ResolveInput{CategoryID: "pavers", QtyEntered: 1, UOMEntered: "SF"}))
	if bd.Source != SourceTierCategory || bd.FinalCents != 39934 {
		t.Fatalf("tier-category 18%% off 48700 → 39934, got %+v", bd)
	}
}

// - U4: CUSTOMER_OVERRIDE beats tier (22% off → 37986) -
func TestResolve_CustomerOverride_BeatsTier(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 48700, "SF"},
		rules:     []Rule{{ID: "r-cat", Scope: ScopeTierCategory, CustomerType: "CONTRACTOR", CategoryID: "pavers", CalcType: CalcPctOffList, DiscountBps: p64(1800), PriceUOM: "SF", Priority: 40, IsActive: true}},
		override:  &Override{ID: "o1", CustomerID: "abc", CategoryID: "pavers", CalcType: CalcPctOffList, DiscountBps: p64(2200), PriceUOM: "SF", Notes: "ABC Landscaping, Pavers override: 22% off"},
	}
	bd := resolve(t, newSvc(st, "pavers"), base(ResolveInput{CategoryID: "pavers", UOMEntered: "SF"}))
	if bd.Source != SourceCustomerOverride || bd.FinalCents != 37986 {
		t.Fatalf("override 22%% off 48700 → 37986, got %+v", bd)
	}
}

// - U5: quantity break bucket select STACKS on the tier base (120 SF → 50-199 bucket) -
// The 50-199 bucket (−20%) is selected and STACKED on the TIER_DEFAULT base (−15%), not used as a
// competing replacement level: 48700 × 0.85 × 0.80 = 33116. Source stays TIER_DEFAULT.
func TestResolve_QuantityBreak_BucketSelect(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 48700, "SF"},
		rules:     []Rule{{ID: "r-def", Scope: ScopeTierDefault, CustomerType: "CONTRACTOR", CalcType: CalcPctOffList, DiscountBps: p64(1500), PriceUOM: "SF", Priority: 10, IsActive: true, DetailText: "Contractor, default: 15% off"}},
		breaks: []QuantityBreak{
			{HeaderID: "h1", ProductID: "blu60", ThresholdUOM: "SF", FromQty: 50, ToQty: f64p(200), ValueType: BreakPctOff, DiscountBps: p64(2000), PriceUOM: "SF", Name: "Volume 50-199 SF: 20% off"},
			{HeaderID: "h1", ProductID: "blu60", ThresholdUOM: "SF", FromQty: 200, ValueType: BreakPctOff, DiscountBps: p64(2500), PriceUOM: "SF"},
		},
	}
	bd := resolve(t, newSvc(st, "pavers"), base(ResolveInput{QtyEntered: 120, UOMEntered: "SF"}))
	if bd.Source != SourceTierDefault || bd.FinalCents != 33116 {
		t.Fatalf("50-199 break −20%% STACKED on tier −15%%: 48700×0.85×0.80 → 33116 (source stays TIER_DEFAULT), got %+v", bd)
	}
	if !strings.Contains(bd.Detail, "Volume 50-199 SF: 20% off") {
		t.Fatalf("breakdown detail must note the stacked break, got %q", bd.Detail)
	}
}

// - U6: CUSTOMER_PRODUCT rule beats a quantity break -
func TestResolve_CustomerProductRule_BeatsBreak(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 48700, "SF"},
		rules:     []Rule{{ID: "r-cp", Scope: ScopeCustomerProduct, CustomerID: "abc", ProductID: "blu60", CalcType: CalcAbsolute, PriceCents: p64(41000), PriceUOM: "SF", Priority: 100, IsActive: true}},
		breaks:    []QuantityBreak{{HeaderID: "h1", ProductID: "blu60", ThresholdUOM: "SF", FromQty: 50, ToQty: f64p(200), ValueType: BreakPctOff, DiscountBps: p64(2000), PriceUOM: "SF"}},
	}
	bd := resolve(t, newSvc(st, "pavers"), base(ResolveInput{QtyEntered: 120, UOMEntered: "SF"}))
	if bd.Source != SourceCustomerProduct || bd.FinalCents != 41000 {
		t.Fatalf("customer+product ABSOLUTE 41000 should win over the break, got %+v", bd)
	}
}

// - U7: full waterfall, highest precedence wins, deterministically -
func TestResolve_Precedence_FullWaterfall(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 48700, "SF"},
		rules: []Rule{
			{ID: "r-def", Scope: ScopeTierDefault, CustomerType: "CONTRACTOR", CalcType: CalcPctOffList, DiscountBps: p64(1500), PriceUOM: "SF", Priority: 10, IsActive: true},
			{ID: "r-cat", Scope: ScopeTierCategory, CustomerType: "CONTRACTOR", CategoryID: "pavers", CalcType: CalcPctOffList, DiscountBps: p64(1800), PriceUOM: "SF", Priority: 40, IsActive: true},
			{ID: "r-cp", Scope: ScopeCustomerProduct, CustomerID: "abc", ProductID: "blu60", CalcType: CalcAbsolute, PriceCents: p64(41000), PriceUOM: "SF", Priority: 100, IsActive: true},
		},
		override: &Override{ID: "o1", CustomerID: "abc", CategoryID: "pavers", CalcType: CalcPctOffList, DiscountBps: p64(2200), PriceUOM: "SF"},
		breaks:   []QuantityBreak{{HeaderID: "h1", ProductID: "blu60", ThresholdUOM: "SF", FromQty: 50, ToQty: f64p(200), ValueType: BreakPctOff, DiscountBps: p64(2000), PriceUOM: "SF"}},
	}
	bd := resolve(t, newSvc(st, "pavers"), base(ResolveInput{CategoryID: "pavers", QtyEntered: 120, UOMEntered: "SF"}))
	if bd.Source != SourceCustomerProduct || bd.FinalCents != 41000 {
		t.Fatalf("full waterfall should select CUSTOMER_PRODUCT 41000, got %+v", bd)
	}
}

// - U-DET: total-order tie-break (same priority + valid_from → id ASC) -
func TestResolve_TotalOrderTieBreak(t *testing.T) {
	vf := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mk := func(order []Rule) Breakdown {
		st := &fakeStore{listPrice: &ListPrice{"blu60", 48700, "SF"}, rules: order}
		return resolve(t, newSvc(st, "pavers"), base(ResolveInput{CategoryID: "pavers", UOMEntered: "SF"}))
	}
	ra := Rule{ID: "aaa", Scope: ScopeTierCategory, CustomerType: "CONTRACTOR", CategoryID: "pavers", CalcType: CalcPctOffList, DiscountBps: p64(1800), PriceUOM: "SF", Priority: 40, ValidFrom: &vf, IsActive: true, DetailText: "A"}
	rb := Rule{ID: "bbb", Scope: ScopeTierCategory, CustomerType: "CONTRACTOR", CategoryID: "pavers", CalcType: CalcPctOffList, DiscountBps: p64(1000), PriceUOM: "SF", Priority: 40, ValidFrom: &vf, IsActive: true, DetailText: "B"}
	// Same priority AND valid_from → id ASC ("aaa") must win, regardless of slice order.
	if got1, got2 := mk([]Rule{ra, rb}), mk([]Rule{rb, ra}); got1.Detail != "A" || got2.Detail != "A" || got1.FinalCents != got2.FinalCents {
		t.Fatalf("tie-break must be deterministic (id ASC → 'aaa'), got %q / %q", got1.Detail, got2.Detail)
	}
}

// - U8: PLT entered, priced per SF (240 SF base, unit 487, line 116880) -
func TestResolve_UOM_PalletEnteredPricedPerSF(t *testing.T) {
	st := &fakeStore{listPrice: &ListPrice{"blu60", 487, "SF"}}
	bd := resolve(t, newSvc(st, "pavers"), base(ResolveInput{CustomerType: "RETAIL", QtyEntered: 2, UOMEntered: "PLT"}))
	if bd.PriceUOM != "SF" || bd.FinalCents != 487 || bd.LineTotalCents != 116880 || bd.QtyBase != 240 {
		t.Fatalf("2 PLT → 240 SF base, unit 487/SF, line 116880, got %+v", bd)
	}
}

// - U9: rule UOM (T) differs from line entry (KG), convert before discount -
func TestResolve_UOM_RuleUomDiffersFromLine(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"agg", 10000, "T"},
		rules:     []Rule{{ID: "r-def", Scope: ScopeTierDefault, CustomerType: "CONTRACTOR", CalcType: CalcPctOffList, DiscountBps: p64(1000), PriceUOM: "T", Priority: 10, IsActive: true}},
	}
	bd := resolve(t, newSvc(st, "agg"), base(ResolveInput{QtyEntered: 500, UOMEntered: "KG"}))
	if bd.PriceUOM != "T" || bd.FinalCents != 9000 || bd.LineTotalCents != 4500 {
		t.Fatalf("rule in T, line 500 KG → 0.5 T, unit 9000/T, line 4500, got %+v", bd)
	}
}

// - U11 (B1 regression): SF-priced / PC-based product rung in its SELL uom -
// The Brussels homework bug: products.sold_by was empty everywhere, so the terminal rang the
// product BASE uom (PC) instead of its PRICE/SELL uom (SF), $9.75/SF at qty 20 read ~$47.56
// instead of $195.00. Migration 0063 backfills sold_by = price_uom so the ring selects SF. This
// asserts the ENGINE math that selection routes to: 20 SF @ 975/SF (4.10 pc/SF) → price_uom SF,
// unit 975, line 19500 ($195.00 pre-break), qty_base 82 PC.
func TestResolve_UOM_SFPricedPCBase_SellUOM_B1(t *testing.T) {
	st := &fakeStore{listPrice: &ListPrice{"brussels", 975, "SF"}}
	svc := NewService(st).
		WithUOMConverter(fakeUOM{map[string]float64{"SF": 4.10, "PC": 1}}).
		WithMarginGate(NewIdentityMarginGate())
	bd := resolve(t, svc, base(ResolveInput{CustomerType: "RETAIL", QtyEntered: 20, UOMEntered: "SF"}))
	if bd.PriceUOM != "SF" || bd.FinalCents != 975 || bd.LineTotalCents != 19500 {
		t.Fatalf("20 SF @ 975/SF → SF price_uom, unit 975, line 19500 ($195.00); got %+v", bd)
	}
	if bd.QtyBase < 81.999 || bd.QtyBase > 82.001 { // 20 SF × 4.10 pc/SF = 82 PC base
		t.Fatalf("qty_base should be 82 PC (20 SF × 4.10 pc/SF), got %v", bd.QtyBase)
	}
}

// - U10: missing conversion factor → errNoConversion (no silent wrong price) -
func TestResolve_UOM_NoConversionFactor_Errors(t *testing.T) {
	st := &fakeStore{listPrice: &ListPrice{"blu60", 48700, "SF"}}
	_, err := newSvc(st, "pavers").ResolvePrice(context.Background(), base(ResolveInput{QtyEntered: 3, UOMEntered: "ZZ"}))
	if !errors.Is(err, errNoConversion) {
		t.Fatalf("missing factor must return errNoConversion, got %v", err)
	}
}

// - U11: below the CONFIGURED floor blocks via CheckMarginAt -
func TestResolve_MarginFloor_Blocks(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 10000, "EA"},
		catPolicy: &CategoryPolicy{CategoryID: "pavers", MarginTargetBps: 3000, MarginFloorBps: 2000},
	}
	// final 10000, cost 8500 → margin 1500 bps: passes the legacy 1200 floor but is
	// BELOW the category's configured 2000 floor → blocked. Proves parameterization.
	bd := resolve(t, newSvc(st, "pavers"), base(ResolveInput{CategoryID: "pavers", UOMEntered: "EA", CostCents: 8500}))
	if !bd.Blocked || bd.BlockPolicy != BlockMarginBelowMinimum || bd.FloorBps != 2000 {
		t.Fatalf("below the configured 2000 floor must block, got %+v", bd)
	}
}

// - U12: an override REQUEST backed by a server-side authority passes -
//
// FLIPPED BY DIB-420. This test used to read "an approved override lets the below-floor price
// through" and it proved something weaker than its name: the input field was the CLIENT'S claim, so
// what it actually pinned was that a request body could clear the margin floor. The behaviour it
// names is still correct and still wanted, it just has to be earned. The full two-arm matrix
// (flag alone refused / flag + authority honoured) lives in dib420_override_authority_test.go.
func TestResolve_MarginFloor_ApprovedOverridePasses(t *testing.T) {
	st := &fakeStore{listPrice: &ListPrice{"blu60", 10000, "EA"}, catPolicy: &CategoryPolicy{CategoryID: "pavers", MarginTargetBps: 3000, MarginFloorBps: 2000}}
	svc := newSvc(st, "pavers").WithOverrideAuthority(&grantingAuthority{source: AuthorityApproval})
	bd := resolve(t, svc, base(ResolveInput{CategoryID: "pavers", UOMEntered: "EA", CostCents: 8500, OverrideRequested: true, UserID: "ryan"}))
	if bd.Blocked {
		t.Fatalf("an override backed by a server-side authority should pass the below-floor price, got %+v", bd)
	}
	if bd.OverrideAuthority != AuthorityApproval {
		t.Fatalf("the honoured override must name its authority, got %q", bd.OverrideAuthority)
	}
}

// - U13: below TARGET but above FLOOR → advisory, not blocked -
func TestResolve_CategoryTarget_AdvisoryNotEnforced(t *testing.T) {
	st := &fakeStore{listPrice: &ListPrice{"blu60", 10000, "EA"}, catPolicy: &CategoryPolicy{CategoryID: "pavers", MarginTargetBps: 3000, MarginFloorBps: 2000}}
	// final 10000, cost 7500 → margin 2500: below the 3000 target, above the 2000 floor.
	bd := resolve(t, newSvc(st, "pavers"), base(ResolveInput{CategoryID: "pavers", UOMEntered: "EA", CostCents: 7500}))
	if bd.Blocked {
		t.Fatalf("25%% margin (below target, above floor) must NOT block, got %+v", bd)
	}
}

// - U14: PCT_OFF_LIST integer-cents, deterministic round-half-up -
func TestCalc_PctOffList_IntegerCents_RoundHalfUp(t *testing.T) {
	if got := mulDivRoundHalfUp(5, 1, 2); got != 3 { // 2.5 → 3 (half up)
		t.Fatalf("round-half-up(2.5)=%d, want 3", got)
	}
	if got := mulDivRoundHalfUp(7, 1, 2); got != 4 { // 3.5 → 4
		t.Fatalf("round-half-up(3.5)=%d, want 4", got)
	}
	if got := applyPctOff(48700, 2200); got != 37986 {
		t.Fatalf("applyPctOff(48700,2200)=%d, want 37986", got)
	}
}

// - U15: MARKUP_ON_COST builds the retail anchor (cost × 1.85) -
func TestCalc_MarkupOnCost(t *testing.T) {
	if got := applyMarkup(45000, 8500); got != 83250 {
		t.Fatalf("applyMarkup(45000,8500)=%d, want 83250 (cost × 1.85)", got)
	}
	st := &fakeStore{rules: []Rule{{ID: "r-def", Scope: ScopeTierDefault, CustomerType: "CONTRACTOR", CalcType: CalcMarkupOnCost, MarkupBps: p64(8500), PriceUOM: "EA", Priority: 10, IsActive: true}}}
	bd := resolve(t, newSvc(st, "pavers"), base(ResolveInput{UOMEntered: "EA", CostCents: 45000}))
	if bd.FinalCents != 83250 {
		t.Fatalf("MARKUP_ON_COST anchor 45000 × 1.85 → 83250, got %+v", bd)
	}
}

// - U16: optimistic-lock conflict on a stale version -
func TestRule_OptimisticLock_Conflict(t *testing.T) {
	st := &fakeStore{byID: map[string]Rule{"r1": {ID: "r1", Scope: ScopeTierCategory, CalcType: CalcPctOffList, DiscountBps: p64(1800), PriceUOM: "SF", Version: 2}}}
	svc := NewService(st)
	if _, err := svc.UpdateRule(context.Background(), "r1", RulePatch{DiscountBps: p64(2000)}, 1, "ryan"); !errors.Is(err, errConflict) {
		t.Fatalf("stale version must 409/conflict, got %v", err)
	}
	if st.byID["r1"].Version != 2 || *st.byID["r1"].DiscountBps != 1800 {
		t.Fatalf("rule must be unchanged after a conflict, got %+v", st.byID["r1"])
	}
}

// - U17: an update writes an audit row with old/new -
func TestRule_AuditWrittenOnUpdate(t *testing.T) {
	st := &fakeStore{byID: map[string]Rule{"r1": {ID: "r1", Scope: ScopeTierCategory, CalcType: CalcPctOffList, DiscountBps: p64(1800), PriceUOM: "SF", Version: 1}}}
	svc := NewService(st)
	out, err := svc.UpdateRule(context.Background(), "r1", RulePatch{DiscountBps: p64(2000)}, 1, "ryan")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if out.Version != 2 {
		t.Fatalf("version should increment to 2, got %d", out.Version)
	}
	var found bool
	for _, a := range st.audits {
		if a.FieldName == "discount_bps" && a.OldValue == "1800" && a.NewValue == "2000" && a.Action == "UPDATE" && a.UserID == "ryan" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an UPDATE audit row discount_bps 1800→2000, got %+v", st.audits)
	}
}

// - U18: a rule outside its validity window is skipped -
func TestResolve_ValidityWindow_ExpiredRuleSkipped(t *testing.T) {
	past := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 48700, "SF"},
		rules:     []Rule{{ID: "r-def", Scope: ScopeTierDefault, CustomerType: "CONTRACTOR", CalcType: CalcPctOffList, DiscountBps: p64(1500), PriceUOM: "SF", Priority: 10, IsActive: true, ValidTo: &past}},
	}
	bd := resolve(t, newSvc(st, "pavers"), base(ResolveInput{UOMEntered: "SF"}))
	if bd.Source != SourceList {
		t.Fatalf("an expired rule must be skipped → LIST, got %+v", bd)
	}
}

// - U21: effective floor = category policy floor (the adjuster) -
func TestResolve_EffectiveFloor_CategoryAdjuster(t *testing.T) {
	mk := func(floor int64) Breakdown {
		st := &fakeStore{listPrice: &ListPrice{"blu60", 10000, "EA"}, catPolicy: &CategoryPolicy{CategoryID: "c", MarginTargetBps: 3000, MarginFloorBps: floor}}
		return resolve(t, newSvc(st, "c"), base(ResolveInput{CategoryID: "c", UOMEntered: "EA", CostCents: 8500}))
	}
	// margin = 1500 bps. Floor 2000 → blocked; floor 1000 → passes. The gate consumes
	// the category's stored floor (passed to CheckMarginAt), not a single global const.
	if hi := mk(2000); !hi.Blocked || hi.FloorBps != 2000 {
		t.Fatalf("floor 2000 must block a 1500-bps margin, got %+v", hi)
	}
	if lo := mk(1000); lo.Blocked {
		t.Fatalf("floor 1000 must pass a 1500-bps margin, got %+v", lo)
	}
}

// - U22: enforcement-disabled bypasses the gate AND writes a BYPASS audit row -
func TestResolve_EnforcementDisabled_BypassWritesAudit(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 10000, "EA"},
		catPolicy: &CategoryPolicy{CategoryID: "aggregates", MarginTargetBps: 1500, MarginFloorBps: 1000, EnforcementDisabled: true},
	}
	// final 10000, cost 9800 → margin 200 bps: far below any floor, but enforcement is disabled.
	bd := resolve(t, newSvc(st, "aggregates"), base(ResolveInput{CategoryID: "aggregates", UOMEntered: "EA", CostCents: 9800, UserID: "ryan"}))
	if bd.Blocked {
		t.Fatalf("enforcement-disabled category must NOT block, got %+v", bd)
	}
	var bypass bool
	for _, a := range st.audits {
		if a.Action == "BYPASS" && a.EntityType == "margin_enforcement" {
			bypass = true
		}
	}
	if !bypass {
		t.Fatalf("a disabled item must still write a BYPASS audit row (no silent bypass), got %+v", st.audits)
	}
}

// - U23: tier/category-scope rules are discount-%-only (ABSOLUTE rejected) -
func TestRule_DiscountOnlyCheck_RejectsAbsoluteOnScope(t *testing.T) {
	svc := NewService(&fakeStore{byID: map[string]Rule{}})
	if _, err := svc.CreateRule(context.Background(), Rule{Scope: ScopeTierCategory, CalcType: CalcAbsolute, PriceCents: p64(41000), PriceUOM: "SF"}, "ryan"); err == nil {
		t.Fatal("ABSOLUTE on a TIER_CATEGORY scope must be rejected (discount-percent-only)")
	}
	if _, err := svc.CreateRule(context.Background(), Rule{Scope: ScopeCustomerProduct, CalcType: CalcAbsolute, PriceCents: p64(41000), PriceUOM: "SF"}, "ryan"); err != nil {
		t.Fatalf("ABSOLUTE on CUSTOMER_PRODUCT (negotiated contract) must be allowed, got %v", err)
	}
}

// - U24: partial-skid SURCHARGE under a weight (T/KG) threshold via ConvertUOM -
func TestResolve_Surcharge_PartialSkidWeightUOM(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"stone", 10000, "T"},
		breaks: []QuantityBreak{{
			HeaderID: "h-skid", ProductID: "stone", Name: "Partial-skid surcharge",
			ThresholdUOM: "T", FromQty: 0, ToQty: f64p(4.5), // under ~4.5 T (partial skid)
			ValueType: BreakSurchargePct, SurchargeBps: p64(1500), PriceUOM: "T",
		}},
	}
	// 500 KG → 0.5 T (via ConvertUOM seam), under threshold → +15% surcharge (price UP). No rule
	// matches, so the base is LIST (retail) and the surcharge STACKS on it: Source stays LIST.
	bd := resolve(t, newSvc(st, "stone"), base(ResolveInput{QtyEntered: 500, UOMEntered: "KG"}))
	if bd.Source != SourceList || bd.FinalCents != 11500 || bd.FinalCents <= bd.OriginalCents {
		t.Fatalf("partial-skid surcharge: 10000 + 15%% → 11500 (price UP, source stays LIST), got %+v", bd)
	}
}

// - F1a: NO explicit list anchor → derive the retail anchor from cost × 1.85 (NEVER $0) -
// Fix #1: a rule matches but no pricing_list_prices row exists. Pre-fix the calc ran off
// originalCents=0 and returned $0; post-fix the LIST anchor is derived (cost × retailMarkupBps).
func TestResolve_NoAnchor_DerivesRetailFromCost(t *testing.T) {
	st := &fakeStore{
		rules: []Rule{{ID: "r-def", Scope: ScopeTierDefault, CustomerType: "CONTRACTOR", CalcType: CalcPctOffList, DiscountBps: p64(1500), PriceUOM: "EA", Priority: 10, IsActive: true}},
	}
	// cost 10000 → derived anchor 10000×1.85 = 18500; 15% off → 15725. NEVER 0.
	bd := resolve(t, newSvc(st, "pavers"), base(ResolveInput{UOMEntered: "EA", CostCents: 10000}))
	if bd.Source != SourceTierDefault || bd.OriginalCents != 18500 || bd.FinalCents != 15725 {
		t.Fatalf("no-anchor derive: cost 10000 → anchor 18500, 15%% off → 15725, got %+v", bd)
	}
	if bd.FinalCents <= 0 || bd.Blocked {
		t.Fatalf("derived price must be > 0 and not spuriously blocked, got %+v", bd)
	}
}

// - F1b: NO anchor AND no cost to derive from → errNoListPrice (422), NEVER $0 -
func TestResolve_NoAnchorNoCost_ErrNoListPrice(t *testing.T) {
	st := &fakeStore{
		rules: []Rule{{ID: "r-def", Scope: ScopeTierDefault, CustomerType: "CONTRACTOR", CalcType: CalcPctOffList, DiscountBps: p64(1500), PriceUOM: "EA", Priority: 10, IsActive: true}},
	}
	_, err := newSvc(st, "pavers").ResolvePrice(context.Background(), base(ResolveInput{UOMEntered: "EA", CostCents: 0}))
	if !errors.Is(err, errNoListPrice) {
		t.Fatalf("a discount with neither anchor nor cost must 422 (errNoListPrice), not price off 0; got %v", err)
	}
}

// - F1c: bare LIST (retail, no rule) with no anchor but a cost still PRICES (derived) -
func TestResolve_List_DerivedAnchorFromCost(t *testing.T) {
	st := &fakeStore{} // no listPrice, no rules
	bd := resolve(t, newSvc(st, "pavers"), base(ResolveInput{CustomerType: "RETAIL", UOMEntered: "EA", CostCents: 10000}))
	if bd.Source != SourceList || bd.FinalCents != 18500 {
		t.Fatalf("retail LIST with no anchor but cost 10000 → derived 18500, got %+v", bd)
	}
}

// - F1d: MARKUP_ON_COST with a $0 cost basis is not a price → errNoListPrice -
func TestResolve_MarkupOnCost_ZeroCost_Errors(t *testing.T) {
	st := &fakeStore{rules: []Rule{{ID: "r-def", Scope: ScopeTierDefault, CustomerType: "CONTRACTOR", CalcType: CalcMarkupOnCost, MarkupBps: p64(8500), PriceUOM: "EA", Priority: 10, IsActive: true}}}
	_, err := newSvc(st, "pavers").ResolvePrice(context.Background(), base(ResolveInput{UOMEntered: "EA", CostCents: 0}))
	if !errors.Is(err, errNoListPrice) {
		t.Fatalf("MARKUP_ON_COST off a $0 cost must 422, got %v", err)
	}
}

// - F2: CUSTOMER_CATEGORY rule applies at the CUSTOMER_OVERRIDE level (beats TIER_CATEGORY) -
// Fix #2: the scope was dead (omitted from MatchingRules + unhandled in selectRule).
func TestResolve_CustomerCategoryRule_BeatsTierCategory(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 48700, "SF"},
		rules: []Rule{
			{ID: "r-cat", Scope: ScopeTierCategory, CustomerType: "CONTRACTOR", CategoryID: "pavers", CalcType: CalcPctOffList, DiscountBps: p64(1800), PriceUOM: "SF", Priority: 40, IsActive: true},
			{ID: "r-cc", Scope: ScopeCustomerCategory, CustomerID: "abc", CategoryID: "pavers", CalcType: CalcPctOffList, DiscountBps: p64(2500), PriceUOM: "SF", Priority: 50, IsActive: true, DetailText: "ABC, Pavers: 25% off"},
		},
	}
	bd := resolve(t, newSvc(st, "pavers"), base(ResolveInput{CategoryID: "pavers", UOMEntered: "SF"}))
	if bd.Source != SourceCustomerCategory || bd.FinalCents != 36525 { // 48700 × 0.75
		t.Fatalf("CUSTOMER_CATEGORY 25%% off 48700 → 36525 must beat TIER_CATEGORY, got %+v", bd)
	}
}

// - F3: a break whose threshold_uom has no conversion fails LOUD (errNoConversion) -
// Fix #3: selectBreak previously `continue`d, silently dropping the break and undercharging.
func TestResolve_Break_BadThresholdUOM_Errors(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 48700, "SF"},
		breaks: []QuantityBreak{{
			HeaderID: "h1", ProductID: "blu60", ThresholdUOM: "ZZ", // no conversion factor
			FromQty: 0, ToQty: f64p(1000), ValueType: BreakPctOff, DiscountBps: p64(2000), PriceUOM: "SF",
		}},
	}
	_, err := newSvc(st, "pavers").ResolvePrice(context.Background(), base(ResolveInput{QtyEntered: 10, UOMEntered: "SF"}))
	if !errors.Is(err, errNoConversion) {
		t.Fatalf("a break with an unconvertible threshold_uom must 422 (errNoConversion), not be silently skipped; got %v", err)
	}
}

// - F4: a SURCHARGE is an ADJUSTMENT on the discounted base, not a level that replaces it -
// Fix #4: contractor 18% off + 15% partial-skid surcharge → discounted-THEN-surcharged
// (48700 × 0.82 × 1.15 = 45924), NOT list + 15% (48700 × 1.15 = 56005).
func TestResolve_Surcharge_AppliesOnTopOfTierDiscount(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 48700, "SF"},
		rules:     []Rule{{ID: "r-cat", Scope: ScopeTierCategory, CustomerType: "CONTRACTOR", CategoryID: "pavers", CalcType: CalcPctOffList, DiscountBps: p64(1800), PriceUOM: "SF", Priority: 40, IsActive: true}},
		breaks: []QuantityBreak{{
			HeaderID: "h-skid", ProductID: "blu60", Name: "Partial-skid surcharge",
			ThresholdUOM: "SF", FromQty: 0, ToQty: f64p(200), // partial skid under 200 SF
			ValueType: BreakSurchargePct, SurchargeBps: p64(1500), PriceUOM: "SF",
		}},
	}
	bd := resolve(t, newSvc(st, "pavers"), base(ResolveInput{CategoryID: "pavers", QtyEntered: 10, UOMEntered: "SF"}))
	if bd.Source != SourceTierCategory {
		t.Fatalf("a surcharge-adjusted line keeps the base source TIER_CATEGORY (composition in detail), got %+v", bd)
	}
	if bd.FinalCents != 45924 {
		t.Fatalf("discounted-then-surcharged: 48700 × 0.82 × 1.15 = 45924, got %d (list+15%% would be 56005)", bd.FinalCents)
	}
	if !strings.Contains(bd.Detail, "Partial-skid surcharge") {
		t.Fatalf("breakdown detail must note the stacked surcharge, got %q", bd.Detail)
	}
}

// - F4b: a quantity DISCOUNT break STACKS on the tier-resolved base (the core stacking fix) -
// Contractor Pavers (−18% TIER_CATEGORY) + a 5% full-skid DISCOUNT break STACK:
// 48700 × 0.82 × 0.95 = 37937 (NOT 48700 × 0.95 = 46265), and never above the −18% base (39934).
func TestResolve_QuantityDiscountBreak_StacksOnTierBase(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 48700, "SF"},
		rules:     []Rule{{ID: "r-cat", Scope: ScopeTierCategory, CustomerType: "CONTRACTOR", CategoryID: "pavers", CalcType: CalcPctOffList, DiscountBps: p64(1800), PriceUOM: "SF", Priority: 40, IsActive: true, DetailText: "Contractor, Pavers: 18% off"}},
		breaks: []QuantityBreak{{
			HeaderID: "h-skid", CategoryID: "pavers", Name: "Full-skid −5%",
			ThresholdUOM: "SF", FromQty: 200, ValueType: BreakPctOff, DiscountBps: p64(500), PriceUOM: "SF",
		}},
	}
	bd := resolve(t, newSvc(st, "pavers"), base(ResolveInput{CategoryID: "pavers", QtyEntered: 240, UOMEntered: "SF"}))
	if bd.Source != SourceTierCategory {
		t.Fatalf("the base source must stay TIER_CATEGORY (the break STACKS, not competes), got %+v", bd)
	}
	if bd.FinalCents != 37937 {
		t.Fatalf("stacked −18%%+−5%%: 48700×0.82×0.95 = 37937, got %d (a competing break would be 46265)", bd.FinalCents)
	}
	if bd.FinalCents >= 39934 {
		t.Fatalf("a bulk discount must NEVER price above the contractor base 39934, got %d", bd.FinalCents)
	}
	if !strings.Contains(bd.Detail, "Contractor, Pavers: 18% off") || !strings.Contains(bd.Detail, "Full-skid −5%") {
		t.Fatalf("breakdown detail must show BOTH the base source AND the stacked break, got %q", bd.Detail)
	}
}

// - F4c: an ABS_OFF break stacks as a flat discount and is clamped to ≥ 0 -
func TestResolve_AbsOffBreak_StacksAndClamps(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 48700, "SF"},
		rules:     []Rule{{ID: "r-cat", Scope: ScopeTierCategory, CustomerType: "CONTRACTOR", CategoryID: "pavers", CalcType: CalcPctOffList, DiscountBps: p64(1800), PriceUOM: "SF", Priority: 40, IsActive: true}},
		breaks: []QuantityBreak{{
			HeaderID: "h-abs", CategoryID: "pavers", Name: "Skid rebate", ThresholdUOM: "SF",
			FromQty: 0, ValueType: BreakAbsOff, DiscountCents: p64(2000), PriceUOM: "SF",
		}},
	}
	// base = 48700 × 0.82 = 39934; flat −2000 ¢ → 37934.
	bd := resolve(t, newSvc(st, "pavers"), base(ResolveInput{CategoryID: "pavers", QtyEntered: 10, UOMEntered: "SF"}))
	if bd.Source != SourceTierCategory || bd.FinalCents != 37934 {
		t.Fatalf("ABS_OFF stacks flat: 39934 − 2000 = 37934 (source stays TIER_CATEGORY), got %+v", bd)
	}
	// A flat discount larger than the base clamps to 0, never negative, never above the base.
	st.breaks[0].DiscountCents = p64(99999999)
	bd2 := resolve(t, newSvc(st, "pavers"), base(ResolveInput{CategoryID: "pavers", QtyEntered: 10, UOMEntered: "SF"}))
	if bd2.FinalCents != 0 {
		t.Fatalf("an over-large ABS_OFF must clamp to 0, got %d", bd2.FinalCents)
	}
}

// - F6: an unknown product (catalog seam not-found) maps to pricing errNotFound (→ 404) -
// Fix #6: GetCategory's product not-found previously surfaced as a generic 500.
func TestResolve_ProductNotFound_MapsToNotFound(t *testing.T) {
	st := &fakeStore{listPrice: &ListPrice{"blu60", 48700, "SF"}}
	svc := NewService(st).
		WithUOMConverter(fakeUOM{stdFactors()}).
		WithMarginGate(NewIdentityMarginGate()).
		WithCategoryResolver(errCat{err: product.ErrNotFound})
	_, err := svc.ResolvePrice(context.Background(), ResolveInput{ProductID: "ghost", CustomerID: "abc", CustomerType: "RETAIL", QtyEntered: 1, UOMEntered: "SF"})
	if !errors.Is(err, errNotFound) {
		t.Fatalf("an unknown product must map to errNotFound (404), got %v", err)
	}
}
