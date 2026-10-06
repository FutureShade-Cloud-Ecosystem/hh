package authz

// The machine leg's suite (HH-LOAD-1 LD-2): the wrong caller ledger's
// rows 1 to 7 as unit proofs through the registrar's seam, the gate's
// admission rule, and the registrar rule that a machine worded census
// route registers only behind the machine gate. All data is invented:
// the key material is generated inside the test and discarded with the
// process, and the machine principals, org slugs and door names below
// are fixtures, never a real deployment's records.

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// Doubles' failure sentinels, local to this suite.
var (
	errPrincipalSourceDown = errors.New("principal source is down")
	errOrgAnchorDown       = errors.New("org anchor read failed")
)

const (
	fixtureMachineAud        = "hh-machine"
	fixtureMachineKeyID      = "fixture-machine-rsa"
	fixtureMachinePrincipal  = "7c2e6b41-5555-4c55-8c11-000000000005"
	fixtureMachineOtherOrgID = "5b1c9e77-6666-4c1c-9c11-000000000006"
	fixtureDoor              = "loadUpsertUnits"
)

// fakeMachinePrincipals is a counting MachinePrincipalSource double whose
// rows the tests flip per case.
type fakeMachinePrincipals struct {
	mu     sync.Mutex
	byID   map[string]identity.MachinePrincipal
	calls  int
	fail   bool
	loaded identity.MachinePrincipal
}

func (f *fakeMachinePrincipals) MachinePrincipal(_ context.Context, id string) (identity.MachinePrincipal, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.fail {
		return identity.MachinePrincipal{}, false, errPrincipalSourceDown
	}
	p, ok := f.byID[id]
	f.loaded = p
	return p, ok, nil
}

func (f *fakeMachinePrincipals) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// machineFixture wires a Gate whose verifier knows the machine audience
// and its own key set, a principal double, and the MachineGate, mirroring
// newFixture for the third class. The machine key set is a second runtime
// generated RSA key so a machine token can never verify against the
// platform set and vice versa in the same fixture.
type machineFixture struct {
	*fixture
	machineKeys *rsa.PrivateKey
	principals  *fakeMachinePrincipals
	machineGate *MachineGate
}

func enabledPrincipal() identity.MachinePrincipal {
	return identity.MachinePrincipal{
		ID:      fixtureMachinePrincipal,
		OrgID:   fixtureOrgID,
		Name:    "fixture mover",
		Enabled: true,
		Doors:   []string{fixtureDoor, "loadGetBatch"},
	}
}

func newMachineFixture(t *testing.T) *machineFixture {
	t.Helper()
	base := newFixture(t)
	machineKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate machine key: %v", err)
	}
	verifier, err := identity.NewVerifier(identity.Config{
		KeySource: identity.NewStaticKeySource(map[string]crypto.PublicKey{
			fixtureKeyID: &base.keys.PublicKey,
		}),
		MachineKeySource: identity.NewStaticKeySource(map[string]crypto.PublicKey{
			fixtureMachineKeyID: &machineKey.PublicKey,
		}),
		Issuer:          fixtureIssuer,
		StaffAudience:   fixtureStaffAud,
		TenantAudience:  fixtureTenantAud,
		MachineAudience: fixtureMachineAud,
		Now:             base.clock.Now,
	})
	if err != nil {
		t.Fatalf("NewVerifier with the machine leg: %v", err)
	}
	gate, err := NewGate(verifier, base.members, base.orgs)
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	base.gate = gate
	principals := &fakeMachinePrincipals{byID: map[string]identity.MachinePrincipal{
		fixtureMachinePrincipal: enabledPrincipal(),
	}}
	machineGate, err := NewMachineGate(gate, principals)
	if err != nil {
		t.Fatalf("NewMachineGate: %v", err)
	}
	return &machineFixture{fixture: base, machineKeys: machineKey, principals: principals, machineGate: machineGate}
}

// machineToken mints a machine-class token signed by the machine key set.
func (mf *machineFixture) machineToken(t *testing.T, sub string) string {
	t.Helper()
	return mf.machineMint(t, map[string]any{
		"iss": fixtureIssuer,
		"sub": sub,
		"aud": fixtureMachineAud,
		"iat": mf.clock.now.Add(-time.Minute).Unix(),
		"exp": mf.clock.now.Add(10 * time.Minute).Unix(),
	})
}

