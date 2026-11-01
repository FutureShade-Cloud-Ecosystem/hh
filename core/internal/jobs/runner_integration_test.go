//go:build integration

// Integration cover for the job runner (migration 0096) against a real migrated Postgres.
// Run with: DATABASE_URL=... go test -tags integration -count=1 ./internal/jobs/
//
// What only a database can prove:
//   - the claim is genuinely single-flight: N goroutines racing one due window produce ONE run;
//   - a claim consumes its window by advancing next_run_at, so the next tick finds nothing;
//   - a run's outcome is durable, job_runs carries the accounting and job_schedules the last status.
//
// FIXTURES RESTORE WHAT THEY TOUCH: each test inserts its OWN uniquely-named schedule and deletes it
// (and its runs) on cleanup. The seeded yard.stale_pick_sweep schedule is never claimed, advanced or
// modified here, so running this suite does not disturb the real nightly cadence.

package jobs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit/legsweep"
	"github.com/jackc/pgx/v5/pgxpool"
)

func jobsPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	fail := func(format string, a ...any) {
		if set {
			t.Fatalf(format, a...)
		}
		t.Skipf(format, a...)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		fail("no database: %v", err)
		return nil
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		fail("database unreachable: %v", err)
		return nil
	}
	t.Cleanup(pool.Close)
	// Row 0.54: the leg leaves the shared database as it found it; the sweep is registered before the
	// restore, so it runs after it.
	legsweep.Register(t, pool)
	legsweep.RestoreSeededRows(t, pool, "job_schedules")
	return pool
}

// dueTestSchedule inserts an INTERVAL schedule that is already due, named uniquely for this test, and
// registers its cleanup. INTERVAL rather than NIGHTLY so the re-arm is a short, deterministic hop.
func dueTestSchedule(t *testing.T, pool *pgxpool.Pool, suffix string) (string, string) {
	t.Helper()
	name := fmt.Sprintf("test.%s.%d", suffix, time.Now().UnixNano())
	var id string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO job_schedules (name, kind, interval_secs, enabled, next_run_at)
		VALUES ($1, 'INTERVAL', 3600, TRUE, now() - interval '1 minute')
		RETURNING id::text`, name).Scan(&id)
	if err != nil {
		t.Fatalf("insert schedule: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM job_runs WHERE job_name = $1`, name)
		_, _ = pool.Exec(c, `DELETE FROM job_schedules WHERE id = $1::uuid`, id)
	})
	return name, id
}

// TestIntegrationOneDueWindowProducesExactlyOneRunUnderContention is an END-TO-END property guard: N
// independent runners over one due window must produce one run. Mutation testing showed it does NOT
// pin the claim's locking (it still passes with SKIP LOCKED removed, because released goroutines
// arrive in order often enough that the first commit moves the window before the second looks). The
// mechanism is pinned by TestIntegrationClaimSkipsAScheduleAnotherTransactionHasLocked below; this one
// stays because the property it asserts is still the one an operator cares about.
func TestIntegrationOneDueWindowProducesExactlyOneRunUnderContention(t *testing.T) {
	pool := jobsPool(t)
	name, id := dueTestSchedule(t, pool, "contention")
	ctx := context.Background()

	var calls int32
	const racers = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		// Each racer is its own Runner over its own Repository, i.e. its own process, as far as the
		// database is concerned.
		r := NewRunner(NewRepository(pool), nil).Register(name, func(context.Context) (Result, error) {
			atomic.AddInt32(&calls, 1)
			return Result{RowsRead: 1}, nil
		})
		wg.Add(1)
		go func(r *Runner) {
			defer wg.Done()
			<-start
			r.RunDue(ctx)
		}(r)
	}
	close(start)
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("%d/%d racers ran the job, the claim is not single-flight", got, racers)
	}

	var runs int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM job_runs WHERE job_name = $1`, name).Scan(&runs); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if runs != 1 {
		t.Fatalf("job_runs rows = %d, want 1", runs)
	}

	// The window was consumed: next_run_at is now in the future.
	var next time.Time
	if err := pool.QueryRow(ctx, `SELECT next_run_at FROM job_schedules WHERE id = $1::uuid`, id).Scan(&next); err != nil {
		t.Fatalf("read next_run_at: %v", err)
	}
	if !next.After(time.Now()) {
		t.Fatalf("next_run_at = %v, want it advanced past now", next)
	}
}

func TestIntegrationRunOutcomeIsDurable(t *testing.T) {
	pool := jobsPool(t)
	name, id := dueTestSchedule(t, pool, "durable")
	ctx := context.Background()

	r := NewRunner(NewRepository(pool), nil).Register(name, func(context.Context) (Result, error) {
		return Result{RowsRead: 42, RowsWritten: 7, Detail: map[string]any{"notified": 7}}, nil
	})
	if n := r.RunDue(ctx); n != 1 {
		t.Fatalf("ran %d jobs, want 1", n)
	}

	var status, detail string
	var read, written int64
	var finished *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT status, rows_read, rows_written, detail::text, finished_at
		FROM job_runs WHERE job_name = $1`, name).Scan(&status, &read, &written, &detail, &finished); err != nil {
		t.Fatalf("read run: %v", err)
	}
	if status != StatusSucceeded || read != 42 || written != 7 || finished == nil {
		t.Fatalf("run row = (%s, %d, %d, %v)", status, read, written, finished)
	}
	if detail != `{"notified": 7}` {
		t.Fatalf("detail = %s", detail)
	}

	var lastStatus string
	if err := pool.QueryRow(ctx, `SELECT last_status FROM job_schedules WHERE id = $1::uuid`, id).Scan(&lastStatus); err != nil {
		t.Fatalf("read schedule: %v", err)
	}
	if lastStatus != StatusSucceeded {
		t.Fatalf("schedule last_status = %q", lastStatus)
	}
}

