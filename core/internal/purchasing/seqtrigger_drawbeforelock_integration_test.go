//go:build integration

// seqtrigger_drawbeforelock_integration_test.go: the draw before lock legs for purchasing_pos and vendors
// (PR 556, round 3c).
//
// The single row writers of both tables used to draw their counter in a CTE and then lock the row. A writer
// that relies on the sync seq trigger takes the row first and the counter after, so two writers on one row
// crossed. The writers now rely on the trigger too. Each leg parks the trigger relying writer (a plain touch
// of the row) between its row lock and its draw with a BEFORE INSERT trigger on scope_sequences
// (testkit.DrawPark), then runs the real writer on the same row. Red at 099f2e27: the writer draws the counter,
// waits for the row, the park releases, the touch wants the counter, 40P01.
package purchasing

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestIntegration_DrawBeforeLock_PurchasingPOWriterBesideAPOWrite(t *testing.T) {
	h := newHarness(t)
	product := h.newProduct(t, "sf1dbl")
	po := h.draftPOAt(t, branchTRT, product, "", 10)

	park := testkit.NewDrawPark(t, h.pool, "purchase_orders", 55010002)
	touch := park.TouchRow("purchasing_pos", "updated_at = now()", po.ID)
	park.WaitParked("the PO touch stands between its row lock and its draw")

	set := make(chan error, 1)
	go func() { set <- NewRepository(h.pool).UpdateDeliveryMethod(context.Background(), po.ID, DeliveryPickup) }()
	park.WaitBlocked("the delivery method write waits")

	park.Answers(map[string]chan error{"touch": touch, "set delivery method": set})
}

func TestIntegration_DrawBeforeLock_VendorMasterWriterBesideAVendorWrite(t *testing.T) {
	h := newHarness(t)
	vendorMasterCleanup(t, h)
	org := h.orgID(t)
	v, err := h.svc.CreateVendor(h.ctx, org, CreateVendorRequest{Name: "Draw Before Lock Supply (demo)"})
	if err != nil {
		t.Fatalf("create vendor: %v", err)
	}

	park := testkit.NewDrawPark(t, h.pool, "vendors", 55010003)
	touch := park.TouchRow("vendors", "updated_at = now()", v.ID)
	park.WaitParked("the vendor touch stands between its row lock and its draw")

	name := "Draw Before Lock Supply Ltd (demo)"
	edit := make(chan error, 1)
	go func() {
		_, err := NewRepository(h.pool).UpdateVendorMaster(context.Background(), org, v.ID, &name, nil, nil, nil)
		edit <- err
	}()
	park.WaitBlocked("the vendor edit waits")

	park.Answers(map[string]chan error{"touch": touch, "vendor edit": edit})
}
