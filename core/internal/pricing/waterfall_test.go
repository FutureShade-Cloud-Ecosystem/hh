package pricing

// waterfall_test.go, GABLE:waterfall-completion. The PRECEDENCE TABLE, proven one interaction at
// a time.
//
// The ruling under test is stated in full at the top of promo.go. Each test below is ONE row of
// that table, and each is named after the interaction rather than the mechanism, so a failure
// reads as "contract no longer beats promo" instead of "TestResolve_7 failed".
//
// THE FIXTURE IS DELIBERATELY DB-FREE. These are precedence assertions, not persistence
// assertions; a fake promo store keeps the arithmetic legible and lets the whole table run in
// milliseconds under -shuffle=on. The DB half (real CHECKs, real window SQL) is covered by
// promo_integration_test.go.

import (
	"context"
	"strings"
	"testing"
	"time"
)

// - DB-free promo fake -

type fakePromoStore struct {
	promos    []Promotion
	clearance *ClearanceItem
	// calls records what the resolver actually asked for, so the "a contract price is final" test
	// can prove promotions were not merely ignored but never fetched.
	promoCalls int
}

func (f *fakePromoStore) ActivePromotions(_ context.Context, _, _, _ string, _ time.Time) ([]Promotion, error) {
	f.promoCalls++
	return f.promos, nil
}
func (f *fakePromoStore) ClearanceFor(_ context.Context, productID string) (*ClearanceItem, error) {
	if f.clearance != nil && f.clearance.ProductID == productID {
		return f.clearance, nil
	}
	return nil, nil
}
func (f *fakePromoStore) ListPromotions(context.Context, bool) ([]Promotion, error) {
	return f.promos, nil
}
func (f *fakePromoStore) GetPromotion(_ context.Context, id string) (Promotion, error) {
	for _, p := range f.promos {
		if p.ID == id {
			return p, nil
		}
	}
	return Promotion{}, errNotFound
}
func (f *fakePromoStore) CreatePromotion(_ context.Context, p Promotion) (Promotion, error) {
	if p.ID == "" {
		p.ID = "promo-" + p.Code
	}
	p.Version = 1
	f.promos = append(f.promos, p)
	return p, nil
}
func (f *fakePromoStore) SavePromotion(_ context.Context, p Promotion, expectedVersion int64) (Promotion, error) {
	for i, cur := range f.promos {
		if cur.ID != p.ID {
			continue
		}
		if cur.Version != expectedVersion {
			return Promotion{}, errConflict
		}
		p.Version = cur.Version + 1
		f.promos[i] = p
		return p, nil
	}
	return Promotion{}, errNotFound
}
func (f *fakePromoStore) DeletePromotion(_ context.Context, id string, _ int64) error {
	for i, cur := range f.promos {
		if cur.ID == id {
			f.promos[i].IsActive = false
			return nil
		}
	}
	return errNotFound
}
func (f *fakePromoStore) ListClearance(context.Context, bool) ([]ClearanceItem, error) {
	if f.clearance == nil {
		return nil, nil
	}
	return []ClearanceItem{*f.clearance}, nil
}
func (f *fakePromoStore) UpsertClearance(_ context.Context, c ClearanceItem) (ClearanceItem, error) {
	c.Version = 1
	f.clearance = &c
	return c, nil
}
func (f *fakePromoStore) DeleteClearance(context.Context, string) error {
	f.clearance = nil
	return nil
}

// june is the promo window every test in this file uses; fixedNow sits squarely inside it so a
// test that is ABOUT precedence never accidentally fails on a date boundary. Boundary behaviour has
// its own tests in promo_test.go.
var (
	juneStart = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	juneEnd   = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	fixedNow  = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
)

// pctPromo builds a store-wide PCT_OFF June promo.
func pctPromo(id string, bps int64) Promotion {
	return Promotion{
		ID: id, Code: id, Name: "June Sale", StartsAt: juneStart, EndsAt: juneEnd,
		CalcType: PromoPctOff, DiscountBps: p64(bps), IsActive: true,
	}
}

