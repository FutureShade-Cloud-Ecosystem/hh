package export

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// catalogue_test.go, the first consumer's rules, tested without a database.
//
// Everything here is the same distinction, made four times: A NUMBER NOBODY HAS IS NOT ZERO. The
// catalogue backend goes to real trouble to keep "no stock record" separate from "0 on hand" and
// "no price anchor" separate from "$0.00", and an export is exactly where that care gets thrown away
// by a helpful `?? 0`.

// -- fakes ----------------------------------------------------------------------------------------

type fakeProducts struct {
	rows []product.Product
	err  error
	// gotFilter records what the service asked for, so the handler's query parsing is observable.
	gotFilter product.Filter
	calls     int
}

func (f *fakeProducts) List(_ context.Context, filter product.Filter) ([]product.Product, error) {
	f.calls++
	f.gotFilter = filter
	return f.rows, f.err
}

type fakeStock struct {
	rows      []inventory.BrowseRow
	err       error
	gotFilter inventory.BrowseFilter
	calls     int
}

func (f *fakeStock) Browse(_ context.Context, filter inventory.BrowseFilter) (inventory.BrowseResult, error) {
	f.calls++
	f.gotFilter = filter
	return inventory.BrowseResult{Rows: f.rows, Total: len(f.rows)}, f.err
}

func cents(v int64) *int64 { return &v }

