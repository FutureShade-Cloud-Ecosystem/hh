//go:build integration

// syncscope_pickups_write_pool_starve_integration_test.go, the connection-starvation regression for
// the pickups sync Write (the contractor confirm push, happy path).
//
// The defect: Write opens a transaction on the pool and then resolves the tenant's order id set
// through the order seam, whose production adapter reads through the shared POOL. That is a second
// acquisition while the transaction still holds its own connection. The pool the deploy runs is small
// (pgx sets max(4, CPUs) and nothing overrides it), so once pool-size confirms are inside their
// transactions together, each holds one connection and waits for a fifth that never comes. Postgres
// sees idle sessions and no lock wait, so nothing can break it: the process stalls to each action
// timeout, nothing is lost because everything rolls back, and availability is what goes.
//
// The leg forces the overlap instead of hoping for it. The store, the pool (size 4) and the order
// seam (the real order repository over the scenario pool, wired as the composition root wires it) are
// all production code. The only addition is a gate at the seam: every call a caller makes after its
// first (the first is the refusal check made before any transaction exists) waits until pool-size
// callers have all reached such a call, and only then does the real read run. On the unfixed tree
// the second call is made inside the transaction, so all four callers hold a connection when the
// gate opens and the reads starve. On the fixed tree no caller makes a second call, so the gate never
// engages and the confirms finish in milliseconds. The gate counts only calls to the order seam: a
// further ORDER SEAM call added after the transaction opens meets the same gate, but a pool read made
// some other way (a direct query on the pool) is not seen by this leg.
//
// The signature capturer is a stub that answers a fixed reference: the production capturer lives in
// the pos module (outside this leg's grant) and this leg isolates the order seam read. The test's own
// helper connections come from a separate pool so they never take a scenario connection. The leg runs
// against whatever DATABASE_URL it is given and skips without one.
package dispatch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	hhsync "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

type writeStarveCallerKey struct{}

// writeStarveGate is the order seam over the real order repository with the overlap gate described
// in the file header.
type writeStarveGate struct {
	repo    *order.Repository
	want    int
	mu      sync.Mutex
	calls   map[string]int
	arrived int
	open    chan struct{}
}

func newWriteStarveGate(repo *order.Repository, want int) *writeStarveGate {
	return &writeStarveGate{repo: repo, want: want, calls: map[string]int{}, open: make(chan struct{})}
}

func (g *writeStarveGate) TenantBrief(ctx context.Context, tenantID, orderID string) (OrderBrief, bool, error) {
	b, ok, err := g.repo.OrderBriefForTenant(ctx, tenantID, orderID)
	if err != nil || !ok {
		return OrderBrief{}, false, err
	}
	return OrderBrief{ID: b.ID, BranchID: b.BranchID, Fulfillment: b.Fulfillment, Status: b.Status}, true, nil
}