// promoSvc builds a resolver with the promo seam wired and the clock pinned inside June.
func promoSvc(st *fakeStore, ps *fakePromoStore, cat string) *Service {
	return newSvc(st, cat).WithPromoStore(ps).withClock(func() time.Time { return fixedNow })
}

// - ROW 1 · contract beats promo, and the promo is never even fetched -

func TestWaterfall_ContractPriceBeatsPromo(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 10000, "EA"},
		rules: []Rule{{
			ID: "r-contract", Scope: ScopeCustomerProduct, CustomerID: "abc", ProductID: "blu60",
			CalcType: CalcAbsolute, PriceCents: p64(7000), PriceUOM: "EA", IsActive: true,
			DetailText: "Negotiated contract price",
		}},
	}
	ps := &fakePromoStore{promos: []Promotion{pctPromo("p-june", 5000)}} // 50% off would be 5000
	bd := resolve(t, promoSvc(st, ps, ""), base(ResolveInput{QtyEntered: 1, UOMEntered: "EA"}))

	if bd.Source != SourceCustomerProduct || bd.FinalCents != 7000 {
		t.Fatalf("a negotiated contract price is FINAL: want CUSTOMER_PRODUCT_RULE/7000, got %s/%d", bd.Source, bd.FinalCents)
	}
	if bd.PromoCode != "" {
		t.Fatalf("a contract line must not advertise a promo code, got %q", bd.PromoCode)
	}
	// The stronger claim: not merely "the promo lost" but "the promo was never consulted". That is
	// what makes the contract path a single round-trip, and a regression to fetch-then-discard
	// would be invisible to a price assertion alone.
	if ps.promoCalls != 0 {
		t.Fatalf("contract path must not fetch promotions at all, got %d calls", ps.promoCalls)
	}
}

// - ROW 2 · promo beats the tier waterfall when it is cheaper -

func TestWaterfall_PromoBeatsTierDiscount(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 10000, "EA"},
		rules: []Rule{{
			ID: "r-def", Scope: ScopeTierDefault, CustomerType: "CONTRACTOR",
			CalcType: CalcPctOffList, DiscountBps: p64(1000), PriceUOM: "EA", IsActive: true,
		}},
	}
	// Tier gives 10% off → 9000. The promo gives 25% off → 7500, which is better, so it wins.
	ps := &fakePromoStore{promos: []Promotion{pctPromo("p-june", 2500)}}
	bd := resolve(t, promoSvc(st, ps, ""), base(ResolveInput{QtyEntered: 1, UOMEntered: "EA"}))

	if bd.Source != SourcePromotion {
		t.Fatalf("a cheaper promo must take over base selection, got source %s", bd.Source)
	}
	if bd.FinalCents != 7500 {
		t.Fatalf("promo 25%% off the 10000 anchor = 7500, got %d", bd.FinalCents)
	}
	if bd.PromoCode != "p-june" {
		t.Fatalf("the winning promo code must reach the breakdown, got %q", bd.PromoCode)
	}
}

// - ROW 2a · THE NEVER-WORSE GUARD -

func TestWaterfall_PromoNeverRaisesAContractorsPrice(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 10000, "EA"},
		rules: []Rule{{
			ID: "r-def", Scope: ScopeTierDefault, CustomerType: "CONTRACTOR",
			CalcType: CalcPctOffList, DiscountBps: p64(1800), PriceUOM: "EA", IsActive: true,
		}},
	}
	// The contractor is already at 18% off (8200). A 10%-off sale is WORSE for them.
	ps := &fakePromoStore{promos: []Promotion{pctPromo("p-june", 1000)}}
	bd := resolve(t, promoSvc(st, ps, ""), base(ResolveInput{QtyEntered: 1, UOMEntered: "EA"}))

	if bd.FinalCents != 8200 {
		t.Fatalf("the customer keeps their better tier price: want 8200, got %d", bd.FinalCents)
	}
	if bd.Source != SourceTierDefault {
		t.Fatalf("source must stay the tier level when the promo loses, got %s", bd.Source)
	}
	if bd.PromoCode != "" {
		t.Fatalf("a losing promo must NOT be advertised as the price source, got %q", bd.PromoCode)
	}
	// Silence would be a support call: the customer has seen the sale sign.
	if !strings.Contains(bd.Detail, "not applied") {
		t.Fatalf("the breakdown must explain why the sale price was not used, got %q", bd.Detail)
	}
}

