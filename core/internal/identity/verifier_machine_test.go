package identity

// The verifier's machine leg (HH-LOAD-1 LD-2, ADR-0024): the third
// bearer class verifies only against the machine key set, never the
// platform's staff key set, no default key exists, and every restriction
// the verifier already carries (the algorithm set, the standard claim
// vocabulary, the audience exclusivity) binds the machine class exactly
// as it binds the other two. All key material is generated inside the
// test and never committed.

import (
	"context"
	"crypto"
	"testing"
	"time"
)

const (
	testMachineAud   = "hh-machine"
	testMachineKeyID = "test-machine-rsa-active"
)

// machineKeys builds a second runtime key set standing in for FB Move's
// configured public keys, distinct from the platform set by construction.
func machineKeys(t *testing.T) *testKeys {
	t.Helper()
	return generateTestKeys(t)
}

func machineKeySource(k *testKeys) KeySource {
	return NewStaticKeySource(map[string]crypto.PublicKey{
		testMachineKeyID: &k.rsaActive.PublicKey,
	})
}

// machineConfig returns a Config that knows all three audiences.
func machineConfig(src KeySource, msrc KeySource, clk *clock) Config {
	cfg := testConfig(src, clk)
	cfg.MachineAudience = testMachineAud
	cfg.MachineKeySource = msrc
	return cfg
}

// machineClaims builds the machine token shape: the machine audience, a
// subject naming the machine principal, and no authority claims of any
// kind, because a machine credential holds none.
func machineClaims(clk *clock, sub string) map[string]any {
	c := staffClaims(clk, sub)
	c["aud"] = testMachineAud
	return c
}

// TestMachineTokenVerifiesOnTheMachineKeySet is the positive half: a
// token FB Move signed, carrying the machine audience, classifies as
// ClassMachine with its subject and window intact.
func TestMachineTokenVerifiesOnTheMachineKeySet(t *testing.T) {
	k := generateTestKeys(t)
	m := machineKeys(t)
	clk := newClock()
	v := mustVerifier(t, machineConfig(&flippableKeys{keys: map[string]crypto.PublicKey{
		testKeyID: &k.rsaActive.PublicKey,
	}}, machineKeySource(m), clk))
	tok, err := v.Verify(context.Background(), mint(t, "RS256", m.rsaActive, testMachineKeyID, machineClaims(clk, testSubject)))
	if err != nil {
		t.Fatalf("machine token: %v", err)
	}
	if tok.Class != ClassMachine {
		t.Fatalf("class %s, want machine", tok.Class)
	}
	if tok.Subject != testSubject || tok.TenantID != "" {
		t.Fatalf("machine token carries %+v", tok)
	}
	if tok.IssuedAt.Unix() != clk.now.Add(-time.Minute).Unix() || tok.ExpiresAt.Unix() != clk.now.Add(10*time.Minute).Unix() {
		t.Fatalf("machine token window carries %+v", tok)
	}
}

// TestMachineAudienceNeverVerifiesOnThePlatformKeySet is the binding the
// plan pins: a machine audience token whose key lives only in the
// platform set is a credential failure, and a machine audience token
// when no machine key set is configured is refused too, so no default
// key can ever exist.
func TestMachineAudienceNeverVerifiesOnThePlatformKeySet(t *testing.T) {
	k := generateTestKeys(t)
	m := machineKeys(t)
	clk := newClock()
	platform := &flippableKeys{keys: map[string]crypto.PublicKey{
		testKeyID: &k.rsaActive.PublicKey,
	}}
	// No machine key set configured at all: the machine audience is
	// nobody for this verifier, whatever key signed the token.
	plain := mustVerifier(t, testConfig(platform, clk))
	signedByPlatform := mint(t, "RS256", k.rsaActive, testKeyID, machineClaims(clk, testSubject))
	_, err := plain.Verify(context.Background(), signedByPlatform)
	if err == nil {
		t.Fatal("a machine audience token verified with no machine key set configured")
	}
	wantClass(t, err, ClassCredential)

	// The machine key set configured, the token still signed by the
	// platform's key: the signature is valid, the audience is the
	// machine one, and the binding refuses it anyway, because the
	// machine class never rides the platform key set.
	v := mustVerifier(t, machineConfig(platform, machineKeySource(m), clk))
	_, err = v.Verify(context.Background(), signedByPlatform)
	if err == nil {
		t.Fatal("a machine audience token verified on the platform key set")
	}
	wantClass(t, err, ClassCredential)
}

