//go:build integration

// journal_idem_pool_cycle_integration_test.go, the connection-starvation regression for the
// idempotency re-read in InsertJournal. The defect: after the header INSERT hit the idempotency
// key's unique violation, InsertJournal asked the SHARED POOL for a connection to re-read the
// winner's id while its own transaction (aborted, but still holding its connection until the
// deferred Rollback) was open. Pool-size callers in flight at that arm together each hold one
// connection and wait for one more: the sessions sit idle in transaction, no lock is waited on, and
// the process stalls to the action timeout.
//
// The shape, fail first: a pool built at 4 EXPLICITLY (pgx's default is max(4, CPUs)), a blocker transaction
// on a SEPARATE single-connection pool that holds an uncommitted header with the contested key, and
// poolSize callers with that same key. Every caller opens its transaction (the leg polls
// pg_stat_activity until all poolSize are inside one), then the blocker commits and every caller
// reaches the unique-violation arm while holding its connection. The leg's own helpers (the
// blocker, the activity poll, the seed) never touch the scenario pool, so the scenario uses exactly
// poolSize connections.
//
// The URL is carried on the command and names this leg's own throwaway database:
//
//	DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:5613/hh_r11_gl_journal?sslmode=disable' \
//	  go test -tags=integration ./internal/gl/ -run TestInsertJournal_IdempotencyReReadJoinsRelease_NoPoolCycle -count=1 -v -p 1
package gl

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	journalPoolCycleYear = 2087
	journalPoolCycleApp  = "gl-journal-pool-cycle-scenario"
)

