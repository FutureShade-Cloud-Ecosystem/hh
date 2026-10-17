//go:build integration

// received_apply_body_integration_test.go, THE BODY A REPLAY MUST MATCH. A key that already
// recorded a payment answers THAT payment only to the request that describes it: the same
// account, the same amount, the same method, the same reference and the same invoice list,
// each read after the route's own normalising. Anything else under that key is a different
// payment wearing a spent key, and the answer is a refusal in words, never the first result
// dressed up as a replay (item 102: the desk that retries with a corrected amount must not be
// told "replayed" with the old figure).
//
// The control sits beside the refusals: the same body in a different list order, and the same
// body naming an invoice the money never reached, are both the same payment and answer the
// first result.
//
// Run: go test -tags integration ./internal/payments/ -run ApplyReceived
package payments

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// The same key with a different body is refused in words, one leg per field: the amount, the
// method, the reference and the invoice list. Nothing is written by any of them.
func TestApplyReceived_TheSameKeyWithADifferentBodyIsRefusedInWords(t *testing.T) {
	w := newApplyWorld(t)
	ctx := context.Background()
	a := w.invoice(t, w.customerID, "BA", 90, 60, 40000)
	b := w.invoice(t, w.customerID, "BB", 30, 10, 40000)
	first, err := w.repo.ApplyReceived(ctx, ApplyRequest{
		CustomerID: w.customerID, Method: "CASH", Reference: "CHQ 771",
		IdempotencyKey: w.tag, InvoiceIDs: []string{a}, AmountCents: 40000,
	})
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if first.PaymentID == "" {
		t.Fatal("the first apply answers a payment")
	}

	different := []struct {
		name string
		body ApplyRequest
	}{
		{"the amount", ApplyRequest{
			CustomerID: w.customerID, Method: "CASH", Reference: "CHQ 771",
			IdempotencyKey: w.tag, InvoiceIDs: []string{a}, AmountCents: 90000}},
		{"the method", ApplyRequest{
			CustomerID: w.customerID, Method: "CARD", Reference: "CHQ 771",
			IdempotencyKey: w.tag, InvoiceIDs: []string{a}, AmountCents: 40000}},
		{"the reference", ApplyRequest{
			CustomerID: w.customerID, Method: "CASH", Reference: "OTHER",
			IdempotencyKey: w.tag, InvoiceIDs: []string{a}, AmountCents: 40000}},
		{"the invoice list", ApplyRequest{
			CustomerID: w.customerID, Method: "CASH", Reference: "CHQ 771",
			IdempotencyKey: w.tag, InvoiceIDs: []string{b}, AmountCents: 40000}},
	}
	for _, tc := range different {
		t.Run("a different "+tc.name, func(t *testing.T) {
			ans, err := w.repo.ApplyReceived(ctx, tc.body)
			if err == nil {
				t.Fatalf("the same key with a different %s must be refused, got the answer %+v", tc.name, ans)
			}
			var bad httpx.BadRequestError
			if !errors.As(err, &bad) {
				t.Fatalf("the refusal is in words, got %T %v", err, err)
			}
			if !strings.Contains(bad.Msg, "that idempotency key already recorded a different payment") {
				t.Fatalf("the refusal says what happened: %q", bad.Msg)
			}
		})
	}

	// Nothing was written by any of the refused bodies: one payment, one allocation, one subledger
	// row, one event, and the second invoice untouched.
	payments, allocations, subledger, outbox := w.moneyRowCounts(t, w.customerID)
	if payments != 1 || allocations != 1 || subledger != 1 || outbox != 1 {
		t.Fatalf("only the first body's payment exists: payments %d allocations %d subledger %d outbox %d",
			payments, allocations, subledger, outbox)
	}
	if bal, _ := w.invoiceState(t, b); bal != 40000 {
		t.Fatalf("the refused list's invoice is untouched, balance %d", bal)
	}
}

// The control beside the refusals: the same body answers the first result whatever order the list
// arrives in, and an invoice the money never reached is still part of the body that posted, so the
// retry that names it again (as the desk's screen would) is the same payment.
func TestApplyReceived_TheSameBodyAnswersTheFirstResult(t *testing.T) {
	w := newApplyWorld(t)
	ctx := context.Background()
	old := w.invoice(t, w.customerID, "SA", 90, 60, 20000)
	newer := w.invoice(t, w.customerID, "SB", 30, 10, 5000)
	in := ApplyRequest{
		CustomerID: w.customerID, Method: "CHEQUE", Reference: "CHQ 313",
		IdempotencyKey: w.tag, InvoiceIDs: []string{old, newer}, AmountCents: 20000,
	}
	first, err := w.repo.ApplyReceived(ctx, in)
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if len(first.Lines) != 2 || first.Lines[1].Outcome != ApplyOutcomeNone {
		t.Fatalf("the youngest named invoice is reached by nothing: %v", first.Lines)
	}

	// The same body, list reversed: the list's order is free (ruling 73), so this is the same payment.
	reversed := in
	reversed.InvoiceIDs = []string{newer, old}
	again, err := w.repo.ApplyReceived(ctx, reversed)
	if err != nil || !again.Replayed || again.PaymentID != first.PaymentID {
		t.Fatalf("the same body in another order answers the first payment: %v %+v", err, again)
	}

	payments, allocations, subledger, outbox := w.moneyRowCounts(t, w.customerID)
	if payments != 1 || allocations != 1 || subledger != 1 || outbox != 1 {
		t.Fatalf("a same body retry posts nothing: payments %d allocations %d subledger %d outbox %d",
			payments, allocations, subledger, outbox)
	}
}
