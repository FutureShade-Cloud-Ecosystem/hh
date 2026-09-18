package guards

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

const (
	fxIssuer    = "test-issuer"
	fxStaffAud  = "hh-staff"
	fxTenantAud = "hh-tenant"
	fxKeyID     = "b3-fixture-rsa"
	fxSubject   = "b3-platform-user-1"
	fxTenant    = "33333333-3333-4333-8333-333333333333"
	fxOrgSlug   = "b3yard"
	fxOrgID     = "44444444-4444-4444-8444-444444444444"
	fxBranchA   = "55555555-5555-4555-8555-555555555555"
	fxBranchB   = "66666666-6666-4666-8666-666666666666"
)

// fakeMembers is a MembershipSource double: member keyed on the slug.
type fakeMembers struct {
	member func(orgSlug string) bool
}

func (f *fakeMembers) OrgMember(_ context.Context, _, orgSlug string) (bool, error) {
	return f.member(orgSlug), nil
}

func (f *fakeMembers) TenantOrgs(context.Context, string) ([]string, error) {
	return []string{fxOrgSlug}, nil
}

// fakeOrgs is an OrgResolver double over a fixed anchor map.
type fakeOrgs struct{ anchors map[string]string }

func (f *fakeOrgs) ResolveSlug(_ context.Context, slug string) (string, bool, error) {
	id, ok := f.anchors[slug]
	return id, ok, nil
}

// fakeStaff is a StaffAuthoritySource double.
type fakeStaff struct {
	mu    sync.Mutex
	auth  identity.StaffAuthority
	ok    bool
	err   error
	calls int
}

func (f *fakeStaff) ResolveStaff(context.Context, string, string) (identity.StaffAuthority, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.auth, f.ok, f.err
}

// fakePortal is a PortalRoleSource double.
type fakePortal struct {
	role string
	ok   bool
	err  error
}

func (f *fakePortal) PortalRole(context.Context, string, string) (string, bool, error) {
	return f.role, f.ok, f.err
}

// knobFake is the injectable knob source.
type knobFake struct {
	hours int
	err   error
	calls int
}

func (k *knobFake) DriverSessionHours(context.Context, string) (int, error) {
	k.calls++
	return k.hours, k.err
}

