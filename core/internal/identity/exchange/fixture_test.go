package exchange

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/ratelimit"
)

const (
	fxProject  = "proj-x"
	fxKey      = "server-key-x"
	fxOrgSlug  = "acme"
	fxOrgID    = "11111111-1111-1111-1111-111111111111"
	fxUserID   = "console_user.1"
	fxUsersRow = "22222222-2222-2222-2222-222222222222"
	fxEmail    = "Person@FutureBuild.ai"
	fxIssuer   = "hh-test-deploy"
	fxAudience = "hh-staff"
)

// ---- fakes ----

type fakeMembers struct {
	mu     sync.Mutex
	member bool
	err    error
	calls  int
}

func (f *fakeMembers) OrgMember(_ context.Context, user, slug string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.member, f.err
}
func (f *fakeMembers) TenantOrgs(context.Context, string) ([]string, error) { return nil, nil }
func (f *fakeMembers) set(m bool)                                           { f.mu.Lock(); f.member = m; f.mu.Unlock() }

type fakeOrgs struct {
	ok  bool
	err error
}

func (f *fakeOrgs) ResolveSlug(_ context.Context, slug string) (string, bool, error) {
	return fxOrgID, f.ok, f.err
}

type fakeClaims struct {
	mu    sync.Mutex
	seen  map[[32]byte]bool
	err   error
	bound time.Duration
	now   time.Time
	calls int
}

func (f *fakeClaims) ClaimJWT(_ context.Context, h [32]byte, exp time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return false, f.err
	}
	if exp.IsZero() || !exp.After(f.now) || exp.After(f.now.Add(f.bound)) {
		return false, identity.ErrJWTExpOutOfBound
	}
	if f.seen[h] {
		return false, nil
	}
	f.seen[h] = true
	return true, nil
}

type fakeStaff struct {
	linkedID     string
	linked       bool
	linkedErr    error
	authority    identity.StaffAuthority
	authorityOK  bool
	authorityErr error
	label        string
	labelOK      bool
	labelErr     error
}

func (f *fakeStaff) LinkedActiveUser(context.Context, string) (string, bool, error) {
	return f.linkedID, f.linked, f.linkedErr
}
func (f *fakeStaff) ResolveStaff(context.Context, string, string) (identity.StaffAuthority, bool, error) {
	return f.authority, f.authorityOK, f.authorityErr
}
func (f *fakeStaff) StaffRoleLabel(context.Context, string) (string, bool, error) {
	return f.label, f.labelOK, f.labelErr
}

type fakeKnob struct {
	hours int
	err   error
}

func (f fakeKnob) DriverSessionHours(context.Context, string) (int, error) { return f.hours, f.err }

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

type fakeMethods struct {
	mu       sync.Mutex
	calls    []string // the org id of each read
	set      []string
	err      error
	callsLog *[]string
}

func (f *fakeMethods) AllowedSignInMethods(_ context.Context, orgID string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, orgID)
	if f.callsLog != nil {
		*f.callsLog = append(*f.callsLog, "methods")
	}
	if f.err != nil {
		return nil, f.err
	}
	return append([]string(nil), f.set...), nil
}

// recordingMinter wraps the real minter and notes each mint.
type recordingMinter struct {
	inner *identity.SessionMinter
	mu    sync.Mutex
	calls int
	log   *[]string
}

func (m *recordingMinter) MintAt(subject string, window time.Duration, at time.Time) (string, error) {
	m.mu.Lock()
	m.calls++
	if m.log != nil {
		*m.log = append(*m.log, "mint")
	}
	m.mu.Unlock()
	return m.inner.MintAt(subject, window, at)
}

// ---- the console stub ----

type consoleStub struct {
	mu         sync.Mutex
	srv        *httptest.Server
	accounts   map[string]string // jwt -> account JSON
	accountSt  int               // forced status for /account, 0 for normal
	sessions   string            // body for /users/*/sessions
	sessionsSt int
	delay      time.Duration
	redirectTo string
	accountHit int32
	sessionHit int32
}

