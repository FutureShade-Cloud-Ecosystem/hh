//go:build integration

package pos

// fw113b_round2_one_instant_integration_test.go, FW1-13b round 2: the POS return is ONE instant.
//
// The pos module reads its clock once for the return (the return's own rows and the
// pos.return.committed event carry that instant). The till, which writes the return's REFUNDED
// payments row and the drawer movement on the same transaction, read its own clock: one return ran on
// two clocks, and the drawer's own date read could name a different day than the return's.
//
// The leg here runs the whole return through the rig's production wiring (the HTTP route, the pos
// service, the real till service bound as the tender seam) and reads the real rows on a migrated and
// seeded gate database.

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestIntegration_FW113B_ThePosReturnCarriesOneInstant is the red leg. Before the fix the return's
// event carried the pos module's instant, the drawer's movement the till's and the REFUNDED payments
// row the database's, so one return carried three instants.
func TestIntegration_FW113B_ThePosReturnCarriesOneInstant(t *testing.T) {
	s := newTrentonStack(t)
	ctx := context.Background()

	// This leg's OWN idempotency keys, not the rig's shared ones: the rig salts its keys per process,
	// so two rigs in one run share them, and a sale rung under another leg's key is that leg's sale
	// (an idempotent replay), which would spend its returnable balance. The shape is the rig's own
	// (the sale's idempotency_key column is a uuid) with a different prefix, so the two never meet.
	salt := fmt.Sprintf("%08x", os.Getpid())[:8]
	saleKey := fmt.Sprintf("5d1bf113-0000-4000-9000-%s0001", salt)
	returnKey := fmt.Sprintf("5d1bf113-0000-4000-9000-%s0002", salt)
	sale := s.ringSale(saleKey, trentonBranchID, s.trentonTill, "trenton", []float64{20}, TenderCash)
	body := s.returnBody(returnKey, sale, 0, RefundCash, 20)
	rec := s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("the cash return must commit, got %d: %s", rec.Code, rec.Body.String())
	}
	returnNo, _ := s.decodeBody(rec)["return_no"].(string)
	if returnNo == "" {
		t.Fatalf("the return must carry its number: %s", rec.Body.String())
	}

	// The return's own event, the instant the pos module read its clock for. The event carries the
	// instant as text (the whole nanosecond value), while a timestamptz column keeps microseconds and
	// truncates, so the instant the rows carry is the event's truncated to the microsecond.
	var eventText string
	if err := s.pool.QueryRow(ctx,
		`SELECT payload->>'occurred_at' FROM events_outbox
		  WHERE topic = 'pos.return.committed' AND payload->>'order_id' = $1`, sale.OrderID).Scan(&eventText); err != nil {
		t.Fatalf("read the return's event: %v", err)
	}
	eventAt, err := time.Parse(time.RFC3339Nano, eventText)
	if err != nil {
		t.Fatalf("read the return's event instant %q: %v", eventText, err)
	}
	wantAt := eventAt.Truncate(time.Microsecond)

	// The REFUNDED payments row the till wrote on the return's transaction.
	var paymentAt time.Time
	if err := s.pool.QueryRow(ctx,
		`SELECT created_at FROM payments WHERE idempotency_key = $1`, "refund:"+returnKey).Scan(&paymentAt); err != nil {
		t.Fatalf("read the return's payments row: %v", err)
	}
	if !paymentAt.Equal(wantAt) {
		t.Fatalf("the return's payments row is stamped %s, want the return's one instant %s", paymentAt, wantAt)
	}

	// The drawer movement the till wrote on the same transaction, named by the return's own number
	// (the drawer's session holds the movements of every earlier return too).
	var movementAt time.Time
	if err := s.pool.QueryRow(ctx,
		`SELECT created_at FROM till_movements WHERE till_session_id = $1::uuid AND reference = $2`,
		s.trentonTill, returnNo).Scan(&movementAt); err != nil {
		t.Fatalf("read the return's movement row: %v", err)
	}
	if !movementAt.Equal(wantAt) {
		t.Fatalf("the return's movement row is stamped %s, want the return's one instant %s", movementAt, wantAt)
	}
}