func fixedClock() func() time.Time {
	return func() time.Time { return time.Date(2026, 8, 11, 9, 30, 0, 0, time.UTC) }
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// twoProducts: one fully-known row, one with no ledger entry and no price anchor.
func twoProducts() []product.Product {
	return []product.Product{
		{
			ID: "p1", SKU: "DBS-AGG-34CLEAR", Name: `Blu 60 <b>"Slab"</b> & Co.`,
			Manufacturer: "Techo-Bloc", Color: "Charcoal", Application: "Patio",
			BaseUOM: "PC", OnHandByBranch: map[string]float64{"trenton": 200, "kingston": 600},
			ListPriceCents: cents(2200), ListPriceUOM: "PC",
		},
		{
			ID: "p2", SKU: "00123", Name: "Border Stone",
			Manufacturer: "Unilock", ColorFamily: "Beige", Application: "Walkway",
			BaseUOM: "SF",
		},
	}
}

func buildSheet(t *testing.T, rows []product.Product, stock []inventory.BrowseRow, q CatalogueQuery) xlSheet {
	t.Helper()
	svc := NewCatalogueService(&fakeProducts{rows: rows}, &fakeStock{rows: stock}, quietLog()).WithClock(fixedClock())
	wb, err := svc.Workbook(context.Background(), q)
	if err != nil {
		t.Fatalf("Workbook: %v", err)
	}
	return parseSheet(t, render(t, wb)["xl/worksheets/sheet1.xml"])
}

// -- columns --------------------------------------------------------------------------------------

func TestCatalogue_ColumnsAreTheScreensColumnsWithTheUnitsBesideTheirNumbers(t *testing.T) {
	sheet := buildSheet(t, twoProducts(), nil, CatalogueQuery{})
	want := []string{"SKU", "Product", "Manufacturer", "Colour", "Application", "On hand", "Available", "Qty UOM", "List price", "Price UOM"}
	for i, header := range want {
		ref := cellRef(i, 3) // title row 1, subtitle row 2, header row 3
		c, ok := cellAt(sheet, ref)
		if !ok || c.Inline.Text != header {
			t.Errorf("header %s = %q, want %q", ref, c.Inline.Text, header)
		}
	}
	if _, ok := cellAt(sheet, cellRef(len(want), 3)); ok {
		t.Errorf("an eleventh column exists; the sheet must carry the screen's columns and no more")
	}
}

func TestCatalogue_ARowCarriesTheScreensValuesWithTheRightTypes(t *testing.T) {
	stock := []inventory.BrowseRow{{ProductID: "p1", OnHandBase: 800, AvailableBase: 795, ReservedBase: 5}}
	sheet := buildSheet(t, twoProducts(), stock, CatalogueQuery{})

	checks := []struct{ ref, want, typ string }{
		{"A4", "DBS-AGG-34CLEAR", "inlineStr"},
		{"B4", `Blu 60 <b>"Slab"</b> & Co.`, "inlineStr"},
		{"C4", "Techo-Bloc", "inlineStr"},
		{"D4", "Charcoal", "inlineStr"},
		{"E4", "Patio", "inlineStr"},
		{"H4", "PC", "inlineStr"},
		{"J4", "PC", "inlineStr"},
	}
	for _, c := range checks {
		got, ok := cellAt(sheet, c.ref)
		if !ok {
			t.Errorf("no cell at %s", c.ref)
			continue
		}
		if got.Inline.Text != c.want || got.Type != c.typ {
			t.Errorf("%s = %+v, want text %q type %q", c.ref, got, c.want, c.typ)
		}
	}
	// On hand is the SUM of the per-branch map, as a number.
	if got, _ := cellAt(sheet, "F4"); got.Value != "800" || got.Type != "" {
		t.Errorf("on hand = %+v, want the numeric sum 800", got)
	}
	// Available comes from the batched stock read, never re-derived here.
	if got, _ := cellAt(sheet, "G4"); got.Value != "795" || got.Type != "" {
		t.Errorf("available = %+v, want the numeric 795 from the browse read", got)
	}
	// The price is money in cents with the currency style.
	if got, _ := cellAt(sheet, "I4"); got.Value != "22.00" || got.Style != "2" {
		t.Errorf("list price = %+v, want 22.00 in the currency style", got)
	}
}

func TestCatalogue_AProductWithNoLedgerRowAndNoAnchorExportsBlanksNotZeroes(t *testing.T) {
	// p2 has no OnHandByBranch, no browse row and no ListPriceCents.
	sheet := buildSheet(t, twoProducts(), []inventory.BrowseRow{{ProductID: "p1", AvailableBase: 795}}, CatalogueQuery{})
	for _, ref := range []string{"F5", "G5", "I5", "J5"} {
		if c, ok := cellAt(sheet, ref); ok {
			t.Errorf("%s = %+v, an unknown quantity/price must be an EMPTY cell; a fabricated zero sums", ref, c)
		}
	}
	// The row is still there, with the facts it does have.
	if c, _ := cellAt(sheet, "A5"); c.Inline.Text != "00123" {
		t.Errorf("the row was dropped or shifted: %+v", c)
	}
	if c, _ := cellAt(sheet, "H5"); c.Inline.Text != "SF" {
		t.Errorf("qty UOM = %+v, want SF, a row we cannot count still knows what it counts in", c)
	}
}

func TestCatalogue_AZeroPriceIsARealPriceAndIsExported(t *testing.T) {
	rows := []product.Product{{ID: "p3", SKU: "FREE-1", Name: "Sample chip", ListPriceCents: cents(0), ListPriceUOM: "EA"}}
	sheet := buildSheet(t, rows, nil, CatalogueQuery{})
	c, ok := cellAt(sheet, "I4")
	if !ok {
		t.Fatal("a $0.00 anchor must be written, it is a price somebody set, unlike a missing one")
	}
	if c.Value != "0.00" {
		t.Errorf("price = %q, want 0.00", c.Value)
	}
}

func TestCatalogue_ColourFallsBackToTheFamilyOnlyWhenTheColourIsUnknown(t *testing.T) {
	sheet := buildSheet(t, twoProducts(), nil, CatalogueQuery{})
	if c, _ := cellAt(sheet, "D4"); c.Inline.Text != "Charcoal" {
		t.Errorf("colour = %q, want the product's own colour", c.Inline.Text)
	}
	if c, _ := cellAt(sheet, "D5"); c.Inline.Text != "Beige" {
		t.Errorf("colour = %q, want the A-18 family as the fallback", c.Inline.Text)
	}
}

func TestCatalogue_StampsTheSheetWithItsProvenance(t *testing.T) {
	sheet := buildSheet(t, twoProducts(), nil, CatalogueQuery{FilterSummary: "Manufacturer: Techo-Bloc"})
	title, _ := cellAt(sheet, "A1")
	if title.Inline.Text != "Product catalogue" {
		t.Errorf("title = %q", title.Inline.Text)
	}
	sub, _ := cellAt(sheet, "A2")
	for _, want := range []string{"2 products", "generated 2026-08-11 09:30", "filter: Manufacturer: Techo-Bloc"} {
		if !strings.Contains(sub.Inline.Text, want) {
			t.Errorf("subtitle %q is missing %q, a filtered extract with no provenance becomes 'the catalogue'", sub.Inline.Text, want)
		}
	}
}

// -- the two reads --------------------------------------------------------------------------------

func TestCatalogue_AsksBothReadsWithTheSameFilterTheScreenUses(t *testing.T) {
	products := &fakeProducts{rows: twoProducts()}
	stock := &fakeStock{}
	svc := NewCatalogueService(products, stock, quietLog()).WithClock(fixedClock())
	filter := product.Filter{Manufacturer: "Techo-Bloc", Color: "Charcoal", Q: "blu", ActiveOnly: true, BranchID: "trenton"}
	if _, err := svc.Workbook(context.Background(), CatalogueQuery{Filter: filter}); err != nil {
		t.Fatal(err)
	}
	if products.calls != 1 || stock.calls != 1 {
		t.Fatalf("calls: products=%d stock=%d, want 1 each", products.calls, stock.calls)
	}
	if products.gotFilter != filter {
		t.Errorf("the product read got %+v, want %+v", products.gotFilter, filter)
	}
	got := stock.gotFilter
	if got.Text != "blu" || got.BranchID != "trenton" {
		t.Errorf("the stock read got text=%q branch=%q", got.Text, got.BranchID)
	}
	if len(got.Manufacturer) != 1 || got.Manufacturer[0] != "Techo-Bloc" {
		t.Errorf("the stock read got manufacturer=%v", got.Manufacturer)
	}
	// An unset facet must be NIL, not [""], a one-element list holding the empty string filters to
	// products whose colour is literally blank.
	if got.Finish != nil || got.Collection != nil || got.ColorFamily != nil {
		t.Errorf("an unset facet became a constraint: %+v", got)
	}
	if got.Limit != stockReadLimit {
		t.Errorf("stock read limit = %d, want %d (what the browse read will actually give)", got.Limit, stockReadLimit)
	}
}

func TestCatalogue_AFailedStockReadStillDownloadsTheCatalogueWithAvailableBlank(t *testing.T) {
	svc := NewCatalogueService(&fakeProducts{rows: twoProducts()}, &fakeStock{err: errors.New("browse exploded")}, quietLog()).
		WithClock(fixedClock())
	wb, err := svc.Workbook(context.Background(), CatalogueQuery{})
	if err != nil {
		t.Fatalf("a failed stock read must not deny the export: %v", err)
	}
	sheet := parseSheet(t, render(t, wb)["xl/worksheets/sheet1.xml"])
	if c, ok := cellAt(sheet, "G4"); ok {
		t.Errorf("available = %+v, want blank when the stock read failed", c)
	}
	if c, _ := cellAt(sheet, "I4"); c.Value != "22.00" {
		t.Errorf("the price is gone too (%+v), the reads are independent", c)
	}
}

func TestCatalogue_AFailedProductReadIsAnError(t *testing.T) {
	svc := NewCatalogueService(&fakeProducts{err: errors.New("list exploded")}, &fakeStock{}, quietLog())
	if _, err := svc.Workbook(context.Background(), CatalogueQuery{}); err == nil {
		t.Error("a failed product read must fail the export, there is nothing to export")
	}
}

func TestCatalogue_NoRowsStillProducesASheetWithHeaders(t *testing.T) {
	sheet := buildSheet(t, nil, nil, CatalogueQuery{})
	if c, _ := cellAt(sheet, "A3"); c.Inline.Text != "SKU" {
		t.Errorf("an empty catalogue must still download its headers, got %+v", c)
	}
	sub, _ := cellAt(sheet, "A2")
	if !strings.Contains(sub.Inline.Text, "0 products") {
		t.Errorf("subtitle = %q, want an honest zero count", sub.Inline.Text)
	}
}

// -- sorting --------------------------------------------------------------------------------------

func sortedSKUs(t *testing.T, rows []product.Product, stock []inventory.BrowseRow, key SortKey, desc bool) []string {
	t.Helper()
	sheet := buildSheet(t, rows, stock, CatalogueQuery{Sort: key, Desc: desc})
	var out []string
	for i := 0; i < len(rows); i++ {
		c, ok := cellAt(sheet, cellRef(0, 4+i))
		if !ok {
			t.Fatalf("no SKU cell on data row %d", i+1)
		}
		out = append(out, c.Inline.Text)
	}
	return out
}

func sortFixture() []product.Product {
	return []product.Product{
		{ID: "a", SKU: "AAA", Name: "Charcoal cap", OnHandByBranch: map[string]float64{"t": 10}, ListPriceCents: cents(500)},
		{ID: "b", SKU: "BBB", Name: "Blu slab", OnHandByBranch: map[string]float64{"t": 90}, ListPriceCents: cents(100)},
		{ID: "c", SKU: "CCC", Name: "Ash border"}, // no stock row, no price: the unknown
		{ID: "d", SKU: "DDD", Name: "Dune paver", OnHandByBranch: map[string]float64{"t": 90}, ListPriceCents: cents(900)},
	}
}

func TestCatalogue_SortsTheWayTheScreenSortsIncludingTheTieBreak(t *testing.T) {
	got := sortedSKUs(t, sortFixture(), nil, SortOnHand, true)
	// 90 and 90 tie → SKU ascending; the unknown goes last.
	want := []string{"BBB", "DDD", "AAA", "CCC"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("descending on-hand order = %v, want %v", got, want)
		}
	}
}

