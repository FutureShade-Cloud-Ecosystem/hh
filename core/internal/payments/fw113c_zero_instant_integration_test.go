//go:build integration

package payments_test

// fw113c_zero_instant_integration_test.go, FW1-13c: the payments store refuses a zero instant.
//
// The held pay out is one transaction with three money facts: the payout row, the drawer's movement
// the till writes beside it, and the payment.paid_out event the GL journal follows. The service
// reads its clock once and hands the instant down (one clock rules that transaction, FW1-13b), so a
// zero clock writes the zero instant (0001-01-01, a day that is not a day) into the payout row and
// into the event. The till's seam, which falls back to its own clock when the caller names none,
// wrote the movement beside them on ITS clock: one pay out, two instants, the drawer naming a day
// the books never named.
//
// The till's own store refuses a zero stamp before any write (FW1-13b). The rows this round added
// sat outside that guard, so the leg drives the real payments service with a clock that answers
// zero and proves the store refuses it in words, before any write: no payout row, no event, no
// drawer movement, no journal.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
)

// fw113cCount counts the rows one query names, on the world's own observation pool.
func fw113cCount(t *testing.T, w *reversalGLWorld, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := w.r.obs.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count the rows: %v", err)
	}
	return n
}

// TestIntegration_FW113C_TheStoreRefusesAZeroInstant is the red leg. Before the fix the pay out on
// a zero clock committed: the payout row and the event took the zero instant, and the till's seam
// replaced it on the movement.
func TestIntegration_FW113C_TheStoreRefusesAZeroInstant(t *testing.T) {
	w := newReversalGLWorld(t)
	w.cleanupPayouts()
	session := w.openSession()
	orig := w.registerReceipt("CASH", 10_000)
	w.inSession(session, orig)
	w.drain()
	w.reverse(orig)

	repo := w.payoutRepo()
	in := payments.PayoutInput{
		PaymentID: orig, Kind: payments.PayoutDrawer, AmountCents: 4_000,
		Reason: "paid back to the customer", ActorID: w.r.userID,
		IdempotencyKey: fmt.Sprintf("fw113c-zero-payout-%d", time.Now().UnixNano()),
		IsManager:      true, TillSessionID: session,
	}
	_, err := payments.NewReverseService(repo).
		WithClock(func() time.Time { return time.Time{} }).
		PayOut(w.r.staffCtx(), in)
	if err == nil {
		t.Fatalf("the store refused nothing: the pay out on a zero clock committed, want the refusal in words (a zero stamp is refused)")
	}
	if !strings.Contains(err.Error(), "instant") || !strings.Contains(err.Error(), "zero") {
		t.Fatalf("the refusal must name the missing instant in words, got %q", err.Error())
	}

	// The refusal stands before any write: no payout row, no event, no drawer movement, no journal.
	if n := fw113cCount(t, w, `SELECT count(*) FROM payment_payouts WHERE payment_id = $1::uuid`, orig); n != 0 {
		t.Fatalf("the refused pay out wrote %d payout rows, want none", n)
	}
	if n := fw113cCount(t, w,
		`SELECT count(*) FROM events_outbox WHERE topic = 'payment.paid_out' AND payload->>'payment_id' = $1`, orig); n != 0 {
		t.Fatalf("the refused pay out wrote %d events, want none", n)
	}
	if n := fw113cCount(t, w, `SELECT count(*) FROM till_movements WHERE till_session_id = $1::uuid`, session); n != 0 {
		t.Fatalf("the refused pay out wrote %d drawer movements, want none", n)
	}
	w.drain()
	if n := fw113cCount(t, w,
		`SELECT count(*) FROM gl_journals WHERE idempotency_key LIKE 'payment.paid_out:%' AND source_ref_id::text = $1`, orig); n != 0 {
		t.Fatalf("the refused pay out posted %d journals, want none", n)
	}
}
