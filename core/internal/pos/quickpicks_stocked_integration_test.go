//go:build integration

package pos

// quickpicks_stocked_integration_test.go: the grid's category representatives are a DB rule (one
// product per application, only where available stock exists), so it is proven against the seeded
// catalogue rather than a fake. Run with: make test-integration (after make reset).

import (
	"context"
	"strings"
	"testing"
)

func TestIntegration_InStockApplicationRepresentatives_OneEachWhereStockExists(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	var loc string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations ORDER BY id LIMIT 1`).Scan(&loc); err != nil {
		t.Skipf("no location on this database: %v", err)
	}
	pick := func(application string) string {
		var id string
		err := pool.QueryRow(ctx, `SELECT id::text FROM products WHERE is_active AND application = $1 ORDER BY sku LIMIT 1`, application).Scan(&id)
		if err != nil {
			t.Skipf("no active %s product on this database: %v", application, err)
		}
		return id
	}
	paver, mulch, soil := pick("Paver"), pick("Mulch"), pick("Soil")
	put := func(product string, onHand, reserved int, deleted bool) {
		del := "NULL"
		if deleted {
			del = "now()"
		}
		_, err := pool.Exec(ctx, `
INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base, qty_reserved, deleted_at)
VALUES ($1, $2, $2, $3, $4, `+del+`)
ON CONFLICT (product_id, location_id) DO UPDATE
   SET on_hand_base = $3, qty_reserved = $4, deleted_at = `+del, product, loc, onHand, reserved)
		if err != nil {
			t.Fatalf("seed stock: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM stock_balances WHERE product_id = ANY($1::uuid[]) AND location_id = $2::uuid`, []string{paver, mulch, soil}, loc)
	})
	put(paver, 40, 0, false) // stocked
	put(mulch, 5, 0, false)  // stocked
	put(soil, 3, 3, false)   // fully reserved: nothing available
	_ = soil

	rows, err := repo.InStockApplicationRepresentatives(ctx)
	if err != nil {
		t.Fatalf("representatives: %v", err)
	}
	seen := map[string]int{}
	for _, r := range rows {
		seen[strings.ToLower(strings.TrimSpace(r.Application))]++
	}
	if seen["paver"] != 1 || seen["mulch"] != 1 {
		t.Fatalf("one tile per stocked category, Paver and Mulch both: %v", seen)
	}
	if seen["soil"] != 0 {
		t.Fatalf("a category with nothing available shows no tile: %v", seen)
	}
	for app, n := range seen {
		if n != 1 {
			t.Fatalf("%s appears %d times: one representative each", app, n)
		}
	}

	// A deleted stock row is not stock.
	put(paver, 40, 0, true)
	rows, err = repo.InStockApplicationRepresentatives(ctx)
	if err != nil {
		t.Fatalf("representatives after delete: %v", err)
	}
	for _, r := range rows {
		if strings.EqualFold(strings.TrimSpace(r.Application), "paver") {
			t.Fatalf("a soft deleted balance counted as stock: %s", r.SKU)
		}
	}
}
