//go:build integration

// precheck_pool_cycle_integration_test.go, the connection-starvation regression for the order money
// precheck. The defect: a manager cancel (UpdateStatusFromChecked) and a guarded line edit
// (UpdateLineGuarded) open a transaction on the order repository's pool and then run the cancel
// predicate INSIDE it. The production invoice reader (billing's Repository.OrderHasFinalizedInvoice)
// read through the POOL, so every in-flight caller held one connection and asked for a SECOND. Pool-size
// callers in flight together pin every connection and each waits for one more: the sessions sit idle in
// transaction (no lock is waited on at that point, the database's deadlock detector is blind to the
// client-side half), and the process stalls to the action timeout.
//
// The leg runs the REAL stack end to end: the real order service and repository, the REAL billing
// repository behind the invoice seam, all on ONE pool built at 4 connections (the deploy default,
// pinned explicitly so the leg measures it on any machine). The overlap is forced, not left to
// scheduling: a blocker transaction on a SEPARATE two-connection helper pool holds the FOR UPDATE row lock of
// every order the callers are about to touch, so each caller opens its transaction (one pool connection
// each) and parks on its order row. Only when the helper pool sees poolSize sessions parked on that lock
// does the blocker commit, and every caller then reaches the precheck with every pool connection taken.
// On the pre-fix tree each precheck needs a fifth connection that never comes and the shared bound fires.
//
// The sale half of the predicate is a permissive stand-in here: the POS reader lives in a module this
// leg's fix does not touch, and a real POS reader would add its own second acquisition (disclosed in the
// lane report), which would mask whether the invoice half is fixed.
//
// The pool size and the database come from the leg's own throwaway database URL carried on the command:
//
//	DATABASE_URL='postgres://...' go test -tags=integration ./internal/order/ -run PrecheckJoinsOrderTx -count=1 -v -p 1
package order

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// precheckLegPools builds the scenario pool (poolSize connections, the ONE constant the contender count
// derives from) and a separate single-connection helper pool for the blocker, the seeding and the
// parked-session probe, so no helper ever eats one of the scenario connections.
func precheckLegPools(t *testing.T, poolSize int) (scenario, helper *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("order precheck pool-cycle leg: DATABASE_URL not set (the leg needs its own throwaway database and skips politely without it)")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	cfg.MaxConns = int32(poolSize)
	cfg.MinConns = 0
	scenario, err = pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("build scenario pool: %v", err)
	}
	t.Cleanup(scenario.Close)

	hcfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse DATABASE_URL for the helper pool: %v", err)
	}
	hcfg.MaxConns = 2 // the blocker's connection plus one for the parked-session probe
	helper, err = pgxpool.NewWithConfig(context.Background(), hcfg)
	if err != nil {
		t.Fatalf("build helper pool: %v", err)
	}
	t.Cleanup(helper.Close)
	if err := helper.Ping(context.Background()); err != nil {
		t.Fatalf("database not reachable: %v", err)
	}
	return scenario, helper
}

// waitParkedOnLock polls the database (over the helper pool) until want sessions are waiting on a lock,
// which is the proof every caller is inside its own transaction before the blocker is released.
func waitParkedOnLock(t *testing.T, helper *pgxpool.Pool, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := helper.QueryRow(context.Background(), `
			SELECT count(*) FROM pg_stat_activity
			 WHERE datname = current_database() AND pid <> pg_backend_pid()
			   AND wait_event_type = 'Lock'`).Scan(&n); err != nil {
			t.Fatalf("probe parked sessions: %v", err)
		}
		if n >= want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("setup failure (not the stall): fewer than %d callers parked on their order rows within 10s", want)
}

type precheckOutcome struct {
	tag      string
	duration time.Duration
	err      error
}

