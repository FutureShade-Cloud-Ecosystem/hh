package testkit

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

// With a session key set, a default token verifies against the session key
// set, and the SessionKID shapes build the negatives that must be judged by
// the signature and algorithm checks under the REAL kid, not refused as an
// unknown kid.
func TestSessionKIDShapesAreJudgedUnderTheRealKid(t *testing.T) {
	session, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	set, err := identity.NewSessionKeySet(&session.PublicKey, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := NewMinter(t)
	m.Session = session
	v, err := identity.NewVerifier(identity.Config{
		KeySource: set, Issuer: KitIssuer, StaffAudience: KitStaffAud, TenantAudience: KitTenantAud,
		Now: m.Clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := v.Verify(ctx, m.StaffToken(t, "sub")); err != nil {
		t.Fatalf("default token under the session key: %v", err)
	}

	otherEC, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	// Each shape must be refused by the check it exists to exercise, named by
	// the verifier's own static message: a forged signature by the signature
	// check, a key of the wrong family by the key and algorithm match, the
	// symmetric token by the algorithm allow list (before any key lookup).
	cases := map[string]struct {
		shape TokenShape
		want  string
	}{
		"forged signature, default key": {TokenShape{SessionKID: true}, "identity: signature did not verify"},
		"forged signature, other EC":    {TokenShape{SessionKID: true, ECKey: otherEC}, "identity: signature did not verify"},
		"RS256 under the EC kid":        {TokenShape{SessionKID: true, Key: m.Keys.RSAActive}, "identity: key does not match algorithm"},
		"HS256 confusion":               {TokenShape{SessionKID: true, Alg: "HS256"}, "identity: algorithm not allowed"},
	}
	for name, c := range cases {
		_, err := v.Verify(ctx, m.Mint(t, StaffClass, "sub", c.shape))
		if err == nil {
			t.Errorf("%s: verified", name)
			continue
		}
		if err.Error() != c.want {
			t.Errorf("%s: refused with %q, want %q", name, err.Error(), c.want)
		}
	}
	// Without SessionKID the same overrides fall to an unknown kid, which is
	// the documented behaviour.
	if _, err := v.Verify(ctx, m.Mint(t, StaffClass, "sub", TokenShape{Key: m.Keys.RSAActive})); err == nil || err.Error() != "identity: unknown key id" {
		t.Errorf("named key without SessionKID: %v", err)
	}
}