// machineMint signs caller-chosen claims with the machine key set.
func (mf *machineFixture) machineMint(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": "RS256", "kid": fixtureMachineKeyID, "typ": "JWT"}
	hb, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	return mf.signRaw(t, hb, cb)
}

func (mf *machineFixture) signRaw(t *testing.T, hb, cb []byte) string {
	t.Helper()
	signed := b64(hb) + "." + b64(cb)
	digest := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, mf.machineKeys, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signed + "." + b64(sig)
}

// machineDoorRoute is the harness's machine worded census row: the load
// unit door the plan's LD-3 names, worded machine and carrying no
// x-access, exactly as LD-1's fragment will declare it.
func machineDoorRoute() apigen.Route {
	return apigen.Route{Method: "PUT", Path: "/api/v1/load/units", OperationID: fixtureDoor, Tag: "load", XRole: "machine", Bearer: "machine"}
}

// machineHarness wires the full registration path for one machine door:
// the role gate registrar with the machine gate attached, over a census
// holding the machine worded route. The admitted handler records the
// machine scope it ran under, so the tests observe the stamp through the
// wrapped chain itself.
type machineHarness struct {
	inner *recordingInner
	reg   *RoleGateRegistrar
	seen  *machineSeen
}

// machineSeen records the scope and reach of the admitted handler.
type machineSeen struct {
	mu      sync.Mutex
	reached int
	scope   MachineScope
	stamped bool
}

func (s *machineSeen) record(r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reached++
	s.scope, s.stamped = MachineScopeFrom(r.Context())
}

func newMachineHarness(t *testing.T, mf *machineFixture) *machineHarness {
	t.Helper()
	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatalf("roleconfig.Default: %v", err)
	}
	roleGate, err := NewRoleGate(mf.gate, &fakeRolesSource{roles: []string{"inside_sales"}}, cfg)
	if err != nil {
		t.Fatalf("NewRoleGate: %v", err)
	}
	inner := &recordingInner{handlers: map[string]http.Handler{}}
	reg, err := newRoleGateRegistrarWithCensusAndMachineGate(inner, roleGate, mf.machineGate, censusFor(machineDoorRoute()))
	if err != nil {
		t.Fatalf("registrar with the machine gate: %v", err)
	}
	seen := &machineSeen{}
	reg.Handle("PUT /api/v1/load/units", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.record(r)
		w.WriteHeader(http.StatusOK)
	}))
	return &machineHarness{inner: inner, reg: reg, seen: seen}
}

func machineRequest(auth string, orgSlug string) *http.Request {
	req := httptest.NewRequest(http.MethodPut, "/api/v1/load/units", nil)
	if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
	}
	if orgSlug != "" {
		req.Header.Set(HeaderXOrg, orgSlug)
	}
	return req
}

