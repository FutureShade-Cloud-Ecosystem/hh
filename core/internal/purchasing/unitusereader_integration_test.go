//go:build integration

// unitusereader_integration_test.go proves the purchasing module's
// loaded unit use read (HH-LOAD-1 LD-3 round 2) beside its own table: a
// vendor sku that sells the product in a unit is a live supplier row
// naming it, so the unit cannot leave the product's set while the sku
// stands and is free once the sku goes. The concrete reader is what the
// wiring hands the unit set door's seam.
//
// Fixtures are invented; the vendor row is the seeded catalogue's.
// Run (the database URL rides the command line):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5673/ld3sets?sslmode=disable' \
//	  go test -tags integration ./internal/purchasing/... -count=1 -run TestLoadedUnitUse
package purchasing

import (
	"context"
	"testing"
)

// fxVendorUseOrgID is the invented org the fixture product carries.
const fxVendorUseOrgID = "4842a800-0000-4000-8000-00000000d401"

// fxVendorUseProductID is the invented fixture product.
const fxVendorUseProductID = "4842a800-0000-4000-8000-00000000d411"

func TestLoadedUnitUseSeesTheVendorSkuFamily(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM purchasing_vendor_skus WHERE product_id = $1::uuid`, []any{fxVendorUseProductID}},
		{`DELETE FROM products WHERE id = $1::uuid`, []any{fxVendorUseProductID}},
	} {
		if _, err := pool.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("vendor use fixture clean: %v", err)
		}
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM purchasing_vendor_skus WHERE product_id = $1::uuid`, fxVendorUseProductID)
		_, _ = pool.Exec(ctx, `DELETE FROM products WHERE id = $1::uuid`, fxVendorUseProductID)
	})
	var vendorID string
	if err := pool.QueryRow(ctx, `SELECT id FROM vendors ORDER BY vendor_no LIMIT 1`).Scan(&vendorID); err != nil {
		t.Fatalf("fixture vendor read: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO products (id, sku, name, base_uom, price_uom, org_id)
		VALUES ($1::uuid, 'FX-VENDOR-USE', 'Fixture Vendor Use Product', 'SF', 'BOX', $2::uuid)`,
		fxVendorUseProductID, fxVendorUseOrgID); err != nil {
		t.Fatalf("fixture product: %v", err)
	}

	read := func(t *testing.T, uom string) bool {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("use tx: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		named, err := LoadedUnitUseReader{}.ProductUnitInUseTx(ctx, tx, fxVendorUseProductID, uom)
		if err != nil {
			t.Fatalf("use read: %v", err)
		}
		return named
	}

	if read(t, "BOX") {
		t.Fatalf("the clear read answers in use")
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO purchasing_vendor_skus (vendor_id, product_id, vendor_sku, vendor_uom, qty_per_vendor_uom)
		VALUES ($1::uuid, $2::uuid, 'FXVENDOR-SKU', 'BOX', 20)`, vendorID, fxVendorUseProductID); err != nil {
		t.Fatalf("plant vendor sku: %v", err)
	}
	if !read(t, "BOX") {
		t.Fatalf("the vendor sku stands and the read answers clear")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM purchasing_vendor_skus WHERE product_id = $1::uuid`, fxVendorUseProductID); err != nil {
		t.Fatalf("drop vendor sku: %v", err)
	}
	if read(t, "BOX") {
		t.Fatalf("the vendor sku is gone and the read still answers in use")
	}
	// A unit no sku names answers clear.
	if read(t, "CYD") {
		t.Fatalf("the untouched unit answers in use")
	}
}
