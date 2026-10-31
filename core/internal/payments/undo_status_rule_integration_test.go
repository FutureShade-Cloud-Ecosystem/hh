//go:build integration

// undo_status_rule_integration_test.go, TM-2 fix round 2, P3-1: one FINALIZED rule at every undo
// door. A reopened invoice reads FINALIZED when its balance is back to its total (>= total_cents)
// and PARTIALLY_PAID below it, whichever door reopened it. The leg plants the case where the two
// old rules disagreed: a balance that lands between total less holdback and total, because
// something other than the receipt took a part of the invoice.
//
// This file covers the void and the reversal; the bounce is in the till package
// (TestNSFStatusRule_...) and billing's credit reverse in the billing package.
//
// Run: go test -tags integration ./internal/payments/ -run UndoStatusRule
package payments

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

func TestUndoStatusRule_VoidAndReversalReadPartiallyPaidBelowTotal(t *testing.T) {
	for _, undo := range []string{"void", "reversal"} {
		t.Run(undo, func(t *testing.T) {
			ctx := context.Background()
			w := newApplyWorld(t)
			inv := w.invoice(t, w.customerID, "UST-"+undo, 5, 0, 100000)
			// Total 100000, holdback 10000; something other than the receipt took 5000, so the
			// balance is 95000, between total less holdback (90000) and total (100000).
			if _, err := w.pool.Exec(ctx,
				`UPDATE invoices SET holdback_cents = 10000, balance_cents = 95000, status = 'PARTIALLY_PAID' WHERE id = $1::uuid`, inv); err != nil {
				t.Fatalf("plant the holdback and the other part payment: %v", err)
			}
			ans, err := w.repo.ApplyReceived(ctx, ApplyRequest{
				CustomerID: w.customerID, Method: "CHEQUE", Reference: "CHQ UST",
				IdempotencyKey: w.tag + "-ust-" + undo, InvoiceIDs: []string{inv}, AmountCents: 2000,
			})
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			if bal, _ := w.invoiceState(t, inv); bal != 93000 {
				t.Fatalf("the apply left %d, want 93000", bal)
			}
			switch undo {
			case "void":
				_, err = w.repo.VoidPayment(ctx, VoidInput{PaymentID: ans.PaymentID, Reason: "status rule", IsManager: true}, time.Now(), houseday.DefaultZone())
			default:
				_, err = w.repo.ReversePayment(ctx, ReverseInput{PaymentID: ans.PaymentID, Reason: "status rule", IsManager: true}, time.Now())
			}
			if err != nil {
				t.Fatalf("%s: %v", undo, err)
			}
			if bal, st := w.invoiceState(t, inv); bal != 95000 || st != "PARTIALLY_PAID" {
				t.Errorf("after the %s the invoice reads %d %s, want 95000 PARTIALLY_PAID (below its total)", undo, bal, st)
			}
		})
	}
}
