//go:build integration

// branchprice_integration_test.go: the LD-7 legs against the real database.
// The schema (migration 0323) and its stamps, the editor's set and delete
// with the price change trail carrying the branch, the resolver's branch
// anchor over the real catalog seam, the price_matrix sync scope keeping the
// dealer-wide anchor only, and the pool four concurrency leg for the write
// that opens a transaction.
//
//	Run with:
//
//	DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:5710/ld7prices?sslmode=disable' \
//	  go test -tags=integration -count=1 -p 1 ./internal/pricing/
package pricing

import (
	"context"
	"errors"
	"fmt"
	"os"
	syncstd "sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// bpFix is one branch price world: the fixture org and branches, one product
// per leg, and the real service over the real repository.
type bpFix struct {
	pool *pgxpool.Pool
	svc  *Service
	n    int
	t    *testing.T
}

func newBPFix(t *testing.T) *bpFix {
	t.Helper()
	pool := intPool(t)
	ctx := context.Background()
	if err := testkit.Build(ctx, testkit.PgxPool(pool)); err != nil {
		t.Fatalf("fixture build: %v", err)
	}
	f := &bpFix{pool: pool, svc: realSvc(pool), t: t}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM price_change_log WHERE product_id IN (SELECT id FROM products WHERE sku LIKE 'FXBP-%')`)
		_, _ = pool.Exec(c, `DELETE FROM pricing_branch_prices WHERE product_id IN (SELECT id FROM products WHERE sku LIKE 'FXBP-%')`)
		_, _ = pool.Exec(c, `DELETE FROM pricing_list_prices WHERE product_id IN (SELECT id FROM products WHERE sku LIKE 'FXBP-%')`)
		_, _ = pool.Exec(c, `DELETE FROM products WHERE sku LIKE 'FXBP-%'`)
		_, _ = pool.Exec(c, `DELETE FROM scope_sequences WHERE scope = 'branch_prices'`)
		_ = testkit.Cleanup(c, testkit.PgxPool(pool))
	})
	return f
}

// product seeds one fixture product with the given base and price units and,
// when wanted, an SF row in its unit set at the named factor, and returns its id.
func (f *bpFix) product(uom string, sfFactor float64) string {
	f.t.Helper()
	f.n++
	id := fmt.Sprintf("41b70000-0000-4000-8000-%012d", f.n)
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `
INSERT INTO products (id, sku, name, base_uom, price_uom, is_active, org_id)
VALUES ($1::uuid, $2::citext, $3, $4, $4, TRUE, $5::uuid)`,
		id, fmt.Sprintf("FXBP-%d", f.n), fmt.Sprintf("branch price leg product %d", f.n), uom, testkit.OrgNorthID); err != nil {
		f.t.Fatalf("seed product: %v", err)
	}
	if _, err := f.pool.Exec(ctx, `
INSERT INTO product_uoms (product_id, uom_code, qty_in_base, is_base)
VALUES ($1::uuid, $2, 1, TRUE) ON CONFLICT (product_id, uom_code) DO NOTHING`,
		id, uom); err != nil {
		f.t.Fatalf("seed base row: %v", err)
	}
	if sfFactor > 0 {
		if _, err := f.pool.Exec(ctx, `
INSERT INTO product_uoms (product_id, uom_code, qty_in_base, is_base)
VALUES ($1::uuid, 'SF', $2, FALSE) ON CONFLICT (product_id, uom_code) DO NOTHING`, id, sfFactor); err != nil {
			f.t.Fatalf("seed unit set: %v", err)
		}
	}
	return id
}

// anchor puts the product's dealer-wide anchor in a known state for the test,
// stamped with a seq so the price_matrix pull (seq IS NOT NULL) sees it.
func (f *bpFix) anchor(productID string, cents int64, uom string) {
	f.t.Helper()
	if _, err := f.pool.Exec(context.Background(), priceSeqCTE+`
INSERT INTO pricing_list_prices (product_id, list_price_cents, price_uom, org_id, seq)
VALUES ($1::uuid, $2, $3, $4::uuid, (SELECT next_value FROM pseq))
ON CONFLICT (product_id) DO UPDATE SET list_price_cents = $2, price_uom = $3, seq = (SELECT next_value FROM pseq)`,
		productID, cents, uom, testkit.OrgNorthID); err != nil {
		f.t.Fatalf("seed anchor: %v", err)
	}
}

// TestBranchPriceSchemaCarriesTheSyncStamps pins migration 0323: the table is
// keyed by product and branch and carries the 0213 stamps pricing_list_prices
// carries (org, seq, tombstone, idempotency key), and the price change log
// carries a nullable branch column.
func TestBranchPriceSchemaCarriesTheSyncStamps(t *testing.T) {
	f := newBPFix(t)
	ctx := context.Background()
	cols := map[string]bool{}
	rows, err := f.pool.Query(ctx, `
SELECT column_name FROM information_schema.columns
 WHERE table_name = 'pricing_branch_prices'`)
	if err != nil {
		t.Fatalf("read columns: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		cols[c] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("columns: %v", err)
	}
	for _, want := range []string{"product_id", "branch_id", "list_price_cents", "price_uom",
		"org_id", "seq", "deleted_at", "idempotency_key", "created_at", "updated_at"} {
		if !cols[want] {
			t.Fatalf("pricing_branch_prices lacks column %q: the table must mirror the stamps pricing_list_prices carries", want)
		}
	}
	var nullable string
	if err := f.pool.QueryRow(ctx, `
SELECT is_nullable FROM information_schema.columns
 WHERE table_name = 'price_change_log' AND column_name = 'branch_id'`).Scan(&nullable); err != nil {
		t.Fatalf("price_change_log.branch_id: %v", err)
	}
	if nullable != "YES" {
		t.Fatalf("price_change_log.branch_id is %s, want nullable", nullable)
	}
}

// TestPutBranchPriceWritesRowAndTrail: the set stamps the org read off the
// product, draws a seq, and files the trail row with the branch named.
func TestPutBranchPriceWritesRowAndTrail(t *testing.T) {
	f := newBPFix(t)
	ctx := context.Background()
	product := f.product("EA", 0)

	out, err := f.svc.PutBranchPrice(ctx, product, testkit.BranchNorthAID,
		BranchPriceWrite{ListPriceCents: 48000, PriceUOM: "EA"}, testkit.UserInsideSalesID)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if out.ListPriceCents != 48000 || out.BranchID != testkit.BranchNorthAID {
		t.Fatalf("put answered %+v", out)
	}
	var org, uom string
	var seq int64
	if err := f.pool.QueryRow(ctx, `
SELECT org_id::text, price_uom::text, seq FROM pricing_branch_prices
 WHERE product_id = $1::uuid AND branch_id = $2::uuid`,
		product, testkit.BranchNorthAID).Scan(&org, &uom, &seq); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if org != testkit.OrgNorthID {
		t.Fatalf("row org %q, want the product's org", org)
	}
	if uom != "EA" || seq == 0 {
		t.Fatalf("row uom %q seq %d, want EA and a drawn seq", uom, seq)
	}
	var before, after *int64
	var branch *string
	var changedBy *string
	if err := f.pool.QueryRow(ctx, `
SELECT before_cents, after_cents, branch_id::text, changed_by::text FROM price_change_log
 WHERE product_id = $1::uuid AND field = 'retail_cents' ORDER BY created_at DESC, id DESC LIMIT 1`,
		product).Scan(&before, &after, &branch, &changedBy); err != nil {
		t.Fatalf("read trail: %v", err)
	}
	if before != nil {
		t.Fatalf("a create's trail carries before %d, want NULL", *before)
	}
	if after == nil || *after != 48000 {
		t.Fatalf("trail after %v, want 48000", after)
	}
	if branch == nil || *branch != testkit.BranchNorthAID {
		t.Fatalf("trail branch %v, want the branch named", branch)
	}
	if changedBy == nil || *changedBy != testkit.UserInsideSalesID {
		t.Fatalf("trail actor %v, want the caller attributed", changedBy)
	}
}

// TestPutBranchPriceUpdateTrailAndNoOp: a second, different set files a
// before and after; an identical set moves nothing and files nothing.
func TestPutBranchPriceUpdateTrailAndNoOp(t *testing.T) {
	f := newBPFix(t)
	ctx := context.Background()
	product := f.product("EA", 0)

	if _, err := f.svc.PutBranchPrice(ctx, product, testkit.BranchNorthAID,
		BranchPriceWrite{ListPriceCents: 48000, PriceUOM: "EA"}, testkit.UserInsideSalesID); err != nil {
		t.Fatalf("first put: %v", err)
	}
	var seq1 int64
	if err := f.pool.QueryRow(ctx, `SELECT seq FROM pricing_branch_prices
		 WHERE product_id = $1::uuid AND branch_id = $2::uuid`,
		product, testkit.BranchNorthAID).Scan(&seq1); err != nil {
		t.Fatalf("read seq 1: %v", err)
	}
	if _, err := f.svc.PutBranchPrice(ctx, product, testkit.BranchNorthAID,
		BranchPriceWrite{ListPriceCents: 52000, PriceUOM: "EA"}, testkit.UserInsideSalesID); err != nil {
		t.Fatalf("second put: %v", err)
	}
	var before, after *int64
	if err := f.pool.QueryRow(ctx, `
SELECT before_cents, after_cents FROM price_change_log
 WHERE product_id = $1::uuid AND field = 'retail_cents'
 ORDER BY created_at DESC, id DESC LIMIT 1`, product).Scan(&before, &after); err != nil {
		t.Fatalf("read trail: %v", err)
	}
	if before == nil || *before != 48000 || after == nil || *after != 52000 {
		t.Fatalf("update trail %v then %v, want 48000 then 52000", before, after)
	}
	var seq2 int64
	if err := f.pool.QueryRow(ctx, `SELECT seq FROM pricing_branch_prices
		 WHERE product_id = $1::uuid AND branch_id = $2::uuid`,
		product, testkit.BranchNorthAID).Scan(&seq2); err != nil {
		t.Fatalf("read seq 2: %v", err)
	}
	if seq2 <= seq1 {
		t.Fatalf("the update did not draw a fresh seq: %d then %d", seq1, seq2)
	}

	// The identical set is a no-op: no new trail row, no fresh seq.
	if _, err := f.svc.PutBranchPrice(ctx, product, testkit.BranchNorthAID,
		BranchPriceWrite{ListPriceCents: 52000, PriceUOM: "EA"}, testkit.UserInsideSalesID); err != nil {
		t.Fatalf("no-op put: %v", err)
	}
	var trail int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM price_change_log
		 WHERE product_id = $1::uuid AND field = 'retail_cents'`, product).Scan(&trail); err != nil {
		t.Fatalf("count trail: %v", err)
	}
	if trail != 2 {
		t.Fatalf("the identical set filed trail row %d, want it to move nothing", trail)
	}
	var seq3 int64
	if err := f.pool.QueryRow(ctx, `SELECT seq FROM pricing_branch_prices
		 WHERE product_id = $1::uuid AND branch_id = $2::uuid`,
		product, testkit.BranchNorthAID).Scan(&seq3); err != nil {
		t.Fatalf("read seq 3: %v", err)
	}
	if seq3 != seq2 {
		t.Fatalf("the identical set drew seq %d, want %d kept", seq3, seq2)
	}
}

