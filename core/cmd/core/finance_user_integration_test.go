//go:build integration

package main

// finance_user_integration_test.go, the named testers' command (W2-2) proved
// through the production wiring: the console exchange composition the serve
// role builds, with only FB Console's account, membership and session
// answers faked over HTTP, on a fresh migrated and seeded database. Person A
// is refused at the exchange's gate 6 before the command, signs in with a
// financial_controller token after it, person B with another finance role
// gets theirs, and A is refused again after --revoke while B still signs in.
// Every guard leg refuses with one line naming the check.

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dispatch"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/exchange"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

const (
	finOrgSlug       = "finbox"
	finIssuer        = "hh-fin-user-test"
	finStaffAudience = "hh-staff-test"
	finTenantAud     = "hh-tenant-test"
)

type finPerson struct {
	platformID string
	email      string
	name       string
	member     bool
}

// finConsole fakes FB Console's answers over HTTP: GET /account keyed on
// the presented JWT, the team membership listing, and the user's session
// listing. Everything else in the exchange is the production composition.
type finConsole struct {
	mu       sync.Mutex
	srv      *httptest.Server
	accounts map[string]finPerson // jwt -> the account it answers
	people   map[string]finPerson // platform id -> the person
	sessSeq  int
}

func newFinConsole(t *testing.T, people ...finPerson) *finConsole {
	t.Helper()
	fc := &finConsole{accounts: map[string]finPerson{}, people: map[string]finPerson{}}
	for _, p := range people {
		fc.people[p.platformID] = p
	}
	srv := httptest.NewServer(http.HandlerFunc(fc.handle))
	t.Cleanup(srv.Close)
	fc.srv = srv
	return fc
}

func (fc *finConsole) handle(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/account":
		fc.mu.Lock()
		p, ok := fc.accounts[r.Header.Get("X-Appwrite-JWT")]
		fc.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprintf(w, `{"$id":%q,"email":%q,"name":%q,"emailVerification":true,"status":true}`,
			p.platformID, p.email, p.name)
	case strings.HasPrefix(r.URL.Path, "/users/") && strings.HasSuffix(r.URL.Path, "/memberships"):
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/users/"), "/memberships")
		fc.mu.Lock()
		p, ok := fc.people[id]
		fc.mu.Unlock()
		if !ok || !p.member {
			fmt.Fprint(w, `{"total":0,"memberships":[]}`)
			return
		}
		fmt.Fprintf(w, `{"total":1,"memberships":[{"teamId":%q,"userId":%q}]}`, "org-"+finOrgSlug, id)
	case strings.HasPrefix(r.URL.Path, "/users/") && strings.HasSuffix(r.URL.Path, "/sessions"):
		fc.mu.Lock()
		fc.sessSeq++
		seq := fc.sessSeq
		fc.mu.Unlock()
		created := time.Now().Add(-10 * time.Minute).UTC().Format("2006-01-02T15:04:05.000+00:00")
		fmt.Fprintf(w, `{"total":1,"sessions":[{"$id":%q,"$createdAt":%q,"provider":"email"}]}`,
			fmt.Sprintf("fin-sess-%d", seq), created)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// jwt registers a fresh Console shaped JWT for the person and answers it.
func (fc *finConsole) jwt(p finPerson) string {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.sessSeq++
	seq := fc.sessSeq
	seg := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	payload := fmt.Sprintf(`{"exp":%d,"projectId":"p","sessionId":%q,"userId":%q}`,
		time.Now().Add(10*time.Minute).Unix(), fmt.Sprintf("fin-sess-%d", seq), p.platformID)
	sig := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("fin-signature-%d-%d", seq, time.Now().UnixNano())))
	tok := seg(`{"alg":"HS256","typ":"JWT"}`) + "." + seg(payload) + "." + sig
	fc.accounts[tok] = p
	return tok
}

var finPeerSeq int

