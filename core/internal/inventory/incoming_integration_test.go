//go:build integration

// incoming_integration_test.go, DIB-460's in-transit detail and DIB-461's reorder read, against
// real Postgres.
//
// What the unit file cannot reach lives here: the SQL. Three claims are asserted against the
// database rather than against a fake -
//
//	the in-transit read agrees with the in_transit BUCKET (same definition, two queries)
//	the branch predicate actually filters (the fake ignores the reach by construction)
//	the single-line replenishment read produces the same suggestion the WORKLIST produces
//
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5432/fixinv460?sslmode=disable' \
//	  go test -tags=integration ./internal/inventory/ -run Incoming -count=1

package inventory

import (
	"context"
	"testing"
)

// shippedTransfer moves qty of a product from `from` to `to` and leaves it IN_TRANSIT.
func shippedTransfer(t *testing.T, svc *Service, from, to, productID string, qty float64) Transfer {
	t.Helper()
	ctx := context.Background()
	tr, err := svc.CreateTransfer(ctx, CreateTransferInput{
		FromBranchID: from, ToBranchID: to, Notes: "DIB-460 fixture",
		Lines: []CreateTransferLine{{ProductID: productID, QtyBase: qty}},
	})
	if err != nil {
		t.Fatalf("create transfer: %v", err)
	}
	shipped, err := svc.ShipTransfer(ctx, tr.ID, "")
	if err != nil {
		t.Fatalf("ship transfer: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = svc.repo.pool.Exec(c, `DELETE FROM stock_transfer_lines WHERE transfer_id = $1`, tr.ID)
		_, _ = svc.repo.pool.Exec(c, `DELETE FROM stock_transfers WHERE id = $1`, tr.ID)
	})
	return shipped
}