func TestInsertJournal_IdempotencyReReadJoinsRelease_NoPoolCycle(t *testing.T) {
	// poolSize is the ONE constant the connection accounting hangs on: the pool is built at it and
	// the caller count derives from it.
	const poolSize = 4

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("journal pool-cycle leg: DATABASE_URL not set (the leg needs its own throwaway database)")
	}
	bg := context.Background()

	// The scenario pool: exactly poolSize connections, tagged so the activity poll can count them.
	cfg, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	cfg.MaxConns = int32(poolSize)
	cfg.MinConns = 0
	cfg.ConnConfig.RuntimeParams["application_name"] = journalPoolCycleApp
	pool, err := pgxpool.NewWithConfig(bg, cfg)
	if err != nil {
		t.Fatalf("build pool: %v", err)
	}
	t.Cleanup(pool.Close)

	// The helper pool (seed, blocker, activity poll): separate, one connection.
	hcfg, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		t.Fatalf("parse DATABASE_URL for the helper pool: %v", err)
	}
	hcfg.MaxConns = 1
	helper, err := pgxpool.NewWithConfig(bg, hcfg)
	if err != nil {
		t.Fatalf("build helper pool: %v", err)
	}
	t.Cleanup(helper.Close)
	if err := helper.Ping(bg); err != nil {
		t.Fatalf("database not reachable: %v", err)
	}

	// Seeding and teardown ride a second helper pool so the blocker can keep the first one's
	// only connection for its transaction.
	seedPool, err := pgxpool.New(bg, dbURL)
	if err != nil {
		t.Fatalf("build seed pool: %v", err)
	}
	t.Cleanup(seedPool.Close)
	ensurePeriods(t, seedPool, journalPoolCycleYear)
	dib418Cleanup(t, seedPool, journalPoolCycleYear)

	key := fmt.Sprintf("journal-pool-cycle-%d", time.Now().UnixNano())

	// The blocker: an uncommitted header with the contested key, so every caller's header INSERT
	// waits on it, and every caller is inside its own transaction before it commits.
	var periodID string
	if err := seedPool.QueryRow(bg, `SELECT id::text FROM fiscal_periods WHERE year = $1 AND month = 6`, journalPoolCycleYear).Scan(&periodID); err != nil {
		t.Fatalf("period: %v", err)
	}
	blocker, err := helper.Begin(bg)
	if err != nil {
		t.Fatalf("blocker begin: %v", err)
	}
	blockerOpen := true
	t.Cleanup(func() {
		if blockerOpen {
			rb, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = blocker.Rollback(rb)
			cancel()
		}
	})
	var winnerID string
	if err := blocker.QueryRow(bg, `
		INSERT INTO gl_journals (fiscal_year, entry_no, period_id, source, memo, status, idempotency_key)
		VALUES ($1, 900001, $2, 'ADJUSTMENT', 'pool cycle winner', 'DRAFT', $3)
		RETURNING id::text`, journalPoolCycleYear, periodID, key).Scan(&winnerID); err != nil {
		t.Fatalf("blocker insert: %v", err)
	}

	repo := NewRepository(pool)
	draft := journalDraft{
		Source:         SourceAdjustment,
		IdempotencyKey: key,
		Memo:           "journal pool cycle",
		Status:         StatusDraft,
		OccurredAt:     time.Date(journalPoolCycleYear, 6, 15, 12, 0, 0, 0, time.UTC),
		Lines: []draftLine{
			{AccountCode: "1000", DebitCents: 100},
			{AccountCode: "1020", CreditCents: 100},
		},
	}

	// One bounded deadline for the whole scenario, so every caller still waiting at the bound is the stall.
	const stallBound = 10 * time.Second
	scenarioCtx, scenarioCancel := context.WithTimeout(bg, stallBound)
	defer scenarioCancel()

	type outcome struct {
		tag      string
		duration time.Duration
		id       string
		created  bool
		err      error
	}
	outCh := make(chan outcome, poolSize)
	var done sync.WaitGroup
	done.Add(poolSize)
	begin := time.Now()
	for i := 1; i <= poolSize; i++ {
		go func(i int) {
			defer done.Done()
			start := time.Now()
			id, created, err := repo.InsertJournal(scenarioCtx, draft)
			outCh <- outcome{fmt.Sprintf("caller-%d", i), time.Since(start), id, created, err}
		}(i)
	}

	// Release the blocker only once all poolSize scenario connections are inside a transaction.
	deadline := time.Now().Add(stallBound)
	for {
		var inTx int
		if err := seedPool.QueryRow(bg, `SELECT count(*) FROM pg_stat_activity
			 WHERE application_name = $1 AND xact_start IS NOT NULL`, journalPoolCycleApp).Scan(&inTx); err != nil {
			t.Fatalf("activity poll: %v", err)
		}
		if inTx >= poolSize {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d callers reached an open transaction within %s", inTx, poolSize, stallBound)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := blocker.Commit(bg); err != nil {
		t.Fatalf("blocker commit: %v", err)
	}
	blockerOpen = false

	done.Wait()
	close(outCh)
	total := time.Since(begin)
	stalled := false
	for o := range outCh {
		if o.err != nil {
			t.Logf("%s: did not complete, stopped at %s: %v", o.tag, o.duration.Round(time.Millisecond), o.err)
			if scenarioCtx.Err() != nil {
				stalled = true
			}
			t.Errorf("%s: InsertJournal returned an error: %v", o.tag, o.err)
			continue
		}
		t.Logf("%s: completed in %s (id %s, created %v)", o.tag, o.duration.Round(time.Millisecond), o.id, o.created)
		if o.created || o.id != winnerID {
			t.Errorf("%s: got id %s created=%v, want the winner %s created=false", o.tag, o.id, o.created, winnerID)
		}
	}
	if stalled {
		t.Errorf("THE STALL REPRODUCED: the %s bound fired with %d callers in flight. Each caller held one pool "+
			"connection in its aborted transaction and asked the pool for a second to re-read the winner, so "+
			"the pool-size callers pinned every connection with no lock waited on anywhere; timings above.",
			stallBound, poolSize)
	}
	t.Logf("scenario finished in %s", total.Round(time.Millisecond))

	var n int
	if err := seedPool.QueryRow(bg, `SELECT count(*) FROM gl_journals WHERE idempotency_key = $1`, key).Scan(&n); err != nil {
		t.Fatalf("count journals: %v", err)
	}
	if n != 1 {
		t.Errorf("%d journals carry the key, want exactly 1", n)
	}
}
