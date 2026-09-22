package product

// coverage_test.go, DIB-80 / S1. The DATA half of the material calculator, driven without a database.
//
// What is worth testing here is not "does the SQL run" (coverage_integration_test.go does that against
// the seeded DB) but the two decisions this file makes on the caller's behalf:
//
//	1. WHERE COVERAGE COMES FROM, and whether the row admits it (applyCoverage).
//	2. WHAT HAPPENS WHEN THERE IS NO COVERAGE SOURCE AT ALL, the honest-empty path, which is the
//	   path most of the seeded catalogue takes today and therefore the one a regression would hit.

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"net/http"
	"net/http/httptest"
	"testing"
)

func f64(v float64) *float64 { return &v }

// nearly compares two float64s to 1e-12, which is far tighter than any coverage number matters to and
// loose enough to survive the difference between a compile-time-folded constant and a runtime divide.
func nearly(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-12
}

// - 1 · applyCoverage: the promoted column, then the UoM reciprocal, then nothing -

func TestCoverage_PromotedColumnWins(t *testing.T) {
	// A product carrying BOTH the A-18-era promoted column and an SF conversion factor. The column is
	// what a human recorded; the factor is what we would have worked out. The human wins, and the row
	// says which one it used.
	var b CoverageBasis
	applyCoverage(&b, f64(0.5), f64(4.10))
	if b.CoverageSFPerUnit == nil || *b.CoverageSFPerUnit != 0.5 {
		t.Fatalf("coverage = %v, want the column value 0.5", b.CoverageSFPerUnit)
	}
	if b.CoverageSource != "column" {
		t.Fatalf("source = %q, want %q", b.CoverageSource, "column")
	}
}

func TestCoverage_DerivedFromSFFactorIsTheReciprocal(t *testing.T) {
	// THE CONVERSION-FACTOR TEST. products.coverage_sf_per_unit is NULL for every seeded product, so
	// this is the path the whole shipped calculator actually runs on: one SF is 4.10 base pieces, so
	// one piece covers 1/4.10 = 0.24390… sq ft. A mutation that returns the factor itself (4.10 sq ft
	// per paver, a paver the size of a card table) fails here.
	var b CoverageBasis
	applyCoverage(&b, nil, f64(4.10))
	if b.CoverageSFPerUnit == nil {
		t.Fatal("coverage is nil; want it derived from the SF factor")
	}
	// The comparison carries a tolerance because Go folds the constant 1.0/4.10 at compile time in
	// exact arithmetic and the runtime division does not, they differ in the last bit, which is a
	// fact about floating point and not about coverage.
	if got, want := *b.CoverageSFPerUnit, 1/4.10; !nearly(got, want) {
		t.Fatalf("derived coverage = %v, want %v (1/4.10)", got, want)
	}
	if b.CoverageSource != "uom" {
		t.Fatalf("source = %q, want %q, a derived number must SAY it was derived", b.CoverageSource, "uom")
	}
}

func TestCoverage_NoSourceLeavesTheRowEmptyAndUnsourced(t *testing.T) {
	// THE NO-COVERAGE-DATA PATH. Neither fact on file (the sealer, the LED light, the jointing sand).
	// The row must come back with nil coverage and an EMPTY source, not zero, and not a guess.
	var b CoverageBasis
	applyCoverage(&b, nil, nil)
	if b.CoverageSFPerUnit != nil {
		t.Fatalf("coverage = %v, want nil when nothing is on file", *b.CoverageSFPerUnit)
	}
	if b.CoverageSource != "" {
		t.Fatalf("source = %q, want empty", b.CoverageSource)
	}
}

func TestCoverage_ZeroAndNegativeAreTreatedAsAbsent(t *testing.T) {
	// A zero in either place is a broken import, not a fact about a paver, and it is the one value
	// that would make the calculator divide by zero and print Infinity at the counter.
	for _, tc := range []struct {
		name           string
		column, factor *float64
	}{
		{"zero column, no factor", f64(0), nil},
		{"negative column, no factor", f64(-2), nil},
		{"no column, zero factor", nil, f64(0)},
		{"no column, negative factor", nil, f64(-4.1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b CoverageBasis
			applyCoverage(&b, tc.column, tc.factor)
			if b.CoverageSFPerUnit != nil || b.CoverageSource != "" {
				t.Fatalf("got coverage=%v source=%q, want the honest-empty row", b.CoverageSFPerUnit, b.CoverageSource)
			}
		})
	}
}

