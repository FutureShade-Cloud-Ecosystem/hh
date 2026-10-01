package pricing

// counter_test.go, the counter price picture (counter.go). DB-free, on the service_test.go
// fakes; exactly additive.
//
// What is under test here is the SUBTRACTION and the WORD that explains it, the two things the
// counter renders and the two things a client-side implementation would get wrong the first time
// a rule changed. The waterfall's own arithmetic is proven by the tests above this file; these
// prove that retail and the customer's price come out of ONE resolve, that a saving is only ever
// claimed when the customer is genuinely better off, and that the read never writes.

import (
	"context"
	"testing"
)

func counterSvc(st *fakeStore) *Service { return newSvc(st, "pavers") }

func counterQuote(t *testing.T, s *Service, in CounterQuoteInput) CounterQuote {
	t.Helper()
	q, err := s.CounterQuote(context.Background(), in)
	if err != nil {
		t.Fatalf("CounterQuote: %v", err)
	}
	return q
}

func retailInput(qty float64) CounterQuoteInput {
	return CounterQuoteInput{ProductID: "blu60", QtyEntered: qty, UOMEntered: "SF"}
}

func accountInput(qty float64) CounterQuoteInput {
	return CounterQuoteInput{CustomerID: "abc", CustomerType: "CONTRACTOR", ProductID: "blu60", QtyEntered: qty, UOMEntered: "SF"}
}

// - the walk-in: retail stands alone -

func TestCounterQuote_WalkInHasNoSavingAndNoBasis(t *testing.T) {
	st := &fakeStore{listPrice: &ListPrice{"blu60", 48700, "SF"}}
	q := counterQuote(t, counterSvc(st), retailInput(10))
	if q.RetailCents != 48700 || q.CustomerCents != 48700 {
		t.Fatalf("with no rule in play both sides are the anchor: %+v", q)
	}
	if q.SavingCents != 0 || q.SavingBps != 0 || q.Basis != "" {
		t.Fatalf("nothing was saved, so nothing may be claimed: %+v", q)
	}
}

// - the account: ONE resolve yields BOTH sides -

func TestCounterQuote_AccountDiscountReportsRetailBesideItAndTheSaving(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 48700, "SF"},
		rules: []Rule{{ID: "r-def", Scope: ScopeTierDefault, CustomerType: "CONTRACTOR", CalcType: CalcPctOffList,
			DiscountBps: p64(1500), PriceUOM: "SF", Priority: 10, IsActive: true}},
	}
	q := counterQuote(t, counterSvc(st), accountInput(10))
	if q.RetailCents != 48700 {
		t.Fatalf("retail is the SAME anchor the discount was taken off, not a second lookup: %+v", q)
	}
	if q.CustomerCents != 41395 {
		t.Fatalf("48700 less 15%% = 41395: %+v", q)
	}
	if q.SavingCents != 48700-41395 {
		t.Fatalf("the saving is the server's subtraction of its own two numbers: %+v", q)
	}
	if q.SavingBps != 1500 {
		t.Fatalf("the saving in bps is off-anchor: %+v", q)
	}
	if q.Basis != BasisAccount {
		t.Fatalf("a standing tier discount is explained as ACCOUNT: %+v", q)
	}
}

// - the contract price: final, and never dressed up as a volume offer -

func TestCounterQuote_ContractPriceIsBasisContractAndTakesNoBreakOffer(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 48700, "SF"},
		rules: []Rule{{ID: "r-cp", Scope: ScopeCustomerProduct, CustomerID: "abc", ProductID: "blu60",
			CalcType: CalcAbsolute, PriceCents: p64(41000), PriceUOM: "SF", Priority: 100, IsActive: true}},
		breaks: []QuantityBreak{{HeaderID: "h1", ProductID: "blu60", ThresholdUOM: "SF", FromQty: 200,
			ValueType: BreakPctOff, DiscountBps: p64(2500), PriceUOM: "SF"}},
	}
	q := counterQuote(t, counterSvc(st), accountInput(10))
	if q.CustomerCents != 41000 || q.Basis != BasisContract {
		t.Fatalf("a negotiated CUSTOMER_PRODUCT price explains itself as CONTRACT: %+v", q)
	}
	if q.NextBreakQty != 0 || q.NextBreakUnitCents != 0 {
		t.Fatalf("breaks do not stack on a contract price, so no threshold may be offered: %+v", q)
	}
}

