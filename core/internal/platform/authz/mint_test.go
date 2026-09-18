package authz

// Test fixtures for the middleware core. Token minting is test-only: the
// package under test verifies, never signs. Key material is generated at
// runtime and discarded with the process (QA B1 inventory, SW-0).

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

const (
	fixtureIssuer    = "test-issuer"
	fixtureStaffAud  = "hh-staff"
	fixtureTenantAud = "hh-tenant"
	fixtureKeyID     = "fixture-rsa-active"
	fixtureSubject   = "platform-user-1"
	fixtureTenant    = "0d9f6a34-2222-4c1c-9c11-000000000002"
	fixtureOrgSlug   = "yardone"
	fixtureOrgID     = "5b1c9e77-3333-4c1c-9c11-000000000003"
	fixtureOtherSlug = "otheryard"
	fixtureOtherID   = "5b1c9e77-4444-4c1c-9c11-000000000004"
)

// testClock is a mutable clock so the D28 cache window is driven without
// sleeping.
type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time { return c.now }

// advance moves the clock, driving the D28 cache window without sleeping.
func (c *testClock) advance(d time.Duration) { c.now = c.now.Add(d) }

// fakeMembership is a counting MembershipSource double whose answers the
// tests flip between calls (QA finding F4).
type fakeMembership struct {
	mu          sync.Mutex
	member      func(platformUserID, orgSlug string) (bool, error)
	orgs        func(platformUserID string) ([]string, error)
	memberCalls int
	orgsCalls   int
}

func (f *fakeMembership) OrgMember(ctx context.Context, uid, slug string) (bool, error) {
	f.mu.Lock()
	f.memberCalls++
	fn := f.member
	f.mu.Unlock()
	return fn(uid, slug)
}

func (f *fakeMembership) TenantOrgs(ctx context.Context, uid string) ([]string, error) {
	f.mu.Lock()
	f.orgsCalls++
	fn := f.orgs
	f.mu.Unlock()
	return fn(uid)
}

func (f *fakeMembership) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.memberCalls, f.orgsCalls
}

// fakeResolver is a counting OrgResolver double: the call count stands in
// for executed core-database statements on the middleware legs, since the
// resolver is the only database seam the middleware holds.
type fakeResolver struct {
	mu      sync.Mutex
	resolve func(slug string) (string, bool, error)
	calls   int
}

func (f *fakeResolver) ResolveSlug(ctx context.Context, slug string) (string, bool, error) {
	f.mu.Lock()
	f.calls++
	fn := f.resolve
	f.mu.Unlock()
	return fn(slug)
}

