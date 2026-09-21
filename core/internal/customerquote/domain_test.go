package customerquote

import (
	"testing"
	"time"
)

// The carried domain rules in their offline form. The totals fixture values
// follow the reference's own test behaviour (hh_pro_dibbits
// src/core/actions/__tests__/customer-quote.test.ts): markup is the rounded
// percentage of material cost, a flat labour line lands whole in labour and
// grand, and margin is grand minus material cost.

func fixtureNow() time.Time { return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) }

func pricedLine(name, qty string, cents int64, validUntil *time.Time) Line {
	return Line{Name: name, Qty: qty, UOM: "EA", UnitCostCents: cents,
		PriceSource: PriceSourceERP, PriceValidUntil: validUntil, Presentation: PresentationCommodity}
}

func TestComputeTotalsCarriedBehaviour(t *testing.T) {
	lines := []Line{pricedLine("Fixture Line One", "10", 10000, nil)}
	labour := []LabourLine{{ID: "lab1", Description: "Framing crew", RateType: "flat", RateCents: 250000}}
	overhead := []OverheadLine{{ID: "ovh1", Label: "Permit", AmountType: "flat", Value: 10000}}
	totals, err := ComputeTotals(lines, 2500, labour, overhead) // 25 percent markup
	if err != nil {
		t.Fatalf("totals: %v", err)
	}
	// materialCost 100000, markup round(100000 * 0.25) = 25000 (the reference's own assertion shape).
	if totals.MaterialCostCents != 100000 || totals.MarkupCents != 25000 || totals.MaterialsCents != 125000 {
		t.Fatalf("materials leg: %+v", totals)
	}
	if totals.LabourCents != 250000 {
		t.Fatalf("flat labour must land whole: %+v", totals)
	}
	if totals.OverheadCents != 10000 {
		t.Fatalf("flat overhead: %+v", totals)
	}
	if totals.GrandCents != 385000 {
		t.Fatalf("grand: %+v", totals)
	}
	if totals.MarginCents != totals.GrandCents-totals.MaterialCostCents || totals.MarginCents != 285000 {
		t.Fatalf("margin is grand minus material cost: %+v", totals)
	}
	if totals.MarginPercent != 74 { // round(285000 * 100 / 385000)
		t.Fatalf("margin percent: %+v", totals)
	}
}

func TestComputeTotalsFractionalLegs(t *testing.T) {
	// Fractional quantity: 2.5 squares at 4000 cents, per-line rounding.
	lines := []Line{pricedLine("Fixture Squares", "2.5", 4000, nil)}
	// Hourly labour: 85 per hour for 1.5 hours.
	labour := []LabourLine{{ID: "lab1", Description: "Crew", RateType: "hourly", RateCents: 8500, Hours: "1.5"}}
	// Percentage overhead: 5 percent (500 bps) on marked-up materials plus labour.
	overhead := []OverheadLine{{ID: "ovh1", Label: "General conditions", AmountType: "percent", Value: 500}}
	totals, err := ComputeTotals(lines, 0, labour, overhead)
	if err != nil {
		t.Fatalf("totals: %v", err)
	}
	if totals.MaterialCostCents != 10000 { // round(4000 * 2.5)
		t.Fatalf("fractional material cost: %+v", totals)
	}
	if totals.LabourCents != 12750 { // round(8500 * 1.5)
		t.Fatalf("hourly labour: %+v", totals)
	}
	if totals.OverheadCents != 1138 { // round((10000 + 12750) * 0.05), the carried base
		t.Fatalf("percent overhead on the carried base: %+v", totals)
	}
	if totals.GrandCents != 23888 {
		t.Fatalf("grand: %+v", totals)
	}
	// Quantities beyond four decimal places refuse, and non-decimals refuse.
	if _, err := ComputeTotals([]Line{pricedLine("x", "1.12345", 100, nil)}, 0, nil, nil); err == nil {
		t.Fatalf("a five-place quantity must refuse")
	}
	if _, err := ComputeTotals([]Line{pricedLine("x", "1e3", 100, nil)}, 0, nil, nil); err == nil {
		t.Fatalf("an exponent quantity must refuse")
	}
	if _, err := ComputeTotals([]Line{pricedLine("x", "-2", 100, nil)}, 0, nil, nil); err == nil {
		t.Fatalf("a negative quantity must refuse")
	}
}

func TestExpiryCapsAndPredicates(t *testing.T) {
	now := fixtureNow()
	later := now.Add(30 * 24 * time.Hour)
	horizon := now.Add(2 * 24 * time.Hour)
	if got := CapValidUntil(later, &horizon); !got.Equal(horizon) {
		t.Fatalf("the offer bound must cap at the supplier horizon")
	}
	if got := CapValidUntil(horizon, &later); !got.Equal(horizon) {
		t.Fatalf("an offer inside the horizon stands")
	}
	if got := CapValidUntil(later, nil); !got.Equal(later) {
		t.Fatalf("no horizon means no cap")
	}
	lines := []Line{
		pricedLine("A", "1", 100, &horizon),
		pricedLine("B", "1", 100, &later),
	}
	if h := SupplierPriceHorizon(lines); h == nil || !h.Equal(horizon) {
		t.Fatalf("the horizon is the earliest supplier expiry")
	}
	// R2: never-priced and priced-and-lapsed both count.
	lapsed := now.Add(-time.Hour)
	if IsLinePriced(pricedLine("A", "1", 100, &lapsed), now) {
		t.Fatalf("a lapsed price is unpriced")
	}
	unpriced := pricedLine("A", "1", 100, nil)
	unpriced.PriceSource = PriceSourceUnpriced
	if IsLinePriced(unpriced, now) {
		t.Fatalf("a never-priced line is unpriced")
	}
	// The offer predicates.
	valid := now.Add(time.Hour)
	if !IsOfferExpired(&valid, now) && IsOfferExpired(nil, now) {
		t.Fatalf("offer expiry predicates are inverted")
	}
	expired := now.Add(-time.Minute)
	if !IsOfferExpired(&expired, now) {
		t.Fatalf("a past offer bound is expired")
	}
	for _, status := range []Status{StatusSent, StatusViewed, StatusChangesRequested} {
		if !CanRespond(status, &valid, now) {
			t.Fatalf("status %s must be respondable on a live offer", status)
		}
	}
	for _, status := range []Status{StatusDraft, StatusAccepted, StatusDeclined, StatusExpired} {
		if CanRespond(status, &valid, now) {
			t.Fatalf("status %s must not be respondable", status)
		}
	}
	if CanRespond(StatusSent, &expired, now) {
		t.Fatalf("an expired offer is not respondable")
	}
}

func TestSpecialOrderSkuPredicate(t *testing.T) {
	if !IsSpecialOrderSku("SO-FX-0001") {
		t.Fatalf("the legacy prefix must read as special order")
	}
	if IsSpecialOrderSku("FX-SO-0001") || IsSpecialOrderSku("") {
		t.Fatalf("only the leading prefix reads as special order")
	}
}

func TestTypedNameBounds(t *testing.T) {
	if ValidTypedName("A") {
		t.Fatalf("a one-character name must fail the carried minimum")
	}
	if !ValidTypedName("Fixture Homeowner") {
		t.Fatalf("an ordinary name must pass")
	}
	if !ValidTypedName("  Padded Name  ") {
		t.Fatalf("the name is judged trimmed")
	}
	if ValidTypedName(stringOfLen(121)) {
		t.Fatalf("a name beyond the carried bound must fail")
	}
	if !ValidTypedName(stringOfLen(120)) {
		t.Fatalf("a name at the bound passes")
	}
}

func stringOfLen(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
