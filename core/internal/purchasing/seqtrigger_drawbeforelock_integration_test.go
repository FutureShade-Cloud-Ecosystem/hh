//go:build integration

// seqtrigger_drawbeforelock_integration_test.go: the draw before lock leg for purchasing_pos (the vendors leg of round 3c is replaced by the counter first leg, seqtrigger_counterfirst_integration_test.go: vendors is a loader table, its counter comes first)
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
