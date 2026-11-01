//go:build integration

// qd1i_approvals_pool4_integration_test.go, the pool-cycle regressions for the approvals sync store
// (decision rows 786 and 790). Two sites asked the pool for a second connection while the sync
// transaction still held the first:
//
//   - Create, on the ON CONFLICT arm (the client chosen id is taken): the read-back went through
//     s.Load on the pool.
//   - writeDecision, on the happy path: s.resolve built an ApprovalRepository on the pool and read
//     the decider's authority between the row lock and the stamp.
//
// pool_max_conns is set ON THE COMMAND, in the database URL of the run; N equal to that pool size real
// pushes through the real engine and the real ApprovalsSyncStore. The barrier is a real lock held by a
// side transaction on its own pool, so every push opens its transaction (holding its pool connection)
// and queues on the lock; the leg waits until every one is observed queued in pg_stat_activity and then
// releases them together. Red: the first push to run asks for another connection and the shared bound
// fires. Green: every push answers inside the bound.
//
// The engine carries the key ledger, as the composition root wires it, so the legs run the production
// shape: each holder keeps one pool connection for its slot lock and the store gets the rest.
//
// Run (own throwaway container, removed afterwards):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:<port>/<db>?sslmode=disable&pool_max_conns=4' \
//	  go test -tags integration ./internal/sync/ -run 'TestQD1I_Approvals' -count=1 -v -p 1
package sync_test

import (
	"context"
	"fmt"
	"os"
	gosync "sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// qd1iPoolSide reads the pool size off the command's URL and opens a side pool of its own for the
// barrier and the queue count.
func qd1iPoolSide(t *testing.T) (int, *pgxpool.Pool) {
	t.Helper()
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
	cfg.MaxConns = 2
	side, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("side pool: %v", err)
	}
	t.Cleanup(side.Close)
	return n, side
}

// qd1iWaitQueued polls pg_stat_activity until want sessions wait on a lock inside a statement
// matching like, or the bound passes.
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
			rows, _ := side.Query(context.Background(), `SELECT state, coalesce(wait_event_type,''), left(query,90) FROM pg_stat_activity WHERE datname = current_database() AND pid <> pg_backend_pid()`)
			for rows.Next() {
				var a, b, c string
				_ = rows.Scan(&a, &b, &c)
				t.Logf("session: %s %s %s", a, b, c)
			}
			rows.Close()
			t.Fatalf("only %d of %d pushes reached the barrier", n, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// qd1iRun pushes one op per caller concurrently, each under the shared bound, behind the barrier the
// hold transaction makes, and fails on a wedge.
func qd1iRun(t *testing.T, f *theGapFixture, side *pgxpool.Pool, hold pgx.Tx, queued string, as sync.ScopeFilter, n int, op func(i int) apigen.SyncPushOperation) {
	t.Helper()
	ctx := context.Background()
	released := false
	release := func() {
		if !released {
			released = true
			_ = hold.Commit(ctx)
		}
	}
	t.Cleanup(release)
	info, _ := sync.Lookup("approvals")

	// The production shape: the composition root wires the key ledger, whose
	// slot lock keeps one pool connection per holder for the whole push, at
	// most half the pool. Only the holders reach the store's transaction and
	// the barrier; the other pushes wait for a holder to finish.
	f.engine.WithKeyLedger(sync.NewPgKeyLedger(f.pool))
	var since time.Time
	if err := f.pool.QueryRow(context.Background(), `SELECT clock_timestamp()`).Scan(&since); err != nil {
		t.Fatalf("read the database clock: %v", err)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM sync_op_hashes WHERE scope = 'approvals' AND created_at >= $1`, since)
	})
	holders := max(1, n/2)

	const bound = 10 * time.Second
	scenario, cancel := context.WithTimeout(ctx, bound)
	defer cancel()

	type outcome struct{ status, err string }
	outs := make([]outcome, n)
	var wg gosync.WaitGroup
	start := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, err := f.engine.Push(scenario, info, as, []apigen.SyncPushOperation{op(i)})
			if err != nil {
				outs[i].err = err.Error()
				return
			}
			outs[i].status = resp.Results[0].Status
		}(i)
	}
	qd1iWaitQueued(t, side, queued, holders, 5*time.Second)
	t.Logf("barrier: %d of %d pushes each hold a lock connection and a transaction and queue on the lock", holders, n)
	release()
	wg.Wait()
	elapsed := time.Since(start)
	if scenario.Err() != nil {
		t.Fatalf("the %d concurrent pushes wedged at pool size %d: the bound (%s) fired after %s, outcomes %+v",
			n, n, bound, elapsed, outs)
	}
	for i, o := range outs {
		if o.err != "" {
			t.Fatalf("push %d: %s", i, o.err)
		}
	}
	t.Logf("pool %d, %d concurrent pushes all answered in %s: %+v", n, n, elapsed, outs)
}

func TestQD1I_ApprovalsCreateTakenIDDoesNotNeedASecondConnection(t *testing.T) {
	n, side := qd1iPoolSide(t)
	f := newGapFixture(t)
	if got := int(f.pool.Config().MaxConns); got != n {
		t.Fatalf("fixture pool is %d, want %d", got, n)
	}
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM permission_approval_requests WHERE detail = 'qd1i pool leg'`)
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM scope_sequences WHERE scope = 'approvals'`)
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM audit_log WHERE entity_type = 'sync.approvals'`)
	})
	info, _ := sync.Lookup("approvals")
	staff := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubInsideSales}
	takenID := uuid.NewString()
	created := gapPush(t, f.engine, info, staff, []apigen.SyncPushOperation{
		gapOp("create", uuid.NewString(), map[string]any{
			"id": takenID, "branch_id": testkit.BranchNorthAID, "policy_type": "CREDIT_LIMIT_EXCEEDED", "detail": "qd1i pool leg",
		}, nil),
	})
	if created[0].Status != "applied" {
		t.Fatalf("filing answered %+v", created[0])
	}

	hold, err := side.Begin(ctx)
	if err != nil {
		t.Fatalf("barrier begin: %v", err)
	}
	if _, err := hold.Exec(ctx, `UPDATE scope_sequences SET next_value = next_value WHERE scope = 'approvals'`); err != nil {
		t.Fatalf("barrier lock: %v", err)
	}
	qd1iRun(t, f, side, hold, "%INSERT INTO scope_sequences%", staff, n, func(i int) apigen.SyncPushOperation {
		return gapOp("create", fmt.Sprintf("31000000-0000-4000-8000-%012d", 9200+i), map[string]any{
			"id": takenID, "branch_id": testkit.BranchNorthAID, "policy_type": "CREDIT_LIMIT_EXCEEDED", "detail": "qd1i pool leg",
		}, nil)
	})
}

