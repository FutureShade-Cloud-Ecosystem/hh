package signout

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

// errWriteRefused stands in for the Postgres row write failing while the
// read path still answers (the review's write only failure shape).
var errWriteRefused = errors.New("row write refused")

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

// P2-2 (review): when the store's row write fails, the retry on the same
// instance must never meet the idempotent 200 arm with the row still
// missing. First two writes fail, the third lands; a fourth call is a
// fast idempotent no-op.
func TestSignOutRetriesTheFailedRowWrite(t *testing.T) {
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

	var writes int
	var writeErr error
	revocations := identity.NewSessionRevocations(nil).
		WithClock(clk).
		WithWriteRow(func(_ context.Context, _ string, _, _ time.Time) error { writes++; return writeErr })
	verifier, err := identity.NewVerifier(identity.Config{
		KeySource: set, Issuer: testIssuer,
		StaffAudience: staffAud, TenantAudience: tenantAud,
		Now: clk, Revocations: revocations,
	})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(verifier, revocations)
	token, err := minter.MintAt("console-user-flaky", time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"token":"` + token + `"}`

	// First call: the row write fails, the answer is 503, while the token
	// already reads revoked on this instance.
	writeErr = errWriteRefused
	if rec := post(t, h, body); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("first sign out: %d %s, want 503", rec.Code, rec.Body.String())
	}
	_, err = verifier.Verify(context.Background(), token)
	ve, ok := err.(*identity.VerifyError)
	if !ok || ve.Reason() != "token revoked" {
		t.Fatalf("verify after the failed write: %v, want token revoked", err)
	}

	// The retry while the write still fails: still 503, never the
	// idempotent 200.
	writeErr = errWriteRefused
	if rec := post(t, h, body); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("retry while the write still fails: %d %s, want 503", rec.Code, rec.Body.String())
	}

	// The write lands: 200, and the row write was attempted again.
	writeErr = nil
	if rec := post(t, h, body); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("sign out after the write lands: %d %s", rec.Code, rec.Body.String())
	}
	if writes < 3 {
		t.Fatalf("row writes attempted = %d, want at least 3", writes)
	}

	// The fourth call is idempotent and writes no further row.
	before := writes
	writeErr = nil
	if rec := post(t, h, body); rec.Code != http.StatusOK {
		t.Fatalf("idempotent call: %d %s", rec.Code, rec.Body.String())
	}
	if writes != before {
		t.Fatalf("the idempotent call wrote again: %d then %d", before, writes)
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
