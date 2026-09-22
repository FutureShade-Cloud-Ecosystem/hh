// Package accept holds the HH-3.3 Phase B acceptance sweeps (QA batch B5):
// SW-1 through SW-6 of the sweep inventory (tools/org/outbox/qa/
// b1-sweep-inventory/sweep-inventory.md) executed against the integrated
// lane tree on JWKS-signed test tokens, consuming the fixture kit
// (core/internal/testkit) and the refusal statuses pinned by ADR-0020 with
// its addendum (docs/adr/0020-org-resolution.md). SW-7, the ERP gate suite
// re-run, is a run requirement recorded in the deliverable, not a Go test
// in this package.
//
// Surface: the lane's proof surface per the EM's ruling on inventory
// finding F1. The identity-owned route shims
// (core/internal/identity/shims) are shaped exactly like the contract's
// routes, and this package adds probe routes over the same shipped Guards
// instance for the two guard classes the shim set does not carry:
// requireAdmin (the shim set samples requireStaff and requireManager only)
// and one route per guardable capability, so SW-1c spans all eight
// capability names of the corrected census (B3 review notes item 9: 43
// capability-gated operations across 8 names, one-to-one onto the
// guardable identity.Capability constants; the per-operation routes land
// with the Phase C modules). Every probe handler is a reached-recorder,
// exactly like the shim handlers: no handler touches any table, so a reach
// proves gate passage and nothing more, and the row-level legs the shim
// surface cannot express stay mapped to the ERP suite re-run (SW-7), as
// the inventory's F1 deep-transition note prescribes.
//
// Wiring: every sweep builds a fresh token set (fresh keypairs, fresh JWK
// Set server, fresh membership and resolver doubles per the isolation
// plan), and resolves org, staff authority and portal role through the
// real pgx-backed resolvers over the throwaway database with the statement
// counter attached at pool construction (B3 review notes item 10: the
// pgx form, never only the double form). The membership side stays the
// kit double (D28's injectable source, finding F4).
//
// Database discipline: the sweeps need a throwaway database with the
// migration set and catalog rows applied, named by HH_B5_TEST_DATABASE_URL.
// Absent the variable every sweep skips politely; the B5 run report sets
// it and asserts zero skips (finding F8). The fixture set is built once
// below, verified count-exact after the run, and cleaned up, so the
// database returns to its starting counts. No sweep mutates a fixture row.
package accept

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/shims"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// dbEnvVar names the throwaway sweep database.
const dbEnvVar = "HH_B5_TEST_DATABASE_URL"

// SubNoCaps is the SW-1c no-capability principal and noCapabilityRole the
// role it resolves to. FIXTURES.md's boundary note binds this package: the
// no-capability principal comes from the capability source double, never
// from a portal row, because every canonical portal role (the 0160 CHECK
// set) carries at least one capability, so no row can represent the empty
// set. identity.CapabilitiesFor answers the empty set for a role outside
// the canonical set (C31, no fail-open default).
const (
	SubNoCaps        = "fxportal-no-caps-alpha"
	noCapabilityRole = "fx-no-capability-role"
)

// SubPortalAbsent is the no-row tenant principal: a well-formed tenant
// bearer whose subject resolves no portal_users row in the token's tenant
// (the fixture builder plants no such row; the membership double carries
// its north tenant team, so the org gate passes and the portal read is
// the deciding seam). It is the one boundary no pin rules: the plain
// tenant class admits it today while capability routes answer it the
// RBAC-403 for lacking authority, and the SW-5 leg that records the
// admission names it deliberate-pending-Tech-Lead, so any change to the
// recorded behavior is a ruling, never a silent drift.
const SubPortalAbsent = "fxportal-absent"

// overridePortal is the capability source double over the real resolver:
// every subject delegates to identity.PortalRoleResolver except the named
// overrides, which resolve a fixed role. It exists so a principal holding
// the empty capability set is representable at all. It is test
// scaffolding in a test file, never code under test.
type overridePortal struct {
	inner guards.PortalRoleSource
	mu    sync.Mutex
	roles map[string]string
}

// PortalRole implements guards.PortalRoleSource.
func (p *overridePortal) PortalRole(ctx context.Context, platformUserID, tenantID string) (string, bool, error) {
	p.mu.Lock()
	role, ok := p.roles[platformUserID]
	p.mu.Unlock()
	if ok {
		return role, true, nil
	}
	return p.inner.PortalRole(ctx, platformUserID, tenantID)
}