// fixture wires a real Gate over runtime-generated keys and the guard
// doubles; authority, portal and knob are flipped per test.
type fixture struct {
	keys   *rsa.PrivateKey
	gate   *authz.Gate
	staff  *fakeStaff
	portal *fakePortal
	g      *Guards
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	verifier, err := identity.NewVerifier(identity.Config{
		KeySource: identity.NewStaticKeySource(map[string]crypto.PublicKey{
			fxKeyID: &key.PublicKey,
		}),
		Issuer:         fxIssuer,
		StaffAudience:  fxStaffAud,
		TenantAudience: fxTenantAud,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	f := &fixture{
		keys: key,
		staff: &fakeStaff{ok: true, auth: identity.StaffAuthority{
			RoleID: "inside_sales", Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
		}},
		portal: &fakePortal{ok: true, role: identity.PortalRoleAccountAdmin},
	}
	members := &fakeMembers{member: func(string) bool { return true }}
	orgs := &fakeOrgs{anchors: map[string]string{fxOrgSlug: fxOrgID}}
	f.gate, err = authz.NewGate(verifier, members, orgs)
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	f.g, err = NewGuards(f.gate, f.staff, f.portal, nil)
	if err != nil {
		t.Fatalf("NewGuards: %v", err)
	}
	return f
}

// withKnob rebuilds the Guards over a knob source.
func (f *fixture) withKnob(t *testing.T, knob identity.DriverSessionHours) *Guards {
	t.Helper()
	g, err := NewGuards(f.gate, f.staff, f.portal, knob)
	if err != nil {
		t.Fatalf("NewGuards: %v", err)
	}
	return g
}

// staffToken mints a staff token whose self-declared window is window
// (exp minus iat).
func (f *fixture) staffToken(t *testing.T, window time.Duration) string {
	t.Helper()
	now := time.Now()
	return f.mint(t, map[string]any{
		"iss": fxIssuer,
		"sub": fxSubject,
		"aud": fxStaffAud,
		"iat": now.Add(-window + 10*time.Minute).Unix(),
		"exp": now.Add(10 * time.Minute).Unix(),
	})
}

func (f *fixture) tenantToken(t *testing.T) string {
	t.Helper()
	now := time.Now()
	return f.mint(t, map[string]any{
		"iss":    fxIssuer,
		"sub":    fxSubject,
		"aud":    fxTenantAud,
		"iat":    now.Add(-time.Minute).Unix(),
		"exp":    now.Add(10 * time.Minute).Unix(),
		"tenant": fxTenant,
	})
}

func (f *fixture) mint(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": "RS256", "kid": fxKeyID, "typ": "JWT"}
	hb, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	b64 := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
	signed := b64(hb) + "." + b64(cb)
	digest := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.keys, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signed + "." + b64(sig)
}

// call runs one request through a handler with the given bearer and
// optional headers, and returns the recorded response.
func call(t *testing.T, h http.Handler, method, target, bearer string, header map[string]string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// captureCaller records the StaffCaller a guard handed its handler.
type callerRecorder struct {
	mu      sync.Mutex
	reached int
	caller  StaffCaller
}

func (c *callerRecorder) handler() StaffHandler {
	return func(w http.ResponseWriter, r *http.Request, caller StaffCaller) {
		c.mu.Lock()
		c.reached++
		c.caller = caller
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}
}

func authorityOf(roleID string, tier int, scope string, branches ...string) identity.StaffAuthority {
	return identity.StaffAuthority{RoleID: roleID, Tier: tier, Scope: scope, Branches: branches}
}

// The tier legs, both halves of every predicate: tier 3 passes staff and
// is refused by manager; tier 2 passes manager and is refused by admin;
// tier 1 global passes admin; tier 1 branch-scoped is refused by admin
// (the scope half of IsAdmin). Refusals are byte-identical to the
// middleware's 403 body.
func TestTierLegsBothHalves(t *testing.T) {
	f := newFixture(t)
	rec := &callerRecorder{}
	cases := []struct {
		name    string
		auth    identity.StaffAuthority
		manager int
		admin   int
	}{
		{"tier 3 branch", authorityOf("inside_sales", identity.TierFrontline, identity.ScopeBranch, fxBranchA), http.StatusForbidden, http.StatusForbidden},
		{"tier 2 branch", authorityOf("branch_manager", identity.TierManager, identity.ScopeBranch, fxBranchA), http.StatusOK, http.StatusForbidden},
		{"tier 1 global", authorityOf("general_manager", identity.TierLeadership, identity.ScopeGlobal), http.StatusOK, http.StatusOK},
		{"tier 1 branch", authorityOf("b3_tier1_branch", identity.TierLeadership, identity.ScopeBranch, fxBranchA), http.StatusOK, http.StatusForbidden},
	}
	for _, c := range cases {
		f.staff.auth = c.auth
		staffCode, body := call(t, f.g.RequireStaff(rec.handler()), http.MethodGet, "/api/v1/x", f.staffToken(t, 10*time.Minute), map[string]string{authz.HeaderXOrg: fxOrgSlug})
		if staffCode != http.StatusOK {
			t.Fatalf("%s: requireStaff status %d body %s", c.name, staffCode, body)
		}
		mgrCode, mgrBody := call(t, f.g.RequireManager(rec.handler()), http.MethodGet, "/api/v1/x", f.staffToken(t, 10*time.Minute), map[string]string{authz.HeaderXOrg: fxOrgSlug})
		if mgrCode != c.manager {
			t.Fatalf("%s: requireManager status %d want %d", c.name, mgrCode, c.manager)
		}
		if mgrCode == http.StatusForbidden && mgrBody != string(authz.BodyForbidden) {
			t.Fatalf("%s: manager refusal body %s", c.name, mgrBody)
		}
		adminCode, adminBody := call(t, f.g.RequireAdmin(rec.handler()), http.MethodGet, "/api/v1/x", f.staffToken(t, 10*time.Minute), map[string]string{authz.HeaderXOrg: fxOrgSlug})
		if adminCode != c.admin {
			t.Fatalf("%s: requireAdmin status %d want %d", c.name, adminCode, c.admin)
		}
		if adminCode == http.StatusForbidden && adminBody != string(authz.BodyForbidden) {
			t.Fatalf("%s: admin refusal body %s", c.name, adminBody)
		}
	}
	if rec.reached != 1+2+3+2 {
		t.Fatalf("handler reached %d times, want one per admitted tier leg", rec.reached)
	}
	// The caller the handler received carries the resolved authority.
	f.staff.auth = authorityOf("branch_manager", identity.TierManager, identity.ScopeBranch, fxBranchA)
	call(t, f.g.RequireStaff(rec.handler()), http.MethodGet, "/api/v1/x", f.staffToken(t, 10*time.Minute), map[string]string{authz.HeaderXOrg: fxOrgSlug})
	_, caller := rec.result()
	if caller.RoleID != "branch_manager" || caller.Tier != identity.TierManager || caller.OrgID != fxOrgID || caller.PlatformUserID != fxSubject {
		t.Fatalf("caller = %+v", caller)
	}
}

func (c *callerRecorder) result() (int, StaffCaller) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reached, c.caller
}

// No active grant, no token, wrong class, and an authority error: the
// guard answers 403, 401, 403 and 503 respectively, each with the shared
// family body, and never reaches the handler.
func TestGuardRefusalFamilies(t *testing.T) {
	f := newFixture(t)
	rec := &callerRecorder{}
	staffHeaders := map[string]string{authz.HeaderXOrg: fxOrgSlug}

	f.staff.ok = false
	code, body := call(t, f.g.RequireStaff(rec.handler()), http.MethodGet, "/api/v1/x", f.staffToken(t, 10*time.Minute), staffHeaders)
	if code != http.StatusForbidden || body != string(authz.BodyForbidden) {
		t.Fatalf("no grant: %d %s", code, body)
	}

	f.staff.ok = true
	code, body = call(t, f.g.RequireStaff(rec.handler()), http.MethodGet, "/api/v1/x", "", staffHeaders)
	if code != http.StatusUnauthorized || body != string(authz.BodyUnauthorized) {
		t.Fatalf("no token: %d %s", code, body)
	}

	code, body = call(t, f.g.RequireStaff(rec.handler()), http.MethodGet, "/api/v1/x", f.tenantToken(t), staffHeaders)
	if code != http.StatusForbidden || body != string(authz.BodyForbidden) {
		t.Fatalf("tenant token on a staff guard: %d %s", code, body)
	}

	code, body = call(t, f.g.RequireCapability(identity.CapViewBilling, func(http.ResponseWriter, *http.Request, authz.TenantScope) {}), http.MethodGet, "/api/v1/x", f.staffToken(t, 10*time.Minute), nil)
	if code != http.StatusForbidden || body != string(authz.BodyForbidden) {
		t.Fatalf("staff token on a capability guard: %d %s", code, body)
	}

	f.staff.err = errors.New("resolver down")
	code, body = call(t, f.g.RequireStaff(rec.handler()), http.MethodGet, "/api/v1/x", f.staffToken(t, 10*time.Minute), staffHeaders)
	if code != http.StatusServiceUnavailable || body != string(authz.BodyUnavailable) {
		t.Fatalf("authority error: %d %s", code, body)
	}

	// The branch-id rule violation answers the 503 family too: an authority
	// row that cannot be interpreted is a data defect, never an admission.
	f.staff.err = identity.ErrGrantShape
	code, body = call(t, f.g.RequireStaff(rec.handler()), http.MethodGet, "/api/v1/x", f.staffToken(t, 10*time.Minute), staffHeaders)
	if code != http.StatusServiceUnavailable || body != string(authz.BodyUnavailable) {
		t.Fatalf("grant shape violation: %d %s", code, body)
	}
	f.staff.err = nil

	if reached, _ := rec.result(); reached != 0 {
		t.Fatalf("handler reached on a refusal %d times", reached)
	}
}

// The account-status refusals (review findings F-1 and F-2), both
// directions: a suspended staff account and an inactive portal user each
// answer 401 with the shared unauthorized body and never reach the
// handler, while the active direction is every admitted leg around them.
func TestAccountStatusRefusals(t *testing.T) {
	f := newFixture(t)
	rec := &callerRecorder{}
	headers := map[string]string{authz.HeaderXOrg: fxOrgSlug}

	// Staff: the inactive sentinel maps to the 401 family, not the 503 a
	// generic authority error takes.
	f.staff.err = identity.ErrStaffUserInactive
	code, body := call(t, f.g.RequireStaff(rec.handler()), http.MethodGet, "/api/v1/x", f.staffToken(t, 10*time.Minute), headers)
	if code != http.StatusUnauthorized || body != string(authz.BodyUnauthorized) {
		t.Fatalf("inactive staff account: %d %s", code, body)
	}
	// The sentinel outranks a tier gate: an inactive account on a manager
	// route is refused for the credential, not the gate.
	f.staff.auth = authorityOf("general_manager", identity.TierLeadership, identity.ScopeGlobal)
	code, body = call(t, f.g.RequireManager(rec.handler()), http.MethodGet, "/api/v1/x", f.staffToken(t, 10*time.Minute), headers)
	if code != http.StatusUnauthorized || body != string(authz.BodyUnauthorized) {
		t.Fatalf("inactive staff account on a manager route: %d %s", code, body)
	}
	f.staff.err = nil

	// Portal: the inactive sentinel maps to the 401 family, not the 403 a
	// lacking capability takes and not the 503 an unreadable role takes.
	f.portal.err = identity.ErrPortalUserInactive
	code, body = call(t, f.g.RequireCapability(identity.CapViewBilling, func(http.ResponseWriter, *http.Request, authz.TenantScope) {}), http.MethodGet, "/api/v1/x", f.tenantToken(t), nil)
	if code != http.StatusUnauthorized || body != string(authz.BodyUnauthorized) {
		t.Fatalf("inactive portal user: %d %s", code, body)
	}
	f.portal.err = nil

	if reached, _ := rec.result(); reached != 0 {
		t.Fatalf("handler reached on an account-status refusal %d times", reached)
	}
}

// A nil predicate is a wiring defect the guard cannot run: 503 with the
// shared body, handler not reached (arbitration A-9).
func TestRequireStaffPredicateNilRefuses(t *testing.T) {
	f := newFixture(t)
	rec := &callerRecorder{}
	h := f.g.RequireStaffPredicate(nil, rec.handler())
	code, body := call(t, h, http.MethodGet, "/api/v1/x", f.staffToken(t, 10*time.Minute), map[string]string{authz.HeaderXOrg: fxOrgSlug})
	if code != http.StatusServiceUnavailable || body != string(authz.BodyUnavailable) {
		t.Fatalf("nil predicate: %d %s", code, body)
	}
	if reached, _ := rec.result(); reached != 0 {
		t.Fatalf("handler reached on a nil predicate")
	}
}

// The driver ceiling legs: within the knob passes, over the knob refuses
// 401 with the shared unauthorized body, both knob edges are exact, every
// uncomputable arm refuses 503, and the non-driver arm keeps the standard
// session without reading the knob.
func TestDriverCeilingLegs(t *testing.T) {
	f := newFixture(t)
	rec := &callerRecorder{}
	headers := map[string]string{authz.HeaderXOrg: fxOrgSlug}
	f.staff.auth = authorityOf(identity.RoleDriver, identity.TierFrontline, identity.ScopeBranch, fxBranchA)

	// Within: knob 12, window 11h.
	within := f.withKnob(t, &knobFake{hours: 12})
	if code, body := call(t, within.RequireStaff(rec.handler()), http.MethodGet, "/api/v1/x", f.staffToken(t, 11*time.Hour), headers); code != http.StatusOK {
		t.Fatalf("within ceiling: %d %s", code, body)
	}

	// Over: knob 1, window 2h; the refusal is the 401 family.
	over := f.withKnob(t, &knobFake{hours: 1})
	code, body := call(t, over.RequireStaff(rec.handler()), http.MethodGet, "/api/v1/x", f.staffToken(t, 2*time.Hour), headers)
	if code != http.StatusUnauthorized || body != string(authz.BodyUnauthorized) {
		t.Fatalf("over ceiling: %d %s", code, body)
	}

	// Edge 1: exactly one hour passes, one second over refuses.
	edge1 := f.withKnob(t, &knobFake{hours: 1})
	if code, _ := call(t, edge1.RequireStaff(rec.handler()), http.MethodGet, "/api/v1/x", f.staffToken(t, time.Hour), headers); code != http.StatusOK {
		t.Fatalf("edge 1 exact: %d", code)
	}
	if code, body := call(t, edge1.RequireStaff(rec.handler()), http.MethodGet, "/api/v1/x", f.staffToken(t, time.Hour+time.Second), headers); code != http.StatusUnauthorized || body != string(authz.BodyUnauthorized) {
		t.Fatalf("edge 1 over: %d %s", code, body)
	}

	// Edge 168: exactly the backstop passes, one second over refuses.
	edge168 := f.withKnob(t, &knobFake{hours: 168})
	if code, _ := call(t, edge168.RequireStaff(rec.handler()), http.MethodGet, "/api/v1/x", f.staffToken(t, 168*time.Hour), headers); code != http.StatusOK {
		t.Fatalf("edge 168 exact: %d", code)
	}
	if code, _ := call(t, edge168.RequireStaff(rec.handler()), http.MethodGet, "/api/v1/x", f.staffToken(t, 168*time.Hour+time.Second), headers); code != http.StatusUnauthorized {
		t.Fatalf("edge 168 over: %d", code)
	}

	// Uncomputable arms: no source wired, source error, out-of-contract
	// values; all 503, none admits.
	if code, body := call(t, f.g.RequireStaff(rec.handler()), http.MethodGet, "/api/v1/x", f.staffToken(t, time.Hour), headers); code != http.StatusServiceUnavailable || body != string(authz.BodyUnavailable) {
		t.Fatalf("unwired knob: %d %s", code, body)
	}
	errKnob := f.withKnob(t, &knobFake{err: errors.New("policy unreadable")})
	if code, _ := call(t, errKnob.RequireStaff(rec.handler()), http.MethodGet, "/api/v1/x", f.staffToken(t, time.Hour), headers); code != http.StatusServiceUnavailable {
		t.Fatalf("erroring knob: %d", code)
	}
	for _, hours := range []int{0, 169} {
		bad := f.withKnob(t, &knobFake{hours: hours})
		if code, _ := call(t, bad.RequireStaff(rec.handler()), http.MethodGet, "/api/v1/x", f.staffToken(t, time.Hour), headers); code != http.StatusServiceUnavailable {
			t.Fatalf("out-of-contract knob %d: %d", hours, code)
		}
	}

	// Per-role selection: a non-driver role over the standard session
	// refuses 401 even with a wide knob, and never reads the knob.
	f.staff.auth = authorityOf("inside_sales", identity.TierFrontline, identity.ScopeBranch, fxBranchA)
	wide := &knobFake{hours: 168}
	wideKnob := f.withKnob(t, wide)
	if code, _ := call(t, wideKnob.RequireStaff(rec.handler()), http.MethodGet, "/api/v1/x", f.staffToken(t, 13*time.Hour), headers); code != http.StatusUnauthorized {
		t.Fatalf("non-driver over the standard session: %d", code)
	}
	if wide.calls != 0 {
		t.Fatalf("non-driver arm read the knob %d times", wide.calls)
	}

	// Ceiling runs ahead of the tier predicate: an over-ceiling driver on
	// a manager route is refused for the credential, not the gate.
	f.staff.auth = authorityOf(identity.RoleDriver, identity.TierFrontline, identity.ScopeBranch, fxBranchA)
	overManager := f.withKnob(t, &knobFake{hours: 1})
	if code, body := call(t, overManager.RequireManager(rec.handler()), http.MethodGet, "/api/v1/x", f.staffToken(t, 2*time.Hour), headers); code != http.StatusUnauthorized || body != string(authz.BodyUnauthorized) {
		t.Fatalf("ceiling before tier: %d %s", code, body)
	}
	if reached, _ := rec.result(); reached != 3 {
		t.Fatalf("handler reached %d times, want the three admitted legs", reached)
	}
}

// The capability legs, both halves for each of the eight guardable
// capabilities: a role that holds it passes, a role that lacks it answers
// the RBAC-403, no linked portal user answers 403, and an unreadable role
// answers 503.
func TestCapabilityLegsBothHalves(t *testing.T) {
	f := newFixture(t)
	tenantHandler := func(w http.ResponseWriter, r *http.Request, scope authz.TenantScope) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok":true}`)
	}
	cases := []struct {
		cap    identity.Capability
		holder string
		lacker string
	}{
		{identity.CapManageUsers, identity.PortalRoleAccountAdmin, identity.PortalRoleBuyer},
		{identity.CapViewBilling, identity.PortalRoleAccountAdmin, identity.PortalRoleFieldCrew},
		{identity.CapPayInvoices, identity.PortalRoleAccountAdmin, identity.PortalRoleBuyerNoPay},
		{identity.CapManagePaymentMethods, identity.PortalRoleAccountAdmin, identity.PortalRoleBuyerNoPay},
		{identity.CapSubmitRFQ, identity.PortalRoleAccountAdmin, identity.PortalRoleAPFinance},
		{identity.CapCreateOrders, identity.PortalRoleAccountAdmin, identity.PortalRoleAPFinance},
		{identity.CapViewOrdersDeliveries, identity.PortalRoleFieldCrew, ""},
		{identity.CapEditDeliveryInstructions, identity.PortalRoleFieldCrew, identity.PortalRoleAPFinance},
	}
	for _, c := range cases {
		f.portal.ok = true
		f.portal.role = c.holder
		if code, body := call(t, f.g.RequireCapability(c.cap, tenantHandler), http.MethodGet, "/api/v1/x", f.tenantToken(t), nil); code != http.StatusOK {
			t.Fatalf("%s holder %s: %d %s", c.cap, c.holder, code, body)
		}
		if c.lacker != "" {
			f.portal.role = c.lacker
			code, body := call(t, f.g.RequireCapability(c.cap, tenantHandler), http.MethodGet, "/api/v1/x", f.tenantToken(t), nil)
			if code != http.StatusForbidden || body != string(authz.BodyForbidden) {
				t.Fatalf("%s lacker %s: %d %s", c.cap, c.lacker, code, body)
			}
		}
		// view_orders_deliveries is held by every canonical role, so its
		// negative half is the absent link rather than a lacking role.
		f.portal.ok = false
		code, body := call(t, f.g.RequireCapability(c.cap, tenantHandler), http.MethodGet, "/api/v1/x", f.tenantToken(t), nil)
		if code != http.StatusForbidden || body != string(authz.BodyForbidden) {
			t.Fatalf("%s no linked user: %d %s", c.cap, code, body)
		}
		f.portal.ok = true
	}
	f.portal.err = errors.New("portal unreadable")
	if code, body := call(t, f.g.RequireCapability(identity.CapViewBilling, tenantHandler), http.MethodGet, "/api/v1/x", f.tenantToken(t), nil); code != http.StatusServiceUnavailable || body != string(authz.BodyUnavailable) {
		t.Fatalf("portal error: %d %s", code, body)
	}
}

// The tenant guard stamps and hands the scope; the tenant leg of G4 still
// applies through the guard (X-Org refused 400).
func TestRequireTenantLegs(t *testing.T) {
	f := newFixture(t)
	var got authz.TenantScope
	reached := 0
	h := f.g.RequireTenant(func(w http.ResponseWriter, r *http.Request, scope authz.TenantScope) {
		reached++
		got = scope
		w.WriteHeader(http.StatusOK)
	})
	if code, body := call(t, h, http.MethodGet, "/api/v1/x", f.tenantToken(t), nil); code != http.StatusOK {
		t.Fatalf("tenant leg: %d %s", code, body)
	}
	if got.TenantID != fxTenant || got.OrgID != fxOrgID || got.PlatformUserID != fxSubject {
		t.Fatalf("tenant scope = %+v", got)
	}
	if code, body := call(t, h, http.MethodGet, "/api/v1/x", f.tenantToken(t), map[string]string{authz.HeaderXOrg: fxOrgSlug}); code != http.StatusBadRequest {
		t.Fatalf("tenant X-Org: %d %s", code, body)
	}
	if code, _ := call(t, h, http.MethodGet, "/api/v1/x", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous tenant: %d", code)
	}
	if reached != 1 {
		t.Fatalf("handler reached %d times", reached)
	}
}

// The sync guard hands the staff caller under the staff bearer and the
// tenant scope under the tenant bearer, and refuses a scopeless handler
// state fail closed.
func TestRequireSyncBothBearers(t *testing.T) {
	f := newFixture(t)
	var lastStaff, lastTenant bool
	h := f.g.RequireSync(func(w http.ResponseWriter, r *http.Request, caller SyncCaller) {
		lastStaff = caller.Staff != nil
		lastTenant = caller.Tenant != nil
		if lastStaff && caller.Staff.RoleID != "inside_sales" {
			t.Errorf("sync staff caller = %+v", caller.Staff)
		}
		if lastTenant && caller.Tenant.TenantID != fxTenant {
			t.Errorf("sync tenant scope = %+v", caller.Tenant)
		}
		w.WriteHeader(http.StatusOK)
	})
	if code, body := call(t, h, http.MethodGet, "/api/v1/sync/orders", f.staffToken(t, 10*time.Minute), map[string]string{authz.HeaderXOrg: fxOrgSlug}); code != http.StatusOK {
		t.Fatalf("sync staff leg: %d %s", code, body)
	}
	if !lastStaff || lastTenant {
		t.Fatalf("sync staff leg carriers: staff=%v tenant=%v", lastStaff, lastTenant)
	}
	if code, body := call(t, h, http.MethodPost, "/api/v1/sync/orders", f.tenantToken(t), nil); code != http.StatusOK {
		t.Fatalf("sync tenant leg: %d %s", code, body)
	}
	if lastStaff || !lastTenant {
		t.Fatalf("sync tenant leg carriers: staff=%v tenant=%v", lastStaff, lastTenant)
	}
	// The staff leg of sync enforces authority: no grant is 403.
	f.staff.ok = false
	if code, body := call(t, h, http.MethodGet, "/api/v1/sync/orders", f.staffToken(t, 10*time.Minute), map[string]string{authz.HeaderXOrg: fxOrgSlug}); code != http.StatusForbidden || body != string(authz.BodyForbidden) {
		t.Fatalf("sync staff no-grant: %d %s", code, body)
	}
}

// Branch facts through the resolved caller, both halves: a branch-scoped
// authority reaches its granted branch only; a global authority reaches
// any branch.
func TestCallerBranchFacts(t *testing.T) {
	f := newFixture(t)
	rec := &callerRecorder{}
	f.staff.auth = authorityOf("inside_sales", identity.TierFrontline, identity.ScopeBranch, fxBranchA)
	call(t, f.g.RequireStaff(rec.handler()), http.MethodGet, "/api/v1/x", f.staffToken(t, 10*time.Minute), map[string]string{authz.HeaderXOrg: fxOrgSlug})
	_, caller := rec.result()
	if !caller.CanAccessBranch(fxBranchA) {
		t.Fatalf("granted branch refused")
	}
	if caller.CanAccessBranch(fxBranchB) {
		t.Fatalf("ungranted branch admitted")
	}
	f.staff.auth = authorityOf("general_manager", identity.TierLeadership, identity.ScopeGlobal)
	call(t, f.g.RequireStaff(rec.handler()), http.MethodGet, "/api/v1/x", f.staffToken(t, 10*time.Minute), map[string]string{authz.HeaderXOrg: fxOrgSlug})
	_, caller = rec.result()
	if !caller.CanAccessBranch(fxBranchB) {
		t.Fatalf("global scope cannot reach an ungranted branch")
	}
}

// Construction fails closed on every missing required collaborator.
func TestNewGuardsFailsClosed(t *testing.T) {
	f := newFixture(t)
	if _, err := NewGuards(nil, f.staff, f.portal, nil); err == nil || !strings.Contains(err.Error(), "gate") {
		t.Fatalf("nil gate: %v", err)
	}
	if _, err := NewGuards(f.gate, nil, f.portal, nil); err == nil {
		t.Fatalf("nil staff source accepted")
	}
	if _, err := NewGuards(f.gate, f.staff, nil, nil); err == nil {
		t.Fatalf("nil portal source accepted")
	}
}
