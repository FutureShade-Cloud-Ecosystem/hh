//go:build integration

// policy_supplier_deposit_integration_test.go, the dealer setting for a
// cancelled special order's supplier deposit against the live pos_policy
// column: the round trip through the real repository, and the branch-override
// resolution the order module's cancel read relies on.
//
// Run: DATABASE_URL=... go test -tags=integration ./internal/pos/... -run PosSupplierDeposit -count=1
package pos

import (
	"context"
	"testing"
)

func TestIntegrationPosSupplierDepositCancel_PolicyRoundTrip(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	before, err := repo.ReadPosPolicy(ctx, "")
	if err != nil {
		t.Fatalf("read the org-default counter policy: %v", err)
	}
	t.Cleanup(func() { _ = repo.UpsertPosPolicy(context.Background(), before) })

	next := before
	next.SupplierDepositCancel = SupplierDepositCancelKept
	if err := repo.UpsertPosPolicy(ctx, next); err != nil {
		t.Fatalf("the supplier_deposit_cancel column refused KEPT: %v", err)
	}
	got, err := repo.ReadPosPolicy(ctx, "")
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if got.SupplierDepositCancel != SupplierDepositCancelKept {
		t.Fatalf("supplier_deposit_cancel did not round trip: %q", got.SupplierDepositCancel)
	}
}

func TestIntegrationPosSupplierDepositCancel_DefaultRead(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	ctx := context.Background()

	got, err := svc.SupplierDepositCancelDefault(ctx, "")
	if err != nil {
		t.Fatalf("default read: %v", err)
	}
	// The seeded org row carries the column default, and the read answers the
	// ruling's default, never an empty word.
	if got != SupplierDepositCancelCreditBack {
		t.Fatalf("the un-set column must answer CREDIT_BACK, got %q", got)
	}
}