// - the bulk break: the actionable half of the explanation wins the word -

func TestCounterQuote_AppliedBreakIsExplainedAsBulk(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 48700, "SF"},
		rules: []Rule{{ID: "r-def", Scope: ScopeTierDefault, CustomerType: "CONTRACTOR", CalcType: CalcPctOffList,
			DiscountBps: p64(1500), PriceUOM: "SF", Priority: 10, IsActive: true}},
		breaks: []QuantityBreak{{HeaderID: "h1", ProductID: "blu60", ThresholdUOM: "SF", FromQty: 50,
			ValueType: BreakPctOff, DiscountBps: p64(2000), PriceUOM: "SF"}},
	}
	// D1: the break's own single price off the anchor (48700 × 0.80 = 38960) REPLACES the tier
	// price (41395); the stacked 33116 is the defect this replaces.
	q := counterQuote(t, counterSvc(st), accountInput(120))
	if q.CustomerCents != 38960 {
		t.Fatalf("48700 × 0.80 = 38960 replaces the tier price: %+v", q)
	}
	if q.Basis != BasisBulk {
		t.Fatalf("when the break wins the contest, BULK is the word, it is the part they can change: %+v", q)
	}
	if q.SavingCents != 48700-38960 {
		t.Fatalf("the saving is measured against RETAIL, not against the tier price: %+v", q)
	}
}

// - the UNREACHED break: an offer to make, priced by the engine, never extrapolated -

func TestCounterQuote_SurfacesTheNearestUnreachedThreshold(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 48700, "SF"},
		breaks: []QuantityBreak{
			{HeaderID: "h1", ProductID: "blu60", ThresholdUOM: "SF", FromQty: 200, ValueType: BreakPctOff, DiscountBps: p64(2500), PriceUOM: "SF"},
			{HeaderID: "h1", ProductID: "blu60", ThresholdUOM: "SF", FromQty: 50, ToQty: f64p(200), ValueType: BreakPctOff, DiscountBps: p64(1000), PriceUOM: "SF"},
		},
	}
	q := counterQuote(t, counterSvc(st), retailInput(10))
	if q.NextBreakQty != 50 || q.NextBreakUOM != "SF" {
		t.Fatalf("the NEAREST threshold above the line is the one worth showing: %+v", q)
	}
	if q.NextBreakUnitCents != 43830 {
		t.Fatalf("the offered unit price is a real resolve AT the threshold (48700 × 0.90): %+v", q)
	}
	if q.NextBreakSaveCents != q.CustomerCents-q.NextBreakUnitCents {
		t.Fatalf("the offered saving is per unit, against what they pay now: %+v", q)
	}
}

func TestCounterQuote_AQualifiedLineIsOfferedNothing(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 48700, "SF"},
		breaks: []QuantityBreak{{HeaderID: "h1", ProductID: "blu60", ThresholdUOM: "SF", FromQty: 50,
			ValueType: BreakPctOff, DiscountBps: p64(1000), PriceUOM: "SF"}},
	}
	q := counterQuote(t, counterSvc(st), retailInput(120))
	if q.NextBreakQty != 0 {
		t.Fatalf("a line already past the only threshold has no opportunity to offer: %+v", q)
	}
	if q.Basis != BasisBulk || q.SavingCents == 0 {
		t.Fatalf("it is getting the break, and says so: %+v", q)
	}
}

// - an uplift is not a saving, and crossing into one is not an opportunity -