// finSignIn runs one exchange request for the person and answers the recorder.
func finSignIn(h *exchange.Handler, p finPerson, tok string) *httptest.ResponseRecorder {
	finPeerSeq++
	rec := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]string{"jwt": tok, "org": finOrgSlug})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/exchange", strings.NewReader(string(body)))
	req.RemoteAddr = fmt.Sprintf("198.51.100.%d:5555", finPeerSeq%200+1)
	h.ServeHTTP(rec, req)
	return rec
}

func TestIntegration_FinanceUser(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "finuser")
	dsn := swapDatabase(os.Getenv("DATABASE_URL"), "demoseed_it_finuser")

	// The seeded Ontario dealer world, through the seed role itself.
	t.Setenv("APP_ENV", "dev")
	seedEnv := func(k string) string {
		switch k {
		case "DATABASE_URL", "CORE_DATABASE_URL":
			return dsn
		case "APP_ENV":
			return "dev"
		}
		return ""
	}
	if err := seedRole(seedEnv); err != nil {
		t.Fatalf("seed: %v", err)
	}

	personA := finPerson{platformID: "fin-console-user-a", email: "fin-controller-a@example.invalid", name: "Financial Controller", member: true}
	personB := finPerson{platformID: "fin-console-user-b", email: "fin-payables-b@example.invalid", name: "Payables and Receivables", member: true}
	console := newFinConsole(t, personA, personB)

	finEnv := func(over map[string]string) func(string) string {
		base := map[string]string{
			"DATABASE_URL":                dsn,
			"CORE_DATABASE_URL":           dsn,
			"HH_BOX_KIND":                 "finance",
			"APP_ENV":                     "staging",
			"CORE_SIGNIN_MODE":            "console",
			"CORE_SIGNIN_EMAIL_ADDRESSES": personA.email + "," + personB.email,
			"HH_WEB_ORG":                  finOrgSlug,
		}
		for k, v := range over {
			if v == "" {
				delete(base, k)
			} else {
				base[k] = v
			}
		}
		return func(k string) string { return base[k] }
	}
	provisionA := []string{"--email", personA.email, "--platform-user-id", personA.platformID, "--display-name", personA.name, "--role", "financial_controller"}
	provisionB := []string{"--email", personB.email, "--platform-user-id", personB.platformID, "--display-name", personB.name, "--role", "payables_receivables"}

	out := &bytes.Buffer{}
	refuse := func(name string, args []string, env map[string]string, want string) {
		t.Helper()
		out.Reset()
		err := financeUserRole(args, out, out, finEnv(env))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: refusal naming %q, got %v (out %q)", name, want, err, out.String())
		}
	}

	// The guard legs, each refused with one line naming the check.
	refuse("wrong APP_ENV", provisionA, map[string]string{"APP_ENV": "production"}, "APP_ENV")
	refuse("wrong sign in mode", provisionA, map[string]string{"CORE_SIGNIN_MODE": "demo"}, "CORE_SIGNIN_MODE")
	refuse("no box kind variable", provisionA, map[string]string{"HH_BOX_KIND": ""}, "HH_BOX_KIND")
	refuse("box kind unset in the database", provisionA, nil, "box_kind")

	// Mark the box, then the flag legs.
	if err := settings.WriteBoxKindTx(ctx, pool, "finance"); err != nil {
		t.Fatalf("marking the box: %v", err)
	}
	refuse("a role off the list", provisionA[:len(provisionA)-2], nil, "role")
	offList := append([]string{}, provisionA...)
	offList[1] = "someone-else@example.invalid"
	refuse("an email off the allow list", offList, nil, "allow list")

	// The production exchange wiring, FB Console faked over HTTP only.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	minter, err := identity.NewSessionMinter(key, finIssuer, finStaffAudience)
	if err != nil {
		t.Fatal(err)
	}
	if err := minter.WithTenantAudience(finTenantAud); err != nil {
		t.Fatal(err)
	}
	orgs, err := authz.NewPgxOrgResolver(pool)
	if err != nil {
		t.Fatal(err)
	}
	staff, err := identity.NewStaffAuthorityResolver(pool)
	if err != nil {
		t.Fatal(err)
	}
	logBuf := &bytes.Buffer{}
	log := slog.New(slog.NewTextHandler(logBuf, nil))
	exch, err := buildExchange(exchangeDeps{
		getenv: finEnv(nil), log: log, pool: pool,
		session:  &sessionComposition{minter: minter, exchangeTTL: time.Hour},
		appwrite: appwriteNames{endpoint: console.srv.URL, project: "fin-proj", apiKey: "fin-key"},
		orgs:     orgs, staff: staff,
		knob:  dispatchDriverSessionKnob{repo: dispatch.NewRepository(pool)},
		audit: audit.NewService(audit.NewRepository(pool), log),
	})
	if err != nil {
		t.Fatalf("buildExchange: %v", err)
	}

	signIn := func(p finPerson) (int, map[string]any) {
		t.Helper()
		rec := finSignIn(exch, p, console.jwt(p))
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body
	}

	// Person A, verified, active, a team member, on the allow list, is
	// refused at gate 6 before the command: no linked ACTIVE users row.
	code, _ := signIn(personA)
	if code != http.StatusForbidden {
		t.Fatalf("A before the command: status %d, want 403 (body in the log)", code)
	}

	// After finance-user, A's exchange answers a staff token whose role is
	// financial_controller.
	out.Reset()
	if err := financeUserRole(provisionA, out, out, finEnv(nil)); err != nil {
		t.Fatalf("finance-user for A: %v (out %q)", err, out.String())
	}
	if !strings.Contains(out.String(), "user_id=") || !strings.Contains(out.String(), "grant_id=") {
		t.Fatalf("the command prints the users row id and the grant id, got %q", out.String())
	}
	code, body := signIn(personA)
	if code != http.StatusOK {
		t.Fatalf("A after the command: status %d, want 200 (log %s)", code, logBuf.String())
	}
	user, _ := body["user"].(map[string]any)
	if user["role_code"] != "financial_controller" {
		t.Fatalf("A's role = %v, want financial_controller", user["role_code"])
	}

	// Person B with another allowed finance role gets theirs: two people
	// hold finance roles, so four eyes approvals have two pairs of eyes.
	out.Reset()
	if err := financeUserRole(provisionB, out, out, finEnv(nil)); err != nil {
		t.Fatalf("finance-user for B: %v (out %q)", err, out.String())
	}
	code, body = signIn(personB)
	if code != http.StatusOK {
		t.Fatalf("B after the command: status %d, want 200 (log %s)", code, logBuf.String())
	}
	user, _ = body["user"].(map[string]any)
	if user["role_code"] != "payables_receivables" {
		t.Fatalf("B's role = %v, want payables_receivables", user["role_code"])
	}

	// After --revoke, A is refused again and B still signs in.
	out.Reset()
	if err := financeUserRole(append([]string{"--revoke"}, provisionA...), out, out, finEnv(nil)); err != nil {
		t.Fatalf("finance-user --revoke for A: %v (out %q)", err, out.String())
	}
	code, _ = signIn(personA)
	if code != http.StatusForbidden {
		t.Fatalf("A after the revoke: status %d, want 403", code)
	}
	code, _ = signIn(personB)
	if code != http.StatusOK {
		t.Fatalf("B after A's revoke: status %d, want 200 (log %s)", code, logBuf.String())
	}

	// One audit row per run, through the audit module's own table.
	var provisioned, revoked int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = 'STAFF_PROVISION'`).Scan(&provisioned); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = 'STAFF_REVOKE'`).Scan(&revoked); err != nil {
		t.Fatal(err)
	}
	if provisioned != 2 || revoked != 1 {
		t.Fatalf("audit rows: %d provisioned, %d revoked, want 2 and 1", provisioned, revoked)
	}
}
