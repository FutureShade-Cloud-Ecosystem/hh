//go:build integration

// browse_like_integration_test.go, the browse surface's text filter against real Postgres.
//
// The filter binds f.Text into the trigram-layer ILIKE, and a bound pattern is still a pattern: on
// main a caller's '%' and '_' act as wildcards inside it, and a '\' is consumed as the pattern's
// own escape character (Postgres's default). Each leg plants a product whose NAME carries the term
// literally beside a decoy whose name differs only in the character a wildcard would have stood in
// for, both under one nonce manufacturer so the match set is exactly this test's rows, and asks for
// the term. The escape must not disturb the indexed expression itself: the ESCAPE clause changes
// how the pattern is read, not the expression migration 0118 wraps the GIN index around.
//
//	Run: DATABASE_URL=... go test -tags integration ./internal/inventory/ -run TestIntegration_Browse_TextIsLiteral -count=1
package inventory

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// mkLikeFixSet plants one literal product and one wildcard decoy under a nonce manufacturer and
// returns their ids. Removed at teardown; the rows carry no stock ledger, so nothing else can be
// perturbed by them existing briefly.
func mkLikeFixSet(t *testing.T, pool *pgxpool.Pool, litName, decName string) (manufacturer string, litID, decID string) {
	t.Helper()
	ctx := context.Background()
	manufacturer = fmt.Sprintf("LIKEFIX-%d", time.Now().UnixNano())
	plant := func(sku, name string) string {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO products (sku, name, base_uom, price_uom, manufacturer, is_active)
			VALUES ($1, $2, 'EA', 'EA', $3, true) RETURNING id::text`, sku, name, manufacturer).Scan(&id); err != nil {
			t.Fatalf("insert browse like fixture %q: %v", name, err)
		}
		return id
	}
	litID = plant(manufacturer+"-LIT", litName)
	decID = plant(manufacturer+"-DEC", decName)
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM products WHERE manufacturer = $1`, manufacturer); err != nil {
			t.Logf("cleanup of browse like fixture %s failed: %v", manufacturer, err)
		}
	})
	return manufacturer, litID, decID
}

// browseTermIsLiteral runs one browse and holds it to the literal-only contract: the literal
// product is in the page, the decoy is not, the pre-limit total counts only the literal row, and
// every returned row's whole text layer (sku + name + manufacturer + collection) contains the term
// as text.
func browseTermIsLiteral(t *testing.T, svc *Service, ctx context.Context, manufacturer, term, litID, decID string) {
	t.Helper()
	res, err := svc.Browse(ctx, BrowseFilter{Text: term, Manufacturer: []string{manufacturer}, Limit: 200})
	if err != nil {
		t.Fatalf("browse %q: %v", term, err)
	}
	var haveLit, haveDec bool
	for _, r := range res.Rows {
		if r.ProductID == litID {
			haveLit = true
		}
		if r.ProductID == decID {
			haveDec = true
		}
		layer := r.SKU + " " + r.Name + " " + r.Manufacturer + " " + r.Collection
		if !strings.Contains(layer, term) {
			t.Fatalf("browse %q returned a row whose text layer contains none of it: %q", term, layer)
		}
	}
	if !haveLit {
		t.Fatalf("browse %q did not find the product whose name contains it literally", term)
	}
	if haveDec {
		t.Fatalf("browse %q matched a row the term fits only as a wildcard, not as text", term)
	}
	if res.Total != 1 {
		t.Fatalf("browse %q total = %d, want 1, the pre-limit count says the wildcard leaked past the page", term, res.Total)
	}
}

// TestIntegration_Browse_TextIsLiteral, the three metacharacter classes a bound pattern can
// misread: % and _ (wildcards on main) and the escape character itself.
func TestIntegration_Browse_TextIsLiteral(t *testing.T) {
	pool := intPool(t)
	svc := newIntSvc(pool)
	ctx := context.Background()

	cases := []struct {
		name  string
		term  string
		lit   string
		decoy string
	}{
		{"a percent sign in the term is text, not any-run", "50%", "Paver 50% Blend", "Paver 501 Blend"},
		{"an underscore in the term is text, not any-one", "A_1", "Paver A_1 Blend", "Paver AB1 Blend"},
		{"the escape character in the term is text too", `Slab\2`, `Paver Slab\2 Blend`, "Paver Slab2 Blend"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manufacturer, litID, decID := mkLikeFixSet(t, pool, tc.lit, tc.decoy)
			browseTermIsLiteral(t, svc, ctx, manufacturer, tc.term, litID, decID)
		})
	}
}