// setRole pins one subject's resolved role (the no-capability injection).
func (p *overridePortal) setRole(platformUserID, role string) {
	p.mu.Lock()
	p.roles[platformUserID] = role
	p.mu.Unlock()
}

// The pinned refusal bodies. The 401, 403 and 503 families are the
// exported constants of core/internal/platform/authz/middleware.go (lines
// 46 to 48), asserted by reference. The four 400 shape bodies are
// unexported there (lines 50 to 53), so the sweeps pin them here byte for
// byte: a change to any of these strings fails the sweeps deliberately,
// which is what a pin is. The addendum's rule holds: the 400 bodies name
// the header defect without echoing any value.
const (
	bodyMissingOrgHeader    = `{"error":"missing required header X-Org"}`
	bodyRepeatedOrgHeader   = `{"error":"header X-Org must appear exactly once"}`
	bodyMalformedOrgHeader  = `{"error":"header X-Org is not a valid org slug"}`
	bodyOrgHeaderNotAllowed = `{"error":"header X-Org is not accepted on this route"}`
)

// allCapabilities are the eight guardable capability names in one fixed
// order, the guard classes of the 43-operation census.
var allCapabilities = []identity.Capability{
	identity.CapManageUsers,
	identity.CapViewBilling,
	identity.CapPayInvoices,
	identity.CapManagePaymentMethods,
	identity.CapSubmitRFQ,
	identity.CapCreateOrders,
	identity.CapViewOrdersDeliveries,
	identity.CapEditDeliveryInstructions,
}

// fixtureSubjects is every platform subject the fixture set plants, for
// the audit-delta assertions: the middleware writes no audit rows this
// lane, so the sweeps prove the refusal half of the audit rule (nothing is
// ever written for a fixture subject, on refusals or admissions alike at
// shim depth; the write half on real mutating routes stays with the ERP
// suite re-run, SW-7).
var fixtureSubjects = []string{
	testkit.SubInsideSales, testkit.SubSalesManager, testkit.SubGeneralMgr, testkit.SubTwoOrg,
	testkit.SubRevoked, testkit.SubInert, testkit.SubDriver, testkit.SubSuspended,
	testkit.SubPortalAdminAlpha, testkit.SubPortalCrewAlpha, testkit.SubPortalBuyerNoPayAlpha,
	testkit.SubPortalAdminBeta, testkit.SubPortalCrewBeta, testkit.SubPortalBuyerBeta,
	testkit.SubPortalAdminGamma, testkit.SubPortalApGamma, testkit.SubPortalInactiveAlpha,
}

// Route surface, shaped like the contract paths. The lifecycle targets
// name the fixture order whose status matches the transition's
// pre-state for readability; the shim handlers read no path parameter.
var staffLifecycleRoutes = []struct {
	method, target, op string
}{
	{http.MethodGet, "/api/v1/orders/" + testkit.OrderAlphaDraftID, "orderRead"},
	{http.MethodPost, "/api/v1/orders/" + testkit.OrderAlphaDraftID + "/confirm", "orderConfirm"},
	{http.MethodPost, "/api/v1/orders/" + testkit.OrderAlphaConfID + "/picking", "orderMarkPicking"},
	{http.MethodPost, "/api/v1/orders/" + testkit.OrderAlphaPickID + "/ready", "orderMarkReady"},
	{http.MethodPost, "/api/v1/orders/" + testkit.OrderAlphaConfID + "/cancel", "orderCancel"},
}

var tenantShimRoutes = []struct {
	method, target, op string
}{
	{http.MethodGet, "/api/v1/tenant/me", "tenantGetMe"},
	{http.MethodPut, "/api/v1/tenant/me", "tenantUpdateMe"},
	{http.MethodPost, "/api/v1/tenant/token/refresh", "tenantRefreshToken"},
	{http.MethodGet, "/api/v1/tenant/invoices", "tenantListInvoices"},
}

var syncRoutes = []struct {
	method, target, op string
}{
	{http.MethodGet, "/api/v1/sync/orders", "syncPull"},
	{http.MethodPost, "/api/v1/sync/orders", "syncPush"},
}

const (
	publicRouteTarget = "/api/public/v1/quotes/fx-quote-token"
	adminProbeTarget  = "/api/v1/fxprobe/admin"
	staffProbeTarget  = "/api/v1/fxprobe/staff"
	branchProbeTarget = "/api/v1/fxprobe/branch"
)