// - ROW 3 · a quantity break NEVER stacks on the promo price (D1) -

func TestWaterfall_QuantityBreakNeverStacksOnThePromoPrice(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 10000, "EA"},
		rules: []Rule{{
			ID: "r-def", Scope: ScopeTierDefault, CustomerType: "CONTRACTOR",
			CalcType: CalcPctOffList, DiscountBps: p64(500), PriceUOM: "EA", IsActive: true,
		}},
		breaks: []QuantityBreak{{
			HeaderID: "h1", ItemID: "i1", ProductID: "blu60", ThresholdUOM: "EA",
			FromQty: 10, ValueType: BreakPctOff, DiscountBps: p64(1000), PriceUOM: "EA",
		}},
	}
	// Tier 5% → 9500. Promo 20% → 8000 wins the base. The break's own single price is
	// 10000 × 0.90 = 9000, which LOSES to the promo: the customer keeps the best single price and
	// never the pile (stacking would answer 10000×0.80×0.90 = 7200).
	ps := &fakePromoStore{promos: []Promotion{pctPromo("p-june", 2000)}}
	bd := resolve(t, promoSvc(st, ps, ""), base(ResolveInput{QtyEntered: 20, UOMEntered: "EA"}))

	if bd.FinalCents != 8000 {
		t.Fatalf("the break prices off the anchor and loses to the promo: 8000 stands, got %d", bd.FinalCents)
	}
	if bd.Source != SourcePromotion {
		t.Fatalf("the promo still prices the line, got source %s", bd.Source)
	}
	if !strings.Contains(bd.Detail, "June Sale") || !strings.Contains(bd.Detail, "not applied") {
		t.Fatalf("the detail must carry both the promo story and the held break, got %q", bd.Detail)
	}
}

// - ROW 3a · the break wins the contest when it is strictly cheaper -

func TestWaterfall_QuantityBreakReplacesWhenStrictlyCheaper(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 10000, "EA"},
		rules: []Rule{{
			ID: "r-def", Scope: ScopeTierDefault, CustomerType: "CONTRACTOR",
			CalcType: CalcPctOffList, DiscountBps: p64(500), PriceUOM: "EA", IsActive: true,
		}},
		breaks: []QuantityBreak{{
			HeaderID: "h1", ItemID: "i1", ProductID: "blu60", ThresholdUOM: "EA",
			FromQty: 10, ValueType: BreakPctOff, DiscountBps: p64(3000), PriceUOM: "EA",
		}},
	}
	// Tier 5% → 9500; promo 20% → 8000; break 30% off the anchor → 7000. The break is the best
	// SINGLE price, so it replaces both: source QUANTITY_BREAK, and the beaten promo is no longer
	// advertised as what prices this line.
	ps := &fakePromoStore{promos: []Promotion{pctPromo("p-june", 2000)}}
	bd := resolve(t, promoSvc(st, ps, ""), base(ResolveInput{QtyEntered: 20, UOMEntered: "EA"}))

	if bd.Source != SourceQuantityBreak || bd.FinalCents != 7000 {
		t.Fatalf("the break's single price 7000 replaces tier and promo, got %s/%d", bd.Source, bd.FinalCents)
	}
	if bd.PromoCode != "" {
		t.Fatalf("the beaten promo must not be advertised as the price source, got %q", bd.PromoCode)
	}
	if !strings.Contains(bd.Detail, "volume break") {
		t.Fatalf("the detail tells the winner's story alone, got %q", bd.Detail)
	}
}

// - ROW 3b · the never-worse arm: the customer's standing price beats the break and STANDS -
// The case the decision calls out by name: under the replaced model the price can be HIGHER than
// the stacked result (the ERP gave contractors both discounts); D1 gives the best single one.

