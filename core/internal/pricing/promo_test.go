package pricing

// promo_test.go, DIB-70. The DATE BOUNDARIES, the branch scope, and the two refusal paths.
//
// Boundary tests are separated from the precedence table (waterfall_test.go) on purpose: a promo
// that stops applying is a different failure from a promo that applies at the wrong price, and
// mixing them makes a red build ambiguous.

import (
	"context"
	"strings"
	"testing"
	"time"
)

// promoAt builds a resolver whose clock reads exactly `now`, so each boundary case is an assertion
// about ONE instant rather than about elapsed real time.
func promoAt(st *fakeStore, ps *fakePromoStore, now time.Time) *Service {
	return newSvc(st, "pavers").WithPromoStore(ps).withClock(func() time.Time { return now })
}

func listOnly() *fakeStore {
	return &fakeStore{listPrice: &ListPrice{"blu60", 10000, "EA"}}
}

func retailLine() ResolveInput {
	return base(ResolveInput{CustomerType: "RETAIL", QtyEntered: 1, UOMEntered: "EA"})
}

// - the window is HALF-OPEN: [starts_at, ends_at) -

func TestPromo_DateBoundaries(t *testing.T) {
	// The window is 2026-06-01T00:00:00Z .. 2026-07-01T00:00:00Z.
	//
	// HALF-OPEN is the ruling (Promotion.withinWindow). The alternative, inclusive at both ends -
	// makes a June promo and a July promo BOTH live at 2026-07-01T00:00:00.000Z, and which one
	// wins that instant depends on the candidate sort. That is a bug that fires for one millisecond
	// a month, never reproduces in a test that calls time.Now(), and shows up as a single
	// mispriced line in a monthly report.
	cases := []struct {
		name      string
		at        time.Time
		wantPromo bool
		wantCents int64
	}{
		{"one nanosecond before the start: NOT live", juneStart.Add(-time.Nanosecond), false, 10000},
		{"exactly at the start: LIVE (inclusive)", juneStart, true, 8000},
		{"mid-window: LIVE", fixedNow, true, 8000},
		{"one nanosecond before the end: LIVE", juneEnd.Add(-time.Nanosecond), true, 8000},
		{"exactly at the end: NOT live (exclusive)", juneEnd, false, 10000},
		{"a month after the end: NOT live", juneEnd.AddDate(0, 1, 0), false, 10000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ps := &fakePromoStore{promos: []Promotion{pctPromo("p-june", 2000)}}
			bd := resolve(t, promoAt(listOnly(), ps, c.at), retailLine())
			gotPromo := bd.Source == SourcePromotion
			if gotPromo != c.wantPromo {
				t.Fatalf("promo live = %v, want %v (source %s)", gotPromo, c.wantPromo, bd.Source)
			}
			if bd.FinalCents != c.wantCents {
				t.Fatalf("price = %d, want %d", bd.FinalCents, c.wantCents)
			}
		})
	}
}

func TestPromo_InactiveRowNeverApplies(t *testing.T) {
	// A retired promo is dead even inside its own window. The repository's ActivePromotions never
	// returns one, but the Go-side guard is what makes that a property of the RULING rather than of
	// one WHERE clause, a future caller that reads promos a different way still cannot resurrect
	// a retired sale.
	p := pctPromo("p-june", 2000)
	p.IsActive = false
	ps := &fakePromoStore{promos: []Promotion{p}}
	bd := resolve(t, promoAt(listOnly(), ps, fixedNow), retailLine())
	if bd.Source == SourcePromotion || bd.FinalCents != 10000 {
		t.Fatalf("a retired promo must not price anything, got %s/%d", bd.Source, bd.FinalCents)
	}
}

// - branch scope -

func TestPromo_BranchScope(t *testing.T) {
	const kgn, trt = "b-kgn", "b-trt"

	pinned := pctPromo("p-kgn", 2000)
	pinned.BranchID = kgn

	t.Run("applies at the branch it is pinned to", func(t *testing.T) {
		ps := &fakePromoStore{promos: []Promotion{pinned}}
		in := retailLine()
		in.BranchID = kgn
		bd := resolve(t, promoAt(listOnly(), ps, fixedNow), in)
		if bd.FinalCents != 8000 {
			t.Fatalf("want 8000 at the pinned branch, got %d", bd.FinalCents)
		}
	})

	t.Run("does not apply at another branch", func(t *testing.T) {
		ps := &fakePromoStore{promos: []Promotion{pinned}}
		in := retailLine()
		in.BranchID = trt
		bd := resolve(t, promoAt(listOnly(), ps, fixedNow), in)
		if bd.FinalCents != 10000 {
			t.Fatalf("a Kingston-only sale must not price a Trenton line, got %d", bd.FinalCents)
		}
	})

	t.Run("a resolve with NO branch context sees only unpinned promos", func(t *testing.T) {
		// The conservative reading, and the one that matters: an empty BranchID means "we do not
		// know where this is" (the low-margin preview, procurement's indicative pricing), NOT
		// "everywhere". Treating unknown as everywhere would leak a one-yard clearance into a quote
		// written at the other yard.
		ps := &fakePromoStore{promos: []Promotion{pinned}}
		bd := resolve(t, promoAt(listOnly(), ps, fixedNow), retailLine())
		if bd.FinalCents != 10000 {
			t.Fatalf("an unknown branch must not inherit a branch-pinned promo, got %d", bd.FinalCents)
		}
	})

	t.Run("an unpinned promo runs everywhere", func(t *testing.T) {
		ps := &fakePromoStore{promos: []Promotion{pctPromo("p-all", 2000)}}
		in := retailLine()
		in.BranchID = trt
		bd := resolve(t, promoAt(listOnly(), ps, fixedNow), in)
		if bd.FinalCents != 8000 {
			t.Fatalf("an unpinned promo applies at every branch, got %d", bd.FinalCents)
		}
	})
}

