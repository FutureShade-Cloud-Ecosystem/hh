package identity

// Test key material and the test token issuer. This file is build-locked to
// tests by its _test.go suffix and is the only signing code anywhere in
// this package: the package itself verifies, the platform issues. Keys are
// generated at runtime from the platform CSPRNG, never committed, never
// printed, and discarded with the process (QA B1 inventory, SW-0).

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"
)

const (
	testIssuer    = "test-issuer"
	testStaffAud  = "hh-staff"
	testTenantAud = "hh-tenant"
	testKeyID     = "test-rsa-active"
	testECKeyID   = "test-ec-active"
	testSubject   = "platform-user-1"
	testTenantID  = "0d9f6a34-1111-4c1c-9c11-000000000001"
)

// testKeys holds runtime-generated asymmetric key material.
type testKeys struct {
	rsaActive *rsa.PrivateKey
	rsaOther  *rsa.PrivateKey
	ecActive  *ecdsa.PrivateKey
	ecOther   *ecdsa.PrivateKey
}

func generateTestKeys(t *testing.T) *testKeys {
	t.Helper()
	rsaActive, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	rsaOther, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	ecActive, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ec key: %v", err)
	}
	ecOther, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ec key: %v", err)
	}
	return &testKeys{rsaActive: rsaActive, rsaOther: rsaOther, ecActive: ecActive, ecOther: ecOther}
}

// jwksJSON builds a JWK Set document carrying the active keys.
func (k *testKeys) jwksJSON() string {
	set := jwkSet{Keys: []jwk{
		rsaJWK(k.rsaActive.PublicKey, testKeyID, RS256),
		ecJWK(&k.ecActive.PublicKey, testECKeyID, ES256),
	}}
	b, err := json.Marshal(set)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func rsaJWK(pub rsa.PublicKey, kid string, alg Alg) jwk {
	return jwk{
		Kty: "RSA", KID: kid, Use: "sig", Alg: string(alg),
		N: base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E: base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
}

func ecJWK(pub *ecdsa.PublicKey, kid string, alg Alg) jwk {
	size := (pub.Curve.Params().N.BitLen() + 7) / 8
	xb := make([]byte, size)
	yb := make([]byte, size)
	pub.X.FillBytes(xb)
	pub.Y.FillBytes(yb)
	return jwk{
		Kty: "EC", KID: kid, Use: "sig", Alg: string(alg), Crv: "P-256",
		X: base64.RawURLEncoding.EncodeToString(xb),
		Y: base64.RawURLEncoding.EncodeToString(yb),
	}
}

// flippableKeys is a KeySource whose key set can be swapped between calls,
// proving verification runs against the live set on every request.
type flippableKeys struct {
	keys map[string]crypto.PublicKey
}

func (f *flippableKeys) KeyByID(_ context.Context, kid string) (crypto.PublicKey, bool, error) {
	key, ok := f.keys[kid]
	return key, ok, nil
}

// failingKeys always errors, standing in for an unreachable JWK Set.
type failingKeys struct{}

func (failingKeys) KeyByID(context.Context, string) (crypto.PublicKey, bool, error) {
	return nil, false, fmt.Errorf("key source is down")
}

// clock is a mutable test clock.
type clock struct{ now time.Time }

func newClock() *clock                   { return &clock{now: time.Now()} }
func (c *clock) Now() time.Time          { return c.now }
func (c *clock) advance(d time.Duration) { c.now = c.now.Add(d) }

// testConfig returns a valid Config over the given source.
func testConfig(src KeySource, clk *clock) Config {
	return Config{
		KeySource:      src,
		Issuer:         testIssuer,
		StaffAudience:  testStaffAud,
		TenantAudience: testTenantAud,
		Now:            clk.Now,
	}
}

func mustVerifier(t *testing.T, cfg Config) *Verifier {
	t.Helper()
	v, err := NewVerifier(cfg)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

// mint signs a compact JWT with the given algorithm and key. It is the
// test issuer: caller-chosen claims, kid, key and expiry, so every
// negative-space leg is one argument away (QA B1 inventory, SW-0 item 2).
func mint(t *testing.T, alg string, key any, kid string, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": alg, "kid": kid, "typ": "JWT"}
	hb, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	signed := b64url(hb) + "." + b64url(cb)
	var sig []byte
	switch alg {
	case "RS256":
		priv := key.(*rsa.PrivateKey)
		digest := sha256.Sum256([]byte(signed))
		sig, err = rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, digest[:])
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
	case "ES256":
		priv := key.(*ecdsa.PrivateKey)
		digest := sha256.Sum256([]byte(signed))
		r, s, err2 := ecdsa.Sign(rand.Reader, priv, digest[:])
		if err2 != nil {
			t.Fatalf("sign: %v", err2)
		}
		// JWS elliptic signatures are the fixed-width r||s concatenation.
		rb := make([]byte, 32)
		sb := make([]byte, 32)
		r.FillBytes(rb)
		s.FillBytes(sb)
		sig = append(rb, sb...)
	case "HS256":
		mac := hmac.New(sha256.New, key.([]byte))
		mac.Write([]byte(signed))
		sig = mac.Sum(nil)
	case "none":
		sig = []byte{}
	default:
		t.Fatalf("test issuer cannot sign %q", alg)
	}
	return signed + "." + b64url(sig)
}

// mintRaw signs an arbitrary payload byteslice, so malformed-claims tests
// can carry a valid signature.
func mintRaw(t *testing.T, key *rsa.PrivateKey, kid string, claims []byte) string {
	t.Helper()
	header := map[string]any{"alg": "RS256", "kid": kid, "typ": "JWT"}
	hb, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	signed := b64url(hb) + "." + b64url(claims)
	digest := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signed + "." + b64url(sig)
}

// mintHMACWithOwnKeyMaterial builds the alg-confusion token: an HS256
// token whose symmetric secret is the verifier's own RSA public key
// material, the classic attack on verifiers that trust the token's alg.
func mintHMACWithOwnKeyMaterial(t *testing.T, pub *rsa.PublicKey, kid string, claims map[string]any) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	return mint(t, "HS256", der, kid, claims)
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// staffClaims and tenantClaims build the two token shapes. The staff shape
// deliberately carries authority-looking claims in some tests to prove
// they are never read.
func staffClaims(clk *clock, sub string) map[string]any {
	return map[string]any{
		"iss": testIssuer,
		"sub": sub,
		"aud": testStaffAud,
		"iat": clk.now.Add(-time.Minute).Unix(),
		"exp": clk.now.Add(10 * time.Minute).Unix(),
	}
}

func tenantClaims(clk *clock, sub, tenant string) map[string]any {
	c := staffClaims(clk, sub)
	c["aud"] = testTenantAud
	c["tenant"] = tenant
	return c
}

// wantClass asserts the error is a VerifyError of the wanted class.
func wantClass(t *testing.T, err error, class ErrorClass) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a %d class error, got none", class)
	}
	var ve *VerifyError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *VerifyError, got %T: %v", err, err)
	}
	if ve.Class() != class {
		t.Fatalf("expected class %d, got %d (%v)", class, ve.Class(), err)
	}
}
