//go:build integration

// syncscope_unit_integration_test.go, the anchor's own unit (HH-LOAD-1
// LD-3 part B): applyMatrixPrice takes the anchor's unit from its
// caller, checked against the product's own unit set, in place of the
// shipped products.price_uom derivation. A caller named unit
// denominates the cents the caller sent and lands on create and on
// conflict; a unit outside the product's set refuses as a typed error
// naming the field, never the value, and nothing lands behind the
// refusal; an absent unit behaves exactly as before. The round trip is
// the cycle's exit line 8: a four decimals fixture price per piece
// loaded per the larger unit prices a line at that price times the
// quantity, rounded once at the line to the cent.
//
// Run with (the URL is the command's own throwaway database):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5712/ld3bcore?sslmode=disable' \
//	  go test -tags=integration ./internal/pricing/ -run TestMatrixAnchor -count=1 -v -p 1
package pricing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// unitFixture is the anchor unit world: a pool over the command's own
// throwaway database and a counter for invented products.
type unitFixture struct {
	pool *pgxpool.Pool
	n    int
	t    *testing.T
}

// newUnitFixtureWithPool wraps a ready pool; the pool 4 legs hand one
// built at four connections so the connection accounting hangs on the
// leg, not on a machine default.
func newUnitFixtureWithPool(t *testing.T, pool *pgxpool.Pool) *unitFixture {
	t.Helper()
	if err := testkit.Build(context.Background(), testkit.PgxPool(pool)); err != nil {
		t.Fatalf("fixture build: %v", err)
	}
	f := &unitFixture{pool: pool, t: t}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM price_change_log WHERE product_id IN (SELECT id FROM products WHERE sku LIKE 'FXLD3B-%')`)
		_, _ = pool.Exec(c, `DELETE FROM pricing_list_prices WHERE product_id IN (SELECT id FROM products WHERE sku LIKE 'FXLD3B-%')`)
		_, _ = pool.Exec(c, `DELETE FROM product_uoms WHERE product_id IN (SELECT id FROM products WHERE sku LIKE 'FXLD3B-%')`)
		_, _ = pool.Exec(c, `DELETE FROM products WHERE sku LIKE 'FXLD3B-%'`)
		_, _ = pool.Exec(c, `DELETE FROM scope_sequences WHERE scope = 'price_matrix'`)
		_ = testkit.Cleanup(c, testkit.PgxPool(pool))
	})
	return f
}

// newUnitFixture builds the world over the pool the shared helper
// derives from DATABASE_URL.
func newUnitFixture(t *testing.T) *unitFixture {
	t.Helper()
	return newUnitFixtureWithPool(t, intPool(t))
}

// product seeds one org stamped product priced per priceUOM with the
// unit set a fixture provides: the PC base row at factor one and, when
// named, one larger unit at its factor (a BOX of one hundred pieces).
// Returns the product id, the anchor row's identity.
func (f *unitFixture) product(priceUOM, largerUOM string, largerFactor int64) string {
	f.n++
	id := fmt.Sprintf("31d3b000-0000-4000-8000-%012d", f.n)
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `
INSERT INTO products (id, sku, name, base_uom, price_uom, is_active, org_id)
VALUES ($1::uuid, $2::citext, $3, 'PC', $4::citext, TRUE, $5::uuid)`,
		id, "FXLD3B-"+id[24:], "FX anchor unit product "+id[24:], priceUOM, testkit.OrgNorthID); err != nil {
		f.t.Fatalf("seed product: %v", err)
	}
	if _, err := f.pool.Exec(ctx, `
INSERT INTO product_uoms (product_id, uom_code, qty_in_base, is_base)
VALUES ($1::uuid, 'PC', 1, TRUE)`, id); err != nil {
		f.t.Fatalf("seed base unit row: %v", err)
	}
	if largerUOM != "" {
		if _, err := f.pool.Exec(ctx, `
INSERT INTO product_uoms (product_id, uom_code, qty_in_base, is_base)
VALUES ($1::uuid, $2::citext, $3, FALSE)`, id, largerUOM, largerFactor); err != nil {
			f.t.Fatalf("seed larger unit row: %v", err)
		}
	}
	return id
}

// write runs the one list price write path on its own transaction the
// way both callers run it, and commits; a refusal rolls back.
func (f *unitFixture) write(in matrixPriceWrite) error {
	ctx := context.Background()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := applyMatrixPrice(ctx, tx, in); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// anchorRow reads the anchor's unit and cents back.
func (f *unitFixture) anchorRow(id string) (string, int64) {
	f.t.Helper()
	var uom string
	var cents int64
	if err := f.pool.QueryRow(context.Background(),
		`SELECT price_uom::text, list_price_cents FROM pricing_list_prices WHERE product_id = $1::uuid`,
		id).Scan(&uom, &cents); err != nil {
		f.t.Fatalf("anchor read: %v", err)
	}
	return uom, cents
}

// TestMatrixAnchorUnitComesFromTheCaller: the caller named unit is the
// anchor's unit on create, not the product's price_uom.
func TestMatrixAnchorUnitComesFromTheCaller(t *testing.T) {
	f := newUnitFixture(t)
	id := f.product("PC", "BOX", 100)
	if err := f.write(matrixPriceWrite{ProductID: id, Cents: 35, PriceUOM: "BOX"}); err != nil {
		t.Fatalf("named unit write: %v", err)
	}
	uom, cents := f.anchorRow(id)
	if uom != "BOX" {
		t.Fatalf("the anchor priced per %q, want the caller's BOX (the product's price_uom is PC)", uom)
	}
	if cents != 35 {
		t.Fatalf("anchor cents %d, want 35", cents)
	}
	var trail int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM price_change_log WHERE product_id = $1::uuid AND field = 'retail_cents'`,
		id).Scan(&trail); err != nil {
		t.Fatalf("trail read: %v", err)
	}
	if trail != 1 {
		t.Fatalf("trail rows %d, want 1 (the write path's own audit spine)", trail)
	}
}

