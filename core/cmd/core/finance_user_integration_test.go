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

	"github.com/jackc/pgx/v5/pgxpool"

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
	sessions map[string]string    // platform id -> the session id its JWTs name
	sessSeq  int
}

func newFinConsole(t *testing.T, people ...finPerson) *finConsole {
	t.Helper()
	fc := &finConsole{accounts: map[string]finPerson{}, people: map[string]finPerson{}, sessions: map[string]string{}}
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
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/users/"), "/sessions")
		fc.mu.Lock()
		sessID := fc.sessions[id]
		fc.mu.Unlock()
		created := time.Now().Add(-10 * time.Minute).UTC().Format("2006-01-02T15:04:05.000+00:00")
		fmt.Fprintf(w, `{"total":1,"sessions":[{"$id":%q,"$createdAt":%q,"provider":"email"}]}`,
			sessID, created)
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
	fc.sessions[p.platformID] = fmt.Sprintf("fin-sess-%d", seq)
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

// finEnvFor is the finance box's environment: the database, the box kind,
// the sign in mode, the allow list and the org slug, with the caller's
// overrides on top. An override with an empty value removes the name.
func finEnvFor(dsn string, addresses []string, over map[string]string) func(string) string {
	base := map[string]string{
		"DATABASE_URL":                dsn,
		"CORE_DATABASE_URL":           dsn,
		"HH_BOX_KIND":                 "finance",
		"APP_ENV":                     "staging",
		"CORE_SIGNIN_MODE":            "console",
		"CORE_SIGNIN_EMAIL_ADDRESSES": strings.Join(addresses, ","),
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
		return finEnvFor(dsn, []string{personA.email, personB.email}, over)
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
	offRole := append([]string{}, provisionA...)
	offRole[len(offRole)-1] = "driver"
	refuse("a role off the list", offRole, nil, "role")
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

// finLiveGrants counts the live grants one platform user holds in one org.
func finLiveGrants(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID, platformUserID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM staff_role_grant
		WHERE org_id = $1::uuid AND platform_user_id = $2 AND deleted_at IS NULL`,
		orgID, platformUserID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// finLiveGrantRoles reads the roles of the live grants one platform user
// holds in one org.
func finLiveGrantRoles(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID, platformUserID string) []string {
	t.Helper()
	rows, err := pool.Query(ctx, `
		SELECT role_id FROM staff_role_grant
		WHERE org_id = $1::uuid AND platform_user_id = $2 AND deleted_at IS NULL
		ORDER BY role_id`,
		orgID, platformUserID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			t.Fatal(err)
		}
		out = append(out, role)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestIntegration_FinanceUserFixes is the fix round's proof (W2-2): the
// allowed role list, the revoke that names its subject, the revoke that
// takes every live grant, the revoke that still works after the email left
// the allow list, and the provision that keeps the org anchor's name. One
// platform subject per leg, so a leg's failure names its own rule and no
// leg leans on another.
func TestIntegration_FinanceUserFixes(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "finuserfix")
	dsn := swapDatabase(os.Getenv("DATABASE_URL"), "demoseed_it_finuserfix")

	// The seeded dealer world, through the seed role itself.
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
	// Mark the box, or every leg refuses at the guard before it acts.
	if err := settings.WriteBoxKindTx(ctx, pool, "finance"); err != nil {
		t.Fatalf("marking the box: %v", err)
	}

	victim := finPerson{platformID: "fin-fix-victim", email: "fin-fix-victim@example.invalid", name: "Fix Victim", member: true}
	other := finPerson{platformID: "fin-fix-other", email: "fin-fix-other@example.invalid", name: "Fix Other", member: true}
	multi := finPerson{platformID: "fin-fix-multi", email: "fin-fix-multi@example.invalid", name: "Fix Multi", member: true}
	gone := finPerson{platformID: "fin-fix-gone", email: "fin-fix-gone@example.invalid", name: "Fix Gone", member: true}
	anchor := finPerson{platformID: "fin-fix-anchor", email: "fin-fix-anchor@example.invalid", name: "Fix Anchor", member: true}
	general := finPerson{platformID: "fin-fix-general", email: "fin-fix-general@example.invalid", name: "Fix General", member: true}
	nameless := finPerson{platformID: "fin-fix-nameless", email: "fin-fix-nameless@example.invalid", name: "Fix Nameless", member: true}
	demotee := finPerson{platformID: "fin-fix-demotee", email: "fin-fix-demotee@example.invalid", name: "Fix Demotee", member: true}
	addresses := []string{victim.email, other.email, multi.email, gone.email, anchor.email, general.email, nameless.email, demotee.email}
	console := newFinConsole(t, victim, other, demotee)
	env := func(over map[string]string) func(string) string { return finEnvFor(dsn, addresses, over) }

	// The production exchange wiring, FB Console faked over HTTP only: the
	// legs that prove a person still signs in after a refused revoke need
	// the real exchange.
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
		getenv: env(nil), log: log, pool: pool,
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

	// The org anchor of the box, as the seed would name it, and the id the
	// grant counts read.
	if _, _, err := identity.ProvisionOrgAnchor(ctx, pool, finOrgSlug, "Finance Box"); err != nil {
		t.Fatalf("the org anchor: %v", err)
	}
	var orgID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM org WHERE slug = $1`, finOrgSlug).Scan(&orgID); err != nil {
		t.Fatal(err)
	}

	provisionArgs := func(p finPerson, role string) []string {
		return []string{"--email", p.email, "--platform-user-id", p.platformID, "--display-name", p.name, "--role", role}
	}

	t.Run("the general manager role is refused", func(t *testing.T) {
		out := &bytes.Buffer{}
		err := financeUserRole(provisionArgs(general, "general_manager"), out, out, env(nil))
		if err == nil {
			t.Fatalf("the general manager role must refuse, got nil (out %q)", out.String())
		}
		if !strings.Contains(err.Error(), "role") {
			t.Fatalf("the refusal must name the role, got %v", err)
		}
	})

	t.Run("a revoke naming another email refuses", func(t *testing.T) {
		out := &bytes.Buffer{}
		if err := financeUserRole(provisionArgs(victim, "financial_controller"), out, out, env(nil)); err != nil {
			t.Fatalf("provisioning the victim: %v", err)
		}
		// The revoke names the other person's email and the victim's
		// platform id: the subject and the affected person may not differ.
		out.Reset()
		err := financeUserRole([]string{"--revoke", "--email", other.email, "--platform-user-id", victim.platformID, "--display-name", victim.name}, out, out, env(nil))
		if err == nil {
			t.Fatalf("a revoke naming another person's email must refuse, got nil (out %q)", out.String())
		}
		if code, _ := signIn(victim); code != http.StatusOK {
			t.Fatalf("the victim after the refused revoke: status %d, want 200 (log %s)", code, logBuf.String())
		}
		if n := finLiveGrants(t, ctx, pool, orgID, victim.platformID); n != 1 {
			t.Fatalf("the victim holds %d live grants after the refused revoke, want 1", n)
		}
		var revokedRows int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'STAFF_REVOKE'`).Scan(&revokedRows); err != nil {
			t.Fatal(err)
		}
		if revokedRows != 0 {
			t.Fatalf("the refused revoke wrote %d audit rows, want 0", revokedRows)
		}
		// The same revoke with the email in another case is the same person.
		out.Reset()
		if err := financeUserRole([]string{"--revoke", "--email", strings.ToUpper(victim.email), "--platform-user-id", victim.platformID, "--display-name", victim.name}, out, out, env(nil)); err != nil {
			t.Fatalf("a revoke with the email in another case must work: %v (out %q)", err, out.String())
		}
		if n := finLiveGrants(t, ctx, pool, orgID, victim.platformID); n != 0 {
			t.Fatalf("the victim holds %d live grants after the revoke, want 0", n)
		}
	})

	t.Run("a revoke does not need the display name", func(t *testing.T) {
		out := &bytes.Buffer{}
		if err := financeUserRole(provisionArgs(nameless, "financial_controller"), out, out, env(nil)); err != nil {
			t.Fatalf("provisioning: %v", err)
		}
		// The revoke names the person by email and platform id; the
		// display name belongs to the provision path only.
		out.Reset()
		err := financeUserRole([]string{"--revoke", "--email", nameless.email, "--platform-user-id", nameless.platformID}, out, out, env(nil))
		if err != nil {
			t.Fatalf("a revoke without the display name must work: %v (out %q)", err, out.String())
		}
		if n := finLiveGrants(t, ctx, pool, orgID, nameless.platformID); n != 0 {
			t.Fatalf("the person holds %d live grants after the revoke, want 0", n)
		}
	})

	t.Run("a revoke takes every live grant", func(t *testing.T) {
		out := &bytes.Buffer{}
		if err := financeUserRole(provisionArgs(multi, "financial_controller"), out, out, env(nil)); err != nil {
			t.Fatalf("provisioning: %v", err)
		}
		// A live grant from another hand (the carried catalog, say) is the
		// revoke's to take too: the provision would have demoted it, so
		// this leg writes it through identity's seam directly.
		if _, err := identity.EnsureStaffGrant(ctx, pool, orgID, multi.platformID, "payables_receivables", nil); err != nil {
			t.Fatalf("the second grant: %v", err)
		}
		if n := finLiveGrants(t, ctx, pool, orgID, multi.platformID); n != 2 {
			t.Fatalf("the person holds %d live grants, want 2", n)
		}
		out.Reset()
		err := financeUserRole([]string{"--revoke", "--email", multi.email, "--platform-user-id", multi.platformID, "--display-name", multi.name, "--role", "financial_controller"}, out, out, env(nil))
		if err != nil {
			t.Fatalf("the revoke: %v (out %q)", err, out.String())
		}
		if n := finLiveGrants(t, ctx, pool, orgID, multi.platformID); n != 0 {
			t.Fatalf("the person still holds %d live grants after the revoke, want 0", n)
		}
		if got := strings.Count(out.String(), "grant_id="); got != 2 {
			t.Fatalf("the revoke printed %d grant ids, want 2 (out %q)", got, out.String())
		}
		if strings.Contains(out.String(), "grant_id=\n") || strings.Contains(out.String(), "grant_id= ") {
			t.Fatalf("the revoke printed an empty grant id (out %q)", out.String())
		}
		// A later provision brings the person back with the role named then,
		// and with that role only.
		out.Reset()
		if err := financeUserRole(provisionArgs(multi, "financial_controller"), out, out, env(nil)); err != nil {
			t.Fatalf("the re-provision: %v", err)
		}
		roles := finLiveGrantRoles(t, ctx, pool, orgID, multi.platformID)
		if len(roles) != 1 || roles[0] != "financial_controller" {
			t.Fatalf("after the re-provision the person holds %v, want financial_controller only", roles)
		}
	})

	t.Run("a revoke works after the email left the allow list", func(t *testing.T) {
		out := &bytes.Buffer{}
		if err := financeUserRole(provisionArgs(gone, "financial_controller"), out, out, env(nil)); err != nil {
			t.Fatalf("provisioning: %v", err)
		}
		// The email is off the allow list now, which is the order an
		// operator cuts access in.
		out.Reset()
		err := financeUserRole([]string{"--revoke", "--email", gone.email, "--platform-user-id", gone.platformID, "--display-name", gone.name}, out, out,
			env(map[string]string{"CORE_SIGNIN_EMAIL_ADDRESSES": ""}))
		if err != nil {
			t.Fatalf("a revoke after the email left the allow list must work: %v (out %q)", err, out.String())
		}
		if n := finLiveGrants(t, ctx, pool, orgID, gone.platformID); n != 0 {
			t.Fatalf("the person holds %d live grants after the revoke, want 0", n)
		}
	})

	t.Run("the provision keeps the org anchor name", func(t *testing.T) {
		out := &bytes.Buffer{}
		slug := "finboxanchor"
		if _, _, err := identity.ProvisionOrgAnchor(ctx, pool, slug, "Anchor As Named"); err != nil {
			t.Fatalf("the anchor: %v", err)
		}
		if err := financeUserRole(provisionArgs(anchor, "financial_controller"), out, out,
			env(map[string]string{"HH_WEB_ORG": slug})); err != nil {
			t.Fatalf("the provision: %v (out %q)", err, out.String())
		}
		var name string
		if err := pool.QueryRow(ctx, `SELECT name FROM org WHERE slug = $1`, slug).Scan(&name); err != nil {
			t.Fatal(err)
		}
		if name != "Anchor As Named" {
			t.Fatalf("the anchor's name = %q, want the name it already had", name)
		}
	})

	t.Run("a provision with another role demotes the old grant", func(t *testing.T) {
		out := &bytes.Buffer{}
		if err := financeUserRole(provisionArgs(demotee, "financial_controller"), out, out, env(nil)); err != nil {
			t.Fatalf("provisioning the controller: %v (out %q)", err, out.String())
		}
		if n := finLiveGrants(t, ctx, pool, orgID, demotee.platformID); n != 1 {
			t.Fatalf("after the first provision the person holds %d live grants, want 1", n)
		}
		// The edited entry names payables_receivables: the controller grant
		// must go in the same transaction, or the person keeps the tier 1
		// role the entry no longer names and an edit can never demote.
		out.Reset()
		if err := financeUserRole(provisionArgs(demotee, "payables_receivables"), out, out, env(nil)); err != nil {
			t.Fatalf("provisioning the payables role: %v (out %q)", err, out.String())
		}
		if n := finLiveGrants(t, ctx, pool, orgID, demotee.platformID); n != 1 {
			t.Fatalf("after the edited provision the person holds %d live grants, want 1, the new one", n)
		}
		roles := finLiveGrantRoles(t, ctx, pool, orgID, demotee.platformID)
		if len(roles) != 1 || roles[0] != "payables_receivables" {
			t.Fatalf("after the edited provision the person holds %v, want payables_receivables only", roles)
		}
		// The exchange resolves the role the entry names now.
		code, body := signIn(demotee)
		if code != http.StatusOK {
			t.Fatalf("the demoted person's sign in: status %d, want 200 (log %s)", code, logBuf.String())
		}
		user, _ := body["user"].(map[string]any)
		if user["role_code"] != "payables_receivables" {
			t.Fatalf("the demoted person's role = %v, want payables_receivables", user["role_code"])
		}
	})
}