// - the two calc shapes that are not a percentage -

func TestPromo_AmountOffAndFixedPrice(t *testing.T) {
	t.Run("AMT_OFF takes cents off the anchor", func(t *testing.T) {
		p := pctPromo("p-amt", 0)
		p.CalcType, p.DiscountBps, p.DiscountCents = PromoAmtOff, nil, p64(1500)
		ps := &fakePromoStore{promos: []Promotion{p}}
		bd := resolve(t, promoAt(listOnly(), ps, fixedNow), retailLine())
		if bd.FinalCents != 8500 {
			t.Fatalf("10000 − 1500 = 8500, got %d", bd.FinalCents)
		}
	})

	t.Run("AMT_OFF never drives the price negative", func(t *testing.T) {
		// Same floor as the ABS_OFF quantity break. A negative unit price would propagate into a
		// negative line total and out into an invoice as a credit nobody authorised.
		p := pctPromo("p-amt", 0)
		p.CalcType, p.DiscountBps, p.DiscountCents = PromoAmtOff, nil, p64(999999)
		ps := &fakePromoStore{promos: []Promotion{p}}
		bd := resolve(t, promoAt(listOnly(), ps, fixedNow), retailLine())
		if bd.FinalCents != 0 {
			t.Fatalf("an over-large amount-off floors at 0, got %d", bd.FinalCents)
		}
	})

	t.Run("FIXED_PRICE in the line's own UOM applies", func(t *testing.T) {
		p := pctPromo("p-fix", 0)
		p.CalcType, p.DiscountBps, p.PriceCents, p.PriceUOM = PromoFixedPrice, nil, p64(6000), "EA"
		ps := &fakePromoStore{promos: []Promotion{p}}
		bd := resolve(t, promoAt(listOnly(), ps, fixedNow), retailLine())
		if bd.FinalCents != 6000 {
			t.Fatalf("a fixed sale price replaces the base, got %d", bd.FinalCents)
		}
	})

	t.Run("FIXED_PRICE in a DIFFERENT UOM is refused, not converted", func(t *testing.T) {
		// The dangerous case. A promo authored as "$60 per PLT" pasted onto a line resolving in EA
		// is a 120× error at the counter, and the conversion that would fix it needs a factor the
		// promo author never supplied. Refusing leaves the ordinary price standing and says why;
		// a wrong price at the till is unrecoverable, a missing sale price is a phone call.
		p := pctPromo("p-fix", 0)
		p.CalcType, p.DiscountBps, p.PriceCents, p.PriceUOM = PromoFixedPrice, nil, p64(6000), "PLT"
		ps := &fakePromoStore{promos: []Promotion{p}}
		bd := resolve(t, promoAt(listOnly(), ps, fixedNow), retailLine())
		if bd.FinalCents != 10000 {
			t.Fatalf("a UOM-mismatched fixed price must NOT apply, got %d", bd.FinalCents)
		}
		if !strings.Contains(bd.Detail, "priced per PLT") {
			t.Fatalf("the breakdown must name both units so the promo can be fixed, got %q", bd.Detail)
		}
	})

	t.Run("FIXED_PRICE matches its UOM case-insensitively", func(t *testing.T) {
		// uoms.code is CITEXT: 'ea' and 'EA' are the same unit in the database. Reading them as a
		// mismatch would silently drop a correctly-authored promo through the refusal path above -
		// the worst kind of bug, because the refusal note makes it look deliberate.
		p := pctPromo("p-fix", 0)
		p.CalcType, p.DiscountBps, p.PriceCents, p.PriceUOM = PromoFixedPrice, nil, p64(6000), "ea"
		ps := &fakePromoStore{promos: []Promotion{p}}
		bd := resolve(t, promoAt(listOnly(), ps, fixedNow), retailLine())
		if bd.FinalCents != 6000 {
			t.Fatalf("'ea' and 'EA' are the same unit, got %d", bd.FinalCents)
		}
	})
}

// - clearance windows -

