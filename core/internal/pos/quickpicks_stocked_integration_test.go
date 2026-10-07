//go:build integration

package pos

// quickpicks_stocked_integration_test.go: the grid's category representatives are a DB rule (one
// product per application, only where available stock exists), so it is proven against a real
// database rather than a fake. The test plants its OWN products under application words no seed uses,
// so it proves its claim on a migrated database and on a migrated and seeded one alike: a seeded
// world stocks nearly every category, and a claim about "a category with nothing available" can only
// be made about a category the test itself owns.
// Run with: make test-integration (after make reset).

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

	// The test's own categories, in words no seed or catalogue uses, with their own SKUs.
	const (
		stockedApp  = "Zz Round9 Stocked"
		reservedApp = "Zz Round9 Reserved"
		deletedApp  = "Zz Round9 Deleted"
	)
	var created []string
	plant := func(sku, application string) string {
		var id string
		err := pool.QueryRow(ctx, `
INSERT INTO products (sku, name, base_uom, price_uom, application, is_active)
VALUES ($1::text, $1::text, 'EA', 'EA', $2::text, true) RETURNING id::text`, sku, application).Scan(&id)
		if err != nil {
			t.Fatalf("plant %s: %v", sku, err)
		}
		created = append(created, id)
		return id
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM stock_balances WHERE product_id = ANY($1::uuid[])`, created)
		_, _ = pool.Exec(ctx, `DELETE FROM products WHERE id = ANY($1::uuid[])`, created)
	})
	stock := func(product string, onHand, reserved int, deleted bool) {
		del := "NULL"
		if deleted {
			del = "now()"
		}
		_, err := pool.Exec(ctx, `
INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base, qty_reserved, deleted_at)
VALUES ($1, $2, $2, $3, $4, `+del+`)`, product, loc, onHand, reserved)
		if err != nil {
			t.Fatalf("seed stock: %v", err)
		}
	}

	stockedA := plant("ZZR9-STOCKED-A", stockedApp)
	stockedB := plant("ZZR9-STOCKED-B", stockedApp) // a second product in the category: still one tile
	reserved := plant("ZZR9-RESERVED", reservedApp)
	deleted := plant("ZZR9-DELETED", deletedApp)
	stock(stockedA, 40, 0, false)
	stock(stockedB, 5, 0, false)
	stock(reserved, 3, 3, false) // fully reserved: nothing available
	stock(deleted, 40, 0, true)  // a soft deleted balance is not stock

	rows, err := repo.InStockApplicationRepresentatives(ctx)
	if err != nil {
		t.Fatalf("representatives: %v", err)
	}
	seen := map[string]int{}
	for _, r := range rows {
		seen[strings.ToLower(strings.TrimSpace(r.Application))]++
	}
	if got := seen[strings.ToLower(stockedApp)]; got != 1 {
		t.Fatalf("a stocked category gets exactly one tile, got %d: %v", got, seen)
	}
	if got := seen[strings.ToLower(reservedApp)]; got != 0 {
		t.Fatalf("a category with nothing available (fully reserved) shows no tile: %v", seen)
	}
	if got := seen[strings.ToLower(deletedApp)]; got != 0 {
		t.Fatalf("a category whose only balance is soft deleted shows no tile: %v", seen)
	}
	// And on any world: one representative per category, never two.
	for app, n := range seen {
		if n != 1 {
			t.Fatalf("%s appears %d times: one representative each", app, n)
		}
	}
}
