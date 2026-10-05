//go:build integration

// syncscope_pickups_sigcapture_tx_starve_integration_test.go, the connection-starvation regression
// for the pickups sync Write's SIGNATURE CAPTURE (the contractor confirm push, happy path, the real
// pos provider).
//
// The defect: Write opens a transaction on the pool, locks the pickup row FOR UPDATE, and then calls
// the SignatureCapturer seam. The production capturer behind that seam is pos's
// TypedNameSignatureProvider (via pos.Service.CaptureTypedSignature), whose capture does two
// SHARED-POOL writes: the pos_signature row (Repository.InsertSignature on the pool) and the audit
// row (audit.Service.Record on the pool). Those are second acquisitions while the transaction still
// holds its own connection. The pool the deploy runs is small (pgx sets max(4, CPUs) and nothing
// overrides it), so once pool-size confirms are inside their transactions together, each holds one
// connection and waits for a fifth that never comes. Postgres sees idle sessions and no lock wait,
// so nothing can break it: the process stalls to each action timeout, nothing is lost because
// everything rolls back, and availability is what goes.
//
// The leg forces the overlap instead of hoping for it. The store, the pool (size 4), the order seam,
// the audit service and the pos service and its typed-name provider are all production code, wired
// as the composition root wires them. The only additions are (1) a blocker transaction on a separate
// single-connection pool that holds every caller's pickup row FOR UPDATE until all pool-size callers
// are inside their open transactions (each one's connection is held from its Begin regardless of
// where it waits), counted precisely through pg_stat_activity by the scenario pool's application
// name, and (2) a leg-side adapter over pos.Service, because the production adapter lives in package
// main and a test cannot import it. On the unfixed tree the blockers release into a full pool: all
// four captures ask the shared pool while all four connections are held, and the writes stall. On
// the fixed tree the capture rides the caller's transaction, no caller asks for a second connection,
// and the confirms finish in milliseconds. The test's own helper connections come from a separate
// pool so they never take a scenario connection. The leg runs against whatever DATABASE_URL it is
// given and skips without one; it pins no port and no database name.
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

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	hhsync "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// sigTxLegCapturer is the leg-side twin of the composition root's dispatchSignatureAdapter: the
// whole attestation crosses the seam into pos.Service, and pos binds the provider's store and audit
// lazily at this call, exactly as production binds them. The real vault is not wired (the leg
// stores the sealed payload's plaintext form), which changes nothing about where the rows are
// written, and that is the only thing this leg is about.
type sigTxLegCapturer struct{ svc *pos.Service }

func (c sigTxLegCapturer) CaptureTyped(ctx context.Context, in TypedAttestation) (string, error) {
	return c.svc.CaptureTypedSignature(ctx, pos.TypedAttestation{
		Purpose: in.Purpose, RefType: in.RefType, RefID: in.RefID,
		TypedName: in.TypedName, Affirmed: in.Affirmed,
	})
}

// CaptureTypedTx is the leg's reason to exist: the confirm's capture rides the CALLER'S transaction
// through pos.Service.CaptureTypedSignatureTx, the same delegation the production adapter makes.
func (c sigTxLegCapturer) CaptureTypedTx(ctx context.Context, tx pgx.Tx, in TypedAttestation) (string, error) {
	return c.svc.CaptureTypedSignatureTx(ctx, tx, pos.TypedAttestation{
		Purpose: in.Purpose, RefType: in.RefType, RefID: in.RefID,
		TypedName: in.TypedName, Affirmed: in.Affirmed,
	})
}

// sigTxLegOrders is the tenant order seam over the real order repository, the composition root's
// adapter shape, without the gate the earlier pickups leg used to force its own overlap.
type sigTxLegOrders struct{ repo *order.Repository }

func (o sigTxLegOrders) TenantBrief(ctx context.Context, tenantID, orderID string) (OrderBrief, bool, error) {
	b, ok, err := o.repo.OrderBriefForTenant(ctx, tenantID, orderID)
	if err != nil || !ok {
		return OrderBrief{}, false, err
	}
	return OrderBrief{ID: b.ID, BranchID: b.BranchID, Fulfillment: b.Fulfillment, Status: b.Status}, true, nil
}

func (o sigTxLegOrders) TenantOrderIDs(ctx context.Context, tenantID string) ([]string, error) {
	return o.repo.OrderIDsByCustomer(ctx, tenantID)
}

