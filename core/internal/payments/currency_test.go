package payments

import (
	"context"
	"errors"
	"testing"
)

// The portal rail charges the dealer's own currency (item 3b slice 2). A dealer with no currency
// source wired is the Ontario dealer: CAD, exactly as before.

type reqGateway struct {
	*spyGateway
	reqs []ChargeRequest
}

func newReqGateway() *reqGateway { return &reqGateway{spyGateway: newSpyGateway()} }

func (g *reqGateway) Charge(ctx context.Context, req ChargeRequest) (ChargeResult, error) {
	g.reqs = append(g.reqs, req)
	return g.spyGateway.Charge(ctx, req)
}

type fixedCurrency struct {
	code  string
	err   error
	reads int
}

func (f *fixedCurrency) DealerCurrency(context.Context) (string, error) {
	f.reads++
	return f.code, f.err
}

func payOnce(t *testing.T, svc *Service, store *fakeStore) (Payment, error) {
	t.Helper()
	ctx := context.Background()
	m, err := svc.AddMethod(ctx, tenantA, AddMethodRequest{Nonce: "n1"})
	if err != nil {
		t.Fatal(err)
	}
	return svc.Pay(ctx, tenantA, PayRequest{InvoiceIDs: []string{"inv-1"}, PaymentMethodID: m.ID})
}

func TestPay_ChargesTheDealersCurrency(t *testing.T) {
	store := newFakeStore()
	store.seedInvoice("inv-1", tenantA, 5000, 0)
	gw := newReqGateway()
	svc := NewService(store, gw).WithCurrency(&fixedCurrency{code: "USD"})
	if _, err := payOnce(t, svc, store); err != nil {
		t.Fatalf("pay: %v", err)
	}
	if len(gw.reqs) != 1 || gw.reqs[0].Currency != "USD" {
		t.Fatalf("charge requests = %+v, want one in USD", gw.reqs)
	}
}

func TestPay_NoCurrencySourceChargesCAD(t *testing.T) {
	store := newFakeStore()
	store.seedInvoice("inv-1", tenantA, 5000, 0)
	gw := newReqGateway()
	if _, err := payOnce(t, NewService(store, gw), store); err != nil {
		t.Fatalf("pay: %v", err)
	}
	if len(gw.reqs) != 1 || gw.reqs[0].Currency != "CAD" {
		t.Fatalf("charge requests = %+v, want one in CAD (the Ontario dealer)", gw.reqs)
	}
}

func TestPay_CurrencyReadFailureRefusesBeforeAnyIntentOrCharge(t *testing.T) {
	store := newFakeStore()
	store.seedInvoice("inv-1", tenantA, 5000, 0)
	gw := newReqGateway()
	boom := errors.New("settings unavailable")
	svc := NewService(store, gw).WithCurrency(&fixedCurrency{err: boom})
	if _, err := payOnce(t, svc, store); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the read failure surfaced", err)
	}
	if len(gw.reqs) != 0 || len(store.intents) != 0 || store.payCalls != 0 {
		t.Fatalf("a failed currency read must claim no intent and move no money: reqs=%d intents=%d pays=%d",
			len(gw.reqs), len(store.intents), store.payCalls)
	}
}

// A replay after the dealer's currency changed answers the stored payment: it never re-reads the
// currency and never re-sends a charge under the new one.
func TestPay_ReplayAfterACurrencyChangeAnswersTheStoredPayment(t *testing.T) {
	store := newFakeStore()
	store.seedInvoice("inv-1", tenantA, 5000, 0)
	gw := newReqGateway()
	cur := &fixedCurrency{code: "CAD"}
	svc := NewService(store, gw).WithCurrency(cur)
	ctx := context.Background()
	m, err := svc.AddMethod(ctx, tenantA, AddMethodRequest{Nonce: "n1"})
	if err != nil {
		t.Fatal(err)
	}
	req := PayRequest{InvoiceIDs: []string{"inv-1"}, PaymentMethodID: m.ID}
	first, err := svc.Pay(ctx, tenantA, req)
	if err != nil {
		t.Fatalf("first pay: %v", err)
	}
	cur.code = "USD"
	readsBefore := cur.reads
	again, err := svc.Pay(ctx, tenantA, req)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if again.ID != first.ID || again.GatewayRef != first.GatewayRef {
		t.Fatalf("replay = %+v, want the stored payment %+v", again, first)
	}
	if len(gw.reqs) != 1 || gw.reqs[0].Currency != "CAD" {
		t.Fatalf("charge requests = %+v, want the one original CAD charge", gw.reqs)
	}
	if cur.reads != readsBefore {
		t.Fatalf("a replay read the currency %d more time(s); it must answer from the intent", cur.reads-readsBefore)
	}
}
