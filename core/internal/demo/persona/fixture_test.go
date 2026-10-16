//go:build demo

package persona

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/flows"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/membership"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

const (
	fxIssuer     = "hh-test-deploy"
	fxAudience   = "hh-staff"
	fxOrgID      = "11111111-1111-1111-1111-111111111111"
	fxPartner    = "66a1b2c3d4e5f60718293a4b"
	fxPartner2   = "66a1b2c3d4e5f60718293a4c"
	fxPartnerRow = "33333333-3333-3333-3333-333333333333"
	fxAddr       = "198.51.100.7:4321"
)

// ---- fakes ----

type fakeOrgs struct {
	ok  bool
	err error
}

func (f *fakeOrgs) ResolveSlug(_ context.Context, slug string) (string, bool, error) {
	if slug != flows.OrgSlug {
		return "", false, nil
	}
	return fxOrgID, f.ok, f.err
}

// fakeStaff answers the partner's linkage and each persona's row and
// authority, the way the identity module does.
type fakeStaff struct {
	mu          sync.Mutex
	partnerRow  string
	partnerOK   bool
	linkedErr   error
	personaOK   bool
	roleDrift   string
	authErr     error
	authMissing bool
	resolved    []string
}

func (f *fakeStaff) LinkedActiveUser(_ context.Context, subject string) (string, bool, error) {
	if f.linkedErr != nil {
		return "", false, f.linkedErr
	}
	if p, ok := flows.PersonaBySubject(subject); ok {
		return p.UserID, f.personaOK, nil
	}
	return f.partnerRow, f.partnerOK, nil
}

func (f *fakeStaff) ResolveStaff(_ context.Context, subject, orgID string) (identity.StaffAuthority, bool, error) {
	f.mu.Lock()
	f.resolved = append(f.resolved, subject)
	f.mu.Unlock()
	if f.authErr != nil {
		return identity.StaffAuthority{}, false, f.authErr
	}
	if f.authMissing {
		return identity.StaffAuthority{}, false, nil
	}
	p, ok := flows.PersonaBySubject(subject)
	if !ok || orgID != fxOrgID {
		return identity.StaffAuthority{}, false, nil
	}
	role := p.Role
	if f.roleDrift != "" {
		role = f.roleDrift
	}
	tier, scope := 3, "branch"
	switch role {
	case "general_manager", "financial_controller", "hr", "dealer_admin":
		tier, scope = 1, "global"
	case "payables_receivables":
		tier, scope = 2, "global"
	case "yard_manager", "sales_manager", "branch_manager", "logistics_manager", "procurement_manager":
		tier = 2
	}
	return identity.StaffAuthority{RoleID: role, Tier: tier, Scope: scope}, true, nil
}

type fakeKnob struct {
	hours int
	err   error
	calls int
}

func (f *fakeKnob) DriverSessionHours(context.Context, string) (int, error) {
	f.calls++
	return f.hours, f.err
}

type fakeAudit struct {
	mu   sync.Mutex
	rows []audit.SensitiveAccess
	err  error
	log  *[]string
}

func (f *fakeAudit) Record(_ context.Context, a audit.SensitiveAccess) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.log != nil {
		*f.log = append(*f.log, "audit")
	}
	if f.err != nil {
		return f.err
	}
	f.rows = append(f.rows, a)
	return nil
}

type recordingMinter struct {
	inner *identity.SessionMinter
	log   *[]string
	err   error
	calls int
	last  struct {
		subject string
		window  time.Duration
	}
}

func (m *recordingMinter) MintAt(subject string, window time.Duration, at time.Time) (string, error) {
	m.calls++
	m.last.subject, m.last.window = subject, window
	*m.log = append(*m.log, "mint")
	if m.err != nil {
		return "", m.err
	}
	return m.inner.MintAt(subject, window, at)
}

// fakeVerifier lets a test choose the bearer's class and error.
type fakeVerifier struct {
	tok   identity.Token
	err   error
	calls int
}

func (v *fakeVerifier) Verify(context.Context, string) (identity.Token, error) {
	v.calls++
	return v.tok, v.err
}

// ---- the fixture ----

type fx struct {
	t       *testing.T
	now     time.Time
	key     *ecdsa.PrivateKey
	order   []string
	minter  *recordingMinter
	verify  *identity.Verifier
	staff   *fakeStaff
	orgs    *fakeOrgs
	knob    *fakeKnob
	audit   *fakeAudit
	logBuf  *bytes.Buffer
	cfg     Config
	h       *Handler
	partner string // a real core bearer for the pinned partner
}

