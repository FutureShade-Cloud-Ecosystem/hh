//go:build integration && demo

package main

// demoseed_taskrepair_integration_test.go, the task sync repair proved against a real Postgres:
// it stamps, a second run changes nothing, it continues an existing counter row, and it never
// reuses a number a live allocator holds uncommitted.

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// liveAllocatorSQL is the live allocator's own draw (internal/tasks/repository.go): next_value is
// the last number issued.
const liveAllocatorSQL = `INSERT INTO scope_sequences (scope, next_value) VALUES ($1, 1)
	ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
	RETURNING next_value`

func repairWorld(t *testing.T, ctx context.Context, suffix string) *pgxpool.Pool {
	t.Helper()
	pool := freshMigratedDB(t, suffix)
	seedOnce(t, ctx, pool)
	// The org row the populate provisions on a booted box: the repair waits for it.
	mustExec(t, ctx, pool, `INSERT INTO org (slug, name) VALUES ($1, 'The demo dealer')`, demoOrgSlug)
	return pool
}

func runRepair(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n, err := repairDemoTaskSync(ctx, tx)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("repairDemoTaskSync: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return n
}

func counter(t *testing.T, ctx context.Context, pool *pgxpool.Pool, scope string) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE((SELECT next_value FROM scope_sequences WHERE scope = $1), 0)`, scope).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func demoSeqs(t *testing.T, ctx context.Context, pool *pgxpool.Pool, taskType string) []int64 {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT seq FROM tasks WHERE id = ANY($1::uuid[]) AND task_type = $2 AND seq IS NOT NULL ORDER BY seq`,
		allDemoTaskIDs(), taskType)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var s int64
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func allDemoTaskIDs() []string {
	ids := make([]string, 0, len(demoTasks))
	for _, tk := range demoTasks {
		ids = append(ids, demoID("task:"+tk.Key))
	}
	return ids
}

func countType(taskType string) int {
	n := 0
	for _, tk := range demoTasks {
		if tk.Type == taskType {
			n++
		}
	}
	return n
}

