//go:build integration

package inventory

// shortfall_novendor_integration_test.go: a managed line with no preferred vendor is a state the
// worklist must read, not fall over on. The demo world plants reorder points before anyone has
// named a vendor, and the worklist query cast the empty vendor text to a uuid for its join, so one
// such row failed the whole read (SQLSTATE 22P02) and no row of the worklist reached the buyer.

import (
	"context"
	"testing"
)

func TestStockTruth_ShortfallReadsALineWithNoPreferredVendor(t *testing.T) {
	pool := intPool(t)
	dib396LeaseShared(t, pool, dib396StockDriftLease)
	svc := newIntSvc(pool)
	ctx := context.Background()
	p := mkProduct(t, pool, "W4B-NOVENDOR")
	seedBalance(t, pool, p, branch1, 10, 900)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM product_reorder_points WHERE product_id = $1`, p)
	})
	if _, err := svc.SetReorderPoint(ctx, ReorderPoint{
		ProductID: p, BranchID: branch1, ReorderPointBase: 50, ReorderQtyBase: 60, IsActive: true,
	}, ""); err != nil {
		t.Fatalf("SetReorderPoint: %v", err)
	}
	wl, err := svc.ShortfallWorklist(ctx, branch1, 500)
	if err != nil {
		t.Fatalf("a line with no preferred vendor failed the whole worklist: %v", err)
	}
	for _, r := range wl.Rows {
		if r.ProductID != p {
			continue
		}
		if r.PreferredVendorID != "" || r.PreferredVendorName != "" {
			t.Errorf("the row names vendor %q (%q), want none", r.PreferredVendorID, r.PreferredVendorName)
		}
		return
	}
	t.Error("the short line with no preferred vendor is not on the worklist")
}
