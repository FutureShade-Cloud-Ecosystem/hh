package identity

import (
	"context"
	"testing"
	"time"
)

// Revocable sessions (HH-678): every minted session token carries a jti,
// the revocation store holds revocations by that id, and the verifier
// refuses a token whose id stands revoked.

func TestMintedTokensCarryAFreshJTI(t *testing.T) {
	m := newTestMinter(t, freshPriv(t))
	set, err := NewSessionKeySet(m.PublicKey(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	v := verifierOver(t, set, nil)
	now := time.Now()
	first, err := m.MintAt("console-user-jti", time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.MintAt("console-user-jti", time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := v.Verify(context.Background(), first)
	if err != nil {
		t.Fatalf("verify first: %v", err)
	}
	if len(tok.JTI) != 32 {
		t.Fatalf("jti = %q, want 32 hex chars", tok.JTI)
	}
	tok2, err := v.Verify(context.Background(), second)
	if err != nil {
		t.Fatalf("verify second: %v", err)
	}
	if tok2.JTI == tok.JTI {
		t.Fatalf("two mints share one jti %q", tok.JTI)
	}
}

func TestVerifierWithoutRevocationsIgnoresTheCheck(t *testing.T) {
	m := newTestMinter(t, freshPriv(t))
	set, _ := NewSessionKeySet(m.PublicKey(), nil, nil)
	v := verifierOver(t, set, nil)
	token, err := m.Mint("console-user-no-store", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// No revocation source is attached: a jti-bearing token still verifies
	// (the store's absence is not a refusal), and a token without a jti
	// never consults one.
	if _, err := v.Verify(context.Background(), token); err != nil {
		t.Fatalf("verify with no store: %v", err)
	}
}

func TestSessionRevocationsInMemoryHoldsAndExpires(t *testing.T) {
	now := time.Now()
	clk := func() time.Time { return now }
	s := NewSessionRevocations(nil).WithClock(clk)
	ctx := context.Background()

	if revoked, err := s.IsRevoked(ctx, ""); err != nil || revoked {
		t.Fatalf("empty jti: %v %v", revoked, err)
	}
	if revoked, err := s.IsRevoked(ctx, "unknown-jti"); err != nil || revoked {
		t.Fatalf("unknown jti: %v %v", revoked, err)
	}
	if err := s.Revoke(ctx, "live-jti", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if revoked, err := s.IsRevoked(ctx, "live-jti"); err != nil || !revoked {
		t.Fatalf("live jti: %v %v", revoked, err)
	}
	// A jti past its exp is dead by itself: Revoke writes nothing and
	// IsRevoked answers false.
	if err := s.Revoke(ctx, "dead-jti", now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if revoked, err := s.IsRevoked(ctx, "dead-jti"); err != nil || revoked {
		t.Fatalf("dead jti: %v %v", revoked, err)
	}
	// A blank jti writes nothing.
	if err := s.Revoke(ctx, "   ", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
}

func TestVerifierRefusesARevokedToken(t *testing.T) {
	m := newTestMinter(t, freshPriv(t))
	set, _ := NewSessionKeySet(m.PublicKey(), nil, nil)
	now := time.Now()
	revocations := NewSessionRevocations(nil).WithClock(func() time.Time { return now })
	v := verifierOver(t, set, func() time.Time { return now })
	v.SetRevocations(revocations)

	token, err := m.MintAt("console-user-revoked", 2*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(context.Background(), token); err != nil {
		t.Fatalf("verify before revocation: %v", err)
	}
	verified, err := v.Verify(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if err := revocations.Revoke(context.Background(), verified.JTI, verified.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	_, err = v.Verify(context.Background(), token)
	wantClass(t, err, ClassCredential)
	ve, ok := err.(*VerifyError)
	if !ok || ve.Reason() != "token revoked" {
		t.Fatalf("err = %v, want token revoked", err)
	}
}

func TestVerifierRefusesARevokedTokenSetThroughConfig(t *testing.T) {
	m := newTestMinter(t, freshPriv(t))
	set, _ := NewSessionKeySet(m.PublicKey(), nil, nil)
	now := time.Now()
	revocations := NewSessionRevocations(nil).WithClock(func() time.Time { return now })
	v, err := NewVerifier(Config{
		KeySource: set, Issuer: signerIssuer,
		StaffAudience: signerAud, TenantAudience: signerTenant,
		Now: func() time.Time { return now }, Revocations: revocations,
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := m.MintAt("console-user-cfg", time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := v.Verify(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if err := revocations.Revoke(context.Background(), tok.JTI, tok.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	_, err = v.Verify(context.Background(), token)
	wantClass(t, err, ClassCredential)
}
