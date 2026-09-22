//go:build integration

// Integration tests run the real pgx Repository against the live Postgres 16 that
// `make reset` provisions. Run with: make test-integration.
package product

import (
	"context"
	"os"
	"testing"

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
			t.Fatalf(format, a...) // CI has a DB configured → a failure must fail, not skip
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

func firstProductID(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM products ORDER BY sku LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// - DIB-211-C evidence for DIB-15 (A-18 UDF schema promotion) -
//
// DIB-15's whole deliverable is migration 0018: ten in-use BisTrack Product UDFs promoted to typed,
// first-class columns, two facet indexes, and one order-line attribute. The catalogue tests below
// exercise a handful of those columns incidentally through the service, but nothing asserted the
// promotion ITSELF, the shape, the types, the nullability, the defaults, the CHECK. A column silently
// dropped or retyped by a later migration would not have failed anything.
//
// Types are asserted because they were decisions, not accidents: TEXT NOT NULL DEFAULT ” (never NULL,
// so facet queries need no COALESCE), DECIMAL(19,4) for quantities (the money/qty convention), and
// nullable INTEGER for the two counts whose BisTrack source is varchar and still needs coercion.
func TestIntegration_A18_PromotedColumnsMatchTheDecodeDecisions(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)

	type colSpec struct {
		dataType string
		nullable bool
		def      string // column_default, "" when none
	}
	// Exactly the promote-list of seed/udf_promote_list.csv, as 0018 built it.
	want := map[string]colSpec{
		"color_family":                      {"text", false, "''::text"},
		"texture":                           {"text", false, "''::text"},
		"product_size":                      {"text", false, "''::text"},
		"sold_by":                           {"text", false, "''::text"},
		"is_featured":                       {"boolean", false, "false"},
		"is_special_order":                  {"boolean", false, "false"},
		"contractor_pricing_event_discount": {"boolean", false, "false"},
		"full_skid_quantity":                {"integer", true, ""},
		"layer_quantity":                    {"integer", true, ""},
		"minimum_order_qty":                 {"numeric", true, ""},
	}
	for col, spec := range want {
		var dataType, isNullable string
		var colDefault *string
		err := pool.QueryRow(ctx, `
			SELECT data_type, is_nullable, column_default
			  FROM information_schema.columns
			 WHERE table_name='products' AND column_name=$1`, col).Scan(&dataType, &isNullable, &colDefault)
		if err != nil {
			t.Errorf("A-18 promoted column products.%s is MISSING: %v", col, err)
			continue
		}
		if dataType != spec.dataType {
			t.Errorf("products.%s: type %q, A-18 promoted it as %q", col, dataType, spec.dataType)
		}
		if gotNullable := isNullable == "YES"; gotNullable != spec.nullable {
			t.Errorf("products.%s: nullable=%v, want %v", col, gotNullable, spec.nullable)
		}
		got := ""
		if colDefault != nil {
			got = *colDefault
		}
		if got != spec.def {
			t.Errorf("products.%s: default %q, want %q", col, got, spec.def)
		}
	}

	// DECIMAL(19,4) is the quantity convention, not merely "numeric".
	var precision, scale int
	if err := pool.QueryRow(ctx, `
		SELECT numeric_precision, numeric_scale FROM information_schema.columns
		 WHERE table_name='products' AND column_name='minimum_order_qty'`).Scan(&precision, &scale); err != nil {
		t.Fatalf("read minimum_order_qty precision: %v", err)
	}
	if precision != 19 || scale != 4 {
		t.Errorf("products.minimum_order_qty is DECIMAL(%d,%d), A-18's qty convention is DECIMAL(19,4)", precision, scale)
	}

	// Indexes: 0018 created two; the 0021 schema-invariants audit (L-8) DROPPED idx_products_texture
	// as dead (texture was promoted but not queryable). UNION-W4 flipped that: DIB-61 made texture a
	// real facet (filter + count badges), so 0124 re-added the index, exactly the condition L-8 named.
	// Both halves are asserted: a missing color_family index is a regression, and a missing texture
	// index now means the 0124 re-add regressed.
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_indexes WHERE tablename='products' AND indexname='idx_products_color_family'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("A-18 facet index idx_products_color_family missing (n=%d err=%v)", n, err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_indexes WHERE tablename='products' AND indexname='idx_products_texture'`).Scan(&n); err != nil {
		t.Fatalf("read texture index: %v", err)
	} else if n != 1 {
		t.Errorf("A-18 facet index idx_products_texture missing (n=%d), DIB-61 made texture a real facet and 0124 re-added it (the condition L-8 named)", n)
	}
}

// The order-line half of A-18 (D-024): the column ships with a closed value set. Asserted by BEHAVIOUR -
// the CHECK must actually refuse an out-of-set value, not merely exist in the catalog.
func TestIntegration_A18_LienHoldbackStatusIsConstrained(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)

	var dflt string
	if err := pool.QueryRow(ctx, `
		SELECT column_default FROM information_schema.columns
		 WHERE table_name='order_lines' AND column_name='lien_holdback_status'`).Scan(&dflt); err != nil {
		t.Fatalf("A-18 order_lines.lien_holdback_status is MISSING: %v", err)
	}
	if dflt != "'NONE'::text" {
		t.Errorf("lien_holdback_status default is %q, A-18 shipped 'NONE'::text", dflt)
	}

	var lineID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM order_lines LIMIT 1`).Scan(&lineID); err != nil {
		t.Skipf("no seeded order line to exercise the constraint: %v", err)
	}
	// Every documented value is accepted...
	for _, v := range []string{"NONE", "REQUIRED", "EXEMPT"} {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		_, err = tx.Exec(ctx, `UPDATE order_lines SET lien_holdback_status=$1 WHERE id=$2`, v, lineID)
		_ = tx.Rollback(ctx) // never keep the write, this is a constraint probe, not a fixture
		if err != nil {
			t.Errorf("lien_holdback_status must accept the documented value %q: %v", v, err)
		}
	}
	// ...and anything else is refused by the database, not merely by application code.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	_, err = tx.Exec(ctx, `UPDATE order_lines SET lien_holdback_status='HOLDBACK_PLEASE' WHERE id=$1`, lineID)
	_ = tx.Rollback(ctx)
	if err == nil {
		t.Error("A-18's lien_holdback_status CHECK did not refuse an out-of-set value, the value set is not actually closed")
	}
}

// The headline A-18 claim: a DECIMAL attribute round-trips with full scale (12.5000).
func TestIntegration_DecimalRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := NewService(NewRepository(pool))
	id := firstProductID(t, pool)

	p, err := svc.Update(ctx, id, map[string]any{"minimum_order_qty": "12.5"})
	if err != nil {
		t.Fatalf("update decimal: %v", err)
	}
	if p.MinimumOrderQty == nil || *p.MinimumOrderQty != "12.5000" {
		got := "nil"
		if p.MinimumOrderQty != nil {
			got = *p.MinimumOrderQty
		}
		t.Fatalf("minimum_order_qty = %q, want \"12.5000\" (DECIMAL(19,4) scale preserved)", got)
	}
	// Re-read confirms it persisted, not just echoed.
	again, err := svc.Get(ctx, id)
	if err != nil || again.MinimumOrderQty == nil || *again.MinimumOrderQty != "12.5000" {
		t.Fatalf("re-read decimal mismatch: %+v (err %v)", again.MinimumOrderQty, err)
	}
}

