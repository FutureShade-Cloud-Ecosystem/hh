package product

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// paging_test.go, GET /api/v1/products gains limit/offset (qol2 park P1).
//
// The two things these tests are really guarding:
//
//  1. The COMPATIBILITY PROMISE. Zero limit means no limit. Three callers depend on the whole
//     catalogue arriving in one bare JSON array, the POS product grid (pos-api.listProducts), the
//     catalogue screen (which sorts and CSV-exports client-side over the rows it is holding) and the
//     server-side .xlsx export. A default page size would truncate all three silently, on a seed of
//     30 products where nothing would notice.
//  2. The TOTAL ORDER. OFFSET is only meaningful over a deterministic sort, and `name` is not
//     unique on this table. The paging tests below therefore include tied names on purpose.

// pagedFake builds a fake store whose names deliberately TIE, so that a repository ordering by
// `name` alone would be free to shuffle them between queries and lose rows across pages.
func pagedFake(n int) *fakeStore {
	items := make([]Product, 0, n)
	for i := 0; i < n; i++ {
		items = append(items, Product{
			ID:       fmt.Sprintf("id-%03d", i),
			SKU:      fmt.Sprintf("SKU-%03d", i),
			Name:     fmt.Sprintf("Paver %d", i%3), // 3 distinct names across n rows → heavy ties
			IsActive: true,
		})
	}
	return newFakeStore(items...)
}

// A caller that asks for no page gets what it has always got: everything.
func TestListPaging_NoLimitStillReturnsTheWholeCatalogue(t *testing.T) {
	svc := NewService(pagedFake(37))
	got, err := svc.List(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 37 {
		t.Fatalf("unpaged list returned %d of 37 products, zero limit must mean NO limit (the POS grid and the .xlsx export both read this way)", len(got))
	}
}

// Walking the list one page at a time must visit every product exactly once. This is the assertion
// the `ORDER BY name, id` tiebreak exists for: with ties and no total order, a row can appear on two
// consecutive pages while another appears on none.
func TestListPaging_LimitAndOffsetWalkEveryProductExactlyOnce(t *testing.T) {
	const total, page = 37, 5
	svc := NewService(pagedFake(total))
	ctx := context.Background()

	seen := map[string]int{}
	pages := 0
	for offset := 0; offset < total+page; offset += page {
		rows, err := svc.List(ctx, Filter{Limit: page, Offset: offset})
		if err != nil {
			t.Fatalf("page at offset %d: %v", offset, err)
		}
		if len(rows) == 0 {
			break
		}
		pages++
		if len(rows) > page {
			t.Fatalf("page at offset %d returned %d rows, more than the limit of %d", offset, len(rows), page)
		}
		for _, p := range rows {
			seen[p.ID]++
		}
	}
	if pages != 8 { // 37 = 7 full pages of 5 + one of 2
		t.Fatalf("walked %d pages of %d over %d products, want 8", pages, page, total)
	}
	if len(seen) != total {
		t.Fatalf("the walk saw %d distinct products, want %d, a paged walk that misses rows is silent data loss", len(seen), total)
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("product %s appeared on %d pages, want exactly 1", id, n)
		}
	}
}

// An offset past the end is an empty page, not an error and not page one.
func TestListPaging_AnOffsetPastTheEndIsAnEmptyPage(t *testing.T) {
	svc := NewService(pagedFake(10))
	rows, err := svc.List(context.Background(), Filter{Limit: 5, Offset: 500})
	if err != nil {
		t.Fatalf("far page: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("offset 500 over 10 products returned %d rows, want an empty page", len(rows))
	}
}

// The clamp: over the cap is capped, negatives are floored. A negative limit must NOT be read as
// "unlimited", that is a caller asking to be bounded and silently not being.
func TestListPaging_LimitIsClampedAndNegativesAreFloored(t *testing.T) {
	svc := NewService(pagedFake(maxListLimit + 40))
	ctx := context.Background()

	big, err := svc.List(ctx, Filter{Limit: maxListLimit * 10})
	if err != nil {
		t.Fatalf("oversized limit: %v", err)
	}
	if len(big) != maxListLimit {
		t.Fatalf("limit=%d returned %d rows, want the %d cap", maxListLimit*10, len(big), maxListLimit)
	}

	neg, err := svc.List(ctx, Filter{Limit: -5, Offset: -100})
	if err != nil {
		t.Fatalf("negative page: %v", err)
	}
	if len(neg) != maxListLimit+40 {
		t.Fatalf("a negative limit returned %d rows, want the whole %d, floored to zero, which means unpaged", len(neg), maxListLimit+40)
	}
}

// Count is the PRE-limit total: the denominator, not the size of the page.
func TestListPaging_CountIsThePreLimitTotal(t *testing.T) {
	svc := NewService(pagedFake(37))
	ctx := context.Background()
	n, err := svc.Count(ctx, Filter{Limit: 5, Offset: 10})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 37 {
		t.Fatalf("Count with a limit of 5 returned %d, want the pre-limit total 37", n)
	}
}

// Count respects the filter, it must be the total of what was ASKED for, not of the table.
func TestListPaging_CountRespectsTheFilter(t *testing.T) {
	svc := NewService(seedProducts())
	n, err := svc.Count(context.Background(), Filter{Finish: "Smooth"})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("Count(finish=Smooth) = %d, want 2", n)
	}
}

