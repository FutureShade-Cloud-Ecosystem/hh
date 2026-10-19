package payments

import (
	"context"
	"errors"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// settleRefusingStore refuses the first settle the way lockAndTotal does when an invoice balance
// moved between the charge and the settle (a counter payment landing at the same moment): the
// balance goes down and the settle answers a bad request or a not found, after the processor has
// already approved the charge. Later settles go through the embedded fake.
type settleRefusingStore struct {
	*fakeStore
	refuse    error
	invoiceID string
	refused   int
}

func (s *settleRefusingStore) PayWithIntent(ctx context.Context, customerID, method, gatewayRef string, allocs []Allocation, intentID string) (Payment, error) {
	if s.refuse != nil {
		err := s.refuse
		s.refuse = nil
		s.refused++
		inv := s.invoices[s.invoiceID]
		inv.balanceCents -= 1000 // the competing payment
		s.invoices[s.invoiceID] = inv
		return Payment{}, err
	}
	return s.fakeStore.PayWithIntent(ctx, customerID, method, gatewayRef, allocs, intentID)
}

func settleRefusedRig(t *testing.T, refuse error) (*settleRefusingStore, *spyGateway, *Service, PayRequest) {
	t.Helper()
	fake := newFakeStore()
	fake.seedInvoice("inv-1", tenantA, 5000, 0)
	store := &settleRefusingStore{fakeStore: fake, refuse: refuse, invoiceID: "inv-1"}
	gw := newSpyGateway()
	svc := NewService(store, gw)
	m, err := svc.AddMethod(context.Background(), tenantA, AddMethodRequest{Nonce: "n1"})
	if err != nil {
		t.Fatalf("add method: %v", err)
	}
	return store, gw, svc, PayRequest{InvoiceIDs: []string{"inv-1"}, PaymentMethodID: m.ID}
}

func assertInFlightNotBadRequest(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrPaymentInFlight) {
		t.Fatalf("a settle refused after an approved charge must be ErrPaymentInFlight (409), got %v", err)
	}
	var bre httpx.BadRequestError
	if errors.As(err, &bre) {
		t.Fatalf("must not carry a bad request (400) after an approved charge: %v", err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("must not carry a not found (404) after an approved charge: %v", err)
	}
}

func TestPay_SettleRefusedAfterApprovedCharge_BadRequestBecomesInFlight(t *testing.T) {
	store, gw, svc, req := settleRefusedRig(t, httpx.BadRequestf("invoice %q balance changed; refresh and retry", "inv-1"))
	ctx := context.Background()

	_, err := svc.Pay(ctx, tenantA, req)
	assertInFlightNotBadRequest(t, err)
	if gw.chargeCalls != 1 {
		t.Fatalf("one approved charge expected, got %d", gw.chargeCalls)
	}
	in := store.intents[idempotencyKey(tenantA, req)]
	if in == nil || in.Status != IntentCharging || in.GatewayRef == "" {
		t.Fatalf("the intent must stay CHARGING with the processor ref stamped: %+v", in)
	}
	stamped := in.GatewayRef

	// The competing payment is reversed: the invoice is whole again. The client resends the same
	// body, which hashes to the same key.
	inv := store.invoices["inv-1"]
	inv.balanceCents += 1000
	store.invoices["inv-1"] = inv

	p, err := svc.Pay(ctx, tenantA, req)
	if err != nil {
		t.Fatalf("the retry must resolve the held charge: %v", err)
	}
	if p.GatewayRef != stamped || p.AmountCents != 5000 {
		t.Fatalf("the retry must settle against the stamped ref %q for 5000, got %+v", stamped, p)
	}
	if gw.chargeCalls != 1 {
		t.Fatalf("the retry must never charge again, charge calls = %d", gw.chargeCalls)
	}
	if store.payCalls != 1 {
		t.Fatalf("exactly one payment row, got %d", store.payCalls)
	}
	if got := store.intents[idempotencyKey(tenantA, req)]; got.Status != IntentSettled {
		t.Fatalf("the intent must close SETTLED, got %s", got.Status)
	}

	// And a further resend replays the same payment.
	again, err := svc.Pay(ctx, tenantA, req)
	if err != nil || again.ID != p.ID || store.payCalls != 1 || gw.chargeCalls != 1 {
		t.Fatalf("a third press must replay: %+v err=%v pays=%d charges=%d", again, err, store.payCalls, gw.chargeCalls)
	}
}

func TestPay_SettleRefusedAfterApprovedCharge_NotFoundBecomesInFlight(t *testing.T) {
	store, gw, svc, req := settleRefusedRig(t, ErrNotFound)
	ctx := context.Background()

	_, err := svc.Pay(ctx, tenantA, req)
	assertInFlightNotBadRequest(t, err)

	inv := store.invoices["inv-1"]
	inv.balanceCents += 1000
	store.invoices["inv-1"] = inv

	if _, err := svc.Pay(ctx, tenantA, req); err != nil {
		t.Fatalf("the retry must resolve the held charge: %v", err)
	}
	if gw.chargeCalls != 1 || store.payCalls != 1 {
		t.Fatalf("one charge and one payment expected, got %d and %d", gw.chargeCalls, store.payCalls)
	}
}

// A balance that stays moved cannot take the money that was charged: the retry stays in flight,
// writes nothing and charges nothing, so a human reconciles against the stamped ref.
func TestPay_HeldChargeWhoseBalanceStaysMovedStaysInFlight(t *testing.T) {
	store, gw, svc, req := settleRefusedRig(t, httpx.BadRequestf("invoice %q balance changed; refresh and retry", "inv-1"))
	ctx := context.Background()

	if _, err := svc.Pay(ctx, tenantA, req); err == nil {
		t.Fatal("first press must be refused")
	}
	for i := 0; i < 2; i++ {
		_, err := svc.Pay(ctx, tenantA, req)
		assertInFlightNotBadRequest(t, err)
	}
	if gw.chargeCalls != 1 || store.payCalls != 0 {
		t.Fatalf("no second charge and no payment row: charges=%d payments=%d", gw.chargeCalls, store.payCalls)
	}
	if in := store.intents[idempotencyKey(tenantA, req)]; in.Status != IntentCharging {
		t.Fatalf("the intent stays CHARGING, got %s", in.Status)
	}
}

// A crash window with no stamped ref (transport lost the answer) still refuses and never resumes.
func TestPay_ChargingWithoutRefStaysInFlightAndNeverRecharges(t *testing.T) {
	store := newFakeStore()
	store.seedInvoice("inv-1", tenantA, 5000, 0)
	gw := newSpyGateway()
	svc := NewService(store, gw)
	ctx := context.Background()
	m, _ := svc.AddMethod(ctx, tenantA, AddMethodRequest{Nonce: "n1"})
	req := PayRequest{InvoiceIDs: []string{"inv-1"}, PaymentMethodID: m.ID}
	store.intents = map[string]*Intent{idempotencyKey(tenantA, req): {
		ID: "intent-x", Status: IntentCharging, AmountCents: 5000, CustomerID: tenantA,
	}}

	_, err := svc.Pay(ctx, tenantA, req)
	if !errors.Is(err, ErrPaymentInFlight) {
		t.Fatalf("want ErrPaymentInFlight, got %v", err)
	}
	if gw.chargeCalls != 0 || store.payCalls != 0 {
		t.Fatalf("nothing may move: charges=%d payments=%d", gw.chargeCalls, store.payCalls)
	}
}