// TestIntegration_IncomingTransfers_DetailAgreesWithTheBucket is the load-bearing one: the four-
// bucket read and this detail read must be answering with the same definition of "on a truck". If
// they ever disagree a card shows "In transit 15" beside a section that lists nothing.
func TestIntegration_IncomingTransfers_DetailAgreesWithTheBucket(t *testing.T) {
	pool := intPool(t)
	// A shipped transfer leaves the source down and the destination not yet up: a transient,
	// legitimate drift window. The shared lease is what stops the whole-DB drift gates reading it
	// as a real breach (DIB-396, the rule stock_truth_integration_test.go states at length).
	dib396LeaseShared(t, pool, 3960001)
	svc := newIntSvc(pool)
	ctx := context.Background()

	product := mkProduct(t, pool, "INV460-TRANSIT")
	// Put stock in Kingston so the ship leg has something to take (SHIP grades against AVAILABLE).
	seedBalance(t, pool, product, branch2, 50, 1900)

	tr := shippedTransfer(t, svc, branch2, branch1, product, 15)

	lines, err := svc.InTransitForProduct(ctx, BranchReach{Global: true}, product)
	if err != nil {
		t.Fatalf("in-transit read: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("got %d in-transit lines, want 1: %+v", len(lines), lines)
	}
	got := lines[0]
	if got.QtyBase != 15 {
		t.Errorf("qty = %v, want 15 (shipped − received)", got.QtyBase)
	}
	if got.TransferNo != tr.TransferNo {
		t.Errorf("transfer_no = %q, want %q", got.TransferNo, tr.TransferNo)
	}
	if got.FromCode != "KGN" || got.ToCode != "TRT" {
		t.Errorf("from/to = %s→%s, want KGN→TRT, 'from where' is the whole reason this read exists",
			got.FromCode, got.ToCode)
	}
	if got.ShippedAt == "" {
		t.Error("shipped_at is empty; it is the only real date a transfer has (there is NO eta column)")
	}
	if got.UOM == "" {
		t.Error("a quantity arrived without its unit")
	}

	// The bucket, from the OTHER read, for the destination branch.
	bucket, err := svc.AvailabilityAtBranch(ctx, product, branch1)
	if err != nil {
		t.Fatalf("branch-stock: %v", err)
	}
	if bucket.InTransitBase != got.QtyBase {
		t.Fatalf("the in_transit BUCKET says %v and the detail read says %v, one card, two answers",
			bucket.InTransitBase, got.QtyBase)
	}
}

// TestIntegration_IncomingTransfers_InboundOnlyAndBranchFenced.
func TestIntegration_IncomingTransfers_InboundOnlyAndBranchFenced(t *testing.T) {
	pool := intPool(t)
	// A shipped transfer leaves the source down and the destination not yet up: a transient,
	// legitimate drift window. The shared lease is what stops the whole-DB drift gates reading it
	// as a real breach (DIB-396, the rule stock_truth_integration_test.go states at length).
	dib396LeaseShared(t, pool, 3960001)
	svc := newIntSvc(pool)
	ctx := context.Background()

	product := mkProduct(t, pool, "INV460-FENCE")
	seedBalance(t, pool, product, branch2, 50, 1900)
	shippedTransfer(t, svc, branch2, branch1, product, 15) // KGN → TRT

	// A Trenton-only caller sees the inbound leg …
	lines, err := svc.InTransitForProduct(ctx, BranchReach{BranchIDs: []string{branch1}}, product)
	if err != nil {
		t.Fatalf("read (TRT): %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("Trenton sees %d inbound legs, want 1", len(lines))
	}

	// … and a Kingston-only caller does NOT. The stock has already left Kingston's on-hand;
	// reporting it as in transit there would invite someone to subtract it twice.
	lines, err = svc.InTransitForProduct(ctx, BranchReach{BranchIDs: []string{branch2}}, product)
	if err != nil {
		t.Fatalf("read (KGN): %v", err)
	}
	if len(lines) != 0 {
		t.Fatalf("the SOURCE branch was shown %d in-transit lines for stock that has left it: %+v", len(lines), lines)
	}

	// And a caller with no grants at all sees nothing.
	lines, err = svc.InTransitForProduct(ctx, BranchReach{}, product)
	if err != nil {
		t.Fatalf("read (no grants): %v", err)
	}
	if len(lines) != 0 {
		t.Fatalf("a caller with NO branch grants was handed %d transfer lines", len(lines))
	}
}

// TestIntegration_IncomingTransfers_AReceivedTransferIsNoLongerComing.
func TestIntegration_IncomingTransfers_AReceivedTransferIsNoLongerComing(t *testing.T) {
	pool := intPool(t)
	// A shipped transfer leaves the source down and the destination not yet up: a transient,
	// legitimate drift window. The shared lease is what stops the whole-DB drift gates reading it
	// as a real breach (DIB-396, the rule stock_truth_integration_test.go states at length).
	dib396LeaseShared(t, pool, 3960001)
	svc := newIntSvc(pool)
	ctx := context.Background()

	product := mkProduct(t, pool, "INV460-LANDED")
	seedBalance(t, pool, product, branch2, 50, 1900)
	tr := shippedTransfer(t, svc, branch2, branch1, product, 15)

	if _, err := svc.ReceiveTransfer(ctx, tr.ID, ""); err != nil {
		t.Fatalf("receive transfer: %v", err)
	}
	lines, err := svc.InTransitForProduct(ctx, BranchReach{Global: true}, product)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(lines) != 0 {
		t.Fatalf("a RECEIVED transfer is still listed as in transit: %+v", lines)
	}
}

// TestIntegration_ReplenishLineFor_AgreesWithTheShortfallWorklist, DIB-461's read is the worklist's
// row for one product. The two must produce the same suggested quantity or the product page and the
// replenishment screen tell a buyer to order different amounts.
func TestIntegration_ReplenishLineFor_AgreesWithTheShortfallWorklist(t *testing.T) {
	pool := intPool(t)
	svc := newIntSvc(pool)
	ctx := context.Background()

	product := mkProduct(t, pool, "INV461-LINE")
	seedBalance(t, pool, product, branch1, 40, 1900)

	var vendorID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM vendors WHERE is_active ORDER BY vendor_no LIMIT 1`).Scan(&vendorID); err != nil {
		t.Skipf("no seeded vendor: %v", err)
	}
	if _, err := svc.SetReorderPoint(ctx, ReorderPoint{
		ProductID: product, BranchID: branch1, ReorderPointBase: 100, ReorderQtyBase: 60,
		OrderMultipleBase: 0, PreferredVendorID: vendorID, LeadTimeDays: 7, IsActive: true,
	}, ""); err != nil {
		t.Fatalf("set reorder point: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM product_reorder_points WHERE product_id = $1`, product)
	})

	// onOrderBase 0: this fixture has no purchase orders, and DIB-470 made the leg an argument so
	// the two reads cannot compute it differently. The seam's own number is proved in
	// TestIntegration_DIB470_ShortfallAgreesWithIncoming.
	line, found, err := svc.repo.ReplenishLineFor(ctx, product, branch1, 0)
	if err != nil || !found {
		t.Fatalf("replenish line: found=%v err=%v", found, err)
	}
	if line.UOM == "" {
		t.Error("the replenishment line carries no unit, the quantity it suggests would be unitless on screen")
	}

	// The same product, through the WORKLIST (it is short: projected 40 < point 100).
	wl, err := svc.ShortfallWorklist(ctx, BranchReach{Global: true}, branch1, 500)
	if err != nil {
		t.Fatalf("worklist: %v", err)
	}
	var fromList ShortfallRow
	for _, r := range wl.Rows {
		if r.ProductID == product {
			fromList = r
		}
	}
	if fromList.ProductID == "" {
		t.Fatal("the fixture is not short according to the worklist, the comparison would prove nothing")
	}
	if line.SuggestedQtyBase != fromList.SuggestedQtyBase {
		t.Fatalf("single-line suggests %v and the worklist suggests %v for the same product",
			line.SuggestedQtyBase, fromList.SuggestedQtyBase)
	}
	if line.ProjectedBase != fromList.ProjectedBase || line.AvailableBase != fromList.AvailableBase {
		t.Fatalf("the two reads disagree about the POSITION: %+v vs %+v", line, fromList)
	}
}

