package identity

// The round 2 review pins (LD-2 review, P1-1 and P1-2): the verifier
// picks its key set by the token's audience, so a platform login never
// consults FB Move's key set, and a repeated audience of one class
// verifies again, as on main, with only two different classes refused.
// Key material is generated inside the tests and never committed.

import (
	"context"
	"crypto"
	"testing"
)

// platformPlatformKeys builds the platform set alone, so any machine set
// beside it is exactly what the test injected.
func platformSet(k *testKeys) *flippableKeys {
	return &flippableKeys{keys: map[string]crypto.PublicKey{
		testKeyID: &k.rsaActive.PublicKey,
	}}
}

// TestPlatformTokensNeverConsultTheMachineKeySet is the P1-1 regression:
// a staff token and a tenant token verify while the machine key source is
// down. Round 1 consulted the machine set first for every token, so these
// answered the 503 family; the ruling selects the set by the audience, so
// a platform login can never wait on or fail because of FB Move's key
// endpoint.
func TestPlatformTokensNeverConsultTheMachineKeySet(t *testing.T) {
	k := generateTestKeys(t)
	clk := newClock()
	// The machine leg is configured with a source that always fails: the
	// deployment shape where FB Move's endpoint is unreachable.
	cfg := machineConfig(platformSet(k), failingKeys{}, clk)
	v := mustVerifier(t, cfg)

	tok, err := v.Verify(context.Background(), mint(t, "RS256", k.rsaActive, testKeyID, staffClaims(clk, testSubject)))
	if err != nil {
		t.Fatalf("staff token while the machine key source is down: %v", err)
	}
	if tok.Class != ClassStaff {
		t.Fatalf("staff token classified %s", tok.Class)
	}

	tenant := tenantClaims(clk, testSubject, testTenantID)
	tok, err = v.Verify(context.Background(), mint(t, "RS256", k.rsaActive, testKeyID, tenant))
	if err != nil {
		t.Fatalf("tenant token while the machine key source is down: %v", err)
	}
	if tok.Class != ClassTenant {
		t.Fatalf("tenant token classified %s", tok.Class)
	}

	// The same shape with the key id present in BOTH sets: a staff token
	// still consults only the platform set and verifies. The ambiguity
	// signal belongs to the machine audience alone.
	both := &flippableKeys{keys: map[string]crypto.PublicKey{
		testKeyID:        &k.rsaActive.PublicKey,
		testMachineKeyID: &machineKeys(t).rsaActive.PublicKey,
	}}
	v = mustVerifier(t, machineConfig(both, machineKeySource(machineKeys(t)), clk))
	if _, err := v.Verify(context.Background(), mint(t, "RS256", k.rsaActive, testKeyID, staffClaims(clk, testSubject))); err != nil {
		t.Fatalf("staff token whose key id also sits in the machine set: %v", err)
	}
}

// TestMachineTokenStillFailsWhenItsOwnSourceFails is the mirror that must
// keep holding: the machine class depends on its own key set, and a machine
// audience token with that source down answers the 503 family, never an
// admission.
func TestMachineTokenStillFailsWhenItsOwnSourceFails(t *testing.T) {
	k := generateTestKeys(t)
	clk := newClock()
	v := mustVerifier(t, machineConfig(platformSet(k), failingKeys{}, clk))
	_, err := v.Verify(context.Background(), mint(t, "RS256", machineKeys(t).rsaActive, testMachineKeyID, machineClaims(clk, testSubject)))
	if err == nil {
		t.Fatal("a machine token verified against a machine key source that is down")
	}
	wantClass(t, err, ClassOperational)
}

// TestDuplicatedAudienceOfOneClassVerifies is the P1-2 pin: an issuer that
// repeats one class's audience verifies, exactly as on main. Only two
// different classes claimed together are refused.
func TestDuplicatedAudienceOfOneClassVerifies(t *testing.T) {
	k := generateTestKeys(t)
	m := machineKeys(t)
	clk := newClock()
	v := mustVerifier(t, machineConfig(platformSet(k), machineKeySource(m), clk))

	staffTwice := staffClaims(clk, testSubject)
	staffTwice["aud"] = []string{testStaffAud, testStaffAud}
	tok, err := v.Verify(context.Background(), mint(t, "RS256", k.rsaActive, testKeyID, staffTwice))
	if err != nil {
		t.Fatalf("duplicated staff audience: %v", err)
	}
	if tok.Class != ClassStaff {
		t.Fatalf("duplicated staff audience classified %s", tok.Class)
	}

	tenantTwice := tenantClaims(clk, testSubject, testTenantID)
	tenantTwice["aud"] = []string{testTenantAud, testTenantAud}
	tok, err = v.Verify(context.Background(), mint(t, "RS256", k.rsaActive, testKeyID, tenantTwice))
	if err != nil {
		t.Fatalf("duplicated tenant audience: %v", err)
	}
	if tok.Class != ClassTenant {
		t.Fatalf("duplicated tenant audience classified %s", tok.Class)
	}

	machineTwice := machineClaims(clk, testSubject)
	machineTwice["aud"] = []string{testMachineAud, testMachineAud}
	tok, err = v.Verify(context.Background(), mint(t, "RS256", m.rsaActive, testMachineKeyID, machineTwice))
	if err != nil {
		t.Fatalf("duplicated machine audience: %v", err)
	}
	if tok.Class != ClassMachine {
		t.Fatalf("duplicated machine audience classified %s", tok.Class)
	}

	// Two different classes together are still refused, on every pairing.
	staffAndTenant := staffClaims(clk, testSubject)
	staffAndTenant["aud"] = []string{testStaffAud, testTenantAud}
	_, err = v.Verify(context.Background(), mint(t, "RS256", k.rsaActive, testKeyID, staffAndTenant))
	if err == nil {
		t.Fatal("a token claiming both platform audiences verified")
	}
	wantClass(t, err, ClassCredential)

	machineAndStaff := machineClaims(clk, testSubject)
	machineAndStaff["aud"] = []string{testMachineAud, testStaffAud}
	_, err = v.Verify(context.Background(), mint(t, "RS256", m.rsaActive, testMachineKeyID, machineAndStaff))
	if err == nil {
		t.Fatal("a token claiming both the machine and the staff audiences verified")
	}
	wantClass(t, err, ClassCredential)
}
