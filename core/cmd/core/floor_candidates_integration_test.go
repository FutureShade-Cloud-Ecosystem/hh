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
	var maxCost int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(MAX(avg_unit_cost_cents), 0) FROM product_branch_costs WHERE product_id = $1`, productID).Scan(&maxCost); err != nil {
		t.Fatalf("max branch cost: %v", err)
	}
	if def, err := cands.CandidateProducts(ctx, "", productID, "", 10); err != nil || len(def) != 1 || def[0].CostCents != maxCost {
		t.Errorf("with no branch the highest branch cost applies: %+v, %v", def, err)
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

	// The reviewer's P2-A case, pinned on the seam: with no branch the cost is the HIGHEST branch
	// cost (the removed repository read's MAX), never the engagement-wide weighted cost, so a
	// breach that only the dearer branch makes is named even when the editor named no branch.
	t.Run("no branch grades at the highest branch cost", func(t *testing.T) {
		var productID, categoryID, baseUOM string
		if err := pool.QueryRow(ctx, `
			SELECT p.id::text, p.category_id::text, p.base_uom::text
			  FROM products p
			  JOIN pricing_list_prices plp ON plp.product_id = p.id AND plp.price_uom = p.base_uom
			 WHERE p.is_active AND p.category_id IS NOT NULL AND p.sku LIKE 'DBS-%'
			 ORDER BY p.sku LIMIT 1`).Scan(&productID, &categoryID, &baseUOM); err != nil {
			t.Fatalf("no seeded product with a base-unit anchor: %v", err)
		}
		branches := []string{"", ""}
		brows, err := pool.Query(ctx, `SELECT id::text FROM locations WHERE loc_type = 'BRANCH' ORDER BY code LIMIT 2`)
		if err != nil {
			t.Fatalf("branches: %v", err)
		}
		defer brows.Close()
		for i := 0; brows.Next(); i++ {
			if err := brows.Scan(&branches[i]); err != nil {
				t.Fatalf("branch: %v", err)
			}
		}
		if branches[0] == "" || branches[1] == "" {
			t.Fatalf("the seed must hold two branches")
		}
		// List 2200 in the base unit, branch costs 1518 and 1540 (the plain average of the
		// two rows would be 1529; the old read fell back to that average, and the average
		// hides the dearer yard's breach), and a 20 percent floor on the category. Every
		// stored price the product could resolve through is cleared so before/after speak
		// only the list price and the proposed rule.
		mustExec(t, ctx, pool, `UPDATE pricing_list_prices SET list_price_cents = 2200 WHERE product_id = $1`, productID)
		mustExec(t, ctx, pool, `DELETE FROM product_branch_costs WHERE product_id = $1`, productID)
		mustExec(t, ctx, pool, `INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents)
			VALUES ($1, $2, 1518), ($1, $3, 1540)`, productID, branches[0], branches[1])
		mustExec(t, ctx, pool, `DELETE FROM stock_balances WHERE product_id = $1`, productID)
		mustExec(t, ctx, pool, `DELETE FROM pricing_rules WHERE customer_type = 'RETAIL' OR category_id = $1`, categoryID)
		mustExec(t, ctx, pool, `DELETE FROM quantity_break_items WHERE header_id IN
			(SELECT id FROM quantity_break_headers WHERE product_id = $1 OR category_id = $2)`, productID, categoryID)
		mustExec(t, ctx, pool, `DELETE FROM quantity_break_headers WHERE product_id = $1 OR category_id = $2`, productID, categoryID)
		mustExec(t, ctx, pool, `INSERT INTO pricing_category_policy (category_id, margin_target_bps, margin_floor_bps, margin_enforcement_disabled)
			VALUES ($1, 3000, 2000, false)
			ON CONFLICT (category_id) DO UPDATE SET margin_target_bps = 3000, margin_floor_bps = 2000, margin_enforcement_disabled = false`, categoryID)

		if one, err := cands.CandidateProducts(ctx, categoryID, productID, branches[0], 10); err != nil || len(one) != 1 || one[0].CostCents != 1518 {
			t.Errorf("the cheaper branch's cost: %+v, %v", one, err)
		}
		if one, err := cands.CandidateProducts(ctx, categoryID, productID, branches[1], 10); err != nil || len(one) != 1 || one[0].CostCents != 1540 {
			t.Errorf("the dearer branch's cost: %+v, %v", one, err)
		}
		noBranch, err := cands.CandidateProducts(ctx, categoryID, productID, "", 10)
		if err != nil || len(noBranch) != 1 || noBranch[0].CostCents != 1540 {
			t.Errorf("with no branch the HIGHEST branch cost applies (1540, not the plain average 1529): %+v, %v", noBranch, err)
		}

		pricingSvc := pricing.NewService(pricing.NewRepository(pool)).
			WithCategoryResolver(productSvc).WithUOMConverter(productSvc).WithMarginGate(pricing.NewIdentityMarginGate()).
			WithCandidateSource(cands)
		thirteen := int64(1300)
		out, err := pricingSvc.PreviewFloor(ctx, pricing.FloorChange{Kind: "rule", Rule: &pricing.Rule{
			Scope: pricing.ScopeTierCategory, CustomerType: "RETAIL", CategoryID: categoryID,
			CalcType: pricing.CalcPctOffList, DiscountBps: &thirteen, PriceUOM: baseUOM, Priority: 10}})
		if err != nil {
			t.Fatalf("PreviewFloor: %v", err)
		}
		if len(out.Items) == 0 {
			t.Fatalf("13 percent off at the highest branch cost must name the product (margin 19.54 percent against a 20 percent floor): %+v", out)
		}
		var it *pricing.FloorPreviewItem
		for i := range out.Items {
			if out.Items[i].ProductID == productID {
				it = &out.Items[i]
			}
		}
		if it == nil {
			t.Fatalf("the 2200 list, 1540 highest cost product is not named (the weighted 1529 cost hides the breach): %+v", out.Items)
		}
		if it.FinalCents != 1914 || it.PreviousFinalCents != 2200 ||
			it.MarginBps != 1954 || it.FloorBps != 2000 || it.AlreadyUnderFloor {
			t.Errorf("the reviewer's figures: %+v", *it)
		}
	})

	// The reviewer's round 3 case, pinned end to end: a stored RETAIL rule at 10 percent off
	// LIMITED TO ONE BRANCH must be graded even when the change names no branch. The preview
	// used to build its resolve with no branch, so the stored-rules read dropped every
	// branch-limited rule: raising the floor previewed zero items while the save left a
	// Kingston RETAIL resolve blocked. With no branch the preview now grades each branch at
	// its own cost with its own branch rules, keeps the worst and names the branch.
	t.Run("no branch still grades a rule limited to a branch", func(t *testing.T) {
		var productID, categoryID, baseUOM string
		if err := pool.QueryRow(ctx, `
			SELECT p.id::text, p.category_id::text, p.base_uom::text
			  FROM products p
			  JOIN pricing_list_prices plp ON plp.product_id = p.id AND plp.price_uom = p.base_uom
			 WHERE p.is_active AND p.category_id IS NOT NULL AND p.sku LIKE 'DBS-%'
			 ORDER BY p.sku LIMIT 1`).Scan(&productID, &categoryID, &baseUOM); err != nil {
			t.Fatalf("no seeded product with a base-unit anchor: %v", err)
		}
		var kgn, other struct{ id, name string }
		brows, err := pool.Query(ctx, `SELECT id::text, name FROM locations WHERE loc_type = 'BRANCH' ORDER BY code`)
		if err != nil {
			t.Fatalf("branches: %v", err)
		}
		defer brows.Close()
		var ids, names []string
		for brows.Next() {
			var id, name string
			if err := brows.Scan(&id, &name); err != nil {
				t.Fatalf("branch: %v", err)
			}
			ids, names = append(ids, id), append(names, name)
		}
		if len(ids) != 2 {
			t.Fatalf("the seed must hold two branches, got %d", len(ids))
		}
		kgn.id, kgn.name = ids[0], names[0] // ORDER BY code puts KGN first, TRT second: Kingston is the dear yard
		other.id = ids[1]
		// List 2200 in the base unit, branch costs 1518 at the other yard and 1540 at Kingston
		// (the plain average would be 1529), a 20 percent floor stored, and every stored price
		// cleared except the one rule under test: RETAIL at 10 percent off, limited to Kingston.
		mustExec(t, ctx, pool, `UPDATE pricing_list_prices SET list_price_cents = 2200 WHERE product_id = $1`, productID)
		mustExec(t, ctx, pool, `DELETE FROM product_branch_costs WHERE product_id = $1`, productID)
		mustExec(t, ctx, pool, `INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents)
			VALUES ($1, $2, 1518), ($1, $3, 1540)`, productID, other.id, kgn.id)
		mustExec(t, ctx, pool, `DELETE FROM stock_balances WHERE product_id = $1`, productID)
		mustExec(t, ctx, pool, `DELETE FROM pricing_rules WHERE customer_type = 'RETAIL' OR category_id = $1`, categoryID)
		mustExec(t, ctx, pool, `DELETE FROM quantity_break_items WHERE header_id IN
			(SELECT id FROM quantity_break_headers WHERE product_id = $1 OR category_id = $2)`, productID, categoryID)
		mustExec(t, ctx, pool, `DELETE FROM quantity_break_headers WHERE product_id = $1 OR category_id = $2`, productID, categoryID)
		mustExec(t, ctx, pool, `INSERT INTO pricing_category_policy (category_id, margin_target_bps, margin_floor_bps, margin_enforcement_disabled)
			VALUES ($1, 3000, 2000, false)
			ON CONFLICT (category_id) DO UPDATE SET margin_target_bps = 3000, margin_floor_bps = 2000, margin_enforcement_disabled = false`, categoryID)
		mustExec(t, ctx, pool, `INSERT INTO pricing_rules (scope, customer_type, category_id, branch_id, calc_type, discount_bps, price_uom, priority)
			VALUES ('TIER_CATEGORY','RETAIL',$1,$2,'PCT_OFF_LIST',1000,$3,40)`, categoryID, kgn.id, baseUOM)

		svc := pricing.NewService(pricing.NewRepository(pool)).
			WithCategoryResolver(productSvc).WithUOMConverter(productSvc).WithMarginGate(pricing.NewIdentityMarginGate()).
			WithCandidateSource(cands)
		out, err := svc.PreviewFloor(ctx, pricing.FloorChange{Kind: "category_policy",
			Policy: &pricing.CategoryPolicy{CategoryID: categoryID, MarginTargetBps: 3000, MarginFloorBps: 2300}})
		if err != nil {
			t.Fatalf("PreviewFloor: %v", err)
		}
		var it *pricing.FloorPreviewItem
		for i := range out.Items {
			if out.Items[i].ProductID == productID {
				it = &out.Items[i]
			}
		}
		if it == nil {
			t.Fatalf("raising the floor to 23 percent must name the product the Kingston rule leaves at 1980 (22.22 percent), got %+v", out.Items)
		}
		if it.CustomerType != "RETAIL" || it.FinalCents != 1980 || it.MarginBps != 2222 || it.FloorBps != 2300 || it.AlreadyUnderFloor {
			t.Errorf("the reviewer's figures (1980 at 2222 bps against a 2300 floor, newly under): %+v", *it)
		}
		if it.BranchID != kgn.id || it.BranchName != kgn.name {
			t.Errorf("the item must name Kingston as the worst branch (id %s, name %s), got id %s name %q", kgn.id, kgn.name, it.BranchID, it.BranchName)
		}
		// The served equivalence the reviewer proved: after the real save, a Kingston RETAIL
		// resolve at Kingston's own cost is blocked at exactly the previewed figures, and the
		// other yard's resolve is not.
		if _, err := svc.UpsertCategoryPolicy(ctx, pricing.CategoryPolicy{
			CategoryID: categoryID, MarginTargetBps: 3000, MarginFloorBps: 2300}, "it-floor-preview"); err != nil {
			t.Fatalf("UpsertCategoryPolicy: %v", err)
		}
		atKgn, err := svc.ResolvePrice(ctx, pricing.ResolveInput{
			CustomerType: "RETAIL", ProductID: productID, CategoryID: categoryID, BranchID: kgn.id,
			QtyEntered: 1, UOMEntered: baseUOM, CostCents: 1540})
		if err != nil {
			t.Fatalf("resolve at Kingston: %v", err)
		}
		if !atKgn.Blocked || atKgn.FinalCents != 1980 || atKgn.MarginBps != 2222 || atKgn.FloorBps != 2300 {
			t.Errorf("the saved floor blocks the Kingston resolve the preview named: %+v", atKgn)
		}
		atOther, err := svc.ResolvePrice(ctx, pricing.ResolveInput{
			CustomerType: "RETAIL", ProductID: productID, CategoryID: categoryID, BranchID: other.id,
			QtyEntered: 1, UOMEntered: baseUOM, CostCents: 1518})
		if err != nil {
			t.Fatalf("resolve at the other yard: %v", err)
		}
		if atOther.Blocked {
			t.Errorf("the other yard pays the 2200 list at 30.9 percent and must not be blocked: %+v", atOther)
		}
	})

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