// runPrecheckCycle drives poolSize concurrent callers, each on its own order, through call, with the
// overlap forced as described in the file header.
func runPrecheckCycle(t *testing.T, scenario, helper *pgxpool.Pool, poolSize int, orders []string,
	call func(ctx context.Context, orderID string) error) {
	t.Helper()
	const stallBound = 10 * time.Second
	ctx := context.Background()

	// The blocker holds every order row's lock on its own connection.
	blocker, err := helper.Begin(ctx)
	if err != nil {
		t.Fatalf("blocker begin: %v", err)
	}
	released := false
	defer func() {
		if !released {
			_ = blocker.Rollback(context.Background())
		}
	}()
	for _, o := range orders {
		if _, err := blocker.Exec(ctx, `SELECT 1 FROM orders WHERE id = $1 FOR UPDATE`, o); err != nil {
			t.Fatalf("blocker lock %s: %v", o, err)
		}
	}

	// One deadline for the whole scenario, started when the callers are launched: every caller still
	// waiting at the bound is the stall.
	scenarioCtx, cancel := context.WithTimeout(ctx, stallBound)
	defer cancel()

	var wg sync.WaitGroup
	out := make(chan precheckOutcome, len(orders))
	start := time.Now()
	for i, o := range orders {
		wg.Add(1)
		go func(i int, o string) {
			defer wg.Done()
			err := call(scenarioCtx, o)
			out <- precheckOutcome{fmt.Sprintf("caller-%d", i+1), time.Since(start), err}
		}(i, o)
	}

	waitParkedOnLock(t, helper, poolSize)
	released = true
	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("blocker release: %v", err)
	}
	wg.Wait()
	close(out)

	stalled := 0
	var other []precheckOutcome
	for o := range out {
		switch {
		case o.err == nil:
			t.Logf("%s: completed at %s", o.tag, o.duration.Round(time.Millisecond))
		case errors.Is(o.err, context.DeadlineExceeded):
			stalled++
			t.Logf("%s: did not complete within the bound, stopped at %s: %v", o.tag, o.duration.Round(time.Millisecond), o.err)
		default:
			other = append(other, o)
			t.Logf("%s: failed at %s with a different error: %v", o.tag, o.duration.Round(time.Millisecond), o.err)
		}
	}
	if stalled > 0 {
		t.Fatalf("STALL: %d of %d callers did not complete within the %s bound. Every caller held one pool connection "+
			"inside its order transaction and the money precheck asked the pool for a second, so %d transactions in "+
			"flight pinned every connection with no lock waited on anywhere. Per-caller timings are logged above.",
			stalled, len(orders), stallBound, poolSize)
	}
	for _, o := range other {
		t.Errorf("%s: unexpected failure after %s: %v", o.tag, o.duration.Round(time.Millisecond), o.err)
	}
}

func TestIntegration_OrderPrecheckJoinsOrderTx_NoPoolCycle(t *testing.T) {
	const poolSize = 4
	// build wires the order service exactly as main.go does for the money seam: the real repository, the
	// real audit spine and the REAL billing repository on the same pool.
	build := func(pool *pgxpool.Pool) *Service {
		auditSvc := audit.NewService(audit.NewRepository(pool), nil)
		return NewService(NewRepository(pool)).
			WithAudit(auditSvc).WithAuditTrail(auditSvc).
			WithCancelGuards(billing.NewRepository(pool), permissiveGuards{}).
			WithReorderPricing(intPricer{}).WithReorderInventory(intInv{}).
			WithCustomerType(intCustType{})
	}

	t.Run("manager cancels", func(t *testing.T) {
		scenario, helper := precheckLegPools(t, poolSize)
		orders := make([]string, poolSize)
		for i := range orders {
			_, orders[i] = seedOrder(t, helper, "CONFIRMED")
		}
		svc := build(scenario)
		runPrecheckCycle(t, scenario, helper, poolSize, orders, func(ctx context.Context, o string) error {
			_, err := svc.TransitionManual(identity.ContextWithStaff(ctx, edIntManagerCtx()), o, ActionCancel, "pool cycle leg")
			return err
		})
		if t.Failed() {
			return
		}
		for _, o := range orders {
			if got := liveStatus(t, helper, o); got != "CANCELLED" {
				t.Errorf("order %s status = %q, want CANCELLED", o, got)
			}
		}
	})

	t.Run("guarded line edits", func(t *testing.T) {
		scenario, helper := precheckLegPools(t, poolSize)
		orders := make([]string, poolSize)
		for i := range orders {
			_, orders[i], _, _ = seedEditOrder(t, helper, "CONFIRMED", 0)
		}
		svc := build(scenario)
		runPrecheckCycle(t, scenario, helper, poolSize, orders, func(ctx context.Context, o string) error {
			_, err := svc.EditLineQty(identity.ContextWithStaff(ctx, edIntManagerCtx()), o, 1, "7")
			return err
		})
		if t.Failed() {
			return
		}
		for _, o := range orders {
			if qty, _, _ := liveLine(t, helper, o, 1); qty != "7.0000" {
				t.Errorf("order %s line qty = %s, want 7.0000", o, qty)
			}
		}
	})
}