// TestMachineKeyIDAmbiguousAcrossSets: a key id present in both the
// machine set and the platform set is a deployment defect, answered as
// operational so it is loud in monitoring rather than resolved by an
// ordering guess.
func TestMachineKeyIDAmbiguousAcrossSets(t *testing.T) {
	k := generateTestKeys(t)
	m := machineKeys(t)
	clk := newClock()
	platform := &flippableKeys{keys: map[string]crypto.PublicKey{
		testKeyID:        &k.rsaActive.PublicKey,
		testMachineKeyID: &m.rsaActive.PublicKey,
	}}
	v := mustVerifier(t, machineConfig(platform, machineKeySource(m), clk))
	_, err := v.Verify(context.Background(), mint(t, "RS256", m.rsaActive, testMachineKeyID, machineClaims(clk, testSubject)))
	if err == nil {
		t.Fatal("an ambiguous key id verified")
	}
	wantClass(t, err, ClassOperational)
}

// TestMachineKeySetTokenCannotClaimAnotherAudience: a token signed by
// the machine key set that claims the staff or tenant audience is a
// credential failure; FB Move's keys can never mint a staff login.
func TestMachineKeySetTokenCannotClaimAnotherAudience(t *testing.T) {
	m := machineKeys(t)
	clk := newClock()
	v := mustVerifier(t, machineConfig(&flippableKeys{keys: map[string]crypto.PublicKey{
		testKeyID: &machineKeys(t).rsaActive.PublicKey,
	}}, machineKeySource(m), clk))
	staffOnMachineKeys := staffClaims(clk, testSubject)
	_, err := v.Verify(context.Background(), mint(t, "RS256", m.rsaActive, testMachineKeyID, staffOnMachineKeys))
	if err == nil {
		t.Fatal("a machine key set token verified claiming the staff audience")
	}
	wantClass(t, err, ClassCredential)
}

// TestMachineTokenAlgorithmRestrictions: the algorithm set the verifier
// carries binds the machine class unchanged; the HMAC confusion token
// signed with the machine key set's own public key material is refused
// before any key material is touched.
func TestMachineTokenAlgorithmRestrictions(t *testing.T) {
	m := machineKeys(t)
	clk := newClock()
	v := mustVerifier(t, machineConfig(&flippableKeys{keys: map[string]crypto.PublicKey{
		testKeyID: &machineKeys(t).rsaActive.PublicKey,
	}}, machineKeySource(m), clk))
	confusion := mintHMACWithOwnKeyMaterial(t, &m.rsaActive.PublicKey, testMachineKeyID, machineClaims(clk, testSubject))
	_, err := v.Verify(context.Background(), confusion)
	if err == nil {
		t.Fatal("the alg confusion token verified on the machine leg")
	}
	wantClass(t, err, ClassCredential)
}

