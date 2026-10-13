package sync

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// routeFixture is the registered sync surface over the DB-free guard
// harness: the real Handler behind the real RequireSync, a fake store
// wired under the pilot scope and under one tenant-class scope, so the
// route legs (the G4 halves, the tenancy classes, the refusal shapes)
// run exactly as the served surface runs them.
type routeFixture struct {
	mux    *http.ServeMux
	staff  string
	tenant string
	fake   *fakeStore
}

func newRouteFixture(t *testing.T) *routeFixture {
	t.Helper()
	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(identity.StaffAuthority{RoleID: "inside_sales", Tier: identity.TierFrontline, Scope: identity.ScopeBranch})
	sg.SetPortalRole(identity.PortalRoleAccountAdmin)
	// The tenant principal's team: one tenant team in the north org.
	sg.Harness.Membership.SetTenantOrgs(tenantSub, testkit.OrgNorthSlug)
	engine := NewEngine()
	fake := &fakeStore{}
	if err := engine.RegisterScope("pick_tasks", fake); err != nil {
		t.Fatalf("register pick_tasks: %v", err)
	}
	if err := engine.RegisterScope("orders", fake); err != nil {
		t.Fatalf("register orders: %v", err)
	}
	staffDouble, portalStub := allWriters()
	h, err := NewHandler(engine, staffDouble, portalStub, nil)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)
	return &routeFixture{
		mux:    mux,
		staff:  sg.Bearer(t),
		tenant: sg.Harness.Minter.TenantToken(t, tenantSub, tenantAlpha),
		fake:   fake,
	}
}

// tenantSub and tenantAlpha are the fixture tenant principal: invented,
// patterned values in the kit's discipline.
const (
	tenantSub   = "fxportal-sync-alpha"
	tenantAlpha = "3ef10000-0000-4000-8000-000000000021"
)

// do runs one request against the registered surface.
func (f *routeFixture) do(t *testing.T, method, target, bearer string, body []byte, header map[string]string) *httptest.ResponseRecorder {
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
	f.mux.ServeHTTP(rec, req)
	return rec
}

// TestG4RouteHalfStaffRequiresXOrg: a JWKS-valid staff bearer without the
// X-Org header never reaches the sync handler (the 400 shape family the
// middleware writes), and the same bearer with the header does reach it
// (the pull answers its own shape, not a gate refusal).
func TestG4RouteHalfStaffRequiresXOrg(t *testing.T) {
	f := newRouteFixture(t)
	rsp := f.do(t, http.MethodGet, "/api/v1/sync/pick_tasks", f.staff, nil, nil)
	if rsp.Code != http.StatusBadRequest {
		t.Fatalf("staff pull without X-Org: %d %s, want 400", rsp.Code, rsp.Body.String())
	}
	if !strings.Contains(rsp.Body.String(), "X-Org") {
		t.Fatalf("the 400 did not name the missing header: %s", rsp.Body.String())
	}
	rsp = f.do(t, http.MethodPost, "/api/v1/sync/pick_tasks", f.staff, []byte(`{"ops":[]}`), nil)
	if rsp.Code != http.StatusBadRequest {
		t.Fatalf("staff push without X-Org: %d, want 400", rsp.Code)
	}
	// The admitted direction of the conditional: the same bearer with
	// the header reaches the handler.
	rsp = f.do(t, http.MethodGet, "/api/v1/sync/pick_tasks?since=0", f.staff, nil, map[string]string{authz.HeaderXOrg: testkit.OrgNorthSlug})
	if rsp.Code != http.StatusOK {
		t.Fatalf("staff pull with X-Org: %d %s, want 200", rsp.Code, rsp.Body.String())
	}
}

