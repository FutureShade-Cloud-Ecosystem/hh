package portal

// dib483_convert_test.go, the CONTRACTOR half of the DIB-483 evidence.
//
// M3-G7B: the walk found that a contractor refused on credit was told "invalid state transition". The
// mechanism is subtle and worth restating where the fix lives: quote.ErrCreditBlocked deliberately
// Unwrap()s to quote.ErrInvalidTransition, because that is how it earns a 409 instead of a 500. The
// consequence is that writeErr's generic `errors.Is(err, quote.ErrInvalidTransition)` case MATCHES a
// credit block, so whichever case is listed first wins, and for the whole of M3 that was the generic
// one. The fix is one reordered case; these tests are what keep it reordered.
//
// M3-G12: the same file pins the convert extension's compatibility promise, a body-less POST, which
// is what every shipped contractor client sends, must reach the service with NO fulfilment chosen.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
)

// dib483Blocked is the walk's repro: $112,200.00 of hardscape against a $50,000.00 limit, annotated
// exactly as quote.checkCredit annotates it.
func dib483Blocked() quote.ErrCreditBlocked {
	return quote.ErrCreditBlocked{Blocked: &identity.ActionBlocked{
		Policy: identity.PolicyCredit,
		Detail: "order would put the balance 6220000 cents over the 5000000 cents limit",
		Data: map[string]string{
			"order_total":  "$112,200.00",
			"credit_limit": "$50,000.00",
			"open_balance": "$0.00",
		},
	}}
}

// TestConvertQuote_CreditRefusalCarriesItsReason is the M3-G7B acceptance at the contractor door.
//
// It asserts on WORDS. A 409 was always correct; a 409 that says "invalid state transition" to a
// customer who is over their credit limit is the defect.
func TestConvertQuote_CreditRefusalCarriesItsReason(t *testing.T) {
	store := &fakeQuoteStore{convertErr: dib483Blocked()}
	h := NewHandler(nil, nil).WithQuotes(quote.NewService(store))

	r := reqWithCaller(http.MethodPost, "/api/portal/v1/quotes/q-1/convert", "", tenantB)
	r.SetPathValue("id", "q-1")
	w := httptest.NewRecorder()
	h.convertQuote(w, r)

	if w.Code != http.StatusConflict {
		t.Fatalf("credit refusal = %d, want 409 (unchanged, this fix must not move the status)", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "invalid state transition") {
		t.Fatalf("REGRESSION: the credit block fell through to the generic transition case. The "+
			"errors.As(&quote.ErrCreditBlocked{}) case must stay ABOVE the errors.Is(ErrInvalidTransition) "+
			"case in writeErr. body: %s", body)
	}
	for _, want := range []string{"$112,200.00", "$50,000.00", "credit limit", "no order was created"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the refusal does not carry %q, the reason was dropped on the way out. body: %s", want, body)
		}
	}
}

// TestConvertQuote_PlainInvalidTransitionStillGeneric is the other side of the ordering. A quote that
// really is in the wrong state (already CONVERTED, still DRAFT) must keep the generic message, the
// fix narrows one case, it does not relabel every 409 as a credit problem.
func TestConvertQuote_PlainInvalidTransitionStillGeneric(t *testing.T) {
	store := &fakeQuoteStore{convertErr: quote.ErrInvalidTransition}
	h := NewHandler(nil, nil).WithQuotes(quote.NewService(store))

	r := reqWithCaller(http.MethodPost, "/api/portal/v1/quotes/q-1/convert", "", tenantB)
	r.SetPathValue("id", "q-1")
	w := httptest.NewRecorder()
	h.convertQuote(w, r)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	if !strings.Contains(w.Body.String(), "invalid state transition") {
		t.Fatalf("a genuine state error lost its message: %s", w.Body.String())
	}
}