func TestWaterfall_TierPriceStandsWhenTheBreakIsWorse(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 10000, "EA"},
		rules: []Rule{{
			ID: "r-def", Scope: ScopeTierDefault, CustomerType: "CONTRACTOR",
			CalcType: CalcPctOffList, DiscountBps: p64(1800), PriceUOM: "EA", IsActive: true,
		}},
		breaks: []QuantityBreak{{
			HeaderID: "h1", ItemID: "i1", ProductID: "blu60", ThresholdUOM: "EA",
			FromQty: 10, ValueType: BreakPctOff, DiscountBps: p64(1000), PriceUOM: "EA",
		}},
	}
	// The contractor is already at 18% off (8200). The break's 10% off the anchor is 9000, worse.
	bd := resolve(t, promoSvc(st, &fakePromoStore{}, ""), base(ResolveInput{QtyEntered: 20, UOMEntered: "EA"}))
	if bd.Source != SourceTierDefault || bd.FinalCents != 8200 {
		t.Fatalf("the tier price 8200 stands (stacking would answer 7380), got %s/%d", bd.Source, bd.FinalCents)
	}
	if !strings.Contains(bd.Detail, "not applied") {
		t.Fatalf("the breakdown must say why the volume break did not land, got %q", bd.Detail)
	}
}

// - ROW 3b · the contract path takes NO break either -

func TestWaterfall_ContractPriceTakesNoQuantityBreak(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 10000, "EA"},
		rules: []Rule{{
			ID: "r-contract", Scope: ScopeCustomerProduct, CustomerID: "abc", ProductID: "blu60",
			CalcType: CalcAbsolute, PriceCents: p64(7000), PriceUOM: "EA", IsActive: true,
		}},
		breaks: []QuantityBreak{{
			HeaderID: "h1", ItemID: "i1", ProductID: "blu60", ThresholdUOM: "EA",
			FromQty: 10, ValueType: BreakPctOff, DiscountBps: p64(1000), PriceUOM: "EA",
		}},
	}
	ps := &fakePromoStore{promos: []Promotion{pctPromo("p-june", 2000)}}
	bd := resolve(t, promoSvc(st, ps, ""), base(ResolveInput{QtyEntered: 50, UOMEntered: "EA"}))
	if bd.FinalCents != 7000 {
		t.Fatalf("contract price is final against BOTH promo and break, got %d", bd.FinalCents)
	}
}

// - ROW 4 · LIST is still the base of last resort, and a promo discounts IT -

func TestWaterfall_PromoDiscountsListWhenNoRuleMatches(t *testing.T) {
	st := &fakeStore{listPrice: &ListPrice{"blu60", 10000, "EA"}}
	ps := &fakePromoStore{promos: []Promotion{pctPromo("p-june", 3000)}}
	bd := resolve(t, promoSvc(st, ps, ""), base(ResolveInput{CustomerType: "RETAIL", QtyEntered: 1, UOMEntered: "EA"}))
	if bd.Source != SourcePromotion || bd.FinalCents != 7000 {
		t.Fatalf("a walk-in on sale pays list less the promo: want PROMOTION/7000, got %s/%d", bd.Source, bd.FinalCents)
	}
}

// - ROW 5 · THE MARGIN GATE EVALUATES THE FINAL PRICE, the break's replaced price included -

