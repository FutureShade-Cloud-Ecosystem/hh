//go:build integration

package payments_test

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
)

// PR 602 round 2, P3-1: the POS pay balance trail lists the RECEIVED payments allocated to an owing order's invoices.
// A reversed original keeps status RECEIVED (the reversal stamps reversed_at), so it used to be listed as money
// received after its reversal returned it. A reversed payment is not on the trail; a live one still is.
func TestIntegration_ReceivedPaymentsForInvoices_ALiveOneIsListedAReversedOneIsNot(t *testing.T) {
	r := newLORig(t, 4)
	ctx := context.Background()
	invLive, invReversed := r.invoice("RTL", 1_000_000), r.invoice("RTR", 1_000_000)
	live := r.payOnce(invLive, 10_000)
	gone := r.payOnce(invReversed, 7_000)
	if _, err := r.repo.ReversePayment(ctx, payments.ReverseInput{PaymentID: gone.ID, Reason: "trail leg", ActorID: r.userID, IsManager: true}, time.Now()); err != nil {
		t.Fatalf("reverse: %v", err)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	got, err := r.repo.ReceivedPaymentsForInvoices(ctx, tx, []string{invLive, invReversed})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	t.Logf("trail: %+v", got)
	seenLive, seenGone := false, false
	for _, p := range got {
		seenLive = seenLive || p.PaymentID == live.ID
		seenGone = seenGone || p.PaymentID == gone.ID
	}
	if !seenLive {
		t.Errorf("the live payment is missing from the trail")
	}
	if seenGone {
		t.Errorf("the reversed payment is listed as received")
	}
}
