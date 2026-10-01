//go:build demo

package issuer

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

// A minted token passes the real verifier, unchanged: the verifier is
// constructed over the issuer's static key source with the staff audience
// and the demo issuer value, exactly the construction the demo serve
// wiring makes, and the token classifies as a staff bearer carrying the
// account's platform user id as its subject.
func TestMintedTokenPassesTheUnchangedVerifier(t *testing.T) {
	iss, err := New("hh-staff", 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	verifier, err := identity.NewVerifier(identity.Config{
		KeySource:      iss.KeySource(),
		Issuer:         ClaimIssuer,
		StaffAudience:  "hh-staff",
		TenantAudience: "hh-tenant",
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	subject := "d7000000-0000-4000-9000-000000000da1"
	token, err := iss.Mint(subject)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	got, err := verifier.Verify(context.Background(), token)
	if err != nil {
		t.Fatalf("the real verifier refused a demo token: %v", err)
	}
	if got.Class != identity.ClassStaff {
		t.Fatalf("class = %v, want staff", got.Class)
	}
	if got.Subject != subject {
		t.Fatalf("subject = %q, want %q", got.Subject, subject)
	}
	if !got.ExpiresAt.After(got.IssuedAt) {
		t.Fatalf("expiry %v does not follow issue %v", got.ExpiresAt, got.IssuedAt)
	}
}

// The token carries the demo issuer value, the staff audience and no
// renewal path: the claims are read straight off the wire here so a
// claim-vocabulary drift fails loudly instead of at a tester's screen.
func TestTokenClaims(t *testing.T) {
	iss, err := New("hh-staff", 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	token, err := iss.Mint("subject-1")
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token is not a compact JWS")
	}
	claims := decodePart(t, parts[1])
	for _, want := range []string{`"iss":"` + ClaimIssuer + `"`, `"sub":"subject-1"`, `"aud":"hh-staff"`} {
		if !strings.Contains(claims, want) {
			t.Fatalf("claims %s do not carry %s", claims, want)
		}
	}
}

// An expired token is refused: the expiry travels in the token and the
// verifier's own expiry check does the refusing, with no grace.
func TestExpiredTokenIsRefused(t *testing.T) {
	iss, err := New("hh-staff", 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	past := time.Now().Add(-2 * DefaultTTL)
	iss.now = func() time.Time { return past }
	token, err := iss.mintAt("subject-1", past)
	if err != nil {
		t.Fatalf("mintAt: %v", err)
	}
	iss.now = time.Now
	verifier, err := identity.NewVerifier(identity.Config{
		KeySource:      iss.KeySource(),
		Issuer:         ClaimIssuer,
		StaffAudience:  "hh-staff",
		TenantAudience: "hh-tenant",
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if _, err := verifier.Verify(context.Background(), token); err == nil {
		t.Fatal("an expired token verified")
	}
}

// A token naming another audience does not pass: the vocabulary the demo
// build constructs the verifier with is the staff pair, and a mint built
// as the serve wiring builds it never emits a tenant token.
func TestTenantVocabularyIsRefused(t *testing.T) {
	tenantMint, err := New("hh-tenant", 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	token, err := tenantMint.Mint("subject-1")
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	staffVerifier, err := identity.NewVerifier(identity.Config{
		KeySource:      tenantMint.KeySource(),
		Issuer:         ClaimIssuer,
		StaffAudience:  "hh-staff",
		TenantAudience: "hh-tenant",
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if _, err := staffVerifier.Verify(context.Background(), token); err == nil {
		t.Fatal("a tenant-audience mint passed a staff-built verifier")
	}
}

func decodePart(t *testing.T, part string) string {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil {
		t.Fatalf("decode part: %v", err)
	}
	return string(b)
}