// capProbeTarget is the probe route of one guardable capability.
func capProbeTarget(cap identity.Capability) string {
	return "/api/v1/tenant/fxcap/" + string(cap)
}

// bench is the shared database half of every sweep: the counting pool (the
// finding-F5 tracer, attached at pool construction so a zero count is a
// proof) over the throwaway database.
type bench struct {
	pool    *pgxpool.Pool
	counter *testkit.StatementCounter
}

var sharedBench *bench

// TestMain builds the fixture set once, runs the sweeps, verifies the
// fixture counts are unchanged (no sweep mutates a fixture row), cleans up
// exactly the fixture rows, and verifies every fixture count returns to
// zero: the isolation plan's count deltas, proven per run.
func TestMain(m *testing.M) {
	dsn := os.Getenv(dbEnvVar)
	if dsn == "" {
		os.Exit(m.Run())
	}
	ctx := context.Background()
	pool, counter, err := testkit.NewCountingPool(ctx, dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "accept: counting pool:", err)
		os.Exit(1)
	}
	db := testkit.PgxPool(pool)
	fail := func(step string, err error) {
		fmt.Fprintf(os.Stderr, "accept: %s: %v\n", step, err)
		pool.Close()
		os.Exit(1)
	}
	if err := testkit.VerifyBaseline(ctx, db); err != nil {
		fail("baseline", err)
	}
	if err := testkit.Build(ctx, db); err != nil {
		fail("fixture build", err)
	}
	counts, err := testkit.FixtureCounts(ctx, db)
	if err != nil {
		fail("fixture counts", err)
	}
	if err := countsEqual(counts, testkit.ExpectedCounts()); err != nil {
		fail("fixture counts after build", err)
	}
	sharedBench = &bench{pool: pool, counter: counter}
	code := m.Run()
	counts, err = testkit.FixtureCounts(ctx, db)
	if err != nil {
		fail("fixture counts after run", err)
	}
	if err := countsEqual(counts, testkit.ExpectedCounts()); err != nil {
		fail("fixture counts drifted during the run", err)
	}
	if err := testkit.Cleanup(ctx, db); err != nil {
		fail("fixture cleanup", err)
	}
	counts, err = testkit.FixtureCounts(ctx, db)
	if err != nil {
		fail("fixture counts after cleanup", err)
	}
	zero := make(map[string]int, len(testkit.ExpectedCounts()))
	for name := range testkit.ExpectedCounts() {
		zero[name] = 0
	}
	if err := countsEqual(counts, zero); err != nil {
		fail("fixture residue after cleanup", err)
	}
	pool.Close()
	os.Exit(code)
}

func countsEqual(got, want map[string]int) error {
	for name, wantN := range want {
		if got[name] != wantN {
			return fmt.Errorf("table %s: %d fixture rows, want %d", name, got[name], wantN)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			return fmt.Errorf("unexpected count for table %s", name)
		}
	}
	return nil
}

// benchFor hands a test the shared bench, skipping politely when the
// database variable is unset.
func benchFor(t *testing.T) *bench {
	t.Helper()
	if sharedBench == nil {
		t.Skip(dbEnvVar + " is not set: the acceptance sweeps need the throwaway database with migrations applied")
	}
	return sharedBench
}

// knobStub is the driver session knob double. No sweep drives the driver
// principal; the stub exists so the Guards wiring is the complete shape.
type knobStub struct{ hours int }

func (k knobStub) DriverSessionHours(context.Context, string) (int, error) { return k.hours, nil }

// branchProbe records what the resolved authority's branch predicates
// answered, so SW-3's carried cross-branch leg asserts live branch breadth
// at the depth the shim surface allows (the request-level cross-branch
// refusal on real order rows stays with the ERP suite re-run, SW-7).
type branchProbe struct {
	reached  int
	grantedA bool
	grantedB bool
}

