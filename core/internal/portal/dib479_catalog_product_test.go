package portal

// dib479_catalog_product_test.go, the portal product detail, and the counter it sits on.
//
// The leak test below is the one that matters. It does not check that the handler happens not to
// copy a cost field today; it marshals the response type with EVERY field populated and asserts
// that a list of forbidden names appears nowhere in the resulting JSON. Add a margin field to the
// projection and this test names it and dies.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
)

// forbiddenOnTheCounter are the substrings that must never appear in a contractor-facing product
// payload. Kept as SUBSTRINGS rather than exact keys on purpose: "margin_bps", "gross_margin" and
// "marginPct" are all the same mistake, and a test that only knew one exact spelling would wave the
// other two through (spec §2.4: dealer margin is the one thing that can never leak).
var forbiddenOnTheCounter = []string{
	"margin", "cost", "floor", "avg_cost", "wac", "landed",
	"vendor", "supplier", "blocked", "block_policy", "bypass", "override",
	"price", "cents", "metadata", "reserved", "soft_held",
}

// fullyPopulatedCatalogProduct fills every exported field of the projection with a recognisable
// value, so the marshalled JSON below contains the complete key set, including omitempty fields,
// which would otherwise vanish and let a forbidden key hide behind a zero value.
func fullyPopulatedCatalogProduct() CatalogProduct {
	n := 4
	days := 3
	return CatalogProduct{
		ProductID: "p1", SKU: "SKU-1", Name: "Blu 60 Slab", Description: "a slab",
		Manufacturer: "Techo", Collection: "Blu", Color: "Shale", ColorFamily: "Grey",
		Finish: "Smooth", Texture: "Fine", Application: "Patio", ProductSize: "60x60",
		SoldBy: "SF", BaseUOM: "EA", DimensionsLWH: "600x600x60",
		FullSkidQuantity: &n, LayerQuantity: &n, PalletCount: &n, MinimumOrderQty: "1.0000",
		IsSpecialOrder: true, IsActive: true, LeadTimeDays: &days,
		Availability: []BranchAvailability{{BranchID: "b1", OnHandBase: 12, InStock: true, LeadTimeDays: &days}},
	}
}

// THE LEAK TEST. Marshal the projection and prove no money/margin/supplier vocabulary survives.
func TestCatalogProduct_ProjectionCarriesNoMoneyOrMarginFields(t *testing.T) {
	blob, err := json.Marshal(fullyPopulatedCatalogProduct())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(blob, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Sanity: the fixture really did populate a full object, so an empty struct cannot pass this
	// test by carrying no keys at all.
	if len(decoded) < 15 {
		t.Fatalf("fixture did not populate the projection (only %d keys)", len(decoded))
	}

	var offenders []string
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		switch node := v.(type) {
		case map[string]any:
			for k, child := range node {
				for _, bad := range forbiddenOnTheCounter {
					if strings.Contains(strings.ToLower(k), bad) {
						offenders = append(offenders, prefix+k+" (matches %"+bad+"%)")
					}
				}
				walk(prefix+k+".", child)
			}
		case []any:
			for _, child := range node {
				walk(prefix, child)
			}
		}
	}
	walk("", decoded)
	if len(offenders) > 0 {
		t.Fatalf("the portal product projection leaks internal vocabulary across the counter: %v\n"+
			"Dealer margin, cost and supplier detail must never cross. If a field is genuinely safe, "+
			"say so HERE with a reason, do not widen the forbidden list to make the test quiet.", offenders)
	}
}

// fakeBranchFacts is a BranchStockReader that also carries the optional lead-time capability, so
// the detail read exercises both enrichment paths without a database.
type fakeBranchFacts struct {
	onHand   map[string]map[string]float64
	leadTime map[string]int
}

func (f fakeBranchFacts) OnHandByBranch(_ context.Context, _ []string) (map[string]map[string]float64, error) {
	return f.onHand, nil
}

func (f fakeBranchFacts) LeadTimeDaysByBranch(_ context.Context, _ string) (map[string]int, error) {
	return f.leadTime, nil
}

// detailHandler builds a portal handler over a product service whose store returns one product and
// whose branch reader returns the given facts.
func detailHandler(t *testing.T, p product.Product, facts fakeBranchFacts) *Handler {
	t.Helper()
	store := &fakeProductStore{row: p}
	svc := product.NewService(store).WithBranchStock(facts)
	return NewHandler(nil, nil).WithProducts(svc)
}

func getDetail(t *testing.T, h *Handler, id string) *httptest.ResponseRecorder {
	t.Helper()
	r := reqWithCaller("GET", "/api/portal/v1/catalog/products/"+id, "", tenantA)
	r.SetPathValue("id", id)
	w := httptest.NewRecorder()
	h.getCatalogProduct(w, r)
	return w
}

