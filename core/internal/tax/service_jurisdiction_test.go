package tax

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The engine binds by the dealer's own jurisdiction. Every leg here builds its own Service and its
// own store: nothing is shared at package level, which is what the two dealer leg proves.

type fakeRegion struct {
	region string
	err    error
}

func (f fakeRegion) TaxRegion(context.Context) (string, error) { return f.region, f.err }

// californiaStore is a store holding the California dealer's rows only: the standard 7.75 percent
// and a manual review row, none of the Ontario ones.
func californiaStore() *fakeStore {
	f := newFakeStore()
	f.rates = map[string]int{
		"CA|" + ReasonStdSales: 775,
		"CA|" + ReasonManual:   775,
	}
	f.rateRows = []Rate{
		{Jurisdiction: "CA", ReasonCode: ReasonStdSales, RateBps: 775, EffectiveFrom: "2000-01-01"},
		{Jurisdiction: "CA", ReasonCode: ReasonManual, RateBps: 775, EffectiveFrom: "2000-01-01"},
	}
	return f
}

func caSvc(f *fakeStore) *Service {
	svc, _ := newSvc(f)
	return svc.WithDealerRegion(fakeRegion{region: "CA"})
}

// Ontario is unchanged and pinned: no region source, an Ontario source and an empty answer all bind
// 13 percent on the Ontario code, with the standard rate carried on the quote.
func TestJurisdiction_OntarioUnchangedAndPinned(t *testing.T) {
	for name, region := range map[string]DealerRegionSource{
		"no source": nil,
		"ON":        fakeRegion{region: "ON"},
		"blank":     fakeRegion{region: ""},
	} {
		svc, _ := newSvc(newFakeStore())
		if region != nil {
			svc.WithDealerRegion(region)
		}
		q, err := svc.AssessSale(context.Background(), SaleAssessment{SubtotalCents: 123_456})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// Pinned figure: 123456 * 13% = 16049.28, half up once = 16049.
		if q.TaxCents != 16_049 || q.RateBps != 1300 || q.ReasonCode != ReasonStdON13 ||
			q.StandardRateBps != 1300 || q.Jurisdiction != "ON" {
			t.Fatalf("%s: %+v", name, q)
		}
	}
}

func TestJurisdiction_CaliforniaBindsSevenSevenFiveThroughTheRing(t *testing.T) {
	svc := caSvc(californiaStore())
	for _, reason := range []string{"", ReasonStdSales, ReasonStdON13} {
		q, err := svc.AssessSale(context.Background(), SaleAssessment{SubtotalCents: 100_000, ReasonCode: reason})
		if err != nil {
			t.Fatalf("reason %q: %v", reason, err)
		}
		if q.TaxCents != 7_750 || q.RateBps != 775 || q.StandardRateBps != 775 ||
			q.ReasonCode != ReasonStdSales || q.Jurisdiction != "CA" {
			t.Fatalf("reason %q: %+v", reason, q)
		}
	}
	// The half cent sale: 100 cents at 7.75 percent is 7.75, rounded half up once to 8.
	q, _ := svc.AssessSale(context.Background(), SaleAssessment{SubtotalCents: 100})
	if q.TaxCents != 8 {
		t.Fatalf("half cent: %d", q.TaxCents)
	}
}

// A relief reason with no row in this jurisdiction falls back to the dealer's own standard, never
// to 13 percent, and a gated relief without evidence binds the dealer's standard too.
func TestJurisdiction_ReliefFallsBackToTheDealersStandard(t *testing.T) {
	svc := caSvc(californiaStore())
	for _, reason := range []string{ReasonONFNPos8, ReasonFNGST5, ReasonFedVendDel} {
		q, err := svc.AssessSale(context.Background(), SaleAssessment{SubtotalCents: 100_000, ReasonCode: reason})
		if err != nil {
			t.Fatalf("%s: %v", reason, err)
		}
		if q.RateBps != 775 || q.TaxCents != 7_750 {
			t.Fatalf("%s: %+v", reason, q)
		}
	}
}

// A dealer with a jurisdiction and no rows for it refuses, never a silent zero and never a quiet
// fall back to the Ontario 13 percent.
func TestJurisdiction_NoRowsRefuses(t *testing.T) {
	svc, _ := newSvc(newFakeStore()) // the Ontario rows only
	svc.WithDealerRegion(fakeRegion{region: "CA"})
	_, err := svc.AssessSale(context.Background(), SaleAssessment{SubtotalCents: 100_000})
	if !errors.Is(err, ErrNoStandardRate) {
		t.Fatalf("want ErrNoStandardRate, got %v", err)
	}
	if err != nil && !strings.Contains(err.Error(), "CA") {
		t.Fatalf("the refusal must name the jurisdiction: %v", err)
	}
}

