package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// consoleStack is FB Console as the exchange sees it: the account read
// under a JWT, the session listing, and (proxied to the membership double
// the composition suites already use) the Team memberships.
type consoleStack struct {
	mu       sync.Mutex
	accounts map[string]string
	sessions map[string]string
	server   *httptest.Server
	n        int
}

func newConsoleStack(t *testing.T, env *compEnv) *consoleStack {
	t.Helper()
	s := &consoleStack{accounts: map[string]string{}, sessions: map[string]string{}}
	target, _ := url.Parse(env.appwrite.server.URL)
	proxy := httputil.NewSingleHostReverseProxy(target)
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch {
		case r.URL.Path == "/account":
			body, ok := s.accounts[r.Header.Get("X-Appwrite-JWT")]
			if !ok {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			fmt.Fprint(w, body)
		case strings.HasSuffix(r.URL.Path, "/sessions"):
			sub := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/users/"), "/sessions")
			fmt.Fprint(w, s.sessions[sub])
		default:
			proxy.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(s.server.Close)
	return s
}

// jwtFor registers a fresh Console JWT for the subject and returns it.
func (s *consoleStack) jwtFor(sub, email string, verified bool) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	b := func(x string) string { return base64.RawURLEncoding.EncodeToString([]byte(x)) }
	payload := fmt.Sprintf(`{"exp":%d,"sessionId":"sess-%s","userId":%q}`, time.Now().Add(10*time.Minute).Unix(), sub, sub)
	tok := b(`{"alg":"HS256"}`) + "." + b(payload) + "." + b(fmt.Sprintf("sig-%s-%d-%d", sub, s.n, time.Now().UnixNano()))
	s.accounts[tok] = fmt.Sprintf(`{"$id":%q,"email":%q,"name":"Fixture Person","emailVerification":%t,"status":true}`, sub, email, verified)
	s.sessions[sub] = fmt.Sprintf(`{"total":1,"sessions":[{"$id":"sess-%s","$createdAt":%q,"provider":"email"}]}`, sub, time.Now().Add(-5*time.Minute).UTC().Format("2006-01-02T15:04:05.000+00:00"))
	return tok
}

// setProvider rewrites the provider of the subject's one Console session.
func (s *consoleStack) setProvider(sub, provider string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sub] = strings.Replace(s.sessions[sub], `"provider":"email"`, fmt.Sprintf(`"provider":%q`, provider), 1)
}

func getMethods(h http.Handler, query string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/methods"+query, nil)
	r.RemoteAddr = "198.51.100.21:4000"
	h.ServeHTTP(rec, r)
	return rec
}

func postTenantExchange(h http.Handler, jwt, org string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"jwt": jwt, "org": org, "class": "tenant"})
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/exchange", strings.NewReader(string(body)))
	r.RemoteAddr = "198.51.100.22:4000"
	h.ServeHTTP(rec, r)
	return rec
}

func postExchange(h http.Handler, jwt, org string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"jwt": jwt, "org": org})
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/exchange", strings.NewReader(string(body)))
	r.RemoteAddr = "198.51.100.20:4000"
	h.ServeHTTP(rec, r)
	return rec
}

