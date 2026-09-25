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

// TestYardScopesRouteTenancy: the yard-pick scopes (HH-3.5 #166) are org
// scopes, and the tenancy refusal runs in the middleware-shaped path
// (the handler's scopeFilter behind RequireSync, before any store call)
// exactly as it runs for the pilot: a tenant bearer answers the RBAC-403
// family on all three, byte-identical across them so the refusal leaks
// nothing about any scope's rows, and a staff bearer with X-Org reaches
// each wired store.
func TestYardScopesRouteTenancy(t *testing.T) {
	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(identity.StaffAuthority{RoleID: "inside_sales", Tier: identity.TierFrontline, Scope: identity.ScopeBranch})
	sg.SetPortalRole(identity.PortalRoleAccountAdmin)
	// The tenant principal's team: one tenant team in the north org.
	sg.Harness.Membership.SetTenantOrgs(tenantSub, testkit.OrgNorthSlug)
	engine := NewEngine()
	fake := &fakeStore{}
	for _, scope := range []string{"shortages", "weights", "attachments"} {
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
	for _, scope := range []string{"shortages", "weights", "attachments"} {
		if rec := do(http.MethodGet, "/api/v1/sync/"+scope, tenant, nil, nil); rec.Code != http.StatusForbidden {
			t.Fatalf("tenant pull on %s: %d %s, want 403", scope, rec.Code, rec.Body.String())
		}
		if rec := do(http.MethodPost, "/api/v1/sync/"+scope, tenant, []byte(`{"ops":[]}`), nil); rec.Code != http.StatusForbidden {
			t.Fatalf("tenant push on %s: %d %s, want 403", scope, rec.Code, rec.Body.String())
		}
		// The admitted direction: a staff caller with X-Org reaches the
		// wired store and the pull answers its own shape, not a refusal.
		if rec := do(http.MethodGet, "/api/v1/sync/"+scope+"?since=0", staff, nil,
			map[string]string{authz.HeaderXOrg: testkit.OrgNorthSlug}); rec.Code != http.StatusOK {
			t.Fatalf("staff pull on %s: %d %s, want 200", scope, rec.Code, rec.Body.String())
		}
		tenantRefusals[scope] = do(http.MethodGet, "/api/v1/sync/"+scope, tenant, nil, nil).Body.String()
	}
	// The refusals are byte-identical across the three scopes.
	if tenantRefusals["shortages"] != tenantRefusals["weights"] || tenantRefusals["shortages"] != tenantRefusals["attachments"] {
		t.Fatalf("the tenant refusals differ across the yard scopes")
	}
}
