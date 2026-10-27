//go:build integration

// org_stamp_integration_test.go (UX-95): the two draft writers (portal reorder and staff duplicate) stamp
// orders.org_id from the branch's org. The CI world has no org and the seeded branches carry none, so the
// leg plants its own org and branch.
package order

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func orgOfOrder(t *testing.T, repo *Repository, orderID string) string {
	t.Helper()
	var org *string
	if err := repo.pool.QueryRow(context.Background(),
		`SELECT org_id::text FROM orders WHERE id = $1::uuid`, orderID).Scan(&org); err != nil {
		t.Fatalf("read order org: %v", err)
	}
	if org == nil {
		return ""
	}
	return *org
}

func TestIntegration_UX95_ReorderDraftStampsTheBranchOrg(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	orgID, branchID := testkit.PlantOrgBranch(t, pool)
	cust := seedDupCustomer(t, pool, "UX95R")
	res, err := repo.CreateReorderDraft(context.Background(), ReorderDraft{
		CustomerID: cust, BranchID: branchID, Fulfillment: "PICKUP", SourceOrderID: cust,
		IdempotencyKey: fmt.Sprintf("ux95-r-%d", time.Now().UnixNano()), SubtotalCents: 1000,
		Lines: []ReorderDraftLine{{LineNo: 1, Description: "l", QtyEntered: "1", UOMEntered: "EA",
			QtyBase: "1", UnitPriceCents: 1000, PriceUOM: "EA", LineTotalCents: 1000}},
	})
	if err != nil {
		t.Fatalf("reorder draft: %v", err)
	}
	if got := orgOfOrder(t, repo, res.OrderID); got != orgID {
		t.Fatalf("reorder draft orders.org_id = %q, want the branch org %q", got, orgID)
	}
}

func TestIntegration_UX95_DuplicateDraftStampsTheBranchOrg(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	orgID, branchID := testkit.PlantOrgBranch(t, pool)
	cust := seedDupCustomer(t, pool, "UX95D")
	res, err := repo.CreateDuplicateDraft(context.Background(), ReorderDraft{
		CustomerID: cust, BranchID: branchID, Fulfillment: "PICKUP", SourceOrderID: cust,
		IdempotencyKey: fmt.Sprintf("ux95-d-%d", time.Now().UnixNano()), SubtotalCents: 1000,
		Lines: []ReorderDraftLine{{LineNo: 1, Description: "l", QtyEntered: "1", UOMEntered: "EA",
			QtyBase: "1", UnitPriceCents: 1000, PriceUOM: "EA", LineTotalCents: 1000}},
	}, "")
	if err != nil {
		t.Fatalf("duplicate draft: %v", err)
	}
	if got := orgOfOrder(t, repo, res.OrderID); got != orgID {
		t.Fatalf("duplicate draft orders.org_id = %q, want the branch org %q", got, orgID)
	}
}