// TestPutBranchPriceRefusesAForeignReference: an unknown product, branch or
// unit is the friendly 422, never a bare 500.
func TestPutBranchPriceRefusesAForeignReference(t *testing.T) {
	f := newBPFix(t)
	ctx := context.Background()
	product := f.product("EA", 0)
	_, err := f.svc.PutBranchPrice(ctx, product, "00000000-0000-4000-8000-000000000000",
		BranchPriceWrite{ListPriceCents: 100, PriceUOM: "EA"}, testkit.UserInsideSalesID)
	if !errors.Is(err, errFKViolation) {
		t.Fatalf("unknown branch: err %v, want errFKViolation", err)
	}
	_, err = f.svc.PutBranchPrice(ctx, product, testkit.BranchNorthAID,
		BranchPriceWrite{ListPriceCents: 100, PriceUOM: "ZZ"}, testkit.UserInsideSalesID)
	if !errors.Is(err, errFKViolation) {
		t.Fatalf("unknown unit: err %v, want errFKViolation", err)
	}
	_, err = f.svc.PutBranchPrice(ctx, "00000000-0000-4000-8000-000000000000", testkit.BranchNorthAID,
		BranchPriceWrite{ListPriceCents: 100, PriceUOM: "EA"}, testkit.UserInsideSalesID)
	if !errors.Is(err, errFKViolation) {
		t.Fatalf("unknown product: err %v, want errFKViolation", err)
	}
}

