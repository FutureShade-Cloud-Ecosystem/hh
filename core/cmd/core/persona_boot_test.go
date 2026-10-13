//go:build demo

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/flows"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/persona"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dispatch"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

const (
	pbPartner    = "a1b2c3d4e5f6a7b8c9d0"
	pbPartnerRow = "33333333-3333-4333-8333-333333333333"
	pbOther      = "a1b2c3d4e5f6a7b8c9d1"
)

// pbPlantPersonas plants, in miniature, what the populate writes for a few
// personas: the users row linked to the subject, its staff grants (a branch
// role at the demo branch, a global one with none) and its branch grant; and
// the pinned partner's linked ACTIVE row.
func pbPlantPersonas(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	var orgID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM org WHERE slug = $1`, flows.OrgSlug).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"sales", "yard-manager", "driver", "general-manager", "financial-controller", "hr", "dealer-admin"} {
		p, _ := flows.PersonaByID(id)
		if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, display_name, status, platform_user_id, org_id)
			VALUES ($1::uuid, $2, $3, 'ACTIVE', $4, $5::uuid)`, p.UserID, p.Email, p.DisplayName(), p.Subject(), orgID); err != nil {
			t.Fatalf("plant %s: %v", id, err)
		}
		for _, role := range p.StaffRoles() {
			var branch any
			if role != "general_manager" && role != "financial_controller" && role != "hr" && role != "dealer_admin" {
				branch = rgDemoBranchID
			}
			if _, err := pool.Exec(ctx, `INSERT INTO staff_role_grant (platform_user_id, role_id, org_id, branch_id) VALUES ($1, $2, $3::uuid, $4::uuid)`,
				p.Subject(), role, orgID, branch); err != nil {
				t.Fatalf("plant %s grant %s: %v", id, role, err)
			}
		}
		if _, err := pool.Exec(ctx, `INSERT INTO user_branch_grants (user_id, branch_id, org_id) VALUES ($1::uuid, $2::uuid, $3::uuid)`, p.UserID, rgDemoBranchID, orgID); err != nil {
			t.Fatalf("plant %s branch: %v", id, err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, display_name, status, platform_user_id, org_id)
		VALUES ($1::uuid, 'partner.one@hh-demo.invalid', 'Partner one (demo)', 'ACTIVE', $2, $3::uuid)`, pbPartnerRow, pbPartner, orgID); err != nil {
		t.Fatal(err)
	}
}

// pbCase boots the REAL demo graph in console mode over the hand built demo
// world plus the planted personas.
func pbCase(t *testing.T, extra map[string]string) (*recordingRegistrar, *appGraph) {
	t.Helper()
	appwrite := newAppwriteFake(t)
	keyFile, _ := writeSigninKey(t, t.TempDir(), "current.pem")
	values := map[string]string{
		"CORE_SIGNIN_MODE":              "console",
		"CORE_TOKEN_ISSUER":             "hh-demo-testers",
		"CORE_APPWRITE_ENDPOINT":        appwrite.server.URL,
		"CORE_APPWRITE_PROJECT":         "fx-appwrite-project",
		"CORE_APPWRITE_API_KEY":         "fx-appwrite-key-placeholder",
		"CORE_SESSION_SIGNING_KEY_FILE": keyFile,
		"CORE_PERSONA_CALLER":           "partner",
		"CORE_PERSONA_PARTNERS":         pbPartner,
	}
	for k, v := range extra {
		values[k] = v
	}
	rec, graph := rgDemoGateCaseWith(t, values)
	pbPlantPersonas(t, graph.pool)
	return rec, graph
}

func pbPost(h http.Handler, bearer, body, remote string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/persona", strings.NewReader(body))
	req.RemoteAddr = remote
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rsp := httptest.NewRecorder()
	h.ServeHTTP(rsp, req)
	return rsp
}

func pbGet(h http.Handler, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Org", flows.OrgSlug)
	rsp := httptest.NewRecorder()
	h.ServeHTTP(rsp, req)
	return rsp
}

type pbAnswer struct {
	Token       string `json:"token"`
	ExpiresAtMs int64  `json:"expiresAtMs"`
	User        struct {
		UserID   string `json:"user_id"`
		RoleCode string `json:"role_code"`
	} `json:"user"`
}

func pbEnter(t *testing.T, h http.Handler, bearer, id, remote string) pbAnswer {
	t.Helper()
	rsp := pbPost(h, bearer, `{"persona":"`+id+`"}`, remote)
	if rsp.Code != http.StatusOK {
		t.Fatalf("persona %s = %d %s", id, rsp.Code, rsp.Body)
	}
	var a pbAnswer
	if err := json.Unmarshal(rsp.Body.Bytes(), &a); err != nil || a.Token == "" {
		t.Fatalf("answer %s: %v", rsp.Body, err)
	}
	return a
}

// The testers' copy over the real database: a partner's real core bearer
// enters as a persona, and the persona token passes the real verifier,
// authz.Gate with X-Org hh-demo, the role gate and the guards for its role,
// and is refused where the role is not allowed. The audit row names the
// partner and the persona and is written once per entry.
func TestBootPersonaTestersCopyEndToEnd(t *testing.T) {
	rec, graph := pbCase(t, nil)
	h := rec.inner
	partner, err := graph.session.minter.Mint(pbPartner, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	count := func() int {
		var n int
		if err := graph.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action = 'AUTH_PERSONA_ENTRY'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	sales := pbEnter(t, h, partner, "sales", "198.51.100.20:1")
	gm := pbEnter(t, h, partner, "general-manager", "198.51.100.20:2")
	if count() != 2 {
		t.Fatalf("audit rows %d, want 2", count())
	}
	var actor, entity, subjectRef, actorSubject string
	if err := graph.pool.QueryRow(context.Background(), `
		SELECT actor_id::text, entity_id::text, changes->>'subject_ref', changes->>'actor_subject'
		  FROM audit_log WHERE action = 'AUTH_PERSONA_ENTRY' ORDER BY created_at, id LIMIT 1`).Scan(&actor, &entity, &subjectRef, &actorSubject); err != nil {
		t.Fatal(err)
	}
	p, _ := flows.PersonaByID("sales")
	if actor != pbPartnerRow || entity != p.UserID || subjectRef != "persona:sales" || actorSubject != pbPartner {
		t.Fatalf("audit row: actor %s entity %s ref %s partner %s", actor, entity, subjectRef, actorSubject)
	}

	// The real verifier classifies the token as a staff bearer of the persona.
	got, err := graph.verify.Verify(context.Background(), sales.Token)
	if err != nil || got.Class != identity.ClassStaff || got.Subject != "persona:sales" {
		t.Fatalf("verify: %+v %v", got, err)
	}

	// Allowed where the role is allowed, refused where it is not.
	if c := pbGet(h, "/api/v1/quotes", sales.Token).Code; c != http.StatusOK {
		t.Fatalf("sales on the quotes list = %d", c)
	}
	if c := pbGet(h, "/api/v1/dashboard/needs", gm.Token).Code; c != http.StatusOK {
		t.Fatalf("general manager on the dashboard needs = %d", c)
	}
	if rsp := pbGet(h, "/api/v1/dispatch/board", sales.Token); rsp.Code != http.StatusForbidden || rsp.Body.String() != string(authz.BodyForbidden) {
		t.Fatalf("sales on the dispatch board = %d %s, want the role gate's 403", rsp.Code, rsp.Body)
	}
	if rsp := pbGet(h, "/api/v1/dispatch/board", gm.Token); rsp.Code == http.StatusForbidden && rsp.Body.String() == string(authz.BodyForbidden) || rsp.Code == http.StatusUnauthorized {
		t.Fatalf("general manager on the dispatch board = %d %s, want the handler's own answer", rsp.Code, rsp.Body)
	}
	// Without X-Org the gate refuses: the org is not in the token. Another org is no member.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/quotes", nil)
	req.Header.Set("Authorization", "Bearer "+sales.Token)
	noOrg := httptest.NewRecorder()
	h.ServeHTTP(noOrg, req)
	if noOrg.Code != http.StatusBadRequest {
		t.Fatalf("no X-Org = %d", noOrg.Code)
	}
	req.Header.Set("X-Org", "another-dealer")
	other := httptest.NewRecorder()
	h.ServeHTTP(other, req)
	if other.Code == http.StatusOK {
		t.Fatal("a persona token reached another org")
	}

	// A persona token cannot chain; an unlisted Console id and a missing bearer are refused.
	if rsp := pbPost(h, sales.Token, `{"persona":"hr"}`, "198.51.100.21:1"); rsp.Code != http.StatusForbidden {
		t.Fatalf("chain = %d", rsp.Code)
	}
	stranger, _ := graph.session.minter.Mint(pbOther, time.Hour)
	if rsp := pbPost(h, stranger, `{"persona":"sales"}`, "198.51.100.22:1"); rsp.Code != http.StatusForbidden {
		t.Fatalf("unlisted = %d", rsp.Code)
	}
	if rsp := pbPost(h, "", `{"persona":"sales"}`, "198.51.100.23:1"); rsp.Code != http.StatusUnauthorized {
		t.Fatalf("no bearer = %d", rsp.Code)
	}
	// Unknown and contractor personas are the shared 403.
	for _, id := range []string{"nobody", "contractor-owner"} {
		if rsp := pbPost(h, partner, `{"persona":"`+id+`"}`, "198.51.100.24:1"); rsp.Code != http.StatusForbidden || rsp.Body.String() != string(authz.BodyForbidden) {
			t.Fatalf("%s = %d %s", id, rsp.Code, rsp.Body)
		}
	}
	if count() != 2 {
		t.Fatalf("a refusal wrote an audit row: %d", count())
	}
}

// The driver persona's token window is the dealer's driver knob, judged at
// mint, and the guards accept it; the yard manager (also a driver) is judged
// by its winning role, the staff window.
func TestBootPersonaDriverCeiling(t *testing.T) {
	rec, graph := pbCase(t, map[string]string{"CORE_PERSONA_CALLER": "open", "CORE_PERSONA_PARTNERS": ""})
	h := rec.inner
	hours, err := dispatchDriverSessionKnob{repo: dispatch.NewRepository(graph.pool)}.DriverSessionHours(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	driver := pbEnter(t, h, "", "driver", "198.51.100.30:1")
	window := func(token string) time.Duration {
		parts := strings.Split(token, ".")
		raw, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			t.Fatal(err)
		}
		var c struct{ Iat, Exp int64 }
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatal(err)
		}
		return time.Duration(c.Exp-c.Iat) * time.Second
	}
	if got := window(driver.Token); got != time.Duration(hours)*time.Hour {
		t.Fatalf("driver window %s, want the knob's %d hours", got, hours)
	}
	if rsp := pbGet(h, "/api/v1/products", driver.Token); rsp.Code != http.StatusOK {
		t.Fatalf("the guards refused the driver's own window: %d %s", rsp.Code, rsp.Body)
	}
	// The driver role is not the sales role: the role gate holds it off quotes.
	if rsp := pbGet(h, "/api/v1/quotes", driver.Token); rsp.Code != http.StatusForbidden {
		t.Fatalf("driver on the quotes list = %d", rsp.Code)
	}
	yard := pbEnter(t, h, "", "yard-manager", "198.51.100.31:1")
	if got := window(yard.Token); got != identity.StaffSessionTTL {
		t.Fatalf("yard manager window %s", got)
	}
}

// The public copy: no bearer, the per address and persona limit holds, the
// audit row carries a bucket and never the address.
func TestBootPersonaPublicCopy(t *testing.T) {
	rec, graph := pbCase(t, map[string]string{"CORE_PERSONA_CALLER": "open", "CORE_PERSONA_PARTNERS": ""})
	h := rec.inner
	for i := 0; i < persona.AddressPersonaAllowance; i++ {
		pbEnter(t, h, "", "sales", "198.51.100.40:1")
	}
	if rsp := pbPost(h, "", `{"persona":"sales"}`, "198.51.100.40:1"); rsp.Code != http.StatusTooManyRequests {
		t.Fatalf("over the persona allowance = %d", rsp.Code)
	}
	var bucket, actor string
	if err := graph.pool.QueryRow(context.Background(), `
		SELECT changes->>'masked_ref', coalesce(changes->>'actor_subject','') FROM audit_log WHERE action = 'AUTH_PERSONA_ENTRY' LIMIT 1`).Scan(&bucket, &actor); err != nil {
		t.Fatal(err)
	}
	if bucket == "" || strings.Contains(bucket, "198") || actor != "" {
		t.Fatalf("bucket %q actor_subject %q", bucket, actor)
	}
	var leaks int
	if err := graph.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE changes::text LIKE '%198.51.100%'`).Scan(&leaks); err != nil || leaks != 0 {
		t.Fatalf("the raw address is in %d audit rows (%v)", leaks, err)
	}
}

