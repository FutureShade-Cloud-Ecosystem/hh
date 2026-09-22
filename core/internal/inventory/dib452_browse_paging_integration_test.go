//go:build integration

// dib452_browse_paging_integration_test.go, DIB-452 against real Postgres.
//
// The unit half (dib452_browse_paging_test.go) proves the clamp, the has-more arithmetic and the
// handler's grade. None of that touches the thing that actually breaks a pager: whether page N+1,
// as a separate query against a live table, repeats or skips a row that page N already answered for.
// That is a claim about ORDER BY, OFFSET and the planner, so it is asserted against the planner.
//
// It builds its OWN population rather than leaning on the seed or on the walkthrough fixture: a
// paging test whose row count depends on what somebody else loaded is a test that passes for the
// wrong reason on somebody else's database.
//
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5432/fixqol2?sslmode=disable' \
//	  go test -tags=integration ./internal/inventory/ -run DIB452 -count=1 -v

package inventory

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// mkPagingSet inserts n products that share one manufacturer, so a filter on it selects EXACTLY this
// test's rows out of whatever else the database holds. Cleaned up at teardown, these rows have no
// stock ledger behind them, so nothing else can be perturbed by them existing briefly.
func mkPagingSet(t *testing.T, pool *pgxpool.Pool, n int) (manufacturer string, ids []string) {
	t.Helper()
	ctx := context.Background()
	// A nonce keeps two runs, or a -count=2, from colliding on the manufacturer name.
	manufacturer = fmt.Sprintf("DIB452-PAGING-%d", time.Now().UnixNano())
	for i := 0; i < n; i++ {
		var id string
		// The SKU carries the ordinal, and the read orders by sku, so the expected page contents are
		// known in advance rather than merely self-consistent.
		err := pool.QueryRow(ctx, `
			INSERT INTO products (sku, name, base_uom, price_uom, manufacturer, is_active)
			VALUES ($1, $2, 'EA', 'EA', $3, true) RETURNING id::text`,
			fmt.Sprintf("%s-%03d", manufacturer, i), fmt.Sprintf("Paging Row %03d", i), manufacturer).Scan(&id)
		if err != nil {
			t.Fatalf("insert paging fixture %d: %v", i, err)
		}
		ids = append(ids, id)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM products WHERE manufacturer = $1`, manufacturer); err != nil {
			t.Logf("cleanup of paging fixture %s failed: %v", manufacturer, err)
		}
	})
	return manufacturer, ids
}

// TestIntegration_DIB452_PagesCoverEveryRowExactlyOnce is the property the catalogue's availability
// column depends on: walk the pages and you have read the whole match set, each row once. A repeat
// is a row that overwrites itself (harmless but hides a skip); a skip is a product whose Available
// cell stays an em dash forever, which is the reported bug.
func TestIntegration_DIB452_PagesCoverEveryRowExactlyOnce(t *testing.T) {
	pool := intPool(t)
	svc := newIntSvc(pool)
	ctx := context.Background()

	const total, pageSize = 23, 5 // deliberately NOT a multiple: the last page is a partial one
	manufacturer, ids := mkPagingSet(t, pool, total)

	seen := map[string]int{}
	pages := 0
	offset := 0
	for {
		res, err := svc.Browse(ctx, BrowseFilter{
			Manufacturer: []string{manufacturer},
			Limit:        pageSize,
			Offset:       offset,
		})
		if err != nil {
			t.Fatalf("browse page at offset %d: %v", offset, err)
		}
		pages++
		if res.Total != total {
			t.Fatalf("page at offset %d reports total %d, want %d, the pre-limit count must not "+
				"depend on which page you asked for, or no pager can know when to stop", offset, res.Total, total)
		}
		if res.Offset != offset || res.Limit != pageSize {
			t.Fatalf("page echoed limit/offset (%d, %d), asked for (%d, %d)",
				res.Limit, res.Offset, pageSize, offset)
		}
		if len(res.Rows) > pageSize {
			t.Fatalf("page at offset %d returned %d rows, more than the %d asked for", offset, len(res.Rows), pageSize)
		}
		for _, r := range res.Rows {
			seen[r.ProductID]++
		}
		if !res.HasMore() {
			break
		}
		offset += len(res.Rows)
		if pages > 20 {
			t.Fatal("paging did not terminate, HasMore never went false, which is the off-by-one " +
				"pointing the other way")
		}
	}

	if want := (total + pageSize - 1) / pageSize; pages != want {
		t.Fatalf("walked %d pages, want %d for %d rows at %d per page", pages, want, total, pageSize)
	}
	if len(seen) != total {
		t.Fatalf("the walk saw %d distinct products, want %d, a row that no page returns is a row "+
			"whose Available cell is an em dash forever", len(seen), total)
	}
	for _, id := range ids {
		switch seen[id] {
		case 1: // as it should be
		case 0:
			t.Fatalf("product %s was SKIPPED by the page walk", id)
		default:
			t.Fatalf("product %s was returned by %d pages, a repeat means the ordering is not total "+
				"and something else is being skipped", id, seen[id])
		}
	}
}

// TestIntegration_DIB452_TheBoundaryRow isolates the single row a paging off-by-one loses: the FIRST
// row of the second page. It is asserted by identity, not by count, because "23 rows came back" stays
// true when page two silently starts one row late and the tail is one longer.
func TestIntegration_DIB452_TheBoundaryRow(t *testing.T) {
	pool := intPool(t)
	svc := newIntSvc(pool)
	ctx := context.Background()

	const total, pageSize = 12, 4
	manufacturer, _ := mkPagingSet(t, pool, total)

	all, err := svc.Browse(ctx, BrowseFilter{Manufacturer: []string{manufacturer}, Limit: total})
	if err != nil {
		t.Fatalf("unpaged read: %v", err)
	}
	if len(all.Rows) != total {
		t.Fatalf("the unpaged reference read returned %d rows, want %d", len(all.Rows), total)
	}

	page2, err := svc.Browse(ctx, BrowseFilter{
		Manufacturer: []string{manufacturer}, Limit: pageSize, Offset: pageSize,
	})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(page2.Rows) != pageSize {
		t.Fatalf("second page returned %d rows, want %d", len(page2.Rows), pageSize)
	}
	// The row at index pageSize of the whole ordered set IS the first row of page two. Off by one in
	// either direction and this is the row that vanishes from the catalogue's availability column.
	if got, want := page2.Rows[0].ProductID, all.Rows[pageSize].ProductID; got != want {
		t.Fatalf("the first row of page two is %s (sku %s), want %s (sku %s), the page boundary is "+
			"off by one and exactly one product's Available cell is now an em dash that never fills in",
			got, page2.Rows[0].SKU, want, all.Rows[pageSize].SKU)
	}
	// And the row BEFORE the boundary belongs to page one, not to both.
	if page2.Rows[0].ProductID == all.Rows[pageSize-1].ProductID {
		t.Fatal("page two begins with the last row of page one, the offset is being applied one short")
	}
}

// TestIntegration_DIB452_CapStillHoldsAgainstRealSQL keeps the other half of the ruling honest: the
// fix was "add pages", not "raise the ceiling". One call must still never answer for more than a
// page, however much is asked for.
func TestIntegration_DIB452_CapStillHoldsAgainstRealSQL(t *testing.T) {
	pool := intPool(t)
	svc := newIntSvc(pool)
	ctx := context.Background()

	res, err := svc.Browse(ctx, BrowseFilter{Limit: 100000})
	if err != nil {
		t.Fatalf("browse with an absurd limit: %v", err)
	}
	if len(res.Rows) > browseMaxLimit {
		t.Fatalf("one call answered for %d rows; the cap is %d and raising it is not this fix",
			len(res.Rows), browseMaxLimit)
	}
	if res.Limit != browseDefaultLimit {
		t.Fatalf("an out-of-range limit answered with limit %d, want the unchanged fallback of %d",
			res.Limit, browseDefaultLimit)
	}
}

// TestIntegration_DIB452_PastTheEndIsAnEmptyPageNotAnError closes the loop a pager walks into every
// time the match set divides exactly by the page size: it asks for one page too many. That has to be
// an ordinary empty answer.
func TestIntegration_DIB452_PastTheEndIsAnEmptyPageNotAnError(t *testing.T) {
	pool := intPool(t)
	svc := newIntSvc(pool)
	ctx := context.Background()

	const total = 6
	manufacturer, _ := mkPagingSet(t, pool, total)

	res, err := svc.Browse(ctx, BrowseFilter{
		Manufacturer: []string{manufacturer}, Limit: 5, Offset: 500,
	})
	if err != nil {
		t.Fatalf("a page past the end must not error: %v", err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("a page past the end returned %d rows", len(res.Rows))
	}
	if res.Total != total {
		t.Fatalf("total = %d on an empty page, want %d, the denominator is a property of the "+
			"filter, not of the page", res.Total, total)
	}
	if res.HasMore() {
		t.Fatal("an empty page past the end says there is more, that is the loop that never ends")
	}
	// The facets still describe the whole match set: an empty page is not an empty filter.
	if len(res.Facets) == 0 {
		t.Fatal("a page past the end dropped the facet panel; the facets belong to the filter, not the page")
	}
}