func (p *branchProbe) handler() guards.StaffHandler {
	return func(w http.ResponseWriter, _ *http.Request, caller guards.StaffCaller) {
		p.reached++
		p.grantedA = caller.CanAccessBranch(testkit.BranchNorthAID)
		p.grantedB = caller.CanAccessBranch(testkit.BranchNorthBID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}
}

// staffProbe adapts a kit recorder to the guards.StaffHandler form: the
// middleware stamped the scope into the request context before the guard
// ran, so the recorder reads the same stamped facts.
func staffProbe(rec *testkit.Recorder) guards.StaffHandler {
	return func(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
		rec.Handler().ServeHTTP(w, r)
	}
}

// tenantProbe adapts a kit recorder to the guards.TenantHandler form.
func tenantProbe(rec *testkit.Recorder) guards.TenantHandler {
	return func(w http.ResponseWriter, r *http.Request, _ authz.TenantScope) {
		rec.Handler().ServeHTTP(w, r)
	}
}

// sweep is one sweep's wiring: a fresh token set over the kit harness
// (fresh keypairs and JWK Set server per sweep, per the isolation plan),
// the membership double pointed at the fixture org facts, and the real
// pgx resolvers over the shared counting pool.
type sweep struct {
	b       *bench
	kit     *testkit.Harness
	mux     *http.ServeMux
	shimRec *shims.Recorder
	probes  map[string]*testkit.Recorder
	branch  *branchProbe
	portal  *overridePortal
}

func newSweep(t *testing.T) *sweep {
	t.Helper()
	b := benchFor(t)
	h := testkit.NewHarness(t)
	h.WireFixtureOrgs(t)
	orgs, err := authz.NewPgxOrgResolver(b.pool)
	if err != nil {
		t.Fatalf("accept: org resolver: %v", err)
	}
	gate, err := authz.NewGate(h.Verifier, h.Membership, orgs)
	if err != nil {
		t.Fatalf("accept: gate: %v", err)
	}
	staff, err := identity.NewStaffAuthorityResolver(b.pool)
	if err != nil {
		t.Fatalf("accept: staff authority resolver: %v", err)
	}
	portalReal, err := identity.NewPortalRoleResolver(b.pool)
	if err != nil {
		t.Fatalf("accept: portal role resolver: %v", err)
	}
	portal := &overridePortal{inner: portalReal, roles: map[string]string{}}
	g, err := guards.NewGuards(gate, staff, portal, knobStub{hours: 12})
	if err != nil {
		t.Fatalf("accept: guards: %v", err)
	}
	s := &sweep{
		b:       b,
		kit:     h,
		mux:     http.NewServeMux(),
		shimRec: shims.NewRecorder(512),
		probes:  map[string]*testkit.Recorder{},
		branch:  &branchProbe{},
		portal:  portal,
	}
	shims.RegisterRoutes(s.mux, g, s.shimRec)
	s.probes["admin"] = testkit.NewRecorder()
	s.mux.Handle(adminProbeTarget, g.RequireAdmin(staffProbe(s.probes["admin"])))
	s.probes["staff"] = testkit.NewRecorder()
	s.mux.Handle(staffProbeTarget, g.RequireStaff(staffProbe(s.probes["staff"])))
	s.mux.Handle(branchProbeTarget, g.RequireStaff(s.branch.handler()))
	for _, cap := range allCapabilities {
		rec := testkit.NewRecorder()
		s.probes["cap:"+string(cap)] = rec
		s.mux.Handle(capProbeTarget(cap), g.RequireCapability(cap, tenantProbe(rec)))
	}
	return s
}

// guardedSurface enumerates every non-public route registered on the
// sweep's mux: the five lifecycle routes, the tenant set, the sync pair,
// and the probe routes. The anonymous sweeps drive the whole surface per
// variant.
func (s *sweep) guardedSurface() []struct {
	method, target string
} {
	var routes []struct {
		method, target string
	}
	for _, r := range staffLifecycleRoutes {
		routes = append(routes, struct{ method, target string }{r.method, r.target})
	}
	for _, r := range tenantShimRoutes {
		routes = append(routes, struct{ method, target string }{r.method, r.target})
	}
	for _, r := range syncRoutes {
		routes = append(routes, struct{ method, target string }{r.method, r.target})
	}
	routes = append(routes,
		struct{ method, target string }{http.MethodGet, adminProbeTarget},
		struct{ method, target string }{http.MethodGet, staffProbeTarget},
		struct{ method, target string }{http.MethodGet, branchProbeTarget},
	)
	for _, cap := range allCapabilities {
		routes = append(routes, struct{ method, target string }{http.MethodGet, capProbeTarget(cap)})
	}
	return routes
}

// do drives one request at the sweep's mux.
func (s *sweep) do(t *testing.T, method, target, bearer string, xOrg ...string) *httptest.ResponseRecorder {
	t.Helper()
	return testkit.Do(t, s.mux, method, target, bearer, xOrg...)
}

func (s *sweep) staffToken(t *testing.T, sub string) string {
	t.Helper()
	return s.kit.Minter.StaffToken(t, sub)
}

func (s *sweep) tenantToken(t *testing.T, sub, tenantID string) string {
	t.Helper()
	return s.kit.Minter.TenantToken(t, sub, tenantID)
}

// shimOpCount counts recorder entries for one operation.
func (s *sweep) shimOpCount(op string) int {
	n := 0
	for _, e := range s.shimRec.Entries() {
		if e.Operation == op {
			n++
		}
	}
	return n
}

// lastShimOp returns the most recent recorder entry for one operation.
func (s *sweep) lastShimOp(t *testing.T, leg, op string) shims.Reached {
	t.Helper()
	entries := s.shimRec.Entries()
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Operation == op {
			return entries[i]
		}
	}
	t.Fatalf("accept: leg %s: no shim entry for operation %s", leg, op)
	return shims.Reached{}
}

