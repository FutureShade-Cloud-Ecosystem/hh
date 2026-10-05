//go:build integration

// syncscope_freeze_starvation_integration_test.go: the sync freeze must not
// ask the shared pool for a second connection while its write transaction
// holds the first. At pool size N, N concurrent freezes each hold one
// connection inside Write; if the snapshot read then goes to the pool, every
// one of them waits for a connection none can release, and the process stalls
// to the action timeout with Postgres seeing only idle-in-transaction
// sessions (connection starvation by second acquisition, no row lock waited
// on).
//
// The overlap is forced, not left to timing: the stock fake holds each caller
// at the snapshot read until every caller is inside its transaction, then lets
// them read together.
//
// Run with:
//
//	DATABASE_URL='postgres://...' go test -tags=integration -count=1 -run TestSyncFreezeNoSecondAcquisition ./internal/invcount/
package invcount

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// freezeStarvePoolSize is the scenario pool size; the contender count derives from it.
const freezeStarvePoolSize = 4

// freezeStarveStock holds every caller at the snapshot read until all
// contenders are inside their transactions, then reads either through the
// shared scenario pool (the unfixed shape) or through the caller's tx.
type freezeStarveStock struct {
	pool    *pgxpool.Pool
	want    int32
	arrived atomic.Int32
	snaps   []Snapshot
}

func (s *freezeStarveStock) rendezvous(ctx context.Context) error {
	s.arrived.Add(1)
	for s.arrived.Load() < s.want {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
	return nil
}

func (s *freezeStarveStock) SnapshotOnHand(ctx context.Context, branchID string) ([]Snapshot, error) {
	if err := s.rendezvous(ctx); err != nil {
		return nil, err
	}
	var one int
	if err := s.pool.QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil {
		return nil, err
	}
	return s.snaps, nil
}

func (s *freezeStarveStock) WeightedAvgCostCents(ctx context.Context, productID, branchID string) (int64, error) {
	return 500, nil
}

func (s *freezeStarveStock) PostCountAdjustmentTx(ctx context.Context, tx pgx.Tx, productID, locationID, branchID string, qtyDelta float64, unitCostCents int64, reason string) (string, error) {
	return "", nil
}

func TestSyncFreezeNoSecondAcquisition(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	bg := context.Background()

	// Helper pool: setup and cleanup only, never one of the scenario connections.
	helper, err := pgxpool.New(bg, url)
	if err != nil {
		t.Fatalf("helper pool: %v", err)
	}
	t.Cleanup(helper.Close)
	if err := testkit.Build(bg, testkit.PgxPool(helper)); err != nil {
		t.Fatalf("fixture build: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = helper.Exec(c, `DELETE FROM count_lines WHERE sheet_id IN (SELECT id FROM count_sheets WHERE notes LIKE 'FXFREEZESTARVE%')`)
		_, _ = helper.Exec(c, `DELETE FROM count_sheets WHERE notes LIKE 'FXFREEZESTARVE%'`)
		_, _ = helper.Exec(c, `DELETE FROM scope_sequences WHERE scope = 'cycle_counts'`)
		_ = testkit.Cleanup(c, testkit.PgxPool(helper))
	})

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	cfg.MaxConns = int32(freezeStarvePoolSize)
	scenario, err := pgxpool.NewWithConfig(bg, cfg)
	if err != nil {
		t.Fatalf("scenario pool: %v", err)
	}
	t.Cleanup(scenario.Close)

	type contender struct{ sheetID, key string }
	cs := make([]contender, freezeStarvePoolSize)
	for i := range cs {
		if err := helper.QueryRow(bg, `
			INSERT INTO count_sheets (sheet_no, branch_id, scope, notes)
			VALUES ('CS-FXSTARVE-' || substr(gen_random_uuid()::text,1,8), $1, 'AD_HOC', $2)
			RETURNING id::text, gen_random_uuid()::text`,
			testkit.BranchNorthAID, fmt.Sprintf("FXFREEZESTARVE %d", i)).Scan(&cs[i].sheetID, &cs[i].key); err != nil {
			t.Fatalf("open sheet %d: %v", i, err)
		}
	}

	stock := &freezeStarveStock{
		pool: scenario,
		want: int32(freezeStarvePoolSize),
		snaps: []Snapshot{
			{ProductID: testkit.ProductID, LocationID: testkit.BranchNorthAID, OnHandBase: 100, AvgCostCents: 500},
		},
	}
	store := NewSyncStore(scenario, stock, func(ctx context.Context, platformUserID string) (string, bool) { return "", false })
	filter := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubInsideSales}

	ctx, cancel := context.WithTimeout(bg, 10*time.Second)
	defer cancel()

	type result struct {
		err error
		dur time.Duration
	}
	results := make(chan result, len(cs))
	for _, c := range cs {
		go func() {
			start := time.Now()
			_, err := store.Write(ctx, filter, c.sheetID, c.key, map[string]any{"id": c.sheetID, "status": "FROZEN"})
			results <- result{err: err, dur: time.Since(start)}
		}()
	}

	var stalled bool
	var lines []string
	for range cs {
		r := <-results
		switch {
		case r.err == nil:
			lines = append(lines, fmt.Sprintf("ok after %v", r.dur))
		case errors.Is(r.err, context.DeadlineExceeded) || ctx.Err() != nil:
			stalled = true
			lines = append(lines, fmt.Sprintf("stalled, gave up after %v: %v", r.dur, r.err))
		default:
			lines = append(lines, fmt.Sprintf("failed after %v: %v", r.dur, r.err))
			t.Errorf("freeze failed for a reason other than a stall: %v", r.err)
		}
	}
	if stalled {
		t.Errorf("STALL: %d concurrent sync freezes on a pool of %d did not finish inside the shared deadline, the snapshot read asks the pool for a second connection while the write transaction holds the first\n  %s",
			freezeStarvePoolSize, freezeStarvePoolSize, strings.Join(lines, "\n  "))
	}
	t.Logf("per-caller timings:\n  %s", strings.Join(lines, "\n  "))
}
