//go:build integration

// syncscope_pick_hold_doors_integration_test.go, item 9 F review N3: the two
// sync will call doors are driven WHOLE with a held fake gate, so deleting
// the hold call inside either door fails a test (the helper-only legs in
// syncscope_pick_hold_test.go cannot see a removed call).
//
// WHERE THESE RUN. The doors read the org fence and lock the pickup row
// through their concrete pool, so a unit leg cannot drive them. The dispatch
// integration package is recorded red on main (HH-191, excluded from CI's
// integration job), so these legs run where the package does: against a
// migrated and seeded DATABASE_URL, with
//
//	go test -tags integration -count=1 -run 'SyncPickHoldDoor' ./internal/dispatch/
//
// Each door has a clear-gate control that proves the refusal comes from the
// hold and not from some earlier gate.
package dispatch

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	hhsync "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// The sync mint (WillCallSyncStore.Create) refuses a held order and writes no
// tag; the same mint over a clear gate applies.
func TestSyncPickHoldDoor_WillCallCreateRefusesAHeldOrder(t *testing.T) {
	f := newBoardFixture(t)
	ctx := context.Background()
	orderID := f.boardWillCallOrder(t, true)
	token := "FXHOLD-MINT-" + uniqueSuffix() + "-0123456789abcdef"
	fields := map[string]any{"order_id": orderID, "token": token, "ttl_hours": 24.0}

	held := NewWillCallSyncStore(f.pool, testOrders{pool: f.pool}, boardPicks{f: f},
		fakePickHoldGate{held: true, owed: 8800}, linkedSyncUser(f.pool))
	_, err := held.Create(ctx, f.north, boardKey(901), fields)
	if err == nil {
		t.Fatal("a held order was minted a rack tag through the sync door")
	}
	if !errors.Is(err, hhsync.ErrOpRefused) || !strings.Contains(err.Error(), pickHoldWords) {
		t.Fatalf("the refusal must be the sync refusal carrying the hold sentence: %v", err)
	}
	var n int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM will_call_pickups WHERE order_id = $1::uuid`, orderID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a refused mint wrote %d pickup row(s), err=%v", n, err)
	}

	clear := NewWillCallSyncStore(f.pool, testOrders{pool: f.pool}, boardPicks{f: f},
		fakePickHoldGate{}, linkedSyncUser(f.pool))
	if _, err := clear.Create(ctx, f.north, boardKey(902), fields); err != nil {
		t.Fatalf("the control: the same mint over a clear gate must apply: %v", err)
	}
}

// The offline confirm (PickupsSyncStore.Write) refuses a held order inside its
// transaction, leaves the pickup ACTIVE and takes no signature; the same
// confirm over a clear gate applies.
func TestSyncPickHoldDoor_PickupsWriteRefusesAHeldOrder(t *testing.T) {
	h := newHarness(t)
	pool := h.pool
	ctx := context.Background()
	if err := testkit.Build(ctx, testkit.PgxPool(pool)); err != nil {
		t.Fatalf("fixture build: %v", err)
	}
	t.Cleanup(func() { _ = testkit.Cleanup(context.Background(), testkit.PgxPool(pool)) })

	seed := func(orderNo string, seq int) string {
		var orderID, pickupID string
		if err := pool.QueryRow(ctx, `
			INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, requested_date, org_id, tenant_id)
			VALUES ($1, $2, $3, 'CONFIRMED', 'WILL_CALL', CURRENT_DATE, $4::uuid, $5::uuid) RETURNING id::text`,
			orderNo, testkit.TenantAlphaID, testkit.BranchNorthAID,
			testkit.OrgNorthID, testkit.TenantAlphaID).Scan(&orderID); err != nil {
			t.Fatalf("seed order: %v", err)
		}
		t.Cleanup(func() {
			c := context.Background()
			_, _ = pool.Exec(c, `DELETE FROM will_call_pickup_events WHERE pickup_id IN (SELECT id FROM will_call_pickups WHERE order_id = $1::uuid)`, orderID)
			_, _ = pool.Exec(c, `DELETE FROM will_call_pickups WHERE order_id = $1::uuid`, orderID)
			_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1::uuid`, orderID)
		})
		if err := pool.QueryRow(ctx, `
			INSERT INTO will_call_pickups (order_id, branch_id, token_hash, status, expires_at, seq)
			VALUES ($1::uuid, $2::uuid, $3, 'ACTIVE', now() + interval '1 day', $4) RETURNING id::text`,
			orderID, testkit.BranchNorthAID, "pickhold-door-hash-"+orderNo, seq).Scan(&pickupID); err != nil {
			t.Fatalf("seed pickup: %v", err)
		}
		return pickupID
	}
	heldPickup := seed("#SO-FXHOLD-CONF-1", 9101)
	clearPickup := seed("#SO-FXHOLD-CONF-2", 9102)

	signer := &recordingSigner{t: t}
	orders := sigTxLegOrders{repo: order.NewRepository(pool)}
	filter := hhsync.ScopeFilter{OrgID: testkit.OrgNorthID, Tenant: true, TenantID: testkit.TenantAlphaID, Actor: "pickhold-door-leg"}
	fields := map[string]any{"status": "CONFIRMED", "collected_by": "Hold Leg", "affirmed": true}

	held := NewPickupsSyncStore(pool, orders, signer, fakePickHoldGate{held: true, owed: 8800}, nil)
	_, err := held.Write(ctx, filter, heldPickup, "b0e40000-0000-4000-8000-000000000901", fields)
	if err == nil {
		t.Fatal("a held order was confirmed collected through the sync door")
	}
	if !errors.Is(err, hhsync.ErrOpRefused) || !strings.Contains(err.Error(), pickHoldWords) {
		t.Fatalf("the refusal must be the sync refusal carrying the hold sentence: %v", err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM will_call_pickups WHERE id = $1::uuid`, heldPickup).Scan(&status); err != nil || status != "ACTIVE" {
		t.Fatalf("a refused confirm must leave the pickup ACTIVE: %q err=%v", status, err)
	}
	if len(signer.calls) != 0 {
		t.Fatalf("a refused confirm took %d signature(s)", len(signer.calls))
	}

	clear := NewPickupsSyncStore(pool, orders, signer, fakePickHoldGate{}, nil)
	if _, err := clear.Write(ctx, filter, clearPickup, "b0e40000-0000-4000-8000-000000000902", fields); err != nil {
		t.Fatalf("the control: the same confirm over a clear gate must apply: %v", err)
	}
}
