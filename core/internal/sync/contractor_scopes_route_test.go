package sync

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// contractorRouteFixture is the registered sync surface over the
// DB-free guard harness, exactly the pilot's routeFixture shape, with
// the #169 scopes wired under the fake store so the tenancy and refusal
// legs run as the served surface runs them.
type contractorRouteFixture struct {
	mux    *http.ServeMux
	staff  string
	tenant string
}

func newContractorRouteFixture(t *testing.T, scopes ...string) *contractorRouteFixture {
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
	return &contractorRouteFixture{
		mux:    mux,
		staff:  sg.Bearer(t),
		tenant: sg.Harness.Minter.TenantToken(t, tenantSub, tenantAlpha),
	}
}

// do runs one request against the fixture's mux.
func (f *contractorRouteFixture) do(method, target, bearer string, body []byte, header map[string]string) *httptest.ResponseRecorder {
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

// TestContractorPushScopesRouteTenancy: the six contractor push
// scopes are tenant scopes; a staff bearer answers the RBAC-403 family
// on both directions, byte-identical across them so the refusal leaks
// nothing about any scope's rows, and a tenant bearer (no X-Org, per
// G4) reaches the wired store on both directions.
func TestContractorPushScopesRouteTenancy(t *testing.T) {
	pushScopes := []string{"orders", "scope_items", "customer_quotes", "team", "delivery_instructions", "pickups"}
	f := newContractorRouteFixture(t, pushScopes...)
	staffRefusals := map[string]string{}
	org := map[string]string{authz.HeaderXOrg: testkit.OrgNorthSlug}
	for _, scope := range pushScopes {
		if rec := f.do(http.MethodGet, "/api/v1/sync/"+scope, f.staff, nil, org); rec.Code != http.StatusForbidden {
			t.Fatalf("staff pull on %s: %d %s, want 403", scope, rec.Code, rec.Body.String())
		}
		if rec := f.do(http.MethodPost, "/api/v1/sync/"+scope, f.staff, []byte(`{"ops":[]}`), org); rec.Code != http.StatusForbidden {
			t.Fatalf("staff push on %s: %d %s, want 403", scope, rec.Code, rec.Body.String())
		}
		if rec := f.do(http.MethodGet, "/api/v1/sync/"+scope+"?since=0", f.tenant, nil, nil); rec.Code != http.StatusOK {
			t.Fatalf("tenant pull on %s: %d %s, want 200", scope, rec.Code, rec.Body.String())
		}
		if rec := f.do(http.MethodPost, "/api/v1/sync/"+scope, f.tenant, []byte(`{"ops":[]}`), nil); rec.Code != http.StatusOK {
			t.Fatalf("tenant push on %s: %d %s, want 200", scope, rec.Code, rec.Body.String())
		}
		staffRefusals[scope] = f.do(http.MethodGet, "/api/v1/sync/"+scope, f.staff, nil, org).Body.String()
	}
	for scope, body := range staffRefusals {
		if body != staffRefusals["orders"] {
			t.Fatalf("the staff refusal on %s differs from orders': %q vs %q", scope, body, staffRefusals["orders"])
		}
	}
	// The G4 tenant half: a tenant bearer that OFFERS X-Org is refused
	// before the handler by the middleware's own 400, the same answer
	// every tenant route gives the header (ADR-0020).
	if rec := f.do(http.MethodGet, "/api/v1/sync/orders", f.tenant, nil,
		map[string]string{authz.HeaderXOrg: testkit.OrgNorthSlug}); rec.Code != http.StatusBadRequest {
		t.Fatalf("tenant pull with X-Org: %d %s, want 400", rec.Code, rec.Body.String())
	}
}

// TestPullOnlyScopesPostRefused: every pull-only scope's POST answers
// the contract's own refusal (the 400 Error envelope carrying the
// pull-only sentence) while its GET reaches the wired store and answers
// the pull shape; the refusal bodies follow one shared shape across
// the fourteen so no scope's rows leak through the wording.
func TestPullOnlyScopesPostRefused(t *testing.T) {
	// The pull-only set, split by the tenancy the registry declares:
	// the org class answers a staff bearer, the tenant class a tenant
	// bearer, and the pull-only refusal is the answer either class
	// meets on the POST of its own scopes.
	pullOnlyOrg := []string{
		"products", "customers", "inventory_lookup", "trucks", "drivers",
		"vendors", "parked_tickets", "warranty_claims",
		"widget_measures", "warranty_board",
	}
	pullOnlyTenant := []string{"projects", "catalog", "payment_methods", "aging"}
	all := append(append([]string{}, pullOnlyOrg...), pullOnlyTenant...)
	f := newContractorRouteFixture(t, all...)
	var refusal string
	for _, scope := range pullOnlyOrg {
		rec := f.do(http.MethodPost, "/api/v1/sync/"+scope, f.staff, []byte(`{"ops":[]}`),
			map[string]string{authz.HeaderXOrg: testkit.OrgNorthSlug})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("pull-only push on %s: %d %s, want 400", scope, rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("pull-only push on %s: body is not the Error envelope: %v", scope, err)
		}
		// The envelope strips the module prefix at the door every
		// envelope leaves through, so the sentence reads "scope X is
		// pull-only" without the "sync: " prefix.
		if msg, _ := body["error"].(string); msg != "scope "+scope+" is pull-only" {
			t.Fatalf("pull-only push on %s answered %q", scope, msg)
		}
		// The envelope's shape is one key, the sentence naming the scope
		// the URL already names; no row of any scope leaks through it.
		var shape map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &shape); err != nil || len(shape) != 1 {
			t.Fatalf("pull-only refusal on %s is not the one-key Error envelope: %s", scope, rec.Body.String())
		}
		refusal = rec.Body.String()
		if rec := f.do(http.MethodGet, "/api/v1/sync/"+scope+"?since=0", f.staff, nil,
			map[string]string{authz.HeaderXOrg: testkit.OrgNorthSlug}); rec.Code != http.StatusOK {
			t.Fatalf("staff pull on %s: %d %s, want 200", scope, rec.Code, rec.Body.String())
		}
	}
	for _, scope := range pullOnlyTenant {
		rec := f.do(http.MethodPost, "/api/v1/sync/"+scope, f.tenant, []byte(`{"ops":[]}`), nil)
		// Money never queues: payment_methods and aging are money scopes,
		// so their POST is the shared RBAC-403 for every role, ahead of
		// the pull-only shape.
		if info, _ := Lookup(scope); info.Money {
			if rec.Code != http.StatusForbidden || rec.Body.String() != string(authz.BodyForbidden) {
				t.Fatalf("tenant push on money scope %s: %d %s, want the shared 403", scope, rec.Code, rec.Body.String())
			}
			if rec := f.do(http.MethodGet, "/api/v1/sync/"+scope+"?since=0", f.tenant, nil, nil); rec.Code != http.StatusOK {
				t.Fatalf("tenant pull on %s: %d %s, want 200", scope, rec.Code, rec.Body.String())
			}
			continue
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("tenant pull-only push on %s: %d %s, want 400", scope, rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("tenant pull-only push on %s: body is not the Error envelope: %v", scope, err)
		}
		if msg, _ := body["error"].(string); msg != "scope "+scope+" is pull-only" {
			t.Fatalf("tenant pull-only push on %s answered %q", scope, msg)
		}
		if rec := f.do(http.MethodGet, "/api/v1/sync/"+scope+"?since=0", f.tenant, nil, nil); rec.Code != http.StatusOK {
			t.Fatalf("tenant pull on %s: %d %s, want 200", scope, rec.Code, rec.Body.String())
		}
	}
	// The org-class pull-only scopes answer the shared 403 family to a
	// tenant bearer, byte-identical across them.
	tenantRefusal := ""
	for _, scope := range []string{"products", "customers", "inventory_lookup", "trucks", "drivers", "vendors", "parked_tickets", "warranty_claims", "widget_measures", "warranty_board"} {
		rec := f.do(http.MethodGet, "/api/v1/sync/"+scope, f.tenant, nil, nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("tenant pull on org scope %s: %d %s, want 403", scope, rec.Code, rec.Body.String())
		}
		if tenantRefusal != "" && rec.Body.String() != tenantRefusal {
			t.Fatalf("the tenant refusal differs across org pull-only scopes")
		}
		tenantRefusal = rec.Body.String()
	}
	_ = refusal
	_ = tenantRefusal
}

// TestUnwiredScopesAnswerHonest501: tracking_events and channels are
// the lane's reported D26 schema-shaped stops; unwired, both routes
// answer the honest 501 rather than a fabricated empty feed.
func TestUnwiredScopesAnswerHonest501(t *testing.T) {
	f := newContractorRouteFixture(t)
	// tracking_events is a tenant-class scope: the tenant caller is the
	// one its honest 501 answers, and a staff caller meets the tenancy
	// refusal first, exactly as on any tenant scope.
	if rec := f.do(http.MethodPost, "/api/v1/sync/tracking_events", f.tenant, []byte(`{"ops":[]}`), nil); rec.Code != http.StatusNotImplemented {
		t.Fatalf("unwired push on tracking_events: %d %s, want 501", rec.Code, rec.Body.String())
	}
	if rec := f.do(http.MethodGet, "/api/v1/sync/tracking_events?since=0", f.tenant, nil, nil); rec.Code != http.StatusNotImplemented {
		t.Fatalf("unwired pull on tracking_events: %d %s, want 501", rec.Code, rec.Body.String())
	}
	if rec := f.do(http.MethodGet, "/api/v1/sync/tracking_events?since=0", f.staff, nil,
		map[string]string{authz.HeaderXOrg: testkit.OrgNorthSlug}); rec.Code != http.StatusForbidden {
		t.Fatalf("staff pull on tracking_events: %d %s, want the tenancy 403", rec.Code, rec.Body.String())
	}
	// channels is an org-class PULL-ONLY scope: its GET is the honest
	// 501 (the wiring fact), while its POST is the pull-only 400 (the
	// policy fact), the refusal the route answers for every pull-only
	// scope whether wired or not.
	if rec := f.do(http.MethodGet, "/api/v1/sync/channels?since=0", f.staff, nil,
		map[string]string{authz.HeaderXOrg: testkit.OrgNorthSlug}); rec.Code != http.StatusNotImplemented {
		t.Fatalf("unwired pull on channels: %d %s, want 501", rec.Code, rec.Body.String())
	}
	if rec := f.do(http.MethodPost, "/api/v1/sync/channels", f.staff, []byte(`{"ops":[]}`),
		map[string]string{authz.HeaderXOrg: testkit.OrgNorthSlug}); rec.Code != http.StatusBadRequest {
		t.Fatalf("pull-only push on channels: %d %s, want 400", rec.Code, rec.Body.String())
	}
}
