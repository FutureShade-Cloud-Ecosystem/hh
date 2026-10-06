package pricing

import (
	"context"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// - D-PA-3 daily low-margin scan (LowMarginScan) -
// The scan grades the ACTUAL sold price (order_lines.unit_price_cents) vs the current per-branch
// WAC cost converted to the line's price_uom, classified via the SAME classifyMargin + category
// policy the manual classifier uses. Brussels: cost 175¢/PC × SF factor 4.10 is 717.5 EXACTLY, and
// the row 820 exact arm rounds it half away from zero → 718¢/SF. (The float arm this replaced
// evaluated 4.1 as 4.0999999999999996 and answered 717, a cent low on an exact half; these pins
// moved with the exact arm and no other expectation in the file moved.)

// fakeScan is a DB-free salesScanStore: it returns a fixed set of committed sale lines and records
// the window/branch it was asked for (so the default-window + branch-filter plumbing is checkable).
type fakeScan struct {
	lines     []LowMarginSaleLine
	err       error
	gotFrom   time.Time
	gotTo     time.Time
	gotBranch string
}

func (f *fakeScan) LowMarginSalesLines(_ context.Context, from, to time.Time, branchID string) ([]LowMarginSaleLine, error) {
	f.gotFrom, f.gotTo, f.gotBranch = from, to, branchID
	return f.lines, f.err
}

// scanSvc wires a Service with the Brussels SF factor (4.10) + a PAVER policy (target 3300 / floor
// 2000, the live homework band) so the scan grades exactly as production does.
func scanSvc(scan *fakeScan) *Service {
	st := &fakeStore{catPolicies: map[string]CategoryPolicy{
		"pavers": {CategoryID: "pavers", MarginTargetBps: 3300, MarginFloorBps: 2000},
	}}
	return NewService(st).
		WithUOMConverter(fakeUOM{map[string]float64{"SF": 4.10, "PC": 1}}).
		WithSalesScanStore(scan)
}

func brusselsLine(id string, price int64, cost *int64) LowMarginSaleLine {
	return LowMarginSaleLine{
		OrderLineID: id, OrderID: "ord-" + id, OrderNo: "SO-" + id, SoldAt: time.Now(),
		BranchID: "trenton", CustomerType: "RETAIL", ProductID: "brussels", CategoryID: "pavers",
		SKU: "DBS-UL-BRUSSELS", ProductName: "Unilock Brussels Block", QtyEntered: 40, UOMEntered: "SF",
		UnitPriceCents: price, PriceUOM: "SF", AvgUnitCostCents: cost,
	}
}

func rowByLine(rows []LowMarginScanRow, id string) (LowMarginScanRow, bool) {
	for _, r := range rows {
		if r.OrderLineID == id {
			return r, true
		}
	}
	return LowMarginScanRow{}, false
}

// TestLowMarginScan_GradesActualSoldPrice is the core money-math test: a below-FLOOR actual price →
// critical; below-TARGET → warn; ≥ target → omitted; a cost-missing line → invalid (never dropped).
func TestLowMarginScan_GradesActualSoldPrice(t *testing.T) {
	cost := int64(175) // ¢/PC (base uom); x SF factor 4.10 = 717.5 exactly → exact arm → 718¢/SF
	scan := &fakeScan{lines: []LowMarginSaleLine{
		brusselsLine("crit", 850, p64(cost)),     // margin ((850-718)*1e4)/850 = 1552 bps < floor 2000 → CRITICAL
		brusselsLine("warn", 975, p64(cost)),     // margin ((975-718)*1e4)/975 = 2635 bps in [2000,3300) → WARN
		brusselsLine("healthy", 1400, p64(cost)), // margin ((1400-718)*1e4)/1400 = 4871 bps ≥ 3300 → OMITTED
		brusselsLine("nocost", 900, nil),         // no product_branch_costs row → INVALID (no cost on file)
	}}
	rows, err := scanSvc(scan).LowMarginScan(context.Background(), time.Now().AddDate(0, 0, -30), time.Now(), "")
	if err != nil {
		t.Fatalf("LowMarginScan: %v", err)
	}

	// The healthy line at/above target must NOT surface (proves the filter).
	if _, ok := rowByLine(rows, "healthy"); ok {
		t.Fatalf("a healthy (≥ target) line must be omitted, but it surfaced: %+v", rows)
	}

	crit, ok := rowByLine(rows, "crit")
	if !ok {
		t.Fatalf("below-floor line missing from scan: %+v", rows)
	}
	if crit.Level != "critical" {
		t.Errorf("below-floor sold price → level %q, want critical", crit.Level)
	}
	if crit.MarginBps != 1552 {
		t.Errorf("critical margin_bps = %d, want 1552", crit.MarginBps)
	}
	if crit.CostInPriceUOMCents != 718 {
		t.Errorf("cost converted to SF = %d¢, want 718¢ (175 x 4.10 = 717.5 exact, half away from zero)", crit.CostInPriceUOMCents)
	}
	if crit.TargetBps != 3300 || crit.FloorBps != 2000 {
		t.Errorf("PAVER policy not applied: target=%d floor=%d, want 3300/2000", crit.TargetBps, crit.FloorBps)
	}
	if crit.SKU != "DBS-UL-BRUSSELS" || crit.ProductName == "" {
		t.Errorf("row must carry product name+sku from the join, got sku=%q name=%q", crit.SKU, crit.ProductName)
	}

	warn, ok := rowByLine(rows, "warn")
	if !ok {
		t.Fatalf("below-target line missing from scan: %+v", rows)
	}
	if warn.Level != "warn" {
		t.Errorf("below-target sold price → level %q, want warn", warn.Level)
	}
	if warn.MarginBps != 2635 {
		t.Errorf("warn margin_bps = %d, want 2635", warn.MarginBps)
	}

	nocost, ok := rowByLine(rows, "nocost")
	if !ok {
		t.Fatalf("cost-missing line must be SURFACED, not dropped: %+v", rows)
	}
	if nocost.Level != "invalid" || nocost.Reason == "" {
		t.Errorf("cost-missing line → level %q reason %q, want invalid + a reason", nocost.Level, nocost.Reason)
	}

	// Worst-first ordering among the valid breaching rows: critical (1552) before warn (2635).
	if len(rows) >= 2 && rows[0].OrderLineID != "crit" {
		t.Errorf("rows must be worst-first: got %q first, want crit", rows[0].OrderLineID)
	}
	// Invalid rows group at the end (after the two breaching rows).
	if got := rows[len(rows)-1].OrderLineID; got != "nocost" {
		t.Errorf("invalid rows must group last, got %q last", got)
	}
}

// TestLowMarginScan_ZeroCostAndZeroPriceAreInvalid: a zero-cost row is NOT a real cost (would render
// a fake 100%% margin) → invalid; a non-positive charged price can't be graded → invalid.
func TestLowMarginScan_ZeroCostAndZeroPriceAreInvalid(t *testing.T) {
	scan := &fakeScan{lines: []LowMarginSaleLine{
		brusselsLine("zerocost", 900, p64(0)),  // cost 0 → treated as no cost on file → INVALID
		brusselsLine("zeroprice", 0, p64(175)), // price 0 → ungradeable → INVALID
	}}
	rows, err := scanSvc(scan).LowMarginScan(context.Background(), time.Now().AddDate(0, 0, -1), time.Now(), "")
	if err != nil {
		t.Fatalf("LowMarginScan: %v", err)
	}
	for _, id := range []string{"zerocost", "zeroprice"} {
		r, ok := rowByLine(rows, id)
		if !ok {
			t.Fatalf("%s line must be surfaced as invalid, not dropped: %+v", id, rows)
		}
		if r.Level != "invalid" || r.Reason == "" {
			t.Errorf("%s → level %q reason %q, want invalid + reason", id, r.Level, r.Reason)
		}
	}
}

// TestLowMarginScan_DefaultPolicyBand: a line whose product has NO category policy is graded against
// the DEFAULT target/floor band (3000/1200), not the floor, mirroring the classifier (finding #12).
func TestLowMarginScan_DefaultPolicyBand(t *testing.T) {
	cost := p64(175) // → 718¢/SF (the exact arm, 717.5 rounded half away from zero)
	mk := func(id string, price int64) LowMarginSaleLine {
		ln := brusselsLine(id, price, cost)
		ln.CategoryID = "" // no policy → default 3000/1200 band
		return ln
	}
	scan := &fakeScan{lines: []LowMarginSaleLine{
		mk("dflt-crit", 800), // margin ((800-718)*1e4)/800 = 1025 bps < default floor 1200 → CRITICAL
		mk("dflt-warn", 900), // margin ((900-718)*1e4)/900 = 2022 bps in [1200,3000) → WARN
	}}
	rows, err := scanSvc(scan).LowMarginScan(context.Background(), time.Now().AddDate(0, 0, -1), time.Now(), "")
	if err != nil {
		t.Fatalf("LowMarginScan: %v", err)
	}
	crit, _ := rowByLine(rows, "dflt-crit")
	if crit.Level != "critical" || crit.FloorBps != defaultFloorBps || crit.TargetBps != defaultTargetBps {
		t.Errorf("no-policy below-floor → %+v, want critical @ default 1200/3000", crit)
	}
	warn, _ := rowByLine(rows, "dflt-warn")
	if warn.Level != "warn" {
		t.Errorf("no-policy below-target → level %q, want warn", warn.Level)
	}
}

// TestLowMarginScan_BadWindow: malformed windows are rejected as bad-request (→ 4xx), never a panic
// or a 500 deep in the service.
func TestLowMarginScan_BadWindow(t *testing.T) {
	svc := scanSvc(&fakeScan{})
	now := time.Now()
	cases := []struct {
		name     string
		from, to time.Time
	}{
		{"zero-from", time.Time{}, now},
		{"zero-to", now, time.Time{}},
		{"to-before-from", now, now.AddDate(0, 0, -1)},
		{"to-equals-from", now, now},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := svc.LowMarginScan(context.Background(), c.from, c.to, "")
			var bre badRequestError
			if !errors.As(err, &bre) {
				t.Fatalf("%s: want badRequestError (→400), got %v", c.name, err)
			}
		})
	}
}

