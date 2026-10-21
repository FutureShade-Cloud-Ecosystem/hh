//go:build integration

// browse_out_at_integration_test.go, the browse row's out-at names (change 5 slice S4, ruling R1's
// Sales stock lookup). The browse row is a roll-up over every branch, so a list line that says
// "Out at <branch>" needs the names on the row itself: the per branch read is one call per product,
// and a lookup list is one line per product. The row carries the names of the branches whose own
// stock record shows nothing available (on hand at or under what is reserved), byte-absent when the
// product is available at every branch that holds a record for it.
//
//	Run: DATABASE_URL=... go test -tags integration ./internal/inventory/ -run TestIntegration_Browse_RowNamesTheBranchesItIsOutOf -count=1
package inventory

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/branches"
	"github.com/jackc/pgx/v5/pgxpool"
)

// mkOutAtFixSet plants one product with a stock record at two nonce branches, available at the
// first and out at the second, beside a second product with no stock record at all, and returns
// the nonce manufacturer both carry, the ids, and the out branch's name. Removed at teardown;
// nothing else reads a nonce sku.
func mkOutAtFixSet(t *testing.T, pool *pgxpool.Pool) (manufacturer, inStockID, outID, outBranchName string) {
	t.Helper()
	ctx := context.Background()
	nonce := fmt.Sprintf("OUTAT-%d", time.Now().UnixNano())
	manufacturer = nonce
	// The branch_id self-FK: pointed at a seeded branch, the way the branches tests plant theirs;
	// the 0003 CHECK couples loc_type to parent_id alone.
	var anySeedBranch string
	if err := pool.QueryRow(ctx,
		`SELECT id FROM locations WHERE loc_type = 'BRANCH' ORDER BY code LIMIT 1`).Scan(&anySeedBranch); err != nil {
		t.Fatalf("read a seeded branch for the fixture's branch_id: %v", err)
	}
	plantBranch := func(code, name string) string {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO locations (branch_id, loc_type, code, name)
			VALUES ($1::uuid, 'BRANCH', $2, $3) RETURNING id::text`, anySeedBranch, code, name).Scan(&id); err != nil {
			t.Fatalf("insert out-at branch fixture %q: %v", name, err)
		}
		return id
	}
	stockedAt := plantBranch(nonce+"-S", "Out at fixture stocked yard")
	outAt := plantBranch(nonce+"-O", "Out at fixture empty yard")
	plantProduct := func(sku string) string {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO products (sku, name, base_uom, price_uom, manufacturer, is_active)
			VALUES ($1, $2, 'EA', 'EA', $3, true) RETURNING id::text`, sku, sku, nonce).Scan(&id); err != nil {
			t.Fatalf("insert out-at product fixture %q: %v", sku, err)
		}
		return id
	}
	inStockID = plantProduct(nonce + "-P1")
	outID = plantProduct(nonce + "-P2")
	balance := func(product, branch string, onHand, reserved string) {
		if _, err := pool.Exec(ctx, `
			INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base, qty_reserved)
			VALUES ($1::uuid, $2::uuid, $2::uuid, $3::numeric, $4::numeric)`, product, branch, onHand, reserved); err != nil {
			t.Fatalf("insert out-at balance fixture: %v", err)
		}
	}
	balance(inStockID, stockedAt, "400", "60")
	balance(inStockID, outAt, "0", "0")
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM stock_balances WHERE product_id IN (SELECT id FROM products WHERE manufacturer = $1)`,
			`DELETE FROM products WHERE manufacturer = $1`,
			`DELETE FROM locations WHERE code LIKE $1 || '-%'`,
		} {
			if _, err := pool.Exec(context.Background(), sql, nonce); err != nil {
				t.Logf("cleanup of out-at fixture %s failed: %v", nonce, err)
			}
		}
	})
	return manufacturer, inStockID, outID, "Out at fixture empty yard"
}

// TestIntegration_Browse_RowNamesTheBranchesItIsOutOf, the two shapes a lookup list has to tell
// apart: available somewhere (the names name only the branches that are out, if any) and no stock
// record anywhere (no names at all, the app says out of stock in its own words).
func TestIntegration_Browse_RowNamesTheBranchesItIsOutOf(t *testing.T) {
	pool := intPool(t)
	svc := newIntSvc(pool)
	ctx := context.Background()
	org := stampTestOrg(t, pool)

	manufacturer, inStockID, outID, outBranchName := mkOutAtFixSet(t, pool)
	// The fixture's own yard has no org; the browse holds to the caller's org, so it joins the leg's org.
	if _, err := pool.Exec(ctx, `UPDATE locations SET org_id = $1 WHERE loc_type = 'BRANCH' AND org_id IS NULL`, org); err != nil {
		t.Fatalf("stamp the fixture yard: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `UPDATE locations SET org_id = NULL WHERE org_id = $1`, org)
	})
	// The composition root binds the branches seam (the branch rows' owner); the test binds the
	// real service over the same pool, the names coming from the branch rows the seed planted.
	svc = svc.WithBranchNameReader(branches.NewService(branches.NewRepository(pool)))
	res, err := svc.Browse(ctx, BrowseFilter{Text: manufacturer, OrgID: org, Limit: 200})
	if err != nil {
		t.Fatalf("browse the out-at fixtures: %v", err)
	}
	byProduct := map[string]BrowseRow{}
	for _, r := range res.Rows {
		byProduct[r.ProductID] = r
	}
	balanced, ok := byProduct[inStockID]
	if !ok {
		t.Fatalf("browse did not answer the product that holds stock at two branches")
	}
	if !slices.Contains(balanced.OutAtBranches, outBranchName) {
		t.Fatalf("the row's out-at names %v do not name the branch whose record shows nothing available (%q)", balanced.OutAtBranches, outBranchName)
	}
	if len(balanced.OutAtBranches) != 1 {
		t.Fatalf("the row's out-at names %v name a branch that still has stock", balanced.OutAtBranches)
	}
	empty, ok := byProduct[outID]
	if !ok {
		t.Fatalf("browse did not answer the product with no stock record")
	}
	if len(empty.OutAtBranches) != 0 {
		t.Fatalf("a product with no stock record at all carries out-at names %v; it is out everywhere and the screen says so in its own words", empty.OutAtBranches)
	}
}
