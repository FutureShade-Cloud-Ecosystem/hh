package payment

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
)

// The POS card rail charges only in the currency the dealer trades in (item 3b slice 2). The merchant
// account's row currency and the dealer's can disagree; a charge then refuses before anything is sent,
// and a refund or void, which reverse the original capture, never does.

type fakeDealerCurrency struct {
	mu    sync.Mutex
	code  string
	err   error
	poolN int
	txN   int
}

func (f *fakeDealerCurrency) DealerCurrency(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.poolN++
	return f.code, f.err
}

func (f *fakeDealerCurrency) DealerCurrencyTx(context.Context, pgx.Tx) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.txN++
	return f.code, f.err
}

func currencySvc(rowCurrency string, src *fakeDealerCurrency) (*Service, *spyProcessor) {
	fr := &fakeResolver{
		cfg:   activeCfg{Processor: "run_payments", MerchantID: "MID-1", Currency: rowCurrency},
		creds: map[string]string{"api_key": "k"},
	}
	sp := &spyProcessor{res: ChargeResult{ProcessorRef: "ref", Status: "captured"}}
	svc := &Service{cfg: fr, proc: sp}
	if src != nil { // a typed nil pointer would be a non nil interface
		svc.currency = src
	}
	return svc, sp
}

func TestCharge_RefusesWhenTheRailAndTheDealerDisagree(t *testing.T) {
	svc, sp := currencySvc("CAD", &fakeDealerCurrency{code: "USD"})
	_, err := svc.Charge(context.Background(), validInput())
	if !errors.Is(err, ErrProcessorNotDispatched) {
		t.Fatalf("err = %v, want a not dispatched refusal", err)
	}
	if !strings.Contains(err.Error(), "CAD") || !strings.Contains(err.Error(), "USD") {
		t.Fatalf("err = %q, want both currency codes named", err)
	}
	if sp.calls != 0 {
		t.Fatalf("the processor was called %d time(s); a mismatch must send nothing", sp.calls)
	}
}

func TestCharge_AgreeingCurrencyCharges(t *testing.T) {
	src := &fakeDealerCurrency{code: "USD"}
	svc, sp := currencySvc("USD", src)
	if _, err := svc.Charge(context.Background(), validInput()); err != nil {
		t.Fatalf("Charge: %v", err)
	}
	if sp.calls != 1 || src.poolN != 1 || src.txN != 0 {
		t.Fatalf("calls=%d poolReads=%d txReads=%d, want one charge on one pool read", sp.calls, src.poolN, src.txN)
	}
}

func TestChargeTx_ReadsTheDealerCurrencyOnTheTransaction(t *testing.T) {
	src := &fakeDealerCurrency{code: "USD"}
	svc, sp := currencySvc("USD", src)
	if _, err := svc.ChargeTx(context.Background(), nil, validInput()); err != nil {
		t.Fatalf("ChargeTx: %v", err)
	}
	if sp.calls != 1 || src.txN != 1 || src.poolN != 0 {
		t.Fatalf("calls=%d txReads=%d poolReads=%d, want the read on the transaction and none on the pool", sp.calls, src.txN, src.poolN)
	}
	svc2, sp2 := currencySvc("CAD", &fakeDealerCurrency{code: "USD"})
	if _, err := svc2.ChargeTx(context.Background(), nil, validInput()); !errors.Is(err, ErrProcessorNotDispatched) || sp2.calls != 0 {
		t.Fatalf("a mismatch inside the transaction: err=%v calls=%d, want a refusal that sent nothing", err, sp2.calls)
	}
}

func TestCharge_AnUnreadableDealerCurrencyRefusesNotDispatched(t *testing.T) {
	svc, sp := currencySvc("CAD", &fakeDealerCurrency{err: errors.New("settings unavailable")})
	if _, err := svc.Charge(context.Background(), validInput()); !errors.Is(err, ErrProcessorNotDispatched) || sp.calls != 0 {
		t.Fatalf("err=%v calls=%d, want a refusal that sent nothing", err, sp.calls)
	}
}

func TestCharge_NoDealerSourceKeepsTheRowCurrency(t *testing.T) {
	svc, sp := currencySvc("CAD", nil)
	if _, err := svc.Charge(context.Background(), validInput()); err != nil || sp.calls != 1 {
		t.Fatalf("err=%v calls=%d, want the scaffold composition to charge as before", err, sp.calls)
	}
}

// A refund reverses the original capture in the currency it was captured in; a rail/dealer mismatch
// must never trap money, so the refund is not guarded and never reads the dealer's currency.
func TestRefund_IsNotGuardedByTheDealerCurrency(t *testing.T) {
	src := &fakeDealerCurrency{code: "USD"}
	svc, _ := currencySvc("CAD", src)
	sp := &spyRefunder{res: RefundResult{ProcessorRef: "rp-1", Status: "refunded"}}
	svc.proc = sp
	if _, err := svc.Refund(context.Background(), RefundInput{ProcessorRef: "ref-1", AmountCents: 500, IdempotencyKey: "k"}); err != nil {
		t.Fatalf("Refund: %v", err)
	}
	if src.poolN != 0 || src.txN != 0 {
		t.Fatalf("a refund read the dealer currency (%d/%d); it must not", src.poolN, src.txN)
	}
}
