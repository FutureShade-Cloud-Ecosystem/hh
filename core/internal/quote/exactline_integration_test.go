//go:build integration

// exactline_integration_test.go, the quote line at the resolver's half-cent edge (decision log
// row 820). The pricing package pins the exact arm with fakes and with its own integration leg;
// this leg pins the QUOTE call path: StaffService.AddLine prices through the real pricing
// service (the production wiring shape: WithPricing(pricingSvc)) and persists the line the
// resolver derived, so a 410-piece line at 15 cents per box of 100 must land at 62 cents, not
// the float's 61.
//
// RED FIRST: seen red on main before the exact path landed.
package quote

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"github.com/jackc/pgx/v5/pgxpool"
)

// zeroCostBasis answers a zero cost so the assertion stays about the line money: a zero cost
// basis cannot block the line (the margin reads 100 percent), and pricing derives the line
// from the planted anchor, not from cost.
type zeroCostBasis struct{}

func (zeroCostBasis) CurrentCostCents(_ context.Context, _, _ string) (int64, bool, error) {
	return 0, false, nil
}

// retailCustType answers RETAIL so no seeded tier rule matches and the planted LIST anchor
// prices the line (the seeded CONTRACTOR TIER_DEFAULT would take the waterfall first).
type retailCustType struct{}

func (retailCustType) CustomerType(_ context.Context, _ string) (string, error) {
	return "RETAIL", nil
}

// exactLineFixture plants the invented pieces-per-box product (base PC, BOX at 100) with a
// 15-cents-per-BOX anchor, plus a customer and a DRAFT quote in Trenton. Cleanup removes
// everything it touched.
func exactLineFixture(t *testing.T, pool *pgxpool.Pool) (cust, quoteID, prodID string) {
	t.Helper()
	ctx := context.Background()
	sku := fmt.Sprintf("EXACTLINE-Q-%d", time.Now().UnixNano())
	tag := fmt.Sprintf("D820-%d", time.Now().UnixNano())

	if err := pool.QueryRow(ctx, `
		INSERT INTO products (sku, name, base_uom, price_uom)
		VALUES ($1, 'Exact line quote fixture', 'PC', 'BOX')
		RETURNING id::text`, sku).Scan(&prodID); err != nil {
		t.Fatalf("seed fixture product: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO product_uoms (product_id, uom_code, qty_in_base, is_base)
		VALUES ($1::uuid, 'PC', 1, true), ($1::uuid, 'BOX', 100, false)`, prodID); err != nil {
		t.Fatalf("seed fixture units: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO pricing_list_prices (product_id, list_price_cents, price_uom)
		VALUES ($1::uuid, 15, 'BOX')`, prodID); err != nil {
		t.Fatalf("seed fixture anchor: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type) VALUES ($1,$2,'CONTRACTOR') RETURNING id::text`,
		tag, tag).Scan(&cust); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO quotes (quote_no, customer_id, branch_id, status, valid_until,
		                    subtotal_cents, tax_cents, total_cents)
		VALUES ($1, $2::uuid, $3::uuid, 'DRAFT', NULL, 0, 0, 0) RETURNING id::text`,
		"#Q-"+tag, cust, intTrenton).Scan(&quoteID); err != nil {
		t.Fatalf("seed quote: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM audit_log WHERE entity_id = $1`, quoteID)
		_, _ = pool.Exec(c, `DELETE FROM quote_lines WHERE quote_id = $1`, quoteID)
		_, _ = pool.Exec(c, `DELETE FROM quotes WHERE id = $1`, quoteID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, cust)
		_, _ = pool.Exec(c, `DELETE FROM pricing_list_prices WHERE product_id = $1::uuid`, prodID)
		_, _ = pool.Exec(c, `DELETE FROM products WHERE id = $1::uuid`, prodID) // unit rows cascade
	})
	return cust, quoteID, prodID
}

// TestIntegration_ExactLineTotal_QuoteLineAt410 adds the 410-piece line through the quote's
// own add-line path with the real pricing service wired: the persisted line must carry the
// exact total 62 (61.5 rounded once, half away from zero), unit price 15 per BOX.
func TestIntegration_ExactLineTotal_QuoteLineAt410(t *testing.T) {
	ctx := qCtxAs(intStaff(intTrenton))
	pool := intPool(t)
	_, quoteID, prodID := exactLineFixture(t, pool)

	// the production wiring shape: the product service is the pricing resolver's catalog and
	// uom seam (wire_money.go), so the DECIMAL factor reaches the resolver through the module.
	prodSvc := product.NewService(product.NewRepository(pool))
	pricingSvc := pricing.NewService(pricing.NewRepository(pool)).
		WithCategoryResolver(prodSvc).
		WithUOMConverter(prodSvc).
		WithMarginGate(pricing.NewIdentityMarginGate())

	repo := NewRepository(pool)
	svc := NewStaffService(repo).
		WithAudit(audit.NewService(audit.NewRepository(pool), nil)).
		WithPricing(pricingSvc).
		WithCostBasis(zeroCostBasis{}).
		WithCustomerType(retailCustType{}).
		WithActorNamer(repo).
		withCaller(qCallerFrom)

	if _, err := svc.AddLine(ctx, quoteID, prodID, "exact line pin at the half cent", "410", "PC"); err != nil {
		t.Fatalf("add line at the 410 case: %v", err)
	}
	line := liveLine(t, pool, quoteID, 1)
	if line.UnitPriceCents != 15 || line.LineTotalCents != 62 {
		t.Fatalf("quote line at 410 PC and 15 cents per BOX of 100: unit %d total %d, want unit 15 and total 62 (the exact total is 61.5, half away from zero rounds up)",
			line.UnitPriceCents, line.LineTotalCents)
	}
}
