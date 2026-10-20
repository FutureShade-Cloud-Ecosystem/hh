//go:build integration

// seqtrigger_counterfirst_integration_test.go: the loader table leg for products (PR 556, round 3e).
//
// The load door's claim transaction takes a table's counter before any item row (LD-5) and holds it to the batch
// commit. A single row REST writer that relies on the sync seq trigger takes the row FIRST and the counter after:
// the batch holds the counter and waits for that row, the edit holds the row and waits for the counter. The REST
// writers of a loader table therefore take the counter first, without advancing it (LockProductsSeqTx).
//
// The REST writer (the real Repository.Update) is the session parked through testkit.DrawPark: at the old code it
// stands holding the product row at its draw; at the new code it stands at its counter take holding nothing. The
// batch is a transaction that takes the counter with the load door's own statement and then wants the product
// row. Red at 55593fff: the batch waits for the row, the park releases, the edit wants the counter, 40P01.
package product

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestIntegration_CounterFirst_ProductEditBesideALoadBatch(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	sku := fmt.Sprintf("CF-%d", time.Now().UnixNano())
	var id string
	if err := pool.QueryRow(ctx, `INSERT INTO products (sku, name, base_uom, price_uom, is_active)
		VALUES ($1, $2, 'EA', 'EA', TRUE) RETURNING id::text`, sku, sku).Scan(&id); err != nil {
		t.Fatalf("seed product: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM products WHERE id = $1::uuid`, id) })

	park := testkit.NewDrawPark(t, pool, "products", 55010101)
	edit := make(chan error, 1)
	go func() {
		_, err := NewRepository(park.Pool).Update(ctx, id, map[string]any{"name": "counter first"})
		edit <- err
	}()
	park.WaitParked("the edit stands at its first touch of the products counter")

	var settled atomic.Bool
	batch := make(chan error, 1)
	go func() {
		defer settled.Store(true)
		tx, err := pool.Begin(ctx)
		if err != nil {
			batch <- err
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()
		// The load door's statement for the counter, then the item's row (LD-5).
		if _, err := tx.Exec(ctx, `UPDATE scope_sequences SET next_value = next_value WHERE scope = 'products'`); err != nil {
			batch <- err
			return
		}
		if _, err := tx.Exec(ctx, `SELECT 1 FROM products WHERE id = $1::uuid FOR NO KEY UPDATE`, id); err != nil {
			batch <- err
			return
		}
		batch <- tx.Commit(ctx)
	}()
	park.WaitBlockedOr("the batch waits for the product row", settled.Load)

	park.Answers(map[string]chan error{"edit": edit, "batch": batch})
}