// TestConvertQuote_CreditBlockIsNotA500 pins the property the Unwrap exists to provide. If someone
// "cleans up" ErrCreditBlocked by dropping Unwrap, the errors.As case still catches it here, but the
// sentinel relationship other consumers rely on would be gone, so assert it directly too.
func TestConvertQuote_CreditBlockIsNotA500(t *testing.T) {
	if !errors.Is(dib483Blocked(), quote.ErrInvalidTransition) {
		t.Fatal("quote.ErrCreditBlocked no longer unwraps to ErrInvalidTransition")
	}
}

// -- M3-G12: the convert extension is backwards compatible ----------------------------------------

// fulfillmentSpyStore records the ConvertOptions the handler passed down, so the compatibility
// promise can be asserted on the actual value rather than on the absence of a crash.
type fulfillmentSpyStore struct {
	fakeQuoteStore
	got   quote.ConvertOptions
	calls int
}

func (f *fulfillmentSpyStore) Convert(_ context.Context, customerID, _ string, opt quote.ConvertOptions) (quote.ConvertResult, error) {
	f.gotCustomerID = customerID
	f.got = opt
	f.calls++
	return quote.ConvertResult{OrderID: "o-1", OrderNo: "#SO-TRT-1041"}, nil
}

// TestConvertQuote_NoBodyMeansNoFulfillmentChosen is the byte-identical promise as the WIRE sees it.
// Every contractor client shipped to date POSTs this route with no body at all; after DIB-483 they
// must still convert, and must still convert to the database's default.
func TestConvertQuote_NoBodyMeansNoFulfillmentChosen(t *testing.T) {
	store := &fulfillmentSpyStore{}
	h := NewHandler(nil, nil).WithQuotes(quote.NewService(store))

	r := reqWithCaller(http.MethodPost, "/api/portal/v1/quotes/q-1/convert", "", tenantB)
	r.SetPathValue("id", "q-1")
	w := httptest.NewRecorder()
	h.convertQuote(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("body-less convert = %d (%s), want 200, the extension broke an existing caller",
			w.Code, w.Body.String())
	}
	if store.calls != 1 {
		t.Fatalf("store called %d times, want 1", store.calls)
	}
	if store.got.Fulfillment != "" {
		t.Fatalf("a body-less convert chose fulfilment %q, it must choose NOTHING so the column "+
			"default applies", store.got.Fulfillment)
	}
}

// TestConvertQuote_WillCallBodyReachesTheService: the extension actually does something when used.
func TestConvertQuote_WillCallBodyReachesTheService(t *testing.T) {
	store := &fulfillmentSpyStore{}
	h := NewHandler(nil, nil).WithQuotes(quote.NewService(store))

	r := reqWithCaller(http.MethodPost, "/api/portal/v1/quotes/q-1/convert",
		`{"fulfillment":"WILL_CALL"}`, tenantB)
	r.SetPathValue("id", "q-1")
	w := httptest.NewRecorder()
	h.convertQuote(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("will-call convert = %d (%s), want 200", w.Code, w.Body.String())
	}
	if store.got.Fulfillment != quote.FulfillWillCall {
		t.Fatalf("service received fulfilment %q, want %q", store.got.Fulfillment, quote.FulfillWillCall)
	}
}

// TestConvertQuote_BadFulfillmentIs400, a nonsense value is refused in plain words, before any write.
func TestConvertQuote_BadFulfillmentIs400(t *testing.T) {
	store := &fulfillmentSpyStore{}
	h := NewHandler(nil, nil).WithQuotes(quote.NewService(store))

	r := reqWithCaller(http.MethodPost, "/api/portal/v1/quotes/q-1/convert",
		`{"fulfillment":"TELEPORT"}`, tenantB)
	r.SetPathValue("id", "q-1")
	w := httptest.NewRecorder()
	h.convertQuote(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad fulfilment = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "WILL_CALL") {
		t.Fatalf("the 400 does not name the legal answers: %s", w.Body.String())
	}
	if store.calls != 0 {
		t.Fatal("a refused fulfilment still reached the store")
	}
}
