//go:build integration

// syncscope_pickups_sigcapture_tx_atomicity_integration_test.go, the all-or-nothing regression for
// the pickups sync Write's SIGNATURE CAPTURE.
//
// The defect beside the starvation: the capture's two writes (the pos_signature row and the
// signature audit row) go through the SHARED pool and COMMIT IMMEDIATELY, while the confirm itself
// commits later, on its own transaction. A confirm that fails after the capture has run therefore
// rolls back its pickup change and leaves the signature row and the audit row behind: orphans that
// look like evidence of a collection that did not happen.
//
// The leg forces the post-capture failure through production code only. The caller-supplied
// idempotency key reaches exactly one statement, the pickup UPDATE, which casts it to uuid; a key
// that is not a uuid makes that statement fail AFTER the capture has written. The leg then asserts
// the confirm refused and NO signature row and NO audit row remain, and that the pickup is still
// ACTIVE. On the unfixed tree both rows were already committed through the pool, so the counts are
// one and the leg is red for the orphan rows. On the fixed tree both rows ride the caller's
// transaction and the rollback removes them. The success arm runs the same confirm with a real key
// and asserts exactly one signature row and exactly one audit row. The capturer is the real pos
// provider wired as the composition root wires it (the production adapter lives in package main, a
// test cannot import it). The leg runs against whatever DATABASE_URL it is given and skips without
// one; it pins no port and no database name.
package dispatch

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	hhsync "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestPickupsSyncWrite_FailedConfirmLeavesNoSignatureOrAuditRows(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("pickups signature tx atomicity leg: DATABASE_URL not set (the leg needs its own throwaway database and skips politely without it)")
	}
	const poolSize = 4
	ctx := context.Background()

	// The scenario pool mirrors the deploy's pool size; the leg itself is single-caller, this arm
	// is about commit boundaries, not about starvation.
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
	helper, err := pgxpool.NewWithConfig(ctx, helperCfg)
	if err != nil {
		t.Fatalf("build helper pool: %v", err)
	}
	t.Cleanup(helper.Close)
	if err := helper.Ping(ctx); err != nil {
		t.Fatalf("database not reachable: %v", err)
	}

	if err := testkit.Build(ctx, testkit.PgxPool(helper)); err != nil {
		t.Fatalf("fixture build: %v", err)
	}
	t.Cleanup(func() { _ = testkit.Cleanup(context.Background(), testkit.PgxPool(helper)) })

	// Two pickups on two will-call orders: one for the failed confirm, one for the success arm.
	seed := func(n int, orderNo string) string {
		var orderID, pickupID string
		if err := helper.QueryRow(ctx, `
			INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, requested_date, org_id, tenant_id)
			VALUES ($1, $2, $3, 'CONFIRMED', 'WILL_CALL', CURRENT_DATE, $4::uuid, $5::uuid) RETURNING id::text`,
			orderNo, testkit.TenantAlphaID, testkit.BranchNorthAID,
			testkit.OrgNorthID, testkit.TenantAlphaID).Scan(&orderID); err != nil {
			t.Fatalf("seed will-call order %d: %v", n, err)
		}
		t.Cleanup(func() {
			c := context.Background()
			_, _ = helper.Exec(c, `DELETE FROM audit_log WHERE entity_type = 'signature' AND entity_id IN (SELECT id::text FROM will_call_pickups WHERE order_id = $1::uuid)`, orderID)
			_, _ = helper.Exec(c, `DELETE FROM pos_signature WHERE ref_type = $1 AND ref_id IN (SELECT id::text FROM will_call_pickups WHERE order_id = $2::uuid)`, RefTypeWillCall, orderID)
			_, _ = helper.Exec(c, `DELETE FROM will_call_pickup_events WHERE pickup_id IN (SELECT id FROM will_call_pickups WHERE order_id = $1::uuid)`, orderID)
			_, _ = helper.Exec(c, `DELETE FROM will_call_pickups WHERE order_id = $1::uuid`, orderID)
			_, _ = helper.Exec(c, `DELETE FROM orders WHERE id = $1::uuid`, orderID)
		})
		if err := helper.QueryRow(ctx, `
			INSERT INTO will_call_pickups (order_id, branch_id, token_hash, status, expires_at, seq)
			VALUES ($1::uuid, $2::uuid, $3, 'ACTIVE', now() + interval '1 day', $4) RETURNING id::text`,
			orderID, testkit.BranchNorthAID, fmt.Sprintf("sigtx-atomicity-leg-hash-%d", n), n).Scan(&pickupID); err != nil {
			t.Fatalf("seed pickup %d: %v", n, err)
		}
		return pickupID
	}
	failedPickup := seed(1, "#SO-FXATOMIC-SIGTX-1")
	okPickup := seed(2, "#SO-FXATOMIC-SIGTX-2")

	auditSvc := audit.NewService(audit.NewRepository(scenario), nil)
	posSvc := pos.NewService(pos.NewRepository(scenario)).
		WithSignatureProvider(pos.NewTypedNameSignatureProvider(nil)).
		WithAudit(auditSvc)
	store := NewPickupsSyncStore(scenario, sigTxLegOrders{repo: order.NewRepository(scenario)}, sigTxLegCapturer{svc: posSvc}, nil, nil)
	filter := hhsync.ScopeFilter{OrgID: testkit.OrgNorthID, Tenant: true, TenantID: testkit.TenantAlphaID, Actor: "sigtx-atomicity-leg"}

	sigRows := func(pickupID string) int {
		t.Helper()
		var n int
		if err := helper.QueryRow(ctx, `SELECT count(*) FROM pos_signature WHERE ref_type = $1 AND ref_id = $2`, RefTypeWillCall, pickupID).Scan(&n); err != nil {
			t.Fatalf("count signature rows: %v", err)
		}
		return n
	}
	auditRows := func(pickupID string) int {
		t.Helper()
		var n int
		if err := helper.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = $1 AND entity_type = 'signature' AND entity_id = $2`,
			audit.ActSignatureCapture, pickupID).Scan(&n); err != nil {
			t.Fatalf("count audit rows: %v", err)
		}
		return n
	}

	// The failed confirm: the key reaches only the pickup UPDATE, which casts it to uuid, so the
	// failure lands strictly AFTER the capture has written. Both trees refuse the confirm the same
	// way; what must differ is what the refusal leaves behind.
	if _, err := store.Write(ctx, filter, failedPickup, "not-a-uuid", map[string]any{
		"status": "CONFIRMED", "collected_by": "Atomicity Leg", "affirmed": true,
	}); err == nil {
		t.Fatal("a confirm whose pickup statement fails must return an error")
	}
	if got := sigRows(failedPickup); got != 0 {
		t.Fatalf("the rolled back confirm left %d signature row(s) behind: the capture committed through the SHARED pool "+
			"instead of riding the caller's transaction, an orphan that looks like evidence of a collection that did not happen", got)
	}
	if got := auditRows(failedPickup); got != 0 {
		t.Fatalf("the rolled back confirm left %d signature audit row(s) behind: the audit committed through the SHARED pool "+
			"instead of riding the caller's transaction", got)
	}
	var status string
	if err := helper.QueryRow(ctx, `SELECT status FROM will_call_pickups WHERE id = $1::uuid`, failedPickup).Scan(&status); err != nil {
		t.Fatalf("read pickup after failed confirm: %v", err)
	}
	if status != "ACTIVE" {
		t.Fatalf("the failed confirm must leave the pickup ACTIVE, found %s", status)
	}

	// The success arm: the same confirm with a real key leaves exactly one of each, no more.
	if _, err := store.Write(ctx, filter, okPickup, "b0e40000-0000-4000-8000-000000000001", map[string]any{
		"status": "CONFIRMED", "collected_by": "Atomicity Leg", "affirmed": true,
	}); err != nil {
		t.Fatalf("the success confirm must go through: %v", err)
	}
	if got := sigRows(okPickup); got != 1 {
		t.Fatalf("the confirmed pickup must carry exactly one signature row, found %d", got)
	}
	if got := auditRows(okPickup); got != 1 {
		t.Fatalf("the confirmed pickup must carry exactly one signature audit row, found %d", got)
	}
}
