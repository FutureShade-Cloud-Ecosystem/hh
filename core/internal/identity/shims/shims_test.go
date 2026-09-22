package shims

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

const (
	fxIssuer   = "test-issuer"
	fxStaffAud = "hh-staff"
	fxTenaAud  = "hh-tenant"
	fxKeyID    = "b3-shim-rsa"
	fxSubject  = "b3-shim-user-1"
	fxTenant   = "77777777-7777-4777-8777-777777777777"
	fxOrgSlug  = "b3shimyard"
	fxOrgID    = "88888888-8888-4888-8888-888888888888"
)

// The doubles are intentionally minimal: membership always true for the
// anchored slug, the anchor fixed, the staff authority and portal role
// settable per leg.
type members struct{ slugs map[string]bool }

func (m *members) OrgMember(_ context.Context, _, slug string) (bool, error) {
	return m.slugs[slug], nil
}

func (m *members) TenantOrgs(context.Context, string) ([]string, error) {
	return []string{fxOrgSlug}, nil
}

type orgs struct{ anchors map[string]string }

func (o *orgs) ResolveSlug(_ context.Context, slug string) (string, bool, error) {
	id, ok := o.anchors[slug]
	return id, ok, nil
}

type staffSrc struct {
	auth identity.StaffAuthority
	ok   bool
}

func (s *staffSrc) ResolveStaff(context.Context, string, string) (identity.StaffAuthority, bool, error) {
	return s.auth, s.ok, nil
}

type portalSrc struct {
	role string
	ok   bool
}

func (p *portalSrc) PortalRole(context.Context, string, string) (string, bool, error) {
	return p.role, p.ok, nil
}

type shimFixture struct {
	keys   *rsa.PrivateKey
	g      *guards.Guards
	mux    *http.ServeMux
	rec    *Recorder
	staff  *staffSrc
	portal *portalSrc
}

