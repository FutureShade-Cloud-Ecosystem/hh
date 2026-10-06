//go:build integration

// qdsr_orderpay_fulfilment_integration_test.go, the fulfilment choice on the order-payment door
// (Quote Desk server reads, read three), proven on real rows through the same environment the QD-5
// legs use. The door converts a cash customer's accepted quote in the same action that takes the
// payment (Cash On Demand: pay before release), and until now it took no fulfilment, so that order
// was always Pickup. It now takes the one the plain convert takes, validated by the same function.
//
//	Run: DATABASE_URL='postgres://.../scratch?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run QDSR -count=1 -p 1
package main

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
)

func (e *qdsrPayEnv) fulfilmentOf(orderID string) string {
	e.t.Helper()
	var f string
	if err := e.pool.QueryRow(context.Background(), `SELECT fulfillment FROM orders WHERE id = $1`, orderID).Scan(&f); err != nil {
		e.t.Fatalf("read fulfillment: %v", err)
	}
	return f
}

// qdsrPayEnv is the QD-5 environment under this file's name.
type qdsrPayEnv struct{ *qd5Env }

func qdsrPaySetup(t *testing.T) *qdsrPayEnv { return &qdsrPayEnv{qd5Setup(t)} }

func (e *qdsrPayEnv) payWith(q qd5Quote, fulfilment, key string) (quote.OrderPaymentResult, error) {
	in := pay("CASH", q.Total, key)
	in.Fulfillment = fulfilment
	return e.svc.TakeOrderPayment(e.counter(), q.QuoteID, in)
}

func TestQDSR_OrderPaymentWithNoFulfilmentIsPickupAsBefore(t *testing.T) {
	e := qdsrPaySetup(t)
	q := e.seedQuote(0, 0, qd5Trenton) // a customer with no credit: the one action converts, bills and pays
	res, err := e.payWith(q, "", "k-default")
	if err != nil {
		t.Fatalf("pay: %v", err)
	}
	if !res.Converted {
		t.Fatalf("the one action converted the quote: %+v", res)
	}
	if got := e.fulfilmentOf(res.OrderID); got != "PICKUP" {
		t.Fatalf("no fulfilment named, order is %q, want PICKUP (the default is unchanged)", got)
	}
}

func TestQDSR_OrderPaymentTakesWillCallAndPickup(t *testing.T) {
	for _, tc := range []struct{ send, want string }{
		{"WILL_CALL", "WILL_CALL"},
		{"will call", "WILL_CALL"}, // the plain convert forgives case and spacing, so does this
		{"will-call", "WILL_CALL"},
		{"PICKUP", "PICKUP"},
	} {
		t.Run(tc.send, func(t *testing.T) {
			e := qdsrPaySetup(t)
			q := e.seedQuote(0, 0, qd5Trenton)
			res, err := e.payWith(q, tc.send, "k-"+tc.want)
			if err != nil {
				t.Fatalf("pay: %v", err)
			}
			if got := e.fulfilmentOf(res.OrderID); got != tc.want {
				t.Fatalf("sent %q, order fulfilment is %q, want %q", tc.send, got, tc.want)
			}
			if c := e.counts(q); c.Orders != 1 || c.Invoices != 1 || c.Payments != 1 {
				t.Fatalf("the fulfilment choice must not change what the action writes: %+v", c)
			}
		})
	}
}

func TestQDSR_OrderPaymentRefusesWhatTheConvertRefusesInTheSameWords(t *testing.T) {
	for _, bad := range []string{"DELIVERY", "delivery", "COURIER", "TRUCK"} {
		t.Run(bad, func(t *testing.T) {
			e := qdsrPaySetup(t)
			q := e.seedQuote(0, 0, qd5Trenton)
			_, payErr := e.payWith(q, bad, "k-bad-"+bad)
			wantBadRequest(t, "order-payment with fulfillment "+bad, payErr)
			// the plain convert's refusal, asked of a quote it will reach the validator on
			other := e.seedQuote(1_000_000, 0, qd5Trenton)
			_, convErr := e.svc.ConvertQuote(e.counter(), other.QuoteID, quote.ConvertOptions{Fulfillment: bad})
			wantBadRequest(t, "plain convert with fulfillment "+bad, convErr)
			if payErr.Error() != convErr.Error() {
				t.Fatalf("the two doors must refuse in the same words:\n pay:     %s\n convert: %s", payErr, convErr)
			}
			if c := e.counts(q); c.Orders != 0 || c.Invoices != 0 || c.Payments != 0 {
				t.Fatalf("a refused fulfilment writes nothing: %+v", c)
			}
			if s := e.quoteStatus(q); s != "ACCEPTED" {
				t.Fatalf("the quote stays ACCEPTED after the refusal, is %s", s)
			}
		})
	}
}

func TestQDSR_OrderPaymentOnAnAlreadyConvertedOrderKeepsTheConversionsChoice(t *testing.T) {
	e := qdsrPaySetup(t)
	q := e.seedQuote(1_000_000, 0, qd5Trenton) // credit customer: converted first through the plain door
	res, err := e.svc.ConvertQuote(e.counter(), q.QuoteID, quote.ConvertOptions{Fulfillment: "WILL_CALL"})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	out, err := e.payWith(q, "PICKUP", "k-after-convert")
	if err != nil {
		t.Fatalf("pay: %v", err)
	}
	if out.OrderID != res.OrderID || out.Converted {
		t.Fatalf("the payment found the existing order: %+v", out)
	}
	if got := e.fulfilmentOf(res.OrderID); got != "WILL_CALL" {
		t.Fatalf("an order's fulfilment is chosen at its conversion; a later payment must not change it, is %q", got)
	}
}