// The default build, console mode, over the real database: the exchange is
// mounted, mints against the fixture's own staff rows, and the minted token
// is accepted by a real staff route behind authz.Gate, the role gate and the
// guards. Replay, unverified email, membership removal between two
// exchanges, a suspended row and a data defect each refuse with the shared
// bodies, and a refusal leaves no AUTH_EXCHANGE row.
func TestBootConsoleExchangeEndToEnd(t *testing.T) {
	env := newCompEnv(t)
	stack := newConsoleStack(t, env)
	env.set("CORE_APPWRITE_ENDPOINT", stack.server.URL)
	env.set("CORE_SIGNIN_EMAIL_DOMAINS", "fixture.test")
	rec, graph := registeredSurface(t, env)
	h := rec.inner

	if rsp := postExchange(h, "", ""); rsp.Code != http.StatusBadRequest {
		t.Fatalf("empty body = %d", rsp.Code)
	}

	count := func() int {
		var n int
		if err := graph.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action = 'AUTH_EXCHANGE'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	base := count()

	sub := testkit.SubSalesManager
	good := stack.jwtFor(sub, "fxstaff.sales.manager@fixture.test", true)
	rsp := postExchange(h, good, testkit.OrgNorthSlug)
	if rsp.Code != http.StatusOK {
		t.Fatalf("exchange = %d %s", rsp.Code, rsp.Body)
	}
	var out struct {
		Token string `json:"token"`
		User  struct {
			UserID, RoleCode, RoleName, Scope string
			Tier                              int
		} `json:"-"`
	}
	_ = json.Unmarshal(rsp.Body.Bytes(), &out)
	if out.Token == "" {
		t.Fatal("no token")
	}
	if count() != base+1 {
		t.Fatalf("audit rows %d -> %d, want one AUTH_EXCHANGE row", base, count())
	}

	// The minted token works on a real staff route.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/needs", nil)
	req.Header.Set("Authorization", "Bearer "+out.Token)
	req.Header.Set("X-Org", testkit.OrgNorthSlug)
	got := httptest.NewRecorder()
	h.ServeHTTP(got, req)
	if got.Code != http.StatusOK {
		t.Fatalf("staff route with the minted token = %d %s", got.Code, got.Body)
	}

	refused := func(name string, rsp *httptest.ResponseRecorder, status int) {
		t.Helper()
		if rsp.Code != status {
			t.Fatalf("%s = %d %s, want %d", name, rsp.Code, rsp.Body, status)
		}
		if strings.Contains(rsp.Body.String(), "token") {
			t.Fatalf("%s carries a token", name)
		}
	}
	before := count()
	refused("replay", postExchange(h, good, testkit.OrgNorthSlug), 401)
	refused("unverified email", postExchange(h, stack.jwtFor(testkit.SubInsideSales, "fxstaff.inside.sales@fixture.test", false), testkit.OrgNorthSlug), 403)
	refused("domain outside the list", postExchange(h, stack.jwtFor(testkit.SubInsideSales, "someone@elsewhere.example", true), testkit.OrgNorthSlug), 403)
	refused("suspended row", postExchange(h, stack.jwtFor(testkit.SubSuspended, "fxstaff.suspended@fixture.test", true), testkit.OrgNorthSlug), 403)
	refused("grant shape defect", postExchange(h, stack.jwtFor(testkit.SubInert, "fxstaff.inert@fixture.test", true), testkit.OrgNorthSlug), 503)
	refused("revoked grant", postExchange(h, stack.jwtFor(testkit.SubRevoked, "fxstaff.revoked@fixture.test", true), testkit.OrgNorthSlug), 403)
	refused("not in that org's Team", postExchange(h, stack.jwtFor(testkit.SubInsideSales, "fxstaff.inside.sales@fixture.test", true), testkit.OrgSouthSlug), 403)
	if count() != before {
		t.Fatalf("a refusal wrote an AUTH_EXCHANGE row (%d -> %d)", before, count())
	}

	// Membership removal takes effect at the next exchange although the
	// per request cache inside the gate has not lapsed.
	other := testkit.SubInsideSales
	if rsp := postExchange(h, stack.jwtFor(other, "fxstaff.inside.sales@fixture.test", true), testkit.OrgNorthSlug); rsp.Code != 200 {
		t.Fatalf("second person = %d %s", rsp.Code, rsp.Body)
	}
	env.appwrite.setTeam(other)
	refused("removed from the Team", postExchange(h, stack.jwtFor(other, "fxstaff.inside.sales@fixture.test", true), testkit.OrgNorthSlug), 403)
}

// The exchange's configuration refusals stop the real boot.
func TestBootConsoleRefusesBadExchangeConfiguration(t *testing.T) {
	for name, kv := range map[string][2]string{
		"bound below the console JWT life": {"CORE_EXCHANGE_JWT_BOUND", "600s"},
		"proxy list not addresses":         {"CORE_TRUSTED_PROXIES", "nope"},
	} {
		env := newCompEnv(t)
		env.set(kv[0], kv[1])
		if _, err := registerRoutes(&recordingRegistrar{inner: http.NewServeMux()}, env.getenv); err == nil {
			t.Errorf("%s: booted", name)
		}
	}
}

// With no allow list configured the route is mounted and refuses every
// sign in.
func TestBootConsoleExchangeWithEmptyListsRefusesEveryone(t *testing.T) {
	env := newCompEnv(t)
	stack := newConsoleStack(t, env)
	env.set("CORE_APPWRITE_ENDPOINT", stack.server.URL)
	rec, _ := registeredSurface(t, env)
	rsp := postExchange(rec.inner, stack.jwtFor(testkit.SubSalesManager, "fxstaff.sales.manager@fixture.test", true), testkit.OrgNorthSlug)
	if rsp.Code != 403 {
		t.Fatalf("= %d %s", rsp.Code, rsp.Body)
	}
}

// The dealer's allowed sign in methods over the real database (item 6c): the
// public route answers the default for an org with no row and for an unknown
// or malformed slug, byte for byte; a row narrows both the route and gate 4b;
// an unknown code in the table is a refusal on the route (503) and at the
// exchange (503), never a smaller set.
func TestBootConsoleSignInMethods(t *testing.T) {
	env := newCompEnv(t)
	stack := newConsoleStack(t, env)
	env.set("CORE_APPWRITE_ENDPOINT", stack.server.URL)
	env.set("CORE_SIGNIN_EMAIL_DOMAINS", "fixture.test")
	rec, graph := registeredSurface(t, env)
	h := rec.inner
	ctx := context.Background()

	const defaultBody = `{"methods":["email_password","google"]}` + "\n"
	for name, q := range map[string]string{
		"org with no row": "?org=" + testkit.OrgNorthSlug,
		"unknown slug":    "?org=no-such-dealer",
		"malformed slug":  "?org=NOT%20A%20SLUG",
		"no org":          "",
	} {
		if r := getMethods(h, q); r.Code != 200 || r.Body.String() != defaultBody {
			t.Fatalf("%s = %d %q", name, r.Code, r.Body)
		}
	}

	var orgID string
	if err := graph.pool.QueryRow(ctx, `SELECT id::text FROM org WHERE slug = $1`, testkit.OrgNorthSlug).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = graph.pool.Exec(ctx, `DELETE FROM org_signin_method WHERE org_id = $1`, orgID) })
	sub := testkit.SubSalesManager
	email := "fxstaff.sales.manager@fixture.test"

	// A dealer with only google: the route says so, an email session is refused.
	if _, err := graph.pool.Exec(ctx, `INSERT INTO org_signin_method (org_id, code) VALUES ($1, 'google')`, orgID); err != nil {
		t.Fatal(err)
	}
	if r := getMethods(h, "?org="+testkit.OrgNorthSlug); r.Code != 200 || r.Body.String() != `{"methods":["google"]}`+"\n" {
		t.Fatalf("google only = %d %q", r.Code, r.Body)
	}
	if r := getMethods(h, "?org=no-such-dealer"); r.Body.String() != defaultBody {
		t.Fatalf("an unknown slug stopped answering the default: %q", r.Body)
	}
	refusedBody := postExchange(h, stack.jwtFor(sub, email, true), testkit.OrgNorthSlug)
	if refusedBody.Code != 403 || strings.Contains(refusedBody.Body.String(), "token") {
		t.Fatalf("email session at a google only dealer = %d %s", refusedBody.Code, refusedBody.Body)
	}
	// The reverse: a dealer with only email_password refuses a google session.
	if _, err := graph.pool.Exec(ctx, `DELETE FROM org_signin_method WHERE org_id = $1`, orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := graph.pool.Exec(ctx, `INSERT INTO org_signin_method (org_id, code) VALUES ($1, 'email_password')`, orgID); err != nil {
		t.Fatal(err)
	}
	g := stack.jwtFor(sub, email, true)
	stack.setProvider(sub, "google")
	if r := postExchange(h, g, testkit.OrgNorthSlug); r.Code != 403 {
		t.Fatalf("google session at an email only dealer = %d %s", r.Code, r.Body)
	}
	// An email session passes there.
	if r := postExchange(h, stack.jwtFor(sub, email, true), testkit.OrgNorthSlug); r.Code != 200 {
		t.Fatalf("email session at an email only dealer = %d %s", r.Code, r.Body)
	}
	// An unknown code in the table refuses, never narrows.
	if _, err := graph.pool.Exec(ctx, `INSERT INTO org_signin_method (org_id, code) VALUES ($1, 'sms')`, orgID); err != nil {
		t.Fatal(err)
	}
	if r := getMethods(h, "?org="+testkit.OrgNorthSlug); r.Code != 503 {
		t.Fatalf("route with an unknown code = %d %s", r.Code, r.Body)
	}
	if r := postExchange(h, stack.jwtFor(sub, email, true), testkit.OrgNorthSlug); r.Code != 503 {
		t.Fatalf("exchange with an unknown code = %d %s", r.Code, r.Body)
	}
}

