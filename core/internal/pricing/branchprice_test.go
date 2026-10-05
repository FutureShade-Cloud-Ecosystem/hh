package pricing

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// branchprice_test.go: the LD-7 pure legs. The resolver's anchor becomes
// branch aware (the branch row at the line's branch when one exists, else the
// dealer-wide anchor) and every rung that prices off the anchor follows it;
// the editor's set/delete validation runs before the store. Integration legs
// (real schema, real trail, pool four) live in branchprice_integration_test.go.

// bpStore builds a fakeStore whose branch map answers product+"|"+branch.
func bpStore(dealerWide *ListPrice, branch map[string]*ListPrice) *fakeStore {
	return &fakeStore{listPrice: dealerWide, branchPrices: branch}
}

// TestResolveBranchPriceWinsAtTheLinesBranch: with a branch row loaded, the
// anchor at that branch IS the branch row, not the dealer-wide anchor.
func TestResolveBranchPriceWinsAtTheLinesBranch(t *testing.T) {
	st := bpStore(
		&ListPrice{ProductID: "blu60", ListPriceCents: 10000, PriceUOM: "EA"},
		map[string]*ListPrice{"blu60|branchA": {ProductID: "blu60", ListPriceCents: 48000, PriceUOM: "SF"}},
	)
	bd := resolve(t, newSvc(st, "pavers"), base(ResolveInput{
		CustomerType: "RETAIL", QtyEntered: 2, UOMEntered: "SF", BranchID: "branchA",
	}))
	if bd.Source != SourceList {
		t.Fatalf("source %q, want LIST", bd.Source)
	}
	if bd.OriginalCents != 48000 || bd.FinalCents != 48000 {
		t.Fatalf("anchor %d/%d, want the branch row's 48000, not the dealer-wide 10000",
			bd.OriginalCents, bd.FinalCents)
	}
	if bd.Detail != "Branch list price" {
		t.Fatalf("detail %q, want the branch anchor named", bd.Detail)
	}
}

// TestResolveFallsBackToDealerWideWhenNoBranchRow: another branch (no row)
// answers exactly the dealer-wide price, and the detail stays "List price".
func TestResolveFallsBackToDealerWideWhenNoBranchRow(t *testing.T) {
	st := bpStore(
		&ListPrice{ProductID: "blu60", ListPriceCents: 10000, PriceUOM: "EA"},
		map[string]*ListPrice{"blu60|branchA": {ProductID: "blu60", ListPriceCents: 48000, PriceUOM: "SF"}},
	)
	atBranch := base(ResolveInput{CustomerType: "RETAIL", QtyEntered: 1, UOMEntered: "EA", BranchID: "branchB"})
	bd := resolve(t, newSvc(st, "pavers"), atBranch)
	if bd.Source != SourceList || bd.OriginalCents != 10000 || bd.FinalCents != 10000 {
		t.Fatalf("branch B answered %+v, want the dealer-wide 10000", bd)
	}
	if bd.Detail != "List price" {
		t.Fatalf("detail %q, want the plain list detail when no branch row stands", bd.Detail)
	}
	// And the identical input with NO branch id at all agrees to the cent.
	bdNoBranch := resolve(t, newSvc(st, "pavers"), base(ResolveInput{
		CustomerType: "RETAIL", QtyEntered: 1, UOMEntered: "EA",
	}))
	if bdNoBranch.FinalCents != bd.FinalCents || bdNoBranch.OriginalCents != bd.OriginalCents {
		t.Fatalf("a branch-less resolve answered %+v, want the same as the missing-branch resolve %+v", bdNoBranch, bd)
	}
}

// TestResolveBranchPriceInAnotherUnitConverts: a branch price denominated per
// SF prices a line entered in EA through the same conversion the dealer-wide
// anchor uses (factor SF 4 per 1 EA here).
func TestResolveBranchPriceInAnotherUnitConverts(t *testing.T) {
	st := bpStore(
		&ListPrice{ProductID: "blu60", ListPriceCents: 10000, PriceUOM: "EA"},
		map[string]*ListPrice{"blu60|branchA": {ProductID: "blu60", ListPriceCents: 4800, PriceUOM: "SF"}},
	)
	svc := NewService(st).
		WithUOMConverter(fakeUOM{map[string]float64{"EA": 1, "SF": 4}}).
		WithMarginGate(NewIdentityMarginGate())
	bd := resolve(t, svc, base(ResolveInput{
		CustomerType: "RETAIL", QtyEntered: 1, UOMEntered: "EA", BranchID: "branchA",
	}))
	if bd.Source != SourceList || bd.PriceUOM != "SF" {
		t.Fatalf("answer %+v, want LIST in the branch price's own unit SF", bd)
	}
	// The unit set reads 1 SF as 4 base units, so one EA entered is a
	// quarter SF: the unit price stays 4800 per SF and the line totals 1200.
	if bd.FinalCents != 4800 {
		t.Fatalf("resolved price %d, want 4800 per SF", bd.FinalCents)
	}
	if bd.LineTotalCents != 1200 {
		t.Fatalf("line total %d, want 1200 for one EA", bd.LineTotalCents)
	}
}

