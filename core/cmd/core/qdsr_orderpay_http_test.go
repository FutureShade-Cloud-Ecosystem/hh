package main

// qdsr_orderpay_http_test.go, the order-payment door's wire legs for the fulfilment choice (Quote
// Desk server reads, read three): the field rides the request body, a value the plain convert
// refuses is a 400 in the convert's own words (the integration-tag file compares the two doors'
// messages) before the quote is looked up, a quote that does not
// exist is a 404, and a role without the Counter Till work grant meets the gate's 403. The database
// truth of what the order is created with is the integration-tag file beside this one.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestQDSROrderPaymentFulfilmentOverTheWire(t *testing.T) {
	fx := newXRoleFixture(t)
	path := "/api/v1/quotes/" + qdsrAbsentID + "/order-payment"
	body := func(fulfilment string) string {
		f := ""
		if fulfilment != "" {
			f = `,"fulfillment":"` + fulfilment + `"`
		}
		return `{"method":"CASH","amount_cents":1000,"reference":"","idempotency_key":"qdsr-wire-1"` + f + `}`
	}
	sales := testkit.SubInsideSales

	// A well-formed request for a quote nobody has: 404, with and without the new field.
	for _, f := range []string{"", "PICKUP", "WILL_CALL"} {
		code, out := qdsrSend(t, fx, sales, http.MethodPost, path, body(f))
		if code != http.StatusNotFound {
			t.Fatalf("fulfillment %q on an unknown quote answers %d %s, want 404", f, code, out)
		}
	}

	// A value the plain convert refuses is refused here before the quote is looked up, in words that
	// name the way forward.
	code, out := qdsrSend(t, fx, sales, http.MethodPost, path, body("DELIVERY"))
	if code != http.StatusBadRequest || !strings.Contains(out, "pickup or will-call") {
		t.Fatalf("DELIVERY answers %d %s, want the convert's 400 naming pickup or will-call", code, out)
	}
	code, out = qdsrSend(t, fx, sales, http.MethodPost, path, body("COURIER"))
	if code != http.StatusBadRequest || !strings.Contains(out, "fulfillment must be PICKUP or WILL_CALL") {
		t.Fatalf("COURIER answers %d %s, want the convert's 400 naming the two", code, out)
	}
	// A role without the Counter Till work grant never reaches the quote.
	for _, sub := range []string{xrSubYardTeam, testkit.SubDriver} {
		code, out = qdsrSend(t, fx, sub, http.MethodPost, path, body("WILL_CALL"))
		rgRefused(t, sub+" is held off the payment door", code, out)
	}
}