// Each cell of the composition: the route is registered in the demo build's
// console mode only, the old mint routes stay unregistered there, and the
// demo mode has the old routes and no persona route.
func TestBootPersonaRouteCells(t *testing.T) {
	rec, _ := pbCase(t, nil)
	if c := serveOnce(rec.inner, http.MethodPost, "/api/v1/auth/persona").Code; c != http.StatusUnauthorized {
		t.Fatalf("console mode persona route answered %d, want the partner rule's 401", c)
	}
	for _, c := range []struct{ m, p string }{{http.MethodGet, "/api/v1/demo/flows"}, {http.MethodPost, "/api/v1/demo/session"}} {
		if code := serveOnce(rec.inner, c.m, c.p).Code; code != http.StatusNotFound {
			t.Errorf("console mode %s %s answered %d, want 404", c.m, c.p, code)
		}
	}
}

// The demo mode serves the old mint routes and no persona route.
func TestBootPersonaRouteAbsentInDemoMode(t *testing.T) {
	demoRec, _ := rgDemoGateCaseWith(t, nil)
	if c := serveOnce(demoRec.inner, http.MethodPost, "/api/v1/auth/persona").Code; c != http.StatusNotFound {
		t.Fatalf("demo mode serves the persona route: %d", c)
	}
	if c := serveOnce(demoRec.inner, http.MethodGet, "/api/v1/demo/flows").Code; c != http.StatusOK {
		t.Fatalf("demo mode lost the old routes: %d", c)
	}
}