func newShimFixture(t *testing.T, tier int, roleID, scope string) *shimFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	verifier, err := identity.NewVerifier(identity.Config{
		KeySource: identity.NewStaticKeySource(map[string]crypto.PublicKey{fxKeyID: &key.PublicKey}),
		Issuer:    fxIssuer, StaffAudience: fxStaffAud, TenantAudience: fxTenaAud,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	f := &shimFixture{keys: key}
	gate, err := authz.NewGate(verifier,
		&members{slugs: map[string]bool{fxOrgSlug: true}},
		&orgs{anchors: map[string]string{fxOrgSlug: fxOrgID}})
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	f.staff = &staffSrc{ok: true, auth: identity.StaffAuthority{RoleID: roleID, Tier: tier, Scope: scope}}
	f.portal = &portalSrc{ok: true, role: identity.PortalRoleAccountAdmin}
	g, err := guards.NewGuards(gate, f.staff, f.portal, nil)
	if err != nil {
		t.Fatalf("NewGuards: %v", err)
	}
	f.g = g
	f.mux = http.NewServeMux()
	f.rec = NewRecorder(64)
	RegisterRoutes(f.mux, g, f.rec)
	RegisterSupersededProofRoutes(f.mux, g, f.rec)
	return f
}

func (f *shimFixture) token(t *testing.T, audience string) string {
	t.Helper()
	now := time.Now()
	claims := map[string]any{
		"iss": fxIssuer, "sub": fxSubject, "aud": audience,
		"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(10 * time.Minute).Unix(),
	}
	if audience == fxTenaAud {
		claims["tenant"] = fxTenant
	}
	header := map[string]any{"alg": "RS256", "kid": fxKeyID, "typ": "JWT"}
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(claims)
	b64 := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
	signed := b64(hb) + "." + b64(cb)
	digest := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.keys, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signed + "." + b64(sig)
}

func (f *shimFixture) do(t *testing.T, method, path, bearer string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
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

// The registrars exercised end to end: the five order
// lifecycle routes behind their contract x-role classes (the superseded
// proof registrar, exactly as the acceptance harness registers it), the tenant set,
// the two sync operations under both bearers, the public operation with
// and without an org header (the G4 public non-rule), and the
// capability-qualified portal operation in both directions.
func TestRegisterRoutesExercised(t *testing.T) {
	f := newShimFixture(t, identity.TierFrontline, "inside_sales", identity.ScopeBranch)
	staff := f.token(t, fxStaffAud)
	tenant := f.token(t, fxTenaAud)
	xorg := map[string]string{authz.HeaderXOrg: fxOrgSlug}

	// The four requireStaff transitions and the read answer 200 and record
	// the stamped staff facts.
	for _, c := range []struct{ method, path, op string }{
		{http.MethodGet, "/api/v1/orders/b3-order-1", "orderRead"},
		{http.MethodPost, "/api/v1/orders/b3-order-1/confirm", "orderConfirm"},
		{http.MethodPost, "/api/v1/orders/b3-order-1/picking", "orderMarkPicking"},
		{http.MethodPost, "/api/v1/orders/b3-order-1/ready", "orderMarkReady"},
	} {
		if got := f.do(t, c.method, c.path, staff, xorg).Code; got != http.StatusOK {
			t.Fatalf("%s: status %d", c.op, got)
		}
		if last := f.rec.Entries()[len(f.rec.Entries())-1]; last.Operation != c.op || last.Staff == nil || last.Staff.RoleID != "inside_sales" || last.Staff.OrgSlug != fxOrgSlug || last.Staff.Tier != identity.TierFrontline {
			t.Fatalf("%s: entry %+v", c.op, last)
		}
	}

	// orderCancel is requireManager: tier 3 is refused 403 and records
	// nothing; tier 2 passes.
	if got := f.do(t, http.MethodPost, "/api/v1/orders/b3-order-1/cancel", staff, xorg).Code; got != http.StatusForbidden {
		t.Fatalf("orderCancel tier 3: %d", got)
	}
	f.staff.auth = identity.StaffAuthority{RoleID: "branch_manager", Tier: identity.TierManager, Scope: identity.ScopeBranch}
	if got := f.do(t, http.MethodPost, "/api/v1/orders/b3-order-1/cancel", staff, xorg).Code; got != http.StatusOK {
		t.Fatalf("orderCancel tier 2: %d", got)
	}
	f.staff.auth = identity.StaffAuthority{RoleID: "inside_sales", Tier: identity.TierFrontline, Scope: identity.ScopeBranch}

	// The tenant set.
	for _, c := range []struct{ method, path, op string }{
		{http.MethodGet, "/api/v1/tenant/me", "tenantGetMe"},
		{http.MethodPut, "/api/v1/tenant/me", "tenantUpdateMe"},
		{http.MethodPost, "/api/v1/tenant/token/refresh", "tenantRefreshToken"},
	} {
		if got := f.do(t, c.method, c.path, tenant, nil).Code; got != http.StatusOK {
			t.Fatalf("%s: status %d", c.op, got)
		}
		if last := f.rec.Entries()[len(f.rec.Entries())-1]; last.Operation != c.op || last.Tenant == nil || last.Tenant.TenantID != fxTenant {
			t.Fatalf("%s: entry %+v", c.op, last)
		}
	}
	if got := f.do(t, http.MethodGet, "/api/v1/tenant/me", tenant, xorg).Code; got != http.StatusBadRequest {
		t.Fatalf("tenant X-Org: %d", got)
	}

	// The two sync operations under both bearers.
	if got := f.do(t, http.MethodGet, "/api/v1/sync/orders", staff, xorg).Code; got != http.StatusOK {
		t.Fatalf("syncPull staff: %d", got)
	}
	if last := f.rec.Entries()[len(f.rec.Entries())-1]; last.Operation != "syncPull" || last.Staff == nil {
		t.Fatalf("syncPull staff entry %+v", last)
	}
	if got := f.do(t, http.MethodPost, "/api/v1/sync/orders", tenant, nil).Code; got != http.StatusOK {
		t.Fatalf("syncPush tenant: %d", got)
	}
	if last := f.rec.Entries()[len(f.rec.Entries())-1]; last.Operation != "syncPush" || last.Tenant == nil {
		t.Fatalf("syncPush tenant entry %+v", last)
	}

	// The public operation: reachable with no credential at all, and an
	// X-Org header on it has zero effect (ADR-0020 leg 2).
	if got := f.do(t, http.MethodGet, "/api/public/v1/quotes/b3-token", "", nil).Code; got != http.StatusOK {
		t.Fatalf("public no credential: %d", got)
	}
	if last := f.rec.Entries()[len(f.rec.Entries())-1]; last.Operation != "quotePublicViewQuote" || last.Staff != nil || last.Tenant != nil {
		t.Fatalf("public entry %+v", last)
	}
	if got := f.do(t, http.MethodGet, "/api/public/v1/quotes/b3-token", "", xorg).Code; got != http.StatusOK {
		t.Fatalf("public with X-Org: %d", got)
	}

	// The capability-qualified portal operation, both directions.
	if got := f.do(t, http.MethodGet, "/api/v1/tenant/invoices", tenant, nil).Code; got != http.StatusOK {
		t.Fatalf("invoices account_admin: %d", got)
	}
	f.portal.role = identity.PortalRoleFieldCrew
	if got := f.do(t, http.MethodGet, "/api/v1/tenant/invoices", tenant, nil).Code; got != http.StatusForbidden {
		t.Fatalf("invoices field_crew: %d", got)
	}

	// The anonymous legs of the guarded surface answer 401 and record
	// nothing.
	before := len(f.rec.Entries())
	if got := f.do(t, http.MethodGet, "/api/v1/orders/b3-order-1", "", xorg).Code; got != http.StatusUnauthorized {
		t.Fatalf("anonymous orderRead: %d", got)
	}
	if got := f.do(t, http.MethodGet, "/api/v1/sync/orders", "", nil).Code; got != http.StatusUnauthorized {
		t.Fatalf("anonymous syncPull: %d", got)
	}
	if len(f.rec.Entries()) != before {
		t.Fatalf("refusals recorded entries")
	}
}

// The recorder is bounded: the oldest entries give way at the limit.
func TestRecorderBounded(t *testing.T) {
	rec := NewRecorder(2)
	rec.record(Reached{Operation: "one"})
	rec.record(Reached{Operation: "two"})
	rec.record(Reached{Operation: "three"})
	entries := rec.Entries()
	if len(entries) != 2 || entries[0].Operation != "two" || entries[1].Operation != "three" {
		t.Fatalf("entries = %+v", entries)
	}
	if len(rec.Entries()) != 2 {
		t.Fatalf("Entries is not a copy")
	}
}

// TestRegisterRoutes_ServedSetExcludesSuperseded pins the S-3 removal: the
// served registrar answers none of the operationIds the real modules
// registered (the five order lifecycle routes and the public quote view
// from the money-spine batch; the tenant me pair and the invoices read
// from the portal re-homing). A re-added entry would collide with the real
// module's pattern on the serve mux and panic at boot; this test catches
// it in the suite instead, naming the operation.
func TestRegisterRoutes_ServedSetExcludesSuperseded(t *testing.T) {
	f := newShimFixture(t, identity.TierFrontline, "inside_sales", identity.ScopeBranch)
	staff := f.token(t, fxStaffAud)
	xorg := map[string]string{authz.HeaderXOrg: fxOrgSlug}
	tenant := f.token(t, fxTenaAud)
	served := http.NewServeMux()
	RegisterRoutes(served, f.g, f.rec)
	staffCases := []struct{ method, path, op string }{
		{http.MethodGet, "/api/v1/orders/b3-order-1", "orderRead"},
		{http.MethodPost, "/api/v1/orders/b3-order-1/confirm", "orderConfirm"},
		{http.MethodPost, "/api/v1/orders/b3-order-1/picking", "orderMarkPicking"},
		{http.MethodPost, "/api/v1/orders/b3-order-1/ready", "orderMarkReady"},
		{http.MethodPost, "/api/v1/orders/b3-order-1/cancel", "orderCancel"},
		{http.MethodGet, "/api/public/v1/quotes/fx-quote-token", "quotePublicViewQuote"},
	}
	for _, c := range staffCases {
		req := httptest.NewRequest(c.method, c.path, nil)
		req.Header.Set("Authorization", "Bearer "+staff)
		for k, v := range xorg {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		served.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: served shim set still answers %d, want 404 (the real module owns the pattern)", c.op, rec.Code)
		}
	}
	for _, c := range []struct{ method, path, op string }{
		{http.MethodGet, "/api/v1/tenant/me", "tenantGetMe"},
		{http.MethodPut, "/api/v1/tenant/me", "tenantUpdateMe"},
		{http.MethodGet, "/api/v1/tenant/invoices", "tenantListInvoices"},
	} {
		req := httptest.NewRequest(c.method, c.path, nil)
		req.Header.Set("Authorization", "Bearer "+tenant)
		rec := httptest.NewRecorder()
		served.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: served shim set still answers %d, want 404 (the portal module owns the pattern)", c.op, rec.Code)
		}
	}
}