func newFx(t *testing.T, mode CallerMode) *fx {
	t.Helper()
	f := &fx{t: t, now: time.Unix(1_900_000_000, 0).UTC(), logBuf: &bytes.Buffer{}}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f.key = key
	m, err := identity.NewSessionMinter(key, fxIssuer, fxAudience)
	if err != nil {
		t.Fatal(err)
	}
	f.minter = &recordingMinter{inner: m, log: &f.order}
	keys, err := identity.NewSessionKeySet(&key.PublicKey, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.verify, err = identity.NewVerifier(identity.Config{
		KeySource: keys, Issuer: fxIssuer, StaffAudience: fxAudience, TenantAudience: "hh-tenant",
		Now: func() time.Time { return f.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	f.staff = &fakeStaff{partnerRow: fxPartnerRow, partnerOK: true, personaOK: true}
	f.orgs = &fakeOrgs{ok: true}
	f.knob = &fakeKnob{hours: 24}
	f.audit = &fakeAudit{log: &f.order}
	f.cfg = Config{
		Mode: mode, Verifier: f.verify, Partners: []string{fxPartner}, Staff: f.staff, Orgs: f.orgs,
		Knob: f.knob, Audit: f.audit, Minter: f.minter, Now: func() time.Time { return f.now },
		Log: slog.New(slog.NewTextHandler(f.logBuf, nil)),
	}
	if mode == CallerOpen {
		f.cfg.Verifier = nil
		f.cfg.Partners = nil
	}
	f.h, err = New(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	f.partner = f.mintFor(fxPartner)
	return f
}

// rebuild constructs the handler again after a test edited f.cfg.
func (f *fx) rebuild() {
	f.t.Helper()
	h, err := New(f.cfg)
	if err != nil {
		f.t.Fatal(err)
	}
	f.h = h
}

func (f *fx) mintFor(subject string) string {
	f.t.Helper()
	tok, err := f.minter.inner.MintAt(subject, time.Hour, f.now)
	if err != nil {
		f.t.Fatal(err)
	}
	return tok
}

func (f *fx) do(body, bearer, remote string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/persona", strings.NewReader(body))
	req.RemoteAddr = remote
	if bearer != "" {
		req.Header.Set("Authorization", bearer)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Add(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	return rec
}

// enter is the common call: the pinned partner's bearer from a fixed
// address.
func (f *fx) enter(id string) *httptest.ResponseRecorder {
	return f.do(`{"persona":"`+id+`"}`, "Bearer "+f.partner, fxAddr)
}

// enterOpen is the public copy's call: no bearer.
func (f *fx) enterOpen(id, remote string) *httptest.ResponseRecorder {
	return f.do(`{"persona":"`+id+`"}`, "", remote)
}

type answer struct {
	Token       string `json:"token"`
	ExpiresAtMs int64  `json:"expiresAtMs"`
	User        struct {
		UserID      string `json:"user_id"`
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
		RoleCode    string `json:"role_code"`
		RoleName    string `json:"role_name"`
		Tier        int    `json:"tier"`
		Scope       string `json:"scope"`
	} `json:"user"`
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) answer {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var a answer
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	return a
}

// claims reads a token's payload without verifying it.
func claims(t *testing.T, token string) (sub string, iat, exp int64) {
	t.Helper()
	parts := strings.Split(token, ".")
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Sub string `json:"sub"`
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	return c.Sub, c.Iat, c.Exp
}

// assertShared asserts a refusal is one of the shared bodies byte for
// byte, carries no token and left no mint and no audit row.
func (f *fx) assertShared(rec *httptest.ResponseRecorder, status int) {
	f.t.Helper()
	var want []byte
	switch status {
	case http.StatusUnauthorized:
		want = authz.BodyUnauthorized
	case http.StatusForbidden:
		want = authz.BodyForbidden
	case http.StatusServiceUnavailable:
		want = authz.BodyUnavailable
	default:
		f.t.Fatalf("no shared body for %d", status)
	}
	if rec.Code != status || rec.Body.String() != string(want) {
		f.t.Fatalf("got %d %q, want %d %q", rec.Code, rec.Body, status, want)
	}
	f.assertNothingMinted()
}

func (f *fx) assertNothingMinted() {
	f.t.Helper()
	if f.minter.calls != 0 {
		f.t.Fatalf("a token was minted %d times", f.minter.calls)
	}
	if len(f.audit.rows) != 0 {
		f.t.Fatalf("%d audit rows written", len(f.audit.rows))
	}
}

var errBoom = errors.New("boom")

// identityMinterFor mints a bearer for the pinned partner under a key the
// verifier does not hold.
func identityMinterFor(t *testing.T, _ string) (string, *identity.SessionMinter) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	m, err := identity.NewSessionMinter(key, fxIssuer, fxAudience)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := m.MintAt(fxPartner, time.Hour, time.Unix(1_900_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	return tok, m
}

type failingKeys struct{}

func (failingKeys) KeyByID(context.Context, string) (crypto.PublicKey, bool, error) {
	return nil, false, errBoom
}

// operationalError returns the real verifier's operational failure: the key
// source cannot answer.
func operationalError(t *testing.T) error {
	t.Helper()
	v, err := identity.NewVerifier(identity.Config{KeySource: failingKeys{}, Issuer: fxIssuer, StaffAudience: fxAudience, TenantAudience: "hh-tenant"})
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := identityMinterFor(t, "")
	_, verr := v.Verify(context.Background(), tok)
	var ve *identity.VerifyError
	if !errors.As(verr, &ve) || ve.Class() != identity.ClassOperational {
		t.Fatalf("the failing key source gave %v", verr)
	}
	return verr
}

// consoleDouble stands for FB Console behind its cache: it never answers
// membership for anyone in these tests, and fails the test if it is asked
// about a persona subject.
type consoleDouble struct{ t *testing.T }

func (c consoleDouble) OrgMember(_ context.Context, id, _ string) (bool, error) {
	if strings.Contains(id, ":") {
		c.t.Fatalf("console was asked about %q", id)
	}
	return false, nil
}
func (consoleDouble) TenantOrgs(context.Context, string) ([]string, error) { return nil, nil }

func compositeFor(t *testing.T) authz.MembershipSource {
	t.Helper()
	c, err := membership.NewComposite(consoleDouble{t})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