// TestTierRuleComputesOffTheBranchAnchor: the tier-category percent rule
// discounts the branch anchor, exactly as it discounts the dealer-wide one.
func TestTierRuleComputesOffTheBranchAnchor(t *testing.T) {
	st := bpStore(
		&ListPrice{ProductID: "blu60", ListPriceCents: 10000, PriceUOM: "EA"},
		map[string]*ListPrice{"blu60|branchA": {ProductID: "blu60", ListPriceCents: 48000, PriceUOM: "SF"}},
	)
	st.rules = []Rule{{
		ID: "r1", Scope: ScopeTierCategory, CustomerType: "CONTRACTOR", CategoryID: "pavers",
		CalcType: CalcPctOffList, DiscountBps: p64(2000), PriceUOM: "SF", IsActive: true,
	}}
	bd := resolve(t, newSvc(st, "pavers"), base(ResolveInput{
		CustomerType: "CONTRACTOR", QtyEntered: 1, UOMEntered: "SF", BranchID: "branchA",
	}))
	if bd.Source != SourceTierCategory {
		t.Fatalf("source %q, want TIER_CATEGORY", bd.Source)
	}
	// 20% off the branch anchor 48000, not off the dealer-wide 10000.
	if bd.OriginalCents != 48000 || bd.FinalCents != 38400 {
		t.Fatalf("anchor %d final %d, want 48000 then 38400 off the branch anchor", bd.OriginalCents, bd.FinalCents)
	}
	if bd.DiscountBps != 2000 {
		t.Fatalf("discount bps %d, want 2000", bd.DiscountBps)
	}
}

// TestQuantityBreakCompetesOffTheBranchAnchor: the break prices its single
// figure off the branch anchor and replaces when strictly cheaper.
func TestQuantityBreakCompetesOffTheBranchAnchor(t *testing.T) {
	st := bpStore(
		&ListPrice{ProductID: "blu60", ListPriceCents: 10000, PriceUOM: "EA"},
		map[string]*ListPrice{"blu60|branchA": {ProductID: "blu60", ListPriceCents: 48000, PriceUOM: "SF"}},
	)
	st.breaks = []QuantityBreak{{
		HeaderID: "h1", ItemID: "i1", Name: "full skid", ThresholdUOM: "SF",
		FromQty: 10, ValueType: BreakPctOff, DiscountBps: p64(1000), PriceUOM: "SF",
	}}
	bd := resolve(t, newSvc(st, "pavers"), base(ResolveInput{
		CustomerType: "RETAIL", QtyEntered: 12, UOMEntered: "SF", BranchID: "branchA",
	}))
	if bd.Source != SourceQuantityBreak {
		t.Fatalf("source %q, want QUANTITY_BREAK", bd.Source)
	}
	// 10% off the branch anchor 48000 = 43200, cheaper than the LIST 48000.
	if bd.FinalCents != 43200 {
		t.Fatalf("break priced %d, want 43200 off the branch anchor", bd.FinalCents)
	}
}

// TestPromotionCompetesOffTheBranchAnchor: the promotion's percent comes off
// the branch anchor (the shelf price the sign in the yard speaks about), and
// the never-worse guard compares against the price the branch waterfall made.
func TestPromotionCompetesOffTheBranchAnchor(t *testing.T) {
	st := bpStore(
		&ListPrice{ProductID: "blu60", ListPriceCents: 10000, PriceUOM: "EA"},
		map[string]*ListPrice{"blu60|branchA": {ProductID: "blu60", ListPriceCents: 48000, PriceUOM: "SF"}},
	)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	svc := newSvc(st, "pavers").WithPromoStore(&fakePromoStore{
		promos: []Promotion{{
			ID: "p1", Code: "JUNE", Name: "sale", CalcType: PromoPctOff, DiscountBps: p64(3000),
			PriceUOM: "SF", StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour), IsActive: true,
		}},
	}).withClock(func() time.Time { return now })
	bd := resolve(t, svc, base(ResolveInput{
		CustomerType: "RETAIL", QtyEntered: 1, UOMEntered: "SF", BranchID: "branchA",
	}))
	if bd.Source != SourcePromotion {
		t.Fatalf("source %q, want PROMOTION", bd.Source)
	}
	// 30% off the branch anchor 48000 = 33600.
	if bd.FinalCents != 33600 {
		t.Fatalf("promo priced %d, want 33600 off the branch anchor 48000", bd.FinalCents)
	}
	if bd.PromoCode != "JUNE" {
		t.Fatalf("promo code %q, want JUNE", bd.PromoCode)
	}
}

