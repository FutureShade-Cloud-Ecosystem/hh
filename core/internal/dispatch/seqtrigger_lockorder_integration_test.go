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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

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

// The same run. The sync cancel of a run takes the run row FOR UPDATE, then the stops counter (the
// un-assign's draw), then the runs counter. ReopenRun and MoveRunDate must take the run row first too:
// a writer that took the stops counter first and the run row through its update would hold the counter
// the cancel is about to want while waiting on the row the cancel holds (S2's probe: 40P01, the reopen
// the victim). The order for every dispatch writer is the run row, the stops counter, the runs counter.
//
// The writer runs on a pool of its own whose sessions carry an application name; a trigger on the
// counter table parks exactly those sessions, right after they touch the stops counter, on an advisory
// lock the test holds. So the writer stands holding the stops counter (in the crossing shape) or the run
// row and then the counter (in the ordered shape), the sync cancel of the SAME run starts behind it, and
// the test releases the writer only when the cancel is seen waiting. In the crossing shape the writer
// then wants the row the cancel holds.
func TestIntegration_SeqTrigger_RunFirstWritersAndTheSyncCancelOnTheSameRun(t *testing.T) {
	writers := map[string]func(repo *Repository, runID string) error{
		"ReopenRun": func(repo *Repository, runID string) error {
			return repo.ReopenRun(context.Background(), runID, "same run leg", "", time.Now())
		},
		"MoveRunDate": func(repo *Repository, runID string) error {
			from := syncToday()
			to := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")
			_, _, _, err := repo.MoveRunDate(context.Background(), runID, from, to, Reschedule{
				BranchID: testkit.BranchNorthAID, EntityType: RescheduleEntityRun, EntityID: runID,
				FromDate: from, ToDate: to, Reason: "same run leg"})
			return err
		},
	}
	for name, write := range writers {
		t.Run(name, func(t *testing.T) {
			f := newSyncFixture(t)
			ctx := context.Background()
			for _, q := range []string{
				`CREATE OR REPLACE FUNCTION sf1_park_stops() RETURNS trigger LANGUAGE plpgsql AS $$
				 BEGIN
				   IF NEW.scope = 'stops' AND current_setting('application_name') = 'sf1-park' THEN
				     PERFORM pg_advisory_xact_lock(4242002);
				   END IF;
				   RETURN NULL;
				 END $$`,
				`CREATE TRIGGER sf1_park_stops AFTER UPDATE ON scope_sequences FOR EACH ROW EXECUTE FUNCTION sf1_park_stops()`,
			} {
				if _, err := f.pool.Exec(ctx, q); err != nil {
					t.Fatalf("park trigger: %v", err)
				}
			}
			t.Cleanup(func() {
				c := context.Background()
				_, _ = f.pool.Exec(c, `DROP TRIGGER IF EXISTS sf1_park_stops ON scope_sequences`)
				_, _ = f.pool.Exec(c, `DROP FUNCTION IF EXISTS sf1_park_stops()`)
			})

			run := syncOpenRun(t, f)
			orderID := syncSeedOrder(t, f, testkit.BranchNorthAID)
			key := syncFreshKey()
			syncApplied(t, f, "stops", f.north, "create", key, map[string]any{
				"order_id": orderID, "scheduled_date": syncToday(), "run_id": run.ID,
				"address_line": "Fixture address", "city": "Fixture City",
			}, nil)

			cfg := f.pool.Config().Copy()
			cfg.ConnConfig.RuntimeParams["application_name"] = "sf1-park"
			cfg.MaxConns = 2
			parkPool, err := pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				t.Fatalf("park pool: %v", err)
			}
			defer parkPool.Close()

			hold, err := f.pool.Acquire(ctx)
			if err != nil {
				t.Fatalf("acquire: %v", err)
			}
			defer hold.Release()
			if _, err := hold.Exec(ctx, `SELECT pg_advisory_lock(4242002)`); err != nil {
				t.Fatalf("hold: %v", err)
			}
			released := false
			release := func() {
				if !released {
					released = true
					_, _ = hold.Exec(ctx, `SELECT pg_advisory_unlock(4242002)`)
				}
			}
			defer release()

			errW := make(chan error, 1)
			go func() { errW <- write(NewRepository(parkPool), run.ID) }()
			waitFor(t, f, name+" parked on its stops counter touch",
				`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted`)

			errC := make(chan error, 1)
			go func() {
				info, _ := sync.Lookup("runs")
				res, err := f.engine.Push(ctx, info, f.north, []apigen.SyncPushOperation{
					syncOp("update", syncFreshKey(), map[string]any{"id": run.ID, "status": RunCancelled}, nil)})
				if err == nil && len(res.Results) == 1 && res.Results[0].Status != "applied" {
					err = fmt.Errorf("the sync cancel answered %+v", res.Results[0])
				}
				errC <- err
			}()
			waitFor(t, f, "the sync cancel waiting behind the parked "+name,
				`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND wait_event <> 'advisory'`)

			release()
			for who, ch := range map[string]chan error{name: errW, "sync cancel": errC} {
				select {
				case err := <-ch:
					if err != nil {
						if strings.Contains(err.Error(), "40P01") || strings.Contains(err.Error(), "deadlock") {
							t.Fatalf("%s was killed as a deadlock victim on the same run: %v", who, err)
						}
						// A clean refusal (the cancel meeting a reopened or moved run) is an answer, not a deadlock.
						t.Logf("%s answered: %v", who, err)
					}
				case <-time.After(30 * time.Second):
					t.Fatalf("%s never finished", who)
				}
			}
		})
	}
}
