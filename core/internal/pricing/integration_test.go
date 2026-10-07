//go:build integration

// Integration tests run the real pgx Repository + the real catalog read seam
// (internal/product) against the Postgres 16 that `make reset` provisions, exercising
// migrations 0023+0024 (I1), an end-to-end resolve (I2), and the pre-tax line-price
// wire-in (I3). Run with: make test-integration (after make migrate/seed).
package pricing

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"github.com/jackc/pgx/v5/pgxpool"
)

func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	fail := func(format string, a ...any) {
		if set {
			t.Fatalf(format, a...)
		}
		t.Skipf(format, a...)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		fail("integration DB unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		fail("integration DB not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func realSvc(pool *pgxpool.Pool) *Service {
	cat := product.NewService(product.NewRepository(pool))
	return NewService(NewRepository(pool)).
		WithCategoryResolver(cat).
		WithUOMConverter(cat).
		WithMarginGate(NewIdentityMarginGate())
}

// withAnchor puts productID into a KNOWN pricing_list_prices state for the duration of a test, no
// anchor (cents==0) or an exact temp anchor, and restores whatever was there before on cleanup.
//
// DIB-378 now backfills a PROVISIONAL retail anchor for the FULL seeded catalog (previously only the
// 4 aggregate SKUs had one), so a test can no longer assume "the seed happened to leave this product
// bare", every product it might pick now already carries one. Tests that specifically want to
// exercise the derived-anchor (cost×1.85) path, or that plant their own exact temp value and assert
// on it, must establish that precondition themselves instead of relying on seed incompleteness.
func withAnchor(t *testing.T, pool *pgxpool.Pool, productID string, cents int64, uom string) {
	t.Helper()
	ctx := context.Background()
	var priorCents int64
	var priorUOM string
	hadPrior := pool.QueryRow(ctx,
		`SELECT list_price_cents, price_uom FROM pricing_list_prices WHERE product_id=$1`, productID).
		Scan(&priorCents, &priorUOM) == nil
	if _, err := pool.Exec(ctx, `DELETE FROM pricing_list_prices WHERE product_id=$1`, productID); err != nil {
		t.Fatalf("clear anchor: %v", err)
	}
	if cents > 0 {
		if _, err := pool.Exec(ctx,
			`INSERT INTO pricing_list_prices (product_id, list_price_cents, price_uom) VALUES ($1,$2,$3)`,
			productID, cents, uom); err != nil {
			t.Fatalf("seed temp anchor: %v", err)
		}
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM pricing_list_prices WHERE product_id=$1`, productID)
		if hadPrior {
			_, _ = pool.Exec(ctx,
				`INSERT INTO pricing_list_prices (product_id, list_price_cents, price_uom) VALUES ($1,$2,$3)`,
				productID, priorCents, priorUOM)
		}
	})
}

// I1, 0023 + 0024 + the seed applied clean; the CONTRACTOR TIER_DEFAULT (migrate-time) and the
// category policy (seed-time, keyed to REAL catalog categories) are live, and NO orphan parallel
// category set leaked in. This is the regression guard for the gate-critical zero-trust /
// migrate-before-seed bug: 0024 must not bootstrap its own product_categories, and every category
// policy must key to a category a REAL product actually belongs to.
func TestIntegration_MigrationsAndSeed(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)

	// Exactly the 5 REAL catalog categories, no PAVERS/RET_WALLS/STEPS_CAPS/AGGREGATES/SAND_BASE/
	// ACCESSORIES orphans from a pricing-owned bootstrap.
	var orphanCats int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM product_categories
		 WHERE code IN ('PAVERS','RET_WALLS','STEPS_CAPS','AGGREGATES','SAND_BASE','ACCESSORIES')`).Scan(&orphanCats); err != nil {
		t.Fatalf("query orphan categories: %v", err)
	}
	if orphanCats != 0 {
		t.Fatalf("found %d orphan pricing-bootstrap categories; migration 0024 must not seed product_categories", orphanCats)
	}

	var policies int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pricing_category_policy`).Scan(&policies); err != nil {
		t.Fatalf("query category policy: %v", err)
	}
	// 6 REAL catalog categories: the original 5 (PAVER/WALL/SLABSTEP/AGGREGATE/ACCESSORY) plus BULK,
	// added by DIB-260/252 as its own enforced by-the-ton front-desk category.
	if policies != 6 {
		t.Fatalf("expected exactly 6 seeded category policies (one per REAL catalog category, incl. BULK/DIB-260), got %d", policies)
	}
	// Every policy must key to a category a REAL seeded product belongs to (the orphan policy had none)
	//, EXCEPT AGGREGATE: DIB-260/252 intentionally reassigned every by-the-ton aggregate SKU to the
	// new BULK category (see the seed's UPDATE ... category_id = BULK), so AGGREGATE is now a
	// deliberately productless taxonomy entry kept for a future non-bulk aggregate SKU, not a leak.
	var policiesWithoutProduct int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM pricing_category_policy pol
		  JOIN product_categories c ON c.id = pol.category_id
		 WHERE c.code <> 'AGGREGATE'
		   AND NOT EXISTS (SELECT 1 FROM products p WHERE p.category_id = pol.category_id)`).Scan(&policiesWithoutProduct); err != nil {
		t.Fatalf("query policy-without-product: %v", err)
	}
	if policiesWithoutProduct != 0 {
		t.Fatalf("%d category policies key to a category NO product uses (orphan-category regression)", policiesWithoutProduct)
	}
	// DIB-252 turned AGGREGATE margin enforcement ON (it was audit-only/disabled before), BULK is the
	// bulk-overhead category now (its own policy, checked below).
	var aggEnforced bool
	if err := pool.QueryRow(ctx, `
		SELECT NOT pol.margin_enforcement_disabled
		  FROM pricing_category_policy pol JOIN product_categories c ON c.id = pol.category_id
		 WHERE c.code = 'AGGREGATE'`).Scan(&aggEnforced); err != nil {
		t.Fatalf("query AGGREGATE policy: %v", err)
	}
	if !aggEnforced {
		t.Fatal("AGGREGATE policy must have margin enforcement ON (DIB-252, no longer audit-only)")
	}

	var tierDefaults int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pricing_rules WHERE scope='TIER_DEFAULT' AND customer_type='CONTRACTOR' AND is_active`).Scan(&tierDefaults); err != nil {
		t.Fatalf("query tier default: %v", err)
	}
	if tierDefaults < 1 {
		t.Fatal("expected the CONTRACTOR TIER_DEFAULT rule to be seeded (the ~99% path)")
	}
}

// I2, end-to-end resolve through the real repo + catalog seam: a CONTRACTOR + a
// category-keyed product resolves to TIER_CATEGORY with the correct math.
func TestIntegration_ResolveEndToEnd(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)

	// A real product that has a list-price anchor + a base UOM row, in a category with NO seeded
	// TIER_CATEGORY rule (PAVER/ACCESSORY/BULK all have one, DIB-378 now anchors the full catalog,
	// so picking one of those would compete with this test's OWN temp rule below).
	var productID, categoryID, priceUOM string
	var listCents int64
	err := pool.QueryRow(ctx, `
		SELECT p.id::text, p.category_id::text, lp.list_price_cents, lp.price_uom::text
		  FROM products p
		  JOIN pricing_list_prices lp ON lp.product_id = p.id
		  JOIN product_categories c ON c.id = p.category_id
		 WHERE c.code NOT IN ('PAVER', 'ACCESSORY', 'BULK')
		 ORDER BY p.sku LIMIT 1`).Scan(&productID, &categoryID, &listCents, &priceUOM)
	if err != nil {
		t.Skipf("no anchored product with an uncontested category to resolve: %v", err)
	}

	// Seed a temporary TIER_CATEGORY rule for that product's category (18% off), resolve,
	// then clean up, proving the category-keyed waterfall on real data.
	var ruleID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO pricing_rules (scope, customer_type, category_id, calc_type, discount_bps, price_uom, priority, detail_text)
		VALUES ('TIER_CATEGORY','CONTRACTOR',$1,'PCT_OFF_LIST',1800,$2,40,'IT-test: 18% off')
		RETURNING id::text`, categoryID, priceUOM).Scan(&ruleID); err != nil {
		t.Fatalf("seed tier-category rule: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM pricing_rules WHERE id=$1`, ruleID) })

	bd, err := realSvc(pool).ResolvePrice(ctx, ResolveInput{
		CustomerType: "CONTRACTOR", ProductID: productID, QtyEntered: 1, UOMEntered: priceUOM,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := listCents * 8200 / 10000
	if bd.Source != SourceTierCategory || bd.FinalCents != want {
		t.Fatalf("expected TIER_CATEGORY final=%d, got %+v", want, bd)
	}
}

// I3, the wire-in derives the pre-tax price trio onto a quote line and NEVER touches
// tax_cents (M5 owns tax).
func TestIntegration_LineDerivation_PreTaxOnly(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)

	var customerID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM customers WHERE customer_type='CONTRACTOR' LIMIT 1`).Scan(&customerID); err != nil {
		t.Skipf("no CONTRACTOR customer seeded: %v", err)
	}
	var branchID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type='BRANCH' LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no branch seeded: %v", err)
	}
	var productID, priceUOM string
	if err := pool.QueryRow(ctx, `
		SELECT p.id::text, lp.price_uom::text FROM products p
		  JOIN pricing_list_prices lp ON lp.product_id = p.id LIMIT 1`).Scan(&productID, &priceUOM); err != nil {
		t.Skipf("no anchored product: %v", err)
	}

	lp, err := realSvc(pool).DeriveLinePrice(ctx, ResolveInput{
		CustomerID: customerID, CustomerType: "CONTRACTOR", ProductID: productID,
		BranchID: branchID, QtyEntered: 2, UOMEntered: priceUOM,
	})
	if err != nil {
		t.Fatalf("derive line price: %v", err)
	}
	if lp.UnitPriceCents <= 0 || lp.LineTotalCents <= 0 || lp.PriceUOM == "" {
		t.Fatalf("line price trio not derived: %+v", lp)
	}

	// Persist a quote + line with ONLY the pre-tax trio; assert tax_cents stays 0.
	var quoteID string
	qno := "IT-PRICING-" + productID[:8]
	if err := pool.QueryRow(ctx, `
		INSERT INTO quotes (quote_no, customer_id, branch_id) VALUES ($1,$2,$3) RETURNING id::text`,
		qno, customerID, branchID).Scan(&quoteID); err != nil {
		t.Fatalf("insert quote: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM quotes WHERE id=$1`, quoteID) })

	if _, err := pool.Exec(ctx, `
		INSERT INTO quote_lines (quote_id, line_no, product_id, qty_entered, uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents)
		VALUES ($1,1,$2,$3,$4,$5,$6,$7,$8)`,
		quoteID, productID, 2.0, priceUOM, lp.QtyBase, lp.UnitPriceCents, lp.PriceUOM, lp.LineTotalCents); err != nil {
		t.Fatalf("insert quote line: %v", err)
	}

	var headerTax int64
	var unit, lineTotal int64
	if err := pool.QueryRow(ctx, `
		SELECT q.tax_cents, l.unit_price_cents, l.line_total_cents
		  FROM quotes q JOIN quote_lines l ON l.quote_id=q.id WHERE q.id=$1`, quoteID).Scan(&headerTax, &unit, &lineTotal); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if headerTax != 0 {
		t.Fatalf("A-07 must never write tax_cents (M5 owns tax); got %d", headerTax)
	}
	if unit != lp.UnitPriceCents || lineTotal != lp.LineTotalCents {
		t.Fatalf("line trio mismatch: got unit=%d line=%d, want unit=%d line=%d", unit, lineTotal, lp.UnitPriceCents, lp.LineTotalCents)
	}
}

