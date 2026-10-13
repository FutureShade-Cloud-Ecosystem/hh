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