func TestWaterfall_MarginFloorEvaluatesThePriceAfterPromoAndBreak(t *testing.T) {
	// Cost 7000. Floor 1200 bps (12%). The line must clear 7954 to pass.
	//   list 10000 → promo 15% → 8500   (margin 1765 bps: PASSES on its own)
	//   break 30% off the anchor → 7000 (margin    0 bps: FAILS, and it WINS the replace contest)
	// Under D1 the break's single price REPLACES the promo price (7000 < 8500), and the gate sees
	// the replaced price. If the gate ran on the promo price instead of the final price, this line
	// would sail through at a margin the yard never agreed to. That is the whole point.
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 10000, "EA"},
		catPolicy: &CategoryPolicy{CategoryID: "pavers", MarginTargetBps: 3000, MarginFloorBps: 1200},
		breaks: []QuantityBreak{{
			HeaderID: "h1", ItemID: "i1", ProductID: "blu60", ThresholdUOM: "EA",
			FromQty: 10, ValueType: BreakPctOff, DiscountBps: p64(3000), PriceUOM: "EA",
		}},
	}
	ps := &fakePromoStore{promos: []Promotion{pctPromo("p-june", 1500)}}
	bd := resolve(t, promoSvc(st, ps, "pavers"),
		base(ResolveInput{CustomerType: "RETAIL", QtyEntered: 20, UOMEntered: "EA", CostCents: 7000}))

	if bd.Source != SourceQuantityBreak || bd.FinalCents != 7000 {
		t.Fatalf("the break replaces the promo price: want QUANTITY_BREAK/7000, got %s/%d", bd.Source, bd.FinalCents)
	}
	if !bd.Blocked || bd.BlockPolicy != BlockMarginBelowMinimum {
		t.Fatalf("the FINAL price is below the floor and must be blocked, got blocked=%v policy=%q margin=%d floor=%d",
			bd.Blocked, bd.BlockPolicy, bd.MarginBps, bd.FloorBps)
	}
}

func TestWaterfall_MarginFloorPassesWhenOnlyThePromoApplies(t *testing.T) {
	// The control for the test above: same promo, same cost, NO break, 8500 clears the floor. If
	// this one also blocked, the previous test would be proving nothing about the break.
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 10000, "EA"},
		catPolicy: &CategoryPolicy{CategoryID: "pavers", MarginTargetBps: 3000, MarginFloorBps: 1200},
	}
	ps := &fakePromoStore{promos: []Promotion{pctPromo("p-june", 1500)}}
	bd := resolve(t, promoSvc(st, ps, "pavers"),
		base(ResolveInput{CustomerType: "RETAIL", QtyEntered: 1, UOMEntered: "EA", CostCents: 7000}))
	if bd.FinalCents != 8500 || bd.Blocked {
		t.Fatalf("promo alone clears the floor: want 8500 unblocked, got %d blocked=%v", bd.FinalCents, bd.Blocked)
	}
}

// - ROW 6 · specificity: clearance > product promo > category promo > store-wide -

func TestWaterfall_PromoSpecificityOrder(t *testing.T) {
	st := &fakeStore{listPrice: &ListPrice{"blu60", 10000, "EA"}}

	storeWide := pctPromo("p-all", 1000) // 9000
	catWide := pctPromo("p-cat", 2000)   // 8000
	catWide.CategoryID = "pavers"
	productOnly := pctPromo("p-prod", 500) // 9500, WORSE, and must still win specificity
	productOnly.ProductID = "blu60"

	// The product promo is the LEAST generous of the three. Specificity is not "best price": a
	// promo authored against one SKU is a deliberate statement about that SKU and outranks a
	// blanket sale. (The never-worse guard still protects the customer against the TIER price;
	// it does not arbitrate between two promos.)
	ps := &fakePromoStore{promos: []Promotion{storeWide, catWide, productOnly}}
	bd := resolve(t, promoSvc(st, ps, "pavers"), base(ResolveInput{CustomerType: "RETAIL", QtyEntered: 1, UOMEntered: "EA"}))
	if bd.PromoCode != "p-prod" || bd.FinalCents != 9500 {
		t.Fatalf("the product-scoped promo must win specificity: want p-prod/9500, got %q/%d", bd.PromoCode, bd.FinalCents)
	}

	// Drop the product promo: the category promo takes over, and the store-wide one loses.
	ps2 := &fakePromoStore{promos: []Promotion{storeWide, catWide}}
	bd2 := resolve(t, promoSvc(st, ps2, "pavers"), base(ResolveInput{CustomerType: "RETAIL", QtyEntered: 1, UOMEntered: "EA"}))
	if bd2.PromoCode != "p-cat" || bd2.FinalCents != 8000 {
		t.Fatalf("the category promo must beat the store-wide one: want p-cat/8000, got %q/%d", bd2.PromoCode, bd2.FinalCents)
	}
}