// I4, the REGRESSION test for the orphan-category mismatch (gate-critical). Unlike I2 (which seeds its
// OWN temp TIER_CATEGORY rule and so only exercises the ENGINE), this resolves a REAL seeded PAVER product
// against the SEEDED rules + policy. It must hit the SEEDED TIER_CATEGORY rule (proving that rule keys to
// the REAL PAVER category, not an orphan 'PAVERS') and the SEEDED PAVER category policy floor (2000 bps, not
// the 1200 default) must apply. On the buggy 0024 seed both the rule and the policy were keyed to orphan
// categories no product used, so a real PAVER resolve fell through to TIER_DEFAULT with the default floor -
// exactly the mismatch this asserts against. Would have CAUGHT the bug.
func TestIntegration_SeededPaverTierCategoryAndPolicy(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)

	// A real PAVER product, given a KNOWN temp anchor (10000 ¢/SF) so the PCT_OFF_LIST math is
	// observable, restored to whatever the seed had on cleanup (DIB-378: every PAVER now carries a
	// seed anchor, so this can no longer rely on one happening to be bare).
	var productID string
	if err := pool.QueryRow(ctx, `
		SELECT p.id::text
		  FROM products p JOIN product_categories c ON c.id = p.category_id
		 WHERE c.code = 'PAVER' LIMIT 1`).Scan(&productID); err != nil {
		t.Skipf("no real PAVER product to resolve: %v", err)
	}
	withAnchor(t, pool, productID, 10000, "SF")

	svc := realSvc(pool)

	// (a) CONTRACTOR + PAVER, zero cost → resolves via the SEEDED TIER_CATEGORY rule (18% off), reports the
	//     SEEDED PAVER policy floor (2000 bps), and is NOT blocked.
	bd, err := svc.ResolvePrice(ctx, ResolveInput{
		CustomerType: "CONTRACTOR", ProductID: productID, QtyEntered: 1, UOMEntered: "SF", CostCents: 0,
	})
	if err != nil {
		t.Fatalf("resolve (above floor): %v", err)
	}
	if bd.Source != SourceTierCategory {
		t.Fatalf("expected the SEEDED TIER_CATEGORY rule to match a real PAVER product, got source=%s (orphan-category regression)", bd.Source)
	}
	if bd.FloorBps != 2000 {
		t.Fatalf("expected the SEEDED PAVER policy floor 2000 bps, got %d (policy keyed to an orphan category?)", bd.FloorBps)
	}
	if bd.OriginalCents != 10000 || bd.FinalCents != 8200 || bd.DiscountBps != 1800 {
		t.Fatalf("expected 18%% off 10000 → 8200 (disc 1800), got orig=%d final=%d disc=%d", bd.OriginalCents, bd.FinalCents, bd.DiscountBps)
	}
	if bd.Blocked {
		t.Fatalf("a comfortably-above-floor PAVER price must not be blocked: %+v", bd)
	}

	// (b) Same product, a cost that drives margin below the PAVER floor → the SEEDED policy floor ENFORCES
	//     (the A-20 below-floor block), confirming the floor is live on real PAVER data.
	blocked, err := svc.ResolvePrice(ctx, ResolveInput{
		CustomerType: "CONTRACTOR", ProductID: productID, QtyEntered: 1, UOMEntered: "SF", CostCents: 100000,
	})
	if err != nil {
		t.Fatalf("resolve (below floor): %v", err)
	}
	if !blocked.Blocked || blocked.BlockPolicy != BlockMarginBelowMinimum {
		t.Fatalf("expected a below-floor PAVER price to block on the SEEDED policy floor, got %+v", blocked)
	}
	if blocked.FloorBps != 2000 {
		t.Fatalf("expected the block to report the SEEDED PAVER floor 2000 bps, got %d", blocked.FloorBps)
	}
	if blocked.Source != SourceTierCategory {
		t.Fatalf("expected the blocked price to still resolve via TIER_CATEGORY, got %s", blocked.Source)
	}
}

