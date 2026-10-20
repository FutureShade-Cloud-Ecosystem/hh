//go:build integration

// seqtrigger_lockorder_integration_test.go: the sync seq trigger (0422) puts a stops counter take
// inside ReopenRun and MoveRunDate, after the runs counter their first statement now draws. The
// run cancel through sync takes the two counters the other way round (its stop unassign draws
// stops, then the run's own seq draws runs). Two transactions on different runs, one of each
// shape, cross on the counters: a deadlock, like the stock_balances case the plan's review found.
// The fix is the C1 way: the run-first writers take the stops counter, without advancing it,
// before their first statement, so every dispatch writer takes stops before runs.
package dispatch

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

const parkLockKey = 4242001

// parkRunUpdates installs a trigger that parks any delivery_runs UPDATE whose reopen_reason is the
// marker on a transaction advisory lock the test holds, so the transaction stands open after its
// first statement (and the counter that statement drew).
func parkRunUpdates(t *testing.T, f *syncFixture) {
	t.Helper()
	ctx := context.Background()
	for _, q := range []string{
		`CREATE OR REPLACE FUNCTION sf1_park_run() RETURNS trigger LANGUAGE plpgsql AS $$
		 BEGIN
		   IF NEW.reopen_reason = 'SF1-PARK' THEN PERFORM pg_advisory_xact_lock(4242001); END IF;
		   RETURN NULL;
		 END $$`,
		`CREATE TRIGGER sf1_park_run AFTER UPDATE ON delivery_runs FOR EACH ROW EXECUTE FUNCTION sf1_park_run()`,
	} {
		if _, err := f.pool.Exec(ctx, q); err != nil {
			t.Fatalf("park trigger: %v", err)
		}
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = f.pool.Exec(c, `DROP TRIGGER IF EXISTS sf1_park_run ON delivery_runs`)
		_, _ = f.pool.Exec(c, `DROP FUNCTION IF EXISTS sf1_park_run()`)
	})
}

func waitFor(t *testing.T, f *syncFixture, what, q string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		var n int
		if err := f.pool.QueryRow(context.Background(), q).Scan(&n); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if n > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("never saw: %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestIntegration_SeqTrigger_RunFirstWritersTakeStopsBeforeRuns(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	parkRunUpdates(t, f)

	// Two planned runs, each with one READY stop aboard.
	mk := func() (Run, string) {
		run := syncOpenRun(t, f)
		orderID := syncSeedOrder(t, f, testkit.BranchNorthAID)
		key := syncFreshKey()
		syncApplied(t, f, "stops", f.north, "create", key, map[string]any{
			"order_id": orderID, "scheduled_date": syncToday(), "run_id": run.ID,
			"address_line": "Fixture address", "city": "Fixture City",
		}, nil)
		return run, syncRowOf(t, f, "stops", f.north, key).ID
	}
	runA, _ := mk()
	runB, _ := mk()

	// The test holds the park lock on its own connection until both transactions stand.
	hold, err := f.pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer hold.Release()
	if _, err := hold.Exec(ctx, `SELECT pg_advisory_lock($1)`, int64(parkLockKey)); err != nil {
		t.Fatalf("hold: %v", err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			_, _ = hold.Exec(ctx, `SELECT pg_advisory_unlock($1)`, int64(parkLockKey))
		}
	}
	defer release()

	repo := NewRepository(f.pool)
	errA := make(chan error, 1)
	go func() {
		// Shape A: run first. The reopen marks the run, then its stops.
		errA <- repo.ReopenRun(ctx, runA.ID, "SF1-PARK", "", time.Now())
	}()
	waitFor(t, f, "the reopen parked after its first statement",
		`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted`)

	errB := make(chan error, 1)
	go func() {
		// Shape B: stops first. The sync cancel of a run un-assigns its READY stops (stops
		// counter), then draws the run's own seq (runs counter).
		info, _ := sync.Lookup("runs")
		_, err := f.engine.Push(ctx, info, f.north, []apigen.SyncPushOperation{
			syncOp("update", syncFreshKey(), map[string]any{"id": runB.ID, "status": RunCancelled}, nil),
		})
		errB <- err
	}()
	// B must stand blocked on a counter, whichever one: the runs counter A holds (the crossing
	// shape), or the stops counter A holds (the ordered shape).
	waitFor(t, f, "the sync cancel waiting on a counter",
		`SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND query LIKE '%scope_sequences%' AND query NOT LIKE '%pg_stat_activity%'`)

	release()
	for name, ch := range map[string]chan error{"reopen": errA, "sync cancel": errB} {
		select {
		case err := <-ch:
			if err != nil {
				if strings.Contains(err.Error(), "40P01") || strings.Contains(err.Error(), "deadlock") {
					t.Fatalf("%s was killed as a deadlock victim: the dispatch writers cross on the stops and runs counters: %v", name, err)
				}
				t.Fatalf("%s failed: %v", name, err)
			}
		case <-time.After(30 * time.Second):
			t.Fatalf("%s never finished", name)
		}
	}
}
