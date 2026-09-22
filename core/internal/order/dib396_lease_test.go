//go:build integration

// dib396_lease_test.go, DIB-396 T1: the whole-DB invariant lease (test-only), the per-package copy.
//
// Verbatim in behaviour to internal/inventory's: an advisory lock held per session, so it spans the
// processes `go test ./...` starts one of per package. This package's W5 reservation fixtures write
// stock_reservations and move stock_balances.qty_reserved on a SEEDED product, a number other
// packages read as availability, so they take it in SHARE mode for the length of the test.
//
// SHARE, not EXCLUSIVE: nothing in this package ASSERTS a whole-DB stock invariant, it only
// transiently perturbs one. Drifters do not conflict with each other, so the suite keeps its
// parallelism; the assertions that DO take it exclusively (internal/gate, internal/platform/db)
// simply wait for these to finish.
//
// Taken FIRST in the fixture so its release runs LAST (t.Cleanup is LIFO), after the fixture has
// restored the projection it moved.
package order

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