// I5, THE CLASS-KILLING TEST (fix #1). Resolve EVERY seeded product (all 30) as a CONTRACTOR
// against the real seeded DB and assert: finalCents > 0 (the $0 bug would fail here), finalCents
// is sane (within a plausible band of the cost basis), the Source is the expected precedence
// level, and there is NO spurious margin block off a $0 price. Most seeded products have NO
// explicit pricing_list_prices anchor (only the 4 aggregates do), pre-fix they all priced off a
// $0 list and returned $0; post-fix the anchor is DERIVED from cost (×1.85). A real PAVER must
// hit its seeded TIER_CATEGORY rule + the derived anchor. Would have CAUGHT the $0 bug.
func TestIntegration_EveryProductPricesNonZero(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := realSvc(pool)

	rows, err := pool.Query(ctx, `
		SELECT p.id::text, p.sku, c.code, p.base_uom::text
		  FROM products p JOIN product_categories c ON c.id = p.category_id
		 WHERE p.sku LIKE 'DBS-%' ORDER BY p.sku`)
	if err != nil {
		t.Fatalf("list products: %v", err)
	}
	type prod struct{ id, sku, cat, baseUOM string }
	var products []prod
	for rows.Next() {
		var p prod
		if err := rows.Scan(&p.id, &p.sku, &p.cat, &p.baseUOM); err != nil {
			rows.Close()
			t.Fatalf("scan product: %v", err)
		}
		products = append(products, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate products: %v", err)
	}
	if len(products) < 30 {
		t.Fatalf("expected the full seeded catalogue (~30 products), got %d", len(products))
	}

	// This test's whole point is the DERIVED anchor (cost×1.85), clear every product's anchor for
	// its duration (DIB-378 now backfills one for the full catalog by default) and restore on cleanup.
	for _, p := range products {
		withAnchor(t, pool, p.id, 0, "")
	}

	// CONTRACTOR seeded waterfall: PAVER + ACCESSORY + BULK (DIB-260's by-the-ton default rule) have a
	// seeded TIER_CATEGORY rule; every other category falls to the seeded CONTRACTOR TIER_DEFAULT.
	// None should fall through to LIST.
	const cost int64 = 5000 // nominal cost basis (¢/base-unit); the derived anchor = cost × 1.85
	var pavers int
	for _, p := range products {
		wantSource := SourceTierDefault
		if p.cat == "PAVER" || p.cat == "ACCESSORY" || p.cat == "BULK" {
			wantSource = SourceTierCategory
		}
		bd, err := svc.ResolvePrice(ctx, ResolveInput{
			CustomerType: "CONTRACTOR", ProductID: p.id, QtyEntered: 1, UOMEntered: p.baseUOM, CostCents: cost,
		})
		if err != nil {
			t.Fatalf("resolve %s (%s): %v", p.sku, p.cat, err)
		}
		if bd.FinalCents <= 0 {
			t.Fatalf("%s (%s): finalCents must be > 0 (the $0 bug), got %+v", p.sku, p.cat, bd)
		}
		// Every resolved line must carry a unit, derived anchors included. A "" PriceUOM is the
		// derived-anchor UOM bug: it cannot persist (quote_lines.price_uom is NOT NULL FK uoms(code)).
		if bd.PriceUOM == "" {
			t.Fatalf("%s (%s): Breakdown.PriceUOM must be non-empty (derived-anchor UOM bug), got %+v", p.sku, p.cat, bd)
		}
		// Sanity band: a contractor unit price between ¼× and 5× the cost basis is plausible;
		// $0 or absurd values fall outside it.
		if bd.FinalCents < cost/4 || bd.FinalCents > cost*5 {
			t.Fatalf("%s (%s): finalCents %d outside the plausible band [%d,%d]", p.sku, p.cat, bd.FinalCents, cost/4, cost*5)
		}
		if bd.Source != wantSource {
			t.Fatalf("%s (%s): expected source %s, got %s", p.sku, p.cat, wantSource, bd.Source)
		}
		if bd.Source == SourceList {
			t.Fatalf("%s (%s): a CONTRACTOR must never fall through to LIST (TIER_DEFAULT seeded)", p.sku, p.cat)
		}
		// No spurious block: the derived/seeded margins here all clear the floor, and AGGREGATE
		// is enforcement-disabled (BYPASS), so nothing should block.
		if bd.Blocked {
			t.Fatalf("%s (%s): no line should block off a healthy derived price, got %+v", p.sku, p.cat, bd)
		}
		if p.cat == "PAVER" {
			pavers++
			if bd.Source != SourceTierCategory {
				t.Fatalf("%s: a real PAVER must hit its seeded TIER_CATEGORY rule, got %s", p.sku, bd.Source)
			}
		}
	}
	if pavers == 0 {
		t.Fatal("expected at least one seeded PAVER product to exercise the tier-category path")
	}

	// A couple as RETAIL: no tier rule applies → LIST (explicit anchor for aggregates, derived
	// for the rest), and the price is still non-zero.
	for _, sku := range []string{"DBS-UL-BRUSSELS", "DBS-AGG-HPB"} {
		var id, baseUOM string
		if err := pool.QueryRow(ctx, `SELECT id::text, base_uom::text FROM products WHERE sku=$1`, sku).Scan(&id, &baseUOM); err != nil {
			t.Fatalf("load retail product %s: %v", sku, err)
		}
		bd, err := svc.ResolvePrice(ctx, ResolveInput{
			CustomerType: "RETAIL", ProductID: id, QtyEntered: 1, UOMEntered: baseUOM, CostCents: cost,
		})
		if err != nil {
			t.Fatalf("retail resolve %s: %v", sku, err)
		}
		if bd.Source != SourceList || bd.FinalCents <= 0 {
			t.Fatalf("retail %s must resolve to a non-zero LIST price, got %+v", sku, bd)
		}
		// A RETAIL LIST resolve returns a unit price WITH a unit even when the anchor is derived
		// (the POST /pricing/resolve endpoint must never return a price with no unit).
		if bd.PriceUOM == "" {
			t.Fatalf("retail %s LIST resolve must carry a PriceUOM (derived-anchor UOM bug), got %+v", sku, bd)
		}
	}
}

// I7, THE DERIVED-ANCHOR UOM TEST (the real failure mode is line persistence). A PAVER with NO
// explicit pricing_list_prices row derives its retail anchor from cost (×1.85); pre-fix the derived
// anchor's UOM was "" so Breakdown.PriceUOM="", and because quote_lines.price_uom is CITEXT NOT NULL
// REFERENCES uoms(code), the wire-in could NOT persist a line for the DOMINANT case (26 of 30 seeded
// products have no anchor). This resolves a real un-anchored PAVER, asserts PriceUOM is non-empty,
// equals the product's base_uom, and is a valid uoms(code); then exercises the actual line-persistence
// path: it derives the line price and INSERTs a quote_line with that price_uom (NOT-NULL + FK must hold),
// and proves the pre-fix "" UOM would have been REJECTED by that same FK. Would have CAUGHT the bug.
func TestIntegration_DerivedAnchorBaseUOM_LinePersists(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := realSvc(pool)

	// A real PAVER product, anchor cleared for the duration so the resolver DERIVES from cost
	// (DIB-378: every PAVER now carries a seed anchor by default).
	var productID, baseUOM string
	if err := pool.QueryRow(ctx, `
		SELECT p.id::text, p.base_uom::text
		  FROM products p JOIN product_categories c ON c.id = p.category_id
		 WHERE c.code = 'PAVER' LIMIT 1`).Scan(&productID, &baseUOM); err != nil {
		t.Skipf("no real PAVER product: %v", err)
	}
	withAnchor(t, pool, productID, 0, "")
	if baseUOM == "" {
		t.Fatal("seed invariant: products.base_uom is NOT NULL and must be non-empty")
	}

	// (a) Resolve the derived-anchor product. PriceUOM must be the product's REAL base UOM, not "".
	const cost int64 = 5000 // ¢/base-unit; derived anchor = cost × 1.85
	bd, err := svc.ResolvePrice(ctx, ResolveInput{
		CustomerType: "CONTRACTOR", ProductID: productID, QtyEntered: 10, UOMEntered: baseUOM, CostCents: cost,
	})
	if err != nil {
		t.Fatalf("resolve derived-anchor PAVER: %v", err)
	}
	if bd.PriceUOM == "" {
		t.Fatalf("derived-anchor Breakdown.PriceUOM must be non-empty (the bug); got %+v", bd)
	}
	if bd.PriceUOM != baseUOM {
		t.Fatalf("derived-anchor PriceUOM must equal the product base_uom %q, got %q", baseUOM, bd.PriceUOM)
	}
	var validUOM bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM uoms WHERE code = $1)`, bd.PriceUOM).Scan(&validUOM); err != nil {
		t.Fatalf("check uoms(code): %v", err)
	}
	if !validUOM {
		t.Fatalf("PriceUOM %q is not a valid uoms(code), the quote_lines FK would reject it", bd.PriceUOM)
	}

	// (b) Exercise the line-persistence path. Derive the trio and INSERT a quote_line with the
	//     derived price_uom, the NOT-NULL + uoms(code) FK is the constraint the pre-fix "" violated.
	lp, err := svc.DeriveLinePrice(ctx, ResolveInput{
		CustomerType: "CONTRACTOR", ProductID: productID, QtyEntered: 10, UOMEntered: baseUOM, CostCents: cost,
	})
	if err != nil {
		t.Fatalf("derive line price: %v", err)
	}
	if lp.PriceUOM != baseUOM || lp.UnitPriceCents <= 0 || lp.LineTotalCents <= 0 {
		t.Fatalf("derived line trio invalid: %+v", lp)
	}

	var customerID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM customers WHERE customer_type='CONTRACTOR' LIMIT 1`).Scan(&customerID); err != nil {
		t.Skipf("no CONTRACTOR customer seeded: %v", err)
	}
	var branchID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type='BRANCH' LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no branch seeded: %v", err)
	}

	var quoteID string
	qno := "IT-DERIVED-" + productID[:8]
	if err := pool.QueryRow(ctx, `
		INSERT INTO quotes (quote_no, customer_id, branch_id) VALUES ($1,$2,$3) RETURNING id::text`,
		qno, customerID, branchID).Scan(&quoteID); err != nil {
		t.Fatalf("insert quote: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM quotes WHERE id=$1`, quoteID) })

	// Pre-fix proof: a "" price_uom (what the derived anchor produced before) is rejected by the
	// NOT-NULL FK to uoms(code), i.e. the line could NOT have persisted for the dominant case.
	if _, err := pool.Exec(ctx, `
		INSERT INTO quote_lines (quote_id, line_no, product_id, qty_entered, uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents)
		VALUES ($1,1,$2,$3,$4,$5,$6,'',$7)`,
		quoteID, productID, 10.0, baseUOM, lp.QtyBase, lp.UnitPriceCents, lp.LineTotalCents); err == nil {
		t.Fatal("a quote_line with an empty price_uom must be REJECTED by the uoms(code) FK (the pre-fix derived-anchor bug)")
	}

	// Post-fix: the derived base UOM persists cleanly (NOT-NULL + FK satisfied).
	if _, err := pool.Exec(ctx, `
		INSERT INTO quote_lines (quote_id, line_no, product_id, qty_entered, uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents)
		VALUES ($1,2,$2,$3,$4,$5,$6,$7,$8)`,
		quoteID, productID, 10.0, baseUOM, lp.QtyBase, lp.UnitPriceCents, lp.PriceUOM, lp.LineTotalCents); err != nil {
		t.Fatalf("insert derived-anchor quote line (the wire-in the fix unblocks): %v", err)
	}

	var gotPriceUOM string
	var gotUnit, gotLine int64
	if err := pool.QueryRow(ctx, `
		SELECT price_uom::text, unit_price_cents, line_total_cents
		  FROM quote_lines WHERE quote_id=$1 AND line_no=2`, quoteID).Scan(&gotPriceUOM, &gotUnit, &gotLine); err != nil {
		t.Fatalf("read back persisted line: %v", err)
	}
	if gotPriceUOM != baseUOM || gotUnit != lp.UnitPriceCents || gotLine != lp.LineTotalCents {
		t.Fatalf("persisted derived line mismatch: got uom=%q unit=%d line=%d, want uom=%q unit=%d line=%d",
			gotPriceUOM, gotUnit, gotLine, baseUOM, lp.UnitPriceCents, lp.LineTotalCents)
	}
}

// I6, fix #2 (SQL): a CUSTOMER_CATEGORY pricing_rules row must MATCH and WIN over the seeded
// TIER_CATEGORY rule. Pre-fix the scope was absent from MatchingRules' WHERE, so the rule was
// dead (never returned by the query). This seeds a real CUSTOMER_CATEGORY rule for a CONTRACTOR
// customer + a real PAVER category and proves it resolves at the CUSTOMER_OVERRIDE level.
func TestIntegration_CustomerCategoryRuleApplies(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)

	var customerID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM customers WHERE customer_type='CONTRACTOR' LIMIT 1`).Scan(&customerID); err != nil {
		t.Skipf("no CONTRACTOR customer seeded: %v", err)
	}
	var productID, categoryID, baseUOM string
	if err := pool.QueryRow(ctx, `
		SELECT p.id::text, p.category_id::text, p.base_uom::text
		  FROM products p JOIN product_categories c ON c.id = p.category_id
		 WHERE c.code='PAVER' LIMIT 1`).Scan(&productID, &categoryID, &baseUOM); err != nil {
		t.Skipf("no PAVER product seeded: %v", err)
	}

	var ruleID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO pricing_rules (scope, customer_id, category_id, calc_type, discount_bps, price_uom, priority, detail_text)
		VALUES ('CUSTOMER_CATEGORY',$1,$2,'PCT_OFF_LIST',2500,'SF',60,'IT-test: account 25% off pavers')
		RETURNING id::text`, customerID, categoryID).Scan(&ruleID); err != nil {
		t.Fatalf("seed CUSTOMER_CATEGORY rule: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM pricing_rules WHERE id=$1`, ruleID) })

	bd, err := realSvc(pool).ResolvePrice(ctx, ResolveInput{
		CustomerID: customerID, CustomerType: "CONTRACTOR", ProductID: productID,
		QtyEntered: 1, UOMEntered: baseUOM, CostCents: 5000,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if bd.Source != SourceCustomerCategory {
		t.Fatalf("expected the CUSTOMER_CATEGORY rule to win (above TIER_CATEGORY), got source=%s", bd.Source)
	}
	if bd.FinalCents <= 0 {
		t.Fatalf("CUSTOMER_CATEGORY resolve must price non-zero, got %+v", bd)
	}
}

// I8, finding #1 (DB CHECK): the 0025 bps-range constraints make the DB itself refuse a
// discount_bps outside 0..10000, so a stray write that bypasses the service can never land a
// "150% off" rule that would drive the resolver to a NEGATIVE price. Pre-0025 this raw INSERT
// succeeded. A valid bps still inserts cleanly.
func TestIntegration_BpsRangeCheck_RejectsOutOfRange(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)

	// 15000 bps must violate the 0025 CHECK (pricing_rules_discount_bps_range).
	_, err := pool.Exec(ctx, `
		INSERT INTO pricing_rules (scope, customer_type, calc_type, discount_bps, price_uom, priority)
		VALUES ('TIER_DEFAULT','GOVERNMENT','PCT_OFF_LIST',15000,'EA',91)`)
	if err == nil {
		t.Fatal("a 15000-bps rule must be rejected by the 0025 DB CHECK (negative-price guard)")
	}

	// A valid bps inserts; clean it up.
	var id string
	if err := pool.QueryRow(ctx, `
		INSERT INTO pricing_rules (scope, customer_type, calc_type, discount_bps, price_uom, priority)
		VALUES ('TIER_DEFAULT','GOVERNMENT','PCT_OFF_LIST',9000,'EA',91)
		RETURNING id::text`).Scan(&id); err != nil {
		t.Fatalf("a valid 9000-bps rule must insert: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM pricing_rules WHERE id=$1`, id) })

	// The override table is bounded too.
	var custID, catID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM customers LIMIT 1`).Scan(&custID); err != nil {
		t.Skipf("no customer seeded: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM product_categories LIMIT 1`).Scan(&catID); err != nil {
		t.Skipf("no category seeded: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO customer_category_overrides (customer_id, category_id, calc_type, discount_bps, price_uom)
		VALUES ($1,$2,'PCT_OFF_LIST',15000,'EA')`, custID, catID); err == nil {
		_, _ = pool.Exec(ctx, `DELETE FROM customer_category_overrides WHERE customer_id=$1 AND category_id=$2`, custID, catID)
		t.Fatal("a 15000-bps override must be rejected by the 0025 DB CHECK")
	}
}

// I9, finding #3 (atomic optimistic lock at the SQL level). Two concurrent SaveRule calls that
// both read version 1 must not both win: the guarded UPDATE ... WHERE id=$1 AND version=$expected
// makes the second match 0 rows → errConflict (409). A missing id → errNotFound (404). Pre-fix
// (WHERE id=$1 only) the second save silently clobbered the first (a lost update).
func TestIntegration_RuleSave_AtomicOptimisticLock(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)

	created, err := repo.CreateRule(ctx, Rule{
		Scope: ScopeTierDefault, CustomerType: "GOVERNMENT", CalcType: CalcPctOffList,
		DiscountBps: p64(1500), PriceUOM: "EA", Priority: 92, IsActive: true,
	})
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM pricing_rules WHERE id=$1`, created.ID) })
	if created.Version != 1 {
		t.Fatalf("a fresh rule must start at version 1, got %d", created.Version)
	}

	next := created
	next.DiscountBps = p64(2000)
	next.Version = created.Version + 1 // 2

	// First writer (expected version 1) commits.
	if _, err := repo.SaveRule(ctx, next, created.Version); err != nil {
		t.Fatalf("the first concurrent save must commit: %v", err)
	}
	// Second writer raced with the SAME stale expected version → 409 (atomic guard).
	if _, err := repo.SaveRule(ctx, next, created.Version); !errors.Is(err, errConflict) {
		t.Fatalf("the second concurrent save must 409 (atomic lock), got %v", err)
	}
	// A guarded write against a missing row → 404 (distinct from the version-mismatch 409).
	ghost := next
	ghost.ID = "00000000-0000-0000-0000-000000000000"
	if _, err := repo.SaveRule(ctx, ghost, created.Version); !errors.Is(err, errNotFound) {
		t.Fatalf("a save against a missing row must 404, got %v", err)
	}
}

// I10, finding #10 (FK violation → 422). A well-formed but nonexistent category/customer id must
// surface as errFKViolation (→ 422), not a bare 500. Covers both pricing_rules and
// pricing_category_policy (the two FK write paths the finding names).
func TestIntegration_ForeignKeyViolation_MapsTo422(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	const ghostCat = "11111111-2222-4333-8444-555555555555" // valid uuid shape, not a real category

	if _, err := repo.CreateRule(ctx, Rule{
		Scope: ScopeTierCategory, CustomerType: "CONTRACTOR", CategoryID: ghostCat,
		CalcType: CalcPctOffList, DiscountBps: p64(1800), PriceUOM: "SF", IsActive: true, Priority: 93,
	}); !errors.Is(err, errFKViolation) {
		t.Fatalf("a rule referencing a nonexistent category must map to errFKViolation (422), got %v", err)
	}

	if _, err := repo.UpsertCategoryPolicy(ctx, CategoryPolicy{
		CategoryID: ghostCat, MarginTargetBps: 3000, MarginFloorBps: 2000,
	}); !errors.Is(err, errFKViolation) {
		t.Fatalf("a category policy for a nonexistent category must map to errFKViolation (422), got %v", err)
	}
}

// I11, finding #2 (scope-edit dim corruption, end-to-end on the real unique index). Editing a
// CUSTOMER_PRODUCT rule to TIER_CATEGORY clears its stale customer_id/product_id server-side, so
// its uq_pricing_rules_active key now equals a real same-(type,category) TIER_CATEGORY peer and the
// edit collides (409). Pre-fix the stale dims survived and the edited rule silently never collided.
func TestIntegration_ScopeEdit_ClearsDimsAndCollides(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := realSvc(pool)

	var customerID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM customers WHERE customer_type='CONTRACTOR' LIMIT 1`).Scan(&customerID); err != nil {
		t.Skipf("no CONTRACTOR customer seeded: %v", err)
	}
	// A WALL category + product: WALL has NO seeded TIER_CATEGORY rule, so our peer is the only one.
	var productID, categoryID string
	if err := pool.QueryRow(ctx, `
		SELECT p.id::text, p.category_id::text FROM products p
		  JOIN product_categories c ON c.id = p.category_id
		 WHERE c.code = 'WALL' LIMIT 1`).Scan(&productID, &categoryID); err != nil {
		t.Skipf("no WALL product seeded: %v", err)
	}

	// (1) A CUSTOMER_PRODUCT rule (carries customer_id + product_id), priority 0.
	cp, err := svc.CreateRule(ctx, Rule{
		Scope: ScopeCustomerProduct, CustomerID: customerID, ProductID: productID,
		CalcType: CalcPctOffList, DiscountBps: p64(1000), PriceUOM: "SF",
	}, "ryan")
	if err != nil {
		t.Fatalf("create CUSTOMER_PRODUCT rule: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM pricing_rules WHERE id=$1`, cp.ID) })

	// (2) A TIER_CATEGORY peer for (CONTRACTOR, WALL), priority 0, the row the edit must collide with.
	peer, err := svc.CreateRule(ctx, Rule{
		Scope: ScopeTierCategory, CustomerType: "CONTRACTOR", CategoryID: categoryID,
		CalcType: CalcPctOffList, DiscountBps: p64(1800), PriceUOM: "SF",
	}, "ryan")
	if err != nil {
		t.Fatalf("create TIER_CATEGORY peer: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM pricing_rules WHERE id=$1`, peer.ID) })

	// (3) Re-scope the CUSTOMER_PRODUCT rule to TIER_CATEGORY (CONTRACTOR, WALL). The server clears
	//     the stale customer_id/product_id, so its normalized key equals the peer → 409 collision.
	_, err = svc.UpdateRule(ctx, cp.ID, RulePatch{
		Scope: strptr(ScopeTierCategory), CustomerType: strptr("CONTRACTOR"), CategoryID: strptr(categoryID),
	}, cp.Version, "ryan")
	if !errors.Is(err, errRuleCollision) {
		t.Fatalf("the re-scoped rule must collide with the TIER_CATEGORY peer (proving stale dims were cleared), got %v", err)
	}
}

// I10, THE REPLACE TEST (D1, HH-184) against the REAL seeded DB + repo + catalog seam. Proves the
// seeded PROVISIONAL breaks compete as ONE single price off the retail anchor and replace the
// resolved price only when strictly cheaper; they never stack on the tier base:
//
//	(a) the PAVER full-skid −5% break loses to the seeded CONTRACTOR Pavers −18% rule at/above a
//	    full skid (1 PLT): the tier price stands unchanged (stacking would drop it to 7790), and
//	    wins as a RETAIL walk-in's price off the anchor (9500 replaces 10000).
//	(b) the NATURAL-STONE partial-skid +10% surcharge (bucketed by WEIGHT via the real ConvertUOM KG
//	    seam) is the contest's structural loser: an uplift can never be strictly cheaper, so the
//	    TIER_DEFAULT base stands and the breakdown says why.
func TestIntegration_SeededQuantityBreaksReplacePerD1(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := realSvc(pool)

	// (a) PAVER full-skid discount against the seeded −18% tier rule.
	var paverID string
	if err := pool.QueryRow(ctx, `
		SELECT p.id::text
		  FROM products p JOIN product_categories c ON c.id = p.category_id
		 WHERE c.code = 'PAVER' LIMIT 1`).Scan(&paverID); err != nil {
		t.Skipf("no real PAVER product: %v", err)
	}
	withAnchor(t, pool, paverID, 10000, "SF")

	// below a skid (1 SF): only the −18% tier rule applies → 8200, no break.
	under, err := svc.ResolvePrice(ctx, ResolveInput{CustomerType: "CONTRACTOR", ProductID: paverID, QtyEntered: 1, UOMEntered: "SF"})
	if err != nil {
		t.Fatalf("resolve under-skid: %v", err)
	}
	if under.Source != SourceTierCategory || under.FinalCents != 8200 {
		t.Fatalf("under a skid: only the seeded −18%% rule applies → 8200, got %+v", under)
	}
	// at/above a full skid (1 PLT): the break's own single price is 10000×0.95 = 9500, the −18%
	// tier already beats it, so the tier price STANDS (the stacking answer 7790 is the old bug).
	above, err := svc.ResolvePrice(ctx, ResolveInput{CustomerType: "CONTRACTOR", ProductID: paverID, QtyEntered: 1, UOMEntered: "PLT"})
	if err != nil {
		t.Fatalf("resolve full-skid: %v", err)
	}
	if above.Source != SourceTierCategory || above.FinalCents != 8200 {
		t.Fatalf("full-skid −5%% loses to −18%% under D1: 8200 stands, got %+v", above)
	}
	if !strings.Contains(above.Detail, "not applied") {
		t.Fatalf("the breakdown must say why the full-skid break did not land, got %q", above.Detail)
	}
	// The replace win on the same schedule: a RETAIL walk-in (no rule matches) crosses the
	// threshold and pays the break's price off the anchor, 9500, source QUANTITY_BREAK.
	retail, err := svc.ResolvePrice(ctx, ResolveInput{CustomerType: "RETAIL", ProductID: paverID, QtyEntered: 1, UOMEntered: "PLT"})
	if err != nil {
		t.Fatalf("resolve full-skid retail: %v", err)
	}
	if retail.Source != SourceQuantityBreak || retail.FinalCents != 9500 {
		t.Fatalf("a walk-in crossing the skid pays the break price 9500, got %+v", retail)
	}

	// (b) NATURAL-STONE partial-skid surcharge never lifts the TIER_DEFAULT base (bucketed by WEIGHT).
	var stoneID, stoneUOM string
	if err := pool.QueryRow(ctx, `SELECT id::text, base_uom::text FROM products WHERE sku='DBS-BANAS-STEP'`).Scan(&stoneID, &stoneUOM); err != nil {
		t.Skipf("no DBS-BANAS-STEP product: %v", err)
	}
	withAnchor(t, pool, stoneID, 10000, stoneUOM)

	// 1 piece ≈ 140 KG < the ~2000 KG partial-skid threshold: the surcharge tier MATCHES, and under
	// D1 an uplift can never win the cheapest-wins contest, so the −15% base 8500 stands (the
	// stacking answer 9350 is the old bug). The KG bucketing goes through the real ConvertUOM seam.
	stone, err := svc.ResolvePrice(ctx, ResolveInput{CustomerType: "CONTRACTOR", ProductID: stoneID, QtyEntered: 1, UOMEntered: stoneUOM})
	if err != nil {
		t.Fatalf("resolve natural-stone partial skid: %v", err)
	}
	if stone.Source != SourceTierDefault || stone.FinalCents != 8500 {
		t.Fatalf("partial-skid surcharge never lifts the tier price: 8500 stands, got %+v", stone)
	}
	if !strings.Contains(stone.Detail, "not applied") {
		t.Fatalf("the breakdown must say why the surcharge never landed, got %q", stone.Detail)
	}
}

// I11, the quantity-break editor BACKEND end-to-end on the real repo: create (header + tiers in one
// tx) → get → resolve (the break REPLACES per D1) → retire (soft-delete) → the retired schedule
// stops matching. Exercises the Manager-write CRUD path + the optimistic-version guard + audit,
// all pricing-owned.
func TestIntegration_QuantityBreakCRUD_RoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := realSvc(pool)

	// A real WALL product (no seeded TIER_CATEGORY rule → resolves via TIER_DEFAULT) with a temp anchor.
	var prodID, baseUOM string
	if err := pool.QueryRow(ctx, `
		SELECT p.id::text, p.base_uom::text
		  FROM products p JOIN product_categories c ON c.id = p.category_id
		 WHERE c.code = 'WALL' LIMIT 1`).Scan(&prodID, &baseUOM); err != nil {
		t.Skipf("no WALL product seeded: %v", err)
	}
	withAnchor(t, pool, prodID, 10000, baseUOM)

	// CREATE a product-scoped volume discount (≥1 base unit → −20% on the base) via the real repo tx.
	created, err := svc.CreateQuantityBreak(ctx, QuantityBreakHeader{
		Code: "IT-CRUD-" + prodID[:8], Name: "IT volume −20%", ProductID: prodID, ThresholdUOM: baseUOM,
		Items: []QuantityBreakItem{{FromQty: 1, ValueType: BreakPctOff, DiscountBps: p64(2000), PriceUOM: baseUOM}},
	}, "ryan-mgr")
	if err != nil {
		t.Fatalf("create quantity break: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM quantity_break_headers WHERE id=$1`, created.ID) })

	// GET round-trips the header + tier.
	got, err := svc.GetQuantityBreak(ctx, created.ID)
	if err != nil || len(got.Items) != 1 || got.Items[0].DiscountBps == nil || *got.Items[0].DiscountBps != 2000 {
		t.Fatalf("get must round-trip the tier, got %+v err=%v", got, err)
	}

	// RESOLVE: the break's own single price off the anchor REPLACES the TIER_DEFAULT (−15%) base
	// under D1: 10000 × 0.80 = 8000 (stacking would answer 10000 × 0.85 × 0.80 = 6800).
	bd, err := svc.ResolvePrice(ctx, ResolveInput{CustomerType: "CONTRACTOR", ProductID: prodID, QtyEntered: 5, UOMEntered: baseUOM})
	if err != nil {
		t.Fatalf("resolve with break: %v", err)
	}
	if bd.Source != SourceQuantityBreak || bd.FinalCents != 8000 {
		t.Fatalf("break REPLACES the tier base: 10000×0.80 = 8000, got %+v", bd)
	}

	// RETIRE under the optimistic version, then the retired schedule stops matching → base only (8500).
	if err := svc.DeleteQuantityBreak(ctx, created.ID, created.Version, "ryan-mgr"); err != nil {
		t.Fatalf("retire quantity break: %v", err)
	}
	after, err := svc.ResolvePrice(ctx, ResolveInput{CustomerType: "CONTRACTOR", ProductID: prodID, QtyEntered: 5, UOMEntered: baseUOM})
	if err != nil {
		t.Fatalf("resolve after retire: %v", err)
	}
	if after.FinalCents != 8500 {
		t.Fatalf("a retired break must stop matching → base only 8500, got %+v", after)
	}
	// a stale-version retire now conflicts (the row moved on).
	if err := svc.DeleteQuantityBreak(ctx, created.ID, created.Version, "ryan-mgr"); !errors.Is(err, errConflict) {
		t.Fatalf("a stale-version retire must conflict, got %v", err)
	}
}

// I-DPA3, the D-PA-3 daily low-margin scan returns the seeded WARN + CRITICAL Brussels rows and
// OMITS the healthy line, over a real PG with migrations 0001→0065 applied (migration 0065 seeds the
// cost + committed sales). The CRITICAL order is a POS CASH ring left at status CONFIRMED, caught
// ONLY by the committed pos_sale facet, so this ALSO proves the facet branch of the committed
// predicate catches cash counter sales (the status branch would miss a CONFIRMED order).
func TestIntegration_LowMarginScan(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := realSvc(pool)

	rows, err := svc.LowMarginScan(ctx, LowMarginFilter{From: time.Now().AddDate(0, 0, -30), To: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("LowMarginScan: %v", err)
	}
	byOrder := map[string]LowMarginScanRow{}
	for _, r := range rows {
		if r.Level != "invalid" {
			byOrder[r.DocNo] = r
		}
	}

	// DIB-388/migration 0087: order numbers are stored in the resolvable '#'-prefixed form, and the
	// migration's backfill brought the pre-existing demo fixtures (0065) onto it. The scan reports the
	// stored number, so these lookups name it.
	crit, ok := byOrder["#SO-DPA3-CRIT"]
	if !ok {
		t.Fatalf("seeded CRITICAL POS-cash sale SO-DPA3-CRIT not returned (committed pos_sale facet predicate?): %+v", rows)
	}
	if crit.Level != "critical" {
		t.Errorf("SO-DPA3-CRIT level = %q, want critical", crit.Level)
	}
	if crit.MarginBps != 1552 {
		t.Errorf("SO-DPA3-CRIT margin_bps = %d, want 1552 ((850-718)*1e4/850)", crit.MarginBps)
	}
	if crit.CostInPriceUOMCents != 718 {
		t.Errorf("SO-DPA3-CRIT cost/SF = %d, want 718 (175 x 4.10 = 717.5 exact, half away from zero)", crit.CostInPriceUOMCents)
	}
	if crit.SKU != "DBS-UL-BRUSSELS" || crit.ProductName == "" {
		t.Errorf("SO-DPA3-CRIT must carry sku+name from the join, got sku=%q name=%q", crit.SKU, crit.ProductName)
	}

	warn, ok := byOrder["#SO-DPA3-WARN"]
	if !ok {
		t.Fatalf("seeded WARN sale SO-DPA3-WARN not returned: %+v", rows)
	}
	if warn.Level != "warn" {
		t.Errorf("SO-DPA3-WARN level = %q, want warn", warn.Level)
	}
	if warn.MarginBps != 2635 {
		t.Errorf("SO-DPA3-WARN margin_bps = %d, want 2635 ((975-718)*1e4/975)", warn.MarginBps)
	}

	// The healthy 1400¢/SF line on the WARN order must NOT surface, only the 975¢/SF WARN line does.
	for _, r := range rows {
		if r.DocNo == "#SO-DPA3-WARN" && r.UnitPriceCents == 1400 {
			t.Errorf("healthy 1400¢/SF line must be omitted, but it surfaced: %+v", r)
		}
	}
}

// - DIB-404: the bulk retail-anchor read behind the catalogue row detail -

// TestIntegration_DIB404_ListPricesFor runs the real SQL for the seam the product catalogue reads its
// "List price" column through. The property that matters is ABSENCE: an id with no anchor must not
// appear in the map, because product.Product.ListPriceCents is a pointer precisely so the catalogue
// can tell "no list price on file" from "the list price is $0.00".
func TestIntegration_DIB404_ListPricesFor(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	repo := NewRepository(pool)

	var anchored, bare, zeroed string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM products ORDER BY sku LIMIT 1`).Scan(&anchored); err != nil {
		t.Fatalf("pick product: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM products ORDER BY sku OFFSET 1 LIMIT 1`).Scan(&bare); err != nil {
		t.Fatalf("pick product: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM products ORDER BY sku OFFSET 2 LIMIT 1`).Scan(&zeroed); err != nil {
		t.Fatalf("pick product: %v", err)
	}
	// DIB-378 backfills an anchor for the whole seeded catalog, so "bare" has to be MADE bare.
	withAnchor(t, pool, anchored, 48000, "EA")
	withAnchor(t, pool, bare, 0, "EA")
	withAnchor(t, pool, zeroed, 0, "EA")
	if _, err := pool.Exec(ctx,
		`INSERT INTO pricing_list_prices (product_id, list_price_cents, price_uom) VALUES ($1::uuid, 0, 'EA')`,
		zeroed); err != nil {
		t.Fatalf("seed zero anchor: %v", err)
	}

	got, err := repo.ListPricesFor(ctx, []string{anchored, bare, zeroed})
	if err != nil {
		t.Fatalf("ListPricesFor: %v", err)
	}

	t.Run("an anchored product carries its cents", func(t *testing.T) {
		if got[anchored] != 48000 {
			t.Errorf("anchored = %d, want 48000", got[anchored])
		}
	})

	t.Run("a product with no anchor is ABSENT, not zero", func(t *testing.T) {
		if _, ok := got[bare]; ok {
			t.Errorf("a product with no pricing_list_prices row must be absent; got %d", got[bare])
		}
	})

	t.Run("an explicit zero anchor is present at zero", func(t *testing.T) {
		v, ok := got[zeroed]
		if !ok {
			t.Fatal("a real 0-cent anchor is a fact the catalogue shows; it must not be dropped")
		}
		if v != 0 {
			t.Errorf("zeroed = %d, want 0", v)
		}
	})

	t.Run("the read is scoped to the requested ids", func(t *testing.T) {
		only, err := repo.ListPricesFor(ctx, []string{anchored})
		if err != nil {
			t.Fatalf("ListPricesFor: %v", err)
		}
		if len(only) != 1 {
			t.Errorf("asked for 1 id, got %d anchors back", len(only))
		}
	})

	t.Run("an empty id set is a no-op, not a full-table scan", func(t *testing.T) {
		empty, err := repo.ListPricesFor(ctx, nil)
		if err != nil {
			t.Fatalf("ListPricesFor(nil): %v", err)
		}
		if len(empty) != 0 {
			t.Errorf("empty request must return an empty map; got %d", len(empty))
		}
	})

	t.Run("the service seam returns the repository read unchanged", func(t *testing.T) {
		// *pricing.Service is what satisfies product.ListPriceReader in main.go.
		via, err := realSvc(pool).ListPriceCentsFor(ctx, []string{anchored, bare})
		if err != nil {
			t.Fatalf("service ListPriceCentsFor: %v", err)
		}
		if via[anchored] != 48000 {
			t.Errorf("service read = %d, want 48000", via[anchored])
		}
		if _, ok := via[bare]; ok {
			t.Error("the service must not invent an anchor the table does not hold")
		}
	})
}

// TestIntegration_DIB437_ListPriceUOMsFor pins the SQL behind the catalogue's price UNIT against a
// real database, the half of DIB-437 that a fake store cannot check.
//
// The load-bearing fact it establishes is that the unit comes from the ANCHOR ROW. products.price_uom
// is a different column with a different job, and in the seeded catalog the two disagree for most
// products; this test sets them to DIFFERENT values on purpose and asserts the anchor's wins.
func TestIntegration_DIB437_ListPriceUOMsFor(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	repo := NewRepository(pool)

	var anchored, bare string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM products ORDER BY sku LIMIT 1`).Scan(&anchored); err != nil {
		t.Fatalf("pick product: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM products ORDER BY sku OFFSET 1 LIMIT 1`).Scan(&bare); err != nil {
		t.Fatalf("pick product: %v", err)
	}
	// The anchor is per PC. The PRODUCT's own price_uom is set to LF, the exact disagreement the
	// seeded data has (Banas coping) and the one a derived-from-the-product implementation would
	// print. DIB-378 backfills an anchor for every seeded product, so "bare" has to be MADE bare.
	withAnchor(t, pool, anchored, 2200, "PC")
	withAnchor(t, pool, bare, 0, "EA")
	var priorProductUOM string
	if err := pool.QueryRow(ctx, `SELECT price_uom::text FROM products WHERE id=$1`, anchored).Scan(&priorProductUOM); err != nil {
		t.Fatalf("read product price_uom: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE products SET price_uom='LF' WHERE id=$1`, anchored); err != nil {
		t.Fatalf("set product price_uom: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `UPDATE products SET price_uom=$2 WHERE id=$1`, anchored, priorProductUOM)
	})

	got, err := repo.ListPriceUOMsFor(ctx, []string{anchored, bare})
	if err != nil {
		t.Fatalf("ListPriceUOMsFor: %v", err)
	}

	t.Run("the unit is the ANCHOR's, not the product's price_uom", func(t *testing.T) {
		if got[anchored] != "PC" {
			t.Errorf("unit = %q, want PC (products.price_uom is LF and must not win)", got[anchored])
		}
	})

	t.Run("a product with no anchor has no unit", func(t *testing.T) {
		if u, ok := got[bare]; ok {
			t.Errorf("a product with no anchor must be absent from the unit map; got %q", u)
		}
	})

	t.Run("an empty id set is a no-op, not a full-table scan", func(t *testing.T) {
		empty, err := repo.ListPriceUOMsFor(ctx, nil)
		if err != nil {
			t.Fatalf("ListPriceUOMsFor(nil): %v", err)
		}
		if len(empty) != 0 {
			t.Errorf("empty request must return an empty map; got %d", len(empty))
		}
	})

	t.Run("the service seam returns the repository read unchanged", func(t *testing.T) {
		// *pricing.Service is what main.go hands product as its ListPriceReader; the optional
		// ListPriceUOMReader assertion is what finds this method at runtime.
		via, err := realSvc(pool).ListPriceUOMsFor(ctx, []string{anchored, bare})
		if err != nil {
			t.Fatalf("service ListPriceUOMsFor: %v", err)
		}
		if via[anchored] != "PC" {
			t.Errorf("service read = %q, want PC", via[anchored])
		}
		if _, ok := via[bare]; ok {
			t.Error("the service must not invent a unit the table does not hold")
		}
	})
}
