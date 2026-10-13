//go:build demo

package main

// demoworld_tenant_test.go: proof of the demo contractor flow through the real demo graph.
//
// The world is built the way a demo box builds it (frozen migrations, demoseed, the populate, demoseed
// again) over a throwaway local Postgres gated on the till demo variable, then demoRegisterRoutes
// serves it: the flow listing carries the class, each of the five contractor flows mints a tenant
// token through the closed table, and the tenant surface answers that token as that contractor's role
// in Summit Ridge Builders and nothing else. The refusal legs: a staff token on the tenant surface, a
// tenant token on a staff route, a deactivated portal user, a changed portal role, and a populate
// re-run that writes nothing.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customerquote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/flows"
)

func TestDemoWorld_TenantFlow(t *testing.T) {
	dsn := os.Getenv(tillDemoDBEnvVar)
	if dsn == "" {
		t.Skip(tillDemoDBEnvVar + " is not set: the demo world proof needs its own throwaway local Postgres")
	}
	requireLocalDSN(t, dsn)
	ctx := context.Background()

	rawKey := make([]byte, 32)
	if _, err := rand.Read(rawKey); err != nil {
		t.Fatalf("generate vault key: %v", err)
	}
	pool, testDSN := qwFreshDB(t, ctx, dsn, "demoworld_tenant")
	t.Setenv("DATABASE_URL", testDSN)
	t.Setenv("CORE_DATABASE_URL", testDSN)
	t.Setenv("PAYMENT_VAULT_KEY", hex.EncodeToString(rawKey))
	t.Setenv("APP_ENV", "dev")
	qwApply(t)
	now := qwClock
	rep, err := demo.RunPopulate(ctx, demo.Options{Getenv: os.Getenv, Now: &now})
	if err != nil {
		t.Fatalf("demo populate: %v", err)
	}
	if rep.TenantAccountsCreated != 1 || rep.PortalUsersCreated != 5 || rep.TenantJobsCreated != 3 {
		t.Fatalf("the populate created %d accounts, %d portal users, %d jobs; want 1, 5, 3",
			rep.TenantAccountsCreated, rep.PortalUsersCreated, rep.TenantJobsCreated)
	}
	qwApply(t)

	// The world's rows, as the tenant resolver reads them.
	if n := qwCount(t, ctx, pool, `SELECT count(*) FROM portal_users WHERE tenant_id = $1::uuid AND customer_id = $1::uuid AND status = 'active' AND platform_user_id = id::text AND email LIKE '%@hh-demo.invalid'`, flows.TenantAccountID); n != 5 {
		t.Fatalf("%d linked contractor portal users, want 5", n)
	}
	if n := qwCount(t, ctx, pool, `SELECT count(DISTINCT role) FROM portal_users WHERE tenant_id = $1::uuid`, flows.TenantAccountID); n != 5 {
		t.Fatalf("%d distinct contractor roles, want 5", n)
	}
	if n := qwCount(t, ctx, pool, `SELECT count(*) FROM projects WHERE tenant_id = $1::uuid AND customer_id = $1::uuid`, flows.TenantAccountID); n != 3 {
		t.Fatalf("%d jobs, want 3", n)
	}
	if n := qwCount(t, ctx, pool, `SELECT count(*) FROM customers WHERE id = $1::uuid AND name = $2 AND org_id IS NOT NULL`, flows.TenantAccountID, flows.TenantAccountName); n != 1 {
		t.Fatalf("%d Summit Ridge Builders accounts stamped with the org, want 1", n)
	}

	// Idempotence: a second populate writes nothing of the contractor world.
	rep2, err := demo.RunPopulate(ctx, demo.Options{Getenv: os.Getenv, Now: &now})
	if err != nil {
		t.Fatalf("second populate: %v", err)
	}
	if rep2.TenantAccountsCreated != 0 || rep2.PortalUsersCreated != 0 || rep2.TenantJobsCreated != 0 {
		t.Fatalf("a second populate created %d, %d, %d contractor rows", rep2.TenantAccountsCreated, rep2.PortalUsersCreated, rep2.TenantJobsCreated)
	}

	// The served surface: the real demo graph.
	acceptKey := make([]byte, 32)
	if _, err := rand.Read(acceptKey); err != nil {
		t.Fatalf("generate acceptance key: %v", err)
	}
	values := map[string]string{
		customerquote.EnvKeyID: "fx-accept-sign-key", customerquote.EnvKey: hex.EncodeToString(acceptKey),
		"DEMO_MODE": "true", "APP_ENV": "demo",
		"CORE_DATABASE_URL": testDSN, "CORE_STAFF_AUDIENCE": "hh-staff", "CORE_TENANT_AUDIENCE": "hh-tenant",
		"PAYMENT_VAULT_KEY": hex.EncodeToString(rawKey),
	}
	getenv := func(k string) string { return values[k] }
	mux := http.NewServeMux()
	graph, err := demoRegisterRoutes(mux, getenv)
	if err != nil {
		t.Fatalf("demoRegisterRoutes: %v", err)
	}
	t.Cleanup(graph.pool.Close)

	calls := 0
	do := func(method, path, body, token string) (int, []byte) {
		t.Helper()
		var rdr *bytes.Reader
		if body != "" {
			rdr = bytes.NewReader([]byte(body))
		} else {
			rdr = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, path, rdr)
		calls++ // a fresh documentation address per call: the mint's per address limit is not under test
		req.RemoteAddr = fmt.Sprintf("192.0.2.%d:4000", calls)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rsp := httptest.NewRecorder()
		mux.ServeHTTP(rsp, req)
		return rsp.Code, rsp.Body.Bytes()
	}

	// The public tenant config names the demo's dealer, not the shared default's nothing and not
	// another dealer (row 1592).
	code, raw := do("GET", "/api/v1/tenant/config", "", "")
	if code != 200 || !strings.Contains(string(raw), `"dealer_name":"Gable Landscape Supply"`) || strings.Contains(strings.ToLower(string(raw)), "dibbits") {
		t.Fatalf("public tenant config = %d %s, want Gable Landscape Supply and no other dealer", code, raw)
	}

	// The listing carries the class of every flow.
	code, raw = do("GET", "/api/v1/demo/flows", "", "")
	if code != 200 {
		t.Fatalf("flows: %d %s", code, raw)
	}
	var list struct {
		Flows []struct{ Flow, Class string } `json:"flows"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	tenantFlows := 0
	for i, f := range list.Flows {
		if want := flows.All()[i].Class(); f.Class != want {
			t.Fatalf("flow %s class %q, want %q", f.Flow, f.Class, want)
		}
		if f.Class == "tenant" {
			tenantFlows++
		}
	}
	if tenantFlows != 5 {
		t.Fatalf("%d tenant flows listed, want 5", tenantFlows)
	}

	mint := func(flow string) (int, string) {
		code, raw := do("POST", "/api/v1/demo/session", `{"flow":"`+flow+`"}`, "")
		var out struct{ Token, Class string }
		_ = json.Unmarshal(raw, &out)
		return code, out.Token
	}

	// Each contractor flow mints a token the tenant surface answers as that role in that account.
	tokens := map[string]string{}
	for _, a := range flows.All() {
		if a.Class() != flows.ClassTenant {
			continue
		}
		code, tok := mint(a.Flow)
		if code != 200 || tok == "" {
			t.Fatalf("mint %s: %d", a.Flow, code)
		}
		tokens[a.Flow] = tok
		code, raw := do("GET", "/api/v1/tenant/me", "", tok)
		if code != 200 {
			t.Fatalf("tenant me as %s: %d %s", a.Flow, code, raw)
		}
		var me map[string]any
		if err := json.Unmarshal(raw, &me); err != nil {
			t.Fatal(err)
		}
		if me["role"] != a.Role {
			t.Fatalf("tenant me as %s names role %v, want %s (body %s)", a.Flow, me["role"], a.Role, raw)
		}
	}
	// The capabilities the roles carry differ the way the core says: the no pay buyer cannot pay.
	code, raw = do("GET", "/api/v1/tenant/me", "", tokens["contractor-buyer-no-pay"])
	if code != 200 || !strings.Contains(string(raw), `"pay_invoices":false`) {
		t.Fatalf("buyer_no_pay me = %d %s, want pay_invoices false", code, raw)
	}

	// A staff token never rides the tenant surface, and a tenant token never a staff route.
	scode, stok := mint("sales")
	if scode != 200 {
		t.Fatalf("mint sales: %d", scode)
	}
	if code, _ := do("GET", "/api/v1/tenant/me", "", stok); code == 200 {
		t.Fatal("a staff token read the tenant surface")
	}
	req := httptest.NewRequest("GET", "/api/v1/staff/approvals", nil)
	req.Header.Set("Authorization", "Bearer "+tokens["contractor-account-admin"])
	req.Header.Set("X-Org", flows.OrgSlug)
	rsp := httptest.NewRecorder()
	mux.ServeHTTP(rsp, req)
	if rsp.Code == 200 {
		t.Fatal("a tenant token read a staff route")
	}

	// The live grant decides: a changed role refuses the mint, a deactivated user refuses the mint and
	// the token already held (the portal role resolver reads the row per request).
	if _, err := pool.Exec(ctx, `UPDATE portal_users SET role = 'account_admin' WHERE email = 'field_crew@hh-demo.invalid'`); err != nil {
		t.Fatal(err)
	}
	if code, _ := mint("contractor-field-crew"); code != http.StatusForbidden {
		t.Fatalf("mint with a changed portal role: %d, want 403", code)
	}
	if _, err := pool.Exec(ctx, `UPDATE portal_users SET role = 'field_crew' WHERE email = 'field_crew@hh-demo.invalid'`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE portal_users SET status = 'inactive' WHERE email = 'buyer@hh-demo.invalid'`); err != nil {
		t.Fatal(err)
	}
	if code, _ := mint("contractor-buyer"); code != http.StatusForbidden {
		t.Fatalf("mint for an inactive portal user: %d, want 403", code)
	}
	if code, _ := do("GET", "/api/v1/tenant/me", "", tokens["contractor-buyer"]); code != http.StatusUnauthorized {
		t.Fatalf("a held token of a deactivated portal user answered %d, want 401", code)
	}
}