func TestIntegration_TaskSyncRepairStampsOnceAndContinuesTheCounter(t *testing.T) {
	ctx := context.Background()
	pool := repairWorld(t, ctx, "taskrepair_a")
	picks, weights := countType("PICK"), countType("NEED_WEIGHT")

	if got := runRepair(t, ctx, pool); got != picks+weights {
		t.Fatalf("first run repaired %d rows, want %d", got, picks+weights)
	}
	for _, c := range []struct {
		typ, scope string
		n          int
	}{{"PICK", "pick_tasks", picks}, {"NEED_WEIGHT", "weights", weights}} {
		seqs := demoSeqs(t, ctx, pool, c.typ)
		if len(seqs) != c.n {
			t.Fatalf("%s: %d seqs, want %d", c.typ, len(seqs), c.n)
		}
		for i, s := range seqs {
			if s != int64(i+1) {
				t.Fatalf("%s: seqs %v are not 1..%d on a fresh box", c.typ, seqs, c.n)
			}
		}
		if got := counter(t, ctx, pool, c.scope); got != int64(c.n) {
			t.Fatalf("%s counter reads %d, want %d", c.scope, got, c.n)
		}
	}
	var unstamped int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE id = ANY($1::uuid[]) AND org_id IS NULL AND seq IS NOT NULL`, allDemoTaskIDs()).Scan(&unstamped); err != nil || unstamped != 0 {
		t.Fatalf("%d repaired tasks carry no org (err %v)", unstamped, err)
	}

	// A second run matches nothing and moves no counter.
	if got := runRepair(t, ctx, pool); got != 0 {
		t.Fatalf("second run repaired %d rows, want 0", got)
	}
	if got := counter(t, ctx, pool, "pick_tasks"); got != int64(picks) {
		t.Fatalf("second run moved the pick counter to %d", got)
	}

	// An existing counter row: three tasks back to NULL, the counter already past live draws.
	mustExec(t, ctx, pool, `UPDATE tasks SET seq = NULL WHERE id IN (
		SELECT id FROM tasks WHERE id = ANY($1::uuid[]) AND task_type = 'PICK' ORDER BY seq DESC LIMIT 3)`, allDemoTaskIDs())
	mustExec(t, ctx, pool, `UPDATE scope_sequences SET next_value = 40 WHERE scope = 'pick_tasks'`)
	if got := runRepair(t, ctx, pool); got != 3 {
		t.Fatalf("continuity run repaired %d rows, want 3", got)
	}
	var lo, hi int64
	if err := pool.QueryRow(ctx, `SELECT min(seq), max(seq) FROM tasks WHERE id = ANY($1::uuid[]) AND task_type = 'PICK' AND seq > 15`, allDemoTaskIDs()).Scan(&lo, &hi); err != nil {
		t.Fatal(err)
	}
	if lo != 41 || hi != 43 {
		t.Fatalf("continued block is %d..%d, want 41..43", lo, hi)
	}
	if got := counter(t, ctx, pool, "pick_tasks"); got != 43 {
		t.Fatalf("pick counter reads %d, want 43", got)
	}
}

func TestIntegration_TaskSyncRepairNeverReusesAnUncommittedLiveDraw(t *testing.T) {
	ctx := context.Background()
	pool := repairWorld(t, ctx, "taskrepair_b")
	runRepair(t, ctx, pool)
	base := counter(t, ctx, pool, "pick_tasks")
	mustExec(t, ctx, pool, `UPDATE tasks SET seq = NULL WHERE id IN (
		SELECT id FROM tasks WHERE id = ANY($1::uuid[]) AND task_type = 'PICK' ORDER BY seq DESC LIMIT 2)`, allDemoTaskIDs())

	// Connection A: a live allocator draw, held uncommitted.
	live, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var liveSeq int64
	if err := live.QueryRow(ctx, liveAllocatorSQL, "pick_tasks").Scan(&liveSeq); err != nil {
		t.Fatal(err)
	}
	if liveSeq != base+1 {
		t.Fatalf("the live draw read %d, want %d", liveSeq, base+1)
	}

	// Connection B: the repair, which must wait on A's row lock.
	done := make(chan error, 1)
	go func() {
		tx, err := pool.Begin(ctx)
		if err != nil {
			done <- err
			return
		}
		if _, err := repairDemoTaskSync(ctx, tx); err != nil {
			_ = tx.Rollback(ctx)
			done <- err
			return
		}
		done <- tx.Commit(ctx)
	}()
	waited := false
	for i := 0; i < 100 && !waited; i++ {
		select {
		case err := <-done:
			t.Fatalf("the repair finished while the live draw was uncommitted (err %v)", err)
		case <-time.After(50 * time.Millisecond):
		}
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND datname = current_database())`).Scan(&waited); err != nil {
			t.Fatal(err)
		}
	}
	if !waited {
		t.Fatal("the repair never waited on the live allocator's row lock")
	}
	if err := live.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("the repair: %v", err)
	}

	var n, distinct int
	if err := pool.QueryRow(ctx, `SELECT count(*), count(DISTINCT seq) FROM tasks WHERE id = ANY($1::uuid[]) AND task_type = 'PICK' AND seq > $2`,
		allDemoTaskIDs(), base).Scan(&n, &distinct); err != nil {
		t.Fatal(err)
	}
	if n != 2 || distinct != 2 {
		t.Fatalf("repaired block above the old counter: %d rows, %d distinct", n, distinct)
	}
	var clash int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE id = ANY($1::uuid[]) AND task_type = 'PICK' AND seq = $2`, allDemoTaskIDs(), liveSeq).Scan(&clash); err != nil || clash != 0 {
		t.Fatalf("%d repaired task(s) reuse the live draw %d (err %v)", clash, liveSeq, err)
	}
	if got := counter(t, ctx, pool, "pick_tasks"); got != base+3 {
		t.Fatalf("pick counter reads %d, want the true last number %d", got, base+3)
	}
}
