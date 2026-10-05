//go:build integration

// invcount_post_realseam_integration_test.go: the desk's post path must not
// ask the shared pool for a second connection while its post transaction
// holds the first. The service is wired exactly as wire_operations.go wires
// it: invcount.Service over invcount.Repository, invCountAdapter over
// inventory.Service over inventory.Repository, the real GL seam, all on one
// pool of 4. Four concurrent posts of distinct COUNTED sheets each open a
// post transaction and hold a connection; if any read inside that transaction
// (the weighted-average cost read, or anything under the adjustment write)
// goes to the pool instead of the transaction, every caller asks the pool for
// a fifth connection that never comes (connection starvation by second
// acquisition, no row lock waited on) and the leg fails inside the shared
// deadline.
//
// The overlap is forced, not left to timing: a query tracer on the scenario
// pool holds every caller at its sheet row lock, the first statement of the
// post transaction, until all callers are inside their transactions. The
// tracer sits below the module seams, so the same leg runs unchanged on the
// unfixed and the fixed tree and goes red if the transaction is dropped at any
// layer of the seam chain (service, adapter, inventory service, repository).
//
// Run with:
//
//	DATABASE_URL='postgres://...' go test -tags=integration -count=1 -run TestInvCountPostRealSeamNoSecondAcquisition ./cmd/core/
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

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/invcount"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
)

// postSeamPoolSize is the scenario pool size; the contender count derives from it.
const postSeamPoolSize = 4

// Seeded fixtures (the tree's own seed: the Trenton branch, four stocked and
// costed products, the two seeded staff users).
const (
	postSeamBranch = "5d1b3f6e-0000-4000-9000-000000000001"
	postSeamMaker  = "d7000000-0000-4000-9000-000000000001"
	postSeamCheck  = "d7000000-0000-4000-9000-000000000002"
)

var postSeamProducts = [postSeamPoolSize]string{
	"a7000000-0000-4000-9000-000000000001",
	"a7000000-0000-4000-9000-000000000002",
	"a7000000-0000-4000-9000-000000000003",
	"a7000000-0000-4000-9000-000000000004",
}

// postSeamBarrier is a pgx query tracer: it holds each caller at its sheet row
// lock until every contender has reached it, so all callers are inside their
// post transactions together before any of them reads again.
type postSeamBarrier struct {
	want    int32
	arrived atomic.Int32
}

func (b *postSeamBarrier) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !strings.Contains(data.SQL, "FROM count_sheets WHERE id = $1 FOR UPDATE") {
		return ctx
	}
	b.arrived.Add(1)
	for b.arrived.Load() < b.want {
		select {
		case <-ctx.Done():
			return ctx
		case <-time.After(time.Millisecond):
		}
	}
	return ctx
}

func (b *postSeamBarrier) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestInvCountPostRealSeamNoSecondAcquisition(t *testing.T) {
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

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	cfg.MaxConns = int32(postSeamPoolSize)
	barrier := &postSeamBarrier{want: int32(postSeamPoolSize)}
	cfg.ConnConfig.Tracer = barrier
	scenario, err := pgxpool.NewWithConfig(bg, cfg)
	if err != nil {
		t.Fatalf("scenario pool: %v", err)
	}
	t.Cleanup(scenario.Close)

	t.Cleanup(func() {
		_, _ = helper.Exec(context.Background(), `DELETE FROM count_sheets WHERE notes LIKE 'FXPOSTSEAM%'`)
	})

	sheetIDs := make([]string, postSeamPoolSize)
	for i := range sheetIDs {
		if err := helper.QueryRow(bg, `
			INSERT INTO count_sheets (sheet_no, branch_id, scope, status, counted_by, approved_by, approved_at, notes)
			VALUES ('CS-FXPOST-' || substr(gen_random_uuid()::text,1,8), $1, 'AD_HOC', 'COUNTED', $2, $3, now(), $4)
			RETURNING id::text`,
			postSeamBranch, postSeamMaker, postSeamCheck, fmt.Sprintf("FXPOSTSEAM %d", i)).Scan(&sheetIDs[i]); err != nil {
			t.Fatalf("open sheet %d: %v", i, err)
		}
		if _, err := helper.Exec(bg, `
			INSERT INTO count_lines (sheet_id, product_id, location_id, snapshot_qty_base, counted_qty_base, variance_base, reason_code)
			VALUES ($1, $2, $3, 100, 99, -1, 'DAMAGE')`,
			sheetIDs[i], postSeamProducts[i], postSeamBranch); err != nil {
			t.Fatalf("add line %d: %v", i, err)
		}
	}

	// The production wiring, on the scenario pool.
	glSvc := gl.NewService(gl.NewRepository(scenario))
	inventorySvc := inventory.NewService(inventory.NewRepository(scenario))
	svc := invcount.NewService(invcount.NewRepository(scenario)).
		WithStockCounter(invCountAdapter{svc: inventorySvc}).
		WithGLPoster(invcountGLPoster{svc: glSvc}).
		WithRuleResolver(glRuleAdapter{svc: glSvc})
	caller := identity.ContextWithStaff(bg, identity.StaffContext{
		UserID: postSeamCheck, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager, Scope: identity.ScopeBranch,
		Status: identity.StatusActive, Branches: map[string]bool{postSeamBranch: true},
	})

	ctx, cancel := context.WithTimeout(caller, 10*time.Second)
	defer cancel()

	type result struct {
		err error
		dur time.Duration
	}
	results := make(chan result, len(sheetIDs))
	for _, id := range sheetIDs {
		go func() {
			start := time.Now()
			err := svc.PostSheet(ctx, id)
			results <- result{err: err, dur: time.Since(start)}
		}()
	}

	var stalled bool
	var lines []string
	for range sheetIDs {
		r := <-results
		switch {
		case r.err == nil:
			lines = append(lines, fmt.Sprintf("ok after %v", r.dur))
		case errors.Is(r.err, context.DeadlineExceeded) || ctx.Err() != nil:
			stalled = true
			lines = append(lines, fmt.Sprintf("stalled, gave up after %v: %v", r.dur, r.err))
		default:
			lines = append(lines, fmt.Sprintf("failed after %v: %v", r.dur, r.err))
			t.Errorf("post failed for a reason other than a stall: %v", r.err)
		}
	}
	if stalled {
		t.Errorf("STALL: %d concurrent sheet posts on a pool of %d through the real service, adapter, inventory service and repository did not finish inside the shared deadline, a read inside the post transaction asks the pool for a second connection while the transaction holds the first\n  %s",
			postSeamPoolSize, postSeamPoolSize, strings.Join(lines, "\n  "))
	}
	t.Logf("per-caller timings:\n  %s", strings.Join(lines, "\n  "))

	if !stalled && !t.Failed() {
		for i, id := range sheetIDs {
			var status string
			var moveID *string
			if err := helper.QueryRow(bg, `
				SELECT s.status, l.move_id::text FROM count_sheets s JOIN count_lines l ON l.sheet_id = s.id WHERE s.id = $1`, id).Scan(&status, &moveID); err != nil {
				t.Fatalf("read back sheet %d: %v", i, err)
			}
			if status != "POSTED" || moveID == nil {
				t.Errorf("sheet %d: status %q, move %v, want POSTED with a COUNT move", i, status, moveID)
			}
		}
	}
}