func TestPickupsSyncWrite_SignatureRidesTx_NoConnectionStarvation(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("pickups signature tx starvation leg: DATABASE_URL not set (the leg needs its own throwaway database and skips politely without it)")
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
	// The application name is how the leg counts ITS OWN callers and nothing else: the wait below
	// counts idle-in-transaction sessions carrying this name, so helper pools and unrelated
	// sessions cannot flake the count.
	scenarioCfg.ConnConfig.RuntimeParams["application_name"] = "pickups-sig-tx-scenario"
	scenario, err := pgxpool.NewWithConfig(ctx, scenarioCfg)
	if err != nil {
		t.Fatalf("build scenario pool: %v", err)
	}
	t.Cleanup(scenario.Close)

	helperCfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	// One connection is the blocker transaction, the second is every other helper use (seeding,
	// polling, verification). Two keeps the blocker's transaction off the polling connection.
	helperCfg.MaxConns = 2
	helperCfg.ConnConfig.RuntimeParams["application_name"] = "pickups-sig-tx-helper"
	helper, err := pgxpool.NewWithConfig(ctx, helperCfg)
	if err != nil {
		t.Fatalf("build helper pool: %v", err)
	}
	t.Cleanup(helper.Close)
	if err := helper.Ping(ctx); err != nil {
		t.Fatalf("database not reachable: %v", err)
	}

	// The world: the module's own fixture world, then one will-call order with one ACTIVE pickup per
	// caller (one active pickup per order is the schema's rule). Cleanup is LIFO, children first,
	// signatures and audit rows included so a red run leaves nothing behind.
	if err := testkit.Build(ctx, testkit.PgxPool(helper)); err != nil {
		t.Fatalf("fixture build: %v", err)
	}
	t.Cleanup(func() { _ = testkit.Cleanup(context.Background(), testkit.PgxPool(helper)) })
	pickupIDs := make([]string, poolSize)
	for i := range pickupIDs {
		var orderID string
		if err := helper.QueryRow(ctx, `
			INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, requested_date, org_id, tenant_id)
			VALUES ($1, $2, $3, 'CONFIRMED', 'WILL_CALL', CURRENT_DATE, $4::uuid, $5::uuid) RETURNING id::text`,
			fmt.Sprintf("#SO-FXSTARVE-SIGTX-%d", i+1), testkit.TenantAlphaID, testkit.BranchNorthAID,
			testkit.OrgNorthID, testkit.TenantAlphaID).Scan(&orderID); err != nil {
			t.Fatalf("seed will-call order %d: %v", i+1, err)
		}
		pickupID := fmt.Sprintf("a0e40000-0000-4000-8000-0000000004%02d", i+1)
		t.Cleanup(func() {
			c := context.Background()
			_, _ = helper.Exec(c, `DELETE FROM audit_log WHERE entity_type = 'signature' AND entity_id = $1`, pickupID)
			_, _ = helper.Exec(c, `DELETE FROM pos_signature WHERE ref_type = $1 AND ref_id = $2`, RefTypeWillCall, pickupID)
			_, _ = helper.Exec(c, `DELETE FROM will_call_pickup_events WHERE pickup_id IN (SELECT id FROM will_call_pickups WHERE order_id = $1::uuid)`, orderID)
			_, _ = helper.Exec(c, `DELETE FROM will_call_pickups WHERE order_id = $1::uuid`, orderID)
			_, _ = helper.Exec(c, `DELETE FROM orders WHERE id = $1::uuid`, orderID)
		})
		if err := helper.QueryRow(ctx, `
			INSERT INTO will_call_pickups (id, order_id, branch_id, token_hash, status, expires_at, seq)
			VALUES ($1::uuid, $2::uuid, $3::uuid, $4, 'ACTIVE', now() + interval '1 day', $5)
			RETURNING id::text`,
			pickupID, orderID, testkit.BranchNorthAID, fmt.Sprintf("sigtx-starve-leg-hash-%d", i+1), i+1).Scan(&pickupIDs[i]); err != nil {
			t.Fatalf("seed pickup %d: %v", i+1, err)
		}
	}

	// The production capturer, wired as the composition root wires it: a typed-name provider over
	// the scenario pool's repository and audit service, bound lazily through pos.Service. Its
	// captures before the fix take the SHARED pool twice; after the fix they ride the caller's tx.
	auditSvc := audit.NewService(audit.NewRepository(scenario), nil)
	posSvc := pos.NewService(pos.NewRepository(scenario)).
		WithSignatureProvider(pos.NewTypedNameSignatureProvider(nil)).
		WithAudit(auditSvc)
	store := NewPickupsSyncStore(scenario, sigTxLegOrders{repo: order.NewRepository(scenario)}, sigTxLegCapturer{svc: posSvc}, nil)
	filter := hhsync.ScopeFilter{OrgID: testkit.OrgNorthID, Tenant: true, TenantID: testkit.TenantAlphaID, Actor: "sigtx-starve-leg"}

	// The blocker: one transaction on its own connection holding every pickup row FOR UPDATE, so no
	// caller can pass its row lock until the leg has seen all poolSize callers idle inside their
	// open transactions (each holds its connection from Begin, wherever it is waiting).
	blockerConn, err := helper.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire blocker connection: %v", err)
	}
	defer blockerConn.Release()
	blockerTx, err := blockerConn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin blocker transaction: %v", err)
	}
	for _, id := range pickupIDs {
		if _, err := blockerConn.Exec(ctx, `SELECT id FROM will_call_pickups WHERE id = $1::uuid FOR UPDATE`, id); err != nil {
			t.Fatalf("blocker lock %s: %v", id, err)
		}
	}

	insideTx := func() int {
		var n int
		if err := helper.QueryRow(ctx, `
			SELECT count(*) FROM pg_stat_activity
			WHERE application_name = 'pickups-sig-tx-scenario'
			  AND (state = 'idle in transaction' OR (state = 'active' AND wait_event_type = 'Lock'))`).Scan(&n); err != nil {
			return -1
		}
		return n
	}

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
			start := time.Now()
			_, err := store.Write(scenarioCtx, filter, pickupIDs[i], fmt.Sprintf("a0e30000-0000-4000-8000-0000000003%02d", i+1), map[string]any{
				"status": "CONFIRMED", "collected_by": "Starve Leg", "affirmed": true,
			})
			outCh <- outcome{tag: tag, err: err, dur: time.Since(start)}
		}(i)
	}

	// Wait until every caller is inside its open transaction (counted by the scenario pool's
	// application name: a caller there is either idle between statements in its transaction or
	// blocked on the blocker's row lock inside one, and either way its connection is held), then
	// let them all go at once. If they never gather, say so: a stall then would have some other
	// cause and must not print the banner.
	overlapForced := false
	deadline := time.Now().Add(stallBound)
	for time.Now().Before(deadline) {
		if insideTx() >= poolSize {
			overlapForced = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := blockerTx.Commit(ctx); err != nil {
		t.Fatalf("release blocker: %v", err)
	}
	wg.Wait()
	close(outCh)

	stalled := errors.Is(scenarioCtx.Err(), context.DeadlineExceeded)
	var bad []string
	for o := range outCh {
		t.Logf("%s: returned in %s: %v", o.tag, o.dur.Round(time.Millisecond), o.err)
		if o.err != nil {
			bad = append(bad, fmt.Sprintf("%s: %v", o.tag, o.err))
		}
	}
	switch {
	case stalled && overlapForced:
		t.Fatalf("STALL: %d confirms held their transactions while every signature capture asked the SHARED pool "+
			"for a connection (the pos_signature insert and the audit insert), and the shared %s bound fired %s after the "+
			"start. Connection starvation by second acquisition: each transaction held one of %d connections and waited "+
			"for one more that only its own commit could free (Postgres sees no lock wait). Outcomes: %s",
			poolSize, stallBound, time.Since(started).Round(time.Millisecond), poolSize, strings.Join(bad, "; "))
	case stalled:
		t.Fatalf("the bound fired but the leg never saw all %d callers inside their transactions, so the stall is not the "+
			"second acquisition (rig or other failure). Outcomes: %s", poolSize, strings.Join(bad, "; "))
	case len(bad) > 0:
		t.Fatalf("every caller must confirm its pickup, got: %s", strings.Join(bad, "; "))
	}

	var confirmed int
	if err := helper.QueryRow(ctx, `SELECT count(*) FROM will_call_pickups WHERE id = ANY($1::uuid[]) AND status = 'CONFIRMED'`, pickupIDs).Scan(&confirmed); err != nil {
		t.Fatalf("count confirmed: %v", err)
	}
	if confirmed != poolSize {
		t.Fatalf("expected %d confirmed pickups, found %d", poolSize, confirmed)
	}
	t.Logf("all %d callers confirmed in %s", poolSize, time.Since(started).Round(time.Millisecond))
}