func TestWaterfall_ClearancePriceOutranksEveryPromo(t *testing.T) {
	st := &fakeStore{listPrice: &ListPrice{"blu60", 10000, "EA"}}
	prodPromo := pctPromo("p-prod", 4000) // 6000
	prodPromo.ProductID = "blu60"
	ps := &fakePromoStore{
		promos: []Promotion{prodPromo},
		clearance: &ClearanceItem{
			ProductID: "blu60", Reason: "discontinued colour", ClearancePriceCents: p64(6500),
			PriceUOM: "EA", StartedAt: juneStart, IsActive: true,
		},
	}
	bd := resolve(t, promoSvc(st, ps, "pavers"), base(ResolveInput{CustomerType: "RETAIL", QtyEntered: 1, UOMEntered: "EA"}))
	if bd.FinalCents != 6500 {
		t.Fatalf("clearance is the most specific statement about a SKU: want 6500, got %d", bd.FinalCents)
	}
	if !bd.Clearance || bd.ClearanceReason != "discontinued colour" {
		t.Fatalf("the clearance badge + reason must ride on the breakdown, got %+v", bd)
	}
}

// - ROW 7 · the clearance BADGE is independent of the clearance PRICE -

func TestWaterfall_ClearanceBadgeWithoutAPriceStillFlagsTheLine(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 10000, "EA"},
		rules: []Rule{{
			ID: "r-def", Scope: ScopeTierDefault, CustomerType: "CONTRACTOR",
			CalcType: CalcPctOffList, DiscountBps: p64(1000), PriceUOM: "EA", IsActive: true,
		}},
	}
	ps := &fakePromoStore{clearance: &ClearanceItem{
		ProductID: "blu60", Reason: "end of line", StartedAt: juneStart, IsActive: true,
	}}
	bd := resolve(t, promoSvc(st, ps, "pavers"), base(ResolveInput{QtyEntered: 1, UOMEntered: "EA"}))
	if bd.FinalCents != 9000 || bd.Source != SourceTierDefault {
		t.Fatalf("a badge-only clearance changes NO price: want TIER_DEFAULT/9000, got %s/%d", bd.Source, bd.FinalCents)
	}
	if !bd.Clearance {
		t.Fatalf("the badge must still be set so POS search and quote lines can mark the item")
	}
}

// A contract customer buying a clearance SKU sees the badge too, the flag is a fact about the
// product, not about the deal.
func TestWaterfall_ClearanceBadgeShowsOnAContractLine(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 10000, "EA"},
		rules: []Rule{{
			ID: "r-contract", Scope: ScopeCustomerProduct, CustomerID: "abc", ProductID: "blu60",
			CalcType: CalcAbsolute, PriceCents: p64(7000), PriceUOM: "EA", IsActive: true,
		}},
	}
	ps := &fakePromoStore{clearance: &ClearanceItem{
		ProductID: "blu60", Reason: "end of line", ClearancePriceCents: p64(5000), PriceUOM: "EA",
		StartedAt: juneStart, IsActive: true,
	}}
	bd := resolve(t, promoSvc(st, ps, "pavers"), base(ResolveInput{QtyEntered: 1, UOMEntered: "EA"}))
	if bd.FinalCents != 7000 {
		t.Fatalf("contract still wins the PRICE even against a cheaper clearance, got %d", bd.FinalCents)
	}
	if !bd.Clearance {
		t.Fatalf("but the BADGE must show, the item is on clearance regardless of who is buying")
	}
}

// - ROW 8 · flag-off: no promo store ⇒ the W3 waterfall, byte for byte -

func TestWaterfall_NoPromoStoreLeavesTheW3WaterfallUntouched(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 10000, "EA"},
		rules: []Rule{{
			ID: "r-def", Scope: ScopeTierDefault, CustomerType: "CONTRACTOR",
			CalcType: CalcPctOffList, DiscountBps: p64(1500), PriceUOM: "EA", IsActive: true,
		}},
	}
	bd := resolve(t, newSvc(st, ""), base(ResolveInput{QtyEntered: 1, UOMEntered: "EA"}))
	if bd.Source != SourceTierDefault || bd.FinalCents != 8500 || bd.Clearance || bd.PromoCode != "" {
		t.Fatalf("an unwired promo seam must be invisible, got %+v", bd)
	}
}
