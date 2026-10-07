//go:build integration

// unitusereader_integration_test.go proves the pricing module's loaded
// unit use read (HH-LOAD-1 LD-3) against every family of row it must
// see: the list price, the rule, the break header's threshold, the
// break item's price unit, and the conversion edge, each planted for
// the fixture product and each answered as a naming; with nothing
// planted the read answers clear. The concrete reader is what the
// wiring hands the unit set door's seam.
//
// Fixtures are invented. Run (the database URL rides the command line):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5671/ld3sets?sslmode=disable' \
//	  go test -tags integration ./internal/pricing/... -count=1 -run TestLoadedUnitUse
package pricing

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

const fxUseOrgID = "4742a800-0000-4000-8000-00000000d301"

func usePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set, integration tests skipped")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("use pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestLoadedUnitUseSeesEveryPricingFamily(t *testing.T) {
	pool := usePool(t)
	ctx := context.Background()
	productID := "4742a800-0000-4000-8000-00000000d311"
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM pricing_uom_conversions WHERE product_id = $1::uuid`, []any{productID}},
		{`DELETE FROM quantity_break_items WHERE header_id IN (SELECT id FROM quantity_break_headers WHERE product_id = $1::uuid)`, []any{productID}},
		{`DELETE FROM quantity_break_headers WHERE product_id = $1::uuid`, []any{productID}},
		{`DELETE FROM pricing_rules WHERE product_id = $1::uuid`, []any{productID}},
		{`DELETE FROM pricing_list_prices WHERE product_id = $1::uuid`, []any{productID}},
		{`DELETE FROM products WHERE id = $1::uuid`, []any{productID}},
		{`DELETE FROM load_key_registry WHERE org_id = $1::uuid`, []any{fxUseOrgID}},
	} {
		if _, err := pool.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("use fixture clean: %v", err)
		}
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM pricing_uom_conversions WHERE product_id = $1::uuid`, productID)
		_, _ = pool.Exec(ctx, `DELETE FROM quantity_break_items WHERE header_id IN (SELECT id FROM quantity_break_headers WHERE product_id = $1::uuid)`, productID)
		_, _ = pool.Exec(ctx, `DELETE FROM quantity_break_headers WHERE product_id = $1::uuid`, productID)
		_, _ = pool.Exec(ctx, `DELETE FROM pricing_rules WHERE product_id = $1::uuid`, productID)
		_, _ = pool.Exec(ctx, `DELETE FROM pricing_list_prices WHERE product_id = $1::uuid`, productID)
		_, _ = pool.Exec(ctx, `DELETE FROM products WHERE id = $1::uuid`, productID)
		_, _ = pool.Exec(ctx, `DELETE FROM load_key_registry WHERE org_id = $1::uuid`, fxUseOrgID)
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO products (id, sku, name, base_uom, price_uom, org_id)
		VALUES ($1::uuid, 'FX-USE-1', 'Fixture Use Product', 'SF', 'BOX', $2::uuid)`,
		productID, fxUseOrgID); err != nil {
		t.Fatalf("fixture product: %v", err)
	}

	read := func(t *testing.T, uom string) bool {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("use tx: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		named, err := LoadedUnitUseReader{}.ProductUnitInUseTx(ctx, tx, productID, nil, uom)
		if err != nil {
			t.Fatalf("use read: %v", err)
		}
		return named
	}

	if read(t, "BOX") {
		t.Fatalf("the clear read answers in use")
	}
	families := []struct {
		name     string
		plant    string
		args     []any
		drop     string
		dropArgs []any
	}{
		{"list price", `INSERT INTO pricing_list_prices (product_id, list_price_cents, price_uom)
			VALUES ($1::uuid, 1234, 'BOX')`, []any{productID},
			`DELETE FROM pricing_list_prices WHERE product_id = $1::uuid AND price_uom = 'BOX'`, []any{productID}},
		{"rule", `INSERT INTO pricing_rules (scope, product_id, calc_type, price_cents, price_uom)
			VALUES ('CUSTOMER_PRODUCT', $1::uuid, 'ABSOLUTE', 1234, 'BOX')`, []any{productID},
			`DELETE FROM pricing_rules WHERE product_id = $1::uuid AND price_uom = 'BOX'`, []any{productID}},
		{"break threshold", `INSERT INTO quantity_break_headers (code, name, product_id, threshold_uom)
			VALUES ('FXUSE1', 'Fixture Use Break', $1::uuid, 'BOX')`, []any{productID},
			`DELETE FROM quantity_break_headers WHERE code = 'FXUSE1'`, nil},
		{"break item price unit", `WITH h AS (
				INSERT INTO quantity_break_headers (code, name, product_id, threshold_uom)
				VALUES ('FXUSE2', 'Fixture Use Break Two', $1::uuid, 'SF') RETURNING id)
			INSERT INTO quantity_break_items (header_id, from_qty, value_type, discount_bps, price_uom)
			SELECT id, 5, 'PCT_OFF', 500, 'BOX' FROM h`, []any{productID},
			`DELETE FROM quantity_break_items WHERE price_uom = 'BOX' AND header_id IN
				(SELECT id FROM quantity_break_headers WHERE code = 'FXUSE2')`, nil},
		{"conversion edge from", `INSERT INTO pricing_uom_conversions (product_id, from_uom, to_uom, factor, source)
			VALUES ($1::uuid, 'BOX', 'SF', 20, 'SEEDED_DEFAULT')`, []any{productID},
			`DELETE FROM pricing_uom_conversions WHERE product_id = $1::uuid AND from_uom = 'BOX'`, []any{productID}},
		{"conversion edge to", `INSERT INTO pricing_uom_conversions (product_id, from_uom, to_uom, factor, source)
			VALUES ($1::uuid, 'SF', 'BOX', 0.05, 'SEEDED_DEFAULT')`, []any{productID},
			`DELETE FROM pricing_uom_conversions WHERE product_id = $1::uuid AND to_uom = 'BOX'`, []any{productID}},
	}
	for _, f := range families {
		if _, err := pool.Exec(ctx, f.plant, f.args...); err != nil {
			t.Fatalf("plant %s: %v", f.name, err)
		}
		if !read(t, "BOX") {
			t.Fatalf("the %s row stands and the read answers clear", f.name)
		}
		if _, err := pool.Exec(ctx, f.drop, f.dropArgs...); err != nil {
			t.Fatalf("drop %s: %v", f.name, err)
		}
		if read(t, "BOX") {
			t.Fatalf("the %s row is gone and the read still answers in use", f.name)
		}
	}
	// A unit nothing names answers clear the whole way.
	if read(t, "CYD") {
		t.Fatalf("the untouched unit answers in use")
	}
}

// TestLoadedUnitUseSeesTheCategoryPromotionAndClearanceFamilies: the
// live rows the first census missed. A promotion's fixed price, a
// clearance price, a category scoped rule and a category scoped break
// (matched the way the resolver matches them: category_id equal to the
// product's category, exactly, no walk up the tree) are pricing rows
// per unit: a unit they name cannot leave the set, and is free when the
// row goes.
func TestLoadedUnitUseSeesTheCategoryPromotionAndClearanceFamilies(t *testing.T) {
	pool := usePool(t)
	ctx := context.Background()
	productID := "4742a800-0000-4000-8000-00000000d312"
	categoryID := "4742a800-0000-4000-8000-00000000d3c1"
	parentID := "4742a800-0000-4000-8000-00000000d3c0"
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM pricing_promotions WHERE code LIKE 'FXUSE-P%'`, nil},
		{`DELETE FROM pricing_clearance_items WHERE product_id = $1::uuid`, []any{productID}},
		{`DELETE FROM quantity_break_items WHERE header_id IN (SELECT id FROM quantity_break_headers WHERE code IN ('FXUSE3','FXUSE4'))`, nil},
		{`DELETE FROM quantity_break_headers WHERE code IN ('FXUSE3','FXUSE4')`, nil},
		{`DELETE FROM pricing_rules WHERE category_id = ANY($1::uuid[]) AND price_uom = 'BOX'`, []any{[]string{categoryID, parentID}}},
		{`DELETE FROM products WHERE id = $1::uuid`, []any{productID}},
		{`DELETE FROM product_categories WHERE id = ANY($1::uuid[])`, []any{[]string{categoryID, parentID}}},
	} {
		if _, err := pool.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("category fixture clean: %v", err)
		}
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM pricing_promotions WHERE code LIKE 'FXUSE-P%'`)
		_, _ = pool.Exec(ctx, `DELETE FROM pricing_clearance_items WHERE product_id = $1::uuid`, productID)
		_, _ = pool.Exec(ctx, `DELETE FROM quantity_break_items WHERE header_id IN (SELECT id FROM quantity_break_headers WHERE code IN ('FXUSE3','FXUSE4'))`)
		_, _ = pool.Exec(ctx, `DELETE FROM quantity_break_headers WHERE code IN ('FXUSE3','FXUSE4')`)
		_, _ = pool.Exec(ctx, `DELETE FROM pricing_rules WHERE category_id = ANY($1::uuid[]) AND price_uom = 'BOX'`, []string{categoryID, parentID})
		_, _ = pool.Exec(ctx, `DELETE FROM products WHERE id = $1::uuid`, productID)
		_, _ = pool.Exec(ctx, `DELETE FROM product_categories WHERE id = ANY($1::uuid[])`, []string{categoryID, parentID})
	})
	// The product's category stands under a parent, so the no walk up the
	// tree leg has a tree to refuse.
	for _, c := range []struct {
		id     string
		parent *string
	}{{parentID, nil}, {categoryID, &parentID}} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO product_categories (id, code, name, parent_id)
			VALUES ($1::uuid, $2, 'Fixture Use Category', $3::uuid)`,
			c.id, "FXUSE-CAT-"+c.id[len(c.id)-2:], c.parent); err != nil {
			t.Fatalf("fixture category: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO products (id, sku, name, base_uom, price_uom, org_id, category_id)
		VALUES ($1::uuid, 'FX-USE-2', 'Fixture Use Product Two', 'SF', 'BOX', $2::uuid, $3::uuid)`,
		productID, fxUseOrgID, categoryID); err != nil {
		t.Fatalf("fixture product: %v", err)
	}

	read := func(t *testing.T, uom string) bool {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("use tx: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		named, err := LoadedUnitUseReader{}.ProductUnitInUseTx(ctx, tx, productID, &categoryID, uom)
		if err != nil {
			t.Fatalf("use read: %v", err)
		}
		return named
	}

	if read(t, "BOX") {
		t.Fatalf("the clear read answers in use")
	}
	families := []struct {
		name     string
		plant    string
		args     []any
		drop     string
		dropArgs []any
	}{
		{"promotion fixed price", `INSERT INTO pricing_promotions
			(code, name, product_id, calc_type, price_cents, price_uom, starts_at, ends_at)
			VALUES ('FXUSE-P1', 'Fixture Use Promo', $1::uuid, 'FIXED_PRICE', 1234, 'BOX', now(), now() + interval '1 hour')`, []any{productID},
			`DELETE FROM pricing_promotions WHERE code = 'FXUSE-P1'`, nil},
		{"clearance price", `INSERT INTO pricing_clearance_items (product_id, clearance_price_cents, price_uom)
			VALUES ($1::uuid, 1234, 'BOX')`, []any{productID},
			`DELETE FROM pricing_clearance_items WHERE product_id = $1::uuid`, []any{productID}},
		{"category rule", `INSERT INTO pricing_rules (scope, customer_type, category_id, calc_type, discount_bps, price_uom)
			VALUES ('TIER_CATEGORY', 'RETAIL', $1::uuid, 'PCT_OFF_LIST', 500, 'BOX')`, []any{categoryID},
			`DELETE FROM pricing_rules WHERE category_id = $1::uuid AND price_uom = 'BOX'`, []any{categoryID}},
		{"category break threshold", `INSERT INTO quantity_break_headers (code, name, category_id, threshold_uom)
			VALUES ('FXUSE3', 'Fixture Use Break Three', $1::uuid, 'BOX')`, []any{categoryID},
			`DELETE FROM quantity_break_headers WHERE code = 'FXUSE3'`, nil},
		{"category break item price unit", `WITH h AS (
				INSERT INTO quantity_break_headers (code, name, category_id, threshold_uom)
				VALUES ('FXUSE4', 'Fixture Use Break Four', $1::uuid, 'SF') RETURNING id)
			INSERT INTO quantity_break_items (header_id, from_qty, value_type, discount_bps, price_uom)
			SELECT id, 5, 'PCT_OFF', 500, 'BOX' FROM h`, []any{categoryID},
			`DELETE FROM quantity_break_items WHERE price_uom = 'BOX' AND header_id IN
				(SELECT id FROM quantity_break_headers WHERE code = 'FXUSE4')`, nil},
	}
	for _, f := range families {
		if _, err := pool.Exec(ctx, f.plant, f.args...); err != nil {
			t.Fatalf("plant %s: %v", f.name, err)
		}
		if !read(t, "BOX") {
			t.Fatalf("the %s row stands and the read answers clear", f.name)
		}
		if _, err := pool.Exec(ctx, f.drop, f.dropArgs...); err != nil {
			t.Fatalf("drop %s: %v", f.name, err)
		}
		if read(t, "BOX") {
			t.Fatalf("the %s row is gone and the read still answers in use", f.name)
		}
	}
	// The resolver matches the product's category exactly: a rule scoped
	// to the category's parent is not this product's pricing row, and the
	// read mirrors that.
	if _, err := pool.Exec(ctx, `INSERT INTO pricing_rules (scope, customer_type, category_id, calc_type, discount_bps, price_uom)
		VALUES ('TIER_CATEGORY', 'RETAIL', $1::uuid, 'PCT_OFF_LIST', 500, 'BOX')`, parentID); err != nil {
		t.Fatalf("plant parent category rule: %v", err)
	}
	if read(t, "BOX") {
		t.Fatalf("the parent category rule named the unit: the read walked the tree the resolver does not walk")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM pricing_rules WHERE category_id = $1::uuid AND price_uom = 'BOX'`, parentID); err != nil {
		t.Fatalf("drop parent category rule: %v", err)
	}
	// A unit nothing names answers clear the whole way.
	if read(t, "CYD") {
		t.Fatalf("the untouched unit answers in use")
	}
}