// TestLowMarginScan_NoFeed: with no live-sales feed wired the scan degrades to a friendly errNoDB
// (503) rather than a nil-deref panic.
func TestLowMarginScan_NoFeed(t *testing.T) {
	svc := NewService(&fakeStore{}) // no scan store wired
	_, err := svc.LowMarginScan(context.Background(), time.Now().AddDate(0, 0, -1), time.Now(), "")
	if !errors.Is(err, errNoDB) {
		t.Fatalf("unwired scan feed → want errNoDB, got %v", err)
	}
}

// - handler: bad dates / branch_id → 400 (sweep #46 hygiene) -

func TestLowMarginScanHandler_BadInput_400(t *testing.T) {
	h := NewHandler(scanSvc(&fakeScan{}), nil)
	bad := []string{
		"/api/v1/pricing/reports/low-margin/scan?from=not-a-date",
		"/api/v1/pricing/reports/low-margin/scan?to=13/2026",
		"/api/v1/pricing/reports/low-margin/scan?branch_id=not-a-uuid",
		"/api/v1/pricing/reports/low-margin/scan?from=2026-07-10&to=2026-07-01", // to before from
	}
	for _, url := range bad {
		rec := httptest.NewRecorder()
		h.lowMarginScan(rec, httptest.NewRequest(http.MethodGet, url, nil), guards.StaffCaller{})
		if rec.Code < 400 || rec.Code >= 500 {
			t.Errorf("%s → status %d, want 4xx", url, rec.Code)
		}
	}
}

