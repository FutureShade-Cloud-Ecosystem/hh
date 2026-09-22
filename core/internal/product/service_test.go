package product

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// fakeStore is an in-memory productStore with real faceted filtering, so the
// service tests exercise filter pass-through and attribute persistence without pgx.
type fakeStore struct {
	items map[string]Product
	// lastSearchLimit records the limit the service passed into Search, so the
	// SearchProducts clamp/default behaviour can be asserted without a DB.
	lastSearchLimit int

	// - W3-A counter search / scan / images -
	lastCounterQuery CounterSearchQuery // what the service handed the store (clamp assertions)
	counterHits      []CounterHit       // when set, CounterSearch returns this verbatim
	synonyms         map[string]string  // term → canonical, the fake alias table
	barcodes         map[string]string  // code → product id
	images           map[string]ProductImage
}

func newFakeStore(ps ...Product) *fakeStore {
	m := make(map[string]Product, len(ps))
	for _, p := range ps {
		if p.Metadata == nil {
			p.Metadata = map[string]any{}
		}
		m[p.ID] = p
	}
	return &fakeStore{items: m}
}

func (f *fakeStore) List(_ context.Context, flt Filter, inStockIDs []string) ([]Product, error) {
	var inStock map[string]bool
	if inStockIDs != nil {
		inStock = make(map[string]bool, len(inStockIDs))
		for _, id := range inStockIDs {
			inStock[id] = true
		}
	}
	var out []Product
	for _, p := range f.items {
		if !matchesFilter(p, flt) {
			continue
		}
		if inStock != nil && !inStock[p.ID] {
			continue // in_stock_only requested; this product is not in the in-stock set
		}
		out = append(out, p)
	}
	// Mirror the repository's ORDER BY name, id, a TOTAL order. f.items is a map, so without this
	// the fake returns rows in Go's randomised map order and no paging assertion means anything.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	// Mirror LIMIT/OFFSET (qol2 P1). Offset past the end is an empty page, not an error, the same
	// thing Postgres does, and what the last page of a pager lands on.
	if flt.Offset > 0 {
		if flt.Offset >= len(out) {
			return nil, nil
		}
		out = out[flt.Offset:]
	}
	if flt.Limit > 0 && flt.Limit < len(out) {
		out = out[:flt.Limit]
	}
	return out, nil
}

// Count mirrors Repository.Count: the matching total BEFORE Limit/Offset.
func (f *fakeStore) Count(_ context.Context, flt Filter, inStockIDs []string) (int, error) {
	unpaged := flt
	unpaged.Limit, unpaged.Offset = 0, 0
	rows, err := f.List(context.Background(), unpaged, inStockIDs)
	return len(rows), err
}

// matchesFilter mirrors the repository WHERE (same-module facets + collection/category/q).
func matchesFilter(p Product, flt Filter) bool {
	if flt.Finish != "" && p.Finish != flt.Finish {
		return false
	}
	if flt.ColorFamily != "" && p.ColorFamily != flt.ColorFamily {
		return false
	}
	if flt.Application != "" && p.Application != flt.Application {
		return false
	}
	if flt.Color != "" && p.Color != flt.Color {
		return false
	}
	if flt.Manufacturer != "" && p.Manufacturer != flt.Manufacturer {
		return false
	}
	if flt.Collection != "" && p.Collection != flt.Collection {
		return false
	}
	if flt.ActiveOnly && !p.IsActive {
		return false
	}
	if flt.Q != "" {
		q := strings.ToLower(flt.Q)
		if !strings.Contains(strings.ToLower(p.Name), q) &&
			!strings.Contains(strings.ToLower(p.SKU), q) &&
			!strings.Contains(strings.ToLower(p.Description), q) {
			return false
		}
	}
	return true
}

// Facets computes per-facet value counts over the filtered set (mirrors the repository).
func (f *fakeStore) Facets(_ context.Context, flt Filter, inStockIDs []string) ([]Facet, error) {
	var inStock map[string]bool
	if inStockIDs != nil {
		inStock = make(map[string]bool, len(inStockIDs))
		for _, id := range inStockIDs {
			inStock[id] = true
		}
	}
	counts := map[string]map[string]int{}
	bump := func(field, val string) {
		if val == "" {
			return
		}
		if counts[field] == nil {
			counts[field] = map[string]int{}
		}
		counts[field][val]++
	}
	for _, p := range f.items {
		if !matchesFilter(p, flt) {
			continue
		}
		if inStock != nil && !inStock[p.ID] {
			continue
		}
		bump("manufacturer", p.Manufacturer)
		bump("collection", p.Collection)
		bump("color", p.Color)
		bump("finish", p.Finish)
		bump("application", p.Application)
		bump("color_family", p.ColorFamily)
	}
	out := []Facet{}
	for _, field := range facetOrder {
		if field == "category" {
			continue // the fake carries no category join
		}
		vals := []FacetValue{}
		for v, n := range counts[field] {
			vals = append(vals, FacetValue{Value: v, Count: n})
		}
		out = append(out, Facet{Field: field, Values: vals})
	}
	return out, nil
}