// TestLoadedUnitUseSeesTheOverrideAndTheBesideCategoryRows: the rows the
// round 2 delta review proved the read must answer. The customer
// category override is a live price row of the plan's family: the
// resolver serves it by customer and category (repository.go
// OverrideFor), its price_uom becomes the declared unit
// (service.go, the override arm) and the result conversion factor
// needs it, so a MARKUP_ON_COST override priced in a unit the set
// drops fails every later resolve for that customer. The beside
// category rows are the mirror's other half: the resolver matches the
// category scoped rules on category_id alone (repository.go
// MatchingRules, the CUSTOMER_CATEGORY and TIER_CATEGORY arms) and the
// break headers on product OR category (repository.go
// QuantityBreaksFor), so a row carrying a product beside the category
// still applies to every product of that category, and the read must
// answer it. Each row blocks the unit while it stands and frees it
// when it goes.
func TestLoadedUnitUseSeesTheOverrideAndTheBesideCategoryRows(t *testing.T) {
	pool := usePool(t)
	ctx := context.Background()
	productID := "4742a800-0000-4000-8000-00000000d313"
	otherID := "4742a800-0000-4000-8000-00000000d314"
	categoryID := "4742a800-0000-4000-8000-00000000d3c2"
	customerID := "4742a800-0000-4000-8000-00000000d3a1"
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM customer_category_overrides WHERE customer_id = $1::uuid`, []any{customerID}},
		{`DELETE FROM pricing_rules WHERE category_id = $1::uuid AND price_uom = 'BOX'`, []any{categoryID}},
		{`DELETE FROM quantity_break_items WHERE header_id IN (SELECT id FROM quantity_break_headers WHERE code IN ('FXUSE5','FXUSE6'))`, nil},
		{`DELETE FROM quantity_break_headers WHERE code IN ('FXUSE5','FXUSE6')`, nil},
		{`DELETE FROM products WHERE id = ANY($1::uuid[])`, []any{[]string{productID, otherID}}},
		{`DELETE FROM product_categories WHERE id = $1::uuid`, []any{categoryID}},
		{`DELETE FROM customers WHERE id = $1::uuid`, []any{customerID}},
	} {
		if _, err := pool.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("beside category fixture clean: %v", err)
		}
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM customer_category_overrides WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(ctx, `DELETE FROM pricing_rules WHERE category_id = $1::uuid AND price_uom = 'BOX'`, categoryID)
		_, _ = pool.Exec(ctx, `DELETE FROM quantity_break_items WHERE header_id IN (SELECT id FROM quantity_break_headers WHERE code IN ('FXUSE5','FXUSE6'))`)
		_, _ = pool.Exec(ctx, `DELETE FROM quantity_break_headers WHERE code IN ('FXUSE5','FXUSE6')`)
		_, _ = pool.Exec(ctx, `DELETE FROM products WHERE id = ANY($1::uuid[])`, []string{productID, otherID})
		_, _ = pool.Exec(ctx, `DELETE FROM product_categories WHERE id = $1::uuid`, categoryID)
		_, _ = pool.Exec(ctx, `DELETE FROM customers WHERE id = $1::uuid`, customerID)
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO customers (id, account_no, name)
		VALUES ($1::uuid, 'FXUSE-CUST', 'Fixture Use Customer')`, customerID); err != nil {
		t.Fatalf("fixture customer: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO product_categories (id, code, name)
		VALUES ($1::uuid, 'FXUSE-CAT-2', 'Fixture Use Category Two')`, categoryID); err != nil {
		t.Fatalf("fixture category: %v", err)
	}
	// Two products of one category: the rows under test carry the other
	// product beside the category, and must still name THIS product's
	// unit, the way the resolver's OR match serves them.
	for _, p := range []struct {
		id  string
		sku string
	}{{productID, "FX-USE-3"}, {otherID, "FX-USE-4"}} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO products (id, sku, name, base_uom, price_uom, org_id, category_id)
			VALUES ($1::uuid, $2, 'Fixture Use Product Three', 'SF', 'BOX', $3::uuid, $4::uuid)`,
			p.id, p.sku, fxUseOrgID, categoryID); err != nil {
			t.Fatalf("fixture product: %v", err)
		}
	}

	read := func(t *testing.T, uom string) bool {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("use tx: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		named, err := LoadedUnitUseReader{}.ProductUnitInUseTx(ctx, tx, productID, &categoryID, uom)
		if err != nil {
			t.Fatalf("use read: %v", err)
		}
		return named
	}

	if read(t, "BOX") {
		t.Fatalf("the clear read answers in use")
	}
	families := []struct {
		name     string
		plant    string
		args     []any
		drop     string
		dropArgs []any
	}{
		// The reviewer's probe: an override for a customer on the
		// product's category, MARKUP_ON_COST priced in the unit, the one
		// calc type whose result stays in the override's own unit.
		{"customer override, markup on cost", `INSERT INTO customer_category_overrides
			(customer_id, category_id, calc_type, markup_bps, price_uom)
			VALUES ($1::uuid, $2::uuid, 'MARKUP_ON_COST', 1500, 'BOX')`, []any{customerID, categoryID},
			`DELETE FROM customer_category_overrides WHERE customer_id = $1::uuid`, []any{customerID}},
		// A break header carrying another product beside the category:
		// the resolver's QuantityBreaksFor answers it for this product
		// (product OR category), and selectBreak converts its threshold
		// through the product's set.
		{"beside category break threshold", `INSERT INTO quantity_break_headers (code, name, product_id, category_id, threshold_uom)
			VALUES ('FXUSE5', 'Fixture Use Break Five', $1::uuid, $2::uuid, 'BOX')`, []any{otherID, categoryID},
			`DELETE FROM quantity_break_headers WHERE code = 'FXUSE5'`, nil},
		// The category scoped rules carry no product predicate in the
		// resolver's match: a rule with a product beside the category
		// applies to every product of the category, either scope.
		{"beside category tier category rule", `INSERT INTO pricing_rules
			(scope, customer_type, product_id, category_id, calc_type, discount_bps, price_uom)
			VALUES ('TIER_CATEGORY', 'RETAIL', $1::uuid, $2::uuid, 'PCT_OFF_LIST', 500, 'BOX')`, []any{otherID, categoryID},
			`DELETE FROM pricing_rules WHERE scope = 'TIER_CATEGORY' AND category_id = $1::uuid AND price_uom = 'BOX'`, []any{categoryID}},
		{"beside category customer category rule", `INSERT INTO pricing_rules
			(scope, customer_id, product_id, category_id, calc_type, discount_bps, price_uom)
			VALUES ('CUSTOMER_CATEGORY', $1::uuid, $2::uuid, $3::uuid, 'PCT_OFF_LIST', 500, 'BOX')`, []any{customerID, otherID, categoryID},
			`DELETE FROM pricing_rules WHERE scope = 'CUSTOMER_CATEGORY' AND category_id = $1::uuid AND price_uom = 'BOX'`, []any{categoryID}},
	}
	for _, f := range families {
		if _, err := pool.Exec(ctx, f.plant, f.args...); err != nil {
			t.Fatalf("plant %s: %v", f.name, err)
		}
		if !read(t, "BOX") {
			t.Fatalf("the %s row stands and the read answers clear", f.name)
		}
		if _, err := pool.Exec(ctx, f.drop, f.dropArgs...); err != nil {
			t.Fatalf("drop %s: %v", f.name, err)
		}
		if read(t, "BOX") {
			t.Fatalf("the %s row is gone and the read still answers in use", f.name)
		}
	}
	// The mirror stays exact the other way: a break header on the other
	// product alone, no category, is not this product's row in the
	// resolver's OR match, and the read answers clear.
	if _, err := pool.Exec(ctx, `INSERT INTO quantity_break_headers (code, name, product_id, threshold_uom)
		VALUES ('FXUSE6', 'Fixture Use Break Six', $1::uuid, 'BOX')`, otherID); err != nil {
		t.Fatalf("plant other product break: %v", err)
	}
	if read(t, "BOX") {
		t.Fatalf("the other product's own break named the unit: the read reads past the resolver's product arm")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM quantity_break_headers WHERE code = 'FXUSE6'`); err != nil {
		t.Fatalf("drop other product break: %v", err)
	}
	// A unit nothing names answers clear the whole way.
	if read(t, "CYD") {
		t.Fatalf("the untouched unit answers in use")
	}
}
