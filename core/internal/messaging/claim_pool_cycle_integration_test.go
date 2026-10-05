//go:build integration

// claim_pool_cycle_integration_test.go, the connection-starvation regression for the lost-race arm of
// ClaimHouseConversation. The defect: the claim opens a transaction, runs its conditional UPDATE on
// it, and on zero rows probes "vanished or already claimed" through the SHARED POOL while the
// transaction still holds its own connection. That is a second acquisition before the first is
// released. Pool-size claimers of one conversation (the losers of a race, exactly the callers that
// reach the zero-row arm) each hold one connection and wait for one more: the sessions sit idle in
// transaction, no lock is waited on, the database's deadlock detector is blind to the client-side
// half, and the process stalls to the action timeout.
//
// The overlap is forced rather than left to scheduling. A blocker transaction on a SEPARATE pool
// claims the conversation and stays open, so every claimer's UPDATE waits on that row inside its own
// open transaction, all poolSize connections held. The leg polls the database until pool-size
// claimers are waiting, then commits the blocker. The waiting UPDATEs wake one after another: the
// first re-checks its predicate, finds the row claimed, gets zero rows and reaches the probe while
// the other claimers still hold their connections. On the unfixed tree that probe asks the shared
// pool for a fifth connection that never comes, so the first claimer never finishes and the queued
// ones never wake. With the probe read through the transaction every claimer returns
// ErrAlreadyClaimed in milliseconds. On the unfixed tree the shared bound fires and the leg fails with
// the stall banner and per-claimer timings.
//
// Connection accounting is exact: the scenario pool is poolSize connections and the claimers use all
// of them. The blocker and the leg's polling and fixture reads ride a separate pool, so they never
// consume a scenario connection. The pool size is set here, from the throwaway database URL carried
// on the command:
//
//	DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:5612/hh_r11_messaging_claim?sslmode=disable' \
//	  go test -tags=integration ./internal/messaging/ -run TestClaimHouseConversation_LostRaceProbeJoinsTx_NoPoolCycle -count=1 -v -p 1
package messaging

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestClaimHouseConversation_LostRaceProbeJoinsTx_NoPoolCycle(t *testing.T) {
	// poolSize is the ONE constant the scenario's connection accounting hangs on: the scenario pool is
	// built at it below and the claimer count is derived from it.
	const poolSize = 4

	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("claim pool-cycle leg: DATABASE_URL not set (the leg needs its own throwaway database and skips politely without it)")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	// 4 is the deploy default the defect stalls at, pinned explicitly rather than left to
	// max(4, CPUs) so the starvation reproduces identically on any machine.
	cfg.MaxConns = int32(poolSize)
	cfg.MinConns = 0
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("build scenario pool: %v", err)
	}
	t.Cleanup(pool.Close)

	// The helper pool: the blocker transaction, the waiter poll and the fixture reads. Separate from
	// the scenario pool so none of it eats one of the poolSize scenario connections.
	helperCfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse DATABASE_URL for the helper pool: %v", err)
	}
	helperCfg.MaxConns = 3
	helper, err := pgxpool.NewWithConfig(context.Background(), helperCfg)
	if err != nil {
		t.Fatalf("build helper pool: %v", err)
	}
	t.Cleanup(helper.Close)
	if err := helper.Ping(context.Background()); err != nil {
		t.Fatalf("database not reachable: %v", err)
	}

	ctx := context.Background()
	repo := NewRepository(pool)
	helperRepo := NewRepository(helper)
	w5Cleanup(t, helper, custQuinte, "", chHouseTrenton)

	convID, err := helperRepo.UpsertHouseConversation(ctx, custQuinte, branchTrenton, chHouseTrenton)
	if err != nil {
		t.Fatalf("fixture conversation: %v", err)
	}
	if _, err := helper.Exec(ctx, `UPDATE msg_house_conversations SET claimed_by = NULL, claimed_at = NULL WHERE id = $1`, convID); err != nil {
		t.Fatalf("fixture reset: %v", err)
	}

	// The bound the red run fails at: seconds, not minutes. The green path completes in
	// milliseconds, so the bound has room to spare on a loaded machine and still caps the stall.
	const stallBound = 10 * time.Second
	claimers := poolSize

	// The blocker claims the conversation and stays open: every claimer's UPDATE waits on the row.
	blocker, err := helper.Begin(ctx)
	if err != nil {
		t.Fatalf("blocker begin: %v", err)
	}
	blockerDone := false
	t.Cleanup(func() {
		if !blockerDone {
			rbCtx, rbCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = blocker.Rollback(rbCtx)
			rbCancel()
		}
	})
	if _, err := blocker.Exec(ctx,
		`UPDATE msg_house_conversations SET claimed_by = $2, claimed_at = now() WHERE id = $1 AND claimed_by IS NULL`,
		convID, uAlexMorgan); err != nil {
		t.Fatalf("blocker claim: %v", err)
	}

	// One bound for the whole scenario, armed when the blocker is released: every claimer still
	// running at the bound is the stall, and none can slip past another's expiry through a freed pool.
	scenarioCtx, scenarioCancel := context.WithCancel(ctx)
	defer scenarioCancel()

	type outcome struct {
		tag      string
		duration time.Duration
		err      error
	}
	outCh := make(chan outcome, claimers)
	var done sync.WaitGroup
	done.Add(claimers)
	starts := make([]time.Time, claimers)
	var startMu sync.Mutex
	for i := 0; i < claimers; i++ {
		go func(i int) {
			defer done.Done()
			startMu.Lock()
			starts[i] = time.Now()
			startMu.Unlock()
			_, err := repo.ClaimHouseConversation(scenarioCtx, convID, uSamRivera)
			outCh <- outcome{fmt.Sprintf("claimer-%d", i+1), time.Since(starts[i]), err}
		}(i)
	}

	// Wait, on the helper pool, until every claimer is inside its transaction with its UPDATE waiting
	// on the blocker's row lock. The poll is bounded so a broken fixture fails instead of hanging.
	waitDeadline := time.Now().Add(stallBound)
	for {
		var waiting int
		if err := helper.QueryRow(ctx, `
			SELECT count(*) FROM pg_stat_activity
			 WHERE datname = current_database()
			   AND wait_event_type = 'Lock'
			   AND query ILIKE '%UPDATE msg_house_conversations%'`).Scan(&waiting); err != nil {
			t.Fatalf("poll waiters: %v", err)
		}
		if waiting >= claimers {
			break
		}
		if time.Now().After(waitDeadline) {
			t.Fatalf("fixture: %d of %d claimers waiting on the blocker at the bound", waiting, claimers)
		}
		time.Sleep(10 * time.Millisecond)
	}

	released := time.Now()
	stopBound := time.AfterFunc(stallBound, scenarioCancel)
	defer stopBound.Stop()
	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("blocker commit: %v", err)
	}
	blockerDone = true

	done.Wait()
	close(outCh)
	boundFired := scenarioCtx.Err() != nil
	var outcomes []outcome
	for o := range outCh {
		outcomes = append(outcomes, o)
		if o.err == nil || errors.Is(o.err, ErrAlreadyClaimed) {
			t.Logf("%s: returned %v after %s", o.tag, o.err, o.duration.Round(time.Millisecond))
		} else {
			t.Logf("%s: stopped at %s: %v", o.tag, o.duration.Round(time.Millisecond), o.err)
		}
	}
	t.Logf("all claimers back %s after the blocker released", time.Since(released).Round(time.Millisecond))

	bad := 0
	for _, o := range outcomes {
		if !errors.Is(o.err, ErrAlreadyClaimed) {
			bad++
		}
	}
	if bad > 0 && boundFired {
		t.Fatalf("THE STALL REPRODUCED: %d of %d claimers did not return ErrAlreadyClaimed within the %s bound. "+
			"Each claimer held one pool connection in its open transaction and asked the shared pool for a "+
			"second to probe the lost race, so the pool-size claimers pinned every connection with no lock "+
			"waited on anywhere; per-claimer timings are logged above.", bad, claimers, stallBound)
	}
	for _, o := range outcomes {
		if !errors.Is(o.err, ErrAlreadyClaimed) {
			t.Errorf("%s: want ErrAlreadyClaimed, got %v", o.tag, o.err)
		}
	}
	if t.Failed() {
		return
	}

	// The outcome claim, checked against the database: the blocker's claim stands and no claimer
	// wrote a claim or an assignment.
	var claimedBy string
	if err := helper.QueryRow(ctx, `SELECT claimed_by::text FROM msg_house_conversations WHERE id = $1`, convID).Scan(&claimedBy); err != nil {
		t.Fatalf("read claim: %v", err)
	}
	if claimedBy != uAlexMorgan {
		t.Errorf("conversation claimed_by = %s, want the blocker's %s", claimedBy, uAlexMorgan)
	}
	var assigned int
	if err := helper.QueryRow(ctx, `SELECT count(*) FROM rep_assignments WHERE customer_id = $1 AND rep_user_id = $2`,
		custQuinte, uSamRivera).Scan(&assigned); err != nil {
		t.Fatalf("count assignments: %v", err)
	}
	if assigned != 0 {
		t.Errorf("a losing claimer wrote %d assignment rows, want 0", assigned)
	}
}