// TestMatrixAnchorUnitOutsideTheSetRefuses: a unit the product's own
// unit set does not carry refuses as the typed error naming the field,
// never the value, and nothing lands behind the refusal: no anchor
// row, no trail row, no seq drawn.
func TestMatrixAnchorUnitOutsideTheSetRefuses(t *testing.T) {
	f := newUnitFixture(t)
	id := f.product("PC", "BOX", 100)
	seq := func() int64 {
		var v int64
		if err := f.pool.QueryRow(context.Background(),
			`SELECT COALESCE((SELECT next_value FROM scope_sequences WHERE scope = 'price_matrix'), 0)`).Scan(&v); err != nil {
			t.Fatalf("seq read: %v", err)
		}
		return v
	}
	seqBefore := seq()
	err := f.write(matrixPriceWrite{ProductID: id, Cents: 35, PriceUOM: "CYD"})
	if !errors.Is(err, errPriceUOMNotInSet) {
		t.Fatalf("the out of set unit answered %v, want the typed price_uom refusal", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "price_uom") {
		t.Fatalf("the refusal %q does not name the field", msg)
	}
	if msg := err.Error(); strings.Contains(msg, "CYD") {
		t.Fatalf("the refusal %q carries the refused value, it must name the field only", msg)
	}
	// A code the catalogue itself does not carry is outside the set the
	// same way: the check is the product's set, not the catalogue's.
	if err := f.write(matrixPriceWrite{ProductID: id, Cents: 35, PriceUOM: "ZZZ"}); !errors.Is(err, errPriceUOMNotInSet) {
		t.Fatalf("the uncatalogued unit answered %v, want the typed price_uom refusal", err)
	}
	var anchors, trail int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM pricing_list_prices WHERE product_id = $1::uuid`, id).Scan(&anchors); err != nil {
		t.Fatalf("anchor count: %v", err)
	}
	if anchors != 0 {
		t.Fatalf("%d anchor rows landed behind the refusal, want none", anchors)
	}
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM price_change_log WHERE product_id = $1::uuid`, id).Scan(&trail); err != nil {
		t.Fatalf("trail count: %v", err)
	}
	if trail != 0 {
		t.Fatalf("%d trail rows landed behind the refusal, want none", trail)
	}
	if seqAfter := seq(); seqAfter != seqBefore {
		t.Fatalf("the refusal moved the price_matrix seq %d to %d, the write never started", seqBefore, seqAfter)
	}
}

