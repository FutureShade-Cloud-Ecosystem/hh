//go:build integration

// addon_branchprice_integration_test.go: the add-on tile read (the DIB-502
// strip's store read) is branch aware, LD-7 round 2. A tile at a branch whose
// price stands shows the price the customer pays there; at a branch without
// one, and with no branch named, it shows the dealer-wide anchor as before.
//
//	Run with:
//
//	DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:5716/ld7prices?sslmode=disable' \
//	  go test -tags integration -p 1 ./internal/pos/ -run TestIntegration_AddonCandidates
package pos

import (
	"context"
	"testing"
)

func TestIntegration_AddonCandidatesShowTheBranchPrice(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	const app = "fxbpaddon"

	// The branch the price stands at is one of the migration's own seeded
	// branches, read off the database (the fixture carries no branch ids of
	// its own in this suite).
	var branchA, branchB string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM locations WHERE loc_type = 'BRANCH' ORDER BY code LIMIT 1`).Scan(&branchA); err != nil {
		t.Fatalf("read a seeded branch: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM locations WHERE loc_type = 'BRANCH' AND id <> $1::uuid LIMIT 1`, branchA).Scan(&branchB); err != nil {
		t.Fatalf("read a second seeded branch: %v", err)
	}
	pBranch := "41bd0000-0000-4000-8000-000000000001"
	pWide := "41bd0000-0000-4000-8000-000000000002"
	for _, id := range []string{pBranch, pWide} {
		if _, err := pool.Exec(ctx, `
INSERT INTO products (id, sku, name, base_uom, price_uom, is_active, application)
VALUES ($1::uuid, $2::citext, $3, 'EA', 'EA', TRUE, $4)`,
			id, "FXBPADDON-"+id[len(id)-1:], "branch price addon leg product", app); err != nil {
			t.Fatalf("seed product: %v", err)
		}
		t.Cleanup(func() {
			c := context.Background()
			_, _ = pool.Exec(c, `DELETE FROM pricing_branch_prices WHERE product_id = $1::uuid`, id)
			_, _ = pool.Exec(c, `DELETE FROM pricing_list_prices WHERE product_id = $1::uuid`, id)
			_, _ = pool.Exec(c, `DELETE FROM products WHERE id = $1::uuid`, id)
		})
	}
	// Dealer-wide anchors on both, each drawn from the price_matrix allocator
	// so the rows are stamped the way the seed stamps them.
	for _, pc := range []struct {
		id    string
		cents int64
	}{{pBranch, 10000}, {pWide, 12000}} {
		if _, err := pool.Exec(ctx, `
WITH pseq AS (
	INSERT INTO scope_sequences (scope) VALUES ('price_matrix')
	ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
	RETURNING next_value
)
INSERT INTO pricing_list_prices (product_id, list_price_cents, price_uom, seq)
VALUES ($1::uuid, $2, 'EA', (SELECT next_value FROM pseq))
ON CONFLICT (product_id) DO UPDATE SET list_price_cents = $2`,
			pc.id, pc.cents); err != nil {
			t.Fatalf("seed anchor: %v", err)
		}
	}
	// A branch price stands on one product at north A only, drawn from the
	// branch_prices allocator the editor writes draw from.
	if _, err := pool.Exec(ctx, `
WITH bseq AS (
	INSERT INTO scope_sequences (scope) VALUES ('branch_prices')
	ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
	RETURNING next_value
)
INSERT INTO pricing_branch_prices (product_id, branch_id, list_price_cents, price_uom, seq)
VALUES ($1::uuid, $2::uuid, 48000, 'EA', (SELECT next_value FROM bseq))
ON CONFLICT (product_id, branch_id) DO UPDATE SET list_price_cents = 48000, deleted_at = NULL`,
		pBranch, branchA); err != nil {
		t.Fatalf("seed branch price: %v", err)
	}

	byID := func(cands []addonCandidate) map[string]addonCandidate {
		out := map[string]addonCandidate{}
		for _, c := range cands {
			out[c.ProductID] = c
		}
		return out
	}

	// At the branch whose price stands: that price is what the tile shows.
	cands, err := repo.ListAddonCandidates(ctx, []string{app}, []string{}, 10, branchA)
	if err != nil {
		t.Fatalf("addon candidates at north A: %v", err)
	}
	got := byID(cands)
	if c, ok := got[pBranch]; !ok || c.ListPriceCents == nil || *c.ListPriceCents != 48000 || c.PriceUOM != "EA" {
		t.Fatalf("at the branch with a branch price the tile reads %+v, want 48000 per EA: the tile must show the price the customer pays there", c)
	}
	if c, ok := got[pWide]; !ok || c.ListPriceCents == nil || *c.ListPriceCents != 12000 {
		t.Fatalf("at the branch without one the tile reads %+v, want the dealer-wide 12000", c)
	}

	// At a branch without any branch price: the dealer-wide answer.
	cands, err = repo.ListAddonCandidates(ctx, []string{app}, []string{}, 10, branchB)
	if err != nil {
		t.Fatalf("addon candidates at north B: %v", err)
	}
	got = byID(cands)
	if c, ok := got[pBranch]; !ok || c.ListPriceCents == nil || *c.ListPriceCents != 10000 {
		t.Fatalf("at the branch without any branch price the tile reads %+v, want the dealer-wide 10000", c)
	}

	// With no branch named at all: unchanged, the dealer-wide answer.
	cands, err = repo.ListAddonCandidates(ctx, []string{app}, []string{}, 10)
	if err != nil {
		t.Fatalf("addon candidates with no branch: %v", err)
	}
	got = byID(cands)
	if c, ok := got[pBranch]; !ok || c.ListPriceCents == nil || *c.ListPriceCents != 10000 {
		t.Fatalf("with the branch argument absent the tile reads %+v, want the unchanged dealer-wide 10000", c)
	}
}
