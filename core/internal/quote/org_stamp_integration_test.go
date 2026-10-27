//go:build integration

// org_stamp_integration_test.go (UX-95): quote conversion stamps orders.org_id from the branch's org.
package quote

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestIntegration_UX95_ConvertStampsTheBranchOrg(t *testing.T) {
	pool := intPool(t)
	orgID, branchID := testkit.PlantOrgBranch(t, pool)
	cust, quoteID, _, _ := seedQuote(t, pool, "ACCEPTED", nil)
	cleanupConvertedOrders(t, pool, quoteID)
	setCreditLimit(t, pool, cust, 5_000_00)
	if _, err := pool.Exec(context.Background(), `UPDATE quotes SET branch_id = $2::uuid WHERE id = $1::uuid`, quoteID, branchID); err != nil {
		t.Fatalf("move quote to the planted branch: %v", err)
	}
	res, err := gatedRepo(pool).Convert(context.Background(), cust, quoteID, ConvertOptions{})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	var got *string
	if err := pool.QueryRow(context.Background(), `SELECT org_id::text FROM orders WHERE id = $1::uuid`, res.OrderID).Scan(&got); err != nil {
		t.Fatalf("read order org: %v", err)
	}
	if got == nil || *got != orgID {
		t.Fatalf("converted orders.org_id = %v, want the branch org %q", got, orgID)
	}
}