// TestG4RouteHalfTenantRejectsXOrg: a JWKS-valid tenant bearer carrying
// the X-Org header never reaches the sync handler, whichever org the
// header names; the same bearer without it does (the tenant leg of the
// dual-bearer surface).
func TestG4RouteHalfTenantRejectsXOrg(t *testing.T) {
	f := newRouteFixture(t)
	for _, slug := range []string{testkit.OrgNorthSlug, testkit.OrgSouthSlug, ""} {
		rsp := f.do(t, http.MethodGet, "/api/v1/sync/orders", f.tenant, nil, map[string]string{authz.HeaderXOrg: slug})
		if rsp.Code != http.StatusBadRequest {
			t.Fatalf("tenant pull carrying X-Org %q: %d, want 400", slug, rsp.Code)
		}
	}
	rsp := f.do(t, http.MethodGet, "/api/v1/sync/orders?since=0", f.tenant, nil, nil)
	if rsp.Code != http.StatusOK {
		t.Fatalf("tenant pull without X-Org: %d %s, want 200", rsp.Code, rsp.Body.String())
	}
}

// TestPullShapeOverRoutes: the route's own legs, the scope echo, the
// empty feed, the high-water mark, and the parameter shapes (strict
// since, fail-quiet limit).
func TestPullShapeOverRoutes(t *testing.T) {
	f := newRouteFixture(t)
	rsp := f.do(t, http.MethodGet, "/api/v1/sync/pick_tasks", f.staff, nil, map[string]string{authz.HeaderXOrg: testkit.OrgNorthSlug})
	if rsp.Code != http.StatusOK {
		t.Fatalf("pull: %d %s", rsp.Code, rsp.Body.String())
	}
	var pull apigen.SyncPullResponse
	if err := json.Unmarshal(rsp.Body.Bytes(), &pull); err != nil {
		t.Fatalf("pull body: %v", err)
	}
	if string(pull.Scope) != "pick_tasks" || pull.Items == nil || pull.Tombstones == nil || pull.NextCursor != nil {
		t.Fatalf("empty pull shape: %+v", pull)
	}
	// since strictness.
	rsp = f.do(t, http.MethodGet, "/api/v1/sync/pick_tasks?since=-1", f.staff, nil, map[string]string{authz.HeaderXOrg: testkit.OrgNorthSlug})
	if rsp.Code != http.StatusBadRequest {
		t.Fatalf("negative since: %d, want 400", rsp.Code)
	}
	rsp = f.do(t, http.MethodGet, "/api/v1/sync/pick_tasks?since=abc", f.staff, nil, map[string]string{authz.HeaderXOrg: testkit.OrgNorthSlug})
	if rsp.Code != http.StatusBadRequest {
		t.Fatalf("malformed since: %d, want 400", rsp.Code)
	}
	// limit fail-quiet (the recorded divergence): garbage reads as the
	// default, an over-maximum clamps.
	rsp = f.do(t, http.MethodGet, "/api/v1/sync/pick_tasks?limit=garbage&limit=999999", f.staff, nil, map[string]string{authz.HeaderXOrg: testkit.OrgNorthSlug})
	if rsp.Code != http.StatusOK {
		t.Fatalf("fail-quiet limit: %d %s", rsp.Code, rsp.Body.String())
	}
}

