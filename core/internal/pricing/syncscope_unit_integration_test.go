//go:build integration

// syncscope_unit_integration_test.go, the anchor's own unit (HH-LOAD-1
// LD-3 part B, round 2): applyMatrixPrice takes the anchor's unit from
// its caller, checked against the product's own unit set, in place of
// the shipped products.price_uom derivation. A caller named unit
// denominates the cents the caller sent and lands on create and on
// conflict, as the catalogue's own stored code (the caller's case is
// normalised away); a unit outside the product's set refuses as a
// typed error naming the field, never the value, and nothing lands
// behind the refusal. A write that names no unit refuses a standing
// anchor whose unit is not the product's own price unit, fail closed,
// so the desk batch and the sync door can never silently
// misdenominate one, and lands as before otherwise. A product no row
// carries refuses naming product_id. The round trip below is the
// cycle's exit line 8 for lines away from a half cent edge and NOT at
// the edge: the edge itself is the plan's risk 1 (the resolver's line
// total crosses float64), documented by the skipped probe, the fix
// waiting on the lead's decision outside this part's fence.
//
// Run with (the URL is the command's own throwaway database):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5714/ld3bcore?sslmode=disable' \
//	  go test -tags=integration ./internal/pricing/ -run TestMatrixAnchor -count=1 -v -p 1
package pricing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
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
		_, _ = pool.Exec(c, `DELETE FROM price_change_lines WHERE batch_id IN (SELECT id FROM price_batches WHERE batch_no LIKE 'FXBATCH-LD3B-%')`)
		_, _ = pool.Exec(c, `DELETE FROM price_batches WHERE batch_no LIKE 'FXBATCH-LD3B-%'`)
		_, _ = pool.Exec(c, `DELETE FROM product_uoms WHERE product_id IN (SELECT id FROM products WHERE sku LIKE 'FXLD3B-%')`)
		_, _ = pool.Exec(c, `DELETE FROM products WHERE sku LIKE 'FXLD3B-%'`)
		// The scope_sequences row is the module's shared allocator, not
		// this fixture's: a throwaway database forgives dropping it, a
		// shared one does not (the counter resets and seqs collide), so
		// the cleanup leaves it standing.
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
// than the refusal); an absent unit write on an anchor standing in a
// unit other than the product's price unit refuses, fail closed, and
// stands nothing behind the refusal.
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
	// An absent unit write on the standing anchor refuses, fail closed:
	// the anchor stands per BOX, the product's price unit is PC, and a
	// caller that names no unit would land a per PC figure on a per BOX
	// anchor (the round 1 behaviour this leg pinned is exactly the
	// mixed unit write the round 2 ruling refuses). The anchor and its
	// trail stand untouched behind the refusal.
	err := f.write(matrixPriceWrite{ProductID: id, Cents: 36})
	if !errors.Is(err, errAnchorUnitMismatch) {
		t.Fatalf("the absent unit write on the per BOX anchor answered %v, want the typed price_uom refusal", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "price_uom") {
		t.Fatalf("the refusal %q does not name the field", msg)
	}
	if uom, cents := f.anchorRow(id); uom != "BOX" || cents != 35 {
		t.Fatalf("the refused write moved the anchor to %q at %d, want BOX at 35 standing", uom, cents)
	}
	var trail int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM price_change_log WHERE product_id = $1::uuid AND field = 'retail_cents'`,
		id).Scan(&trail); err != nil {
		t.Fatalf("trail read: %v", err)
	}
	if trail != 2 {
		t.Fatalf("the trail holds %d rows behind the refusal, want the write path's own 2", trail)
	}
}

// TestMatrixAnchorNamedUnitRoundTripsThroughTheResolver is the cycle
// exit line 8 for lines AWAY FROM a half cent edge and NOT at the edge
// (the claim, narrowed in round 2): a four decimals fixture price per
// piece (0.0035 a piece, sub cent) loads per the larger unit the
// fixture's set provides (a BOX of one hundred, 35 whole cents a box)
// and prices a line at that price times the quantity, rounded once at
// the line to the cent: 777 pieces answer 272 cents, never the zero a
// per unit rounding would give first (0.35 cents a piece rounds to 0).
// The edge itself is the plan's risk 1 and the skipped probe above;
// this leg proves the round trip only where the float does not sit on
// a half.
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

// TestMatrixAnchorHalfCentEdgeIsRiskOne documents the one line the
// round trip above cannot prove: the half cent edge. The case, with
// its exact numbers: 410 pieces priced at 15 cents per BOX of one
// hundred is exactly 61.5 cents of line, and HH's own rounding is
// half away from zero, so the right answer is 62. The resolver prices
// the line through float64 (410/100 is 4.1, and 4.1 times 15 is
// 61.499999999999993), rounds, and answers 61, one cent low. This is
// the plan's own risk 1 (floating point in the resolver); the code
// that is wrong is outside this part's fence and shared with the
// till, and the fix, an integer line path or the plan's decimal path
// move, is the lead's pending decision with the principal. The probe
// rides skipped, naming risk 1 and the pending decision, so the case
// stays visible and flips on the moment the resolver stops crossing
// the float; the round trip above keeps its narrowed claim (away from
// the edge, not at it).
func TestMatrixAnchorHalfCentEdgeIsRiskOne(t *testing.T) {
	t.Skip("the plan's risk 1, not this part's fence: the resolver's line total crosses float64 at a half cent edge (410 pieces at 15 cents per BOX of one hundred answers 61, the exact 61.5 rounds to 62); the fix, an integer line path or the plan's decimal path move, is the lead's pending decision shared with the till lane, so the probe rides skipped and flips on when the resolver is fixed")
	f := newUnitFixture(t)
	id := f.product("PC", "BOX", 100)
	if err := f.write(matrixPriceWrite{ProductID: id, Cents: 15, PriceUOM: "BOX"}); err != nil {
		t.Fatalf("anchor load per the larger unit: %v", err)
	}
	bd, err := realSvc(f.pool).ResolvePrice(context.Background(), ResolveInput{
		ProductID: id, QtyEntered: 410, UOMEntered: "PC",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if bd.PriceUOM != "BOX" || bd.FinalCents != 15 {
		t.Fatalf("resolve answered %q at %d cents, want BOX at 15 (the anchor's own unit)", bd.PriceUOM, bd.FinalCents)
	}
	if bd.LineTotalCents != 62 {
		t.Fatalf("the half cent line answered %d cents, want 62 (410 pieces at 15 cents per BOX of one hundred is exactly 61.5, half away from zero)", bd.LineTotalCents)
	}
}

// deskPublish runs the desk batch's own path: one APPROVED batch, one
// after_retail_cents line for the product, the module's own batch
// apply. A sheet's retail figure is per the product's price unit (the
// sheet's product reference names products.price_uom, FOB freight is
// per it), so the helper names no unit, exactly as ApplyBatch does.
// Returns the batch number so a caller can read the batch's status.
func (f *unitFixture) deskPublish(productID string, afterCents int64) (string, error) {
	ctx := context.Background()
	batchNo := fmt.Sprintf("FXBATCH-LD3B-%d", time.Now().UnixNano())
	var batchID string
	if err := f.pool.QueryRow(ctx, `
INSERT INTO price_batches (batch_no, status, effective_date, source_ref)
VALUES ($1, 'APPROVED', now(), 'fx ld3b desk') RETURNING id::text`, batchNo).Scan(&batchID); err != nil {
		return batchNo, err
	}
	if _, err := f.pool.Exec(ctx, `
INSERT INTO price_change_lines (batch_id, product_id, after_retail_cents)
VALUES ($1::uuid, $2::uuid, $3)`, batchID, productID, afterCents); err != nil {
		return batchNo, err
	}
	_, _, err := realSvc(f.pool).ApplyBatch(ctx, batchID, testkit.UserInsideSalesID, time.Now().UTC())
	return batchNo, err
}

// batchTrail counts the trail rows one batch wrote.
func (f *unitFixture) batchTrail(batchNo string) int {
	f.t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM price_change_log
		  WHERE batch_id = (SELECT id FROM price_batches WHERE batch_no = $1)`,
		batchNo).Scan(&n); err != nil {
		f.t.Fatalf("batch trail read: %v", err)
	}
	return n
}

