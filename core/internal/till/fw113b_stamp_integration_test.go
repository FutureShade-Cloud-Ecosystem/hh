//go:build integration

package till_test

// fw113b_stamp_integration_test.go, FW1-13b's leg: one clock rules each till transaction. The till
// service's clock is pinned to a day in an ENDED month of the books, a lodgement and a bounce are
// recorded through that pinned service, and the movement row, the bounce row, the deposit slip's
// link rows (0441) and BOTH journals must carry that day in the house zone. Before the fix the rows
// take the created_at column default (the database's now()), so a pinned clock never reaches them,
// and the journals date from that same wall clock through the payloads' occurred_at. The rule is
// 616's deposit cancel: the caller's own instant dates the document and the journal follows it.

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

// dayInHouseZone reads one timestamp's calendar day in the house zone.
func (w *mjWorld) dayInHouseZone(query string, args ...any) string {
	w.t.Helper()
	var day string
	if err := w.pool.QueryRow(context.Background(), query, args...).Scan(&day); err != nil {
		w.t.Fatalf("read the stamped day: %v", err)
	}
	return day
}

// TestFW113BPinnedClockStampsTheMovementTheBounceAndBothJournals is the red leg. The pinned day is
// June 15 of the books' FIRST seeded year at noon in the house zone: a month that ended long before
// the run, so the wall clock's day can never stand in for it.
func TestFW113BPinnedClockStampsTheMovementTheBounceAndBothJournals(t *testing.T) {
	w := newMJWorld(t)
	ctx := context.Background()

	var minYear int
	if err := w.pool.QueryRow(ctx, `SELECT COALESCE(MIN(year), 0) FROM fiscal_periods`).Scan(&minYear); err != nil {
		t.Skipf("no fiscal periods to read: %v", err)
	}
	if minYear == 0 {
		t.Skipf("no fiscal periods seeded")
	}
	zone := houseday.Location()
	pinned := time.Date(minYear, time.June, 15, 12, 0, 0, 0, zone)
	wantDay := pinned.In(zone).Format("2006-01-02")

	svc := till.NewService(till.NewRepository(w.pool)).WithClock(func() time.Time { return pinned })

	// The lodgement: a cheque named on its slip, so the movement, the 0441 link rows and the ruled
	// DR 1000 / CR 1020 journal all land in the one transaction.
	cheque := w.slipPayment("CHEQUE", 30_00)
	if err := svc.Lodge(staffCtx(w.userID, w.branchID), till.MovementInput{
		TillSessionID: w.sessionID, MovementType: "LODGEMENT", Method: "CHEQUE",
		AmountCents: 30_00, Reason: "FW1-13b pinned clock slip", PaymentIDs: []string{cheque},
	}); err != nil {
		t.Fatalf("the lodgement: %v", err)
	}
	var movementID string
	if err := w.pool.QueryRow(ctx,
		`SELECT id::text FROM till_movements WHERE till_session_id = $1::uuid AND movement_type = 'LODGEMENT'`, w.sessionID).Scan(&movementID); err != nil {
		t.Fatalf("find the lodgement: %v", err)
	}
	if got := w.dayInHouseZone(
		`SELECT to_char(created_at AT TIME ZONE $2, 'YYYY-MM-DD') FROM till_movements WHERE id = $1::uuid`,
		movementID, zone.String()); got != wantDay {
		t.Fatalf("the lodgement movement row is stamped %s, want the pinned clock's day %s", got, wantDay)
	}
	if got := w.dayInHouseZone(
		`SELECT to_char(created_at AT TIME ZONE $2, 'YYYY-MM-DD') FROM till_lodgement_payments WHERE till_movement_id = $1::uuid`,
		movementID, zone.String()); got != wantDay {
		t.Fatalf("the slip's link row is stamped %s, want the pinned clock's day %s", got, wantDay)
	}

	// The bounce of the lodged cheque (DEPOSITED at lodgement, so the bounce credits the bank).
	if _, err := svc.MarkNSF(staffCtx(w.userID, w.branchID), till.NSFInput{PaymentID: cheque, Reason: "FW1-13b pinned clock bounce"}); err != nil {
		t.Fatalf("the bounce: %v", err)
	}
	if got := w.dayInHouseZone(
		`SELECT to_char(created_at AT TIME ZONE $2, 'YYYY-MM-DD') FROM payment_nsf WHERE payment_id = $1::uuid`,
		cheque, zone.String()); got != wantDay {
		t.Fatalf("the bounce row is stamped %s, want the pinned clock's day %s", got, wantDay)
	}

	// Both journals date from that same pinned value, through the payloads' occurred_at, never from
	// the outbox consumer's clock.
	w.drain()
	for _, key := range []string{"till.movement.posted:" + movementID, "payment.bounced:" + cheque} {
		if got := w.dayInHouseZone(
			`SELECT to_char(posted_at AT TIME ZONE $2, 'YYYY-MM-DD') FROM gl_journals WHERE idempotency_key = $1`,
			key, zone.String()); got != wantDay {
			t.Fatalf("the journal %s is dated %s, want the pinned clock's day %s", key, got, wantDay)
		}
	}
}
