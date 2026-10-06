//go:build integration

// unitusereader_integration_test.go proves the inventory module's
// loaded unit use read (HH-LOAD-1 LD-3): a stock move entered in the
// unit answers as a naming, and with the move gone the read answers
// clear. The concrete reader is what the wiring hands the unit set
// door's seam.
//
// Fixtures are invented. Run (the database URL rides the command line):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5671/ld3sets?sslmode=disable' \
//	  go test -tags integration ./internal/inventory/... -count=1 -run TestLoadedUnitUse
package inventory

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

const fxUseOrgID = "4842a800-0000-4000-8000-00000000d401"

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

func TestLoadedUnitUseSeesTheStockRow(t *testing.T) {
	pool := usePool(t)
	ctx := context.Background()
	productID := "4842a800-0000-4000-8000-00000000d411"
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM stock_moves WHERE product_id = $1::uuid`, []any{productID}},
		{`DELETE FROM products WHERE id = $1::uuid`, []any{productID}},
	} {
		if _, err := pool.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("use fixture clean: %v", err)
		}
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM stock_moves WHERE product_id = $1::uuid`, productID)
		_, _ = pool.Exec(ctx, `DELETE FROM products WHERE id = $1::uuid`, productID)
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO products (id, sku, name, base_uom, price_uom, org_id)
		VALUES ($1::uuid, 'FX-USE-2', 'Fixture Stock Use Product', 'SF', 'BOX', $2::uuid)`,
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
	if _, err := pool.Exec(ctx, `
		INSERT INTO stock_moves (product_id, to_location_id, branch_id, qty_base, qty_entered, uom_entered, move_type)
		VALUES ($1::uuid, (SELECT id FROM locations LIMIT 1), (SELECT id FROM locations LIMIT 1), 20, 1, 'BOX', 'RECEIPT')`,
		productID); err != nil {
		t.Fatalf("plant stock row: %v", err)
	}
	if !read(t, "BOX") {
		t.Fatalf("the stock row stands and the read answers clear")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM stock_moves WHERE product_id = $1::uuid AND uom_entered = 'BOX'`, productID); err != nil {
		t.Fatalf("drop stock row: %v", err)
	}
	if read(t, "BOX") {
		t.Fatalf("the stock row is gone and the read still answers in use")
	}
}