func (g *writeStarveGate) TenantOrderIDs(ctx context.Context, tenantID string) ([]string, error) {
	caller, _ := ctx.Value(writeStarveCallerKey{}).(string)
	g.mu.Lock()
	g.calls[caller]++
	later := g.calls[caller] > 1
	if later {
		g.arrived++
		if g.arrived == g.want {
			close(g.open)
		}
	}
	g.mu.Unlock()
	if later {
		select {
		case <-g.open:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return g.repo.OrderIDsByCustomer(ctx, tenantID)
}

func (g *writeStarveGate) gateOpened() bool {
	select {
	case <-g.open:
		return true
	default:
		return false
	}
}

func (g *writeStarveGate) callsBy(caller string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls[caller]
}

type writeStarveSig struct{}

func (writeStarveSig) CaptureTyped(context.Context, TypedAttestation) (string, error) {
	return "starve-leg-signature-ref", nil
}

func TestPickupsSyncWrite_TenantIDsBeforeTx_NoConnectionStarvation(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("pickups write starvation leg: DATABASE_URL not set (the leg needs its own throwaway database and skips politely without it)")
	}
	const (
		poolSize   = 4
		stallBound = 10 * time.Second
	)
	ctx := context.Background()

	scenarioCfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	scenarioCfg.MaxConns = int32(poolSize)
	scenario, err := pgxpool.NewWithConfig(ctx, scenarioCfg)
	if err != nil {
		t.Fatalf("build scenario pool: %v", err)
	}
	t.Cleanup(scenario.Close)

	helperCfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	helperCfg.MaxConns = 1
	probePool, err := pgxpool.NewWithConfig(ctx, helperCfg)
	if err != nil {
		t.Fatalf("build probe pool: %v", err)
	}
	t.Cleanup(probePool.Close)
	if err := probePool.Ping(ctx); err != nil {
		t.Fatalf("database not reachable: %v", err)
	}

	// The world: the module's own fixture world, then one will-call order with one ACTIVE pickup per
	// caller (one active pickup per order is the schema's rule). Cleanup is LIFO, children first.
	if err := testkit.Build(ctx, testkit.PgxPool(probePool)); err != nil {
		t.Fatalf("fixture build: %v", err)
	}
	t.Cleanup(func() { _ = testkit.Cleanup(context.Background(), testkit.PgxPool(probePool)) })
	pickupIDs := make([]string, poolSize)
	for i := range pickupIDs {
		var orderID string
		if err := probePool.QueryRow(ctx, `
			INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, requested_date, org_id, tenant_id)
			VALUES ($1, $2, $3, 'CONFIRMED', 'WILL_CALL', CURRENT_DATE, $4::uuid, $5::uuid) RETURNING id::text`,
			fmt.Sprintf("#SO-FXSTARVE-PICKUPS-%d", i+1), testkit.TenantAlphaID, testkit.BranchNorthAID,
			testkit.OrgNorthID, testkit.TenantAlphaID).Scan(&orderID); err != nil {
			t.Fatalf("seed will-call order %d: %v", i+1, err)
		}
		t.Cleanup(func() {
			_, _ = probePool.Exec(context.Background(), `DELETE FROM will_call_pickup_events WHERE pickup_id IN (SELECT id FROM will_call_pickups WHERE order_id = $1::uuid)`, orderID)
			_, _ = probePool.Exec(context.Background(), `DELETE FROM will_call_pickups WHERE order_id = $1::uuid`, orderID)
			_, _ = probePool.Exec(context.Background(), `DELETE FROM orders WHERE id = $1::uuid`, orderID)
		})
		if err := probePool.QueryRow(ctx, `
			INSERT INTO will_call_pickups (order_id, branch_id, token_hash, status, expires_at, seq)
			VALUES ($1::uuid, $2::uuid, $3, 'ACTIVE', now() + interval '1 day', 1) RETURNING id::text`,
			orderID, testkit.BranchNorthAID, fmt.Sprintf("starve-leg-hash-%d", i+1)).Scan(&pickupIDs[i]); err != nil {
			t.Fatalf("seed pickup %d: %v", i+1, err)
		}
	}

	gate := newWriteStarveGate(order.NewRepository(scenario), poolSize)
	store := NewPickupsSyncStore(scenario, gate, writeStarveSig{}, nil)
	filter := hhsync.ScopeFilter{OrgID: testkit.OrgNorthID, Tenant: true, TenantID: testkit.TenantAlphaID, Actor: "starve-leg"}

	// One deadline for the whole scenario: on the unfixed tree every caller still waiting at the
	// bound is the stall.
	scenarioCtx, cancel := context.WithTimeout(ctx, stallBound)
	defer cancel()

	type outcome struct {
		tag string
		err error
		dur time.Duration
	}
	outCh := make(chan outcome, poolSize)
	var wg sync.WaitGroup
	started := time.Now()
	for i := 0; i < poolSize; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tag := fmt.Sprintf("caller-%d", i+1)
			cctx := context.WithValue(scenarioCtx, writeStarveCallerKey{}, tag)
			start := time.Now()
			_, err := store.Write(cctx, filter, pickupIDs[i], fmt.Sprintf("a0e30000-0000-4000-8000-0000000002%02d", i+1), map[string]any{
				"status": "CONFIRMED", "collected_by": "Starve Leg", "affirmed": true,
			})
			outCh <- outcome{tag: tag, err: err, dur: time.Since(start)}
		}(i)
	}
	wg.Wait()
	close(outCh)

	stalled := errors.Is(scenarioCtx.Err(), context.DeadlineExceeded)
	var bad []string
	for o := range outCh {
		t.Logf("%s: returned in %s (%d order seam calls): %v", o.tag, o.dur.Round(time.Millisecond), gate.callsBy(o.tag), o.err)
		if o.err != nil {
			bad = append(bad, fmt.Sprintf("%s: %v", o.tag, o.err))
		}
	}
	switch {
	case stalled && gate.gateOpened():
		t.Fatalf("STALL: %d writes held their transactions, every one asked the order seam for a second read, and the shared %s "+
			"bound fired %s after the start. The seam reads through the shared POOL while all %d connections were held "+
			"(connection starvation by second acquisition; Postgres sees no lock wait). Outcomes: %s",
			poolSize, stallBound, time.Since(started).Round(time.Millisecond), poolSize, strings.Join(bad, "; "))
	case stalled:
		t.Fatalf("the bound fired but the gate never opened, so the stall is not the second acquisition (rig or other failure). Outcomes: %s", strings.Join(bad, "; "))
	case len(bad) > 0:
		t.Fatalf("every caller must confirm its pickup, got: %s", strings.Join(bad, "; "))
	}

	var confirmed int
	if err := probePool.QueryRow(ctx, `SELECT count(*) FROM will_call_pickups WHERE id = ANY($1::uuid[]) AND status = 'CONFIRMED'`, pickupIDs).Scan(&confirmed); err != nil {
		t.Fatalf("count confirmed: %v", err)
	}
	if confirmed != poolSize {
		t.Fatalf("expected %d confirmed pickups, found %d", poolSize, confirmed)
	}
	t.Logf("all %d callers confirmed in %s", poolSize, time.Since(started).Round(time.Millisecond))
}
