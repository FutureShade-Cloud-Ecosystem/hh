//go:build integration

// fw1_01b_cancel_date_integration_test.go, FW1-01b round 2's leg on the deposit cancel's own
// date (S3's review, FROM-PRINCIPAL 112, P1), and round 2b's leg on the cancel's CLOCK
// (FROM-PRINCIPAL 121).
//
// THE RULE ROUND 2B FIXES. The cancel reads clock_timestamp() in its OWN transaction, and that
// instant rides the event as occurred_at and stamps the settlement. The journal the GL posts for
// the cancel and the settlement's AR rows are then ONE clock by construction, so the ledger's date
// and the customer's statement can never land in different months.
//
// WHAT THE LEG PROVES, IN FOUR PARTS.
//  1. THE EVENT CARRIES THE SETTLEMENT'S OWN INSTANT: it equals the settlement's closed AR row's
//     stamp, the row the settlement wrote with that instant.
//  2. IT IS THE DATABASE'S CLOCK: it falls inside the bracket the database itself reports around
//     the cancel.
//  3. IT IS NOT THE WRITER'S GO CLOCK: the writer's clock is pinned to a far future sentinel the
//     database can never be at, and the event must not carry it.
//  4. THE SETTLEMENT'S TWO ROWS NEVER TIE: the credit back stamps its own later instant, so the
//     gate that reads the customer's balance off the LATEST row (ORDER BY occurred_at DESC LIMIT 1,
//     no tiebreaker) reads the credit back and not the closed row.
//
// WHY THE PINNED WRITER CLOCK IS A FAR FUTURE SENTINUM. The writer's clock is pinned so the leg can
// see the cancel ignore it. A far future year is the house's sentinel pattern: the gap to the real
// clock is decades wide and only widens, so the comparison's outcome can never change with the
// calendar.
package billing

import (
	"context"
	"testing"
	"time"
)

// fw1b2bWriterYear is the far future year the writer's own Go clock is pinned to in this leg: a
// year no real row and no real clock can ever carry, so the leg can see the cancel ignore the
// writer's clock without the assertion ever being in doubt.
const fw1b2bWriterYear = 2089

// TestIntegration_FW1_01B_R2_DepositCancelCarriesTheCancelsOwnInstant is P1's leg, strengthened by
// round 2b: the cancel event's date is the settlement's own instant, the same instant the AR rows
// stamp, so the ledger's journal date and the customer's statement can never land in different
// months.
func TestIntegration_FW1_01B_R2_DepositCancelCarriesTheCancelsOwnInstant(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	// A PART PAID deposit (4000 taken, 1500 still owed): the settlement writes TWO AR rows, the
	// closed part and the credit back, and the two must never tie.
	_, orderID, dep := seedDepositOrderAt(t, pool, tag, 1_500)

	// The writer's Go clock, pinned to a fixed instant the database can never be at: the cancel
	// must not read it.
	writerAt := time.Date(fw1b2bWriterYear, 3, 10, 9, 0, 0, 0, time.UTC)
	w := NewInvoiceWriter(pool).WithClock(func() time.Time { return writerAt })

	// The DATABASE's own clock, bracketed around the cancel.
	var before, after time.Time
	if err := pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&before); err != nil {
		t.Fatalf("read the database's clock before the cancel: %v", err)
	}
	if _, err := w.CancelOrderDeposits(ctx, nil, orderID, "CREDIT_BACK", "the round 2b clock proof"); err != nil {
		t.Fatalf("settle: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&after); err != nil {
		t.Fatalf("read the database's clock after the cancel: %v", err)
	}

	// The event's own date.
	var carried *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT (payload->>'occurred_at')::timestamptz FROM events_outbox
		  WHERE topic = 'deposit.cancelled' AND (payload->>'invoice_id')::uuid = $1::uuid`, dep).Scan(&carried); err != nil {
		t.Fatalf("read the cancel event: %v", err)
	}
	if carried == nil {
		t.Fatalf("the cancel event carries no occurred_at; the cancel's own instant must ride it")
	}

	// (3) The writer's Go clock is not the clock the cancel reads.
	if carried.UTC().Equal(writerAt.UTC()) {
		t.Fatalf("the cancel event carries the writer's pinned Go clock %v; the cancel reads the DATABASE's clock",
			carried.UTC())
	}
	// (2) The database's own clock, bracketed around the cancel.
	if carried.Before(before) || carried.After(after) {
		t.Fatalf("the cancel event carries %v, outside the database's own bracket %v..%v; the cancel must read the database's clock",
			carried.UTC(), before.UTC(), after.UTC())
	}

	// The settlement's two AR rows: the closed part's row (source_type 'invoice') and the credit
	// back's row (source_type 'deposit_cancel').
	var closedAt, creditAt *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT
		   (SELECT occurred_at FROM customer_transactions
		     WHERE source_ref_id = $1::uuid AND source_type = 'invoice'
		     ORDER BY occurred_at LIMIT 1),
		   (SELECT occurred_at FROM customer_transactions
		     WHERE source_ref_id = $1::uuid AND source_type = 'deposit_cancel'
		     ORDER BY occurred_at LIMIT 1)`, dep).Scan(&closedAt, &creditAt); err != nil {
		t.Fatalf("read the settlement's AR rows: %v", err)
	}
	if closedAt == nil || creditAt == nil {
		t.Fatalf("the settlement wrote %v closed row and %v credit row; a part paid settlement writes both",
			closedAt, creditAt)
	}
	// (1) The event carries the settlement's own instant, the one the closed row stamps.
	if !carried.UTC().Equal(closedAt.UTC()) {
		t.Fatalf("the cancel event carries %v, want the settlement's own stamp %v (one clock by construction)",
			carried.UTC(), closedAt.UTC())
	}
	// (4) The two rows never tie: the credit back is the later row the gates read.
	if !creditAt.After(*closedAt) {
		t.Fatalf("the settlement's AR rows stamp %v and %v; two rows that tie let the gate read the balance off either",
			closedAt.UTC(), creditAt.UTC())
	}
}
