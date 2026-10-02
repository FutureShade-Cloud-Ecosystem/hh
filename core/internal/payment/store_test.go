package payment

import (
	"context"
	"errors"
	"testing"
)

// -- DB-free fakes (the internal/pricing service-test pattern) --------------

// fakeResolver implements configResolver (and the optional credsReencryptor) without a database, so
// the pure Charge gateway call is unit-testable. It hands Charge a FRESH copy of creds each call so
// in-place rotation by the processor is observable.
type fakeResolver struct {
	cfg   activeCfg
	creds map[string]string
	err   error

	calls         int
	reencryptCall int
	reencProc     string
	reencCreds    map[string]string
}

func (f *fakeResolver) activeConfig(_ context.Context) (activeCfg, map[string]string, error) {
	f.calls++
	if f.err != nil {
		return activeCfg{}, nil, f.err
	}
	cp := make(map[string]string, len(f.creds))
	for k, v := range f.creds {
		cp[k] = v
	}
	return f.cfg, cp, nil
}

func (f *fakeResolver) reencryptActiveCreds(_ context.Context, processor string, creds map[string]string) error {
	f.reencryptCall++
	f.reencProc = processor
	f.reencCreds = creds
	return nil
}

// spyProcessor records what Service.Charge dispatched and can simulate an api_key rotation.
type spyProcessor struct {
	res       ChargeResult
	err       error
	calls     int
	gotMID    string
	gotAmount int64
	gotToken  string
	mutateKey string // if set, simulates the live client rotating the api_key in-place
}

func (p *spyProcessor) Name() string { return "spy" }
func (p *spyProcessor) Charge(_ context.Context, creds map[string]string, mid string, amount int64, token string) (ChargeResult, error) {
	p.calls++
	p.gotMID, p.gotAmount, p.gotToken = mid, amount, token
	if p.mutateKey != "" {
		creds["api_key"] = p.mutateKey
	}
	return p.res, p.err
}
func (p *spyProcessor) Refund(context.Context, map[string]string, string, string, int64) (RefundResult, error) {
	return RefundResult{}, ErrNotImplemented
}

func validInput() ChargeInput {
	return ChargeInput{AmountCents: 12500, PaymentToken: "tok_runtest_4242", IdempotencyKey: "client-uuid", CardType: "CREDIT"}
}

// T12, Charge before any active config returns ErrNoConfig (US-RP01/02).
func TestService_Charge_NoConfig(t *testing.T) {
	svc := &Service{cfg: &fakeResolver{err: ErrNoConfig}, proc: RunPaymentsStub{}}
	_, err := svc.Charge(context.Background(), validInput())
	if !errors.Is(err, ErrNoConfig) {
		t.Fatalf("err = %v, want ErrNoConfig", err)
	}
}

// T13, configured (stub) → captured ChargeReceipt with the processor name + a non-empty ref (US-RP02).
func TestService_Charge_Captured_StubDefault(t *testing.T) {
	fr := &fakeResolver{
		cfg:   activeCfg{Processor: "run_payments", MerchantID: "MID-1", Currency: "CAD"},
		creds: map[string]string{"api_key": "tkn_ppc_test"},
	}
	svc := &Service{cfg: fr, proc: RunPaymentsStub{}}
	rcpt, err := svc.Charge(context.Background(), validInput())
	if err != nil {
		t.Fatalf("Charge: %v", err)
	}
	if rcpt.Processor != "run_payments" || rcpt.Status != "captured" || rcpt.ProcessorRef == "" {
		t.Fatalf("receipt = %+v, want run_payments/captured/non-empty ref", rcpt)
	}
}

// T14, the boundary test (unit form): Charge's ONLY side effects are reading payment_config (the
// resolver) and dispatching to the processor. Service holds no payments handle, so it cannot write a
// payments row (US-RP07, D-RUNPAY-1). The real row-count assertion is I2 (integration).
func TestService_Charge_WritesNoPaymentsRow(t *testing.T) {
	fr := &fakeResolver{
		cfg:   activeCfg{Processor: "run_payments", MerchantID: "MID-1"},
		creds: map[string]string{"api_key": "k"},
	}
	sp := &spyProcessor{res: ChargeResult{ProcessorRef: "ref", Status: "captured"}}
	svc := &Service{cfg: fr, proc: sp}
	if _, err := svc.Charge(context.Background(), validInput()); err != nil {
		t.Fatalf("Charge: %v", err)
	}
	if fr.calls != 1 {
		t.Fatalf("activeConfig calls = %d, want exactly 1 (read payment_config once)", fr.calls)
	}
	if sp.calls != 1 {
		t.Fatalf("processor calls = %d, want exactly 1", sp.calls)
	}
	if sp.gotAmount != 12500 || sp.gotToken != "tok_runtest_4242" || sp.gotMID != "MID-1" {
		t.Fatalf("processor dispatched wrong args: %+v", sp)
	}
	// No re-encrypt happened (api_key unchanged), Charge wrote nothing at all.
	if fr.reencryptCall != 0 {
		t.Fatalf("no credential re-encrypt expected, got %d", fr.reencryptCall)
	}
}