func (f *fakeStore) Get(_ context.Context, id string) (Product, error) {
	p, ok := f.items[id]
	if !ok {
		return Product{}, ErrNotFound
	}
	return p, nil
}

func (f *fakeStore) Update(_ context.Context, id string, attrs map[string]any) (Product, error) {
	p, ok := f.items[id]
	if !ok {
		return Product{}, ErrNotFound
	}
	for k, v := range attrs {
		switch k {
		case "finish":
			p.Finish = v.(string)
		case "color_family":
			p.ColorFamily = v.(string)
		case "is_featured":
			p.IsFeatured = v.(bool)
		case "layer_quantity":
			n := v.(int)
			p.LayerQuantity = &n
		case "metadata":
			p.Metadata = v.(map[string]any)
		}
	}
	f.items[id] = p
	return p, nil
}

// GetCategory / ConvertUOM satisfy the catalog read seam (A-07). The product
// service tests don't exercise pricing, so these are minimal stand-ins.
func (f *fakeStore) GetCategory(_ context.Context, id string) (string, error) {
	if _, ok := f.items[id]; !ok {
		return "", ErrNotFound
	}
	return "", nil
}

func (f *fakeStore) ConvertUOM(_ context.Context, id, _ string) (float64, error) {
	if _, ok := f.items[id]; !ok {
		return 0, ErrNotFound
	}
	return 0, ErrNoConversion
}

func (f *fakeStore) GetBaseUOM(_ context.Context, id string) (string, error) {
	if _, ok := f.items[id]; !ok {
		return "", ErrNotFound
	}
	return "EA", nil
}

