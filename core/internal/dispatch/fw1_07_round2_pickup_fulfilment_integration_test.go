//go:build integration

package dispatch

// fw1_07_round2_pickup_fulfilment_integration_test.go, round 2 (S3's P2): the offline pickup's
// confirm runs the order's fulfilment inside the CONFIRM'S OWN TRANSACTION.
//
// A fulfilment AFTER the commit, with its error discarded, could leave a delivered order with no
// stock relief and no COGS: the pickup says collected, the books say the goods never left, and
// nothing retries it. The fulfilment rides the confirm's transaction, so a fulfilment that refuses
// leaves the pickup ACTIVE and the clerk's next attempt is the retry.

import (
	"context"
	"errors"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	hhsync "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"github.com/jackc/pgx/v5"
)

// refusingFulfiller is the order fulfilment seam refusing, the way a relief that cannot move the
// stock refuses.
type refusingFulfiller struct{ err error }

func (f refusingFulfiller) MarkFulfilled(context.Context, string, string) error { return f.err }

func (f refusingFulfiller) MarkFulfilledTx(context.Context, pgx.Tx, string, string) error {
	return f.err
}

// countingFulfiller is the seam as a witness: how many times the order was handed off.
type countingFulfiller struct{ calls int }

func (f *countingFulfiller) MarkFulfilled(context.Context, string, string) error {
	f.calls++
	return nil
}

func (f *countingFulfiller) MarkFulfilledTx(context.Context, pgx.Tx, string, string) error {
	f.calls++
	return nil
}

// The offline confirm and the order's fulfilment are one fate: a fulfilment that refuses leaves
// the pickup ACTIVE (the collection is not recorded), and a fulfilment that accepts confirms it.
func TestFW1_07R2_ThePickupFulfilmentRidesTheConfirmTransaction(t *testing.T) {
	h := newHarness(t)
	pool := h.pool
	ctx := context.Background()
	if err := testkit.Build(ctx, testkit.PgxPool(pool)); err != nil {
		t.Fatalf("fixture build: %v", err)
	}
	t.Cleanup(func() { _ = testkit.Cleanup(context.Background(), testkit.PgxPool(pool)) })

	seed := func(orderNo string, seq int) (orderID, pickupID string) {
		t.Helper()
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
			orderID, testkit.BranchNorthAID, "pickfulfil-door-hash-"+orderNo, seq).Scan(&pickupID); err != nil {
			t.Fatalf("seed pickup: %v", err)
		}
		return orderID, pickupID
	}
	refusedOrder, refusedPickup := seed("#SO-FXPICK-FULFIL-1", 9201)
	acceptedOrder, acceptedPickup := seed("#SO-FXPICK-FULFIL-2", 9202)

	filter := hhsync.ScopeFilter{OrgID: testkit.OrgNorthID, Tenant: true, TenantID: testkit.TenantAlphaID, Actor: "pickup-fulfilment-leg"}
	fields := map[string]any{"status": "CONFIRMED", "collected_by": "Fulfilment Leg", "affirmed": true}
	orders := sigTxLegOrders{repo: order.NewRepository(pool)}

	// The refusal: the order's fulfilment refuses, so the collection is not recorded.
	refused := NewPickupsSyncStore(pool, orders, &recordingSigner{t: t}, fakePickHoldGate{}, nil).
		WithOrderFulfiller(refusingFulfiller{err: errors.New("the order's stock could not be relieved")})
	if _, err := refused.Write(ctx, filter, refusedPickup, "b0e40000-0000-4000-8000-000000000911", fields); err == nil {
		t.Fatal("a refused fulfilment confirmed the pickup collected")
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM will_call_pickups WHERE id = $1::uuid`, refusedPickup).Scan(&status); err != nil || status != "ACTIVE" {
		t.Fatalf("a refused fulfilment must leave the pickup ACTIVE, got %q (err=%v)", status, err)
	}

	// The control: the same confirm over a fulfilment that accepts confirms the pickup and
	// hands the order off.
	counter := &countingFulfiller{}
	accepted := NewPickupsSyncStore(pool, orders, &recordingSigner{t: t}, fakePickHoldGate{}, nil).
		WithOrderFulfiller(counter)
	if _, err := accepted.Write(ctx, filter, acceptedPickup, "b0e40000-0000-4000-8000-000000000912", fields); err != nil {
		t.Fatalf("the control: the same confirm over an accepting fulfilment must apply: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM will_call_pickups WHERE id = $1::uuid`, acceptedPickup).Scan(&status); err != nil || status != "CONFIRMED" {
		t.Fatalf("the accepted confirm must confirm the pickup, got %q (err=%v)", status, err)
	}
	if counter.calls == 0 {
		t.Fatal("the confirm did not hand the order off at all")
	}
	_ = refusedOrder
	_ = acceptedOrder
}