func TestCatalogue_UnknownSortsLastInBothDirections(t *testing.T) {
	asc := sortedSKUs(t, sortFixture(), nil, SortOnHand, false)
	if asc[len(asc)-1] != "CCC" {
		t.Errorf("ascending order = %v; a row with no ledger entry is not a small number, it goes last", asc)
	}
	desc := sortedSKUs(t, sortFixture(), nil, SortOnHand, true)
	if desc[len(desc)-1] != "CCC" {
		t.Errorf("descending order = %v; the unknown must not head the list either", desc)
	}
}

func TestCatalogue_SortsByNameCaseInsensitively(t *testing.T) {
	rows := []product.Product{
		{ID: "a", SKU: "A", Name: "beta"},
		{ID: "b", SKU: "B", Name: "Alpha"},
	}
	got := sortedSKUs(t, rows, nil, SortName, false)
	if got[0] != "B" {
		t.Errorf("name sort = %v; 'Alpha' sorts before 'beta', a case-sensitive sort puts every capitalised name first", got)
	}
}

func TestCatalogue_SortsByAvailableFromTheStockRead(t *testing.T) {
	stock := []inventory.BrowseRow{
		{ProductID: "a", AvailableBase: 5},
		{ProductID: "b", AvailableBase: 50},
		{ProductID: "d", AvailableBase: 1},
	}
	got := sortedSKUs(t, sortFixture(), stock, SortAvailable, true)
	if got[0] != "BBB" || got[len(got)-1] != "CCC" {
		t.Errorf("available sort = %v, want BBB first and the unpositioned CCC last", got)
	}
}

