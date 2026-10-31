//go:build integration

// portal_payoff_growth_integration_test.go, TM-2b fix P3-1: a quoted discount settles the whole open amount.
package payments

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestPortalPayoff_AQuotedDiscountMustSettleTheWholeOpenAmount (P3-1): the portal quotes 58016 with
// 1184 off a 59200 open amount (a desk payment of 40000 taken); during the charge the desk payment is
// reversed, so the payable grows to 100000. Amount plus discount no longer equals the payable, and the
// settle refuses into flight rather than closing a partial with a discount.
func TestPortalPayoff_AQuotedDiscountMustSettleTheWholeOpenAmount(t *testing.T) {
	ctx := context.Background()
	pw := newPayoffWorld(t)
	inv := pw.discountInvoice(t, pw.customerID, "GROW", 100000, houseToday())
	desk, err := pw.repo.ApplyReceived(ctx, ApplyRequest{
		CustomerID: pw.customerID, Method: "CHEQUE", Reference: "CHQ G1",
		IdempotencyKey: pw.tag + "-grow", InvoiceIDs: []string{inv}, AmountCents: 40000,
	})
	if err != nil {
		t.Fatalf("the desk payment: %v", err)
	}
	var actor string
	if err := pw.pool.QueryRow(ctx, `SELECT id::text FROM users LIMIT 1`).Scan(&actor); err != nil {
		t.Fatalf("an actor: %v", err)
	}
	pw.gw.onCharge = func() {
		if _, err := pw.repo.ReversePayment(ctx, ReverseInput{
			PaymentID: desk.PaymentID, Reason: "bounced", ActorID: actor, IsManager: true,
		}, time.Now()); err != nil {
			t.Errorf("reverse the desk payment inside the charge: %v", err)
		}
	}
	_, err = pw.svc.Pay(ctx, pw.customerID, PayRequest{InvoiceIDs: []string{inv}, Nonce: "nonce-" + pw.tag})
	if !errors.Is(err, ErrPaymentInFlight) {
		t.Fatalf("a payable that grew mid charge must refuse the settle into flight; got %v", err)
	}
	if bal, st := pw.invoiceState(t, inv); bal != 100000 || st != "FINALIZED" {
		t.Errorf("nothing settled: want 100000 FINALIZED; got %d %s", bal, st)
	}
	var cards int
	if err := pw.pool.QueryRow(ctx,
		`SELECT count(*) FROM payments WHERE customer_id = $1::uuid AND method = 'CARD'`, pw.customerID).Scan(&cards); err != nil {
		t.Fatalf("count card payments: %v", err)
	}
	if cards != 0 {
		t.Errorf("the refused settle wrote no card payment; found %d", cards)
	}
}