func TestCoverage_ZeroColumnStillFallsBackToTheFactor(t *testing.T) {
	// A row that carries a zero column AND a real factor must not be stranded: the factor is the
	// DATA_MODEL's source of truth on conflict, so it is used, and the source says "uom".
	var b CoverageBasis
	applyCoverage(&b, f64(0), f64(2.78))
	if b.CoverageSFPerUnit == nil || !nearly(*b.CoverageSFPerUnit, 1/2.78) {
		t.Fatalf("coverage = %v, want 1/2.78", b.CoverageSFPerUnit)
	}
	if b.CoverageSource != "uom" {
		t.Fatalf("source = %q, want %q", b.CoverageSource, "uom")
	}
}

func TestCoverage_DecimalTextParsesAndNullStaysNull(t *testing.T) {
	// DECIMAL(19,8) is read as text and parsed (the ConvertUOM convention) so NULL stays
	// distinguishable from 0.0 all the way to the JSON.
	s := "4.10000000"
	if got := parseDecimalPtr(&s); got == nil || *got != 4.1 {
		t.Fatalf("parseDecimalPtr(%q) = %v, want 4.1", s, got)
	}
	if got := parseDecimalPtr(nil); got != nil {
		t.Fatalf("parseDecimalPtr(nil) = %v, want nil", got)
	}
	bad := "not a number"
	if got := parseDecimalPtr(&bad); got != nil {
		t.Fatalf("parseDecimalPtr(%q) = %v, want nil, one corrupt factor must not fail the read", bad, got)
	}
	zero := "0"
	if got := positivePtr(parseDecimalPtr(&zero)); got != nil {
		t.Fatalf("positivePtr(0) = %v, want nil, zero tonnes per cubic yard is not a density", got)
	}
}

// - 2 · the Service seam: capability, id hygiene, and empty-is-not-an-error -

// coverageFake is a productStore that ALSO answers the optional coverage capability.
type coverageFake struct {
	*fakeStore
	rows    []CoverageBasis
	lastIDs []string
	err     error
}

func (c *coverageFake) CoverageBasis(_ context.Context, ids []string) ([]CoverageBasis, error) {
	c.lastIDs = ids
	if c.err != nil {
		return nil, c.err
	}
	return c.rows, nil
}

func TestCoverage_ServiceWithoutCapabilityReturnsEmptyNotError(t *testing.T) {
	// A store that predates the feature. Empty is never an error (DIB-471's ruling): the calculator
	// must render "nothing to calculate", not "your database is broken".
	svc := NewService(newFakeStore())
	rows, err := svc.CoverageBasis(context.Background(), nil)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if rows == nil || len(rows) != 0 {
		t.Fatalf("rows = %v, want a non-nil empty slice", rows)
	}
}

func TestCoverage_ServiceDedupesAndTrimsIDs(t *testing.T) {
	fake := &coverageFake{fakeStore: newFakeStore()}
	svc := NewService(fake)
	if _, err := svc.CoverageBasis(context.Background(), []string{" a ", "a", "", "b"}); err != nil {
		t.Fatalf("err = %v", err)
	}
	if got, want := len(fake.lastIDs), 2; got != want {
		t.Fatalf("ids = %v, want 2 cleaned ids", fake.lastIDs)
	}
	if fake.lastIDs[0] != "a" || fake.lastIDs[1] != "b" {
		t.Fatalf("ids = %v, want [a b]", fake.lastIDs)
	}
}

func TestCoverage_ServiceCapsTheIDList(t *testing.T) {
	ids := make([]string, 0, maxCoverageIDs+50)
	for i := 0; i < maxCoverageIDs+50; i++ {
		ids = append(ids, string(rune('a'+i%26))+string(rune('a'+i/26))+string(rune('a'+i/676))+itoa(i))
	}
	fake := &coverageFake{fakeStore: newFakeStore()}
	if _, err := NewService(fake).CoverageBasis(context.Background(), ids); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(fake.lastIDs) != maxCoverageIDs {
		t.Fatalf("ids length = %d, want the %d cap", len(fake.lastIDs), maxCoverageIDs)
	}
}