// TestIntegration_CreateReorderPO_DraftsFromTheRealPolicy, the whole DIB-461(a) path over real
// data, with the purchasing seam recorded rather than bound (purchasing is proved end to end in the
// Chromium walkthrough; what matters here is that the DRAFT is built from the database's own numbers).
func TestIntegration_CreateReorderPO_DraftsFromTheRealPolicy(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()

	product := mkProduct(t, pool, "INV461-DRAFT")
	seedBalance(t, pool, product, branch1, 40, 1900)

	var vendorID, vendorName string
	if err := pool.QueryRow(ctx, `SELECT id::text, name FROM vendors WHERE is_active ORDER BY vendor_no LIMIT 1`).
		Scan(&vendorID, &vendorName); err != nil {
		t.Skipf("no seeded vendor: %v", err)
	}
	rec := &intPOCreator{}
	svc := newIntSvc(pool).WithPOCreator(rec)

	// No replenishment line yet: the refusal must name the fix rather than draft a vendorless PO.
	if _, err := svc.CreateReorderPO(ctx, BranchReach{Global: true}, ReorderRequest{ProductID: product, BranchID: branch1}); err == nil {
		t.Fatal("a product with no replenishment policy was ordered from nobody")
	}
	if rec.called {
		t.Fatal("a purchase order was drafted before there was a vendor to send it to")
	}

	if _, err := svc.SetReorderPoint(ctx, ReorderPoint{
		ProductID: product, BranchID: branch1, ReorderPointBase: 100, ReorderQtyBase: 60,
		OrderMultipleBase: 40, PreferredVendorID: vendorID, LeadTimeDays: 7, IsActive: true,
	}, ""); err != nil {
		t.Fatalf("set reorder point: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM product_reorder_points WHERE product_id = $1`, product)
	})

	res, err := svc.CreateReorderPO(ctx, BranchReach{Global: true}, ReorderRequest{ProductID: product, BranchID: branch1})
	if err != nil {
		t.Fatalf("reorder: %v", err)
	}
	if !rec.called {
		t.Fatal("the POCreator seam was never reached")
	}
	if rec.got.VendorID != vendorID || rec.got.BranchID != branch1 {
		t.Fatalf("draft = %+v, want the policy's vendor at the asked-for branch", rec.got)
	}
	// point 100 − projected 40 = 60 needed, rounded UP to two 40-unit skids.
	if len(rec.got.Lines) != 1 || rec.got.Lines[0].QtyBase != 80 {
		t.Fatalf("draft lines = %+v, want one line of 80 (60 needed → two 40-skids)", rec.got.Lines)
	}
	if res.VendorName != vendorName {
		t.Errorf("vendor name = %q, want %q, the buyer must see where it went", res.VendorName, vendorName)
	}
	if res.PONo != "#PO-INT-1" {
		t.Errorf("po_no = %q, want the number the seam returned", res.PONo)
	}
}

// intPOCreator records the draft the service hands the seam.
type intPOCreator struct {
	got    SuggestedPO
	called bool
}

func (c *intPOCreator) CreateDraftPO(_ context.Context, in SuggestedPO) (string, string, error) {
	c.got, c.called = in, true
	return "po-int-1", "#PO-INT-1", nil
}