// The refusal shapes: an unknown scope answers 400, a pull-only scope's
// POST answers 400 in the contract's refusal envelope, and an unwired
// scope answers the honest 501.
func TestRouteRefusalShapes(t *testing.T) {
	f := newRouteFixture(t)
	xorg := map[string]string{authz.HeaderXOrg: testkit.OrgNorthSlug}
	rsp := f.do(t, http.MethodGet, "/api/v1/sync/not_a_scope", f.staff, nil, xorg)
	if rsp.Code != http.StatusBadRequest || !strings.Contains(rsp.Body.String(), "unknown sync scope") {
		t.Fatalf("unknown scope pull: %d %s", rsp.Code, rsp.Body.String())
	}
	rsp = f.do(t, http.MethodPost, "/api/v1/sync/products", f.staff, []byte(`{"ops":[]}`), xorg)
	if rsp.Code != http.StatusBadRequest || !strings.Contains(rsp.Body.String(), "pull-only") {
		t.Fatalf("pull-only push: %d %s", rsp.Code, rsp.Body.String())
	}
	rsp = f.do(t, http.MethodGet, "/api/v1/sync/weights", f.staff, nil, xorg)
	if rsp.Code != http.StatusNotImplemented {
		t.Fatalf("unwired scope pull: %d %s, want 501", rsp.Code, rsp.Body.String())
	}
	// A pull-only scope's GET is the implemented half.
	rsp = f.do(t, http.MethodGet, "/api/v1/sync/products", f.staff, nil, xorg)
	if rsp.Code != http.StatusNotImplemented {
		t.Fatalf("pull-only unwired pull: %d, want the honest 501", rsp.Code)
	}
}

// Tenancy over the routes: a tenant bearer on an org scope never reads
// the org's rows (the RBAC-403, byte-identical family body), and a
// tenant-class scope serves the tenant while a second tenant's rows
// never leak: the middleware-stamped tenant is the only predicate.
func TestRouteTenancyIsolation(t *testing.T) {
	f := newRouteFixture(t)
	// Seed one org row (the north org's staff surface) and two tenant
	// rows in different tenants.
	staffFilter := ScopeFilter{OrgID: testkit.OrgNorthID, Actor: "fxstaff-any"}
	_, err := f.fake.Create(nil, staffFilter, key(1), map[string]any{"id": rowID(1), "v": "org"})
	if err != nil {
		t.Fatalf("seed org row: %v", err)
	}
	tenantA := ScopeFilter{OrgID: testkit.OrgNorthID, TenantID: tenantAlpha, Tenant: true, Actor: tenantSub}
	tenantB := ScopeFilter{OrgID: testkit.OrgNorthID, TenantID: "3ef10000-0000-4000-8000-000000000022", Tenant: true, Actor: "fxportal-sync-beta"}
	if _, err := f.fake.Create(nil, tenantA, key(2), map[string]any{"id": rowID(2), "v": "alpha"}); err != nil {
		t.Fatalf("seed alpha row: %v", err)
	}
	if _, err := f.fake.Create(nil, tenantB, key(3), map[string]any{"id": rowID(3), "v": "beta"}); err != nil {
		t.Fatalf("seed beta row: %v", err)
	}

	// A tenant bearer on the org-class pilot scope: the family 403, and
	// the body is the byte-identical family refusal (no scope facts
	// leak).
	rsp := f.do(t, http.MethodGet, "/api/v1/sync/pick_tasks", f.tenant, nil, nil)
	if rsp.Code != http.StatusForbidden || rsp.Body.String() != string(authz.BodyForbidden) {
		t.Fatalf("tenant on org scope: %d %q, want the family 403", rsp.Code, rsp.Body.String())
	}
	// The mirror: a staff bearer on a tenant-class scope.
	rsp = f.do(t, http.MethodGet, "/api/v1/sync/orders", f.staff, nil, map[string]string{authz.HeaderXOrg: testkit.OrgNorthSlug})
	if rsp.Code != http.StatusForbidden || rsp.Body.String() != string(authz.BodyForbidden) {
		t.Fatalf("staff on tenant scope: %d %q, want the family 403", rsp.Code, rsp.Body.String())
	}
	// The tenant's own pull on the tenant-class scope: only its rows.
	rsp = f.do(t, http.MethodGet, "/api/v1/sync/orders", f.tenant, nil, nil)
	if rsp.Code != http.StatusOK {
		t.Fatalf("tenant pull: %d %s", rsp.Code, rsp.Body.String())
	}
	var pull apigen.SyncPullResponse
	if err := json.Unmarshal(rsp.Body.Bytes(), &pull); err != nil {
		t.Fatalf("tenant pull body: %v", err)
	}
	if len(pull.Items) != 1 || pull.Items[0].Id != rowID(2) {
		t.Fatalf("tenant pull saw %d items, want exactly its own row: %+v", len(pull.Items), pull.Items)
	}
	// And a staff pull on the org scope never sees tenant rows.
	rsp = f.do(t, http.MethodGet, "/api/v1/sync/pick_tasks", f.staff, nil, map[string]string{authz.HeaderXOrg: testkit.OrgNorthSlug})
	if rsp.Code != http.StatusOK {
		t.Fatalf("staff pull: %d", rsp.Code)
	}
	pull = apigen.SyncPullResponse{}
	if err := json.Unmarshal(rsp.Body.Bytes(), &pull); err != nil {
		t.Fatalf("staff pull body: %v", err)
	}
	if len(pull.Items) != 1 || pull.Items[0].Id != rowID(1) {
		t.Fatalf("staff pull saw tenant rows: %+v", pull.Items)
	}
}