// The response carries per-branch availability and per-branch lead time, and resolves the headline
// lead time the way HH Pro's Product.leadTimeDays means it: 0 when it is on hand somewhere.
func TestCatalogProduct_AvailabilityAndLeadTime(t *testing.T) {
	h := detailHandler(t, product.Product{ID: "p1", SKU: "SKU-1", Name: "Blu 60", IsActive: true, BaseUOM: "EA"},
		fakeBranchFacts{
			onHand:   map[string]map[string]float64{"p1": {"trenton": 40, "kingston": 0}},
			leadTime: map[string]int{"trenton": 5, "kingston": 9},
		})
	w := getDetail(t, h, "p1")
	if w.Code != http.StatusOK {
		t.Fatalf("detail = %d (%s)", w.Code, w.Body.String())
	}
	var got CatalogProduct
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Availability) != 2 {
		t.Fatalf("want a row per branch, got %+v", got.Availability)
	}
	// Stocked branch first, and it reports in_stock with its quantity + its own lead time.
	first := got.Availability[0]
	if first.BranchID != "trenton" || !first.InStock || first.OnHandBase != 40 {
		t.Fatalf("stocked branch must sort first and report its position: %+v", first)
	}
	if first.LeadTimeDays == nil || *first.LeadTimeDays != 5 {
		t.Fatalf("branch lead time missing: %+v", first)
	}
	if got.Availability[1].InStock {
		t.Fatalf("kingston has none on hand and must not report in_stock: %+v", got.Availability[1])
	}
	if got.LeadTimeDays == nil || *got.LeadTimeDays != 0 {
		t.Fatalf("headline lead time must be 0 when the product is on hand somewhere, got %v", got.LeadTimeDays)
	}
}

// With nothing on hand anywhere, the headline lead time is the SHORTEST one on file.
func TestCatalogProduct_LeadTimeIsTheShortestWhenNothingIsStocked(t *testing.T) {
	h := detailHandler(t, product.Product{ID: "p1", SKU: "S", Name: "N", IsActive: true},
		fakeBranchFacts{
			onHand:   map[string]map[string]float64{"p1": {"trenton": 0}},
			leadTime: map[string]int{"trenton": 9, "kingston": 4},
		})
	var got CatalogProduct
	if err := json.Unmarshal(getDetail(t, h, "p1").Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.LeadTimeDays == nil || *got.LeadTimeDays != 4 {
		t.Fatalf("headline lead time = %v, want the shortest (4)", got.LeadTimeDays)
	}
}

// A branch with NO lead-time policy carries no lead time, never a zero, which would read as
// "available today". A product nobody manages carries no headline lead time at all.
func TestCatalogProduct_NoPolicyMeansNoLeadTimeNotZero(t *testing.T) {
	h := detailHandler(t, product.Product{ID: "p1", SKU: "S", Name: "N", IsActive: true},
		fakeBranchFacts{onHand: map[string]map[string]float64{"p1": {"trenton": 0}}})
	body := getDetail(t, h, "p1").Body.Bytes()
	var got CatalogProduct
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.LeadTimeDays != nil {
		t.Fatalf("no policy on file must mean no lead time, got %v", *got.LeadTimeDays)
	}
	if len(got.Availability) != 1 || got.Availability[0].LeadTimeDays != nil {
		t.Fatalf("branch row must carry no lead time: %+v", got.Availability)
	}
	if strings.Contains(string(body), `"lead_time_days":0`) {
		t.Fatalf("an unknown lead time must be ABSENT, not zero: %s", body)
	}
}

// A discontinued product still resolves, it is reported inactive, not hidden. Answering
// "no such product" for something sitting on a pallet is the DIB-443 failure in a new costume.
func TestCatalogProduct_DiscontinuedProductStillResolves(t *testing.T) {
	h := detailHandler(t, product.Product{ID: "p1", SKU: "S", Name: "Old Line", IsActive: false},
		fakeBranchFacts{onHand: map[string]map[string]float64{"p1": {"trenton": 3}}})
	w := getDetail(t, h, "p1")
	if w.Code != http.StatusOK {
		t.Fatalf("discontinued product = %d, want 200", w.Code)
	}
	var got CatalogProduct
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.IsActive {
		t.Fatal("is_active must be reported honestly")
	}
}

// An unknown id is a 404, never a 403 (which would confirm existence) and never a 500.
func TestCatalogProduct_UnknownIDIs404(t *testing.T) {
	store := &fakeProductStore{err: product.ErrNotFound}
	h := NewHandler(nil, nil).WithProducts(product.NewService(store))
	if w := getDetail(t, h, "00000000-0000-4000-9000-000000000000"); w.Code != http.StatusNotFound {
		t.Fatalf("unknown product = %d, want 404 (body %s)", w.Code, w.Body.String())
	}
	// A malformed id is the same answer: it cannot name a row, so it is a miss, not a complaint.
	if w := getDetail(t, h, "not-a-uuid"); w.Code != http.StatusNotFound {
		t.Fatalf("malformed id = %d, want 404", w.Code)
	}
}

// THE ROUTE IS GATED BY CONSTRUCTION. Behind g.RequireCapability(submit_rfq)
// under the bearer split (HH-3.2 fold), so the carried mapping decides: the
// personas that plan and buy get in; the one that only pays does not.
func TestCatalogProductRoute_IsGatedOnSubmitRFQ(t *testing.T) {
	cases := []struct {
		role string
		want bool
	}{
		{string(RoleAccountAdmin), true},
		{string(RoleBuyer), true},
		{string(RoleBuyerNoPay), true}, // the PM plans and buys, must see the catalogue
		{string(RoleAPFinance), false}, // A/P does not buy, so it does not shop
		{string(RoleFieldCrew), false},
	}
	for _, c := range cases {
		if got := identity.CapabilitiesFor(c.role).Has(identity.CapSubmitRFQ); got != c.want {
			t.Errorf("%s: submit_rfq = %v, want %v", c.role, got, c.want)
		}
	}
}
