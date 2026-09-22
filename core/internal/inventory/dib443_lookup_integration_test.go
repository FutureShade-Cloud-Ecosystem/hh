//go:build integration

// dib443_lookup_integration_test.go, DIB-443 against real Postgres.
//
// The unit half (dib443_lookup_test.go) runs on the DB-free fake, which means it can prove the
// handler's CONTRACT but not the premise the contract rests on: that a non-UUID product_id really
// does blow up in the driver, and that a product with no stock rows really does come back clean.
// Both of those are claims about Postgres, so they are asserted against Postgres.
//
// Without this file the unit suite is asserting a story about a 500 nobody reproduced.
//
//	DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:5432/fix443?sslmode=disable' \
//	  go test -tags=integration ./internal/inventory/ -run DIB443 -count=1 -v

package inventory

import (
	"context"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"io"
	"log/slog"
	"net/http"
	"testing"
)

// TestIntegration_DIB443_NoHistoryProductReadsClean is the reported condition, built rather than
// found: a product row with nothing in stock_balances and nothing in stock_moves. The fresh seed
// gives every product opening moves, so this state only exists on a long-lived database, which is
// exactly why it reached a tester before it reached a test.
func TestIntegration_DIB443_NoHistoryProductReadsClean(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	productID := mkProduct(t, pool, "DIB443-NOHIST")

	// Nothing has ever moved against it, assert that, so the test cannot quietly become a test of
	// a product that does have stock.
	var balances, moves int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM stock_balances WHERE product_id = $1),
		(SELECT count(*) FROM stock_moves    WHERE product_id = $1)`, productID).Scan(&balances, &moves); err != nil {
		t.Fatalf("count fixture rows: %v", err)
	}
	if balances != 0 || moves != 0 {
		t.Fatalf("fixture is not a no-history product: %d balances, %d moves", balances, moves)
	}

	onHand, err := repo.OnHandFor(ctx, productID)
	if err != nil {
		t.Fatalf("OnHandFor on a product with no stock history must not error: %v", err)
	}
	if len(onHand) != 0 {
		t.Fatalf("want no on-hand rows, got %d", len(onHand))
	}

	ledger, err := repo.RecentMoves(ctx, productID, "", 50)
	if err != nil {
		t.Fatalf("RecentMoves on a product with no stock history must not error: %v", err)
	}
	if len(ledger) != 0 {
		t.Fatalf("want no moves, got %d", len(ledger))
	}

	// End to end through the handler: 200, and an empty list rather than a null.
	h := NewHandler(newIntSvc(pool), slog.New(slog.NewTextHandler(io.Discard, nil)))
	rec := get(t, func(w http.ResponseWriter, r *http.Request) { h.onHand(w, r, guards.StaffCaller{}) }, "/api/v1/inventory/on-hand?product_id="+productID)
	if rec.Code != http.StatusOK || rec.Body.String() != `{"on_hand":[]}`+"\n" {
		t.Fatalf("no-history on-hand: got %d %q, want 200 {\"on_hand\":[]}", rec.Code, rec.Body.String())
	}
	rec = get(t, func(w http.ResponseWriter, r *http.Request) { h.moves(w, r, guards.StaffCaller{}) }, "/api/v1/inventory/moves?product_id="+productID)
	if rec.Code != http.StatusOK || rec.Body.String() != `{"moves":[]}`+"\n" {
		t.Fatalf("no-history moves: got %d %q, want 200 {\"moves\":[]}", rec.Code, rec.Body.String())
	}
}

// TestIntegration_DIB443_NonUUIDWouldBreakTheDriver pins the PREMISE of the fix rather than the fix:
// the repository, handed a SKU, fails at the uuid cast. That failure is untyped, so writeErr can
// only call it "internal error", which is the 500 the tester photographed. The handler-level grade
// is what stops it ever getting here; this asserts the thing being stopped is real.
func TestIntegration_DIB443_NonUUIDWouldBreakTheDriver(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	if _, err := repo.OnHandFor(ctx, "DIB443-NOSTOCK"); err == nil {
		t.Fatal("OnHandFor accepted a SKU as a product id, the premise of DIB-443 no longer holds, " +
			"so the handler's 400 may be guarding nothing; re-check before deleting it")
	}
	if _, err := repo.RecentMoves(ctx, "DIB443-NOSTOCK", "", 50); err == nil {
		t.Fatal("RecentMoves accepted a SKU as a product id, see above")
	}

	// And through the handler, the same input is a 400 that never reaches SQL.
	h := NewHandler(newIntSvc(pool), slog.New(slog.NewTextHandler(io.Discard, nil)))
	for name, rec := range map[string]int{
		"on-hand": get(t, func(w http.ResponseWriter, r *http.Request) { h.onHand(w, r, guards.StaffCaller{}) }, "/api/v1/inventory/on-hand?product_id=DIB443-NOSTOCK").Code,
		"moves":   get(t, func(w http.ResponseWriter, r *http.Request) { h.moves(w, r, guards.StaffCaller{}) }, "/api/v1/inventory/moves?product_id=DIB443-NOSTOCK").Code,
	} {
		if rec != http.StatusBadRequest {
			t.Fatalf("%s: a SKU in product_id must be 400, got %d", name, rec)
		}
	}
}
