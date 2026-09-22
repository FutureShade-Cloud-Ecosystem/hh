//go:build integration

// dib396_lease_test.go, DIB-396 T1: the whole-DB invariant lease (test-only).
//
// A handful of fixtures MUST break a global invariant for the length of a test: a
// stock_move written with no matching balance projection, or a stock_balances row forced
// without a matching move. On the SHARED integration DB that window is visible to every
// other package running in parallel (`go test ./...` runs one process per package), and
// the whole-DB reconciliation gates, internal/gate's TestGate_StockReconciles_NoNegative
// and internal/platform/db's TestIntegration_StockBalanceDrift_IsZero, read it as a real
// breach. That is the residual order-dependent class DIB-396 left open: nothing is wrong
// with the code, the two sides simply overlap in time.
//
// The lease fixes it WITHOUT weakening either gate. It is a Postgres advisory lock, so it
// is held per session and spans processes:
//
//	dib396LeaseShared   , taken by a fixture that transiently drifts. Drifters do not
//	                       conflict with each other, so the suite keeps its parallelism.
//	dib396LeaseExclusive, taken by a test that ASSERTS a whole-DB invariant. It waits
//	                       until no drifting fixture is in flight.
//
// Postgres queues a pending exclusive request ahead of NEW shared requests, so an
// assertion cannot be starved by a stream of drifters. A PERMANENT drift (the thing these
// gates exist to catch) is unaffected: the lease only delays the read, it never filters
// a row. Both are released in t.Cleanup, and because the lease is taken FIRST its release
// runs LAST (t.Cleanup is LIFO), after the fixture has restored what it touched.
package inventory

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// dib396StockDriftLease fences v_stock_balance_drift / stock_balances whole-table reads.
const dib396StockDriftLease = int64(3960001)

// dib396PaymentConfigLease fences payment_config, which has a one-active-row unique index:
// a test that wipes the table to assert Configure's retire-and-replace behaviour would
// otherwise pull the active row out from under another package's card-rail test.
const dib396PaymentConfigLease = int64(3960002)

// dib396LeaseShared takes the lease in SHARE mode for the rest of the test, "a drifting
// fixture is in flight". Call it BEFORE the first write that breaks the invariant.
func dib396LeaseShared(t *testing.T, pool *pgxpool.Pool, key int64) {
	t.Helper()
	dib396lease(t, pool, key, "pg_advisory_lock_shared", "pg_advisory_unlock_shared")
}

// dib396LeaseExclusive takes the lease in EXCLUSIVE mode for the rest of the test, "hold
// the drifting fixtures, I am reading a whole-DB invariant". Call it before the read.
func dib396LeaseExclusive(t *testing.T, pool *pgxpool.Pool, key int64) {
	t.Helper()
	dib396lease(t, pool, key, "pg_advisory_lock", "pg_advisory_unlock")
}

func dib396lease(t *testing.T, pool *pgxpool.Pool, key int64, lockFn, unlockFn string) {
	t.Helper()
	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("DIB-396 lease: acquire connection: %v", err)
	}
	if _, err := conn.Exec(context.Background(), "SELECT "+lockFn+"($1)", key); err != nil {
		conn.Release()
		t.Fatalf("DIB-396 lease: %s(%d): %v", lockFn, key, err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), "SELECT "+unlockFn+"($1)", key)
		conn.Release()
	})
}