// A blank area on a rate row lands in the dealer's own jurisdiction, not Ontario.
func TestJurisdiction_BlankAreaRowLandsInTheDealersJurisdiction(t *testing.T) {
	f := californiaStore()
	svc := caSvc(f)
	r, err := svc.CreateRate(context.Background(), Rate{ReasonCode: ReasonStdSales, RateBps: 800, EffectiveFrom: "2026-08-01"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Jurisdiction != "CA" {
		t.Fatalf("blank area bound to %q, want CA", r.Jurisdiction)
	}
	// An Ontario dealer's blank area is still Ontario.
	on, _ := newSvc(newFakeStore())
	r, err = on.CreateRate(context.Background(), Rate{ReasonCode: ReasonStdON13, RateBps: 1300, EffectiveFrom: "2026-08-01"})
	if err != nil || r.Jurisdiction != "ON" {
		t.Fatalf("ontario blank area: %+v %v", r, err)
	}
}

// A standard row must carry its jurisdiction's own standard reason.
func TestJurisdiction_StandardRowsCarryTheirOwnReason(t *testing.T) {
	svc := caSvc(californiaStore())
	if _, err := svc.CreateRate(context.Background(), Rate{Jurisdiction: "CA", ReasonCode: ReasonStdON13, RateBps: 775}); err == nil {
		t.Fatal("an Ontario standard code was planted in California")
	}
	on, _ := newSvc(newFakeStore())
	if _, err := on.CreateRate(context.Background(), Rate{Jurisdiction: "ON", ReasonCode: ReasonStdSales, RateBps: 1300}); err == nil {
		t.Fatal("the neutral standard code was planted in Ontario")
	}
}

// The ungated-below-standard guard measures against the dealer's own standard.
func TestJurisdiction_UngatedBelowStandardIsMeasuredAgainstTheDealersStandard(t *testing.T) {
	svc := caSvc(californiaStore())
	// 700 is below California's 775 and ungated: refused.
	_, err := svc.CreateRate(context.Background(), Rate{Jurisdiction: "CA", ReasonCode: ReasonFNTProduct, RateBps: 700})
	if err == nil {
		t.Fatal("an ungated reason below the dealer's standard was planted")
	}
	// 800 is above California's 775 (and far below Ontario's 13 percent): allowed.
	if _, err := svc.CreateRate(context.Background(), Rate{Jurisdiction: "CA", ReasonCode: ReasonFNTProduct, RateBps: 800}); err != nil {
		t.Fatalf("a row above the dealer's standard was refused: %v", err)
	}
}

// The profile rule compares against the jurisdiction's standard rate, not a literal 13 percent.
func TestJurisdiction_ProfileRuleReadsTheJurisdictionsStandard(t *testing.T) {
	svc := caSvc(californiaStore())
	// 800 bps is below Ontario's 13 percent but above California's 7.75: no forced number needed.
	if _, err := svc.UpsertProfile(context.Background(), StatusCardProfile{Code: "CA_P", Name: "p", RateBps: 800}); err != nil {
		t.Fatalf("a profile at the dealer's standard or above was refused: %v", err)
	}
	if _, err := svc.UpsertProfile(context.Background(), StatusCardProfile{Code: "CA_Q", Name: "q", RateBps: 500}); err == nil {
		t.Fatal("a relief profile below the dealer's standard must force the number")
	}
	// Ontario is unchanged: 800 bps without a number is still refused.
	on, _ := newSvc(newFakeStore())
	if _, err := on.UpsertProfile(context.Background(), StatusCardProfile{Code: "ON_P", Name: "p", RateBps: 800}); err == nil {
		t.Fatal("ontario profile rule moved")
	}
}

// Two dealers in one process: neither leaks into the other.
func TestJurisdiction_TwoDealersInOneProcessDoNotLeak(t *testing.T) {
	ontario, _ := newSvc(newFakeStore())
	ontario.WithDealerRegion(fakeRegion{region: "ON"})
	california := caSvc(californiaStore())
	for i := 0; i < 3; i++ {
		a, err := ontario.AssessSale(context.Background(), SaleAssessment{SubtotalCents: 100_000})
		if err != nil || a.RateBps != 1300 || a.ReasonCode != ReasonStdON13 {
			t.Fatalf("ontario after a california ring: %+v %v", a, err)
		}
		b, err := california.AssessSale(context.Background(), SaleAssessment{SubtotalCents: 100_000})
		if err != nil || b.RateBps != 775 || b.ReasonCode != ReasonStdSales {
			t.Fatalf("california after an ontario ring: %+v %v", b, err)
		}
	}
}

// The configuration read is live: the next ring after a change binds the new jurisdiction.
func TestJurisdiction_TheNextRingFollowsTheConfiguration(t *testing.T) {
	f := newFakeStore()
	for k, v := range californiaStore().rates {
		f.rates[k] = v
	}
	svc, _ := newSvc(f)
	src := &fakeRegion{region: "ON"}
	svc.WithDealerRegion(regionFunc(func() (string, error) { return src.region, src.err }))
	if q, _ := svc.AssessSale(context.Background(), SaleAssessment{SubtotalCents: 100_000}); q.RateBps != 1300 {
		t.Fatalf("before: %+v", q)
	}
	src.region = "CA"
	if q, _ := svc.AssessSale(context.Background(), SaleAssessment{SubtotalCents: 100_000}); q.RateBps != 775 {
		t.Fatalf("after: %+v", q)
	}
}

type regionFunc func() (string, error)

func (f regionFunc) TaxRegion(context.Context) (string, error) { return f() }

// A failing configuration read refuses the ring; it never guesses Ontario for a dealer that is not.
func TestJurisdiction_ConfigReadFailureRefuses(t *testing.T) {
	svc, _ := newSvc(newFakeStore())
	svc.WithDealerRegion(fakeRegion{err: errors.New("boom")})
	if _, err := svc.AssessSale(context.Background(), SaleAssessment{SubtotalCents: 100}); err == nil {
		t.Fatal("a failed configuration read was answered as Ontario")
	}
}

// The recompute divides the snapshot's full HST by the standard rate the row was WRITTEN under.
func TestJurisdiction_ReconstructBaseUsesTheSnapshotsOwnStandardRate(t *testing.T) {
	// 775 bps on a 100.00 base is 7.75 -> 8 cents at write; reading it back at the dealer's current
	// standard (say it moved to 8 percent) must not re-price the row.
	src := Snapshot{FullHSTCents: 775, StandardRateBps: 775}
	if got := reconstructBase(src); got != 10_000 {
		t.Fatalf("base from a 775 row: %d", got)
	}
	// Rows written before the column existed carry no standard rate: they are Ontario at 13 percent.
	if got := reconstructBase(Snapshot{FullHSTCents: 1300}); got != 10_000 {
		t.Fatalf("base from a legacy row: %d", got)
	}
}

func TestJurisdiction_RecomputeKeepsTheDealersStandardRateOnTheNewRow(t *testing.T) {
	f := californiaStore()
	f.w4 = []Snapshot{{
		OrderID: "order-ca", ReasonCode: ReasonStdSales, RuleVersion: ruleVersionW4,
		RateBps: 775, StandardRateBps: 775, FullHSTCents: 775, NetCollectedCents: 775,
	}}
	svc := caSvc(f)
	if _, err := svc.RecomputeReport(context.Background(), "2026-01-01", "2026-12-31"); err != nil {
		t.Fatal(err)
	}
	got := f.inserted[0]
	if got.RateBps != 775 || got.StandardRateBps != 775 || got.FullHSTCents != 775 || got.ReasonCode != ReasonStdSales {
		t.Fatalf("recomputed row: %+v", got)
	}
}

// The admin edge normalises the jurisdiction's case as the engine does for the dealer's region: a row
// posted as "ca" lands in CA, and "on" with the Ontario standard reason is accepted.
func TestJurisdiction_CreateRateNormalisesTheJurisdictionCase(t *testing.T) {
	svc := caSvc(californiaStore())
	r, err := svc.CreateRate(context.Background(), Rate{Jurisdiction: " ca ", ReasonCode: ReasonStdSales, RateBps: 800, EffectiveFrom: "2026-08-01"})
	if err != nil || r.Jurisdiction != "CA" {
		t.Fatalf("lower case california: %+v %v", r, err)
	}
	on, _ := newSvc(newFakeStore())
	r, err = on.CreateRate(context.Background(), Rate{Jurisdiction: "on", ReasonCode: ReasonStdON13, RateBps: 1300, EffectiveFrom: "2026-08-01"})
	if err != nil || r.Jurisdiction != "ON" {
		t.Fatalf("lower case ontario: %+v %v", r, err)
	}
}
