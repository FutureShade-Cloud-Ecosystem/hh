//go:build integration

// floor_candidates_integration_test.go, the composition root's two seams for the pricing
// editors' floor warning (CP0-6): the product source over the product and inventory services,
// and the customer type read. Pricing reads neither the products nor the cost tables itself.
//
//	Run: DATABASE_URL='postgres://.../postgres?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run FloorCandidates -count=1
package main

import (
	"context"
	"errors"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
)

func TestFloorCandidates_TheSeamsReadThroughTheOwningServices(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "floorcand")
	body, err := seedFS.ReadFile("dibbits_seed.sql")
	if err != nil {
		t.Fatalf("reading the embedded seed: %v", err)
	}
	if _, err := pool.Exec(ctx, string(body)); err != nil {
		t.Fatalf("applying the seed: %v", err)
	}
	productSvc := product.NewService(product.NewRepository(pool))
	invSvc := inventory.NewService(inventory.NewRepository(pool))
	cands := pricingFloorCandidates{products: productSvc, inventory: invSvc}

	var productID, categoryID, branchID string
	var branchCost int64
	if err := pool.QueryRow(ctx, `
		SELECT p.id::text, p.category_id::text, pbc.branch_id::text, pbc.avg_unit_cost_cents
		  FROM products p JOIN product_branch_costs pbc ON pbc.product_id = p.id
		 WHERE p.is_active AND p.category_id IS NOT NULL AND pbc.avg_unit_cost_cents > 0
		 ORDER BY p.sku LIMIT 1`).Scan(&productID, &categoryID, &branchID, &branchCost); err != nil {
		t.Fatalf("no seeded product with a branch cost: %v", err)
	}

	one, err := cands.CandidateProducts(ctx, "", productID, branchID, 10)
	if err != nil || len(one) != 1 || one[0].ProductID != productID || one[0].BaseUOM == "" || one[0].SKU == "" || one[0].CategoryID != categoryID {
		t.Fatalf("by product: %+v, %v", one, err)
	}
	if one[0].CostCents != branchCost {
		t.Errorf("with a branch the cost is that branch's: %d, want %d", one[0].CostCents, branchCost)
	}
	if def, err := cands.CandidateProducts(ctx, "", productID, "", 10); err != nil || len(def) != 1 || def[0].CostCents <= 0 {
		t.Errorf("with no branch the engagement-wide cost applies: %+v, %v", def, err)
	}
	if none, err := cands.CandidateProducts(ctx, "", "00000000-0000-4000-8000-000000000000", "", 10); err != nil || len(none) != 0 {
		t.Errorf("an unknown product is no candidate, not an error: %+v, %v", none, err)
	}
	if bad, err := cands.CandidateProducts(ctx, categoryID, productID, "", 10); err != nil || len(bad) != 1 {
		t.Errorf("a product inside its own category: %+v, %v", bad, err)
	}
	if bad, err := cands.CandidateProducts(ctx, "00000000-0000-4000-8000-000000000000", productID, "", 10); err != nil || len(bad) != 0 {
		t.Errorf("a product outside the named category is no candidate: %+v, %v", bad, err)
	}
	cat, err := cands.CandidateProducts(ctx, categoryID, "", "", 1000)
	if err != nil || len(cat) == 0 {
		t.Fatalf("by category: %+v, %v", cat, err)
	}
	for _, c := range cat {
		if c.CategoryID != categoryID {
			t.Errorf("a category query returned another category's product: %+v", c)
		}
	}
	capped, err := cands.CandidateProducts(ctx, "", "", "", 3)
	if err != nil || len(capped) != 3 {
		t.Fatalf("the limit must hold: %d, %v", len(capped), err)
	}
	// past the catalogue's own page of 200 the seam keeps reading, so the preview's extra row (its
	// truncation signal) is there
	var active int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM products WHERE is_active`).Scan(&active); err != nil {
		t.Fatalf("count: %v", err)
	}
	wide, err := cands.CandidateProducts(ctx, "", "", "", pricing.PreviewProductCap+1)
	if err != nil || len(wide) != min(active, pricing.PreviewProductCap+1) {
		t.Errorf("a wide read returns %d, got %d, %v", min(active, pricing.PreviewProductCap+1), len(wide), err)
	}

	custSvc := pricingFloorCustomers{svc: customer.NewService(customer.NewRepository(pool))}
	var custID, custType string
	if err := pool.QueryRow(ctx, `SELECT id::text, customer_type FROM customers WHERE customer_type='CONTRACTOR' LIMIT 1`).Scan(&custID, &custType); err != nil {
		t.Fatalf("no CONTRACTOR customer seeded: %v", err)
	}
	if got, err := custSvc.CustomerType(ctx, custID); err != nil || got != "CONTRACTOR" {
		t.Errorf("the customer's own type: %q, %v", got, err)
	}
	if _, err := custSvc.CustomerType(ctx, "00000000-0000-4000-8000-000000000000"); !errors.Is(err, pricing.ErrFloorCustomerNotFound) {
		t.Errorf("an unknown customer is the pricing not found: %v", err)
	}

	// the whole path: the preview of a deep customer price, through the real services, names
	// products and grades the customer at its own type
	pricingSvc := pricing.NewService(pricing.NewRepository(pool)).
		WithCategoryResolver(productSvc).WithUOMConverter(productSvc).WithMarginGate(pricing.NewIdentityMarginGate()).
		WithCandidateSource(cands).WithFloorCustomerType(custSvc)
	ninety := int64(9500)
	out, err := pricingSvc.PreviewFloor(ctx, pricing.FloorChange{Kind: "override", Override: &pricing.Override{
		CustomerID: custID, CategoryID: categoryID, CalcType: pricing.CalcPctOffList, DiscountBps: &ninety, PriceUOM: one[0].BaseUOM}})
	if err != nil {
		t.Fatalf("PreviewFloor: %v", err)
	}
	if len(out.Items) == 0 {
		t.Fatalf("95 percent off the category must name products: %+v", out)
	}
	for _, it := range out.Items {
		if it.CustomerType != "CONTRACTOR" {
			t.Errorf("a customer change is graded at the customer's type, got %q", it.CustomerType)
		}
	}
}