// The tenant leg over the real database: a contractor with a linked ACTIVE
// portal row exchanges for a tenant token whose tenant claim is the row's
// account, and the token works on a real tenant route behind the gate and
// the guards. A person with the tenant Team and no row, an inactive row,
// the staff Team alone and a session method outside the tenant table each
// refuse with the shared bodies and write no AUTH_EXCHANGE row.
func TestBootConsoleTenantExchangeEndToEnd(t *testing.T) {
	env := newCompEnv(t)
	stack := newConsoleStack(t, env)
	env.set("CORE_APPWRITE_ENDPOINT", stack.server.URL)
	rec, graph := registeredSurface(t, env)
	h := rec.inner
	count := func() int {
		var n int
		if err := graph.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action = 'AUTH_EXCHANGE'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	base := count()

	sub := testkit.SubPortalAdminAlpha
	jwt := stack.jwtFor(sub, "fxportal.admin.alpha@fixture.test", true)
	stack.setProvider(sub, "magic-url")
	rsp := postTenantExchange(h, jwt, testkit.OrgNorthSlug)
	if rsp.Code != http.StatusOK {
		t.Fatalf("tenant exchange = %d %s", rsp.Code, rsp.Body)
	}
	var out struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rsp.Body.Bytes(), &out)
	if out.Token == "" || count() != base+1 {
		t.Fatalf("token %q, audit rows %d -> %d", out.Token, base, count())
	}
	var actor string
	if err := graph.pool.QueryRow(context.Background(),
		`SELECT actor_id::text FROM audit_log WHERE action = 'AUTH_EXCHANGE' ORDER BY created_at DESC LIMIT 1`).Scan(&actor); err != nil || actor != testkit.PortalAdminAlphaID {
		t.Fatalf("audit actor = %q (%v), want the portal row", actor, err)
	}

	// The minted token works on a real tenant route, no X-Org, and reads
	// the row of the exchange's own tenant.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tenant/me", nil)
	req.Header.Set("Authorization", "Bearer "+out.Token)
	got := httptest.NewRecorder()
	h.ServeHTTP(got, req)
	if got.Code != http.StatusOK {
		t.Fatalf("tenant route with the minted token = %d %s", got.Code, got.Body)
	}
	// The same token is no staff bearer.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/needs", nil)
	req.Header.Set("Authorization", "Bearer "+out.Token)
	req.Header.Set("X-Org", testkit.OrgNorthSlug)
	got = httptest.NewRecorder()
	h.ServeHTTP(got, req)
	if got.Code != http.StatusForbidden {
		t.Fatalf("tenant token on a staff route = %d", got.Code)
	}

	refused := func(name string, rsp *httptest.ResponseRecorder, status int) {
		t.Helper()
		if rsp.Code != status || strings.Contains(rsp.Body.String(), "token") {
			t.Fatalf("%s = %d %s, want %d and no token", name, rsp.Code, rsp.Body, status)
		}
	}
	before := count()
	mk := func(sub, email, provider string) string {
		j := stack.jwtFor(sub, email, true)
		stack.setProvider(sub, provider)
		return j
	}
	refused("replay", postTenantExchange(h, jwt, testkit.OrgNorthSlug), 401)
	refused("team but no portal row", postTenantExchange(h, mk(xrNoPortalRowSub, "norow@fixture.test", "magic-url"), testkit.OrgNorthSlug), 403)
	refused("inactive row", postTenantExchange(h, mk(testkit.SubPortalInactiveAlpha, "fxportal.inactive.alpha@fixture.test", "magic-url"), testkit.OrgNorthSlug), 403)
	refused("account email is not the row's", postTenantExchange(h, mk(testkit.SubPortalCrewAlpha, "someone.else@fixture.test", "magic-url"), testkit.OrgNorthSlug), 403)
	refused("password session", postTenantExchange(h, mk(testkit.SubPortalCrewAlpha, "fxportal.crew.alpha@fixture.test", "email"), testkit.OrgNorthSlug), 403)
	refused("the staff Team does not open the tenant leg", postTenantExchange(h, mk(testkit.SubSalesManager, "fxstaff.sales.manager@fixture.test", "magic-url"), testkit.OrgNorthSlug), 403)
	refused("another org", postTenantExchange(h, mk(testkit.SubPortalCrewAlpha, "fxportal.crew.alpha@fixture.test", "magic-url"), testkit.OrgSouthSlug), 403)
	if count() != before {
		t.Fatalf("a refusal wrote an AUTH_EXCHANGE row (%d -> %d)", before, count())
	}
}