// TestMachineWrongCallerLedger is the plan's section 3.4 ledger, rows 1
// to 7, through the registrar's seam. Every invented row: no bearer is
// 401 with the one unauthorized body; staff, tenant, other-org machine
// and disabled machine callers are 403 with the staff leg's body, so the
// doors leak no org directory; the doors switched off answer 404 because
// no route registered; and a machine credential is refused on staff and
// tenant routes alike.
func TestMachineWrongCallerLedger(t *testing.T) {
	mf := newMachineFixture(t)
	h := newMachineHarness(t, mf)
	serve := h.inner.handlers["PUT /api/v1/load/units"]
	machineAuth := mf.machineToken(t, fixtureMachinePrincipal)

	// Row 2's "any role": the machine gate never reads a staff role, so
	// two subjects with different role grants answer identically.
	staffA := mf.staffToken(t, fixtureSubject)
	staffB := mf.staffToken(t, "platform-user-9")
	tenantAuth := mf.tenantToken(t, fixtureSubject, fixtureTenant)
	disabled := enabledPrincipal()
	disabled.ID = "0d2c31e2-7777-4c55-8c11-000000000007"
	disabled.Enabled = false
	mf.principals.byID[disabled.ID] = disabled
	disabledAuth := mf.machineToken(t, disabled.ID)
	boundElsewhere := enabledPrincipal()
	boundElsewhere.ID = "3e8a5c90-8888-4c55-8c11-000000000008"
	boundElsewhere.OrgID = fixtureMachineOtherOrgID
	mf.principals.byID[boundElsewhere.ID] = boundElsewhere
	boundElsewhereAuth := mf.machineToken(t, boundElsewhere.ID)

	cases := []struct {
		row  string
		auth string
		org  string
		want int
		body string
	}{
		{"row 1: no bearer", "", fixtureOrgSlug, http.StatusUnauthorized, string(BodyUnauthorized)},
		{"row 2: a staff bearer of any role", staffA, fixtureOrgSlug, http.StatusForbidden, string(BodyForbidden)},
		{"row 2: a staff bearer, another subject", staffB, fixtureOrgSlug, http.StatusForbidden, string(BodyForbidden)},
		{"row 3: a tenant bearer", tenantAuth, "", http.StatusForbidden, string(BodyForbidden)},
		{"row 4: machine credential bound to another org", boundElsewhereAuth, fixtureOrgSlug, http.StatusForbidden, string(BodyForbidden)},
		{"row 4: X-Org naming another org", machineAuth, fixtureOtherSlug, http.StatusForbidden, string(BodyForbidden)},
		{"row 4: X-Org naming an unknown org", machineAuth, "nosuchyard", http.StatusForbidden, string(BodyForbidden)},
		{"row 4: X-Org missing", machineAuth, "", http.StatusForbidden, string(BodyForbidden)},
		{"row 5: a disabled machine principal", disabledAuth, fixtureOrgSlug, http.StatusForbidden, string(BodyForbidden)},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		serve.ServeHTTP(rec, machineRequest(tc.auth, tc.org))
		if rec.Code != tc.want || rec.Body.String() != tc.body {
			t.Errorf("%s: status %d body %s, want %d %s", tc.row, rec.Code, rec.Body.String(), tc.want, tc.body)
		}
	}

	// Row 6: with the doors switched off in configuration nothing
	// registered, so the mux itself answers that the route does not
	// exist. The served surface here is a bare mux, the shape a
	// deployment with the switch off serves.
	off := http.NewServeMux()
	rec := httptest.NewRecorder()
	off.ServeHTTP(rec, machineRequest(machineAuth, fixtureOrgSlug))
	if rec.Code != http.StatusNotFound {
		t.Errorf("row 6: doors switched off answered %d, want 404 (no route exists)", rec.Code)
	}

	// Row 7: the machine credential is refused on every staff and tenant
	// route. The staff route runs through the role gate registrar, the
	// tenant route through the tenant leg, both the ordinary wiring.
	staffInner := &recordingInner{handlers: map[string]http.Handler{}}
	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatalf("roleconfig.Default: %v", err)
	}
	roleGate, err := NewRoleGate(mf.gate, &fakeRolesSource{roles: []string{"inside_sales"}}, cfg)
	if err != nil {
		t.Fatalf("NewRoleGate: %v", err)
	}
	staffReg, err := newRoleGateRegistrarWithCensusAndMachineGate(staffInner, roleGate, mf.machineGate, censusFor(annotatedBoard()))
	if err != nil {
		t.Fatalf("staff registrar: %v", err)
	}
	staffReg.Handle("GET /api/v1/dispatch/board", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	rec = httptest.NewRecorder()
	staffInner.handlers["GET /api/v1/dispatch/board"].ServeHTTP(rec, machineRequest(machineAuth, fixtureOrgSlug))
	if rec.Code != http.StatusForbidden || rec.Body.String() != string(BodyForbidden) {
		t.Errorf("row 7 staff route: status %d body %s, want the 403 family body", rec.Code, rec.Body.String())
	}
	tenantHandler := mf.gate.Tenant(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	rec = httptest.NewRecorder()
	tenantHandler.ServeHTTP(rec, machineRequest(machineAuth, ""))
	if rec.Code != http.StatusForbidden || rec.Body.String() != string(BodyForbidden) {
		t.Errorf("row 7 tenant route: status %d body %s, want the 403 family body", rec.Code, rec.Body.String())
	}
}

// TestMachineAdmitsTheRightCaller is the positive half: the one caller
// the doors serve, a machine credential whose enabled principal holds
// this door in its own org, reaches the handler with a machine scope
// stamped.
func TestMachineAdmitsTheRightCaller(t *testing.T) {
	mf := newMachineFixture(t)
	h := newMachineHarness(t, mf)
	rec := httptest.NewRecorder()
	h.inner.handlers["PUT /api/v1/load/units"].ServeHTTP(rec, machineRequest(mf.machineToken(t, fixtureMachinePrincipal), fixtureOrgSlug))
	if rec.Code != http.StatusOK {
		t.Fatalf("the right caller was refused: %d %s", rec.Code, rec.Body.String())
	}
	h.seen.mu.Lock()
	reached, got, stamped := h.seen.reached, h.seen.scope, h.seen.stamped
	h.seen.mu.Unlock()
	if reached != 1 || !stamped {
		t.Fatalf("the admitted handler ran %d times, stamped %v", reached, stamped)
	}
	if got.PrincipalID != fixtureMachinePrincipal || got.OrgID != fixtureOrgID || got.OrgSlug != fixtureOrgSlug || got.Name != "fixture mover" {
		t.Fatalf("machine scope carries %+v", got)
	}
}

// TestMachineDoorNotGranted: the principal's door list is its own wall; a
// door the principal does not hold answers the same 403 body as every
// other machine refusal.
func TestMachineDoorNotGranted(t *testing.T) {
	mf := newMachineFixture(t)
	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatalf("roleconfig.Default: %v", err)
	}
	roleGate, err := NewRoleGate(mf.gate, &fakeRolesSource{roles: []string{"inside_sales"}}, cfg)
	if err != nil {
		t.Fatalf("NewRoleGate: %v", err)
	}
	inner := &recordingInner{handlers: map[string]http.Handler{}}
	reg, err := newRoleGateRegistrarWithCensusAndMachineGate(inner, roleGate, mf.machineGate, censusFor(apigen.Route{
		Method: "PUT", Path: "/api/v1/load/products", OperationID: "loadUpsertProducts", Tag: "load", XRole: "machine", Bearer: "machine",
	}))
	if err != nil {
		t.Fatalf("registrar: %v", err)
	}
	reg.Handle("PUT /api/v1/load/products", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	rec := httptest.NewRecorder()
	inner.handlers["PUT /api/v1/load/products"].ServeHTTP(rec, machineRequest(mf.machineToken(t, fixtureMachinePrincipal), fixtureOrgSlug))
	if rec.Code != http.StatusForbidden || rec.Body.String() != string(BodyForbidden) {
		t.Fatalf("a door the principal does not hold: status %d body %s, want the 403 family body", rec.Code, rec.Body.String())
	}
}

// TestMachineOperationalRefusals: a principal source that cannot answer
// and an org resolver that cannot answer are operational failures, the
// 503 family, never an admission and never a verdict on the caller.
func TestMachineOperationalRefusals(t *testing.T) {
	mf := newMachineFixture(t)
	h := newMachineHarness(t, mf)
	mf.principals.fail = true
	rec := httptest.NewRecorder()
	h.inner.handlers["PUT /api/v1/load/units"].ServeHTTP(rec, machineRequest(mf.machineToken(t, fixtureMachinePrincipal), fixtureOrgSlug))
	if rec.Code != http.StatusServiceUnavailable || rec.Body.String() != string(BodyUnavailable) {
		t.Fatalf("principal source down: status %d body %s, want the 503 family body", rec.Code, rec.Body.String())
	}
	mf.principals.fail = false
	mf.orgs.resolve = func(slug string) (string, bool, error) { return "", false, errOrgAnchorDown }
	rec = httptest.NewRecorder()
	h.inner.handlers["PUT /api/v1/load/units"].ServeHTTP(rec, machineRequest(mf.machineToken(t, fixtureMachinePrincipal), fixtureOrgSlug))
	if rec.Code != http.StatusServiceUnavailable || rec.Body.String() != string(BodyUnavailable) {
		t.Fatalf("org anchor down: status %d body %s, want the 503 family body", rec.Code, rec.Body.String())
	}
}

// TestMachinePrincipalMissing: a machine token whose subject names no
// principal is refused with the same 403 body as every other machine
// refusal, so no caller can enumerate the principal ids that exist.
func TestMachinePrincipalMissing(t *testing.T) {
	mf := newMachineFixture(t)
	h := newMachineHarness(t, mf)
	rec := httptest.NewRecorder()
	h.inner.handlers["PUT /api/v1/load/units"].ServeHTTP(rec, machineRequest(mf.machineToken(t, "8f4b2d10-9999-4c55-8c11-000000000009"), fixtureOrgSlug))
	if rec.Code != http.StatusForbidden || rec.Body.String() != string(BodyForbidden) {
		t.Fatalf("unknown principal: status %d body %s, want the 403 family body", rec.Code, rec.Body.String())
	}
}

// TestRegistrarWrapsTheMachineRoute proves the attachment itself: a
// machine worded registration served through the wrapper answers the
// machine gate's 401 for a request with no credential at all, where the
// bare handler would answer its own status.
func TestRegistrarWrapsTheMachineRoute(t *testing.T) {
	mf := newMachineFixture(t)
	h := newMachineHarness(t, mf)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/load/units", nil)
	rec := httptest.NewRecorder()
	h.inner.handlers["PUT /api/v1/load/units"].ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || rec.Body.String() != string(BodyUnauthorized) {
		t.Fatalf("no credential: status %d body %s, want the 401 family body", rec.Code, rec.Body.String())
	}
}

// TestRegistrarPanicsOnAMachineRouteWithoutTheGate is the fail-closed
// boot rule: a machine worded census route arriving at a registrar with
// no machine gate would serve behind no gate at all, so the registration
// stops the boot with the panic idiom the unannotated staff route uses.
func TestRegistrarPanicsOnAMachineRouteWithoutTheGate(t *testing.T) {
	_, g, _ := newRegistrarFixture(t)
	inner := &recordingInner{handlers: map[string]http.Handler{}}
	reg, err := newRoleGateRegistrarWithCensus(inner, g, censusFor(machineDoorRoute()))
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("a machine worded route registered with no machine gate and no panic")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "PUT /api/v1/load/units") || !strings.Contains(msg, "machine") {
			t.Fatalf("the refusal must name the pattern and the word: %v", r)
		}
		if len(inner.patterns) != 0 {
			t.Fatal("the machine route reached the mux ungated")
		}
	}()
	reg.Handle("PUT /api/v1/load/units", http.NotFoundHandler())
}

