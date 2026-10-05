//go:build integration

// conversion_pin_integration_test.go, the two resolve-level conversion pins the round 1 K3 review
// named in its P2 (reports/FIX-review-r1-k3.md): the cost conversion and the anchor conversion
// (service.go, step 5) had no committed test that failed when the exact arm was reverted to its
// float shape, so a regression of the row 820 change on those arms left every suite green. Each
// pin drives the REAL resolver (real product module, the exact text seam wired the way
// wire_money.go wires it) through a line whose exact rational answer is a half cent the binary
// float drops, and asserts the exact answer. The mutation record (revert the one arm, see red,
// restore) lives in reports/FIX-coder-report-r2.md.
package pricing

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// costPinFixture plants an invented product sold per square foot: base PC, SF at qty_in_base 4.1
// (DECIMAL(19,8), so the exact seam hands the resolver the text 4.10000000), and a
// CUSTOMER_PRODUCT MARKUP_ON_COST rule at ZERO markup declaring SF. With no markup the unit price
// IS the cost converted into the result unit, so the resolver's step 5 cost conversion is the
// whole answer: 175 cents per PC x 4.1 = 717.5 cents per SF exactly. Invented fixtures only, the
// SKU is unique per run, cleanup removes the rule and the product (the unit rows cascade).
func costPinFixture(t *testing.T, pool *pgxpool.Pool, customerID string) (productID string) {
	t.Helper()
	ctx := context.Background()
	sku := fmt.Sprintf("COSTPIN-%d", time.Now().UnixNano())
	if err := pool.QueryRow(ctx, `
		INSERT INTO products (sku, name, base_uom, price_uom)
		VALUES ($1, 'Cost conversion pin fixture', 'PC', 'SF')
		RETURNING id::text`, sku).Scan(&productID); err != nil {
		t.Fatalf("seed cost-pin product: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO product_uoms (product_id, uom_code, qty_in_base, is_base)
		VALUES ($1::uuid, 'PC', 1, true), ($1::uuid, 'SF', 4.1, false)`, productID); err != nil {
		t.Fatalf("seed cost-pin units: %v", err)
	}
	var ruleID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO pricing_rules (scope, customer_id, product_id, calc_type, markup_bps, price_uom, priority, detail_text)
		VALUES ('CUSTOMER_PRODUCT', $1, $2::uuid, 'MARKUP_ON_COST', 0, 'SF', 70, 'IT-test: at-cost SF pin')
		RETURNING id::text`, customerID, productID).Scan(&ruleID); err != nil {
		t.Fatalf("seed cost-pin rule: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM pricing_rules WHERE id=$1`, ruleID)
		_, _ = pool.Exec(c, `DELETE FROM products WHERE id=$1::uuid`, productID) // unit rows cascade
	})
	return productID
}

// TestIntegration_CostConversionPin_175x41 prices a zero-markup MARKUP_ON_COST line at cost 175
// cents per PC resolved into SF (factor 4.1): the exact arm answers round(175 x 41/10) =
// round(717.5) = 718, half away from zero; the float arm's 175 x 4.0999999999999996 lands at
// 717.4999999999999 and answers 717, a cent off the shelf arithmetic. The K3 review's worked
// example, pinned so a revert of the cost conversion arm turns this test red at 717.
func TestIntegration_CostConversionPin_175x41(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)

	var customerID, customerType string
	if err := pool.QueryRow(ctx, `SELECT id::text, customer_type::text FROM customers LIMIT 1`).Scan(&customerID, &customerType); err != nil {
		t.Skipf("no customer seeded: %v", err)
	}
	svc := realSvc(pool)
	pid := costPinFixture(t, pool, customerID)

	bd, err := svc.ResolvePrice(ctx, ResolveInput{
		CustomerID: customerID, CustomerType: customerType, ProductID: pid,
		QtyEntered: 1, UOMEntered: "SF", CostCents: 175,
	})
	if err != nil {
		t.Fatalf("resolve the cost pin: %v", err)
	}
	if bd.Source != SourceCustomerProduct || bd.PriceUOM != "SF" {
		t.Fatalf("the contract rule must win and price per SF, got source=%s uom=%s", bd.Source, bd.PriceUOM)
	}
	if bd.FinalCents != 718 {
		t.Fatalf("cost 175 cents per PC into SF at factor 4.1 is 717.5 exactly (half away from zero 718, the float's 717.4999999999999 answers 717), got %d", bd.FinalCents)
	}
	if bd.LineTotalCents != 718 { // one SF, so the line total is the converted cost itself
		t.Fatalf("line total %d, want 718 (one SF at the converted cost)", bd.LineTotalCents)
	}
}