// TestExistingResolveCasesUnchangedWithTheBranchSeam: with NO branch price
// loaded for the line's branch, every existing resolve answers exactly as
// before the branch step existed. The table walks the rungs; each case runs
// with a branch id whose read misses and must equal the recorded answer.
func TestExistingResolveCasesUnchangedWithTheBranchSeam(t *testing.T) {
	st := bpStore(
		&ListPrice{ProductID: "blu60", ListPriceCents: 48700, PriceUOM: "SF"},
		nil,
	)
	st.rules = []Rule{
		{ID: "r1", Scope: ScopeTierDefault, CustomerType: "CONTRACTOR", CalcType: CalcPctOffList,
			DiscountBps: p64(1800), PriceUOM: "SF", IsActive: true},
		// The contract price belongs to a different customer, so the
		// override customer keeps an override-shaped waterfall.
		{ID: "r2", Scope: ScopeCustomerProduct, CustomerID: "spare", ProductID: "blu60",
			CalcType: CalcAbsolute, PriceCents: p64(2500), PriceUOM: "SF", IsActive: true},
	}
	st.override = &Override{ID: "o1", CustomerID: "abc", CategoryID: "pavers",
		CalcType: CalcPctOffList, DiscountBps: p64(1000), PriceUOM: "SF"}
	svc := newSvc(st, "pavers")

	inList := ResolveInput{CustomerType: "RETAIL", ProductID: "blu60", QtyEntered: 1, UOMEntered: "SF", BranchID: "branchA"}
	inTier := ResolveInput{CustomerType: "CONTRACTOR", ProductID: "blu60", QtyEntered: 1, UOMEntered: "SF", BranchID: "branchA"}
	inContract := ResolveInput{CustomerType: "RETAIL", CustomerID: "spare", ProductID: "blu60", QtyEntered: 1, UOMEntered: "SF", BranchID: "branchA"}
	inOverride := ResolveInput{CustomerType: "CONTRACTOR", CustomerID: "abc", ProductID: "blu60", QtyEntered: 1, UOMEntered: "SF", BranchID: "branchA"}
	cases := []struct {
		name string
		in   ResolveInput
		want struct {
			source string
			cents  int64
		}
	}{
		{"walk in retail is LIST", inList,
			struct {
				source string
				cents  int64
			}{SourceList, 48700}},
		{"contractor tier default is 18 percent off", inTier,
			struct {
				source string
				cents  int64
			}{SourceTierDefault, 39934}},
		{"contract price is final", inContract,
			struct {
				source string
				cents  int64
			}{SourceCustomerProduct, 2500}},
		{"override beats the tier", inOverride,
			struct {
				source string
				cents  int64
			}{SourceCustomerOverride, 43830}},
	}
	for _, c := range cases {
		bd := resolve(t, svc, c.in)
		if bd.Source != c.want.source || bd.FinalCents != c.want.cents {
			t.Fatalf("%s: answered %s %d, want %s %d (the pre-LD-7 answer)",
				c.name, bd.Source, bd.FinalCents, c.want.source, c.want.cents)
		}
	}
}

// TestPutBranchPriceRefusesNegativeCents: money is integer cents and never
// negative; the refusal names the field, never a value.
func TestPutBranchPriceRefusesNegativeCents(t *testing.T) {
	st := bpStore(nil, nil)
	_, err := NewService(st).PutBranchPrice(context.Background(), "p1", "branchA",
		BranchPriceWrite{ListPriceCents: -1, PriceUOM: "SF"}, "user-1")
	if err == nil {
		t.Fatal("a negative branch price must refuse")
	}
	var up unprocessableError
	if !errors.As(err, &up) {
		t.Fatalf("err %v, want a 422 unprocessable refusal", err)
	}
	if !strings.Contains(up.msg, "list_price_cents") {
		t.Fatalf("refusal %q must name the field", up.msg)
	}
}

// TestPutBranchPriceRefusesMissingUnit: a price with no unit is not a price.
func TestPutBranchPriceRefusesMissingUnit(t *testing.T) {
	st := bpStore(nil, nil)
	_, err := NewService(st).PutBranchPrice(context.Background(), "p1", "branchA",
		BranchPriceWrite{ListPriceCents: 100, PriceUOM: ""}, "user-1")
	if err == nil {
		t.Fatal("a branch price with no unit must refuse")
	}
	var br badRequestError
	if !errors.As(err, &br) {
		t.Fatalf("err %v, want a 400 bad request", err)
	}
}

