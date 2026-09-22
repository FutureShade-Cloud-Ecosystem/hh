//go:build integration

// dib396_lease_test.go, DIB-396 T1: the whole-DB invariant lease (test-only), the per-package copy.
//
// This package's W5 quote-hold fixtures write stock_reservations and (on conversion) move
// stock_balances.qty_reserved on a SEEDED product, a number other packages read as availability -
// so they take it in SHARE mode for the length of the test. Nothing here ASSERTS a whole-DB stock
// invariant, so SHARE is correct: drifters do not conflict with each other and the suite keeps its
// parallelism, while the packages that DO assert one (internal/gate, internal/platform/db) simply
// wait for these to finish.
//
// Taken FIRST in the fixture so its release runs LAST (t.Cleanup is LIFO), after the fixture has
// restored the projection it moved.
package quote

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// dib396StockDriftLease fences v_stock_balance_drift / stock_balances whole-table reads.
const dib396StockDriftLease = int64(3960001)

// dib396LeaseShared takes the lease in SHARE mode for the rest of the test.
func dib396LeaseShared(t *testing.T, pool *pgxpool.Pool, key int64) {
	t.Helper()
	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("DIB-396 lease: acquire connection: %v", err)
	}
	if _, err := conn.Exec(context.Background(), "SELECT pg_advisory_lock_shared($1)", key); err != nil {
		conn.Release()
		t.Fatalf("DIB-396 lease: pg_advisory_lock_shared(%d): %v", key, err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock_shared($1)", key)
		conn.Release()
	})
}