// A store that cannot count still serves pages; only the total is unavailable, and it is reported as
// a distinguishable error rather than a zero total (a pager told "0 of 0" hides the rows it is
// holding).
func TestListPaging_AStoreThatCannotCountStillPages(t *testing.T) {
	// Wrapping the fake in a struct that embeds only the productStore INTERFACE hides everything the
	// concrete fake also happens to implement, including Count. That is the shape another package's
	// fake has: it satisfies productStore and nothing more.
	var store productStore = struct{ productStore }{pagedFake(12)}
	svc := NewService(store)
	ctx := context.Background()

	rows, err := svc.List(ctx, Filter{Limit: 5})
	if err != nil {
		t.Fatalf("page from an uncountable store: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("uncountable store returned %d rows for limit=5", len(rows))
	}
	if _, err := svc.Count(ctx, Filter{}); !errors.Is(err, errNoCount) {
		t.Fatalf("Count on an uncountable store returned %v, want errNoCount", err)
	}
}

// - the HTTP surface -

func listReq(t *testing.T, svc *Service, query string) *httptest.ResponseRecorder {
	t.Helper()
	sg := testkit.NewStaffGuards(t)
	h := NewHandler(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/products"+query, nil))
	return rec
}

// The unpaged response shape is unchanged: a bare JSON array, no envelope, and no X-Total-Count
// header to make an existing caller think the endpoint has changed under it.
func TestProductsHandler_UnpagedResponseIsStillABareArray(t *testing.T) {
	rec := listReq(t, NewService(pagedFake(12)), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	var rows []Product
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("the unpaged body is no longer a bare array: %v (body: %s)", err, rec.Body.String())
	}
	if len(rows) != 12 {
		t.Fatalf("unpaged response carried %d of 12 products", len(rows))
	}
	if got := rec.Header().Get("X-Total-Count"); got != "" {
		t.Fatalf("X-Total-Count=%q on an unpaged read; the header belongs to a page", got)
	}
}

// A paged read carries the pre-limit total in a header, so the body shape does not fork.
func TestProductsHandler_APagedReadCarriesTheTotalInAHeader(t *testing.T) {
	rec := listReq(t, NewService(pagedFake(37)), "?limit=5&offset=10")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	var rows []Product
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("paged body is not a bare array: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("limit=5 returned %d rows", len(rows))
	}
	if got := rec.Header().Get("X-Total-Count"); got != "37" {
		t.Fatalf("X-Total-Count=%q, want \"37\", a pager with no denominator is how a user concludes the filter is broken", got)
	}
}

// A bad offset is a 400, never page one. Reading it as 0 hands back page one to a caller that will
// merge it as page three: silent data loss dressed as success (inventory.offsetParam's rule).
func TestProductsHandler_ABadPageParameterIs400NotPageOne(t *testing.T) {
	for _, q := range []string{"?offset=-1", "?offset=banana", "?limit=-1", "?limit=lots", "?limit=5&offset=2.5"} {
		rec := listReq(t, NewService(pagedFake(12)), q)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET /api/v1/products%s → %d, want 400 (body: %s)", q, rec.Code, rec.Body.String())
		}
	}
}

// offset alone, with no limit, is a legitimate "skip the first N".
func TestProductsHandler_OffsetWithoutLimitSkipsAndStillTotals(t *testing.T) {
	rec := listReq(t, NewService(pagedFake(12)), "?offset=9")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	var rows []Product
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("body: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("offset=9 over 12 products returned %d rows, want 3", len(rows))
	}
	if got := rec.Header().Get("X-Total-Count"); got != "12" {
		t.Fatalf("X-Total-Count=%q, want \"12\"", got)
	}
}