func TestCounterQuote_SurchargeNeverReportsANegativeSaving(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 48700, "SF"},
		breaks: []QuantityBreak{{HeaderID: "h1", ProductID: "blu60", ThresholdUOM: "SF", FromQty: 1, ToQty: f64p(50),
			ValueType: BreakSurchargePct, SurchargeBps: p64(1000), PriceUOM: "SF"}},
	}
	// D1: an uplift can never win the cheapest-wins contest, so the surcharge never lands at all:
	// the walk-in pays retail, and nothing claims a saving in either direction.
	q := counterQuote(t, counterSvc(st), retailInput(10))
	if q.CustomerCents != q.RetailCents {
		t.Fatalf("a surcharge tier never adjusts the price under the replace rule: %+v", q)
	}
	if q.SavingCents != 0 || q.Basis != "" {
		t.Fatalf("'you save -$4.10' is not a thing a counter says: %+v", q)
	}
}

func TestCounterQuote_ASurchargeTierIsNeverOfferedAsAnOpportunity(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 48700, "SF"},
		breaks: []QuantityBreak{{HeaderID: "h1", ProductID: "blu60", ThresholdUOM: "SF", FromQty: 500,
			ValueType: BreakSurchargePct, SurchargeBps: p64(1000), PriceUOM: "SF"}},
	}
	q := counterQuote(t, counterSvc(st), retailInput(10))
	if q.NextBreakQty != 0 {
		t.Fatalf("buying more to be charged more is not an offer: %+v", q)
	}
}

// - the read is READ-ONLY: browsing a screen writes no audit row -

func TestCounterQuotes_WriteNoAuditRows(t *testing.T) {
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 48700, "SF"},
		rules: []Rule{{ID: "r-def", Scope: ScopeTierDefault, CustomerType: "CONTRACTOR", CalcType: CalcPctOffList,
			DiscountBps: p64(1500), PriceUOM: "SF", Priority: 10, IsActive: true}},
		breaks: []QuantityBreak{{HeaderID: "h1", ProductID: "blu60", ThresholdUOM: "SF", FromQty: 200,
			ValueType: BreakPctOff, DiscountBps: p64(2500), PriceUOM: "SF"}},
	}
	s := counterSvc(st)
	if _, err := s.CounterQuotes(context.Background(), []CounterQuoteInput{accountInput(10), accountInput(300)}); err != nil {
		t.Fatalf("CounterQuotes: %v", err)
	}
	if len(st.audits) != 0 {
		t.Fatalf("painting a search page must not append %d pricing_audit_log rows", len(st.audits))
	}
}

// - one bad SKU must never blank a counter screen -

func TestCounterQuotes_AnUnpriceableRowDoesNotFailTheBatch(t *testing.T) {
	// No list price and no cost to derive one from: the waterfall refuses rather than guessing.
	st := &fakeStore{}
	rows, err := counterSvc(st).CounterQuotes(context.Background(), []CounterQuoteInput{retailInput(1), retailInput(2)})
	if err != nil {
		t.Fatalf("a product that cannot be priced is a ROW, not a batch failure: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("every asked product answers: %+v", rows)
	}
	for _, r := range rows {
		if !r.Unpriceable {
			t.Fatalf("an unpriced row says so instead of reporting CA$0.00: %+v", r)
		}
		if r.RetailCents != 0 || r.CustomerCents != 0 {
			t.Fatalf("nothing may be invented for it: %+v", r)
		}
	}
}

// - a zero/absent quantity asks about one unit -

func TestCounterQuote_AbsentQuantityAsksAboutOne(t *testing.T) {
	st := &fakeStore{listPrice: &ListPrice{"blu60", 48700, "SF"}}
	q := counterQuote(t, counterSvc(st), CounterQuoteInput{ProductID: "blu60", UOMEntered: "SF"})
	if q.RetailCents != 48700 {
		t.Fatalf("a tile that knows no quantity still gets a price: %+v", q)
	}
}
