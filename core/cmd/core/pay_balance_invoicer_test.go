package main

// pay_balance_invoicer_test.go pins the composition root's refusal mapping for
// the Pay balance take's raise (item 9 delta review P2-A): every refusal the
// billing writer can still return once the pre read has done its work crosses
// as the take door's own fixed 409 sentence, never a bare 500 (the order
// module's words are engineering vocabulary a cashier cannot act on) and never
// the ring's "sale" word (a payment is not a sale). The mapping lives here,
// between modules, because the fence keeps pos from importing its peers; this
// test is what keeps an errors.As arm from being dropped quietly.

import (
	"errors"
	"fmt"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
)

func TestPayBalanceRaiseRefusal_MapsEveryRefusalToTheDoorsOwnWords(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want error
	}{
		{"an order already invoiced another way", &billing.OrderAlreadyInvoicedError{InvoiceNo: "INV-7"}, pos.ErrPayBalanceRestUnbillable},
		{"the desk's conflict wrapper (cancelled, no lines, arithmetic)", quote.NewOrderPaymentConflict("order #SO-9 is cancelled and cannot be invoiced"), pos.ErrPayBalanceRaiseRefused},
		{"the order module's own refusal, unwrapped", &order.AgreedBillingRefusal{Msg: "order #SO-9 has no lines to invoice"}, pos.ErrPayBalanceRaiseRefused},
		{"nothing to invoice", billing.ErrNothingToInvoice, pos.ErrPayBalanceRaiseRefused},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := payBalanceRaiseRefusal(tc.err)
			if !errors.Is(got, tc.want) {
				t.Fatalf("the %s maps to %v, want the door's own %v", tc.name, got, tc.want)
			}
		})
	}
	if got := payBalanceRaiseRefusal(fmt.Errorf("billing: insert invoice: connection refused")); got != nil {
		t.Fatalf("an internal failure must cross untouched for the log, got %v", got)
	}
}
