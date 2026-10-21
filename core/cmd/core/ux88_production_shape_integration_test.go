//go:build integration

package main

// ux88_production_shape_integration_test.go, UX-88 round 4: a production shaped box. No demo seed, no
// testkit world: the migrations' own two branches (Trenton and Kingston) with no org on them, one org
// the way a dealer's box gets it, and the dealer's own staff reading. Every UX-88 read holds to the
// caller's org through the location's org_id, so a box whose branches carry none answers empty to
// everyone; the identity populate and migration 0421 are the two ways such a box takes the org, and
// each is proved here to leave the reads answering.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/branches"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
)

const (
	prodTrenton  = "5d1b3f6e-0000-4000-9000-000000000001"
	prodKingston = "5d1b3f6e-0000-4000-9000-000000000002"
)

// migration0421 reads the 0421 migration's body from the migrations directory; a missing file fails the
// leg, so the number cannot be dropped silently.
func migration0421(t *testing.T) string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "db", "migrations", "0421_*.sql"))
	if err != nil || len(files) != 1 {
		t.Fatalf("want exactly one 0421 migration, got %v (%v)", files, err)
	}
	body, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read %s: %v", files[0], err)
	}
	return string(body)
}

// prodOrgless counts the locations that carry no org.
func prodOrgless(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM locations WHERE org_id IS NULL`).Scan(&n); err != nil {
		t.Fatalf("count the org-less locations: %v", err)
	}
	return n
}

func prodResetOrgs(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slugs ...string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `UPDATE locations SET org_id = NULL`); err != nil {
		t.Fatalf("clear the locations' org: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM org`); err != nil {
		t.Fatalf("clear the orgs: %v", err)
	}
	for _, s := range slugs {
		if _, err := pool.Exec(ctx, `INSERT INTO org (slug, name) VALUES ($1::citext, $1::text)`, s); err != nil {
			t.Fatalf("plant org %s: %v", s, err)
		}
	}
}

// The three cases of the migration. Idempotent: a second run changes nothing.
func TestUX88_Migration0421_TakesTheOnlyOrg(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "ux88m0421")
	body := migration0421(t)
	run := func() {
		t.Helper()
		if _, err := pool.Exec(ctx, body); err != nil {
			t.Fatalf("0421 failed: %v", err)
		}
	}
	var total int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM locations`).Scan(&total); err != nil || total < 2 {
		t.Fatalf("locations = %d (%v), want the migration planted branches", total, err)
	}

	// Zero orgs: untouched.
	prodResetOrgs(t, ctx, pool)
	run()
	if n := prodOrgless(t, ctx, pool); n != total {
		t.Fatalf("zero orgs: %d of %d locations org-less, want all %d untouched", n, total, total)
	}
	// Two orgs: untouched, and no org is chosen for the box.
	prodResetOrgs(t, ctx, pool, "ux88-a", "ux88-b")
	run()
	if n := prodOrgless(t, ctx, pool); n != total {
		t.Fatalf("two orgs: %d of %d locations org-less, want all %d untouched", n, total, total)
	}
	// One org: every org-less location takes it; a location already in an org keeps its own.
	prodResetOrgs(t, ctx, pool, "ux88-only")
	if _, err := pool.Exec(ctx, `
		INSERT INTO locations (id, branch_id, parent_id, loc_type, code, name, address, org_id)
		SELECT '88a00000-0000-4000-8000-0000000000c9', '88a00000-0000-4000-8000-0000000000c9', NULL, 'BRANCH', 'UX88OWN', 'Own org branch', '{}', o.id
		  FROM org o`); err != nil {
		t.Fatalf("plant a branch that already has the org: %v", err)
	}
	run()
	if n := prodOrgless(t, ctx, pool); n != 0 {
		t.Fatalf("one org: %d locations still org-less, want none", n)
	}
	var other int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM locations l JOIN org o ON o.id = l.org_id WHERE o.slug = 'ux88-only'`).Scan(&other); err != nil || other != total+1 {
		t.Fatalf("locations in the only org = %d (%v), want %d", other, err, total+1)
	}
	// Idempotent: a rerun changes nothing, deletes nothing.
	var before, after string
	snap := `SELECT coalesce(string_agg(l.id::text || ':' || l.org_id::text || ':' || l.updated_at::text, ',' ORDER BY l.id), '') FROM locations l`
	if err := pool.QueryRow(ctx, snap).Scan(&before); err != nil {
		t.Fatal(err)
	}
	run()
	if err := pool.QueryRow(ctx, snap).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("a second run of 0421 changed the locations:\n%s\n%s", before, after)
	}
	// A box with a second org later is left alone: the first org keeps its branches, nothing moves.
	if _, err := pool.Exec(ctx, `INSERT INTO org (slug, name) VALUES ('ux88-late', 'late')`); err != nil {
		t.Fatal(err)
	}
	run()
	if err := pool.QueryRow(ctx, snap).Scan(&after); err != nil || before != after {
		t.Fatalf("0421 with two orgs changed the locations (%v)", err)
	}
}