// TestDeleteBranchPriceTombstonesAndTrails: the delete tombstones with a
// fresh seq, files the trail with a NULL after, and the second delete is the
// 404; the resolver at that branch answers the dealer-wide anchor again.
func TestDeleteBranchPriceTombstonesAndTrails(t *testing.T) {
	f := newBPFix(t)
	ctx := context.Background()
	product := f.product("EA", 12)
	f.anchor(product, 10000, "EA")

	if _, err := f.svc.PutBranchPrice(ctx, product, testkit.BranchNorthAID,
		BranchPriceWrite{ListPriceCents: 48000, PriceUOM: "EA"}, testkit.UserInsideSalesID); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := f.svc.DeleteBranchPrice(ctx, product, testkit.BranchNorthAID, testkit.UserSalesMgrID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var deleted bool
	var seq int64
	if err := f.pool.QueryRow(ctx, `SELECT (deleted_at IS NOT NULL), seq FROM pricing_branch_prices
		 WHERE product_id = $1::uuid AND branch_id = $2::uuid`,
		product, testkit.BranchNorthAID).Scan(&deleted, &seq); err != nil {
		t.Fatalf("read tombstone: %v", err)
	}
	if !deleted || seq == 0 {
		t.Fatalf("tombstone deleted=%v seq=%d, want the row retained with a fresh seq", deleted, seq)
	}
	var before, after *int64
	if err := f.pool.QueryRow(ctx, `
SELECT before_cents, after_cents FROM price_change_log
 WHERE product_id = $1::uuid AND field = 'retail_cents'
 ORDER BY created_at DESC, id DESC LIMIT 1`, product).Scan(&before, &after); err != nil {
		t.Fatalf("read trail: %v", err)
	}
	if before == nil || *before != 48000 || after != nil {
		t.Fatalf("delete trail %v then %v, want 48000 then NULL", before, after)
	}
	if err := f.svc.DeleteBranchPrice(ctx, product, testkit.BranchNorthAID, testkit.UserSalesMgrID); !errors.Is(err, errNotFound) {
		t.Fatalf("second delete err %v, want errNotFound", err)
	}
	// The anchor at the branch is the dealer-wide one again, on the real seam.
	bd, err := f.svc.ResolvePrice(ctx, ResolveInput{
		CustomerType: "RETAIL", ProductID: product,
		BranchID: testkit.BranchNorthAID, QtyEntered: 1, UOMEntered: "EA",
	})
	if err != nil {
		t.Fatalf("resolve after delete: %v", err)
	}
	if bd.Source != SourceList || bd.FinalCents != 10000 {
		t.Fatalf("after the delete the branch resolved %+v, want the dealer-wide LIST 10000", bd)
	}
}

// TestPutBranchPriceRevivesATombstone: a set over a deleted row brings the
// branch price back and files the trail.
func TestPutBranchPriceRevivesATombstone(t *testing.T) {
	f := newBPFix(t)
	ctx := context.Background()
	product := f.product("EA", 0)
	if _, err := f.svc.PutBranchPrice(ctx, product, testkit.BranchNorthAID,
		BranchPriceWrite{ListPriceCents: 48000, PriceUOM: "EA"}, testkit.UserInsideSalesID); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := f.svc.DeleteBranchPrice(ctx, product, testkit.BranchNorthAID, testkit.UserInsideSalesID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	out, err := f.svc.PutBranchPrice(ctx, product, testkit.BranchNorthAID,
		BranchPriceWrite{ListPriceCents: 49000, PriceUOM: "EA"}, testkit.UserInsideSalesID)
	if err != nil {
		t.Fatalf("revive put: %v", err)
	}
	if out.ListPriceCents != 49000 {
		t.Fatalf("revive answered %+v", out)
	}
	var deleted bool
	if err := f.pool.QueryRow(ctx, `SELECT (deleted_at IS NOT NULL) FROM pricing_branch_prices
		 WHERE product_id = $1::uuid AND branch_id = $2::uuid`,
		product, testkit.BranchNorthAID).Scan(&deleted); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if deleted {
		t.Fatal("the set did not revive the tombstoned row")
	}
}

// TestBranchPriceListFilters: the list answers live rows, filters by product
// and by branch, and never names a tombstone.
func TestBranchPriceListFilters(t *testing.T) {
	f := newBPFix(t)
	ctx := context.Background()
	p1 := f.product("EA", 0)
	p2 := f.product("EA", 0)
	for _, pc := range []struct{ p, b string }{
		{p1, testkit.BranchNorthAID}, {p1, testkit.BranchNorthBID}, {p2, testkit.BranchNorthAID},
	} {
		if _, err := f.svc.PutBranchPrice(ctx, pc.p, pc.b,
			BranchPriceWrite{ListPriceCents: 1000, PriceUOM: "EA"}, testkit.UserInsideSalesID); err != nil {
			t.Fatalf("put %s/%s: %v", pc.p, pc.b, err)
		}
	}
	if err := f.svc.DeleteBranchPrice(ctx, p2, testkit.BranchNorthAID, testkit.UserInsideSalesID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	all, err := f.svc.ListBranchPrices(ctx, BranchPriceFilter{Limit: 100})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("list answered %d rows, want the 2 live rows (the tombstone stays off)", len(all))
	}
	byProduct, err := f.svc.ListBranchPrices(ctx, BranchPriceFilter{ProductID: p1, Limit: 100})
	if err != nil {
		t.Fatalf("list by product: %v", err)
	}
	if len(byProduct) != 2 {
		t.Fatalf("product filter answered %d rows, want 2", len(byProduct))
	}
	byBranch, err := f.svc.ListBranchPrices(ctx, BranchPriceFilter{BranchID: testkit.BranchNorthBID, Limit: 100})
	if err != nil {
		t.Fatalf("list by branch: %v", err)
	}
	if len(byBranch) != 1 || byBranch[0].ProductID != p1 {
		t.Fatalf("branch filter answered %+v, want p1 at north B", byBranch)
	}
}

// TestResolverBranchAnchorTableOnTheRealDB is the table test over the real
// catalog seam: branch present, absent, other branch, and the tier rule off
// the branch anchor, with the units converted through the real unit set.
func TestResolverBranchAnchorTableOnTheRealDB(t *testing.T) {
	f := newBPFix(t)
	ctx := context.Background()
	product := f.product("EA", 12) // 12 SF per EA
	f.anchor(product, 10000, "EA")
	if _, err := f.svc.PutBranchPrice(ctx, product, testkit.BranchNorthAID,
		BranchPriceWrite{ListPriceCents: 48000, PriceUOM: "SF"}, testkit.UserInsideSalesID); err != nil {
		t.Fatalf("put branch price: %v", err)
	}

	cases := []struct {
		name       string
		branch     string
		uom        string
		wantSource string
		wantCents  int64
		wantUOM    string
	}{
		{"the branch row is the anchor", testkit.BranchNorthAID, "SF", SourceList, 48000, "SF"},
		{"another branch falls back", testkit.BranchNorthBID, "EA", SourceList, 10000, "EA"},
	}
	for _, c := range cases {
		bd, err := f.svc.ResolvePrice(ctx, ResolveInput{
			CustomerType: "RETAIL", ProductID: product,
			BranchID: c.branch, QtyEntered: 1, UOMEntered: c.uom,
		})
		if err != nil {
			t.Fatalf("%s: resolve: %v", c.name, err)
		}
		if bd.Source != c.wantSource || bd.FinalCents != c.wantCents || bd.PriceUOM != c.wantUOM {
			t.Fatalf("%s: answered %s %d %s, want %s %d %s",
				c.name, bd.Source, bd.FinalCents, bd.PriceUOM, c.wantSource, c.wantCents, c.wantUOM)
		}
	}
	// The branch anchor converts into the entered unit too: one EA entered
	// is one twelfth of a SF at this set, so the unit price stays 48000 per
	// SF and the line totals 4000.
	bd, err := f.svc.ResolvePrice(ctx, ResolveInput{
		CustomerType: "RETAIL", ProductID: product,
		BranchID: testkit.BranchNorthAID, QtyEntered: 1, UOMEntered: "EA",
	})
	if err != nil {
		t.Fatalf("resolve in EA: %v", err)
	}
	if bd.FinalCents != 48000 || bd.PriceUOM != "SF" || bd.LineTotalCents != 4000 {
		t.Fatalf("converted answer %d %s line %d, want 48000 per SF with line total 4000",
			bd.FinalCents, bd.PriceUOM, bd.LineTotalCents)
	}
}

// TestPriceMatrixSyncScopeKeepsDealerWideOnly (reading 10.8): the price_matrix
// scope's pull projection never learns of a branch price. A product with a
// branch row and a dealer-wide anchor pulls exactly the dealer-wide row, and
// a branch-only product (no dealer-wide anchor) pulls nothing at all.
func TestPriceMatrixSyncScopeKeepsDealerWideOnly(t *testing.T) {
	f := newBPFix(t)
	ctx := context.Background()
	both := f.product("EA", 0)
	f.anchor(both, 10000, "EA")
	if _, err := f.svc.PutBranchPrice(ctx, both, testkit.BranchNorthAID,
		BranchPriceWrite{ListPriceCents: 48000, PriceUOM: "EA"}, testkit.UserInsideSalesID); err != nil {
		t.Fatalf("put branch price: %v", err)
	}
	branchOnly := f.product("EA", 0)
	if _, err := f.svc.PutBranchPrice(ctx, branchOnly, testkit.BranchNorthAID,
		BranchPriceWrite{ListPriceCents: 48000, PriceUOM: "EA"}, testkit.UserInsideSalesID); err != nil {
		t.Fatalf("put branch price 2: %v", err)
	}

	store := NewSyncStore(f.pool, nil)
	items, tombstones, err := store.Pull(ctx, sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubInsideSales}, 0, 200)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	seen := map[string]map[string]any{}
	for _, it := range items {
		seen[it.ID] = it.Data
	}
	data, ok := seen[both]
	if !ok {
		t.Fatalf("the dealer-wide anchor row must still pull")
	}
	if data["list_price_cents"] != float64(10000) {
		t.Fatalf("the pull answers %v for the anchored product, want the dealer-wide 10000, never the branch 48000", data["list_price_cents"])
	}
	if _, ok := data["branch_id"]; ok {
		t.Fatalf("the pull projection carries a branch field: the price_matrix scope serves the dealer-wide anchor only")
	}
	if _, ok := seen[branchOnly]; ok {
		t.Fatalf("a branch-only product pulled through price_matrix: the scope must serve the dealer-wide anchor only")
	}
	if len(tombstones) != 0 {
		t.Fatalf("the pull answered tombstones %v, want none", tombstones)
	}
}

// TestBranchPricePoolFour: the editor's write opens one transaction and holds
// exactly one connection. Three concurrent sets of distinct rows, on a pool
// of four with the fourth connection held behind the scope allocator's row,
// all finish once the blocker releases: no caller asked the pool for a second
// connection inside its transaction.
func TestBranchPricePoolFour(t *testing.T) {
	url := bpURL(t)
	helper := intPool(t)
	ctx := context.Background()
	if err := testkit.Build(ctx, testkit.PgxPool(helper)); err != nil {
		t.Fatalf("fixture build: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = helper.Exec(c, `DELETE FROM price_change_log WHERE product_id IN (SELECT id FROM products WHERE sku LIKE 'FXBP4-%')`)
		_, _ = helper.Exec(c, `DELETE FROM pricing_branch_prices WHERE product_id IN (SELECT id FROM products WHERE sku LIKE 'FXBP4-%')`)
		_, _ = helper.Exec(c, `DELETE FROM products WHERE sku LIKE 'FXBP4-%'`)
		_, _ = helper.Exec(c, `DELETE FROM scope_sequences WHERE scope = 'branch_prices'`)
		_ = testkit.Cleanup(c, testkit.PgxPool(helper))
	})

	products := make([]string, bpPoolSize)
	svc := bpScenario(t, url)
	for i := range products {
		products[i] = fmt.Sprintf("41b80000-0000-4000-8000-%012d", i+1)
		if _, err := helper.Exec(ctx, `
INSERT INTO products (id, sku, name, base_uom, price_uom, is_active, org_id)
VALUES ($1::uuid, $2::citext, $3, 'EA', 'EA', TRUE, $4::uuid)`,
			products[i], fmt.Sprintf("FXBP4-%d", i), fmt.Sprintf("pool four leg product %d", i), testkit.OrgNorthID); err != nil {
			t.Fatalf("seed product %d: %v", i, err)
		}
	}
	release := bpBlockOnScopeAllocator(t, url)
	defer release()

	legCtx, cancel := context.WithTimeout(context.Background(), bpStallDeadline)
	defer cancel()
	errs := make([]error, bpPoolSize)
	var wg syncstd.WaitGroup
	for i := 0; i < bpPoolSize; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = svc.PutBranchPrice(legCtx, products[i], testkit.BranchNorthAID,
				BranchPriceWrite{ListPriceCents: int64(1000 + i), PriceUOM: "EA"}, testkit.UserInsideSalesID)
		}(i)
	}
	parked := bpParked(t, helper)
	release()
	wg.Wait()
	var failed []string
	for i, err := range errs {
		if err != nil {
			failed = append(failed, fmt.Sprintf("put %d: %v", i, err))
		}
	}
	if len(failed) > 0 {
		t.Fatalf("the writes failed at pool size %d with %d parked (connection starvation: a write asked the pool for a second connection inside its transaction):\n%s",
			bpPoolSize, parked, joinLines(failed))
	}
	t.Logf("all %d branch price sets finished with %d parked on the allocator row", bpPoolSize, parked)
}

