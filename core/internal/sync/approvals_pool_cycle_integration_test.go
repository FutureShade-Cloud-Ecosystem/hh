//go:build integration

// approvals_pool_cycle_integration_test.go, the pool-cycle leg for the
// approvals scope's Create conflict arm (UX-108 round 4, the sync Create
// conflict arm census).
//
// THE DEFECT CLASS. Create opens a transaction on the pool, inserts with ON
// CONFLICT (id) DO NOTHING RETURNING, and when the client chosen id is taken
// (the insert answers no row) it reads the row back to answer the collision. A
// read-back through the shared pool while the transaction still holds its own
// connection is a second acquisition before the first is released: pool-size
// such creates in flight starve the process. The pool the deploy runs is small
// (pgx sets max(4, CPUs) and nothing overrides it), so once pool-size creates
// are in flight on taken ids, the callers that reach the arm each hold their
// connection and wait for one more, and the rest queue in Postgres on the row
// locks those callers hold (the scope's own seq draw, or the taken row).
// Nothing in the database can break the cycle because it closes through the
// client-side pool: the process stalls to each action timeout, nothing is lost
// because everything rolls back, and availability is what goes.
//
// THE LEG. It forces the overlap instead of hoping for it: a blocker transaction
// on a SEPARATE single-connection pool inserts the card with id X and holds it
// uncommitted. Pool-size creates, all pushing id X over the real store at pool
// size 4, each open a transaction and queue inside it (behind the blocker's row
// or behind the scope's seq draw). Once the probe sees pool-size sessions
// waiting on a lock, the blocker commits and the callers proceed: at least one
// reaches the conflict arm holding its transaction, and the rest may queue on
// that caller's row locks. A read-back through the pool starves them and the
// shared bound fires (a STALL banner); a read-back through the transaction they
// already hold answers ErrRowExists within milliseconds.
//
// The test's own helper connections come from separate pools so they never take
// one of the scenario connections. The leg runs against whatever DATABASE_URL it
// is given and skips without one.
package sync_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	hhsync "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// ux108r4ApprovalRowID is the id every caller collides on, and the card the
// blocker holds uncommitted under it.
const ux108r4ApprovalRowID = "d1080400-0000-4000-8000-000000000012"

// TestUX108R4ApprovalsCreateConflictArm_ReadsThroughTx_NoPoolCycle: pool-size
// creates that collide on one taken id all finish, and none of them starves.
func TestUX108R4ApprovalsCreateConflictArm_ReadsThroughTx_NoPoolCycle(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("approvals pool-cycle leg: DATABASE_URL not set (the leg needs its own throwaway database and skips politely without it)")
	}
	const (
		poolSize   = 4
		stallBound = 15 * time.Second
	)
	ctx := context.Background()

	// The world the callers act in: the kit's org, branches, users and roles.
	f := newGapFixture(t)
	requester, ok := linkedUser(f.pool)(ctx, testkit.SubGeneralMgr)
	if !ok {
		t.Fatal("no linked user for the general manager")
	}

	// The scenario pool: the store under test runs on it, at pool size 4.
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

	// Helpers ride their own single-connection pools: one for the blocker
	// transaction, one for the probe, so none of them eats a scenario
	// connection.
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

	// The blocker holds the taken card uncommitted, so the callers' inserts
	// queue inside their own transactions.
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
	if _, err := blocker.Exec(ctx, `
		INSERT INTO permission_approval_requests (id, requested_by, branch_id, policy_type, detail, status, seq)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'CREDIT_LIMIT_EXCEEDED', 'ux108r4: the taken card', 'PENDING', 900000012)`,
		ux108r4ApprovalRowID, requester, testkit.BranchNorthBID); err != nil {
		t.Fatalf("blocker insert: %v", err)
	}

	// The store under test runs on the scenario pool, so the pool size the leg
	// proves is the store's own.
	store := identity.NewApprovalsSyncStore(scenario, linkedUser(f.pool))
	filter := hhsync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubGeneralMgr}

	// One deadline for the whole scenario: on an unfixed tree every caller
	// still waiting at the bound is the stall.
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
			_, err := store.Create(scenarioCtx, filter, uuid.NewString(), map[string]any{
				"id":          ux108r4ApprovalRowID,
				"branch_id":   testkit.BranchNorthBID,
				"policy_type": "CREDIT_LIMIT_EXCEEDED",
				"detail":      "ux108r4: a colliding filing"})
			outCh <- outcome{tag: fmt.Sprintf("caller-%d", i), err: err, dur: time.Since(start)}
		}(i)
	}

	// Wait until every caller is queued on a lock inside its own transaction.
	// The blocker is not waiting on anything, so the count is the callers
	// alone.
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