// prodReads asserts what the dealer's own staff are answered on a box where the org holds its branches:
// the stock glance, the availability peek, the named branch read, Browse and the branch list for a
// scoped caller and a GLOBAL caller of the org.
func prodReads(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID string) {
	t.Helper()
	var pid string
	if err := pool.QueryRow(ctx,
		`INSERT INTO products (sku, name, base_uom, price_uom) VALUES ($1::text, $1::text, 'EA', 'EA') RETURNING id::text`,
		fmt.Sprintf("ux88-prod-%d", time.Now().UnixNano())).Scan(&pid); err != nil {
		t.Fatalf("plant product: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM stock_balances WHERE product_id = $1`, pid)
		_, _ = pool.Exec(context.Background(), `DELETE FROM products WHERE id = $1`, pid)
	})
	for branch, qty := range map[string]int{prodTrenton: 11, prodKingston: 22} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base) VALUES ($1, $2, $2, $3)`, pid, branch, qty); err != nil {
			t.Fatalf("plant stock: %v", err)
		}
	}
	inv := inventory.NewService(inventory.NewRepository(pool))

	glance, err := pos.NewRepository(pool).ReadStockOnHand(ctx, orgID, pid)
	if err != nil || glance[prodTrenton] != 11 || glance[prodKingston] != 22 || len(glance) != 2 {
		t.Errorf("stock glance = %v (%v), want Trenton 11 and Kingston 22", glance, err)
	}
	peek, err := inv.AvailabilityPeek(ctx, orgID, pid)
	if err != nil || len(peek) != 2 {
		t.Errorf("availability peek = %+v (%v), want both branches", peek, err)
	}
	if row, err := inv.AvailabilityAtBranchInOrg(ctx, orgID, pid, prodTrenton); err != nil || row.OnHandBase != 11 {
		t.Errorf("branch stock at Trenton = %+v (%v), want 11", row, err)
	}
	all, err := inv.AvailabilityAllBranches(ctx, orgID, pid)
	if err != nil || len(all) != 2 {
		t.Errorf("branch stock, all branches = %+v (%v), want both", all, err)
	}
	var sku string
	if err := pool.QueryRow(ctx, `SELECT sku FROM products WHERE id = $1`, pid).Scan(&sku); err != nil {
		t.Fatal(err)
	}
	res, err := inv.Browse(ctx, inventory.BrowseFilter{Text: sku, OrgID: orgID})
	if err != nil || len(res.Rows) != 1 || res.Rows[0].OnHandBase != 33 {
		t.Errorf("browse = %+v (%v), want one row with 33 on hand", res.Rows, err)
	}
	br := branches.NewService(branches.NewRepository(pool))
	global, err := br.ListFor(ctx, branches.CallerScope{OrgID: orgID, AllBranches: true})
	if err != nil || len(global) != 2 {
		t.Errorf("a GLOBAL caller of the org reaches %+v (%v), want both branches", global, err)
	}
	scoped, err := br.ListFor(ctx, branches.CallerScope{OrgID: orgID, BranchIDs: []string{prodTrenton}})
	if err != nil || len(scoped) != 1 || scoped[0].ID != prodTrenton {
		t.Errorf("a scoped caller granted Trenton reaches %+v (%v), want Trenton alone", scoped, err)
	}
}

func TestUX88_ProductionShape_IdentityPopulateOnly(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "ux88prodpop")
	report, err := identity.Populate(ctx, pool, identity.PopulateConfig{OrgSlug: "ux88-dealer", OrgName: "UX88 Dealer"})
	if err != nil {
		t.Fatalf("identity populate: %v", err)
	}
	if n := prodOrgless(t, ctx, pool); n != 0 {
		t.Errorf("%d locations still carry no org after the identity populate, want none", n)
	}
	prodReads(t, ctx, pool, report.OrgID)
}

func TestUX88_ProductionShape_Migration0421Alone(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "ux88prod0421")
	// The box has its one org, created the way an operator or a provisioning step creates it, with no
	// populate run; the migration is the only thing that stamps the branches.
	var orgID string
	if err := pool.QueryRow(ctx, `INSERT INTO org (slug, name) VALUES ('ux88-dealer', 'UX88 Dealer') RETURNING id::text`).Scan(&orgID); err != nil {
		t.Fatalf("plant the org: %v", err)
	}
	if _, err := pool.Exec(ctx, migration0421(t)); err != nil {
		t.Fatalf("0421 failed: %v", err)
	}
	if n := prodOrgless(t, ctx, pool); n != 0 {
		t.Errorf("%d locations still carry no org after 0421, want none", n)
	}
	prodReads(t, ctx, pool, orgID)
}