func (f *fakeResolver) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fixture wires a Gate over runtime-generated keys, counting doubles and
// a mutable clock.
type fixture struct {
	keys     *rsa.PrivateKey
	verifier *identity.Verifier
	members  *fakeMembership
	orgs     *fakeResolver
	gate     *Gate
	clock    *testClock
	member   bool
	orgList  []string
	slugOK   map[string]string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	clk := &testClock{now: time.Now()}
	verifier, err := identity.NewVerifier(identity.Config{
		KeySource: identity.NewStaticKeySource(map[string]crypto.PublicKey{
			fixtureKeyID: &key.PublicKey,
		}),
		Issuer:         fixtureIssuer,
		StaffAudience:  fixtureStaffAud,
		TenantAudience: fixtureTenantAud,
		Now:            clk.Now,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	f := &fixture{
		keys:     key,
		verifier: verifier,
		clock:    clk,
		member:   true,
		orgList:  []string{fixtureOrgSlug},
		slugOK:   map[string]string{fixtureOrgSlug: fixtureOrgID, fixtureOtherSlug: fixtureOtherID},
	}
	f.members = &fakeMembership{
		member: func(uid, slug string) (bool, error) { return f.member, nil },
		orgs:   func(uid string) ([]string, error) { return f.orgList, nil },
	}
	f.orgs = &fakeResolver{
		resolve: func(slug string) (string, bool, error) {
			id, ok := f.slugOK[slug]
			return id, ok, nil
		},
	}
	f.gate, err = NewGate(verifier, f.members, f.orgs)
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	return f
}

// staffToken mints a staff-class token.
func (f *fixture) staffToken(t *testing.T, sub string) string {
	return f.mint(t, map[string]any{
		"iss": fixtureIssuer,
		"sub": sub,
		"aud": fixtureStaffAud,
		"iat": f.clock.now.Add(-time.Minute).Unix(),
		"exp": f.clock.now.Add(10 * time.Minute).Unix(),
	})
}

// tenantToken mints a tenant-class token.
func (f *fixture) tenantToken(t *testing.T, sub, tenant string) string {
	return f.mint(t, map[string]any{
		"iss":    fixtureIssuer,
		"sub":    sub,
		"aud":    fixtureTenantAud,
		"iat":    f.clock.now.Add(-time.Minute).Unix(),
		"exp":    f.clock.now.Add(10 * time.Minute).Unix(),
		"tenant": tenant,
	})
}

// expiredStaffToken mints a staff token whose exp has passed.
func (f *fixture) expiredStaffToken(t *testing.T, sub string) string {
	return f.mint(t, map[string]any{
		"iss": fixtureIssuer,
		"sub": sub,
		"aud": fixtureStaffAud,
		"iat": f.clock.now.Add(-2 * time.Hour).Unix(),
		"exp": f.clock.now.Add(-time.Hour).Unix(),
	})
}

// confusionToken mints the HS256 alg-confusion token, signed with the
// verifier's own public key material.
func (f *fixture) confusionToken(t *testing.T, sub string) string {
	return f.mintHMAC(t, map[string]any{
		"iss": fixtureIssuer,
		"sub": sub,
		"aud": fixtureStaffAud,
		"iat": f.clock.now.Add(-time.Minute).Unix(),
		"exp": f.clock.now.Add(10 * time.Minute).Unix(),
	})
}

func (f *fixture) mint(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": "RS256", "kid": fixtureKeyID, "typ": "JWT"}
	return f.sign(t, header, claims)
}

func (f *fixture) mintHMAC(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": "HS256", "kid": fixtureKeyID, "typ": "JWT"}
	return f.sign(t, header, claims)
}

func (f *fixture) sign(t *testing.T, header, claims map[string]any) string {
	t.Helper()
	hb, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	signed := b64(hb) + "." + b64(cb)
	var sig []byte
	switch header["alg"] {
	case "RS256":
		digest := sha256.Sum256([]byte(signed))
		sig, err = rsa.SignPKCS1v15(rand.Reader, f.keys, crypto.SHA256, digest[:])
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
	case "HS256":
		der, err2 := x509PublicKeyDER(&f.keys.PublicKey)
		if err2 != nil {
			t.Fatalf("marshal public key: %v", err2)
		}
		mac := hmacSHA256(der, []byte(signed))
		sig = mac
	default:
		t.Fatalf("cannot sign alg %v", header["alg"])
	}
	return signed + "." + b64(sig)
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// x509PublicKeyDER and hmacSHA256 exist only to build the alg-confusion
// token: the symmetric secret is the verifier's own public key material,
// which is the classic attack on verifiers that trust the token's alg.
func x509PublicKeyDER(pub *rsa.PublicKey) ([]byte, error) {
	return x509.MarshalPKIXPublicKey(pub)
}

func hmacSHA256(key, msg []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(msg)
	return mac.Sum(nil)
}

// recorder records handler reach and the scope the handler saw.
type recorder struct {
	mu      sync.Mutex
	reached int
	staff   StaffScope
	tenant  TenantScope
}

func (rec *recorder) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.reached++
		var ok bool
		rec.staff, ok = StaffScopeFrom(r.Context())
		if !ok {
			rec.staff = StaffScope{}
		}
		rec.tenant, ok = TenantScopeFrom(r.Context())
		if !ok {
			rec.tenant = TenantScope{}
		}
		rec.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok":true}`)
	})
}

func (rec *recorder) result() (int, StaffScope, TenantScope) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.reached, rec.staff, rec.tenant
}