func TestIntegrationAFailedRunRecordsTheReason(t *testing.T) {
	pool := jobsPool(t)
	name, _ := dueTestSchedule(t, pool, "failure")
	ctx := context.Background()

	r := NewRunner(NewRepository(pool), nil).Register(name, func(context.Context) (Result, error) {
		return Result{}, errors.New("the yard feed was unreachable")
	})
	r.RunDue(ctx)

	var status, msg string
	if err := pool.QueryRow(ctx, `SELECT status, error FROM job_runs WHERE job_name = $1`, name).Scan(&status, &msg); err != nil {
		t.Fatalf("read run: %v", err)
	}
	if status != StatusFailed || msg != "the yard feed was unreachable" {
		t.Fatalf("failed run = (%s, %q)", status, msg)
	}
}

func TestIntegrationTheShippedNightlySweepScheduleExists(t *testing.T) {
	// 0096 seeds the one real job this wave wires. If the row is renamed or dropped, the sweep stops
	// silently, this is the guard that makes that loud.
	pool := jobsPool(t)
	var kind string
	var minute int
	var enabled bool
	err := pool.QueryRow(context.Background(),
		`SELECT kind, nightly_minute, enabled FROM job_schedules WHERE name = 'yard.stale_pick_sweep'`).
		Scan(&kind, &minute, &enabled)
	if err != nil {
		t.Fatalf("the seeded nightly sweep schedule is missing: %v", err)
	}
	if kind != KindNightly || !enabled {
		t.Fatalf("sweep schedule = (%s, enabled=%v)", kind, enabled)
	}
	if minute != 540 {
		t.Fatalf("sweep minute = %d, want 540 (09:00 UTC, before the yard opens)", minute)
	}
}

func TestIntegrationSeedNextRunFillsOnlyMissingWindows(t *testing.T) {
	pool := jobsPool(t)
	ctx := context.Background()
	name := fmt.Sprintf("test.seed.%d", time.Now().UnixNano())
	var id string
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_schedules (name, kind, nightly_minute, enabled, next_run_at)
		VALUES ($1, 'NIGHTLY', 300, TRUE, NULL) RETURNING id::text`, name).Scan(&id); err != nil {
		t.Fatalf("insert: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM job_schedules WHERE id = $1::uuid`, id)
	})

	repo := NewRepository(pool)
	if _, err := repo.SeedNextRun(ctx, time.Now()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var next time.Time
	if err := pool.QueryRow(ctx, `SELECT next_run_at FROM job_schedules WHERE id = $1::uuid`, id).Scan(&next); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !next.After(time.Now()) {
		t.Fatalf("seeded next_run_at = %v, want a future time", next)
	}

	// Seeding again must not move a window that already exists.
	before := next
	if _, err := repo.SeedNextRun(ctx, time.Now()); err != nil {
		t.Fatalf("seed 2: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT next_run_at FROM job_schedules WHERE id = $1::uuid`, id).Scan(&next); err != nil {
		t.Fatalf("read 2: %v", err)
	}
	if !next.Equal(before) {
		t.Fatalf("a second seed moved an existing window: %v → %v", before, next)
	}
}

func TestIntegrationClaimSkipsAScheduleAnotherTransactionHasLocked(t *testing.T) {
	// The DETERMINISTIC single-flight guard. The N-racer test above passes even with the claim's
	// locking removed, because eight goroutines released at once still tend to arrive in order and the
	// first commit moves next_run_at out of range before the second one looks, it proves the
	// end-to-end property, not the mechanism. This proves the mechanism, with no timing luck: a
	// transaction holds the schedule row locked, and ClaimDue must SKIP it (return "nothing due")
	// rather than block on it or claim it.
	pool := jobsPool(t)
	name, id := dueTestSchedule(t, pool, "interleave")
	ctx := context.Background()

	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin holder: %v", err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	var held string
	if err := holder.QueryRow(ctx, `SELECT id::text FROM job_schedules WHERE id = $1::uuid FOR UPDATE`, id).Scan(&held); err != nil {
		t.Fatalf("hold the row: %v", err)
	}

	// A short deadline so a claim that BLOCKS (what happens if SKIP LOCKED is dropped) fails fast and
	// legibly instead of hanging the package until its global timeout.
	claimCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	var ran int32
	r := NewRunner(NewRepository(pool), nil).Register(name, func(context.Context) (Result, error) {
		atomic.AddInt32(&ran, 1)
		return Result{}, nil
	})
	r.RunDue(claimCtx)

	if got := atomic.LoadInt32(&ran); got != 0 {
		t.Fatalf("the job ran %d time(s) while another transaction held its schedule locked", got)
	}
	if claimCtx.Err() != nil {
		t.Fatal("ClaimDue BLOCKED on a locked schedule instead of skipping it, SKIP LOCKED is not in force")
	}

	// Releasing the lock makes it claimable again: the skip is a skip, not a permanent loss.
	if err := holder.Rollback(ctx); err != nil {
		t.Fatalf("release: %v", err)
	}
	r.RunDue(ctx)
	if got := atomic.LoadInt32(&ran); got != 1 {
		t.Fatalf("after the lock was released the job ran %d time(s), want exactly 1", got)
	}
}