// A console mode boot without the caller rule is refused, with every bad
// value of it.
func TestBootConsolePersonaConfigurationRefusals(t *testing.T) {
	appwrite := newAppwriteFake(t)
	keyFile, _ := writeSigninKey(t, t.TempDir(), "current.pem")
	base := map[string]string{
		"CORE_SIGNIN_MODE":              "console",
		"CORE_TOKEN_ISSUER":             "hh-demo-testers",
		"CORE_APPWRITE_ENDPOINT":        appwrite.server.URL,
		"CORE_APPWRITE_PROJECT":         "fx-appwrite-project",
		"CORE_APPWRITE_API_KEY":         "fx-appwrite-key-placeholder",
		"CORE_SESSION_SIGNING_KEY_FILE": keyFile,
	}
	for name, extra := range map[string]map[string]string{
		"no caller rule":     {},
		"unknown rule":       {"CORE_PERSONA_CALLER": "both"},
		"open with partners": {"CORE_PERSONA_CALLER": "open", "CORE_PERSONA_PARTNERS": pbPartner},
		"colon partner":      {"CORE_PERSONA_CALLER": "partner", "CORE_PERSONA_PARTNERS": "persona:sales"},
	} {
		values := map[string]string{}
		for k, v := range base {
			values[k] = v
		}
		for k, v := range extra {
			values[k] = v
		}
		if _, err := rgDemoGateCaseErr(t, values); err == nil {
			t.Errorf("%s: booted", name)
		}
	}
}

// The lead's rule, pending the principal's A6 ruling on admin cards on the
// public copy: the open copy refuses every admin persona with the shared 403
// and writes no row, and still mints a non admin one.
func TestBootPersonaPublicCopyRefusesAdminCards(t *testing.T) {
	rec, graph := pbCase(t, map[string]string{"CORE_PERSONA_CALLER": "open", "CORE_PERSONA_PARTNERS": ""})
	for i, id := range []string{"general-manager", "financial-controller", "hr", "dealer-admin"} {
		rsp := pbPost(rec.inner, "", `{"persona":"`+id+`"}`, "198.51.100.6"+string(rune('0'+i))+":1")
		if rsp.Code != http.StatusForbidden || rsp.Body.String() != string(authz.BodyForbidden) {
			t.Fatalf("%s = %d %s", id, rsp.Code, rsp.Body)
		}
	}
	var n int
	if err := graph.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action = 'AUTH_PERSONA_ENTRY'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("audit rows %d (%v)", n, err)
	}
	pbEnter(t, rec.inner, "", "sales", "198.51.100.70:1")
}