// TestLowMarginScanHandler_DefaultsAndOK: no params → default last-30-day window, 200, and the feed
// is asked for a ~30-day window; an explicit branch_id is threaded through.
func TestLowMarginScanHandler_DefaultsAndOK(t *testing.T) {
	scan := &fakeScan{lines: []LowMarginSaleLine{brusselsLine("crit", 850, p64(175))}}
	h := NewHandler(scanSvc(scan), nil)

	rec := httptest.NewRecorder()
	h.lowMarginScan(rec, httptest.NewRequest(http.MethodGet, "/api/v1/pricing/reports/low-margin/scan", nil), guards.StaffCaller{})
	if rec.Code != http.StatusOK {
		t.Fatalf("default scan → status %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if d := scan.gotTo.Sub(scan.gotFrom); d < 29*24*time.Hour || d > 31*24*time.Hour {
		t.Errorf("default window ≈30 days expected, got %v", d)
	}

	branch := "5d1b3f6e-0000-4000-9000-000000000001"
	rec2 := httptest.NewRecorder()
	h.lowMarginScan(rec2, httptest.NewRequest(http.MethodGet,
		"/api/v1/pricing/reports/low-margin/scan?from=2026-07-01&to=2026-07-15&branch_id="+branch, nil), guards.StaffCaller{})
	if rec2.Code != http.StatusOK {
		t.Fatalf("valid scan → status %d (%s), want 200", rec2.Code, rec2.Body.String())
	}
	if scan.gotBranch != branch {
		t.Errorf("branch_id not threaded to the feed: got %q, want %q", scan.gotBranch, branch)
	}
}