// resetProbes clears every probe recorder ahead of a leg.
func (s *sweep) resetProbes() {
	for _, rec := range s.probes {
		rec.Reset()
	}
	s.branch.reached = 0
}

// assertNoReachAnywhere fails when any handler on the surface was reached:
// every probe recorder empty, the branch probe untouched, and the shim
// recorder unchanged since the snapshot.
func (s *sweep) assertNoReachAnywhere(t *testing.T, leg string, shimSnapshot int) {
	t.Helper()
	if got := len(s.shimRec.Entries()); got != shimSnapshot {
		t.Fatalf("accept: leg %s: shim recorder gained %d entries on a refusal leg", leg, got-shimSnapshot)
	}
	for name, rec := range s.probes {
		if n := rec.Count(); n != 0 {
			t.Fatalf("accept: leg %s: probe %s reached %d times on a refusal leg", leg, name, n)
		}
	}
	if s.branch.reached != 0 {
		t.Fatalf("accept: leg %s: branch probe reached %d times on a refusal leg", leg, s.branch.reached)
	}
}

// auditRowsForFixtureSubjects counts audit_log rows stamped with any
// fixture subject. The lane's middleware and shims write no audit rows;
// the sweeps assert the count stays zero (the refusal half of the audit
// rule; the write half is SW-7's ERP suite).
func (s *sweep) auditRowsForFixtureSubjects(t *testing.T) int {
	t.Helper()
	var n int
	if err := s.b.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE acting_platform_user_id = ANY($1)`,
		fixtureSubjects).Scan(&n); err != nil {
		t.Fatalf("accept: audit count: %v", err)
	}
	return n
}

// orderStatusUnchanged reads the fixture order's status twice around one
// refused call and fails when the refusal moved the row. The value is the
// sweep's own planted fixture constant; it is compared, never reported.
func (s *sweep) orderStatusUnchanged(t *testing.T, leg, orderID string, drive func()) {
	t.Helper()
	read := func() string {
		var status string
		if err := s.b.pool.QueryRow(context.Background(),
			`SELECT status FROM orders WHERE id = $1`, orderID).Scan(&status); err != nil {
			t.Fatalf("accept: leg %s: order status read: %v", leg, err)
		}
		return status
	}
	before := read()
	drive()
	after := read()
	if before != after {
		t.Fatalf("accept: leg %s: the refused call moved the order row", leg)
	}
}

// mintWindowedStaff mints a staff token with an explicit self-declared
// window, for the ceiling legs. The stamped window is asserted through the
// probe reach, never re-parsed.
func (s *sweep) mintWindowedStaff(t *testing.T, sub string, iat, exp time.Time) string {
	t.Helper()
	return s.kit.Minter.Mint(t, testkit.StaffClass, sub, testkit.TokenShape{IssuedAt: &iat, ExpiresAt: &exp})
}

// assertStampedWindow fails unless the staff probe's last reach carries
// exactly the minted window (seconds precision, the JWT NumericDate form).
func (s *sweep) assertStampedWindow(t *testing.T, leg string, iat, exp time.Time) {
	t.Helper()
	reach := s.probes["staff"].Last(t)
	if want := time.Unix(iat.Unix(), 0); !reach.IssuedAt.Equal(want) {
		t.Fatalf("accept: leg %s: handler saw issued-at %v, want %v", leg, reach.IssuedAt, want)
	}
	if want := time.Unix(exp.Unix(), 0); !reach.ExpiresAt.Equal(want) {
		t.Fatalf("accept: leg %s: handler saw expiry %v, want %v", leg, reach.ExpiresAt, want)
	}
}
