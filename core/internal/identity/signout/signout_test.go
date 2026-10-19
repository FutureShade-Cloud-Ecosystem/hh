package signout

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

const (
	testIssuer = "hh-test-signout"
	staffAud   = "hh-staff"
	tenantAud  = "hh-tenant"
)

type fixture struct {
	verifier    *identity.Verifier
	minter      *identity.SessionMinter
	revocations *identity.SessionRevocations
	handler     http.Handler
	clk         func() time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	minter, err := identity.NewSessionMinter(key, testIssuer, staffAud)
	if err != nil {
		t.Fatal(err)
	}
	set, err := identity.NewSessionKeySet(minter.PublicKey(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	clk := func() time.Time { return now }
	revocations := identity.NewSessionRevocations(nil).WithClock(clk)
	verifier, err := identity.NewVerifier(identity.Config{
		KeySource: set, Issuer: testIssuer,
		StaffAudience: staffAud, TenantAudience: tenantAud,
		Now: clk, Revocations: revocations,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{verifier: verifier, minter: minter, revocations: revocations, handler: NewHandler(verifier, revocations), clk: clk}
}

func (f *fixture) mint(t *testing.T, at time.Time) string {
	t.Helper()
	token, err := f.minter.MintAt("console-user-signout", time.Hour, at)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func post(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/sign-out", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func assertOK(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || !out.OK {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestSignOutRevokesTheToken(t *testing.T) {
	f := newFixture(t)
	token := f.mint(t, f.clk())
	assertOK(t, post(t, f.handler, `{"token":"`+token+`"}`))
	// The revoked token no longer verifies: the credential refusal.
	_, err := f.verifier.Verify(context.Background(), token)
	if err == nil {
		t.Fatal("the signed out token still verifies")
	}
	ve, ok := err.(*identity.VerifyError)
	if !ok || ve.Class() != identity.ClassCredential || ve.Reason() != "token revoked" {
		t.Fatalf("err = %v, want credential token revoked", err)
	}
}

func TestSignOutIsIdempotentForRevokedAndExpired(t *testing.T) {
	f := newFixture(t)
	token := f.mint(t, f.clk())
	assertOK(t, post(t, f.handler, `{"token":"`+token+`"}`))
	// The second presentation of the now revoked token: still ok.
	assertOK(t, post(t, f.handler, `{"token":"`+token+`"}`))
	// An expired token: already dead, which is what a sign out asks for.
	expired := f.mint(t, f.clk().Add(-2*time.Hour))
	assertOK(t, post(t, f.handler, `{"token":"`+expired+`"}`))
}

func TestSignOutRefusesAForgedToken(t *testing.T) {
	f := newFixture(t)
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherMinter, err := identity.NewSessionMinter(other, testIssuer, staffAud)
	if err != nil {
		t.Fatal(err)
	}
	forged, err := otherMinter.Mint("console-user-forged", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	rec := post(t, f.handler, `{"token":"`+forged+`"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("Missing or invalid credentials")) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

// The route gate's probe legs all send a body with other fields and never
// read the Authorization header: every leg meets the same 400 at the body
// gate, before any verification runs.
func TestSignOutBodyGateAnswersEveryLegTheSame400(t *testing.T) {
	f := newFixture(t)
	legs := []string{"", `{"typed_name":"Fixture Homeowner","accepted":true}`, `{"token":"a.b.c","typed_name":"x"}`, `not json`}
	want := ""
	for _, body := range legs {
		rec := post(t, f.handler, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q: status = %d, want 400", body, rec.Code)
		}
		if want == "" {
			want = rec.Body.String()
			continue
		}
		if rec.Body.String() != want {
			t.Fatalf("body %q answered %s, want %s", body, rec.Body.String(), want)
		}
	}
}

func TestSignOutTokenShapeGate(t *testing.T) {
	f := newFixture(t)
	for _, body := range []string{`{}`, `{"token":""}`, `{"token":"   "}`, `{"token":"no-dots"}`, `{"token":"a.b"}`} {
		rec := post(t, f.handler, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q: status = %d, want 400", body, rec.Code)
		}
	}
}

func TestSignOutWithoutAStoreStillAnswersOK(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	minter, err := identity.NewSessionMinter(key, testIssuer, staffAud)
	if err != nil {
		t.Fatal(err)
	}
	set, err := identity.NewSessionKeySet(minter.PublicKey(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := identity.NewVerifier(identity.Config{
		KeySource: set, Issuer: testIssuer,
		StaffAudience: staffAud, TenantAudience: tenantAud,
	})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(verifier, nil)
	token, err := minter.Mint("console-user-nostore", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	assertOK(t, post(t, h, `{"token":"`+token+`"}`))
}