func TestQD1I_ApprovalsDecideDoesNotNeedASecondConnection(t *testing.T) {
	n, side := qd1iPoolSide(t)
	f := newGapFixture(t)
	if got := int(f.pool.Config().MaxConns); got != n {
		t.Fatalf("fixture pool is %d, want %d", got, n)
	}
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM permission_approval_requests WHERE detail = 'qd1i pool leg'`)
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM scope_sequences WHERE scope = 'approvals'`)
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM audit_log WHERE entity_type = 'sync.approvals'`)
	})
	info, _ := sync.Lookup("approvals")
	staff := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubInsideSales}
	gm := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubGeneralMgr}
	id := uuid.NewString()
	created := gapPush(t, f.engine, info, staff, []apigen.SyncPushOperation{
		gapOp("create", uuid.NewString(), map[string]any{
			"id": id, "branch_id": testkit.BranchNorthAID, "policy_type": "CREDIT_LIMIT_EXCEEDED", "detail": "qd1i pool leg",
		}, nil),
	})
	if created[0].Status != "applied" {
		t.Fatalf("filing answered %+v", created[0])
	}

	// The barrier is the request row itself: every decision opens its transaction and queues on the
	// row lock the decide path takes first.
	hold, err := side.Begin(ctx)
	if err != nil {
		t.Fatalf("barrier begin: %v", err)
	}
	if _, err := hold.Exec(ctx, `SELECT 1 FROM permission_approval_requests WHERE id = $1::uuid FOR UPDATE`, id); err != nil {
		t.Fatalf("barrier lock: %v", err)
	}
	qd1iRun(t, f, side, hold, "%FOR UPDATE OF a%", gm, n, func(i int) apigen.SyncPushOperation {
		return gapOp("update", fmt.Sprintf("31000000-0000-4000-8000-%012d", 9300+i),
			map[string]any{"id": id, "status": "APPROVED"}, nil)
	})
	var status string
	if err := f.pool.QueryRow(ctx, `SELECT status FROM permission_approval_requests WHERE id = $1::uuid`, id).Scan(&status); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if status != "APPROVED" {
		t.Fatalf("the request ended %s, want APPROVED (the first decision applies)", status)
	}
}
