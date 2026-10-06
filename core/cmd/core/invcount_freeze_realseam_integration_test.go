//go:build integration

// invcount_freeze_realseam_integration_test.go: the sync freeze's snapshot
// read must ride the write transaction through the REAL production seam,
// not only through invcount's own Write. The store is wired exactly as
// wire_operations.go wires it: invCountAdapter over inventory.Service over
// inventory.Repository, all on one pool of 4. Four concurrent freezes on
// distinct open sheets each hold a connection inside Write; a barrier in
// front of the real adapter's tx method holds every caller until all four
// are inside their transactions, then they read together. If the adapter
// ignores the transaction, or the repository reads from the pool instead of
// the transaction, every caller asks the pool for a fifth connection that
// never comes (connection starvation by second acquisition, no row lock
// waited on) and the leg fails inside the shared deadline.
//
// Run with:
//
//	DATABASE_URL='postgres://...' go test -tags=integration -count=1 -run TestSyncFreezeRealSeamNoSecondAcquisition ./cmd/core/
package main

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

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/invcount"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// realSeamPoolSize is the scenario pool size; the contender count derives from it.
const realSeamPoolSize = 4

// realSeamBarrier holds each caller in front of the real adapter's tx method
// until every contender has reached it, then delegates to the real adapter.
type realSeamBarrier struct {
	invCountAdapter
	want    int32
	arrived atomic.Int32
}

func (b *realSeamBarrier) SnapshotOnHandTx(ctx context.Context, tx pgx.Tx, branchID string) ([]invcount.Snapshot, error) {
	b.arrived.Add(1)
	for b.arrived.Load() < b.want {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
	return b.invCountAdapter.SnapshotOnHandTx(ctx, tx, branchID)
}

func TestSyncFreezeRealSeamNoSecondAcquisition(t *testing.T) {
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
		_, _ = helper.Exec(c, `DELETE FROM count_lines WHERE sheet_id IN (SELECT id FROM count_sheets WHERE notes LIKE 'FXREALSEAM%')`)
		_, _ = helper.Exec(c, `DELETE FROM count_sheets WHERE notes LIKE 'FXREALSEAM%'`)
		_, _ = helper.Exec(c, `DELETE FROM scope_sequences WHERE scope = 'cycle_counts'`)
		_ = testkit.Cleanup(c, testkit.PgxPool(helper))
	})

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	cfg.MaxConns = int32(realSeamPoolSize)
	scenario, err := pgxpool.NewWithConfig(bg, cfg)
	if err != nil {
		t.Fatalf("scenario pool: %v", err)
	}
	t.Cleanup(scenario.Close)

	type contender struct{ sheetID, key string }
	cs := make([]contender, realSeamPoolSize)
	for i := range cs {
		if err := helper.QueryRow(bg, `
			INSERT INTO count_sheets (sheet_no, branch_id, scope, notes)
			VALUES ('CS-FXSEAM-' || substr(gen_random_uuid()::text,1,8), $1, 'AD_HOC', $2)
			RETURNING id::text, gen_random_uuid()::text`,
			testkit.BranchNorthAID, fmt.Sprintf("FXREALSEAM %d", i)).Scan(&cs[i].sheetID, &cs[i].key); err != nil {
			t.Fatalf("open sheet %d: %v", i, err)
		}
	}

	// The production wiring, on the scenario pool.
	inventorySvc := inventory.NewService(inventory.NewRepository(scenario))
	stock := &realSeamBarrier{invCountAdapter: invCountAdapter{svc: inventorySvc}, want: int32(realSeamPoolSize)}
	store := invcount.NewSyncStore(scenario, stock, func(ctx context.Context, platformUserID string) (string, bool) { return "", false })
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
		t.Errorf("STALL: %d concurrent sync freezes on a pool of %d through the real adapter, service and repository did not finish inside the shared deadline, the snapshot read asks the pool for a second connection while the write transaction holds the first\n  %s",
			realSeamPoolSize, realSeamPoolSize, strings.Join(lines, "\n  "))
	}
	t.Logf("per-caller timings:\n  %s", strings.Join(lines, "\n  "))
}
