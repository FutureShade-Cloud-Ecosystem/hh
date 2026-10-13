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

// gapRouteFixture is the registered sync surface over the DB-free
// guard harness, the yard and contractor route fixtures' shape, with
// the three gap scopes wired under the fake store so the tenancy legs
// run as the served surface runs them.
type gapRouteFixture struct {
	mux    *http.ServeMux
	staff  string
	tenant string
}

func newGapRouteFixture(t *testing.T, scopes ...string) *gapRouteFixture {
	t.Helper()
	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(identity.StaffAuthority{RoleID: "inside_sales", Tier: identity.TierFrontline, Scope: identity.ScopeBranch})
	sg.SetPortalRole(identity.PortalRoleAccountAdmin)
	sg.Harness.Membership.SetTenantOrgs(tenantSub, testkit.OrgNorthSlug)
	engine := NewEngine()
	fake := &fakeStore{}
	for _, scope := range scopes {
		if err := engine.RegisterScope(scope, fake); err != nil {
			t.Fatalf("register %s: %v", scope, err)
		}
	}
	staffDouble, portalStub := allWriters()
	h, err := NewHandler(engine, staffDouble, portalStub, nil)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)
	return &gapRouteFixture{
		mux:    mux,
		staff:  sg.Bearer(t),
		tenant: sg.Harness.Minter.TenantToken(t, tenantSub, tenantAlpha),
	}
}

// do runs one request against the fixture's mux.
func (f *gapRouteFixture) do(method, target, bearer string, body []byte, header map[string]string) *httptest.ResponseRecorder {
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
	f.mux.ServeHTTP(rec, req)
	return rec
}

// TestGapScopesRouteTenancy: the three scopes' tenancy classes run in
// the middleware-shaped path (the handler's scopeFilter behind
// RequireSync, before any store call). approvals is an org scope: a
// tenant bearer answers the RBAC-403 family on both directions,
// byte-identical to every other org scope's refusal. payments is a
// tenant scope: the staff bearer answers the same family. invoices
// admits BOTH classes: the staff bearer reaches the store through
// X-Org and the tenant bearer through the token alone, and the G4
// rule holds on the shared route (a tenant bearer offering X-Org is
// refused the 400 the middleware owns, never read as an org hint).
func TestGapScopesRouteTenancy(t *testing.T) {
	f := newGapRouteFixture(t, "approvals", "invoices", "payments")
	org := map[string]string{authz.HeaderXOrg: testkit.OrgNorthSlug}

	// approvals: tenant callers refused both directions, staff with
	// X-Org reaches the store.
	if rec := f.do(http.MethodGet, "/api/v1/sync/approvals", f.tenant, nil, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("tenant pull on approvals: %d %s, want 403", rec.Code, rec.Body.String())
	}
	if rec := f.do(http.MethodPost, "/api/v1/sync/approvals", f.tenant, []byte(`{"ops":[]}`), nil); rec.Code != http.StatusForbidden {
		t.Fatalf("tenant push on approvals: %d %s, want 403", rec.Code, rec.Body.String())
	}
	if rec := f.do(http.MethodGet, "/api/v1/sync/approvals?since=0", f.staff, nil, org); rec.Code != http.StatusOK {
		t.Fatalf("staff pull on approvals: %d %s, want 200", rec.Code, rec.Body.String())
	}

	// payments: the staff bearer refused both directions, the tenant
	// bearer reaches the store on the token alone.
	if rec := f.do(http.MethodGet, "/api/v1/sync/payments", f.staff, nil, org); rec.Code != http.StatusForbidden {
		t.Fatalf("staff pull on payments: %d %s, want 403", rec.Code, rec.Body.String())
	}
	if rec := f.do(http.MethodPost, "/api/v1/sync/payments", f.staff, []byte(`{"ops":[]}`), org); rec.Code != http.StatusForbidden {
		t.Fatalf("staff push on payments: %d %s, want 403", rec.Code, rec.Body.String())
	}
	if rec := f.do(http.MethodGet, "/api/v1/sync/payments?since=0", f.tenant, nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("tenant pull on payments: %d %s, want 200", rec.Code, rec.Body.String())
	}

	// invoices: both caller classes reach the store, each through its
	// own G4 leg.
	if rec := f.do(http.MethodGet, "/api/v1/sync/invoices?since=0", f.staff, nil, org); rec.Code != http.StatusOK {
		t.Fatalf("staff pull on invoices: %d %s, want 200", rec.Code, rec.Body.String())
	}
	if rec := f.do(http.MethodGet, "/api/v1/sync/invoices?since=0", f.tenant, nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("tenant pull on invoices: %d %s, want 200", rec.Code, rec.Body.String())
	}
	// The G4 tenant half: a tenant bearer that OFFERS X-Org is refused
	// before the handler by the middleware's own 400.
	if rec := f.do(http.MethodGet, "/api/v1/sync/invoices", f.tenant, nil, org); rec.Code != http.StatusBadRequest {
		t.Fatalf("tenant pull with X-Org: %d %s, want 400", rec.Code, rec.Body.String())
	}

	// The refusals are byte-identical across the tenancy-mismatched
	// scopes so the body leaks nothing about any scope's rows.
	one := f.do(http.MethodGet, "/api/v1/sync/approvals", f.tenant, nil, nil).Body.String()
	two := f.do(http.MethodGet, "/api/v1/sync/payments", f.staff, nil, org).Body.String()
	if one != two {
		t.Fatalf("the tenancy refusals differ across the gap scopes: %q vs %q", one, two)
	}
}