// The push route's body legs: one result per op, invalid JSON the 400.
func TestPushRouteBodyLegs(t *testing.T) {
	f := newRouteFixture(t)
	xorg := map[string]string{authz.HeaderXOrg: testkit.OrgNorthSlug}
	rsp := f.do(t, http.MethodPost, "/api/v1/sync/pick_tasks", f.staff, []byte(`not json`), xorg)
	if rsp.Code != http.StatusBadRequest {
		t.Fatalf("invalid json: %d", rsp.Code)
	}
	body, _ := json.Marshal(apigen.SyncPushRequest{Ops: []apigen.SyncPushOperation{{
		ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1), "v": "one"},
	}}})
	rsp = f.do(t, http.MethodPost, "/api/v1/sync/pick_tasks", f.staff, body, xorg)
	if rsp.Code != http.StatusOK {
		t.Fatalf("push: %d %s", rsp.Code, rsp.Body.String())
	}
	var pushed apigen.SyncPushResponse
	if err := json.Unmarshal(rsp.Body.Bytes(), &pushed); err != nil {
		t.Fatalf("push body: %v", err)
	}
	if len(pushed.Results) != 1 || pushed.Results[0].Status != "applied" {
		t.Fatalf("push results: %+v", pushed.Results)
	}
}

// The handler construction guards and the registrar form: the two
// patterns the parity suite expects, method-prefixed, behind RequireSync.
func TestHandlerRegistrationShape(t *testing.T) {
	sg := testkit.NewStaffGuards(t)
	engine := NewEngine()
	staffDouble, portalStub := allWriters()
	h, err := NewHandler(engine, staffDouble, portalStub, nil)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	if _, err := NewHandler(nil, staffDouble, portalStub, nil); err == nil {
		t.Fatalf("a nil engine constructed a handler")
	}
	if _, err := NewHandler(engine, nil, portalStub, nil); err == nil {
		t.Fatalf("a nil staff roles seam constructed a handler")
	}
	if _, err := NewHandler(engine, staffDouble, nil, nil); err == nil {
		t.Fatalf("a nil portal roles seam constructed a handler")
	}
	rec := &patternRecorder{}
	h.RegisterRoutes(rec, sg.Guards)
	want := []string{
		"GET /api/v1/sync/{scope}",
		"POST /api/v1/sync/{scope}",
	}
	if len(rec.patterns) != len(want) {
		t.Fatalf("registered %v, want %v", rec.patterns, want)
	}
	for i, p := range want {
		if rec.patterns[i] != p {
			t.Fatalf("pattern %d = %q, want %q", i, rec.patterns[i], p)
		}
	}
}

// patternRecorder satisfies httpx.Registrar for the registration test.
type patternRecorder struct {
	patterns []string
	httpx.Registrar
}

func (p *patternRecorder) Handle(pattern string, h http.Handler) {
	p.patterns = append(p.patterns, pattern)
}