// Search satisfies the catalog-search seam (P4.1). A minimal case-insensitive substring match
// over name+sku so the product service tests can exercise SearchProducts' clamp/validation.
func (f *fakeStore) Search(_ context.Context, query string, limit int) ([]CatalogResult, error) {
	f.lastSearchLimit = limit
	var out []CatalogResult
	q := strings.ToLower(query)
	for _, p := range f.items {
		// Mirror the repository: discontinued products are never surfaced to the picker,
		// and the term is matched literally (strings.Contains treats % and _ as ordinary
		// runes), so a query with LIKE metacharacters cannot wildcard-match here.
		if !p.IsActive {
			continue
		}
		if strings.Contains(strings.ToLower(p.Name), q) || strings.Contains(strings.ToLower(p.SKU), q) {
			out = append(out, CatalogResult{ProductID: p.ID, SKU: p.SKU, Name: p.Name, BaseUOM: "EA"})
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

// - W3-A counter-search fakes -
//
// The fake mirrors the repository's RANK ORDER (exact → sku prefix → name prefix → name substring →
// synonym), not its SQL: the service's job is clamping, the min-term gate and enrichment, and those
// are what these unit tests pin. The ranking itself is proved against real Postgres in the
// integration tests, because a hand-rolled rank in a fake proves only that the fake agrees with
// itself.
func (f *fakeStore) CounterSearch(_ context.Context, q CounterSearchQuery) ([]CounterHit, error) {
	f.lastCounterQuery = q
	if f.counterHits != nil {
		return f.counterHits, nil
	}
	term := strings.ToLower(q.Q)
	type scored struct {
		hit  CounterHit
		rank int
	}
	var rows []scored
	for _, p := range f.items {
		if !p.IsActive {
			continue
		}
		name, sku := strings.ToLower(p.Name), strings.ToLower(p.SKU)
		rank := -1
		switch {
		case sku == term:
			rank = 0
		case strings.HasPrefix(sku, term):
			rank = 1
		case strings.HasPrefix(name, term):
			rank = 2
		case strings.Contains(name, term):
			rank = 3
		case f.synonyms[term] != "" && strings.Contains(name, strings.ToLower(f.synonyms[term])):
			rank = 5
		}
		if rank < 0 {
			continue
		}
		rows = append(rows, scored{CounterHit{
			ProductID: p.ID, SKU: p.SKU, Name: p.Name, Application: p.Application,
			ColorFamily: p.ColorFamily, BaseUOM: "EA", MatchReason: matchReasonFor(rank),
		}, rank})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].rank < rows[j].rank })
	var out []CounterHit
	for _, r := range rows {
		out = append(out, r.hit)
		if len(out) >= q.Limit {
			break
		}
	}
	return out, nil
}

func (f *fakeStore) ResolveScan(_ context.Context, code string) (CounterHit, bool, error) {
	if id, ok := f.barcodes[code]; ok {
		for _, p := range f.items {
			if p.ID == id {
				return CounterHit{ProductID: p.ID, SKU: p.SKU, Name: p.Name, BaseUOM: "EA", MatchReason: matchExact}, true, nil
			}
		}
	}
	for _, p := range f.items {
		if strings.EqualFold(p.SKU, code) && p.IsActive {
			return CounterHit{ProductID: p.ID, SKU: p.SKU, Name: p.Name, BaseUOM: "EA", MatchReason: matchExact}, true, nil
		}
	}
	return CounterHit{}, false, nil
}

func seedProducts() *fakeStore {
	return newFakeStore(
		Product{ID: "1", SKU: "PAV-SMO-PATIO", Name: "Smooth Patio Paver", Finish: "Smooth", Application: "Patio", ColorFamily: "Grey", IsActive: true},
		Product{ID: "2", SKU: "PAV-TUM-PATIO", Name: "Tumbled Patio Paver", Finish: "Tumbled", Application: "Patio", ColorFamily: "Tan", IsActive: true},
		Product{ID: "3", SKU: "WAL-SMO-WALL", Name: "Smooth Wall Block", Finish: "Smooth", Application: "Wall", ColorFamily: "Grey", IsActive: true},
	)
}

// TestEditableColumns_Locked turns silent four-way drift (migration ⇄ struct ⇄
// allowlist ⇄ Lit form) into a failing test: any add/rename of an editable column
// must be made here deliberately, prompting the reviewer to update all four.
func TestEditableColumns_Locked(t *testing.T) {
	want := map[string]string{
		"name": "text", "description": "text", "manufacturer": "text", "collection": "text",
		"color": "text", "finish": "text", "application": "text", "color_family": "text",
		"texture": "text", "product_size": "text", "sold_by": "text", "dimensions_lwh": "text",
		"is_featured": "bool", "is_special_order": "bool", "contractor_pricing_event_discount": "bool",
		"full_skid_quantity": "int", "layer_quantity": "int", "pallet_count": "int",
		"minimum_order_qty": "decimal", "metadata": "json",
	}
	if len(editableColumns) != len(want) {
		t.Fatalf("editableColumns has %d keys, expected %d, update this test AND the migration/struct/Lit form together", len(editableColumns), len(want))
	}
	for k, kind := range want {
		if editableColumns[k] != kind {
			t.Errorf("editableColumns[%q] = %q, want %q", k, editableColumns[k], kind)
		}
	}
}

func TestProduct_List_Faceted(t *testing.T) {
	svc := NewService(seedProducts())
	ctx := context.Background()

	all, err := svc.List(ctx, Filter{})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("empty filter should return all 3, got %d", len(all))
	}

	smoothPatio, err := svc.List(ctx, Filter{Finish: "Smooth", Application: "Patio"})
	if err != nil {
		t.Fatalf("faceted list: %v", err)
	}
	if len(smoothPatio) != 1 || smoothPatio[0].ID != "1" {
		t.Fatalf("finish=Smooth&application=Patio should return only SKU 1, got %+v", smoothPatio)
	}

	grey, err := svc.List(ctx, Filter{ColorFamily: "Grey"})
	if err != nil {
		t.Fatalf("color_family list: %v", err)
	}
	if len(grey) != 2 {
		t.Fatalf("color_family=Grey should return 2, got %d", len(grey))
	}
}