// TestMachineTokenClaimVocabulary: the standard vocabulary binds the
// machine class as it binds the others: no subject is malformed, no
// expiry is malformed, and an audience claiming both the machine and the
// staff value is refused.
func TestMachineTokenClaimVocabulary(t *testing.T) {
	m := machineKeys(t)
	clk := newClock()
	v := mustVerifier(t, machineConfig(&flippableKeys{keys: map[string]crypto.PublicKey{
		testKeyID: &machineKeys(t).rsaActive.PublicKey,
	}}, machineKeySource(m), clk))
	noSub := machineClaims(clk, "")
	delete(noSub, "sub")
	_, err := v.Verify(context.Background(), mint(t, "RS256", m.rsaActive, testMachineKeyID, noSub))
	if err == nil {
		t.Fatal("a machine token with no subject verified")
	}
	wantClass(t, err, ClassMalformed)

	both := machineClaims(clk, testSubject)
	both["aud"] = []string{testMachineAud, testStaffAud}
	_, err = v.Verify(context.Background(), mint(t, "RS256", m.rsaActive, testMachineKeyID, both))
	if err == nil {
		t.Fatal("a token claiming both the machine and the staff audiences verified")
	}
	wantClass(t, err, ClassCredential)
}

// TestMachineTenantClaimIgnored: the tenant claim is read on no class
// but tenant; a machine token carrying one verifies as machine with an
// empty tenant linkage.
func TestMachineTenantClaimIgnored(t *testing.T) {
	m := machineKeys(t)
	clk := newClock()
	v := mustVerifier(t, machineConfig(&flippableKeys{keys: map[string]crypto.PublicKey{
		testKeyID: &machineKeys(t).rsaActive.PublicKey,
	}}, machineKeySource(m), clk))
	claims := machineClaims(clk, testSubject)
	claims["tenant"] = testTenantID
	tok, err := v.Verify(context.Background(), mint(t, "RS256", m.rsaActive, testMachineKeyID, claims))
	if err != nil {
		t.Fatalf("machine token carrying a tenant claim: %v", err)
	}
	if tok.Class != ClassMachine || tok.TenantID != "" {
		t.Fatalf("the tenant claim was read on the machine class: %+v", tok)
	}
}

// TestMachineConfigValidation holds the construction refusals: the
// machine audience and its key set arrive together or not at all, and
// the machine audience must differ from both configured audiences.
func TestMachineConfigValidation(t *testing.T) {
	k := generateTestKeys(t)
	m := machineKeys(t)
	clk := newClock()
	src := &flippableKeys{keys: map[string]crypto.PublicKey{testKeyID: &k.rsaActive.PublicKey}}
	msrc := machineKeySource(m)

	audNoKeys := testConfig(src, clk)
	audNoKeys.MachineAudience = testMachineAud
	if _, err := NewVerifier(audNoKeys); err == nil {
		t.Fatal("a machine audience with no key set constructed")
	}

	keysNoAud := testConfig(src, clk)
	keysNoAud.MachineKeySource = msrc
	if _, err := NewVerifier(keysNoAud); err == nil {
		t.Fatal("a machine key set with no audience constructed")
	}

	clash := machineConfig(src, msrc, clk)
	clash.MachineAudience = testStaffAud
	if _, err := NewVerifier(clash); err == nil {
		t.Fatal("a machine audience equal to the staff audience constructed")
	}

	clashTenant := machineConfig(src, msrc, clk)
	clashTenant.MachineAudience = testTenantAud
	if _, err := NewVerifier(clashTenant); err == nil {
		t.Fatal("a machine audience equal to the tenant audience constructed")
	}

	if _, err := NewVerifier(machineConfig(src, msrc, clk)); err != nil {
		t.Fatalf("the valid machine configuration was refused: %v", err)
	}
}

// TestMachineKeySourceOperationalFailure: a machine key set that cannot
// answer is operational, the 503 family at the boundary, never a
// verdict on the token.
func TestMachineKeySourceOperationalFailure(t *testing.T) {
	k := generateTestKeys(t)
	clk := newClock()
	v := mustVerifier(t, machineConfig(&flippableKeys{keys: map[string]crypto.PublicKey{
		testKeyID: &k.rsaActive.PublicKey,
	}}, failingKeys{}, clk))
	_, err := v.Verify(context.Background(), mint(t, "RS256", machineKeys(t).rsaActive, testMachineKeyID, machineClaims(clk, testSubject)))
	if err == nil {
		t.Fatal("a machine token verified against a key source that is down")
	}
	wantClass(t, err, ClassOperational)
}
