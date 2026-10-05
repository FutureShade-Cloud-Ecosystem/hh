//go:build integration

// qd1i_pool4_integration_test.go, the pool-cycle regression for the quotes sync Create (decision rows
// 786 and 790). The defect: a pushed create whose client chosen id is already taken lands on the
// ON CONFLICT arm INSIDE the sync transaction, and that arm read the existing row back through
// SyncStore.Load, which asks the POOL for a second connection (the row, then the caller's authority).
// A create that holds its transaction and asks the pool for another connection starves at the pool
// the deploy runs: pool-size such creates in flight wedge until each action timeout, and Postgres
// sees nothing.
//
// The shape: pool_max_conns is set ON THE COMMAND, in this leg's throwaway database URL; N equal to
// that pool size real pushes through the real engine and the real SyncStore, each with the same taken
// id. The barrier is a real lock: a side transaction on its own pool holds the quotes scope's
// sequence row, so every create opens its transaction (holding its pool connection) and queues on the
// sequence allocation; the leg waits until every one is observed queued in pg_stat_activity, then
// releases them together. Red: the first create to run asks for a second connection and the shared
// bound fires. Green: every push answers inside the bound.
//
// Run (own throwaway container, removed afterwards):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:<port>/<db>?sslmode=disable&pool_max_conns=4' \
//	  go test -tags integration ./internal/quote/ -run TestQD1I_SyncCreateTakenIDDoesNotNeedASecondConnection -count=1 -v -p 1
package quote

import (
	"context"
	"fmt"
	"os"
	gosync "sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// qd1iWaitQueued polls pg_stat_activity (through the side pool) until want sessions are waiting on a
// lock inside a statement matching like, or the bound passes.
func qd1iWaitQueued(t *testing.T, side *pgxpool.Pool, like string, want int, bound time.Duration) {
	t.Helper()
	deadline := time.Now().Add(bound)
	for {
		var n int
		if err := side.QueryRow(context.Background(), `
			SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE $1`, like).Scan(&n); err != nil {
			t.Fatalf("count queued sessions: %v", err)
		}
		if n >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d creates reached the barrier", n, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestQD1I_SyncCreateTakenIDDoesNotNeedASecondConnection(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("pool-cycle leg: DATABASE_URL not set")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	if cfg.MaxConns < 2 {
		t.Skipf("pool-cycle leg: pool_max_conns on the command's DATABASE_URL must be at least 2, got %d", cfg.MaxConns)
	}
	n := int(cfg.MaxConns)

	f := newSyncFixture(t)
	if got := int(f.pool.Config().MaxConns); got != n {
		t.Fatalf("fixture pool is %d, want %d", got, n)
	}
	ctx := context.Background()
	sideCfg := cfg.Copy()
	sideCfg.MaxConns = 2
	side, err := pgxpool.NewWithConfig(ctx, sideCfg)
	if err != nil {
		t.Fatalf("side pool: %v", err)
	}
	t.Cleanup(side.Close)

	// The taken id: one quote already in the org, which also leaves the scope's sequence row in
	// place for the barrier to hold.
	takenID := syncCreate(t, f, map[string]any{
		"customer_id": testkit.TenantAlphaID, "branch_id": testkit.BranchNorthAID,
	})

	hold, err := side.Begin(ctx)
	if err != nil {
		t.Fatalf("barrier begin: %v", err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			_ = hold.Commit(ctx)
		}
	}
	t.Cleanup(release)
	if _, err := hold.Exec(ctx, `UPDATE scope_sequences SET next_value = next_value WHERE scope = 'quotes'`); err != nil {
		t.Fatalf("barrier lock: %v", err)
	}

	const bound = 10 * time.Second
	scenario, cancel := context.WithTimeout(ctx, bound)
	defer cancel()

	type outcome struct {
		status string
		err    string
	}
	outs := make([]outcome, n)
	var wg gosync.WaitGroup
	start := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, err := f.engine.Push(scenario, f.info, f.filter, []apigen.SyncPushOperation{{
				ClientId: fmt.Sprintf("31000000-0000-4000-8000-%012d", 9100+i), Op: "create",
				Payload: map[string]any{"id": takenID, "customer_id": testkit.TenantAlphaID, "branch_id": testkit.BranchNorthAID},
			}})
			if err != nil {
				outs[i].err = err.Error()
				return
			}
			outs[i].status = resp.Results[0].Status
		}(i)
	}
	qd1iWaitQueued(t, side, "%INSERT INTO scope_sequences%", n, 5*time.Second)
	t.Logf("barrier: %d creates each hold a transaction and queue on the sequence row", n)
	release()
	wg.Wait()
	elapsed := time.Since(start)
	if scenario.Err() != nil {
		t.Fatalf("the %d concurrent taken-id creates wedged at pool size %d: the bound (%s) fired after %s, outcomes %+v",
			n, n, bound, elapsed, outs)
	}
	for i, o := range outs {
		if o.err != "" {
			t.Fatalf("create %d: %s", i, o.err)
		}
	}
	t.Logf("pool %d, %d concurrent taken-id creates all answered in %s: %+v", n, n, elapsed, outs)
}
