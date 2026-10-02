package payments

import (
	"context"
	"testing"
)

// The discriminator answers true only for the stub's exact construction. Every near miss must
// stay false: the bare prefix, a tail one char short or long, uppercase hex, a non hex
// character, a live processor's digit shaped transaction id, the till capture stub's prefix,
// and the vault token's shape.
func TestIsStubChargeRef_TrueOnlyForTheExactStubConstruction(t *testing.T) {
	cases := []struct {
		name string
		ref  string
		want bool
	}{
		{"the stub's exact construction", "ch_0123456789abcdef", true},
		{"the bare prefix alone", "ch_", false},
		{"a tail one char short", "ch_0123456789abcde", false},
		{"a tail one char long", "ch_0123456789abcdef0", false},
		{"uppercase hex in the tail", "ch_0123456789ABCDEF", false},
		{"a non hex tail", "ch_0123456789abcdeg", false},
		{"a live processor's digit id", "8812340077", false},
		{"the till capture stub's prefix", "rp_sim_0123456789abcdef", false},
		{"the vault token's shape", "tok_0123456789abcdef", false},
		{"the empty ref", "", false},
	}
	for _, tc := range cases {
		if got := IsStubChargeRef(tc.ref); got != tc.want {
			t.Errorf("%s: IsStubChargeRef(%q) = %v, want %v", tc.name, tc.ref, got, tc.want)
		}
	}
}

// The tie: the real gateway's charge must mint exactly the construction the exported check
// names. While Charge mints and IsStubChargeRef agree, the receipt's simulated stamp and the
// mint cannot disagree; if the exported prefix ever moves to a value the stub does not mint,
// this leg fails naming the drift.
func TestStubGateway_ChargeMintsARefTheShapeCheckNames(t *testing.T) {
	res, err := NewStubGateway().Charge(context.Background(), ChargeRequest{
		CustomerID:     "cust-shape-tie",
		AmountCents:    5000,
		IdempotencyKey: "idem-shape-tie",
	})
	if err != nil {
		t.Fatalf("stub charge: %v", err)
	}
	if res.Outcome != OutcomeApproved {
		t.Fatalf("fixture assumption broken: outcome %q, want approved", res.Outcome)
	}
	if !IsStubChargeRef(res.GatewayRef) {
		t.Fatalf("the stub minted %q, which the exported shape check (prefix %q) does not name: the mint and the discriminator have drifted", res.GatewayRef, StubChargeRefPrefix)
	}

	// The vault token rides the same mint family under a different prefix; it must never
	// answer to the charge check, or a vaulted method's token could one day stamp paper.
	v, err := NewStubGateway().Vault(context.Background(), VaultRequest{CustomerID: "cust-shape-tie", Nonce: "nonce-shape-tie"})
	if err != nil {
		t.Fatalf("stub vault: %v", err)
	}
	if IsStubChargeRef(v.Token) {
		t.Fatalf("the vault token %q must not answer to the charge ref check", v.Token)
	}
}
