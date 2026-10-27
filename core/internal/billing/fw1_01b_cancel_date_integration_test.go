//go:build integration

// fw1_01b_cancel_date_integration_test.go, FW1-01b round 2's red leg on the deposit cancel's
// own date (S3's review, FROM-PRINCIPAL 112, P1): the settlement stamps the cancel's OWN
// instant on the event, so the ledger books the cancel in the month the cancel happened and
// never in the month the deposit was taken months earlier.
//
// The leg pins the writer's clock, which is the module's own date clock (the invoice's issue
// and due dates read it too), so the event's date is the cancel's own instant exactly. In
// production that clock is the same wall clock the database stamps the settlement's AR rows
// with, so the customer's statement and the ledger never land in different months.
package billing

import (
	"context"
	"testing"
	"time"
)

// TestIntegration_FW1_01B_R2_DepositCancelCarriesTheCancelsOwnInstant is P1's red half: the
// cancel event carries no date at all today, so the ledger has nothing but the deposit's
// finalization to date the cancel by.
func TestIntegration_FW1_01B_R2_DepositCancelCarriesTheCancelsOwnInstant(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	_, orderID, dep := seedDepositOrderAt(t, pool, tag, 4_000)

	// The cancel's own instant.
	cancelAt := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	w := NewInvoiceWriter(pool).WithClock(func() time.Time { return cancelAt })
	if _, err := w.CancelOrderDeposits(ctx, nil, orderID, "CREDIT_BACK", "the round 2 date proof"); err != nil {
		t.Fatalf("settle: %v", err)
	}

	var occurred *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT (payload->>'occurred_at')::timestamptz FROM events_outbox
		  WHERE topic = 'deposit.cancelled' AND (payload->>'invoice_id')::uuid = $1::uuid`, dep).Scan(&occurred); err != nil {
		t.Fatalf("read the cancel event: %v", err)
	}
	if occurred == nil {
		t.Fatalf("the cancel event carries no occurred_at; the cancel's own instant must ride it")
	}
	if !occurred.UTC().Equal(cancelAt) {
		t.Fatalf("the cancel event carries %v, want the cancel's own instant %v", occurred.UTC(), cancelAt)
	}
}