// An ill-formed charge fails BEFORE any processor dispatch (no silent capture; US-RP02).
func TestService_Charge_BadInput_NoDispatch(t *testing.T) {
	fr := &fakeResolver{cfg: activeCfg{Processor: "run_payments", MerchantID: "MID-1"}, creds: map[string]string{"api_key": "k"}}
	sp := &spyProcessor{res: ChargeResult{Status: "captured"}}
	svc := &Service{cfg: fr, proc: sp}
	for _, in := range []ChargeInput{
		{AmountCents: 12500, PaymentToken: ""},
		{AmountCents: 0, PaymentToken: "tok"},
	} {
		if _, err := svc.Charge(context.Background(), in); !errors.Is(err, errBadCharge) {
			t.Fatalf("err = %v, want errBadCharge", err)
		}
	}
	if sp.calls != 0 {
		t.Fatalf("processor must NOT be dispatched for ill-formed input, got %d calls", sp.calls)
	}
}

// A processor error surfaces with Status="failed" (US-RP03 the till must not record a RECEIVED row).
func TestService_Charge_ProcessorError_Failed(t *testing.T) {
	fr := &fakeResolver{cfg: activeCfg{Processor: "run_payments", MerchantID: "MID-1"}, creds: map[string]string{"api_key": "k"}}
	sp := &spyProcessor{res: ChargeResult{Status: "failed"}, err: errors.New("declined")}
	svc := &Service{cfg: fr, proc: sp}
	rcpt, err := svc.Charge(context.Background(), validInput())
	if err == nil {
		t.Fatal("a processor error must propagate")
	}
	if rcpt.Status != "failed" {
		t.Fatalf("status = %q, want failed", rcpt.Status)
	}
}

// When the live client rotates an expired api_key, Service re-encrypts the rotated creds into the
// active payment_config row (never the payments table), ARCH_DELTA §2.11.
func TestService_Charge_ReencryptsRotatedCreds(t *testing.T) {
	fr := &fakeResolver{
		cfg:   activeCfg{Processor: "run_payments", MerchantID: "MID-1"},
		creds: map[string]string{"api_key": "expired"},
	}
	sp := &spyProcessor{res: ChargeResult{ProcessorRef: "ref", Status: "captured"}, mutateKey: "fresh"}
	svc := &Service{cfg: fr, proc: sp}
	if _, err := svc.Charge(context.Background(), validInput()); err != nil {
		t.Fatalf("Charge: %v", err)
	}
	if fr.reencryptCall != 1 {
		t.Fatalf("re-encrypt calls = %d, want 1 after rotation", fr.reencryptCall)
	}
	if fr.reencProc != "run_payments" {
		t.Fatalf("re-encrypt processor = %q", fr.reencProc)
	}
	if fr.reencCreds["api_key"] != "fresh" {
		t.Fatalf("re-encrypted api_key = %q, want fresh", fr.reencCreds["api_key"])
	}
}

// HH-186, Store.Configure refuses surcharge_enabled=true before touching the pool or the vault:
// the row can never say enabled while no tender path computes a surcharge, and D18 requires a
// recorded jurisdiction and cap before enabling (none is). Every caller gets the same answer,
// not just the HTTP route.
func TestStore_Configure_RefusesSurchargeEnable(t *testing.T) {
	st := NewStore(nil, nil)
	err := st.Configure(context.Background(), "run_payments", "MID-1", "CAD", "", 240, true, map[string]string{"api_key": "k"}, "")
	if !errors.Is(err, ErrSurchargeNotEnforced) {
		t.Fatalf("Configure err = %v, want ErrSurchargeNotEnforced", err)
	}
}

// credentialKeys strips the injected non-secret keys so they never reach the ciphertext.
func TestCredentialKeys_StripsInjected(t *testing.T) {
	got := credentialKeys(map[string]string{
		"api_key": "k", "refresh_token": "rt",
		"__idem": "uuid", "base_url": "https://x", "currency": "CAD",
	})
	for _, k := range []string{"__idem", "base_url", "currency"} {
		if _, ok := got[k]; ok {
			t.Fatalf("injected key %q must be stripped before encryption", k)
		}
	}
	if got["api_key"] != "k" || got["refresh_token"] != "rt" {
		t.Fatalf("real credentials must be preserved, got %+v", got)
	}
}
