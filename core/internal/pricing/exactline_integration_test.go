//go:build integration

// exactline_integration_test.go, the relay's case through the REAL resolver with a REAL unit
// set (decision log row 820). The unit-test leg (exactline_test.go) proves the arm over fakes;
// this leg proves the wiring: the product factor reaches the resolver through the product
// module's read seam exactly as production wires it (wire_money.go: the product service into
// WithUOMConverter), against a product_uoms row stored as DECIMAL(19,8) text.
//
// RED FIRST: seen red on main (61, want 62) before the exact path landed.
package pricing

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// exactFixture plants an invented pieces-per-box product: base PC, BOX at qty_in_base 100,
// with a 15-cents-per-BOX retail anchor. Invented fixtures only, the SKU is unique per run,
// and cleanup removes the anchor, the unit rows and the product.
func exactFixture(t *testing.T, pool *pgxpool.Pool) (productID string) {
	t.Helper()
	ctx := context.Background()
	sku := fmt.Sprintf("EXACTLINE-%d", time.Now().UnixNano())
	if err := pool.QueryRow(ctx, `
		INSERT INTO products (sku, name, base_uom, price_uom)
		VALUES ($1, 'Exact line fixture (load test)', 'PC', 'BOX')
		RETURNING id::text`, sku).Scan(&productID); err != nil {
		t.Fatalf("seed fixture product: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO product_uoms (product_id, uom_code, qty_in_base, is_base)
		VALUES ($1::uuid, 'PC', 1, true), ($1::uuid, 'BOX', 100, false)`, productID); err != nil {
		t.Fatalf("seed fixture units: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO pricing_list_prices (product_id, list_price_cents, price_uom)
		VALUES ($1::uuid, 15, 'BOX')`, productID); err != nil {
		t.Fatalf("seed fixture anchor: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM pricing_list_prices WHERE product_id=$1::uuid`, productID)
		_, _ = pool.Exec(c, `DELETE FROM products WHERE id=$1::uuid`, productID) // unit rows cascade
	})
	return productID
}

// TestIntegration_ExactLineTotal_RelayCase prices 410 PC at 15 cents per BOX of 100 through
// the real resolver and the real unit set: the line total must be 62 (the exact total 61.5,
// half away from zero), not the float's 61. The neighbouring whole-box lines pin that the
// away-from-edge answers did not move.
func TestIntegration_ExactLineTotal_RelayCase(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := realSvc(pool)
	pid := exactFixture(t, pool)

	bd, err := svc.ResolvePrice(ctx, ResolveInput{
		CustomerType: "RETAIL", ProductID: pid, QtyEntered: 410, UOMEntered: "PC",
	})
	if err != nil {
		t.Fatalf("resolve 410 PC at 15 per BOX: %v", err)
	}
	if bd.FinalCents != 15 || bd.PriceUOM != "BOX" || bd.OriginalCents != 15 {
		t.Fatalf("unit price wrong: got %d cents per %s (original %d), want 15 cents per BOX", bd.FinalCents, bd.PriceUOM, bd.OriginalCents)
	}
	if bd.QtyBase != 410 {
		t.Fatalf("qty_base %v, want 410", bd.QtyBase)
	}
	if bd.LineTotalCents != 62 {
		t.Fatalf("410 PC at 15 cents per BOX of 100: line total %d, want 62 (the exact total is 61.5, half away from zero rounds up)", bd.LineTotalCents)
	}

	// The till's door onto the same waterfall must carry the same exact line.
	lp, err := svc.DeriveCounterLinePrice(ctx, ResolveInput{
		CustomerType: "RETAIL", ProductID: pid, QtyEntered: 410, UOMEntered: "PC",
	})
	if err != nil {
		t.Fatalf("counter resolve: %v", err)
	}
	if lp.LineTotalCents != 62 || lp.UnitPriceCents != 15 {
		t.Fatalf("till line at the 410 case: total %d unit %d, want 62 and 15", lp.LineTotalCents, lp.UnitPriceCents)
	}

	// The review's away-from-edge probes: these were already right on the float arm.
	for _, tc := range []struct {
		qty  float64
		want int64
	}{{10, 2}, {30, 5}, {4100, 615}} {
		bd, err := svc.ResolvePrice(ctx, ResolveInput{
			CustomerType: "RETAIL", ProductID: pid, QtyEntered: tc.qty, UOMEntered: "PC",
		})
		if err != nil {
			t.Fatalf("resolve %v PC: %v", tc.qty, err)
		}
		if bd.LineTotalCents != tc.want {
			t.Fatalf("%v PC at 15 per BOX: line total %d, want %d", tc.qty, bd.LineTotalCents, tc.want)
		}
	}
}