func TestProduct_Update_PersistsAttrs(t *testing.T) {
	store := seedProducts()
	svc := NewService(store)
	ctx := context.Background()

	updated, err := svc.Update(ctx, "1", map[string]any{
		"finish":         "Tumbled",
		"color_family":   "Charcoal",
		"is_featured":    true,
		"layer_quantity": float64(40), // JSON number
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Finish != "Tumbled" || updated.ColorFamily != "Charcoal" || !updated.IsFeatured {
		t.Fatalf("attrs not applied: %+v", updated)
	}
	if updated.LayerQuantity == nil || *updated.LayerQuantity != 40 {
		t.Fatalf("layer_quantity not coerced/persisted: %+v", updated.LayerQuantity)
	}

	got, _ := svc.Get(ctx, "1")
	if got.Finish != "Tumbled" || got.ColorFamily != "Charcoal" {
		t.Fatalf("re-query did not reflect update: %+v", got)
	}
}

func TestProduct_Metadata_RoundTrip(t *testing.T) {
	svc := NewService(seedProducts())
	ctx := context.Background()

	meta := map[string]any{"sizing": "6x9", "lighting": "LED-ready", "recommended_for": "driveways"}
	updated, err := svc.Update(ctx, "2", map[string]any{"metadata": meta})
	if err != nil {
		t.Fatalf("update metadata: %v", err)
	}
	if updated.Metadata["sizing"] != "6x9" || updated.Metadata["lighting"] != "LED-ready" {
		t.Fatalf("metadata round-trip failed: %+v", updated.Metadata)
	}
}

func TestProduct_SearchProducts_RejectsEmptyQuery(t *testing.T) {
	svc := NewService(seedProducts())
	ctx := context.Background()

	for _, q := range []string{"", "   ", "\t\n"} {
		_, err := svc.SearchProducts(ctx, q, 10)
		if err == nil {
			t.Fatalf("query %q: expected error, got nil", q)
		}
		var bre httpx.BadRequestError
		if !errors.As(err, &bre) {
			t.Fatalf("query %q: expected httpx.BadRequestError, got %T (%v)", q, err, err)
		}
	}
}

func TestProduct_SearchProducts_ClampsLimit(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"zero -> default", 0, defaultSearchLimit},
		{"negative -> default", -5, defaultSearchLimit},
		{"over max -> max", maxSearchLimit + 100, maxSearchLimit},
		{"in range -> unchanged", 7, 7},
		{"at max -> unchanged", maxSearchLimit, maxSearchLimit},
		{"one -> unchanged", 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := seedProducts()
			svc := NewService(store)
			if _, err := svc.SearchProducts(context.Background(), "paver", tc.in); err != nil {
				t.Fatalf("search: %v", err)
			}
			if store.lastSearchLimit != tc.want {
				t.Fatalf("limit %d clamped to %d, want %d", tc.in, store.lastSearchLimit, tc.want)
			}
		})
	}
}

func TestProduct_SearchProducts_PassesThroughResults(t *testing.T) {
	svc := NewService(seedProducts())
	ctx := context.Background()

	// "smooth" matches the two Smooth-named products (case-insensitive in the fake).
	hits, err := svc.SearchProducts(ctx, "  Smooth  ", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits for 'Smooth', got %d (%+v)", len(hits), hits)
	}
	ids := map[string]bool{}
	for _, h := range hits {
		ids[h.ProductID] = true
		if h.SKU == "" || h.Name == "" || h.BaseUOM == "" {
			t.Fatalf("result not populated through the service: %+v", h)
		}
	}
	if !ids["1"] || !ids["3"] {
		t.Fatalf("expected product ids 1 and 3, got %v", ids)
	}
}

