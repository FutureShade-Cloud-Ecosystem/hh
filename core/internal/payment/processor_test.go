package payment

import (
	"context"
	"strings"
	"testing"
)

// goodCreds is the minimal verified-key set the stub/live guards require.
func goodCreds() map[string]string { return map[string]string{"api_key": "tkn_ppc_test"} }

// T3, a valid stub charge captures with a non-empty ProcessorRef (US-RP02).
func TestStub_Charge_Captured(t *testing.T) {
	res, err := RunPaymentsStub{}.Charge(context.Background(), goodCreds(), "MID-1", 12500, "tok_runtest_4242")
	if err != nil {
		t.Fatalf("Charge: %v", err)
	}
	if res.Status != "captured" {
		t.Fatalf("status = %q, want captured", res.Status)
	}
	if res.ProcessorRef == "" {
		t.Fatal("ProcessorRef must be non-empty")
	}
}

// T4, the stub is deterministic: the same idempotency key yields the same ProcessorRef across two
// charges (US-RP02 replay-safe). Service injects the key via creds["__idem"].
func TestStub_Charge_Deterministic(t *testing.T) {
	mk := func() map[string]string { c := goodCreds(); c["__idem"] = "client-uuid-abc"; return c }
	a, err := RunPaymentsStub{}.Charge(context.Background(), mk(), "MID-1", 12500, "tok_x")
	if err != nil {
		t.Fatalf("first charge: %v", err)
	}
	b, err := RunPaymentsStub{}.Charge(context.Background(), mk(), "MID-1", 12500, "tok_x")
	if err != nil {
		t.Fatalf("second charge: %v", err)
	}
	if a.ProcessorRef != b.ProcessorRef {
		t.Fatalf("non-deterministic ref: %q != %q", a.ProcessorRef, b.ProcessorRef)
	}
	// A different key must change the ref (it is derived from the key, not constant).
	c := goodCreds()
	c["__idem"] = "client-uuid-different"
	d, _ := RunPaymentsStub{}.Charge(context.Background(), c, "MID-1", 12500, "tok_x")
	if d.ProcessorRef == a.ProcessorRef {
		t.Fatal("a different idempotency key must yield a different ProcessorRef")
	}
}

// T5, the stub refuses ill-formed charges (no silent capture): empty token, non-positive amount,
// missing merchant id, missing creds (US-RP02).
func TestStub_Charge_RejectsIllFormed(t *testing.T) {
	cases := []struct {
		name   string
		creds  map[string]string
		mid    string
		amount int64
		token  string
	}{
		{"empty token", goodCreds(), "MID-1", 12500, ""},
		{"zero amount", goodCreds(), "MID-1", 0, "tok_x"},
		{"negative amount", goodCreds(), "MID-1", -1, "tok_x"},
		{"missing merchant", goodCreds(), "", 12500, "tok_x"},
		{"missing creds", map[string]string{}, "MID-1", 12500, "tok_x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := RunPaymentsStub{}.Charge(context.Background(), tc.creds, tc.mid, tc.amount, tc.token)
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if res.Status == "captured" {
				t.Fatal("an ill-formed charge must NOT be captured")
			}
		})
	}
}

// T6, compile-time assertion that the only instrument argument is a token string (US-RP04). There
// is NO PAN parameter anywhere in the seam; if the signature changed this would not compile.
func TestStub_Charge_NeverHasPANParam(t *testing.T) {
	var _ func(context.Context, map[string]string, string, int64, string) (ChargeResult, error) = RunPaymentsStub{}.Charge
	var _ func(context.Context, map[string]string, string, int64, string) (ChargeResult, error) = NewRunPaymentsClient("").Charge
}

// T26, both processors satisfy Processor INCLUDING Refund (the seam is part of the contract, US-RP10).
func TestProcessor_InterfaceIncludesRefund(t *testing.T) {
	var _ Processor = RunPaymentsStub{}
	var _ Processor = NewRunPaymentsClient("https://staging-javelin.runpayments.io")
}

// T27 (stub half), W5/A-05: the stub's Refund now reverses DETERMINISTICALLY (mirrors the verified
// void-or-refund A-outcome): a stable rp_ref_* ProcessorRef + Status "refunded", replay-stable on the
// idempotency key, and guarded on the required creds/ref/amount. The live half (httptest contract, no live
// call) lives in run_payments_test.go. Together they replace the W4 ErrNotImplemented pair.
func TestProcessor_Refund_Deterministic(t *testing.T) {
	res, err := RunPaymentsStub{}.Refund(context.Background(), goodCreds(), "MID-1", "rp_sim_x", 12500)
	if err != nil {
		t.Fatalf("stub Refund: %v", err)
	}
	if res.Status != "refunded" {
		t.Fatalf("stub Refund status = %q, want refunded", res.Status)
	}
	if !strings.HasPrefix(res.ProcessorRef, "rp_ref_") {
		t.Fatalf("stub Refund ref = %q, want rp_ref_ prefix", res.ProcessorRef)
	}
	// Deterministic: same inputs → same ref.
	res2, _ := RunPaymentsStub{}.Refund(context.Background(), goodCreds(), "MID-1", "rp_sim_x", 12500)
	if res2.ProcessorRef != res.ProcessorRef {
		t.Fatalf("stub Refund not deterministic: %q vs %q", res2.ProcessorRef, res.ProcessorRef)
	}
	// Guards: missing ref and non-positive amount are rejected.
	stub := RunPaymentsStub{}
	if _, err := stub.Refund(context.Background(), goodCreds(), "MID-1", "", 100); err == nil {
		t.Fatalf("stub Refund must reject an empty processor ref")
	}
	if _, err := stub.Refund(context.Background(), goodCreds(), "MID-1", "rp_sim_x", 0); err == nil {
		t.Fatalf("stub Refund must reject a non-positive amount")
	}
}
