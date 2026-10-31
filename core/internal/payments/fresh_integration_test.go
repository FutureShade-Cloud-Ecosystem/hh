//go:build integration

// fresh_integration_test.go: the sync freshness harness proven on a real
// write path that is already clean. A payer who has synced the payments feed
// must see the reversing entry a manager's undo posts on the next pull from
// the device's cursor.
package payments

import (
	"context"
	"fmt"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync/harness"
)

func TestIntegration_Fresh_PaymentReversalReachesThePayersFeed(t *testing.T) {
	ctx := context.Background()
	rig := newPayRig(t, NewStubGateway())
	cleanupIntents(t, rig)

	pay, err := rig.svc.Pay(ctx, rig.customerID, PayRequest{
		InvoiceIDs: []string{rig.invoiceA, rig.invoiceB}, PaymentMethodID: rig.methodID, ExpectedCents: 55000,
	})
	if err != nil {
		t.Fatalf("seed payment: %v", err)
	}

	info, ok := sync.Lookup("payments")
	if !ok {
		t.Fatalf("scope payments missing")
	}
	engine := sync.NewEngine()
	if err := engine.RegisterScope("payments", NewPaymentsSyncStore(rig.pool, rig.svc)); err != nil {
		t.Fatalf("register: %v", err)
	}

	// The payments fence is the tenant; the org anchor is set for both
	// caller classes and the store reads only the customer.
	const orgID = "3ef10000-0000-4000-8000-000000000001"

	harness.Fresh(t, harness.FreshCase{
		Engine: engine,
		Scope:  info,
		Filter: sync.ScopeFilter{OrgID: orgID, TenantID: rig.customerID, Tenant: true, Actor: "fresh"},
		Write: func(ctx context.Context) (string, error) {
			rev, err := NewReverseService(NewRepository(rig.pool)).Reverse(ctx, ReverseInput{
				PaymentID: pay.ID, Reason: "freshness harness", IsManager: true,
			})
			return rev.ReversalPaymentID, err
		},
		Want: func(data map[string]any) error {
			if data["status"] != "VOID" {
				return fmt.Errorf("status is %v, want VOID", data["status"])
			}
			return nil
		},
	})
}