func TestCatalogue_NoSortKeyKeepsTheAPIsOwnOrder(t *testing.T) {
	got := sortedSKUs(t, sortFixture(), nil, "", false)
	want := []string{"AAA", "BBB", "CCC", "DDD"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unsorted order = %v, want the read's own order %v", got, want)
		}
	}
}

func TestParseSortKey_RejectsAnythingTheScreenCannotProduce(t *testing.T) {
	if got := ParseSortKey("on_hand"); got != SortOnHand {
		t.Errorf("ParseSortKey(on_hand) = %q", got)
	}
	// A bad parameter is "no sort", not an error: nobody should be denied their export over a typo
	// in a query string.
	if got := ParseSortKey("DROP TABLE products"); got != "" {
		t.Errorf("ParseSortKey of junk = %q, want empty", got)
	}
}

// -- the endpoint ---------------------------------------------------------------------------------

func newTestHandler(products *fakeProducts, stock *fakeStock) *Handler {
	svc := NewCatalogueService(products, stock, quietLog()).WithClock(fixedClock())
	return NewHandler(svc, quietLog()).WithClock(fixedClock())
}

func TestEndpoint_ServesAnXLSXWithTheRightHeadersAndADatedFilename(t *testing.T) {
	h := newTestHandler(&fakeProducts{rows: twoProducts()}, &fakeStock{})
	rec := httptest.NewRecorder()
	h.catalogueXLSX(rec, httptest.NewRequest(http.MethodGet, "/api/v1/exports/catalogue.xlsx", nil), guards.StaffCaller{})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != ContentType {
		t.Errorf("Content-Type = %q, want the .xlsx type %q", ct, ContentType)
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != `attachment; filename="catalogue-2026-08-11.xlsx"` {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if rec.Header().Get("Content-Length") == "" {
		t.Error("no Content-Length, the browser shows an indeterminate download")
	}
	// And the body is a real package, not an error page with a spreadsheet MIME type.
	got := parts(t, rec.Body.Bytes())
	if _, ok := got["xl/worksheets/sheet1.xml"]; !ok {
		t.Fatalf("the body is not an xlsx package: %v", keys(got))
	}
	sheet := parseSheet(t, got["xl/worksheets/sheet1.xml"])
	if c, _ := cellAt(sheet, "A4"); c.Inline.Text != "DBS-AGG-34CLEAR" {
		t.Errorf("first data row = %+v", c)
	}
}

func TestEndpoint_PassesTheScreensFiltersAndSortThrough(t *testing.T) {
	products := &fakeProducts{rows: twoProducts()}
	h := newTestHandler(products, &fakeStock{})
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/exports/catalogue.xlsx?manufacturer=Techo-Bloc&color=Charcoal&application=Patio&q=blu&in_stock_only=true&branch_id=trenton&sort=list_price&dir=desc&filter_summary=Manufacturer%3A+Techo-Bloc", nil)
	rec := httptest.NewRecorder()
	h.catalogueXLSX(rec, req, guards.StaffCaller{})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	f := products.gotFilter
	if f.Manufacturer != "Techo-Bloc" || f.Color != "Charcoal" || f.Application != "Patio" || f.Q != "blu" {
		t.Errorf("filter = %+v", f)
	}
	if !f.InStockOnly || f.BranchID != "trenton" {
		t.Errorf("filter = %+v, want the in-stock toggle and the branch through", f)
	}
	if !f.ActiveOnly {
		t.Error("the export must default to ACTIVE products, the screen does, and an export quietly carrying discontinued stock is a file someone quotes from")
	}
	sheet := parseSheet(t, parts(t, rec.Body.Bytes())["xl/worksheets/sheet1.xml"])
	sub, _ := cellAt(sheet, "A2")
	if !strings.Contains(sub.Inline.Text, "filter: Manufacturer: Techo-Bloc") {
		t.Errorf("the filter summary did not reach the sheet: %q", sub.Inline.Text)
	}
}

func TestEndpoint_ActiveFalseIsHonouredSoAnInactiveAuditIsStillPossible(t *testing.T) {
	products := &fakeProducts{rows: twoProducts()}
	h := newTestHandler(products, &fakeStock{})
	rec := httptest.NewRecorder()
	h.catalogueXLSX(rec, httptest.NewRequest(http.MethodGet, "/api/v1/exports/catalogue.xlsx?active=false", nil), guards.StaffCaller{})
	if products.gotFilter.ActiveOnly {
		t.Error("active=false was ignored")
	}
}

func TestEndpoint_CapsACallerSuppliedFilterSummary(t *testing.T) {
	h := newTestHandler(&fakeProducts{rows: twoProducts()}, &fakeStock{})
	rec := httptest.NewRecorder()
	long := strings.Repeat("x", 5000)
	h.catalogueXLSX(rec, httptest.NewRequest(http.MethodGet, "/api/v1/exports/catalogue.xlsx?filter_summary="+long, nil), guards.StaffCaller{})
	sheet := parseSheet(t, parts(t, rec.Body.Bytes())["xl/worksheets/sheet1.xml"])
	sub, _ := cellAt(sheet, "A2")
	if len(sub.Inline.Text) > 400 {
		t.Errorf("a caller-supplied string of %d characters landed in the sheet", len(sub.Inline.Text))
	}
}

func TestEndpoint_AFailedReadIsA500AndNotAHalfWrittenFile(t *testing.T) {
	h := newTestHandler(&fakeProducts{err: errors.New("boom")}, &fakeStock{})
	rec := httptest.NewRecorder()
	h.catalogueXLSX(rec, httptest.NewRequest(http.MethodGet, "/api/v1/exports/catalogue.xlsx", nil), guards.StaffCaller{})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); strings.Contains(ct, "spreadsheet") {
		t.Errorf("a failure was served as a spreadsheet (%s), the download would land as a corrupt file", ct)
	}
}

func TestEndpoint_EmptyCatalogueDownloadsASheetWithHeaders(t *testing.T) {
	h := newTestHandler(&fakeProducts{}, &fakeStock{})
	rec := httptest.NewRecorder()
	h.catalogueXLSX(rec, httptest.NewRequest(http.MethodGet, "/api/v1/exports/catalogue.xlsx", nil), guards.StaffCaller{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want a real file for an empty result", rec.Code)
	}
	sheet := parseSheet(t, parts(t, rec.Body.Bytes())["xl/worksheets/sheet1.xml"])
	if c, _ := cellAt(sheet, "A3"); c.Inline.Text != "SKU" {
		t.Errorf("headers missing from the empty export: %+v", c)
	}
}

func TestRoutes_AreRegisteredBehindTheStaffGuard(t *testing.T) {
	// Guards-swap carry note (HH-3.2 fold): the counting guard stub became
	// the kit's real Guards; the anonymous leg is the guard's own 401/403
	// family and the authenticated leg carries the kit's bearer.
	h := newTestHandler(&fakeProducts{rows: twoProducts()}, &fakeStock{})
	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(identity.StaffAuthority{RoleID: "inside_sales", Tier: 3, Scope: "global"})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)

	anon := httptest.NewRecorder()
	mux.ServeHTTP(anon, httptest.NewRequest(http.MethodGet, "/api/v1/exports/catalogue.xlsx", nil))
	if anon.Code == http.StatusOK {
		t.Errorf("an anonymous caller got %d, the whole priced catalogue as one file is not public data", anon.Code)
	}

	authed := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/exports/catalogue.xlsx", nil)
	req.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
	req.Header.Set("X-Org", testkit.OrgNorthSlug)
	mux.ServeHTTP(authed, req)
	if authed.Code != http.StatusOK {
		t.Errorf("an authenticated caller got %d", authed.Code)
	}
}

func TestRoutes_RefuseToRegisterWithoutAGuard(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("registering with a nil guard must panic, failing closed beats silently exposing the export")
		}
	}()
	newTestHandler(&fakeProducts{}, &fakeStock{}).RegisterRoutes(http.NewServeMux(), nil)
}

func TestCatalogueFilename_IsDated(t *testing.T) {
	if got := CatalogueFilename(fixedClock()()); got != "catalogue-2026-08-11.xlsx" {
		t.Errorf("filename = %q", got)
	}
}
