package payment

import "testing"

// T18, surcharge is OFF by default: enabled=false → 0 for any input (US-RP05 default-off).
func TestSurcharge_DisabledIsZero(t *testing.T) {
	for _, ct := range []string{"CREDIT", "DEBIT", "PREPAID", "UNKNOWN", ""} {
		if got := OntarioSurcharge(10000, ct, 300, false); got != 0 {
			t.Fatalf("disabled surcharge for %q = %d, want 0", ct, got)
		}
	}
}

// T19, credit is surcharged at min(2.4%, MDR): 200bps → 2.0%; 300bps → clamp to the 2.4% cap (US-RP05).
func TestSurcharge_CreditMinOfCapAndMDR(t *testing.T) {
	if got := OntarioSurcharge(10000, "CREDIT", 200, true); got != 200 {
		t.Fatalf("MDR 200bps on 10000 = %d, want 200 (2.0%%)", got)
	}
	if got := OntarioSurcharge(10000, "CREDIT", 300, true); got != 240 {
		t.Fatalf("MDR 300bps on 10000 = %d, want 240 (clamped to 2.4%% cap)", got)
	}
}

// T20, debit and prepaid are NEVER surcharged, even when enabled (prohibited in Ontario; US-RP05).
func TestSurcharge_DebitPrepaidExempt(t *testing.T) {
	if got := OntarioSurcharge(10000, "DEBIT", 200, true); got != 0 {
		t.Fatalf("DEBIT surcharge = %d, want 0", got)
	}
	if got := OntarioSurcharge(10000, "PREPAID", 200, true); got != 0 {
		t.Fatalf("PREPAID surcharge = %d, want 0", got)
	}
}

// T21, a misconfigured MDR (600bps) is clamped to the 2.4% legal cap, never the 6.0% MDR (US-RP05).
func TestSurcharge_ClampsMisconfiguredMDR(t *testing.T) {
	if got := OntarioSurcharge(10000, "CREDIT", 600, true); got != 240 {
		t.Fatalf("MDR 600bps on 10000 = %d, want 240 (2.4%% cap)", got)
	}
	// A negative (misconfigured) MDR floors at 0.
	if got := OntarioSurcharge(10000, "CREDIT", -50, true); got != 0 {
		t.Fatalf("negative MDR = %d, want 0", got)
	}
}

// T28, the persisted card_type values "DEBIT"/"PREPAID" are surcharge-exempt even when enabled, so
// the debit/prepaid exemption is data-backed before W16 (US-RP10; reinforces T20).
func TestSurcharge_PersistedCardType_DebitPrepaidExempt(t *testing.T) {
	for _, ct := range []string{"DEBIT", "PREPAID"} {
		if got := OntarioSurcharge(50000, ct, 240, true); got != 0 {
			t.Fatalf("persisted card_type %q surcharge = %d, want 0", ct, got)
		}
	}
	// And the persisted "CREDIT" is the only one that surcharges (the calculator already has its input).
	if got := OntarioSurcharge(50000, "CREDIT", 240, true); got != 1200 {
		t.Fatalf("CREDIT surcharge on 50000 @240bps = %d, want 1200", got)
	}
}
