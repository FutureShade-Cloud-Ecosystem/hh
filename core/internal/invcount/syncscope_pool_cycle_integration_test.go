//go:build integration

// syncscope_pool_cycle_integration_test.go, the pool-cycle regression for the invcount cycle_counts sync Create conflict arm.
// The defect: Create opens a transaction on the pool, inserts with ON CONFLICT (id) DO NOTHING, and
// when the client chosen id is taken (the insert answers no row) it called the public Load, which
// read through the POOL while the transaction still held its own connection. That is a second
// acquisition before the first is released. The pool the deploy runs is small (pgx sets max(4, CPUs)
// and nothing overrides it), so when pool-size creates reach that arm together each holds one
// connection and waits for a fifth that never comes. Postgres sees nothing wrong for the starved
// caller, the process stalls to each action timeout, and nothing is lost because everything rolls
// back; availability is what goes.
//
// The leg forces the overlap instead of hoping for it: a blocker transaction on a SEPARATE
// single-connection pool inserts the row with id X and holds it uncommitted. Pool-size creates, all
// pushing id X over the real store at pool size 4, each open a transaction and queue inside it
// (behind the blocker's row, a parent row lock or the seq draw). Once the probe sees pool-size
// sessions waiting on a lock, the blocker commits, and the callers then proceed to the conflict arm
// holding its transaction. Unfixed they starve and the shared bound fires (a STALL banner); fixed
// they all answer ErrRowExists within milliseconds.
//
// The test's own helper connections come from separate pools so they never take one of the scenario
// connections. The leg runs against whatever DATABASE_URL it is given and skips without one.
package invcount

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	hhsync "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

const sheetCycleRowID = "a0e30000-0000-4000-8000-000000000018"

func TestSheetSyncCreateConflictArm_ReadsThroughTx_NoPoolCycle(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("invcount cycle_counts pool-cycle leg: DATABASE_URL not set (the leg needs its own throwaway database and skips politely without it)")
	}
	const (
		poolSize   = 4
		stallBound = 10 * time.Second
	)
	ctx := context.Background()

	scenarioCfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	scenarioCfg.MaxConns = int32(poolSize)
	scenario, err := pgxpool.NewWithConfig(ctx, scenarioCfg)
	if err != nil {
		t.Fatalf("build scenario pool: %v", err)
	}
	t.Cleanup(scenario.Close)

	// Helpers ride their own single-connection pools: one for the blocker transaction, one for the
	// probe and the fixtures, so none of them eats a scenario connection.
	helperPool := func(name string) *pgxpool.Pool {
		cfg, err := pgxpool.ParseConfig(url)
		if err != nil {
			t.Fatalf("parse DATABASE_URL: %v", err)
		}
		cfg.MaxConns = 1
		p, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatalf("build %s pool: %v", name, err)
		}
		t.Cleanup(p.Close)
		return p
	}
	blockerPool := helperPool("blocker")
	probePool := helperPool("probe")
	if err := probePool.Ping(ctx); err != nil {
		t.Fatalf("database not reachable: %v", err)
	}

	// The world: this leg's parents, built on the probe pool. Cleanup is LIFO, children first, and
	// runs before the pools close.
	if err := testkit.Build(ctx, testkit.PgxPool(probePool)); err != nil {
		t.Fatalf("fixture build: %v", err)
	}
	t.Cleanup(func() { _ = testkit.Cleanup(context.Background(), testkit.PgxPool(probePool)) })
	t.Cleanup(func() {
		_, _ = probePool.Exec(context.Background(), `DELETE FROM count_sheets WHERE id = $1::uuid`, sheetCycleRowID)
	})

	store := NewSyncStore(scenario, nil, nil)
	filter := hhsync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: "pool-cycle-leg"}
	fields := func(int) map[string]any {
		return map[string]any{"id": sheetCycleRowID, "branch_id": testkit.BranchNorthAID, "scope": "FULL"}
	}

	blocker, err := blockerPool.Begin(ctx)
	if err != nil {
		t.Fatalf("blocker begin: %v", err)
	}
	blockerOpen := true
	t.Cleanup(func() {
		if blockerOpen {
			_ = blocker.Rollback(context.Background())
		}
	})
	// A complete row: every column the real Load scans (seq included) is set so the real Load
	// answers it instead of failing its scan.
	if _, err := blocker.Exec(ctx, `
		INSERT INTO count_sheets (id, sheet_no, branch_id, scope, seq)
		VALUES ($1, 'CS-CYCLE-BLOCKER', $2::uuid, 'FULL', 1)`, sheetCycleRowID, testkit.BranchNorthAID); err != nil {
		t.Fatalf("blocker insert: %v", err)
	}

	// One deadline for the whole scenario: on the unfixed tree every caller still waiting at the
	// bound is the stall.
	scenarioCtx, cancel := context.WithTimeout(ctx, stallBound)
	defer cancel()

	type outcome struct {
		tag string
		err error
		dur time.Duration
	}
	outCh := make(chan outcome, poolSize)
	var wg sync.WaitGroup
	for i := 1; i <= poolSize; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			start := time.Now()
			_, err := store.Create(scenarioCtx, filter, fmt.Sprintf("a0e30000-0000-4000-8000-0000000001%02d", i), fields(i))
			outCh <- outcome{tag: fmt.Sprintf("caller-%d", i), err: err, dur: time.Since(start)}
		}(i)
	}

	// Wait until every caller is queued on a lock inside its own transaction. The blocker is not
	// waiting on anything, so the count is the callers alone.
	waiting := 0
	for scenarioCtx.Err() == nil {
		if err := probePool.QueryRow(ctx, `
			SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock' AND pid <> pg_backend_pid()`).Scan(&waiting); err != nil {
			t.Fatalf("probe: %v", err)
		}
		if waiting >= poolSize {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if waiting < poolSize {
		cancel()
		wg.Wait()
		t.Fatalf("the rig did not put %d callers inside their transactions (saw %d waiting on a lock): the overlap is unproven", poolSize, waiting)
	}

	released := time.Now()
	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("blocker commit: %v", err)
	}
	blockerOpen = false

	wg.Wait()
	close(outCh)
	stalled := errors.Is(scenarioCtx.Err(), context.DeadlineExceeded)
	var bad []string
	for o := range outCh {
		t.Logf("%s: returned in %s after the release: %v", o.tag, o.dur.Round(time.Millisecond), o.err)
		if !errors.Is(o.err, hhsync.ErrRowExists) {
			bad = append(bad, fmt.Sprintf("%s: %v", o.tag, o.err))
		}
	}
	if stalled {
		t.Fatalf("STALL: %d creates held their transactions and the shared %s bound fired "+
			"%s after the release. At least one caller reached the conflict arm and asked the shared POOL for a second "+
			"connection while all %d were held; the others may be queued on that caller's row locks. The cycle closes "+
			"through the client-side pool, which the database cannot see (connection starvation by second acquisition). Outcomes: %s",
			poolSize, stallBound, time.Since(released).Round(time.Millisecond), poolSize, strings.Join(bad, "; "))
	}
	if len(bad) > 0 {
		t.Fatalf("every caller must answer ErrRowExists, got: %s", strings.Join(bad, "; "))
	}
	t.Logf("all %d callers answered ErrRowExists %s after the release", poolSize, time.Since(released).Round(time.Millisecond))
}
