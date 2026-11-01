// pay_shown_guard_test.go, row 0.51: the payment service holds the shown amount itself.
//
// The two doors (the portal route and the sync create) already refuse a missing expected_cents in words.
// This leg calls the service directly, the way a third door that forgot the check would, and pins that the
// service refuses too, before any quote, claim or charge.
package payments

import (
	"context"
	"errors"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

func TestPay_TheServiceRefusesAMissingShownAmountItself(t *testing.T) {
	for name, expected := range map[string]int64{"omitted": 0, "negative": -5} {
		t.Run(name, func(t *testing.T) {
			store := newFakeStore()
			store.seedInvoice("inv-1", tenantA, 49000, 0)
			gw := newSpyGateway()
			svc := NewService(store, gw)
			ctx := context.Background()
			m, _ := svc.AddMethod(ctx, tenantA, AddMethodRequest{Nonce: "n1"})

			_, err := svc.Pay(ctx, tenantA, PayRequest{InvoiceIDs: []string{"inv-1"}, PaymentMethodID: m.ID, ExpectedCents: expected})
			var bre httpx.BadRequestError
			if !errors.As(err, &bre) {
				t.Errorf("a direct call without a positive expected_cents must be a bad request; got %v", err)
			}
			if n := gw.chargeCalls; n != 0 {
				t.Errorf("nothing is charged on a refused call; the gateway saw %d", n)
			}
			if store.payCalls != 0 {
				t.Errorf("nothing is settled on a refused call; saw %d", store.payCalls)
			}
		})
	}
}
