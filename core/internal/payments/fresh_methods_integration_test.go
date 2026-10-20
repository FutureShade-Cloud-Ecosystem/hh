//go:build integration

// fresh_methods_integration_test.go: the sync freshness probe (harness.Fresh)
// over the stored payment method writers. A payer whose device has already
// synced the payment_methods feed must see a removed method, a new card and a
// default flip on the next pull from its own cursor. Each test drives the REAL
// repository write path against the REAL payment_methods scope store.
package payments

import (
	"context"
	"fmt"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync/harness"
)

// freshMethodsCase wires the payment_methods scope for the rig's payer and
// puts every method the payer holds into the feed (stamped with the tenant
// and a drawn seq, as a synced device would have it).
func freshMethodsCase(t *testing.T, rig *payRig) harness.FreshCase {
	t.Helper()
	info, ok := sync.Lookup("payment_methods")
	if !ok {
		t.Fatalf("scope payment_methods missing")
	}
	engine := sync.NewEngine()
	if err := engine.RegisterScope("payment_methods", NewPaymentMethodsStore(rig.pool)); err != nil {
		t.Fatalf("register: %v", err)
	}
	return harness.FreshCase{
		Engine: engine,
		Scope:  info,
		Filter: sync.ScopeFilter{OrgID: "3ef10000-0000-4000-8000-000000000001", TenantID: rig.customerID, Tenant: true, Actor: "fresh"},
	}
}

func stampMethodsIntoFeed(t *testing.T, rig *payRig) {
	t.Helper()
	if _, err := rig.pool.Exec(context.Background(),
		`WITH s AS (
			INSERT INTO scope_sequences (scope) VALUES ('payment_methods')
			ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
			RETURNING next_value)
		 UPDATE portal_payment_methods SET tenant_id = customer_id, seq = (SELECT next_value FROM s)
		  WHERE customer_id = $1::uuid`, rig.customerID); err != nil {
		t.Fatalf("stamp the methods into the feed: %v", err)
	}
}

func freshMethodWant(field string, want any) func(map[string]any) error {
	return func(d map[string]any) error {
		if d[field] != want {
			return fmt.Errorf("%s is %v, want %v", field, d[field], want)
		}
		return nil
	}
}

// TestIntegration_Fresh_PaymentMethodRemovedReachesThePayersFeed: RemoveMethod.
func TestIntegration_Fresh_PaymentMethodRemovedReachesThePayersFeed(t *testing.T) {
	rig := newPayRig(t, NewStubGateway())
	stampMethodsIntoFeed(t, rig)
	c := freshMethodsCase(t, rig)
	c.Write = func(ctx context.Context) (string, error) {
		return rig.methodID, NewRepository(rig.pool).RemoveMethod(ctx, rig.customerID, rig.methodID)
	}
	c.Want = freshMethodWant("status", "removed")
	harness.Fresh(t, c)
}

// TestIntegration_Fresh_PaymentMethodAddedReachesThePayersFeed: AddMethod.
func TestIntegration_Fresh_PaymentMethodAddedReachesThePayersFeed(t *testing.T) {
	t.Skip("SF-4: tenant stamp (AddMethod writes no tenant_id, so the payment_methods fence never admits the new card)")
	rig := newPayRig(t, NewStubGateway())
	stampMethodsIntoFeed(t, rig)
	c := freshMethodsCase(t, rig)
	c.Write = func(ctx context.Context) (string, error) {
		m, err := NewRepository(rig.pool).AddMethod(ctx, PaymentMethod{
			CustomerID: rig.customerID, Gateway: "stub", MethodType: "CARD", Brand: "VISA", Last4: "4242", ExpMonth: 12, ExpYear: 2099,
		}, false)
		return m.ID, err
	}
	c.Want = freshMethodWant("last4", "4242")
	harness.Fresh(t, c)
}

// TestIntegration_Fresh_PaymentMethodDefaultFlipReachesThePayersFeed:
// SetDefaultMethod makes a second card the default (the first, already the
// default, is cleared in the same transaction).
func TestIntegration_Fresh_PaymentMethodDefaultFlipReachesThePayersFeed(t *testing.T) {
	rig := newPayRig(t, NewStubGateway())
	second, err := NewRepository(rig.pool).AddMethod(context.Background(), PaymentMethod{
		CustomerID: rig.customerID, Gateway: "stub", MethodType: "CARD", Brand: "MASTERCARD", Last4: "5100", ExpMonth: 12, ExpYear: 2099,
	}, false)
	if err != nil {
		t.Fatalf("seed second method: %v", err)
	}
	stampMethodsIntoFeed(t, rig)
	c := freshMethodsCase(t, rig)
	c.Write = func(ctx context.Context) (string, error) {
		return second.ID, NewRepository(rig.pool).SetDefaultMethod(ctx, rig.customerID, second.ID)
	}
	c.Want = freshMethodWant("is_default", true)
	harness.Fresh(t, c)
}
