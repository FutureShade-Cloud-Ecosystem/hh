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
	cases := map[string]TokenShape{
		"forged signature, default key": {SessionKID: true},
		"forged signature, other EC":    {SessionKID: true, ECKey: otherEC},
		"RS256 under the EC kid":        {SessionKID: true, Key: m.Keys.RSAActive},
		"HS256 confusion":               {SessionKID: true, Alg: "HS256"},
	}
	for name, shape := range cases {
		_, err := v.Verify(ctx, m.Mint(t, StaffClass, "sub", shape))
		if err == nil {
			t.Errorf("%s: verified", name)
			continue
		}
		if ve, ok := err.(*identity.VerifyError); ok && ve.Error() == "identity: unknown key id" {
			t.Errorf("%s: refused as an unknown kid, not by the check it exercises", name)
		}
	}
	// Without SessionKID the same overrides fall to an unknown kid, which is
	// the documented behaviour.
	if _, err := v.Verify(ctx, m.Mint(t, StaffClass, "sub", TokenShape{Key: m.Keys.RSAActive})); err == nil || err.Error() != "identity: unknown key id" {
		t.Errorf("named key without SessionKID: %v", err)
	}
}