// Faceted filter is parameterized AND correct: every row matches the facet value.
func TestIntegration_FacetedQuery(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := NewService(NewRepository(pool))

	var mfr string
	if err := pool.QueryRow(ctx, `SELECT manufacturer FROM products WHERE manufacturer <> '' LIMIT 1`).Scan(&mfr); err != nil {
		t.Skipf("no manufacturer facet data: %v", err)
	}
	got, err := svc.List(ctx, Filter{Manufacturer: mfr})
	if err != nil {
		t.Fatalf("faceted list: %v", err)
	}
	if len(got) == 0 {
		t.Fatalf("expected at least one %q product", mfr)
	}
	for _, p := range got {
		if p.Manufacturer != mfr {
			t.Fatalf("faceted filter leaked a non-%q product: %s/%s", mfr, p.SKU, p.Manufacturer)
		}
	}

	// A SQL-injection-shaped facet value must be treated as a LITERAL: no error AND no
	// rows (a concatenation bug would make the OR '1'='1' match everything).
	inj, err := svc.List(ctx, Filter{Manufacturer: "x' OR '1'='1"})
	if err != nil {
		t.Fatalf("injection-shaped facet must be safely parameterized, got error: %v", err)
	}
	if len(inj) != 0 {
		t.Fatalf("injection-shaped facet returned %d rows; parameterization may be broken", len(inj))
	}
}

// The repository allowlist re-check (L-7) blocks a non-editable column even when the
// service layer is bypassed entirely.
func TestIntegration_RepoUpdate_RejectsNonEditableColumn(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	id := firstProductID(t, pool)

	if _, err := repo.Update(ctx, id, map[string]any{"sku": "HACKED"}); err == nil {
		t.Fatal("repo.Update must reject a non-allowlisted column (sku), even bypassing the service")
	}
	// the product's sku must be unchanged
	var sku string
	_ = pool.QueryRow(ctx, `SELECT sku FROM products WHERE id = $1`, id).Scan(&sku)
	if sku == "HACKED" {
		t.Fatal("sku was mutated despite the allowlist guard")
	}
}

