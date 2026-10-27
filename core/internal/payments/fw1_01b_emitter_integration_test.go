//go:build integration

// fw1_01b_emitter_integration_test.go, FW1-01b round 2's emitter legs (S3's review,
// FROM-PRINCIPAL 112, P2): the payments module's events carry their own act's instant. The
// reversal carries the instant the reversing row was written, the re-application the instant
// the money was applied, so the ledger books each in the month the act happened and never in
// the month the outbox row was read.
package payments_test

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
)

// fw1b2EventDate reads one event's own instant off the outbox row.
func fw1b2EventDate(t *testing.T, w *reversalGLWorld, topic, key, value string) *time.Time {
	t.Helper()
	var carried *time.Time
	if err := w.r.obs.QueryRow(context.Background(),
		`SELECT (payload->>'occurred_at')::timestamptz FROM events_outbox
		  WHERE topic = $1 AND payload->>$2 = $3`, topic, key, value).Scan(&carried); err != nil {
		t.Fatalf("read the %s event: %v", topic, err)
	}
	return carried
}

// TestIntegration_FW1_01B_R2_ReversalCarriesItsOwnInstant is the reversal's emitter leg: the
// payment.reversed event carries the instant the reversal was written.
func TestIntegration_FW1_01B_R2_ReversalCarriesItsOwnInstant(t *testing.T) {
	w := newReversalGLWorld(t)
	orig := w.receipt("CHEQUE", 10_000)

	at := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	rev, err := w.r.repo.ReversePayment(context.Background(), payments.ReverseInput{
		PaymentID: orig, Reason: "the round 2 date proof", ActorID: w.r.userID, IsManager: true}, at)
	if err != nil {
		t.Fatalf("reverse: %v", err)
	}
	carried := fw1b2EventDate(t, w, "payment.reversed", "reverses_payment_id", orig)
	if carried == nil {
		t.Fatalf("the reversal event carries no occurred_at; the reversal's own instant must ride it")
	}
	if !carried.UTC().Equal(at) {
		t.Fatalf("the reversal event carries %v, want the reversal's own instant %v", carried.UTC(), at)
	}
	_ = rev
}

// TestIntegration_FW1_01B_R2_ReapplyCarriesItsOwnInstant is the re-application's emitter leg:
// the payment.reapplied event carries the instant the held money was applied.
func TestIntegration_FW1_01B_R2_ReapplyCarriesItsOwnInstant(t *testing.T) {
	w := newReversalGLWorld(t)
	orig := w.receipt("CHEQUE", 10_000)
	if _, err := w.r.repo.ReversePayment(context.Background(), payments.ReverseInput{
		PaymentID: orig, Reason: "the round 2 date proof", ActorID: w.r.userID, IsManager: true}, time.Now()); err != nil {
		t.Fatalf("reverse: %v", err)
	}
	// The invoice the receipt paid: the reversal reopened it, so it can take the money back.
	var invID string
	if err := w.r.obs.QueryRow(context.Background(),
		`SELECT invoice_id::text FROM payment_allocations WHERE payment_id = $1::uuid`, orig).Scan(&invID); err != nil {
		t.Fatalf("find the receipt's invoice: %v", err)
	}

	at := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	if _, err := w.r.repo.ReapplyHeld(context.Background(), payments.ReapplyInput{
		PaymentID:      orig,
		Reason:         "the round 2 date proof",
		ActorID:        w.r.userID,
		IdempotencyKey: "fw1b-r2-reapply",
		IsManager:      true,
		Allocations:    []payments.Allocation{{InvoiceID: invID, AmountCents: 10_000}},
	}, at); err != nil {
		t.Fatalf("reapply: %v", err)
	}
	carried := fw1b2EventDate(t, w, "payment.reapplied", "payment_id", orig)
	if carried == nil {
		t.Fatalf("the re-application event carries no occurred_at; the re-application's own instant must ride it")
	}
	if !carried.UTC().Equal(at) {
		t.Fatalf("the re-application event carries %v, want the re-application's own instant %v", carried.UTC(), at)
	}
}