// TestPutBranchPriceRequiresProductAndBranch: both key parts are required.
func TestPutBranchPriceRequiresProductAndBranch(t *testing.T) {
	st := bpStore(nil, nil)
	svc := NewService(st)
	if _, err := svc.PutBranchPrice(context.Background(), "", "branchA",
		BranchPriceWrite{ListPriceCents: 100, PriceUOM: "SF"}, "u"); err == nil {
		t.Fatal("a put with no product must refuse")
	}
	if _, err := svc.PutBranchPrice(context.Background(), "p1", "",
		BranchPriceWrite{ListPriceCents: 100, PriceUOM: "SF"}, "u"); err == nil {
		t.Fatal("a put with no branch must refuse")
	}
}

// TestPutBranchPriceRefusesAUnitTheProductCannotConvert: a branch price per a
// unit outside the product's set would strand every resolve of that product at
// that branch (the resolver prices through the same conversion the unit set
// holds), so the set refuses before the store is touched, naming the field.
func TestPutBranchPriceRefusesAUnitTheProductCannotConvert(t *testing.T) {
	st := bpStore(nil, map[string]*ListPrice{})
	_, err := newSvc(st, "pavers").PutBranchPrice(context.Background(), "p1", "branchA",
		BranchPriceWrite{ListPriceCents: 100, PriceUOM: "BOX"}, "user-1")
	if err == nil {
		t.Fatal("a branch price per an unconvertible unit must refuse")
	}
	var up unprocessableError
	if !errors.As(err, &up) {
		t.Fatalf("err %v, want a 422 unprocessable refusal", err)
	}
	if !strings.Contains(up.msg, "price_uom") {
		t.Fatalf("refusal %q must name the field", up.msg)
	}
	if len(st.branchPrices) != 0 {
		t.Fatalf("the refusing set reached the store anyway: %v", st.branchPrices)
	}
	// A unit the set holds still sets.
	if _, err := newSvc(st, "pavers").PutBranchPrice(context.Background(), "p1", "branchA",
		BranchPriceWrite{ListPriceCents: 100, PriceUOM: "SF"}, "user-1"); err != nil {
		t.Fatalf("a convertible unit must set: %v", err)
	}
}

// TestPutBranchPriceStoresAndAnswers: the editor's set lands on the store and
// answers the stored row.
func TestPutBranchPriceStoresAndAnswers(t *testing.T) {
	st := bpStore(nil, nil)
	out, err := NewService(st).PutBranchPrice(context.Background(), "p1", "branchA",
		BranchPriceWrite{ListPriceCents: 48000, PriceUOM: "SF"}, "user-1")
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if out.ListPriceCents != 48000 || out.PriceUOM != "SF" || out.BranchID != "branchA" {
		t.Fatalf("put answered %+v, want the stored row", out)
	}
	got, err := st.BranchListPrice(context.Background(), "p1", "branchA")
	if err != nil || got == nil || got.ListPriceCents != 48000 {
		t.Fatalf("read back %+v err %v, want the stored row", got, err)
	}
}

// TestDeleteBranchPriceMissingRowIsNotFound: deleting a row that is not there
// is the 404, never a silent success.
func TestDeleteBranchPriceMissingRowIsNotFound(t *testing.T) {
	st := bpStore(nil, nil)
	err := NewService(st).DeleteBranchPrice(context.Background(), "p1", "branchA", "user-1")
	if !errors.Is(err, errNotFound) {
		t.Fatalf("err %v, want errNotFound", err)
	}
}

// TestDeleteBranchPriceRemovesTheAnchor: after a delete the resolver at that
// branch answers the dealer-wide anchor again.
func TestDeleteBranchPriceRemovesTheAnchor(t *testing.T) {
	st := bpStore(
		&ListPrice{ProductID: "blu60", ListPriceCents: 10000, PriceUOM: "EA"},
		map[string]*ListPrice{"blu60|branchA": {ProductID: "blu60", ListPriceCents: 48000, PriceUOM: "SF"}},
	)
	svc := NewService(st)
	if err := svc.DeleteBranchPrice(context.Background(), "blu60", "branchA", "user-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	bd := resolve(t, svc, base(ResolveInput{CustomerType: "RETAIL", QtyEntered: 1, UOMEntered: "EA", BranchID: "branchA"}))
	if bd.FinalCents != 10000 {
		t.Fatalf("after the delete the branch answered %d, want the dealer-wide 10000", bd.FinalCents)
	}
}
