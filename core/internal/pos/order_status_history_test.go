package pos

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
)

// statusRows returns every ORDER_STATUS_CHANGED record the recorder captured, in emit order.
func (r *fakeRecorder) statusRows() []audit.SensitiveAccess {
	var out []audit.SensitiveAccess
	for _, x := range r.rows {
		if x.Action == audit.ActOrderStatusChanged {
			out = append(out, x)
		}
	}
	return out
}

// DIB-159 REGRESSION (MAJOR-1): a committed COLLECT_NOW sale inserts the order at CONFIRMED and never
// advances orders.status past CONFIRMED, fulfilment at ring is a stock decrement + order.fulfilled
// event (GL COGS), NOT a status-column write. So the timeline must record ONLY the real ""→CONFIRMED
// creation transition; it must NOT fabricate a CONFIRMED→FULFILLED row for a status the order never had.
// (Before this fix the sale emitted a second, fabricated FULFILLED transition that contradicted the
// source-of-truth status column.)
func TestSale_CollectNow_EmitsOnlyCreationStatus_NoFabricatedFulfilled(t *testing.T) {
	h := newHarness()
	out, err := h.svc.Sale(context.Background(), baseInput())
	if err != nil {
		t.Fatalf("sale: %v", err)
	}
	rows := h.rec.statusRows()
	if len(rows) != 1 {
		t.Fatalf("COLLECT_NOW sale emitted %d ORDER_STATUS_CHANGED rows, want exactly 1 (creation only, orders.status never reaches FULFILLED): %+v", len(rows), rows)
	}
	// The single row is the creation transition "" → CONFIRMED, keyed to the order, attributed, entity "order".
	r0 := rows[0]
	if r0.EntityType != "order" || r0.EntityID != out.OrderID {
		t.Errorf("row0 entity = %s/%s, want order/%s", r0.EntityType, r0.EntityID, out.OrderID)
	}
	if r0.Extra["old_status"] != "" || r0.Extra["new_status"] != "CONFIRMED" {
		t.Errorf("row0 from→to = %v→%v, want →CONFIRMED", r0.Extra["old_status"], r0.Extra["new_status"])
	}
	// No row may claim a FULFILLED transition the status column never underwent.
	for _, r := range rows {
		if r.Extra["new_status"] == "FULFILLED" {
			t.Errorf("fabricated FULFILLED transition emitted (orders.status never advances to FULFILLED at ring): %+v", r.Extra)
		}
	}
}

// A COLLECT_LATER sale is not fulfilled at ring, only the creation transition (""→CONFIRMED) is
// recorded; no spurious FULFILLED row appears (the fulfilment path did not run).
func TestSale_CollectLater_EmitsOnlyCreationStatus(t *testing.T) {
	h := newHarness()
	in := baseInput()
	in.SaleType = SaleCollectLater
	if _, err := h.svc.Sale(context.Background(), in); err != nil {
		t.Fatalf("sale: %v", err)
	}
	rows := h.rec.statusRows()
	if len(rows) != 1 {
		t.Fatalf("COLLECT_LATER sale emitted %d ORDER_STATUS_CHANGED rows, want exactly 1 (creation only): %+v", len(rows), rows)
	}
	if rows[0].Extra["new_status"] != "CONFIRMED" {
		t.Errorf("the single row must be the →CONFIRMED creation transition, got %v", rows[0].Extra)
	}
}
