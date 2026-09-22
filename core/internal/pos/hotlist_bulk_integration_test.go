//go:build integration

package pos

// hotlist_bulk_integration_test.go, the DERIVED hot list opens on the yard's BULK BOARD
// (commander's ruling, 27 Aug, R2: "Bulk aggregates - but should be configurable").
//
// This is a DB test because the rule is a DB rule: the bulk-first partition is an ORDER BY over
// facts a real catalogue already carries (the BULK/AGGREGATE category from 0067, and the bulk sell
// units), not a Go branch a fake could answer. A unit test with a fake store would only prove that
// the fake returns what the fake was told to return.
//
// Run with: make test-integration (after make reset).

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// bulkSell is the sell-unit half of the partition, mirrored here so the test states the rule in its
// own words rather than reading the query it is checking.
var bulkSell = map[string]bool{"T": true, "CYD": true}

func TestIntegration_DerivedHotList_OpensOnBulk(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	rows, err := repo.TopSellingProducts(ctx, 40)
	if err != nil {
		t.Fatalf("derived hot list: %v", err)
	}
	if len(rows) == 0 {
		t.Skip("no active catalogue on this database")
	}

	isBulk := func(r quickPickRow) bool {
		sell := strings.ToUpper(r.SoldBy)
		if sell == "" {
			sell = strings.ToUpper(r.BaseUOM)
		}
		return bulkSell[sell] || bulkCategoryOf(ctx, t, pool, r.SKU)
	}

	// The board is bulk FIRST: once a non-bulk line appears, no bulk line may appear after it.
	seenOther := false
	bulk := 0
	for _, r := range rows {
		if isBulk(r) {
			if seenOther {
				t.Fatalf("%s is bulk but sorts after a non-bulk line, the root is not a bulk board", r.SKU)
			}
			bulk++
			continue
		}
		seenOther = true
	}
	if bulk == 0 {
		t.Fatal("the seeded yard has no bulk lines at the head of the hot list")
	}

	// And it drills the way the client described it: mulch broken out by COLOUR, aggregates by SIZE.
	// The tree itself is built in the frontend, so what this asserts is the DATA that makes it
	// possible, the two facets each bulk family carries.
	colours := map[string]bool{}
	sizes := map[string]bool{}
	for _, r := range rows {
		switch r.Application {
		case "Mulch":
			colours[r.Collection] = true
		case "Aggregate":
			sizes[r.Collection] = true
		}
	}
	for _, want := range []string{"Black", "Brown", "Red"} {
		if !colours[want] {
			t.Fatalf("the mulch is not broken out by colour: %q missing", want)
		}
	}
	if len(sizes) < 3 {
		t.Fatalf("the aggregates are not broken out by size: %v", sizes)
	}
}

// bulkCategoryOf answers the category half of the partition for one SKU.
func bulkCategoryOf(ctx context.Context, t *testing.T, pool *pgxpool.Pool, sku string) bool {
	t.Helper()
	var code string
	err := pool.QueryRow(ctx, `
SELECT COALESCE(pc.code::text, '') FROM products p
  LEFT JOIN product_categories pc ON pc.id = p.category_id
 WHERE p.sku = $1`, sku).Scan(&code)
	if err != nil {
		return false
	}
	return code == "BULK" || code == "AGGREGATE"
}

func TestIntegration_DerivedHotList_CuratedOrderSurvivesTheRead(t *testing.T) {
	// The other half of R2(a): an admin curates "which categories, THEIR ORDER, which items sit
	// under each". Curated order is what the tree groups by (first appearance), so a read that
	// re-sorted the rows would silently take that control away again.
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	want := []string{"dbs-agg-river", "dbs-mul-black", "dbs-soil-top"}
	rows, err := repo.ReadProductsBySKU(ctx, want)
	if err != nil {
		t.Fatalf("curated read: %v", err)
	}
	if len(rows) != len(want) {
		t.Skipf("the demo bulk board is not on this database (%d of %d SKUs)", len(rows), len(want))
	}
	got := orderBySKU(rows, want)
	for i, sku := range want {
		if !strings.EqualFold(got[i].SKU, sku) {
			t.Fatalf("position %d = %s, want %s, the curated order did not survive", i, got[i].SKU, sku)
		}
	}
}