func (s *consoleStub) handle(w http.ResponseWriter, r *http.Request) {
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	if s.redirectTo != "" {
		http.Redirect(w, r, s.redirectTo, http.StatusTemporaryRedirect)
		return
	}
	switch {
	case r.URL.Path == "/account":
		atomic.AddInt32(&s.accountHit, 1)
		if s.accountSt != 0 {
			w.WriteHeader(s.accountSt)
			return
		}
		s.mu.Lock()
		body, ok := s.accounts[r.Header.Get("X-Appwrite-JWT")]
		s.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, body)
	case strings.HasPrefix(r.URL.Path, "/users/") && strings.HasSuffix(r.URL.Path, "/sessions"):
		atomic.AddInt32(&s.sessionHit, 1)
		if r.Header.Get("X-Appwrite-Key") != fxKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if s.sessionsSt != 0 {
			w.WriteHeader(s.sessionsSt)
			return
		}
		fmt.Fprint(w, s.sessions)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// ---- the fixture ----

type fx struct {
	t        *testing.T
	now      time.Time
	console  *consoleStub
	members  *fakeMembers
	orgs     *fakeOrgs
	claims   *fakeClaims
	staff    *fakeStaff
	knob     fakeKnob
	audit    *fakeAudit
	methods  *fakeMethods
	minter   *recordingMinter
	key      *ecdsa.PrivateKey
	cfg      Config
	h        *Handler
	order    []string
	jwtCount int
	logBuf   *bytes.Buffer
}

func accountJSON(id, email string, verified, active bool) string {
	return fmt.Sprintf(`{"$id":%q,"email":%q,"name":"Display Name","emailVerification":%t,"status":%t}`, id, email, verified, active)
}

func sessionsJSON(parts ...string) string {
	return fmt.Sprintf(`{"total":%d,"sessions":[%s]}`, len(parts), strings.Join(parts, ","))
}

func sessionJSON(id string, created time.Time, provider string) string {
	return fmt.Sprintf(`{"$id":%q,"$createdAt":%q,"provider":%q}`, id, created.UTC().Format("2006-01-02T15:04:05.000+00:00"), provider)
}

func newFx(t *testing.T) *fx {
	t.Helper()
	f := &fx{t: t, now: time.Now().Truncate(time.Second)}
	f.console = &consoleStub{accounts: map[string]string{}}
	f.console.sessions = sessionsJSON(sessionJSON("sess_1", f.now.Add(-10*time.Minute), "email"))
	srv := httptest.NewServer(http.HandlerFunc(f.console.handle))
	t.Cleanup(srv.Close)
	f.console.srv = srv
	client, err := authz.NewConsoleClient(srv.URL, fxProject, fxKey, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
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
	f.members = &fakeMembers{member: true}
	f.orgs = &fakeOrgs{ok: true}
	f.claims = &fakeClaims{seen: map[[32]byte]bool{}, bound: identity.MinJWTClaimBound, now: f.now}
	f.staff = &fakeStaff{
		linkedID: fxUsersRow, linked: true,
		authority:   identity.StaffAuthority{RoleID: "yard_team", Tier: 3, Scope: "branch"},
		authorityOK: true, label: "Yard Team", labelOK: true,
	}
	f.knob = fakeKnob{hours: 8}
	f.audit = &fakeAudit{log: &f.order}
	f.methods = &fakeMethods{set: []string{"email_password", "google"}, callsLog: &f.order}
	f.logBuf = &bytes.Buffer{}
	f.cfg = Config{
		Console: client, Members: f.members, Orgs: f.orgs, Claims: f.claims, Staff: f.staff,
		Knob: f.knob, Audit: f.audit, Minter: f.minter, Methods: f.methods,
		AllowedDomains:   []string{"futurebuild.ai"},
		AllowedAddresses: nil,
		TokenTTL:         time.Hour,
		Now:              func() time.Time { return f.now },
		Log:              slog.New(slog.NewTextHandler(f.logBuf, nil)),
	}
	return f
}

// build constructs the handler from the current cfg, with the fakes
// re-read so tests may replace them first.
func (f *fx) build() *Handler {
	f.t.Helper()
	f.cfg.Knob = f.knob
	h, err := New(f.cfg)
	if err != nil {
		f.t.Fatal(err)
	}
	f.h = h
	return h
}

func seg(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

// jwt builds a fresh Console shaped JWT (unique signature) and registers the
// account the stub answers for it.
func (f *fx) jwt(userID, sessionID string, exp time.Time, account string) string {
	f.jwtCount++
	payload := fmt.Sprintf(`{"exp":%d,"projectId":"p","sessionId":%q,"userId":%q}`, exp.Unix(), sessionID, userID)
	sig := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("signature-%d-%d", f.jwtCount, time.Now().UnixNano())))
	tok := seg(`{"alg":"HS256","typ":"JWT"}`) + "." + seg(payload) + "." + sig
	f.console.mu.Lock()
	f.console.accounts[tok] = account
	f.console.mu.Unlock()
	return tok
}

// goodJWT is a JWT that passes everything under the default fixture.
func (f *fx) goodJWT() string {
	return f.jwt(fxUserID, "sess_1", f.now.Add(10*time.Minute), accountJSON(fxUserID, fxEmail, true, true))
}

func (f *fx) post(jwt, org string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"jwt": jwt, "org": org})
	return f.postRaw(string(body), "198.51.100.7:5555")
}

func (f *fx) postRaw(body, peer string) *httptest.ResponseRecorder {
	if f.h == nil {
		f.build()
	}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/exchange", strings.NewReader(body))
	r.RemoteAddr = peer
	f.h.ServeHTTP(rec, r)
	return rec
}

// assertRefusal checks status, the shared body byte for byte, no token, no
// audit row and no mint.
func (f *fx) assertRefusal(rec *httptest.ResponseRecorder, status int, body []byte) {
	f.t.Helper()
	if rec.Code != status {
		f.t.Fatalf("status = %d, want %d (body %q)", rec.Code, status, rec.Body.String())
	}
	if body != nil && !bytes.Equal(rec.Body.Bytes(), body) {
		f.t.Fatalf("body = %q, want %q", rec.Body.Bytes(), body)
	}
	if strings.Contains(rec.Body.String(), "token") {
		f.t.Fatal("a refusal carries a token")
	}
	if len(f.audit.rows) != 0 {
		f.t.Fatalf("a refusal wrote %d audit row(s)", len(f.audit.rows))
	}
	if f.minter.calls != 0 {
		f.t.Fatal("a refusal minted")
	}
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

func decodeAnswer(t *testing.T, rec *httptest.ResponseRecorder) answer {
	t.Helper()
	var a answer
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		t.Fatalf("answer: %v (%s)", err, rec.Body.String())
	}
	return a
}

func tokenWindow(t *testing.T, tok string) (iat, exp int64) {
	t.Helper()
	parts := strings.Split(tok, ".")
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Iat int64 `json:"iat"`
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	return c.Iat, c.Exp
}

var errBoom = errors.New("boom")

var (
	bodyU = authz.BodyUnauthorized
	bodyF = authz.BodyForbidden
	bodyV = authz.BodyUnavailable
)

var _ = ratelimit.DefaultBurstWindow
