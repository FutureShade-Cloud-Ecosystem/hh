//go:build integration

// dib419_window_isolation_test.go, DIB-419 regression guard (test-only).
//
// TestIntegration_Recompute_InsertOnlyIdempotentParity used to run its recompute over a
// "2020-01-01".."2030-12-31" window. RecomputeReport scans tax_decision_snapshot by
// created_at across the WHOLE table, so that window swept in every W4-flat-13 snapshot
// internal/pos commits when it runs a counter sale, and `go test ./...` runs one process
// per package IN PARALLEL against the shared integration DB. A POS sale landing between
// the test's two RecomputeReport calls made the second one insert a row, and the
// idempotency assertion ("second recompute inserted %d, want 0") failed on code that was
// working perfectly. Reproduced deterministically before the fix:
//
//	first recompute: scanned=8 inserted=1
//	second recompute inserted 1, want 0 (idempotent)   <-- FAIL
//
// The fix is a SCOPED assertion, not a weaker one: the fixture stamps its own W4 row into
// an isolated far-future year and recomputes only that year, so the scan sees exactly one
// row and the counts became EXACT (1/1, then 1/0/1) rather than the loose `>= 1` the
// whole-DB window had forced.
//
// This guard holds that property. It does what a parallel internal/pos sale does, commit
// a W4-flat-13 snapshot for a different order, at "now", between the two recomputes, and
// asserts the isolated window does not notice. If anyone widens the window back to a
// now-spanning range, this fails.
package tax

import (
	"context"
	"testing"
)

func TestIntegration_DIB419_RecomputeWindow_IgnoresParallelPOSSnapshots(t *testing.T) {
	pool := intPool(t)
	if pool == nil {
		return
	}
	defer pool.Close()
	svc := intSvc(t, pool)
	ctx := context.Background()

	orderID := seededOrderID(t, pool)
	// A DIFFERENT order stands in for the one internal/pos is selling against.
	var foreign string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM orders WHERE id::text <> $1 ORDER BY id LIMIT 1`, orderID).Scan(&foreign); err != nil {
		t.Fatalf("need a second seeded order to stand in for a parallel POS sale: %v", err)
	}

	clear := func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM tax_decision_snapshot WHERE order_id = ANY(ARRAY[$1,$2]::uuid[])`, orderID, foreign); err != nil {
			t.Errorf("DIB-419 guard teardown: %v", err)
		}
	}
	clear()
	defer clear() // defer, not t.Cleanup, the pool is closed by the defer above.

	// The fixture's own settled W4 row, stamped into the isolated year.
	if _, err := pool.Exec(ctx, `
		INSERT INTO tax_decision_snapshot (order_id, tax_reason_code, rule_version, rate_bps, full_hst_cents, net_collected_cents, automated, created_at)
		VALUES ($1::uuid,'STD_ON_HST_13','W4-flat-13',1300,1300,1300,false, TIMESTAMPTZ '`+dib419StampedAt+`')`, orderID); err != nil {
		t.Fatal(err)
	}

	res, err := svc.RecomputeReport(ctx, dib419WindowFrom, dib419WindowTo)
	if err != nil {
		t.Fatal(err)
	}
	if res.Scanned != 1 || res.Inserted != 1 {
		t.Fatalf("isolated window before the parallel sale: scanned=%d inserted=%d, want 1/1", res.Scanned, res.Inserted)
	}

	// <<< a parallel internal/pos counter sale commits its tax snapshot at "now" >>>
	if _, err := pool.Exec(ctx, `
		INSERT INTO tax_decision_snapshot (order_id, tax_reason_code, rule_version, rate_bps, full_hst_cents, net_collected_cents, automated)
		VALUES ($1::uuid,'STD_ON_HST_13','W4-flat-13',1300,1300,1300,false)`, foreign); err != nil {
		t.Fatal(err)
	}

	// The isolated window must not have noticed: same single row, already recomputed.
	res2, err := svc.RecomputeReport(ctx, dib419WindowFrom, dib419WindowTo)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Scanned != 1 || res2.Inserted != 0 || res2.SkippedExists != 1 {
		t.Fatalf("a parallel POS snapshot leaked into the recompute window: scanned=%d inserted=%d skipped=%d, want 1/0/1",
			res2.Scanned, res2.Inserted, res2.SkippedExists)
	}

	// And the foreign row is genuinely still there and genuinely un-recomputed, proving
	// the guard passed because the window EXCLUDED it, not because the insert vanished.
	var foreignW4, foreignM5 int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE rule_version='W4-flat-13'),
		       count(*) FILTER (WHERE rule_version=$2)
		FROM tax_decision_snapshot WHERE order_id=$1::uuid`, foreign, ruleVersionM5).Scan(&foreignW4, &foreignM5); err != nil {
		t.Fatal(err)
	}
	if foreignW4 != 1 || foreignM5 != 0 {
		t.Fatalf("stand-in POS row: w4=%d m5=%d, want 1/0 (it must survive, un-recomputed)", foreignW4, foreignM5)
	}
}
