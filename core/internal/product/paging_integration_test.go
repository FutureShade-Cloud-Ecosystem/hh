//go:build integration

package product

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// paging_integration_test.go, the qol2 P1 page against REAL Postgres.
//
// The unit tests can prove the fake pages correctly; only Postgres can prove the ORDER BY does,
// because the property under test is one the fake cannot have: with `ORDER BY name` alone, tied
// names come back in whatever order the planner finds cheapest, and that order is allowed to differ
// between two executions of the SAME query. That is why an OFFSET pager over a non-unique sort loses
// rows, and it is why these fixtures deliberately insert products that all share ONE name.

// seedTiedNames inserts n products that share a single name (and therefore tie on the old sort key),
// returning their ids. Cleaned up by id, never by a truncate.
func seedTiedNames(t *testing.T, pool *pgxpool.Pool, name string, n int) []string {
	t.Helper()
	ctx := context.Background()
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO products (sku, name, base_uom, price_uom, is_active)
			 VALUES ($1, $2, 'EA', 'EA', true) RETURNING id::text`,
			fmt.Sprintf("%s-%03d", name, i), name).Scan(&id); err != nil {
			t.Fatalf("seed tied product %d: %v", i, err)
		}
		ids = append(ids, id)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM products WHERE id = ANY($1::uuid[])`, ids)
	})
	return ids
}

// Paging a filtered set whose rows ALL share one name must still visit every row exactly once.
// Under `ORDER BY name` this is the test that fails: the sort is not a total order, so a row can be
// served on two consecutive pages while another is served on neither.
func TestIntegration_ProductPagingVisitsEveryTiedRowExactlyOnce(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := NewService(NewRepository(pool))

	const total, page = 23, 4
	name := "ZZZ Paging Fixture Paver"
	ids := seedTiedNames(t, pool, name, total)

	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}

	seen := map[string]int{}
	for offset := 0; ; offset += page {
		rows, err := svc.List(ctx, Filter{Q: name, Limit: page, Offset: offset})
		if err != nil {
			t.Fatalf("page at offset %d: %v", offset, err)
		}
		if len(rows) == 0 {
			break
		}
		if len(rows) > page {
			t.Fatalf("page at offset %d returned %d rows, over the limit of %d", offset, len(rows), page)
		}
		for _, p := range rows {
			seen[p.ID]++
		}
		if offset > total*2 {
			t.Fatalf("the pager did not terminate by offset %d", offset)
		}
	}

	if len(seen) != total {
		t.Fatalf("paged walk over %d rows that all share the name %q saw %d distinct products, ORDER BY name alone is not a total order, so OFFSET drops rows",
			total, name, len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("product %s came back on %d pages, want exactly 1", id, n)
		}
		if !want[id] {
			t.Fatalf("the walk returned a product outside the fixture: %s", id)
		}
	}
}

// The order is STABLE across calls: the same page asked for twice is the same rows, in the same
// sequence. A pager whose page 2 changes between two clicks is the same defect wearing a different
// hat, and this is the assertion that catches a tiebreak that is present but not deterministic.
func TestIntegration_ProductPagingIsStableAcrossCalls(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := NewService(NewRepository(pool))

	name := "ZZZ Stable Order Fixture"
	seedTiedNames(t, pool, name, 12)

	pageAt := func(offset int) []string {
		rows, err := svc.List(ctx, Filter{Q: name, Limit: 5, Offset: offset})
		if err != nil {
			t.Fatalf("page at %d: %v", offset, err)
		}
		out := make([]string, 0, len(rows))
		for _, p := range rows {
			out = append(out, p.ID)
		}
		return out
	}
	first, second := pageAt(5), pageAt(5)
	if len(first) != 5 {
		t.Fatalf("page at offset 5 returned %d rows, want 5", len(first))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("the same page differed between two calls at row %d (%s vs %s), the sort is not deterministic", i, first[i], second[i])
		}
	}
}

// The compatibility promise, against the real repository: no limit parameter, whole catalogue. This
// is the read the .xlsx export makes, and it must not shrink.
func TestIntegration_ProductListWithoutALimitReturnsTheWholeCatalogue(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := NewService(NewRepository(pool))

	var dbTotal int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM products WHERE is_active`).Scan(&dbTotal); err != nil {
		t.Fatalf("count active products: %v", err)
	}
	if dbTotal < 2 {
		t.Fatalf("the seed has %d active products; this test needs a catalogue", dbTotal)
	}

	all, err := svc.List(ctx, Filter{ActiveOnly: true})
	if err != nil {
		t.Fatalf("unpaged list: %v", err)
	}
	if len(all) != dbTotal {
		t.Fatalf("unpaged list returned %d of %d active products, zero limit must still mean the whole catalogue (the .xlsx export reads exactly this way)", len(all), dbTotal)
	}

	total, err := svc.Count(ctx, Filter{ActiveOnly: true})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != dbTotal {
		t.Fatalf("Count(active) = %d, want %d", total, dbTotal)
	}
}

// Count is the PRE-limit total against the real query, and it is built from the same WHERE as the
// page, a count computed from a different definition of "matching" is worse than no count.
func TestIntegration_ProductCountIsThePreLimitTotalOfTheSameFilter(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := NewService(NewRepository(pool))

	name := "ZZZ Count Fixture"
	seedTiedNames(t, pool, name, 7)

	rows, err := svc.List(ctx, Filter{Q: name, Limit: 3})
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("limit=3 returned %d rows", len(rows))
	}
	total, err := svc.Count(ctx, Filter{Q: name, Limit: 3})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 7 {
		t.Fatalf("Count for the filter = %d, want 7 (the total the pager needs, not the size of the page)", total)
	}
}