// TestBranchPricePoolFourBitesAStrayPoolCall is the bite twin: the same leg
// driven by a writer that, inside its transaction, makes one stray call
// through the pool. With three contenders and the fourth connection held,
// the stray call starves and the twin fails fast, which is the proof the leg
// bites on exactly the defect it hunts.
func TestBranchPricePoolFourBitesAStrayPoolCall(t *testing.T) {
	url := bpURL(t)
	helper := intPool(t)
	ctx := context.Background()
	if err := testkit.Build(ctx, testkit.PgxPool(helper)); err != nil {
		t.Fatalf("fixture build: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = helper.Exec(c, `DELETE FROM price_change_log WHERE product_id IN (SELECT id FROM products WHERE sku LIKE 'FXBPB-%')`)
		_, _ = helper.Exec(c, `DELETE FROM pricing_branch_prices WHERE product_id IN (SELECT id FROM products WHERE sku LIKE 'FXBPB-%')`)
		_, _ = helper.Exec(c, `DELETE FROM products WHERE sku LIKE 'FXBPB-%'`)
		_, _ = helper.Exec(c, `DELETE FROM scope_sequences WHERE scope = 'branch_prices'`)
		_ = testkit.Cleanup(c, testkit.PgxPool(helper))
	})

	products := make([]string, bpPoolSize)
	for i := range products {
		products[i] = fmt.Sprintf("41b90000-0000-4000-8000-%012d", i+1)
		if _, err := helper.Exec(ctx, `
INSERT INTO products (id, sku, name, base_uom, price_uom, is_active, org_id)
VALUES ($1::uuid, $2::citext, $3, 'EA', 'EA', TRUE, $4::uuid)`,
			products[i], fmt.Sprintf("FXBPB-%d", i), fmt.Sprintf("bite twin product %d", i), testkit.OrgNorthID); err != nil {
			t.Fatalf("seed product %d: %v", i, err)
		}
	}
	scn := bpScenarioPool(t, url)
	svc := NewService(NewRepository(scn))
	release := bpBlockOnScopeAllocator(t, url)
	defer release()

	legCtx, cancel := context.WithTimeout(context.Background(), bpStallDeadline)
	defer cancel()
	errs := make([]error, bpPoolSize)
	var wg syncstd.WaitGroup
	for i := 0; i < bpPoolSize; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = strayPutBranchPrice(legCtx, scn, svc, products[i], testkit.BranchNorthAID,
				BranchPriceWrite{ListPriceCents: int64(2000 + i), PriceUOM: "EA"}, testkit.UserInsideSalesID)
		}(i)
	}
	parked := bpParked(t, helper)
	release()
	wg.Wait()
	bitten := 0
	for i, err := range errs {
		if err != nil {
			bitten++
			t.Logf("the stray call bit contender %d: %v", i, err)
		}
	}
	if bitten == 0 {
		t.Fatal("the bite twin finished clean: the leg cannot see the starvation it hunts, so it proves nothing")
	}
	t.Logf("the stray pool call bit %d of %d contenders with %d parked", bitten, bpPoolSize, parked)
}