// - M4 catalog-filter delta (A-4) -

// TestIntegration_Facets_CountsHonorFilter proves /facets returns per-value counts that
// reflect the applied filter, against the seeded catalogue.
func TestIntegration_Facets_CountsHonorFilter(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := NewService(NewRepository(pool))

	all, err := svc.Facets(ctx, Filter{})
	if err != nil {
		t.Fatalf("Facets(all): %v", err)
	}
	// Pick a manufacturer that actually has products.
	var mfr string
	var mfrCount int
	for _, f := range all {
		if f.Field != "manufacturer" {
			continue
		}
		for _, v := range f.Values {
			if v.Count > mfrCount {
				mfr, mfrCount = v.Value, v.Count
			}
		}
	}
	if mfr == "" {
		t.Skip("no manufacturer facet data seeded")
	}
	// The manufacturer facet count must equal the number of products the list returns for it.
	list, err := svc.List(ctx, Filter{Manufacturer: mfr})
	if err != nil {
		t.Fatalf("List(mfr): %v", err)
	}
	if len(list) != mfrCount {
		t.Fatalf("manufacturer %q facet count %d != list length %d", mfr, mfrCount, len(list))
	}

	// Under a manufacturer filter, the manufacturer facet must collapse to that one value.
	filtered, err := svc.Facets(ctx, Filter{Manufacturer: mfr})
	if err != nil {
		t.Fatalf("Facets(mfr): %v", err)
	}
	for _, f := range filtered {
		if f.Field != "manufacturer" {
			continue
		}
		if len(f.Values) != 1 || f.Values[0].Value != mfr {
			t.Fatalf("manufacturer facet under its own filter = %+v, want just %q", f.Values, mfr)
		}
	}
}

// TestIntegration_NewFilterParams_Parameterized proves the new q / collection / category_id
// filters are parameterized (injection-shaped input is a literal, never a predicate).
func TestIntegration_NewFilterParams_Parameterized(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := NewService(NewRepository(pool))

	for _, f := range []Filter{
		{Q: "x' OR '1'='1"},
		{Collection: "x' OR '1'='1"},
	} {
		got, err := svc.List(ctx, f)
		if err != nil {
			t.Fatalf("injection-shaped filter %+v must be safe, got: %v", f, err)
		}
		if len(got) != 0 {
			t.Fatalf("injection-shaped filter %+v returned %d rows; parameterization may be broken", f, len(got))
		}
	}
}

// TestIntegration_InStockOnly_ExcludesZeroStock proves in_stock_only narrows to products with
// positive on-hand, reading stock through the inventory StockReader seam.
func TestIntegration_InStockOnly_ExcludesZeroStock(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)

	all, err := NewService(NewRepository(pool)).List(ctx, Filter{})
	if err != nil {
		t.Fatalf("List(all): %v", err)
	}

	// A StockReader that reports only products with on_hand_base > 0 (the same query the
	// inventory module runs).
	svc := NewService(NewRepository(pool)).WithStock(intStockReader{pool: pool})
	inStock, err := svc.List(ctx, Filter{InStockOnly: true})
	if err != nil {
		t.Fatalf("List(in_stock_only): %v", err)
	}
	if len(inStock) > len(all) {
		t.Fatalf("in_stock_only returned MORE (%d) than the full catalogue (%d)", len(inStock), len(all))
	}
	// Every returned product must have positive on-hand somewhere.
	for _, p := range inStock {
		var oh float64
		_ = pool.QueryRow(ctx, `SELECT COALESCE(SUM(on_hand_base),0) FROM stock_balances WHERE product_id = $1`, p.ID).Scan(&oh)
		if oh <= 0 {
			t.Fatalf("in_stock_only leaked a zero-stock product %s (on_hand=%v)", p.SKU, oh)
		}
	}
}

// intStockReader is the integration StockReader: the same on-hand>0 read the inventory module
// exposes, so the product package can exercise in_stock_only without importing inventory.
type intStockReader struct{ pool *pgxpool.Pool }

func (r intStockReader) InStockProductIDs(ctx context.Context, branchID string) ([]string, error) {
	q := `SELECT DISTINCT product_id::text FROM stock_balances WHERE on_hand_base > 0`
	args := []any{}
	if branchID != "" {
		q += ` AND branch_id = $1`
		args = append(args, branchID)
	}
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
