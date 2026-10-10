//go:build integration

// received_apply_spelling_integration_test.go, ONE INVOICE IS ONE INVOICE WHATEVER ITS SPELLING.
// uuid.Parse accepts upper case, braces, the urn prefix and the undashed form, and Postgres folds
// them all onto the same row, so a list naming one invoice twice in two spellings used to double
// the walk: a preview promised a split that paid a 10000 invoice 20000, and the apply surfaced the
// allocation primary key violation as a raw 500. The ids are normalised once at the entry, so the
// duplicate is refused in words in BOTH the preview and the apply, and the two always agree.
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

// One invoice named twice, in any pair of spellings, is refused in words by the preview and by the
// apply alike, nothing is applied, and the answer is never a raw 500.
func TestApplyReceived_AnInvoiceNamedTwiceInAnySpellingIsRefusedInWords(t *testing.T) {
	w := newApplyWorld(t)
	ctx := context.Background()
	inv := w.invoice(t, w.customerID, "TW", 90, 60, 10000)

	spellings := map[string]string{
		"the canonical spelling again": inv,
		"upper case":                   strings.ToUpper(inv),
		"braces":                       "{" + inv + "}",
		"the urn prefix":               "urn:uuid:" + inv,
		"undashed":                     strings.ReplaceAll(inv, "-", ""),
		"surrounding whitespace":       "  " + inv + " ",
	}
	for name, second := range spellings {
		for _, preview := range []bool{false, true} {
			shape := "apply"
			if preview {
				shape = "preview"
			}
			t.Run(name+" in a "+shape, func(t *testing.T) {
				ans, err := w.repo.ApplyReceived(ctx, ApplyRequest{
					CustomerID: w.customerID, Method: "CASH",
					IdempotencyKey: w.tag + "-" + shape + "-" + strings.ToLower(strings.ReplaceAll(name, " ", "-")),
					InvoiceIDs:     []string{inv, second}, AmountCents: 15000, Preview: preview,
				})
				if err == nil {
					t.Fatalf("one invoice named twice must be refused, got %+v", ans)
				}
				var bad httpx.BadRequestError
				if !errors.As(err, &bad) {
					t.Fatalf("the refusal is in words, never a raw error, got %T %v", err, err)
				}
				if !strings.Contains(bad.Msg, "the same invoice is named twice") {
					t.Fatalf("the refusal names the duplicate: %q", bad.Msg)
				}
			})
		}
	}
	payments, allocations, subledger, outbox := w.moneyRowCounts(t, w.customerID)
	if payments != 0 || allocations != 0 || subledger != 0 || outbox != 0 {
		t.Fatalf("nothing applied by any spelling refusal: %d %d %d %d", payments, allocations, subledger, outbox)
	}
	if b, _ := w.invoiceState(t, inv); b != 10000 {
		t.Fatalf("the invoice is untouched, balance %d", b)
	}
}

// The account id is validated and canonicalised exactly like the invoice ids (the delta reviews'
// P3): a malformed customer_id used to reach the lock statement's uuid cast and surface the 22P02
// as the route's raw 500, and an upper case spelling posted a payment whose exact retry was then
// refused as another account's, because the stored id is lower case and was compared raw. The id is
// parsed and re-spelled at the entry with the invoice ids, so a non uuid is refused in words in
// both the preview and the apply, and every spelling of one account id behaves exactly like the
// canonical one, its own replay included.
func TestApplyReceived_TheCustomerIDIsValidatedAndCanonicalisedLikeTheInvoiceIDs(t *testing.T) {
	w := newApplyWorld(t)
	ctx := context.Background()
	inv := w.invoice(t, w.customerID, "CID", 90, 60, 10000)

	for _, preview := range []bool{false, true} {
		shape := "apply"
		if preview {
			shape = "preview"
		}
		t.Run("a malformed customer id in a "+shape, func(t *testing.T) {
			_, err := w.repo.ApplyReceived(ctx, ApplyRequest{
				CustomerID: "not-a-uuid", Method: "CASH",
				IdempotencyKey: w.tag + "-bad-" + shape, InvoiceIDs: []string{inv}, AmountCents: 100, Preview: preview,
			})
			var bad httpx.BadRequestError
			if !errors.As(err, &bad) {
				t.Fatalf("a malformed customer id is refused in words, never a raw 500, got %T %v", err, err)
			}
			if !strings.Contains(bad.Msg, "customer id must be a uuid") {
				t.Fatalf("the refusal names the customer id: %q", bad.Msg)
			}
		})
	}
	if payments, _, _, _ := w.moneyRowCounts(t, w.customerID); payments != 0 {
		t.Fatalf("a malformed customer id writes nothing, found %d payment rows", payments)
	}

	first, err := w.repo.ApplyReceived(ctx, ApplyRequest{
		CustomerID: strings.ToUpper(w.customerID), Method: "CASH",
		IdempotencyKey: w.tag + "-upper", InvoiceIDs: []string{inv}, AmountCents: 100,
	})
	if err != nil || first.Replayed || first.PaymentID == "" {
		t.Fatalf("an upper case customer id posts like the canonical one: %v %+v", err, first)
	}
	retry, err := w.repo.ApplyReceived(ctx, ApplyRequest{
		CustomerID: w.customerID, Method: "CASH",
		IdempotencyKey: w.tag + "-upper", InvoiceIDs: []string{inv}, AmountCents: 100,
	})
	if err != nil || !retry.Replayed || retry.PaymentID != first.PaymentID {
		t.Fatalf("the exact retry under the same key answers the first payment whatever the spelling: %v %+v", err, retry)
	}
	if payments, _, _, _ := w.moneyRowCounts(t, w.customerID); payments != 1 {
		t.Fatalf("one payment row for the upper case id and its retry, found %d", payments)
	}
}