// The pool four leg's shared pieces.

const (
	bpPoolSize      = 4
	bpStallDeadline = 15 * time.Second
	bpOverlapWindow = 5 * time.Second
	bpScenarioApp   = "ld7_branch_price_scenario"
)

func bpURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	return url
}

func bpScenario(t *testing.T, url string) *Service {
	t.Helper()
	return NewService(NewRepository(bpScenarioPool(t, url)))
}

func bpScenarioPool(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	cfg.MaxConns = bpPoolSize
	cfg.ConnConfig.RuntimeParams["application_name"] = bpScenarioApp
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("scenario pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// bpBlockOnScopeAllocator holds the branch_prices scope allocator's row FOR
// UPDATE on its own single connection pool, the first row every editor write
// locks inside its transaction. The returned func releases it (idempotent).
func bpBlockOnScopeAllocator(t *testing.T, url string) func() {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	cfg.MaxConns = 1
	bpool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("blocker pool: %v", err)
	}
	t.Cleanup(bpool.Close)
	tx, err := bpool.Begin(context.Background())
	if err != nil {
		t.Fatalf("blocker begin: %v", err)
	}
	if _, err := tx.Exec(context.Background(), `
INSERT INTO scope_sequences (scope) VALUES ('branch_prices') ON CONFLICT (scope) DO NOTHING`); err != nil {
		t.Fatalf("blocker seed allocator: %v", err)
	}
	if _, err := tx.Exec(context.Background(),
		`SELECT 1 FROM scope_sequences WHERE scope = 'branch_prices' FOR UPDATE`); err != nil {
		t.Fatalf("blocker lock: %v", err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			_ = tx.Rollback(context.Background())
		}
	}
	t.Cleanup(release)
	return release
}

// bpParked waits until every scenario contender is parked on the blocker's
// lock, or the overlap window ends.
func bpParked(t *testing.T, monitor *pgxpool.Pool) int {
	t.Helper()
	parked := 0
	for until := time.Now().Add(bpOverlapWindow); time.Now().Before(until); time.Sleep(20 * time.Millisecond) {
		if err := monitor.QueryRow(context.Background(), `
SELECT count(*) FROM pg_stat_activity
 WHERE application_name = $1 AND wait_event_type = 'Lock'`, bpScenarioApp).Scan(&parked); err != nil {
			t.Fatalf("monitor: %v", err)
		}
		if parked >= bpPoolSize {
			break
		}
	}
	return parked
}

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}

// strayPutBranchPrice is the bite twin of the editor's write: the identical
// statement shape, with ONE stray call through the pool made while the
// transaction's connection is held, the defect shape the pool four leg hunts.
func strayPutBranchPrice(ctx context.Context, pool *pgxpool.Pool, svc *Service,
	productID, branchID string, in BranchPriceWrite, userID string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("bite twin begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM scope_sequences WHERE scope = 'branch_prices' FOR UPDATE`); err != nil {
		return fmt.Errorf("bite twin lock: %w", err)
	}
	// THE STRAY CALL: a second connection from the same pool while the
	// transaction's connection is held. With the fourth pool connection
	// behind the blocker this waits for a connection that never comes.
	if _, err := pool.Exec(ctx, `SELECT 1`); err != nil {
		return fmt.Errorf("the stray pool call failed: %w", err)
	}
	if _, err := svc.PutBranchPrice(ctx, productID, branchID, in, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
