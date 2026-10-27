//go:build integration

package payments_test

// fw113b_round2_one_instant_integration_test.go, FW1-13b round 2: the held payout is ONE instant.
//
// The held pay out is the payments module's act (SF-2m): the payout row, the drawer movement the till
// writes on the same transaction and the payment.paid_out event that posts the journal. The payout
// row took the database's clock, the drawer movement the till's clock and the journal the GL
// consumer's clock, so one transaction ran on three clocks and the journal's own day could differ
// from the money's.
//
// The leg here runs the held payout through the real payments service with the real till service
// bound as the till's seam (the production wiring shape) and reads the real rows on a migrated and
// seeded gate database.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fw113bPayoutPinnedDay is the instant this leg pins: 15 April of the books' FIRST seeded year, noon
// in the house zone. April is a month of that year no leg reads (the tie-out legs read June, the
// walk reads the month the run happens in), and a day that ended long before the run.
func fw113bPayoutPinnedDay(t *testing.T, pool *pgxpool.Pool) time.Time {
	t.Helper()
	var minYear int
	if err := pool.QueryRow(context.Background(), `SELECT COALESCE(MIN(year), 0) FROM fiscal_periods`).Scan(&minYear); err != nil {
		t.Skipf("no fiscal periods to read: %v", err)
	}
	if minYear == 0 {
		t.Skipf("no fiscal periods seeded")
	}
	return time.Date(minYear, time.April, 15, 12, 0, 0, 0, houseday.Location())
}

// TestIntegration_FW113B_TheHeldPayoutCarriesOneInstant is the red leg. Before the fix the payout
// row carried the database's clock, the drawer movement the till's and the journal the consumer's.
func TestIntegration_FW113B_TheHeldPayoutCarriesOneInstant(t *testing.T) {
	w := newReversalGLWorld(t)
	w.cleanupPayouts()
	session := w.openSession()
	orig := w.registerReceipt("CASH", 10_000)
	w.inSession(session, orig)
	w.drain()
	w.reverse(orig)

	pinned := fw113bPayoutPinnedDay(t, w.r.pool)
	repo := w.payoutRepo()
	in := payments.PayoutInput{
		PaymentID: orig, Kind: payments.PayoutDrawer, AmountCents: 4_000,
		Reason: "paid back to the customer", ActorID: w.r.userID,
		IdempotencyKey: fmt.Sprintf("fw113b-r2-payout-%d", time.Now().UnixNano()),
		IsManager:      true, TillSessionID: session,
	}
	res, err := payments.NewReverseService(repo).WithClock(func() time.Time { return pinned }).PayOut(w.r.staffCtx(), in)
	if err != nil {
		t.Fatalf("the drawer pay out was refused: %v", err)
	}
	// The journal and the event this leg's pay out posts are cleaned up by their own keys, so the
	// shared gate database keeps no row this leg left behind.
	t.Cleanup(func() {
		c := context.Background()
		_, _ = w.r.obs.Exec(c, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE idempotency_key = $1)`,
			"payment.paid_out:"+res.PayoutID)
		_, _ = w.r.obs.Exec(c, `DELETE FROM gl_journals WHERE idempotency_key = $1`, "payment.paid_out:"+res.PayoutID)
		_, _ = w.r.obs.Exec(c, `DELETE FROM events_outbox WHERE topic = 'payment.paid_out' AND payload->>'payout_id' = $1`, res.PayoutID)
	})

	// The payout row the payments module writes.
	var payoutAt time.Time
	if err := w.r.obs.QueryRow(context.Background(),
		`SELECT created_at FROM payment_payouts WHERE id = $1::uuid`, res.PayoutID).Scan(&payoutAt); err != nil {
		t.Fatalf("read the payout row: %v", err)
	}
	if !payoutAt.Equal(pinned) {
		t.Fatalf("the payout row is stamped %s, want the pay out's one instant %s", payoutAt, pinned)
	}

	// The drawer movement the till writes on the same transaction.
	var movementAt time.Time
	if err := w.r.obs.QueryRow(context.Background(),
		`SELECT created_at FROM till_movements WHERE till_session_id = $1::uuid AND reason LIKE 'HELD PAYOUT%'`,
		session).Scan(&movementAt); err != nil {
		t.Fatalf("read the pay out's movement row: %v", err)
	}
	if !movementAt.Equal(pinned) {
		t.Fatalf("the pay out's movement row is stamped %s, want the pay out's one instant %s", movementAt, pinned)
	}

	// The event the journal follows.
	var occurredAt time.Time
	if err := w.r.obs.QueryRow(context.Background(),
		`SELECT (payload->>'occurred_at')::timestamptz FROM events_outbox
		  WHERE topic = 'payment.paid_out' AND payload->>'payout_id' = $1`, res.PayoutID).Scan(&occurredAt); err != nil {
		t.Fatalf("read the pay out's event: %v", err)
	}
	if !occurredAt.Equal(pinned) {
		t.Fatalf("the pay out's event carries %s, want the pay out's one instant %s", occurredAt, pinned)
	}

	// And the journal it posts, dated from that same instant.
	w.drain()
	var postedAt time.Time
	if err := w.r.obs.QueryRow(context.Background(),
		`SELECT posted_at FROM gl_journals WHERE idempotency_key = $1`, "payment.paid_out:"+res.PayoutID).Scan(&postedAt); err != nil {
		t.Fatalf("read the pay out's journal: %v", err)
	}
	if !postedAt.Equal(pinned) {
		t.Fatalf("the pay out's journal is dated %s, want the pay out's one instant %s", postedAt, pinned)
	}
}