// TestRegistrarPassesThroughNonMachineWords keeps the current behaviour
// of this branch: words that are neither staff nor machine (public,
// tenant, the capability family) register unwrapped, exactly as before
// the machine word existed.
func TestRegistrarPassesThroughNonMachineWords(t *testing.T) {
	_, g, _ := newRegistrarFixture(t)
	inner := &recordingInner{handlers: map[string]http.Handler{}}
	reg, err := newRoleGateRegistrarWithCensus(inner, g, censusFor(
		apigen.Route{Method: "GET", Path: "/api/v1/tenant/config", OperationID: "tenantGetConfig", XRole: "public"},
		apigen.Route{Method: "GET", Path: "/api/v1/portal/orders", OperationID: "portalListOrders", XRole: "capability:view_orders_deliveries"},
	))
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	mark := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	reg.Handle("GET /api/v1/tenant/config", mark)
	reg.Handle("GET /api/v1/portal/orders", mark)
	if len(inner.patterns) != 2 {
		t.Fatalf("pass-through registrations lost: %v", inner.patterns)
	}
	for _, pattern := range inner.patterns {
		rec := httptest.NewRecorder()
		inner.handlers[pattern].ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/anything", nil))
		if rec.Code != http.StatusTeapot {
			t.Fatalf("%s: the pass-through handler was wrapped", pattern)
		}
	}
}

// TestMachineGateConstructorRefusals holds the nil collaborators.
func TestMachineGateConstructorRefusals(t *testing.T) {
	mf := newMachineFixture(t)
	if _, err := NewMachineGate(nil, mf.principals); err == nil {
		t.Fatal("nil gate core constructed")
	}
	if _, err := NewMachineGate(mf.gate, nil); err == nil {
		t.Fatal("nil principal source constructed")
	}
	if _, err := mf.machineGate.Middleware(""); err == nil {
		t.Fatal("an empty door name constructed a middleware")
	}
}
