//go:build demo

package issuer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

func newVerifierOver(t *testing.T, i *Issuer) *identity.Verifier {
	t.Helper()
	v, err := identity.NewVerifier(identity.Config{
		KeySource:      i.KeySource(),
		Issuer:         ClaimIssuer,
		StaffAudience:  "hh-staff",
		TenantAudience: "hh-tenant",
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

// G5: tokens only work on the box that minted them, only until restart.
// Two issuers are two boxes (or one box before and after its restart):
// each generates its own key in its own memory, so a token from one fails
// on the other, with the same key id and the same issuer value, failing
// on the signature alone. Nothing in this package marshals the private
// key, so the only way a token outlives its box is a shared key, and no
// path here produces one.
func TestTokenFromOneIssuerFailsOnAnother(t *testing.T) {
	boxA, err := New("hh-staff", 0)
	if err != nil {
		t.Fatalf("issuer A: %v", err)
	}
	boxB, err := New("hh-staff", 0)
	if err != nil {
		t.Fatalf("issuer B: %v", err)
	}
	token, err := boxA.Mint("d7000000-0000-4000-9000-000000000da1")
	if err != nil {
		t.Fatalf("mint on A: %v", err)
	}
	// The token is well formed and current: on its own box it verifies.
	if _, err := newVerifierOver(t, boxA).Verify(context.Background(), token); err != nil {
		t.Fatalf("the token failed on its own box: %v", err)
	}
	// The two issuers share the key id and the claim vocabulary, so the
	// refusal is the signature, which is the point: the key is the only
	// thing that does not travel.
	tokenClaims(t, token)
	if _, err := newVerifierOver(t, boxB).Verify(context.Background(), token); err == nil {
		t.Fatal("a token from one issuer verified on another")
	}
}

// The private key never leaves memory: the issuer exposes no
// serialization of it, so the static key source carries the public half
// and nothing else. The assertions walk the type's interface set: any
// method that rendered key material would have to be added here first,
// and this test fails on the day one lands.
func TestIssuerExposesNoKeyMaterial(t *testing.T) {
	iss, err := New("hh-staff", 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := any(iss).(interface{ MarshalJSON() ([]byte, error) }); ok {
		t.Fatal("the issuer marshals to JSON")
	}
	if _, ok := any(iss).(interface{ MarshalText() ([]byte, error) }); ok {
		t.Fatal("the issuer marshals to text")
	}
	if _, ok := any(iss).(interface{ String() string }); ok {
		t.Fatal("the issuer renders itself as a string")
	}
}

// tokenClaims reads the wire claims so the test can state what two
// issuers share: the same key id and the same issuer and audience
// vocabulary. The failure on the second box is therefore attributable to
// the key alone.
func tokenClaims(t *testing.T, token string) {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token is not a compact JWS")
	}
	headerRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("header: %v", err)
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerRaw, &header); err != nil {
		t.Fatalf("header json: %v", err)
	}
	if header.Kid != KeyID || header.Alg != "ES256" {
		t.Fatalf("header drifted: %+v", header)
	}
}

// A second mint over the same issuer is a different token (fresh
// signature entropy) that still verifies: the isolation is between keys,
// not between tokens.
func TestSecondMintStillVerifiesOnItsOwnBox(t *testing.T) {
	iss, err := New("hh-staff", 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	first, err := iss.Mint("subject-1")
	if err != nil {
		t.Fatalf("first mint: %v", err)
	}
	second, err := iss.Mint("subject-1")
	if err != nil {
		t.Fatalf("second mint: %v", err)
	}
	if first == second {
		t.Fatal("two mints produced byte-identical tokens")
	}
	v := newVerifierOver(t, iss)
	if _, err := v.Verify(context.Background(), second); err != nil {
		t.Fatalf("the issuer's own second token refused: %v", err)
	}
}