// TestEscapeLikePattern_Literal pins the LIKE-metacharacter escaping the SKU ILIKE prefix
// relies on (finding #8): %, _ and \ from a user term must be escaped so that under
// `ESCAPE '\'` they match literally instead of acting as wildcards.
func TestEscapeLikePattern_Literal(t *testing.T) {
	cases := map[string]string{
		`abc`: `abc`,
		`a%b`: `a\%b`,
		`a_b`: `a\_b`,
		`a\b`: `a\\b`,
		`%_`:  `\%\_`,
		// backslash must be escaped first so an escaped wildcard is not double-processed.
		`\%`: `\\\%`,
	}
	for in, want := range cases {
		if got := escapeLikePattern(in); got != want {
			t.Errorf("escapeLikePattern(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestProduct_SearchProducts_MatchesMetacharsLiterally guards finding #8 at the seam: a query
// containing % or _ must not behave like a wildcard. With literal matching, "S_ooth" matches
// nothing (no product name contains that substring), whereas a leaked LIKE wildcard would have
// matched "Smooth ...".
func TestProduct_SearchProducts_MatchesMetacharsLiterally(t *testing.T) {
	svc := NewService(seedProducts())
	ctx := context.Background()

	for _, q := range []string{"S_ooth", "Smooth%Block", "%"} {
		hits, err := svc.SearchProducts(ctx, q, 10)
		if err != nil {
			t.Fatalf("search %q: %v", q, err)
		}
		if len(hits) != 0 {
			t.Fatalf("query %q should match literally (0 hits), got %d (%+v)", q, len(hits), hits)
		}
	}
}

// TestProduct_SearchProducts_ExcludesInactive guards finding #11: discontinued products must
// not surface in the line-add picker even when their name/SKU matches the term.
func TestProduct_SearchProducts_ExcludesInactive(t *testing.T) {
	store := newFakeStore(
		Product{ID: "1", SKU: "PAV-ACTIVE", Name: "Active Paver", IsActive: true},
		Product{ID: "2", SKU: "PAV-DISCO", Name: "Discontinued Paver", IsActive: false},
	)
	svc := NewService(store)

	hits, err := svc.SearchProducts(context.Background(), "Paver", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 || hits[0].ProductID != "1" {
		t.Fatalf("expected only the active product (id 1), got %+v", hits)
	}
}

func TestProduct_Update_RejectsUnknownColumn(t *testing.T) {
	svc := NewService(seedProducts())
	ctx := context.Background()

	if _, err := svc.Update(ctx, "1", map[string]any{"sku": "HACKED"}); err == nil {
		t.Fatal("expected rejection of non-editable column sku")
	}
	if _, err := svc.Update(ctx, "1", map[string]any{"definitely_not_a_column": 1}); err == nil {
		t.Fatal("expected rejection of unknown column")
	}
	if _, err := svc.Update(ctx, "1", map[string]any{"is_featured": "yes"}); err == nil {
		t.Fatal("expected type-mismatch rejection (bool column given a string)")
	}
}

// - M4 catalog-filter delta (A-4) -

// fakeStock is an in-memory product.StockReader for the in_stock_only tests.
type fakeStock struct {
	ids       []string
	branchArg string
}

func (f *fakeStock) InStockProductIDs(_ context.Context, branchID string) ([]string, error) {
	f.branchArg = branchID
	return f.ids, nil
}

// m4Store seeds a catalogue with two manufacturers/collections so facet counts + the new
// filters can be asserted.
func m4Store() *fakeStore {
	return newFakeStore(
		Product{ID: "1", SKU: "TB-BLU-01", Name: "Blu 60 Smooth", Manufacturer: "Techo-Bloc", Collection: "Blu 60", Color: "Onyx", Finish: "Smooth", Application: "Patio", ColorFamily: "Grey", Description: "smooth slab", IsActive: true},
		Product{ID: "2", SKU: "TB-BLU-02", Name: "Blu 60 Tumbled", Manufacturer: "Techo-Bloc", Collection: "Blu 60", Color: "Champlain", Finish: "Tumbled", Application: "Patio", ColorFamily: "Tan", Description: "tumbled slab", IsActive: true},
		Product{ID: "3", SKU: "UNI-BRU-01", Name: "Brussels Block", Manufacturer: "Unilock", Collection: "Brussels", Color: "Sierra", Finish: "Tumbled", Application: "Driveway", ColorFamily: "Brown", Description: "block", IsActive: true},
	)
}

// TestM4_FacetCounts, /facets returns honest per-value counts over the filtered set.
func TestM4_FacetCounts(t *testing.T) {
	svc := NewService(m4Store())
	facets, err := svc.Facets(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("Facets: %v", err)
	}
	byField := map[string]map[string]int{}
	for _, f := range facets {
		byField[f.Field] = map[string]int{}
		for _, v := range f.Values {
			byField[f.Field][v.Value] = v.Count
		}
	}
	if got := byField["manufacturer"]["Techo-Bloc"]; got != 2 {
		t.Fatalf("manufacturer Techo-Bloc count = %d, want 2", got)
	}
	if got := byField["manufacturer"]["Unilock"]; got != 1 {
		t.Fatalf("manufacturer Unilock count = %d, want 1", got)
	}
	if got := byField["collection"]["Blu 60"]; got != 2 {
		t.Fatalf("collection Blu 60 count = %d, want 2", got)
	}
	if got := byField["finish"]["Tumbled"]; got != 2 {
		t.Fatalf("finish Tumbled count = %d, want 2", got)
	}
}

// TestM4_FacetCounts_ReflectFilter, counts narrow when a filter is applied.
func TestM4_FacetCounts_ReflectFilter(t *testing.T) {
	svc := NewService(m4Store())
	facets, err := svc.Facets(context.Background(), Filter{Manufacturer: "Techo-Bloc"})
	if err != nil {
		t.Fatalf("Facets: %v", err)
	}
	for _, f := range facets {
		if f.Field != "finish" {
			continue
		}
		total := 0
		for _, v := range f.Values {
			total += v.Count
		}
		if total != 2 {
			t.Fatalf("finish facet total under Techo-Bloc filter = %d, want 2", total)
		}
	}
}

// TestM4_CollectionFilterNarrows, the new collection param narrows the list.
func TestM4_CollectionFilterNarrows(t *testing.T) {
	svc := NewService(m4Store())
	got, err := svc.List(context.Background(), Filter{Collection: "Blu 60"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("collection=Blu 60 returned %d, want 2", len(got))
	}
}

// TestM4_QSearchNarrows, q matches name/sku/description.
func TestM4_QSearchNarrows(t *testing.T) {
	svc := NewService(m4Store())
	got, err := svc.List(context.Background(), Filter{Q: "brussels"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != "3" {
		t.Fatalf("q=brussels returned %+v, want only id 3", got)
	}
}

// TestM4_InStockOnly_ExcludesZeroStock, in_stock_only drops products absent from the
// StockReader set, and passes the branch scope through.
func TestM4_InStockOnly_ExcludesZeroStock(t *testing.T) {
	stock := &fakeStock{ids: []string{"1", "3"}} // id 2 is out of stock
	svc := NewService(m4Store()).WithStock(stock)
	got, err := svc.List(context.Background(), Filter{InStockOnly: true, BranchID: "branch-trt"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("in_stock_only returned %d, want 2 (id 2 excluded)", len(got))
	}
	for _, p := range got {
		if p.ID == "2" {
			t.Fatal("zero-stock product id 2 leaked past in_stock_only")
		}
	}
	if stock.branchArg != "branch-trt" {
		t.Fatalf("branch scope = %q, want branch-trt", stock.branchArg)
	}
}

// TestM4_InStockOnly_FailsClosedWithoutReader, in_stock_only with no StockReader wired
// returns no products (fail closed), never a silently-unfiltered list.
func TestM4_InStockOnly_FailsClosedWithoutReader(t *testing.T) {
	svc := NewService(m4Store()) // no WithStock
	got, err := svc.List(context.Background(), Filter{InStockOnly: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("in_stock_only without a StockReader returned %d, want 0 (fail closed)", len(got))
	}
}

// - DIB-404: catalogue row detail (per-branch on-hand + list price) -

// fakeBranchQty / fakeListPrices are in-memory doubles for the two DIB-404 read seams. Each records
// the id set it was asked for, so the "one bulk call, not one per row" property is assertable.
type fakeBranchQty struct {
	byProduct map[string]map[string]float64
	calls     int
	lastIDs   []string
	err       error
}

func (f *fakeBranchQty) OnHandByBranch(_ context.Context, ids []string) (map[string]map[string]float64, error) {
	f.calls++
	f.lastIDs = append([]string(nil), ids...)
	if f.err != nil {
		return nil, f.err
	}
	return f.byProduct, nil
}

type fakeListPrices struct {
	cents   map[string]int64
	calls   int
	lastIDs []string
	err     error
}

func (f *fakeListPrices) ListPriceCentsFor(_ context.Context, ids []string) (map[string]int64, error) {
	f.calls++
	f.lastIDs = append([]string(nil), ids...)
	if f.err != nil {
		return nil, f.err
	}
	return f.cents, nil
}

const (
	d404Trenton  = "5d1b3f6e-0000-4000-9000-000000000001"
	d404Kingston = "5d1b3f6e-0000-4000-9000-000000000002"
)

func d404Svc(qty *fakeBranchQty, lp *fakeListPrices) *Service {
	svc := NewService(m4Store())
	if qty != nil {
		svc = svc.WithBranchStock(qty)
	}
	if lp != nil {
		svc = svc.WithListPrices(lp)
	}
	return svc
}

func TestDIB404_CatalogueRowsCarryOnHandAndListPrice(t *testing.T) {
	qty := &fakeBranchQty{byProduct: map[string]map[string]float64{
		"1": {d404Trenton: 200, d404Kingston: 50},
		"3": {d404Kingston: 12},
	}}
	lp := &fakeListPrices{cents: map[string]int64{"1": 48000, "3": 1250}}
	list, err := d404Svc(qty, lp).List(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	byID := map[string]Product{}
	for _, p := range list {
		byID[p.ID] = p
	}
	if got := byID["1"].OnHandByBranch[d404Trenton]; got != 200 {
		t.Errorf("product 1 Trenton on-hand = %v, want 200", got)
	}
	if got := byID["1"].OnHandByBranch[d404Kingston]; got != 50 {
		t.Errorf("product 1 Kingston on-hand = %v, want 50", got)
	}
	if byID["1"].ListPriceCents == nil || *byID["1"].ListPriceCents != 48000 {
		t.Errorf("product 1 list price = %v, want 48000", byID["1"].ListPriceCents)
	}
	// A product the ledger and the price book both know only partially still renders.
	if _, ok := byID["3"].OnHandByBranch[d404Trenton]; ok {
		t.Error("product 3 has no Trenton ledger row; it must be ABSENT, not zero")
	}
}

func TestDIB404_UnknownProductCarriesNoDetail(t *testing.T) {
	// The whole point of the omitempty/pointer shape: a product with no ledger row and no retail
	// anchor must say NOTHING, so the catalogue renders "-". Reporting 0 on hand invites a promise
	// nobody checked, and reporting $0.00 invites someone to sell at it.
	qty := &fakeBranchQty{byProduct: map[string]map[string]float64{"1": {d404Trenton: 5}}}
	lp := &fakeListPrices{cents: map[string]int64{"1": 100}}
	list, err := d404Svc(qty, lp).List(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, p := range list {
		if p.ID == "1" {
			continue
		}
		if p.OnHandByBranch != nil {
			t.Errorf("product %s has no ledger row; on_hand_by_branch = %v, want nil", p.ID, p.OnHandByBranch)
		}
		if p.ListPriceCents != nil {
			t.Errorf("product %s has no anchor; list_price_cents = %v, want nil", p.ID, *p.ListPriceCents)
		}
	}
}

func TestDIB404_ZeroListPriceIsNotTheSameAsNoListPrice(t *testing.T) {
	// A real $0.00 anchor is a fact the catalogue should show. Only ABSENCE means unknown.
	lp := &fakeListPrices{cents: map[string]int64{"1": 0}}
	list, err := d404Svc(nil, lp).List(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, p := range list {
		if p.ID != "1" {
			continue
		}
		if p.ListPriceCents == nil {
			t.Fatal("an explicit 0-cent anchor must be carried, not dropped as if absent")
		}
		if *p.ListPriceCents != 0 {
			t.Errorf("list_price_cents = %d, want 0", *p.ListPriceCents)
		}
	}
}

func TestDIB404_DetailIsReadInOneBulkCallPerList(t *testing.T) {
	// The catalogue lists the whole filtered set. One read per row is the shape that turns a page
	// into 30+ round trips, so the seams are bulk BY CONTRACT and this pins it.
	qty := &fakeBranchQty{byProduct: map[string]map[string]float64{}}
	lp := &fakeListPrices{cents: map[string]int64{}}
	list, err := d404Svc(qty, lp).List(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) < 3 {
		t.Fatalf("fixture should list 3 products; got %d", len(list))
	}
	if qty.calls != 1 || lp.calls != 1 {
		t.Errorf("expected exactly one bulk call per seam for a %d-row page; got stock=%d price=%d",
			len(list), qty.calls, lp.calls)
	}
	if len(qty.lastIDs) != len(list) || len(lp.lastIDs) != len(list) {
		t.Errorf("both seams must be asked for every listed id: stock=%d price=%d rows=%d",
			len(qty.lastIDs), len(lp.lastIDs), len(list))
	}
}

func TestDIB404_ASeamFailureDegradesTheRowsNotTheList(t *testing.T) {
	// A catalogue is a browsing surface and neither number is authoritative here. If the stock ledger
	// or the price book is unreachable, the list must still render, 500ing the catalogue trades a
	// less detailed page for no page at all.
	qty := &fakeBranchQty{err: errors.New("ledger down")}
	lp := &fakeListPrices{cents: map[string]int64{"1": 48000}}
	list, err := d404Svc(qty, lp).List(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("a failing stock seam must not fail the catalogue: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("expected all 3 products; got %d", len(list))
	}
	for _, p := range list {
		if p.OnHandByBranch != nil {
			t.Errorf("product %s must carry NO on-hand when the seam failed, not a guess: %v", p.ID, p.OnHandByBranch)
		}
	}
	// The seam that worked still enriches.
	var sawPrice bool
	for _, p := range list {
		if p.ID == "1" && p.ListPriceCents != nil && *p.ListPriceCents == 48000 {
			sawPrice = true
		}
	}
	if !sawPrice {
		t.Error("one failing seam must not suppress the other")
	}
}

func TestDIB404_UnwiredSeamsLeaveThePayloadUnchanged(t *testing.T) {
	// Additive by construction: a Service with neither seam wired serves exactly what it served
	// before the fields existed.
	list, err := NewService(m4Store()).List(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, p := range list {
		if p.OnHandByBranch != nil || p.ListPriceCents != nil {
			t.Errorf("unwired service must not invent row detail: %+v", p)
		}
	}
}

// - DIB-437: the anchor's UNIT rides with the anchor -

// fakeListPricesWithUOM is a ListPriceReader that ALSO implements the optional ListPriceUOMReader.
// Separate from fakeListPrices on purpose: keeping a reader that does NOT implement the widening
// lets the tests below prove both halves of "optional".
type fakeListPricesWithUOM struct {
	fakeListPrices
	uoms     map[string]string
	uomCalls int
	uomErr   error
}

func (f *fakeListPricesWithUOM) ListPriceUOMsFor(_ context.Context, _ []string) (map[string]string, error) {
	f.uomCalls++
	if f.uomErr != nil {
		return nil, f.uomErr
	}
	return f.uoms, nil
}

func TestDIB437_ListPriceCarriesItsUOM(t *testing.T) {
	lp := &fakeListPricesWithUOM{
		fakeListPrices: fakeListPrices{cents: map[string]int64{"1": 48000, "3": 1250}},
		uoms:           map[string]string{"1": "SF", "3": "PC"},
	}
	list, err := NewService(m4Store()).WithListPrices(lp).List(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	byID := map[string]Product{}
	for _, p := range list {
		byID[p.ID] = p
	}
	if got := byID["1"].ListPriceUOM; got != "SF" {
		t.Errorf("product 1 list_price_uom = %q, want SF", got)
	}
	if got := byID["3"].ListPriceUOM; got != "PC" {
		t.Errorf("product 3 list_price_uom = %q, want PC", got)
	}
	if lp.uomCalls != 1 {
		t.Errorf("the unit read must be ONE bulk call for the page, got %d", lp.uomCalls)
	}
}

func TestDIB437_NoAnchorMeansNoUnit(t *testing.T) {
	// A unit with no price behind it would render as a bare "/ PC" beside an em dash, a unit for a
	// price nobody has set. Product "2" has no anchor here but IS present in the unit map, which is
	// exactly the shape a stale/looser reader could return.
	lp := &fakeListPricesWithUOM{
		fakeListPrices: fakeListPrices{cents: map[string]int64{"1": 48000}},
		uoms:           map[string]string{"1": "SF", "2": "EA"},
	}
	list, err := NewService(m4Store()).WithListPrices(lp).List(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, p := range list {
		if p.ListPriceCents == nil && p.ListPriceUOM != "" {
			t.Errorf("product %s carries a unit %q with no price", p.ID, p.ListPriceUOM)
		}
	}
}

func TestDIB437_UnitReaderIsOptionalAndFailSoft(t *testing.T) {
	// Half one: a reader that does not implement the widening still prices the rows.
	plain := &fakeListPrices{cents: map[string]int64{"1": 48000}}
	list, err := NewService(m4Store()).WithListPrices(plain).List(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, p := range list {
		if p.ID == "1" && (p.ListPriceCents == nil || p.ListPriceUOM != "") {
			t.Errorf("plain reader: want price with no unit, got cents=%v uom=%q", p.ListPriceCents, p.ListPriceUOM)
		}
	}
	// Half two: a widened reader whose unit read ERRORS costs the row its unit, not its price.
	broken := &fakeListPricesWithUOM{
		fakeListPrices: fakeListPrices{cents: map[string]int64{"1": 48000}},
		uomErr:         errors.New("price book down"),
	}
	list, err = NewService(m4Store()).WithListPrices(broken).List(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("a failing unit read must not fail the catalogue: %v", err)
	}
	for _, p := range list {
		if p.ID == "1" {
			if p.ListPriceCents == nil || *p.ListPriceCents != 48000 {
				t.Errorf("price must survive a failed unit read, got %v", p.ListPriceCents)
			}
			if p.ListPriceUOM != "" {
				t.Errorf("a failed unit read must leave the unit empty, not guess: %q", p.ListPriceUOM)
			}
		}
	}
}