func TestPromo_ClearanceWindow(t *testing.T) {
	ends := juneEnd
	cases := []struct {
		name      string
		at        time.Time
		item      ClearanceItem
		wantBadge bool
		wantCents int64
	}{
		{
			name:      "open-ended clearance is live indefinitely",
			at:        juneEnd.AddDate(1, 0, 0),
			item:      ClearanceItem{ProductID: "blu60", ClearancePriceCents: p64(6000), PriceUOM: "EA", StartedAt: juneStart, IsActive: true},
			wantBadge: true, wantCents: 6000,
		},
		{
			name:      "before it starts: neither badge nor price",
			at:        juneStart.Add(-time.Hour),
			item:      ClearanceItem{ProductID: "blu60", ClearancePriceCents: p64(6000), PriceUOM: "EA", StartedAt: juneStart, IsActive: true},
			wantBadge: false, wantCents: 10000,
		},
		{
			name:      "at its end instant: over (exclusive, like a promo)",
			at:        juneEnd,
			item:      ClearanceItem{ProductID: "blu60", ClearancePriceCents: p64(6000), PriceUOM: "EA", StartedAt: juneStart, EndsAt: &ends, IsActive: true},
			wantBadge: false, wantCents: 10000,
		},
		{
			name:      "retired row: nothing, even mid-window",
			at:        fixedNow,
			item:      ClearanceItem{ProductID: "blu60", ClearancePriceCents: p64(6000), PriceUOM: "EA", StartedAt: juneStart, IsActive: false},
			wantBadge: false, wantCents: 10000,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			item := c.item
			ps := &fakePromoStore{clearance: &item}
			bd := resolve(t, promoAt(listOnly(), ps, c.at), retailLine())
			if bd.Clearance != c.wantBadge {
				t.Fatalf("badge = %v, want %v", bd.Clearance, c.wantBadge)
			}
			if bd.FinalCents != c.wantCents {
				t.Fatalf("price = %d, want %d", bd.FinalCents, c.wantCents)
			}
		})
	}
}

// - validation -

func TestValidatePromotion_RejectsUnboundedAndMalformed(t *testing.T) {
	ok := Promotion{Code: "P", Name: "P", StartsAt: juneStart, EndsAt: juneEnd, CalcType: PromoPctOff, DiscountBps: p64(1000)}
	if err := validatePromotion(ok); err != nil {
		t.Fatalf("a well-formed promo must validate, got %v", err)
	}

	cases := []struct {
		name string
		mut  func(p *Promotion)
		want string
	}{
		{"no end date", func(p *Promotion) { p.EndsAt = time.Time{} }, "not a promotion"},
		{"end before start", func(p *Promotion) { p.EndsAt = juneStart.Add(-time.Hour) }, "after starts_at"},
		{"zero-length window", func(p *Promotion) { p.EndsAt = p.StartsAt }, "after starts_at"},
		{"unknown calc type", func(p *Promotion) { p.CalcType = "BOGO" }, "calc_type must be"},
		{"pct with no bps", func(p *Promotion) { p.DiscountBps = nil }, "requires discount_bps"},
		{"bps over 100%", func(p *Promotion) { p.DiscountBps = p64(10001) }, "discount_bps"},
		{"product AND category", func(p *Promotion) { p.ProductID, p.CategoryID = "p", "k" }, "not both"},
		{"fixed price with no uom", func(p *Promotion) {
			p.CalcType, p.DiscountBps, p.PriceCents = PromoFixedPrice, nil, p64(500)
		}, "requires price_uom"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := ok
			c.mut(&p)
			err := validatePromotion(p)
			if err == nil {
				t.Fatalf("expected a rejection")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q must explain the problem (%q)", err, c.want)
			}
		})
	}
}

// - admin: the audit row a promo write leaves behind -

func TestCreatePromotion_AuditsWithADrillThrough(t *testing.T) {
	st := listOnly()
	ps := &fakePromoStore{}
	svc := newSvc(st, "pavers").WithPromoStore(ps)
	p := Promotion{
		Code: "JUNE", Name: "June Sale", CategoryID: "k-pavers", StartsAt: juneStart, EndsAt: juneEnd,
		CalcType: PromoPctOff, DiscountBps: p64(2000),
	}
	if _, err := svc.CreatePromotion(context.Background(), p, "u-1"); err != nil {
		t.Fatalf("CreatePromotion: %v", err)
	}
	if len(st.audits) != 1 {
		t.Fatalf("want exactly one audit row, got %d", len(st.audits))
	}
	got := st.audits[0]
	if got.EntityType != "promotion" || got.Action != "CREATE" || got.UserID != "u-1" {
		t.Fatalf("audit row must name the entity, the action and the actor, got %+v", got)
	}
	// A category-scoped promo drills through to the category, the same most-specific-dimension
	// rule ruleRef uses, so the audit viewer does not have to special-case promotions.
	if got.RefEntityType != RefCategory || got.RefEntityID != "k-pavers" {
		t.Fatalf("drill-through must point at the category, got (%q,%q)", got.RefEntityType, got.RefEntityID)
	}
}

func TestPromoRef_StoreWidePromoHasNoNavigableRecord(t *testing.T) {
	// Same honesty rule as TIER_DEFAULT: "everything, everywhere, for a month" is not a record.
	if gotT, gotID := promoRef(Promotion{Code: "JUNE"}); gotT != "" || gotID != "" {
		t.Fatalf("a store-wide promo must yield no reference, got (%q,%q)", gotT, gotID)
	}
}