func TestCoverage_ServiceSurfacesAStoreFault(t *testing.T) {
	// The other half of the ruling: a read that FAILED is a fault and must say so. Empty-is-not-an-
	// error may not be stretched into swallowing a broken query.
	boom := errors.New("connection reset")
	fake := &coverageFake{fakeStore: newFakeStore(), err: boom}
	if _, err := NewService(fake).CoverageBasis(context.Background(), nil); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the store's error", err)
	}
}

// - 3 · the route: envelope + honest empty -

func TestCoverage_HandlerAnswersItemsEnvelope(t *testing.T) {
	rows := []CoverageBasis{{
		ProductID: "p1", SKU: "DBS-UL-BRUSSELS", Name: "Unilock Brussels Block",
		BaseUOM: "PC", BaseDimension: "count", SellUOM: "SF", SellDimension: "area",
		SellUOMFactor: f64(4.10), CoverageSFPerUnit: f64(1.0 / 4.10), CoverageSource: "uom",
		PalletFactor: f64(50), BreakPalletAllowed: true, WasteFactorPct: 5,
	}}
	h := NewHandler(NewService(&coverageFake{fakeStore: newFakeStore(), rows: rows}), nil)
	rec := httptest.NewRecorder()
	h.coverage(rec, httptest.NewRequest(http.MethodGet, "/api/v1/products/coverage", nil), guards.StaffCaller{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Items []CoverageBasis `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v, body %s", err, rec.Body.String())
	}
	if len(body.Items) != 1 || body.Items[0].SKU != "DBS-UL-BRUSSELS" {
		t.Fatalf("items = %+v, want the one basis row", body.Items)
	}
	if body.Items[0].CoverageSource != "uom" {
		t.Fatalf("coverage_source = %q, want it carried to the wire", body.Items[0].CoverageSource)
	}
}

func TestCoverage_HandlerEmptyCatalogueIs200AndEmptyArray(t *testing.T) {
	h := NewHandler(NewService(&coverageFake{fakeStore: newFakeStore()}), nil)
	rec := httptest.NewRecorder()
	h.coverage(rec, httptest.NewRequest(http.MethodGet, "/api/v1/products/coverage", nil), guards.StaffCaller{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, an empty catalogue is an answer", rec.Code)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(body["items"]) != "[]" {
		t.Fatalf("items = %s, want [] (never null, the loader must not have to guess)", body["items"])
	}
}

func TestCoverage_HandlerPassesIDsThrough(t *testing.T) {
	fake := &coverageFake{fakeStore: newFakeStore()}
	h := NewHandler(NewService(fake), nil)
	rec := httptest.NewRecorder()
	h.coverage(rec, httptest.NewRequest(http.MethodGet, "/api/v1/products/coverage?ids=p1,+p2+,,p1", nil), guards.StaffCaller{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(fake.lastIDs) != 2 || fake.lastIDs[0] != "p1" || fake.lastIDs[1] != "p2" {
		t.Fatalf("ids = %v, want [p1 p2]", fake.lastIDs)
	}
}

func TestCoverage_RouteIsRegisteredOnTheCounterMux(t *testing.T) {
	// The route exists and is behind the staff guard, the calculator is a counter surface, and the
	// guard is what keeps the yard's packaging/waste rules off an anonymous request.
	mux := http.NewServeMux()
	sg := testkit.NewStaffGuards(t)
	h := NewHandler(NewService(&coverageFake{fakeStore: newFakeStore()}), nil)
	h.RegisterCounterRoutes(mux, sg.Guards)
	_, pattern := mux.Handler(httptest.NewRequest(http.MethodGet, "/api/v1/products/coverage", nil))
	if pattern != "GET /api/v1/products/coverage" {
		t.Fatalf("pattern = %q, want the coverage route to bind", pattern)
	}
	// The route sits behind the staff constructor: an anonymous request is
	// refused (401 family from the gate), never served the calculator.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/products/coverage", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous coverage request = %d, want 401 behind the staff guard", rec.Code)
	}
}

// itoa avoids importing strconv into the test just for the id-cap fixture.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf []byte
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}
