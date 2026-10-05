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
		named, err := LoadedUnitUseReader{}.ProductUnitInUseTx(ctx, tx, productID, uom)
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
