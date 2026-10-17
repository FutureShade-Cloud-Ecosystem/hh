//go:build integration

// stock_lookup_like_integration_test.go, the product resolver's name prefix arm against real Postgres.
//
// The resolver's third attempt binds the typed reference into `p.name ILIKE $1 || '%'`, and a bound
// pattern is still a pattern: before the escape a typed '%' or '_' acted as a wildcard, and because
// the arm is LIMIT 1 ordered by sku the wildcard resolved to whichever fitting product's sku sorted
// first. The callers put the resolved product on a stock lookup and a reorder action, so the
// contract under test is the resolver's own: a wildcard reference resolves to NOT FOUND unless a
// product literally carrying that text exists, and a literal match resolves to that product.
//
//	Run: DATABASE_URL=... go test -tags integration ./internal/inventory/ -run TestIntegration_ResolveProductRef_NamePrefixIsLiteral -count=1
package inventory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// stockLikeNonce is 8 hex characters of entropy with no clock in it, enough to keep this file's
// terms off every other fixture's rows on the shared gate database.
func stockLikeNonce() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "fixed"
	}
	return hex.EncodeToString(b[:])
}

// plantStockLikeProduct plants one product and returns its id. The stock ledger references products
// ON DELETE RESTRICT, so the ledger rows are cleared first at teardown exactly as mkProduct does,
// even though this file plants none.
func plantStockLikeProduct(t *testing.T, pool *pgxpool.Pool, sku, name string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO products (sku, name, base_uom, price_uom, is_active)
		VALUES ($1, $2, 'EA', 'EA', true) RETURNING id::text`, sku, name).Scan(&id); err != nil {
		t.Fatalf("plant product %q: %v", name, err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM stock_balances WHERE product_id = $1::uuid`, id)
		_, _ = pool.Exec(c, `DELETE FROM stock_moves WHERE product_id = $1::uuid`, id)
		_, _ = pool.Exec(c, `DELETE FROM product_branch_costs WHERE product_id = $1::uuid`, id)
		_, _ = pool.Exec(c, `DELETE FROM products WHERE id = $1::uuid`, id)
	})
	return id
}

// TestIntegration_ResolveProductRef_NamePrefixIsLiteral, the metacharacter classes a bound pattern
// can misread, in both shapes: with no literal match in the catalogue (the reference must resolve to
// not found) and with a literal match planted beside the decoy (the reference must resolve to the
// literal product). The literal's sku starts "ZZZ" and the decoy's "AAA" so that on the unescaped
// query, where both fit the pattern and ORDER BY p.is_active DESC, p.sku decides, the decoy
// deterministically wins: that is the wrong-product resolution this test exists to close.
func TestIntegration_ResolveProductRef_NamePrefixIsLiteral(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	notFound := []struct {
		name  string
		term  string
		decoy string
	}{
		{"a percent sign with no literal match resolves to not found",
			"STLK-%s 50%%", "STLK-%s 501 Paver"},
		{"an underscore with no literal match resolves to not found",
			"STLK-%s A_1", "STLK-%s AB1 Paver"},
		{"the escape character with no literal match resolves to not found",
			`STLK-%s Slab\2`, "STLK-%s Slab2 Paver"},
	}
	for _, tc := range notFound {
		t.Run(tc.name, func(t *testing.T) {
			tag := stockLikeNonce()
			term := fmt.Sprintf(tc.term, tag)
			decoy := fmt.Sprintf(tc.decoy, tag)
			plantStockLikeProduct(t, pool, "AAA-STLK-"+tag, decoy)
			if _, found, err := repo.ResolveProductRef(ctx, term); err != nil {
				t.Fatalf("resolve %q: %v", term, err)
			} else if found {
				t.Fatalf("resolve %q found a product, want not found: no product's name carries the "+
					"reference as text, only the decoy %q fits it as a wildcard", term, decoy)
			}
		})
	}

	wrongProduct := []struct {
		name    string
		term    string
		literal string
		decoy   string
	}{
		{"a percent sign with a literal match resolves to that product, not the decoy",
			"STLK-%s 50%%", "STLK-%s 50%% Paver", "STLK-%s 501 Paver"},
		{"an underscore with a literal match resolves to that product, not the decoy",
			"STLK-%s A_1", "STLK-%s A_1 Paver", "STLK-%s AB1 Paver"},
	}
	for _, tc := range wrongProduct {
		t.Run(tc.name, func(t *testing.T) {
			tag := stockLikeNonce()
			term := fmt.Sprintf(tc.term, tag)
			litID := plantStockLikeProduct(t, pool, "ZZZ-STLK-"+tag, fmt.Sprintf(tc.literal, tag))
			plantStockLikeProduct(t, pool, "AAA-STLK-"+tag, fmt.Sprintf(tc.decoy, tag))
			got, found, err := repo.ResolveProductRef(ctx, term)
			if err != nil {
				t.Fatalf("resolve %q: %v", term, err)
			}
			if !found {
				t.Fatalf("resolve %q did not resolve, want the product whose name carries it as text", term)
			}
			if got.ProductID != litID {
				t.Fatalf("resolve %q landed on %q (%q), want the literal product %q: a wildcard "+
					"reference must never resolve onto a different product", term, got.ProductID, got.Name, fmt.Sprintf(tc.literal, tag))
			}
		})
	}

	t.Run("a reference with no metacharacter still resolves", func(t *testing.T) {
		tag := stockLikeNonce()
		litID := plantStockLikeProduct(t, pool, "ZZZ-STLK-"+tag, "STLK-"+tag+" Blu Paver")
		if got, found, err := repo.ResolveProductRef(ctx, "STLK-"+tag+" Blu"); err != nil || !found {
			t.Fatalf("found=%v err=%v, a plain name prefix must keep resolving", found, err)
		} else if got.ProductID != litID {
			t.Fatalf("resolved to %q, want %q", got.ProductID, litID)
		}
	})
}
