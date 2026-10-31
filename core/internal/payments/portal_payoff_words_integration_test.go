//go:build integration

// portal_payoff_words_integration_test.go, TM-2b fix P3-2: the refusal says what changed.
package payments

import (
	"context"
	"strings"
	"testing"
)

// TestPortalPayoff_ALapsedDiscountRefusalSaysTheDiscountNoLongerHolds (P3-2): a terms change mid charge
// is refused in the words of the cause, not as a changed balance.
func TestPortalPayoff_ALapsedDiscountRefusalSaysTheDiscountNoLongerHolds(t *testing.T) {
	ctx := context.Background()
	pw := newPayoffWorld(t)
	plain := pw.invoice(t, pw.customerID, "NOTERMS2", 5, -25, 30000)
	_, err := pw.repo.Pay(ctx, pw.customerID, "CARD", "gw-words-"+pw.tag,
		[]Allocation{{InvoiceID: plain, AmountCents: 27000, DiscountCents: 3000}})
	if err == nil || !strings.Contains(err.Error(), "the discount quoted no longer holds") {
		t.Fatalf("want the words \"the discount quoted no longer holds\"; got %v", err)
	}
	if strings.Contains(err.Error(), "balance changed") {
		t.Errorf("a terms change is not a balance change: %v", err)
	}
}