// TestMatrixAnchorAbsentUnitKeepsTheShippedDerivation: a write that
// names no unit derives the anchor's unit from products.price_uom
// exactly as before, every existing caller's shape and result.
func TestMatrixAnchorAbsentUnitKeepsTheShippedDerivation(t *testing.T) {
	f := newUnitFixture(t)
	id := f.product("PC", "", 0)
	if err := f.write(matrixPriceWrite{ProductID: id, Cents: 1200}); err != nil {
		t.Fatalf("absent unit write: %v", err)
	}
	uom, cents := f.anchorRow(id)
	if uom != "PC" || cents != 1200 {
		t.Fatalf("the anchor priced per %q at %d, want the shipped derivation PC at 1200", uom, cents)
	}
}

// TestMatrixAnchorNamedUnitMovesAStandingAnchor: the caller's named
// unit denominates its cents, so a named unit write on an anchor that
// already stands per another unit moves the cents and the unit
// together (a confident half moved anchor is the one outcome worse
// than the refusal); an absent unit write still leaves a standing unit
// untouched.
func TestMatrixAnchorNamedUnitMovesAStandingAnchor(t *testing.T) {
	f := newUnitFixture(t)
	id := f.product("PC", "BOX", 100)
	if err := f.write(matrixPriceWrite{ProductID: id, Cents: 1200}); err != nil {
		t.Fatalf("standing anchor write: %v", err)
	}
	if uom, cents := f.anchorRow(id); uom != "PC" || cents != 1200 {
		t.Fatalf("the standing anchor reads %q at %d, want PC at 1200", uom, cents)
	}
	before := int64(1200)
	if err := f.write(matrixPriceWrite{ProductID: id, Cents: 35, PriceUOM: "BOX", Before: &before}); err != nil {
		t.Fatalf("named unit move: %v", err)
	}
	if uom, cents := f.anchorRow(id); uom != "BOX" || cents != 35 {
		t.Fatalf("the moved anchor reads %q at %d, want the caller's BOX at 35 (the cents are per BOX)", uom, cents)
	}
	// An absent unit write on the standing anchor leaves the unit as it
	// stands: the existing callers' behaviour is untouched.
	if err := f.write(matrixPriceWrite{ProductID: id, Cents: 36}); err != nil {
		t.Fatalf("absent unit write on the moved anchor: %v", err)
	}
	if uom, cents := f.anchorRow(id); uom != "BOX" || cents != 36 {
		t.Fatalf("the absent unit write moved the unit to %q at %d, want BOX at 36", uom, cents)
	}
}

// TestMatrixAnchorNamedUnitRoundTripsThroughTheResolver is the cycle
// exit line 8: a four decimals fixture price per piece (0.0035 a
// piece, sub cent) loads per the larger unit the fixture's set
// provides (a BOX of one hundred, 35 whole cents a box) and prices a
// line at that price times the quantity, rounded once at the line to
// the cent: 777 pieces answer 272 cents, never the zero a per unit
// rounding would give first (0.35 cents a piece rounds to 0).
func TestMatrixAnchorNamedUnitRoundTripsThroughTheResolver(t *testing.T) {
	f := newUnitFixture(t)
	id := f.product("PC", "BOX", 100)
	if err := f.write(matrixPriceWrite{ProductID: id, Cents: 35, PriceUOM: "BOX"}); err != nil {
		t.Fatalf("anchor load per the larger unit: %v", err)
	}
	svc := realSvc(f.pool)
	bd, err := svc.ResolvePrice(context.Background(), ResolveInput{
		ProductID: id, QtyEntered: 777, UOMEntered: "PC",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if bd.Source != SourceList {
		t.Fatalf("resolve source %q, want the plain list anchor", bd.Source)
	}
	if bd.PriceUOM != "BOX" || bd.FinalCents != 35 {
		t.Fatalf("resolve answered %q at %d cents, want BOX at 35 (the anchor's own unit)", bd.PriceUOM, bd.FinalCents)
	}
	if bd.LineTotalCents != 272 {
		t.Fatalf("the line answered %d cents, want 272 (0.0035 a piece times 777, rounded once at the line)", bd.LineTotalCents)
	}
	bd2, err := svc.ResolvePrice(context.Background(), ResolveInput{
		ProductID: id, QtyEntered: 1000, UOMEntered: "PC",
	})
	if err != nil {
		t.Fatalf("resolve 1000: %v", err)
	}
	if bd2.LineTotalCents != 350 {
		t.Fatalf("the thousand piece line answered %d cents, want 350 (ten boxes at 35)", bd2.LineTotalCents)
	}
}
