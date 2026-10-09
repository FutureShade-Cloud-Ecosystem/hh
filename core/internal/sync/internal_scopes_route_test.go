package sync

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// TestInternalScopesRouteTenancy: the internal frontends' outbox scopes
// (HH-3.5 #168) are org scopes, and the tenancy refusal runs in the
// middleware-shaped path exactly as it runs for the pilot and the
// yard-pick scopes: a tenant bearer answers the RBAC-403 family on all
// seven, byte-identical across them so the refusal leaks nothing about
// any scope's rows, and a staff bearer with X-Org reaches each wired
// store. price_matrix is the one exception the catalog declares on both
// sides: a tenant bearer reaches it too (its org arriving from the
// token's team, never a header, per G4), so the dual-bearer half is
// proven here beside the org-scope refusals.
func TestInternalScopesRouteTenancy(t *testing.T) {
	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(identity.StaffAuthority{RoleID: "inside_sales", Tier: identity.TierFrontline, Scope: identity.ScopeBranch})
	sg.SetPortalRole(identity.PortalRoleAccountAdmin)
	sg.Harness.Membership.SetTenantOrgs(tenantSub, testkit.OrgNorthSlug)
	engine := NewEngine()
	fake := &fakeStore{}
	orgScopes := []string{"quotes", "loads", "delivery_stops", "will_call", "purchase_orders", "cycle_counts"}
	for _, scope := range append(orgScopes, "price_matrix") {
		if err := engine.RegisterScope(scope, fake); err != nil {
			t.Fatalf("register %s: %v", scope, err)
		}
	}
	h, err := NewHandler(engine, nil)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)
	staff := sg.Bearer(t)
	tenant := sg.Harness.Minter.TenantToken(t, tenantSub, tenantAlpha)

	do := func(method, target, bearer string, body []byte, header map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		var reader *bytes.Reader
		if body == nil {
			reader = bytes.NewReader(nil)
		} else {
			reader = bytes.NewReader(body)
		}
		req := httptest.NewRequest(method, target, reader)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		for k, v := range header {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	tenantRefusals := map[string]string{}
	for _, scope := range orgScopes {
		if rec := do(http.MethodGet, "/api/v1/sync/"+scope, tenant, nil, nil); rec.Code != http.StatusForbidden {
			t.Fatalf("tenant pull on %s: %d %s, want 403", scope, rec.Code, rec.Body.String())
		}
		if rec := do(http.MethodPost, "/api/v1/sync/"+scope, tenant, []byte(`{"ops":[]}`), nil); rec.Code != http.StatusForbidden {
			t.Fatalf("tenant push on %s: %d %s, want 403", scope, rec.Code, rec.Body.String())
		}
		if rec := do(http.MethodGet, "/api/v1/sync/"+scope+"?since=0", staff, nil,
			map[string]string{authz.HeaderXOrg: testkit.OrgNorthSlug}); rec.Code != http.StatusOK {
			t.Fatalf("staff pull on %s: %d %s, want 200", scope, rec.Code, rec.Body.String())
		}
		tenantRefusals[scope] = do(http.MethodGet, "/api/v1/sync/"+scope, tenant, nil, nil).Body.String()
	}
	first := tenantRefusals[orgScopes[0]]
	for scope, body := range tenantRefusals {
		if body != first {
			t.Fatalf("the tenant refusal on %s differs from %s's, the refusals must be byte-identical", scope, orgScopes[0])
		}
	}

	// price_matrix serves both caller classes: the tenant bearer pulls
	// it (no X-Org, the org from the token's team), and the tenant
	// bearer with X-Org is refused per G4 exactly as every dual-bearer
	// route refuses: the header itself is rejected (the contract's own
	// parameter refusal, a 400 that names the header).
	if rec := do(http.MethodGet, "/api/v1/sync/price_matrix?since=0", tenant, nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("tenant pull on price_matrix: %d %s, want 200", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodGet, "/api/v1/sync/price_matrix?since=0", tenant, nil,
		map[string]string{authz.HeaderXOrg: testkit.OrgNorthSlug}); rec.Code != http.StatusBadRequest {
		t.Fatalf("tenant pull with X-Org on price_matrix: %d %s, want 400 (G4)", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodGet, "/api/v1/sync/price_matrix?since=0", staff, nil,
		map[string]string{authz.HeaderXOrg: testkit.OrgNorthSlug}); rec.Code != http.StatusOK {
		t.Fatalf("staff pull on price_matrix: %d %s, want 200", rec.Code, rec.Body.String())
	}

	// The messaging scopes are WIRED stores now (House Messages core, 0385
	// and internal/messaging/syncscope.go): the composition-level proof that
	// the real server serves them is cmd/core's messaging_sync_comp_test;
	// here the route half proves what the wiring closed, that a staff bearer
	// reaches a wired store on all four (channels is pull-only: its POST
	// meets the pull-only 400 before the store is consulted) and the tenant
	// bearer meets the shared RBAC-403 family byte-identically across them.
	// tracking_events stays the honest-unwired example: no module owns a
	// table for it, so its GET answers the 501 a fabricated feed would lie
	// about.
	for _, scope := range []string{"messages", "acks", "notify_prefs"} {
		if err := engine.RegisterScope(scope, fake); err != nil {
			t.Fatalf("register %s: %v", scope, err)
		}
	}
	if err := engine.RegisterScope("channels", fake); err != nil {
		t.Fatalf("register channels: %v", err)
	}
	for _, scope := range []string{"messages", "acks", "notify_prefs", "channels"} {
		if rec := do(http.MethodGet, "/api/v1/sync/"+scope+"?since=0", staff, nil,
			map[string]string{authz.HeaderXOrg: testkit.OrgNorthSlug}); rec.Code != http.StatusOK {
			t.Fatalf("%s pull: %d %s, want 200", scope, rec.Code, rec.Body.String())
		}
		if rec := do(http.MethodGet, "/api/v1/sync/"+scope, tenant, nil, nil); rec.Code != http.StatusForbidden {
			t.Fatalf("tenant pull on %s: %d %s, want 403", scope, rec.Code, rec.Body.String())
		}
	}
	if rec := do(http.MethodPost, "/api/v1/sync/channels", staff, []byte(`{"ops":[]}`),
		map[string]string{authz.HeaderXOrg: testkit.OrgNorthSlug}); rec.Code != http.StatusBadRequest {
		t.Fatalf("channels push: %d %s, want the pull-only 400", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodGet, "/api/v1/sync/tracking_events", tenant, nil, nil); rec.Code != http.StatusNotImplemented {
		t.Fatalf("tracking_events pull: %d %s, want the honest 501", rec.Code, rec.Body.String())
	}
}
