//go:build integration

// org_stamp_integration_test.go (UX-95): the counter's order write stamps orders.org_id from the branch's org.
package pos

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestIntegration_UX95_RingStampsTheBranchOrg(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	repo := NewRepository(pool)
	orgID, branchID := testkit.PlantOrgBranch(t, pool)
	cust := seedRingCustomer(t, pool)
	var id string
	if err := repo.RunInTx(ctx, func(tx pgx.Tx) error {
		var err error
		id, _, err = repo.InsertOrder(ctx, tx, orderRow{
			CustomerID: cust, BranchID: branchID, Status: "CONFIRMED", Fulfillment: "PICKUP",
			SubtotalCents: 1000, TaxCents: 130, TotalCents: 1130, Metadata: map[string]any{},
		})
		return err
	}); err != nil {
		t.Fatalf("insert order: %v", err)
	}
	var got *string
	if err := pool.QueryRow(ctx, `SELECT org_id::text FROM orders WHERE id = $1::uuid`, id).Scan(&got); err != nil {
		t.Fatalf("read order org: %v", err)
	}
	if got == nil || *got != orgID {
		t.Fatalf("ring orders.org_id = %v, want the branch org %q", got, orgID)
	}
}