// batchStatus reads one batch's status back by its number.
func (f *unitFixture) batchStatus(batchNo string) string {
	f.t.Helper()
	var status string
	if err := f.pool.QueryRow(context.Background(),
		`SELECT status FROM price_batches WHERE batch_no = $1`, batchNo).Scan(&status); err != nil {
		f.t.Fatalf("batch status read: %v", err)
	}
	return status
}

// doorWrite pushes one anchor write through the sync door's own entry,
// the same store the engine wires, naming no unit, the way every
// device payload arrives.
func (f *unitFixture) doorWrite(t *testing.T, productID string, cents int64) (sync.RowState, error) {
	t.Helper()
	store := NewSyncStore(f.pool, nil)
	return store.Write(context.Background(),
		sync.ScopeFilter{OrgID: testkit.OrgNorthID}, productID, "",
		map[string]any{"id": productID, "list_price_cents": cents})
}

// TestMatrixAnchorAbsentUnitRefusesAMismatchedStandingAnchor is the
// round 2 ruling: the desk batch names no unit, so an anchor standing
// per BOX under a product priced per PC would take the sheet's per PC
// figure silently, a factor of the units apart, confident and wrong.
// The apply refuses as the typed error naming price_uom, fail closed,
// and the batch writes nothing: no anchor move, no trail row, no seq
// drawn, the batch left APPROVED.
func TestMatrixAnchorAbsentUnitRefusesAMismatchedStandingAnchor(t *testing.T) {
	f := newUnitFixture(t)
	id := f.product("PC", "BOX", 100)
	if err := f.write(matrixPriceWrite{ProductID: id, Cents: 3500, PriceUOM: "BOX"}); err != nil {
		t.Fatalf("anchor load per the larger unit: %v", err)
	}
	seq := func() int64 {
		var v int64
		if err := f.pool.QueryRow(context.Background(),
			`SELECT COALESCE((SELECT next_value FROM scope_sequences WHERE scope = 'price_matrix'), 0)`).Scan(&v); err != nil {
			t.Fatalf("seq read: %v", err)
		}
		return v
	}
	seqBefore := seq()
	batchNo, err := f.deskPublish(id, 3600)
	if !errors.Is(err, errAnchorUnitMismatch) {
		t.Fatalf("the desk batch onto the per BOX anchor answered %v, want the typed price_uom refusal", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "price_uom") {
		t.Fatalf("the refusal %q does not name the field", msg)
	}
	if uom, cents := f.anchorRow(id); uom != "BOX" || cents != 3500 {
		t.Fatalf("the refused batch moved the anchor to %q at %d, want BOX at 3500 standing", uom, cents)
	}
	if n := f.batchTrail(batchNo); n != 0 {
		t.Fatalf("the refused batch wrote %d trail rows, want none", n)
	}
	if n := f.batchStatus(batchNo); n != "APPROVED" {
		t.Fatalf("the refused batch sits %q, want APPROVED, nothing applied", n)
	}
	if seqAfter := seq(); seqAfter != seqBefore {
		t.Fatalf("the refused batch moved the price_matrix seq %d to %d, the write never started", seqBefore, seqAfter)
	}
}

// TestMatrixAnchorAbsentUnitLandsOnAnAnchorInTheProductsPriceUnit: the
// same desk write onto an anchor standing in the product's own price
// unit lands exactly as before, every shipped batch's shape and
// result, the trail and the seq with it.
func TestMatrixAnchorAbsentUnitLandsOnAnAnchorInTheProductsPriceUnit(t *testing.T) {
	f := newUnitFixture(t)
	id := f.product("PC", "", 0)
	if err := f.write(matrixPriceWrite{ProductID: id, Cents: 3500}); err != nil {
		t.Fatalf("standing anchor write: %v", err)
	}
	batchNo, err := f.deskPublish(id, 3600)
	if err != nil {
		t.Fatalf("the desk batch onto the per PC anchor: %v", err)
	}
	if uom, cents := f.anchorRow(id); uom != "PC" || cents != 3600 {
		t.Fatalf("the desk publish left the anchor %q at %d, want PC at 3600", uom, cents)
	}
	if n := f.batchTrail(batchNo); n != 1 {
		t.Fatalf("the landed batch wrote %d trail rows, want its one retail row", n)
	}
	if n := f.batchStatus(batchNo); n != "APPLIED" {
		t.Fatalf("the landed batch sits %q, want APPLIED", n)
	}
}

// TestMatrixSyncDoorAbsentUnitRefusesAMismatchedStandingAnchor: the
// sync door names no unit either, so the same refusal stands behind
// the same check for a device's push, and the anchor stands untouched.
func TestMatrixSyncDoorAbsentUnitRefusesAMismatchedStandingAnchor(t *testing.T) {
	f := newUnitFixture(t)
	id := f.product("PC", "BOX", 100)
	if err := f.write(matrixPriceWrite{ProductID: id, Cents: 3500, PriceUOM: "BOX"}); err != nil {
		t.Fatalf("anchor load per the larger unit: %v", err)
	}
	_, err := f.doorWrite(t, id, 3600)
	if !errors.Is(err, errAnchorUnitMismatch) {
		t.Fatalf("the sync door write onto the per BOX anchor answered %v, want the typed price_uom refusal", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "price_uom") {
		t.Fatalf("the refusal %q does not name the field", msg)
	}
	if uom, cents := f.anchorRow(id); uom != "BOX" || cents != 3500 {
		t.Fatalf("the refused door write moved the anchor to %q at %d, want BOX at 3500 standing", uom, cents)
	}
	var trail int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM price_change_log WHERE product_id = $1::uuid AND field = 'retail_cents'`,
		id).Scan(&trail); err != nil {
		t.Fatalf("trail read: %v", err)
	}
	if trail != 1 {
		t.Fatalf("the trail holds %d rows behind the refusal, want the load's own 1", trail)
	}
}

// TestMatrixSyncDoorAbsentUnitLandsOnAnAnchorInTheProductsPriceUnit:
// the door's write onto an anchor standing in the product's own price
// unit lands exactly as before, the shipped push shape and result.
func TestMatrixSyncDoorAbsentUnitLandsOnAnAnchorInTheProductsPriceUnit(t *testing.T) {
	f := newUnitFixture(t)
	id := f.product("PC", "", 0)
	if err := f.write(matrixPriceWrite{ProductID: id, Cents: 3500}); err != nil {
		t.Fatalf("standing anchor write: %v", err)
	}
	rs, err := f.doorWrite(t, id, 3600)
	if err != nil {
		t.Fatalf("the door write onto the per PC anchor: %v", err)
	}
	if rs.Data["list_price_cents"] != float64(3600) {
		t.Fatalf("the door read back %v, want the landed 3600", rs.Data["list_price_cents"])
	}
	if uom, cents := f.anchorRow(id); uom != "PC" || cents != 3600 {
		t.Fatalf("the door write left the anchor %q at %d, want PC at 3600", uom, cents)
	}
}

// TestMatrixAnchorNamedUnitLandsTheCataloguesOwnCode: the caller's
// case is normalised away, the anchor lands as the catalogue's own
// stored code, the way the unit doors read the code back, so the
// breakdown and the sync projection never carry a code the catalogue
// does not spell.
func TestMatrixAnchorNamedUnitLandsTheCataloguesOwnCode(t *testing.T) {
	f := newUnitFixture(t)
	id := f.product("PC", "BOX", 100)
	if err := f.write(matrixPriceWrite{ProductID: id, Cents: 35, PriceUOM: "box"}); err != nil {
		t.Fatalf("lower case named unit write: %v", err)
	}
	if uom, cents := f.anchorRow(id); uom != "BOX" || cents != 35 {
		t.Fatalf("the anchor landed per %q at %d, want the catalogue's BOX at 35", uom, cents)
	}
	bd, err := realSvc(f.pool).ResolvePrice(context.Background(), ResolveInput{
		ProductID: id, QtyEntered: 100, UOMEntered: "PC",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if bd.PriceUOM != "BOX" || bd.LineTotalCents != 35 {
		t.Fatalf("resolve answered %q at %d cents, want the catalogue's BOX at 35 for the hundred piece line", bd.PriceUOM, bd.LineTotalCents)
	}
}

// TestMatrixAnchorMissingProductNamesTheProductField: a product no row
// carries refuses under its own field, product_id, the unit named or
// not, never as the unit set refusal (round 1 named the wrong field
// for a missing product).
func TestMatrixAnchorMissingProductNamesTheProductField(t *testing.T) {
	f := newUnitFixture(t)
	missing := "31d3bfff-0000-4000-8000-000000000001"
	err := f.write(matrixPriceWrite{ProductID: missing, Cents: 35, PriceUOM: "BOX"})
	if !errors.Is(err, errProductMissing) {
		t.Fatalf("the missing product answered %v, want the typed product_id refusal", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "product_id") {
		t.Fatalf("the refusal %q does not name the field", msg)
	}
	if msg := err.Error(); strings.Contains(msg, "price_uom") {
		t.Fatalf("the refusal %q names price_uom for a missing product, want product_id", msg)
	}
	err = f.write(matrixPriceWrite{ProductID: missing, Cents: 35})
	if !errors.Is(err, errProductMissing) {
		t.Fatalf("the absent unit write on the missing product answered %v, want the typed product_id refusal", err)
	}
	var rows int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM pricing_list_prices WHERE product_id = $1::uuid`, missing).Scan(&rows); err != nil {
		t.Fatalf("anchor count: %v", err)
	}
	if rows != 0 {
		t.Fatalf("%d anchor rows landed for a missing product, want none", rows)
	}
}
