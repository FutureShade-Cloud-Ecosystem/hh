package identity

import (
	"context"
	"crypto"
	"strings"
	"testing"
	"time"
)

func staticSource(k *testKeys) KeySource {
	return NewStaticKeySource(map[string]crypto.PublicKey{
		testKeyID:   &k.rsaActive.PublicKey,
		testECKeyID: &k.ecActive.PublicKey,
	})
}

func TestVerifyRS256StaffTokenOK(t *testing.T) {
	k := generateTestKeys(t)
	clk := newClock()
	v := mustVerifier(t, testConfig(staticSource(k), clk))
	token := mint(t, "RS256", k.rsaActive, testKeyID, staffClaims(clk, testSubject))

	got, err := v.Verify(context.Background(), token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.Class != ClassStaff {
		t.Fatalf("class = %v, want staff", got.Class)
	}
	if got.Subject != testSubject {
		t.Fatalf("subject = %q", got.Subject)
	}
	if got.TenantID != "" {
		t.Fatalf("staff token carries tenant linkage %q", got.TenantID)
	}
	if !got.IssuedAt.Before(got.ExpiresAt) {
		t.Fatalf("times not carried: iat %v exp %v", got.IssuedAt, got.ExpiresAt)
	}
}

func TestVerifyES256TenantTokenOK(t *testing.T) {
	k := generateTestKeys(t)
	clk := newClock()
	v := mustVerifier(t, testConfig(staticSource(k), clk))
	token := mint(t, "ES256", k.ecActive, testECKeyID, tenantClaims(clk, testSubject, testTenantID))

	got, err := v.Verify(context.Background(), token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.Class != ClassTenant {
		t.Fatalf("class = %v, want tenant", got.Class)
	}
	if got.TenantID != testTenantID {
		t.Fatalf("tenant = %q, want %q", got.TenantID, testTenantID)
	}
}

// The both-halves discipline on the allowed-algorithm set: each default
// algorithm verifies, and a narrowed set refuses the other one.
func TestVerifyAllowedAlgorithmSetNarrows(t *testing.T) {
	k := generateTestKeys(t)
	clk := newClock()

	rsaOnly := testConfig(staticSource(k), clk)
	rsaOnly.Algorithms = []Alg{RS256}
	v := mustVerifier(t, rsaOnly)
	if _, err := v.Verify(context.Background(), mint(t, "RS256", k.rsaActive, testKeyID, staffClaims(clk, testSubject))); err != nil {
		t.Fatalf("rs256 under rsa-only set: %v", err)
	}
	_, err := v.Verify(context.Background(), mint(t, "ES256", k.ecActive, testECKeyID, staffClaims(clk, testSubject)))
	wantClass(t, err, ClassCredential)

	ecOnly := testConfig(staticSource(k), clk)
	ecOnly.Algorithms = []Alg{ES256}
	v2 := mustVerifier(t, ecOnly)
	if _, err := v2.Verify(context.Background(), mint(t, "ES256", k.ecActive, testECKeyID, staffClaims(clk, testSubject))); err != nil {
		t.Fatalf("es256 under ec-only set: %v", err)
	}
	_, err = v2.Verify(context.Background(), mint(t, "RS256", k.rsaActive, testKeyID, staffClaims(clk, testSubject)))
	wantClass(t, err, ClassCredential)
}

func TestVerifyMalformedForms(t *testing.T) {
	k := generateTestKeys(t)
	clk := newClock()
	v := mustVerifier(t, testConfig(staticSource(k), clk))
	valid := mint(t, "RS256", k.rsaActive, testKeyID, staffClaims(clk, testSubject))
	parts := strings.Split(valid, ".")

	cases := map[string]string{
		"empty":           "",
		"two parts":       parts[0] + "." + parts[1],
		"four parts":      valid + "." + parts[2],
		"bad header b64":  "!!!." + parts[1] + "." + parts[2],
		"header not json": b64url([]byte("not json")) + "." + parts[1] + "." + parts[2],
		// Claims-payload defects are minted with a valid signature:
		// signature verification precedes claims parsing.
		"claims not json":   mintRaw(t, k.rsaActive, testKeyID, []byte("[1]")),
		"claims not object": mintRaw(t, k.rsaActive, testKeyID, []byte("null")),
		"bad signature":     parts[0] + "." + parts[1] + ".!!!",
	}
	for name, token := range cases {
		_, err := v.Verify(context.Background(), token)
		if err == nil {
			t.Fatalf("%s: expected refusal", name)
		}
		wantClass(t, err, ClassMalformed)
	}
}

func TestVerifyWrongKeyAndUnknownKid(t *testing.T) {
	k := generateTestKeys(t)
	clk := newClock()
	v := mustVerifier(t, testConfig(staticSource(k), clk))

	// Signed by a key the set does not carry, under a kid it does not name.
	_, err := v.Verify(context.Background(), mint(t, "RS256", k.rsaOther, "other-kid", staffClaims(clk, testSubject)))
	wantClass(t, err, ClassCredential)

	// Names a real kid but signed by the wrong key: the signature must not
	// verify.
	_, err = v.Verify(context.Background(), mint(t, "RS256", k.rsaOther, testKeyID, staffClaims(clk, testSubject)))
	wantClass(t, err, ClassCredential)

	// Names the retired kid after the set rotated it out.
	retired := mint(t, "RS256", k.rsaOther, "test-rsa-retired", staffClaims(clk, testSubject))
	_, err = v.Verify(context.Background(), retired)
	wantClass(t, err, ClassCredential)

	// No kid at all.
	noKid := mint(t, "RS256", k.rsaActive, "", staffClaims(clk, testSubject))
	_, err = v.Verify(context.Background(), noKid)
	wantClass(t, err, ClassCredential)
}

func TestVerifyExpiry(t *testing.T) {
	k := generateTestKeys(t)
	clk := newClock()
	v := mustVerifier(t, testConfig(staticSource(k), clk))

	claims := staffClaims(clk, testSubject)
	claims["exp"] = clk.now.Add(-time.Second).Unix()
	_, err := v.Verify(context.Background(), mint(t, "RS256", k.rsaActive, testKeyID, claims))
	wantClass(t, err, ClassCredential)

	// Expiry gets no grace: the moment exp passes the clock, refusal.
	live := staffClaims(clk, testSubject)
	live["exp"] = clk.now.Add(time.Minute).Unix()
	token := mint(t, "RS256", k.rsaActive, testKeyID, live)
	if _, err := v.Verify(context.Background(), token); err != nil {
		t.Fatalf("live token refused: %v", err)
	}
	clk.advance(2 * time.Minute)
	_, err = v.Verify(context.Background(), token)
	wantClass(t, err, ClassCredential)

	// Missing expiry is malformed and fails closed.
	delete(live, "exp")
	_, err = v.Verify(context.Background(), mint(t, "RS256", k.rsaActive, testKeyID, live))
	wantClass(t, err, ClassMalformed)
}

func TestVerifyIssuerAudienceSubject(t *testing.T) {
	k := generateTestKeys(t)
	clk := newClock()
	v := mustVerifier(t, testConfig(staticSource(k), clk))
	ctx := context.Background()

	wrongIss := staffClaims(clk, testSubject)
	wrongIss["iss"] = "other-issuer"
	_, err := v.Verify(ctx, mint(t, "RS256", k.rsaActive, testKeyID, wrongIss))
	wantClass(t, err, ClassCredential)

	wrongAud := staffClaims(clk, testSubject)
	wrongAud["aud"] = "some-other-audience"
	_, err = v.Verify(ctx, mint(t, "RS256", k.rsaActive, testKeyID, wrongAud))
	wantClass(t, err, ClassCredential)

	audArray := staffClaims(clk, testSubject)
	audArray["aud"] = []string{"unrelated", testStaffAud}
	if _, err := v.Verify(ctx, mint(t, "RS256", k.rsaActive, testKeyID, audArray)); err != nil {
		t.Fatalf("audience array form refused: %v", err)
	}

	bothAuds := staffClaims(clk, testSubject)
	bothAuds["aud"] = []string{testStaffAud, testTenantAud}
	_, err = v.Verify(ctx, mint(t, "RS256", k.rsaActive, testKeyID, bothAuds))
	wantClass(t, err, ClassCredential)

	audNumber := staffClaims(clk, testSubject)
	audNumber["aud"] = 7
	_, err = v.Verify(ctx, mint(t, "RS256", k.rsaActive, testKeyID, audNumber))
	wantClass(t, err, ClassMalformed)

	noSub := staffClaims(clk, "")
	delete(noSub, "sub")
	_, err = v.Verify(ctx, mint(t, "RS256", k.rsaActive, testKeyID, noSub))
	wantClass(t, err, ClassMalformed)
}

func TestVerifyIssuedAtAndNotBefore(t *testing.T) {
	k := generateTestKeys(t)
	clk := newClock()
	v := mustVerifier(t, testConfig(staticSource(k), clk))
	ctx := context.Background()

	future := staffClaims(clk, testSubject)
	future["iat"] = clk.now.Add(10 * time.Minute).Unix()
	_, err := v.Verify(ctx, mint(t, "RS256", k.rsaActive, testKeyID, future))
	wantClass(t, err, ClassCredential)

	// Within the skew tolerance a slightly future iat passes; the tolerance
	// exists for clock drift between platform and core.
	skew := staffClaims(clk, testSubject)
	skew["iat"] = clk.now.Add(30 * time.Second).Unix()
	if _, err := v.Verify(ctx, mint(t, "RS256", k.rsaActive, testKeyID, skew)); err != nil {
		t.Fatalf("future iat within skew refused: %v", err)
	}

	notYet := staffClaims(clk, testSubject)
	notYet["nbf"] = clk.now.Add(time.Hour).Unix()
	_, err = v.Verify(ctx, mint(t, "RS256", k.rsaActive, testKeyID, notYet))
	wantClass(t, err, ClassCredential)

	past := staffClaims(clk, testSubject)
	past["nbf"] = clk.now.Add(-time.Minute).Unix()
	if _, err := v.Verify(ctx, mint(t, "RS256", k.rsaActive, testKeyID, past)); err != nil {
		t.Fatalf("past nbf refused: %v", err)
	}

	// Missing iat is refused: the downstream driver session ceiling is
	// enforced against issued-at, so a token without it cannot support
	// that check.
	noIat := staffClaims(clk, testSubject)
	delete(noIat, "iat")
	_, err = v.Verify(ctx, mint(t, "RS256", k.rsaActive, testKeyID, noIat))
	wantClass(t, err, ClassMalformed)
}

// The alg-confusion defense: an HS256 token signed with the verifier's own
// RSA public key material, and an unsigned none token, are both refused
// because the algorithm is checked before any key material is touched.
func TestVerifyAlgConfusionRefused(t *testing.T) {
	k := generateTestKeys(t)
	clk := newClock()
	v := mustVerifier(t, testConfig(staticSource(k), clk))
	ctx := context.Background()

	confusion := mintHMACWithOwnKeyMaterial(t, &k.rsaActive.PublicKey, testKeyID, staffClaims(clk, testSubject))
	_, err := v.Verify(ctx, confusion)
	wantClass(t, err, ClassCredential)

	none := mint(t, "none", nil, testKeyID, staffClaims(clk, testSubject))
	_, err = v.Verify(ctx, none)
	wantClass(t, err, ClassCredential)

	// And no symmetric secret exists anywhere in the verifier: the error
	// text of the confusion refusal is static and carries no token bytes.
	ve, ok := err.(*VerifyError)
	if !ok || strings.Contains(ve.Error(), confusion) {
		t.Fatalf("error echoes token content: %v", err)
	}
}

// The key must belong to the algorithm's family: naming the right kid with
// the wrong family of key never verifies.
func TestVerifyKeyFamilyMismatch(t *testing.T) {
	k := generateTestKeys(t)
	clk := newClock()
	v := mustVerifier(t, testConfig(staticSource(k), clk))
	ctx := context.Background()

	_, err := v.Verify(ctx, mint(t, "RS256", k.rsaActive, testECKeyID, staffClaims(clk, testSubject)))
	wantClass(t, err, ClassCredential)

	_, err = v.Verify(ctx, mint(t, "ES256", k.ecActive, testKeyID, staffClaims(clk, testSubject)))
	wantClass(t, err, ClassCredential)
}

func TestVerifyTenantClaimRequired(t *testing.T) {
	k := generateTestKeys(t)
	clk := newClock()
	v := mustVerifier(t, testConfig(staticSource(k), clk))

	noTenant := staffClaims(clk, testSubject)
	noTenant["aud"] = testTenantAud
	_, err := v.Verify(context.Background(), mint(t, "RS256", k.rsaActive, testKeyID, noTenant))
	wantClass(t, err, ClassMalformed)
}

// D3 and the StaffBearer contract description: the staff token carries no
// role claims and none are read. Tokens with invented authority claims
// verify exactly like tokens without them, and the verified Token struct
// has nowhere to carry authority even if a caller tried.
func TestVerifyAuthorityClaimsIgnored(t *testing.T) {
	k := generateTestKeys(t)
	clk := newClock()
	v := mustVerifier(t, testConfig(staticSource(k), clk))
	ctx := context.Background()

	plain := mint(t, "RS256", k.rsaActive, testKeyID, staffClaims(clk, testSubject))
	dressed := staffClaims(clk, testSubject)
	dressed["role"] = "super_admin"
	dressed["tier"] = 1
	dressed["branches"] = []string{"all"}
	dressedToken := mint(t, "RS256", k.rsaActive, testKeyID, dressed)

	a, errA := v.Verify(ctx, plain)
	b, errB := v.Verify(ctx, dressedToken)
	if errA != nil || errB != nil {
		t.Fatalf("authority claims changed verification: %v %v", errA, errB)
	}
	if a.Class != b.Class || a.Subject != b.Subject || a.TenantID != b.TenantID {
		t.Fatalf("authority claims changed the token model")
	}
}

func TestVerifyOperationalFailureClass(t *testing.T) {
	k := generateTestKeys(t)
	clk := newClock()
	v := mustVerifier(t, testConfig(failingKeys{}, clk))
	_, err := v.Verify(context.Background(), mint(t, "RS256", k.rsaActive, testKeyID, staffClaims(clk, testSubject)))
	wantClass(t, err, ClassOperational)
}

// D28: the credential is never cached. Two verifications of the same token
// against a key set that rotates between them: the second must fail closed
// against the live set.
func TestVerifyCredentialNeverCached(t *testing.T) {
	k := generateTestKeys(t)
	clk := newClock()
	src := &flippableKeys{keys: map[string]crypto.PublicKey{
		testKeyID:   &k.rsaActive.PublicKey,
		testECKeyID: &k.ecActive.PublicKey,
	}}
	v := mustVerifier(t, testConfig(src, clk))
	token := mint(t, "RS256", k.rsaActive, testKeyID, staffClaims(clk, testSubject))

	if _, err := v.Verify(context.Background(), token); err != nil {
		t.Fatalf("first verify: %v", err)
	}
	src.keys = map[string]crypto.PublicKey{testECKeyID: &k.ecActive.PublicKey}
	_, err := v.Verify(context.Background(), token)
	wantClass(t, err, ClassCredential)
}

func TestNewVerifierValidation(t *testing.T) {
	clk := newClock()
	good := testConfig(nil, clk)
	good.JWKSURL = "https://keys.example.test/jwks"
	good.KeySource = nil
	if _, err := NewVerifier(good); err != nil {
		t.Fatalf("valid config refused: %v", err)
	}

	cases := map[string]func(Config) Config{
		"no key source": func(c Config) Config { c.KeySource = nil; c.JWKSURL = ""; return c },
		"bad jwks url": func(c Config) Config {
			c.KeySource = nil
			c.JWKSURL = "not a url"
			return c
		},
		"empty issuer":       func(c Config) Config { c.Issuer = ""; return c },
		"no staff audience":  func(c Config) Config { c.StaffAudience = ""; return c },
		"no tenant audience": func(c Config) Config { c.TenantAudience = ""; return c },
		"same audiences": func(c Config) Config {
			c.TenantAudience = c.StaffAudience
			return c
		},
		"unknown algorithm": func(c Config) Config { c.Algorithms = []Alg{"HS256"}; return c },
	}
	for name, mutate := range cases {
		cfg := mutate(testConfig(staticSource(generateTestKeys(t)), clk))
		if _, err := NewVerifier(cfg); err == nil {
			t.Fatalf("%s: expected construction refusal", name)
		}
	}
}

// Error messages are static text: no token bytes, kid or claim value may
// appear in any VerifyError, so nothing downstream can echo them.
func TestVerifyErrorsNeverCarryTokenContent(t *testing.T) {
	k := generateTestKeys(t)
	clk := newClock()
	v := mustVerifier(t, testConfig(staticSource(k), clk))

	bad := []string{
		mint(t, "RS256", k.rsaOther, testKeyID, staffClaims(clk, testSubject)),
		mint(t, "RS256", k.rsaActive, "attacker-kid", staffClaims(clk, testSubject)),
		"garbage.token.value",
	}
	for _, token := range bad {
		_, err := v.Verify(context.Background(), token)
		if err == nil {
			t.Fatalf("expected refusal")
		}
		if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "attacker-kid") {
			t.Fatalf("error echoes token content: %v", err)
		}
	}
}
