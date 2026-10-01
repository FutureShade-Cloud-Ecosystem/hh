package main

// The x-role equality assertions (B-assert, security-critical), migrated
// at the Phase C integration: for every route the core registers, the
// gate the handler actually runs must equal the x-role the document
// declares for that operation, and the bearer class the gate actually
// requires must equal the document's security scheme. The fixture is the
// fold's registerRoutes(r, getenv) itself, driven over the identity
// environment map and the gate database (compfixture_test.go), so the
// assertion surface is the FULL served surface and every guard resolves
// through the real collaborators: JWKS verification against the kit's
// served key set, Team membership against the Appwrite double, org
// anchoring, staff authority and portal roles against the fixture
// database's synthetic rows. The single source of both document facts is
// the generated census table (core/internal/platform/apigen/
// routes.gen.go, Route.XRole and Route.Bearer, regenerated from
// core/api/openapi.yaml), and the binding from operationId to
// registration path is the parity test's own machinery: registerRoutes
// over a recordingRegistrar, patterns matched against the census. No
// route is named by a literal here except the customer-quote public pair,
// whose classification cells the C5 recipe orders into the table by name.
// A new registration is asserted the moment it lands; a removed one stops
// being asserted; a census edit or a guard swap without the other fails
// below.
//
// The gate class is observed, not asked: each constructor family
// (core/internal/identity/guards/guards.go) refuses and admits on a fixed
// skeleton of probe legs, with statuses and bodies pinned by ADR-0020
// (docs/adr/0020-org-resolution.md, enforcement semantics and refusal
// statuses) and bodies that are the authz constants
// (core/internal/platform/authz/middleware.go). The probe skeleton was
// widened at this integration per the B4 review obligation: the portal
// registers multiple capability routes now, and over the carried role
// lattice the two-role skeleton left the eight capability names in two
// behavioral classes; the buyer, buyer_no_pay and ap_finance legs (their
// rows derived from identity.CapabilitiesFor, the same model the guard
// reads) split view_billing from the pay pair and view_orders_deliveries
// from edit_delivery_instructions. The residual pairs no skeleton can
// split (pay_invoices versus manage_payment_methods, submit_rfq versus
// create_orders: no canonical role holds one and not the other,
// core/internal/identity/portal.go) are behavioral equivalence under the
// carried lattice, recorded here so nobody mistakes the limit for a gap.
//
// The admitted legs observe the handler's own answer (the C5 recipe's
// admitted-body awareness): the served set is the full module family, the
// admitted probes reach real handlers, and the assertion is that the GATE
// PASSED, observed as the answer wearing none of the three authz family
// refusal bodies (the gate's refusals are byte-pinned constants; a
// handler's own 403 wears a different body). The customer-quote send's
// staff legs therefore reach StaffSend over the gate database, and the
// public pair's cells pin the module's own classification (404 absent,
// 410 dead, 200 live) exactly as the C5 review specified.

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customerquote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// xrOrgSlug is the probe org (the fixture's north anchor) and xrXOrg the
// staff legs' org selection header value.
const xrXOrg = testkit.OrgNorthSlug

// xrProbeDeadline bounds every probe. Every handler answers in
// milliseconds except the one SSE stream route, whose admitted legs hold
// the connection until the request context ends; the deadline ends it, and
// the recorded answer (the stream's ready event) is the handler's own.
const xrProbeDeadline = 1500 * time.Millisecond

// xrProbeBody is the JSON body every mutating probe carries: the public
// accept pair's typed-name-and-affirmative shape, which both acceptance
// surfaces (the dealer pair's and the customer-quote pair's) read. A
// guarded write route's own body validation answers what it answers; the
// gate layer this suite asserts never reads a body.
var xrProbeBody = []byte(`{"typed_name":"Fixture Homeowner","accepted":true}`)

// xrUUIDParam substitutes a well-formed, never-planted uuid for every {id}
// parameter: a well-formed absent resource answers the module's not-found
// family, never a parse refusal, so the admitted legs observe the
// handler's answer rather than its input validation.
const xrUUIDParam = "3ef10000-0000-4000-8000-0f0000000001"

// xrFixture is the registered surface under probe: the same registerRoutes
// the binary serves, over the identity environment map and the gate
// database, with the customer-quote classification tokens planted through
// the module's own seams.
type xrFixture struct {
	env        *compEnv
	reg        *recordingRegistrar
	registered []string
	tokens     *cqClassificationTokens
}

func newXRoleFixture(t *testing.T) *xrFixture {
	t.Helper()
	env := newCompEnv(t)
	reg, graph := registeredSurface(t, env)
	tokens := plantClassificationTokens(t, env, graph.pool)
	// The matrix legs' principals: the planted staff roles whose cells the
	// three tier legs' roles do not reach (plan item RG).
	plantMatrixStaff(t, env, graph.pool)
	return &xrFixture{env: env, reg: reg, registered: reg.patterns, tokens: tokens}
}

// xrLeg is one probe: it mints the leg's credential through the kit and
// returns the complete request headers, Authorization included. The staff
// and portal subjects are the fixture database's synthetic principals, so
// the guards resolve real authority rows: the three staff tiers, and the
// five canonical contractor roles across their tenants.
type xrLeg struct {
	name  string
	apply func(t *testing.T, fx *xrFixture) map[string]string
}

// xrLegs is the fixed probe skeleton every family is asserted against. The
// anonymous leg is first because the public family compares every
// admitting body against the anonymous body. The staff legs beyond the
// three tier legs exist because the role gate holds each role to its own
// row of the dealer's configuration (D48, plan item RG): the financial
// controller, the branch manager, the procurement manager and the yard
// team are the planted callers whose cells reach the routes no tier leg
// reaches (the money desk's admin cells, the dispatch board's work cells,
// buying and stock's manage cells, the yard pick surface), so the bearer
// class assertion has an admitted staff caller on every staff route and
// the family assertions observe the matrix refusing real roles.
var xrLegs = []xrLeg{
	{"anonymous", func(*testing.T, *xrFixture) map[string]string { return nil }},
	{"malformed bearer", func(*testing.T, *xrFixture) map[string]string {
		return map[string]string{"Authorization": "Bearer not-a-token"}
	}},
	{"staff bearer without X-Org", func(t *testing.T, fx *xrFixture) map[string]string {
		return map[string]string{"Authorization": "Bearer " + fx.env.minter.StaffToken(t, testkit.SubInsideSales)}
	}},
	{"staff tier 3 branch", func(t *testing.T, fx *xrFixture) map[string]string {
		return map[string]string{
			"Authorization":  "Bearer " + fx.env.minter.StaffToken(t, testkit.SubInsideSales),
			authz.HeaderXOrg: xrXOrg,
		}
	}},
	{"staff tier 2 branch", func(t *testing.T, fx *xrFixture) map[string]string {
		return map[string]string{
			"Authorization":  "Bearer " + fx.env.minter.StaffToken(t, testkit.SubSalesManager),
			authz.HeaderXOrg: xrXOrg,
		}
	}},
	{"staff tier 1 global", func(t *testing.T, fx *xrFixture) map[string]string {
		return map[string]string{
			"Authorization":  "Bearer " + fx.env.minter.StaffToken(t, testkit.SubGeneralMgr),
			authz.HeaderXOrg: xrXOrg,
		}
	}},
	{"staff financial controller global", func(t *testing.T, fx *xrFixture) map[string]string {
		return map[string]string{
			"Authorization":  "Bearer " + fx.env.minter.StaffToken(t, xrSubFinancialController),
			authz.HeaderXOrg: xrXOrg,
		}
	}},
	{"staff branch manager branch", func(t *testing.T, fx *xrFixture) map[string]string {
		return map[string]string{
			"Authorization":  "Bearer " + fx.env.minter.StaffToken(t, xrSubBranchManager),
			authz.HeaderXOrg: xrXOrg,
		}
	}},
	{"staff procurement manager branch", func(t *testing.T, fx *xrFixture) map[string]string {
		return map[string]string{
			"Authorization":  "Bearer " + fx.env.minter.StaffToken(t, xrSubProcurementManager),
			authz.HeaderXOrg: xrXOrg,
		}
	}},
	{"staff yard team branch", func(t *testing.T, fx *xrFixture) map[string]string {
		return map[string]string{
			"Authorization":  "Bearer " + fx.env.minter.StaffToken(t, xrSubYardTeam),
			authz.HeaderXOrg: xrXOrg,
		}
	}},
	{"tenant account admin", func(t *testing.T, fx *xrFixture) map[string]string {
		return map[string]string{"Authorization": "Bearer " + fx.env.minter.TenantToken(t, testkit.SubPortalAdminAlpha, testkit.TenantAlphaID)}
	}},
	{"tenant field crew", func(t *testing.T, fx *xrFixture) map[string]string {
		return map[string]string{"Authorization": "Bearer " + fx.env.minter.TenantToken(t, testkit.SubPortalCrewAlpha, testkit.TenantAlphaID)}
	}},
	{"tenant buyer", func(t *testing.T, fx *xrFixture) map[string]string {
		return map[string]string{"Authorization": "Bearer " + fx.env.minter.TenantToken(t, testkit.SubPortalBuyerBeta, testkit.TenantBetaID)}
	}},
	{"tenant buyer no pay", func(t *testing.T, fx *xrFixture) map[string]string {
		return map[string]string{"Authorization": "Bearer " + fx.env.minter.TenantToken(t, testkit.SubPortalBuyerNoPayAlpha, testkit.TenantAlphaID)}
	}},
	{"tenant ap finance", func(t *testing.T, fx *xrFixture) map[string]string {
		return map[string]string{"Authorization": "Bearer " + fx.env.minter.TenantToken(t, testkit.SubPortalApGamma, testkit.TenantGammaID)}
	}},
	{"tenant no portal row", func(t *testing.T, fx *xrFixture) map[string]string {
		return map[string]string{"Authorization": "Bearer " + fx.env.minter.TenantToken(t, xrNoPortalRowSub, testkit.TenantAlphaID)}
	}},
	{"tenant bearer carrying X-Org", func(t *testing.T, fx *xrFixture) map[string]string {
		return map[string]string{
			"Authorization":  "Bearer " + fx.env.minter.TenantToken(t, testkit.SubPortalAdminAlpha, testkit.TenantAlphaID),
			authz.HeaderXOrg: xrXOrg,
		}
	}},
}

// xrLegByName finds one probe leg by name; the bearer test's legs are a
// subset of the skeleton and name them by constant string only.
func xrLegByName(t *testing.T, name string) xrLeg {
	t.Helper()
	for _, l := range xrLegs {
		if l.name == name {
			return l
		}
	}
	t.Fatalf("no probe leg named %q", name)
	return xrLeg{}
}

// xrWant is the pinned expectation for one leg under one family. Exactly
// one of the five shapes of pin holds: status alone with a nil body (a
// gate refusal whose status the table names); body non-nil (a
// byte-identical family refusal); shape (a 400 X-Org shape refusal: the
// 400 bodies are package-private in authz by the landed B2 review, so the
// pin is the status plus two properties from the ADR-0020 addendum, never
// a family body and never an echo of a sent value); anonBody (admitted
// with the byte-identical anonymous answer, the public leg's zero-effect
// rule, the status observed rather than pinned because the served public
// routes answer their own families: the config leg 200, the tracking
// pixels 200 and 204, the intake's content-type refusal, the token
// classifiers 404); handler (admitted: the gate passed, observed as the
// answer wearing none of the three authz family refusal bodies, the
// handler's own answer being each module's suite to pin).
type xrWant struct {
	status   int
	body     []byte
	shape    bool
	anonBody bool
	handler  bool
}

func xrAdmitted() xrWant { return xrWant{handler: true} }

func xrAnonIdentical() xrWant { return xrWant{anonBody: true} }

func xrRefuse(status int, body []byte) xrWant { return xrWant{status: status, body: body} }

func xrShapeRefused() xrWant { return xrWant{status: http.StatusBadRequest, shape: true} }

// The staff legs, their planted roles and their tiers. The role gate
// holds each role to its own row of the dealer's configuration, so the
// staff legs' expectations are computed from the matrix, never pinned:
// a cell edit in the decided default moves the expectations here and the
// composition test follows it (the tests were brought to the matrix,
// plan item RG's third clause).
var xrStaffLegs = []struct {
	legName string
	role    string
	tier    int
}{
	{"staff tier 3 branch", "inside_sales", identity.TierFrontline},
	{"staff tier 2 branch", "sales_manager", identity.TierManager},
	{"staff tier 1 global", "general_manager", identity.TierLeadership},
	{"staff financial controller global", "financial_controller", identity.TierLeadership},
	{"staff branch manager branch", "branch_manager", identity.TierManager},
	{"staff procurement manager branch", "procurement_manager", identity.TierManager},
	{"staff yard team branch", "yard_team", identity.TierFrontline},
}

// xrMatrixAdmits answers whether the role's row of the configuration
// reaches the route's annotated level on at least one surface the route
// serves, the role gate's admission rule.
func xrMatrixAdmits(t *testing.T, cfg *roleconfig.Config, route apigen.Route, role string) bool {
	t.Helper()
	for _, surface := range route.Surfaces {
		ok, err := cfg.Holds(role, surface, roleconfig.Level(route.Level))
		if err != nil {
			t.Fatalf("matrix read for %s on %s: %v", role, surface, err)
		}
		if ok {
			return true
		}
	}
	return false
}

// xrFamilyFor maps the document's x-role spelling onto the expectation
// table of one guard constructor family. The second return names the
// family for failure messages; ok false means the spelling names no family
// this suite can assert. The staff legs of the three tier families are
// the tier gate's answer AND the role gate's answer: a leg is admitted
// only when its tier passes the x-role and its row of the dealer's
// configuration reaches the route's annotated level on a served surface.
func xrFamilyFor(t *testing.T, cfg *roleconfig.Config, route apigen.Route) (map[string]xrWant, string, bool) {
	shape := xrShapeRefused()
	u := xrRefuse(http.StatusUnauthorized, authz.BodyUnauthorized)
	f := xrRefuse(http.StatusForbidden, authz.BodyForbidden)
	staffWant := func(minTier int) map[string]xrWant {
		wants := make(map[string]xrWant, len(xrStaffLegs))
		for _, leg := range xrStaffLegs {
			if leg.tier <= minTier && xrMatrixAdmits(t, cfg, route, leg.role) {
				wants[leg.legName] = xrAdmitted()
			} else {
				wants[leg.legName] = f
			}
		}
		return wants
	}
	switch {
	case xrole(route) == "public":
		// The unguarded public leg (ADR-0020 leg 2): no credential, no
		// header, no bearer class has any effect on the answer, so every
		// leg must return the anonymous answer byte for byte.
		wants := make(map[string]xrWant, len(xrLegs))
		for _, leg := range xrLegs {
			wants[leg.name] = xrAnonIdentical()
		}
		return wants, "Public (unguarded)", true
	case xrole(route) == "requireStaff":
		wants := map[string]xrWant{
			"anonymous": u, "malformed bearer": u,
			"staff bearer without X-Org": shape,
			"tenant account admin":       f, "tenant field crew": f, "tenant buyer": f, "tenant buyer no pay": f,
			"tenant ap finance": f, "tenant no portal row": f, "tenant bearer carrying X-Org": f,
		}
		for name, want := range staffWant(identity.TierFrontline) {
			wants[name] = want
		}
		return wants, "RequireStaff (any active grant) and the role gate", true
	case xrole(route) == "requireManager":
		wants := map[string]xrWant{
			"anonymous": u, "malformed bearer": u,
			"staff bearer without X-Org": shape,
			"tenant account admin":       f, "tenant field crew": f, "tenant buyer": f, "tenant buyer no pay": f,
			"tenant ap finance": f, "tenant no portal row": f, "tenant bearer carrying X-Org": f,
		}
		for name, want := range staffWant(identity.TierManager) {
			wants[name] = want
		}
		return wants, "RequireManager (tier 1 or 2) and the role gate", true
	case xrole(route) == "requireAdmin":
		wants := map[string]xrWant{
			"anonymous": u, "malformed bearer": u,
			"staff bearer without X-Org": shape,
			"tenant account admin":       f, "tenant field crew": f, "tenant buyer": f, "tenant buyer no pay": f,
			"tenant ap finance": f, "tenant no portal row": f, "tenant bearer carrying X-Org": f,
		}
		for name, want := range staffWant(identity.TierLeadership) {
			wants[name] = want
		}
		return wants, "RequireAdmin (tier 1 global) and the role gate", true
	case xrole(route) == "tenant":
		wants := map[string]xrWant{
			"anonymous": u, "malformed bearer": u,
			"staff bearer without X-Org": f, "staff tier 3 branch": f, "staff tier 2 branch": f, "staff tier 1 global": f,
			"staff financial controller global": f, "staff branch manager branch": f,
			"staff procurement manager branch": f, "staff yard team branch": f,
			// No capability is consulted: a role that holds nothing is still
			// admitted, and the no-row boundary is the unpinned leg guards.go
			// records as admitted.
			"tenant account admin": xrAdmitted(), "tenant field crew": xrAdmitted(), "tenant buyer": xrAdmitted(),
			"tenant buyer no pay": xrAdmitted(), "tenant ap finance": xrAdmitted(), "tenant no portal row": xrAdmitted(),
			"tenant bearer carrying X-Org": shape,
		}
		return wants, "RequireTenant (tenant bearer, portal row status)", true
	case xrole(route) == "scope-member":
		wants := map[string]xrWant{
			"anonymous": u, "malformed bearer": u,
			"staff bearer without X-Org": shape,
			// The sync surface carries no tier gate and no role gate: any
			// staff bearer with an org selection passes the staff leg.
			"staff tier 3 branch": xrAdmitted(), "staff tier 2 branch": xrAdmitted(), "staff tier 1 global": xrAdmitted(),
			"staff financial controller global": xrAdmitted(), "staff branch manager branch": xrAdmitted(),
			"staff procurement manager branch": xrAdmitted(), "staff yard team branch": xrAdmitted(),
			"tenant account admin": xrAdmitted(), "tenant field crew": xrAdmitted(), "tenant buyer": xrAdmitted(),
			"tenant buyer no pay": xrAdmitted(), "tenant ap finance": xrAdmitted(), "tenant no portal row": xrAdmitted(),
			"tenant bearer carrying X-Org": shape,
		}
		return wants, "RequireSync (either bearer, G4 bearer-conditioned)", true
	case strings.HasPrefix(xrole(route), "capability:"):
		// The capability family derives its tenant-leg expectations from the
		// same carried role-to-capability model the guard reads
		// (identity.CapabilitiesFor, pinned both-halves per capability in
		// guards_test.go), so the table stays correct for every capability
		// name without literals: a role that holds the named capability is
		// admitted, one that lacks it answers the RBAC-403, and no linked
		// portal user is no authority and answers the RBAC-403. The five
		// canonical roles are probed (the B4 widening), which splits
		// view_billing from the pay pair and view_orders_deliveries from
		// edit_delivery_instructions; the residual equivalence pairs
		// (pay_invoices versus manage_payment_methods, submit_rfq versus
		// create_orders) no canonical role splits under the carried lattice.
		capName := identity.Capability(strings.TrimPrefix(xrole(route), "capability:"))
		wants := map[string]xrWant{
			"anonymous": u, "malformed bearer": u,
			"staff bearer without X-Org": f, "staff tier 3 branch": f, "staff tier 2 branch": f, "staff tier 1 global": f,
			"staff financial controller global": f, "staff branch manager branch": f,
			"staff procurement manager branch": f, "staff yard team branch": f,
			"tenant no portal row":         f,
			"tenant bearer carrying X-Org": shape,
		}
		for role, legName := range map[string]string{
			identity.PortalRoleAccountAdmin: "tenant account admin",
			identity.PortalRoleFieldCrew:    "tenant field crew",
			identity.PortalRoleBuyer:        "tenant buyer",
			identity.PortalRoleBuyerNoPay:   "tenant buyer no pay",
			identity.PortalRoleAPFinance:    "tenant ap finance",
		} {
			if identity.CapabilitiesFor(role).Has(capName) {
				wants[legName] = xrAdmitted()
			} else {
				wants[legName] = f
			}
		}
		return wants, "RequireCapability (" + xrole(route) + ")", true
	}
	return nil, "", false
}

// xrole reads the census x-role spelling of one route.
func xrole(route apigen.Route) string { return route.XRole }

// xrCensusByPattern indexes the generated census by method-prefixed
// pattern, the same index the parity test asserts both directions against,
// so operationId, XRole and Bearer arrive through that machinery and never
// from a literal in this file.
func xrCensusByPattern(t *testing.T) map[string]apigen.Route {
	t.Helper()
	byPattern := make(map[string]apigen.Route, len(apigen.Routes))
	for _, route := range apigen.Routes {
		pattern := route.Method + " " + route.Path
		if _, dup := byPattern[pattern]; dup {
			t.Fatalf("document has two operations for %s", pattern)
		}
		byPattern[pattern] = route
	}
	return byPattern
}

// xrPathParam substitutes a fixed non-empty segment for every path
// parameter in a documented template, so the probe request matches the
// registered pattern without naming any resource.
var xrPathParam = regexp.MustCompile(`\{[^/}]+\}`)

// xrProbePath substitutes the probe segments for one documented template:
// the uuid-shaped absent id for {id}, the absent token for {token} (the
// classification cells below substitute the planted tokens), and a fixed
// non-empty segment for every other parameter name.
func xrProbePath(path string) string {
	return xrPathParam.ReplaceAllStringFunc(path, func(param string) string {
		switch param {
		case "{id}":
			return xrUUIDParam
		case "{token}":
			return xrAbsentToken
		default:
			return "probe"
		}
	})
}

// xrTokenPath substitutes one raw token for the {token} parameter of a
// documented template (the classification cells' probe paths).
func xrTokenPath(path, raw string) string {
	return xrPathParam.ReplaceAllStringFunc(path, func(param string) string {
		if param == "{token}" {
			return raw
		}
		return xrProbePath("x/{" + strings.Trim(param, "{}") + "}")
	})
}

// probe issues one request through the registered surface. Every mutating
// probe carries the JSON body and every probe carries a bounded context
// (the SSE stream route's admitted legs hold until it ends).
func (fx *xrFixture) probe(t *testing.T, method, path string, header map[string]string) (int, string) {
	t.Helper()
	var body *bytes.Reader
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodDelete {
		body = bytes.NewReader(xrProbeBody)
	} else {
		body = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, body)
	ctx, cancel := context.WithTimeout(context.Background(), xrProbeDeadline)
	defer cancel()
	req = req.WithContext(ctx)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rsp := httptest.NewRecorder()
	fx.reg.inner.ServeHTTP(rsp, req)
	return rsp.Code, rsp.Body.String()
}

// probeLeg runs one probe leg against one census route.
func (fx *xrFixture) probeLeg(t *testing.T, route apigen.Route, leg xrLeg) (int, string) {
	t.Helper()
	return fx.probe(t, route.Method, xrProbePath(route.Path), leg.apply(t, fx))
}

// TestRegisteredRouteGateMatchesDocumentXRole is the x-role equality
// assertion: for every registered route, the gate the handler runs is
// observed on the fixed probe skeleton and must match the expectation
// table of the family the document's x-role names, with the staff legs'
// expectations computed from the dealer's configuration (plan item RG:
// the tests are brought to the matrix, never the matrix to the tests). A
// document x-role edit without the gate, a gate swap without the
// document, or a matrix cell edit the gate does not follow, fails here.
func TestRegisteredRouteGateMatchesDocumentXRole(t *testing.T) {
	fx := newXRoleFixture(t)
	census := xrCensusByPattern(t)
	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatalf("roleconfig.Default: %v", err)
	}
	if len(fx.registered) == 0 {
		t.Fatal("registerRoutes registered nothing; the assertion surface is empty")
	}
	for _, pattern := range fx.registered {
		route, ok := census[pattern]
		if !ok {
			t.Fatalf("registered route %s is not in the document; the parity test owns this direction", pattern)
		}
		t.Run(route.OperationID, func(t *testing.T) {
			if route.XRole == "operator" {
				t.Fatalf("x-role operator (documented for %s) names a class no guard constructor implements: RequireStaffPredicate composes it once its lane records what the class means, and asserting a family the guard layer has not decided would invent policy", route.OperationID)
			}
			wants, family, ok := xrFamilyFor(t, cfg, route)
			if !ok {
				t.Fatalf("document x-role %q names no gate family this suite knows", route.XRole)
			}
			anonCode, anonBody := fx.probeLeg(t, route, xrLegs[0])
			for _, leg := range xrLegs {
				want, ok := wants[leg.name]
				if !ok {
					t.Fatalf("leg %q has no expectation under x-role %q; the table must be total over the probe skeleton", leg.name, route.XRole)
				}
				code, body := fx.probeLeg(t, route, leg)
				if want.status != 0 && code != want.status {
					t.Fatalf("leg %q: status %d, want %d under document x-role %q (%s); the registered gate does not equal the document",
						leg.name, code, want.status, route.XRole, family)
				}
				if want.body != nil && body != string(want.body) {
					t.Fatalf("leg %q: refusal body %q, want the pinned family body %q", leg.name, body, want.body)
				}
				if want.shape {
					for _, familyBody := range [][]byte{authz.BodyUnauthorized, authz.BodyForbidden, authz.BodyUnavailable} {
						if body == string(familyBody) {
							t.Fatalf("leg %q: the 400 shape refusal wears a family body %q", leg.name, body)
						}
					}
					if strings.Contains(body, xrXOrg) || strings.Contains(body, testkit.TenantAlphaID) {
						t.Fatalf("leg %q: the 400 shape refusal echoes a sent value: %q", leg.name, body)
					}
				}
				if want.handler {
					for _, familyBody := range [][]byte{authz.BodyUnauthorized, authz.BodyForbidden, authz.BodyUnavailable} {
						if body == string(familyBody) {
							t.Fatalf("leg %q: admitted under document x-role %q (%s) but the answer wears the gate's refusal body %q: the registered gate does not equal the document",
								leg.name, route.XRole, family, body)
						}
					}
				}
				if want.anonBody && (code != anonCode || body != anonBody) {
					t.Fatalf("leg %q: changed the public answer (ADR-0020 leg 2 zero effect): status %d body %q, want the anonymous answer %d %q",
						leg.name, code, body, anonCode, anonBody)
				}
			}
			// The customer-quote public pair's classification cells (the C5
			// recipe): 404 absent, 410 dead, 200 live, the module's own
			// token classification, asserted once per route beside the
			// leg-identity loop.
			if strings.HasPrefix(route.OperationID, "customerQuotePublic") {
				fx.assertClassificationCells(t, route)
			}
		})
	}
	t.Logf("x-role equality asserted for %d registered operations against the census families", len(fx.registered))
}

// assertClassificationCells pins the customer-quote public pair's own
// classification (ADR 0022 decision 1): a well-formed never-minted token
// answers 404 with the never-existed sentence, a retired token answers 410
// with the dead-link sentence, and a live token serves the operation (the
// view 200; the accept 200 once, on its own fresh quote, because the
// acceptance flips the quote's state). The leg-identity loop above proved
// no credential changes any of these answers.
func (fx *xrFixture) assertClassificationCells(t *testing.T, route apigen.Route) {
	t.Helper()
	wantAbsent := `{"error":` + strconv.Quote(customerquote.MsgLinkNotExist) + `}`
	wantDead := `{"error":` + strconv.Quote(customerquote.SentenceLinkNoLongerValid) + `}`
	cell := func(name, raw string, wantStatus int, wantBody string) {
		t.Helper()
		code, body := fx.probe(t, route.Method, xrTokenPath(route.Path, raw), nil)
		if code != wantStatus {
			t.Fatalf("%s classification cell %s: status %d body %s, want %d", route.OperationID, name, code, body, wantStatus)
		}
		if wantBody != "" && strings.TrimSpace(body) != wantBody {
			t.Fatalf("%s classification cell %s: body %q, want %q", route.OperationID, name, body, wantBody)
		}
	}
	cell("absent token", fx.tokens.absent, http.StatusNotFound, wantAbsent)
	cell("dead token", fx.tokens.dead, http.StatusGone, wantDead)
	if route.Method == http.MethodGet {
		cell("live token", fx.tokens.liveView, http.StatusOK, "")
		return
	}
	cell("live token", fx.tokens.liveAccept, http.StatusOK, "")
}

// TestRegisteredRouteBearerMatchesCensus asserts the census Bearer column
// equals the bearer class the registered gate actually requires, observed
// as which credential class the gate admits and which it refuses with the
// RBAC-403. A shim registered under the wrong bearer family, or a census
// Bearer edit without the gate, fails here even when the x-role family
// assertion alone would pass; the sync pair is the class where the two are
// independent (Tech Lead B3 review note). The staff probe is the first
// planted staff caller the dealer's configuration admits for that route
// (the tier legs alone do not cover the routes whose cells sit with the
// financial controller, the branch manager, the procurement manager or
// the yard team), so the observation isolates the credential class from
// both the x-role tier gates and the role gate. Admitted here means the
// whole gate stack passed (the handler's own answer, never a family
// refusal), because the served handlers are DB-backed modules.
func TestRegisteredRouteBearerMatchesCensus(t *testing.T) {
	fx := newXRoleFixture(t)
	census := xrCensusByPattern(t)
	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatalf("roleconfig.Default: %v", err)
	}
	// xrStaffAdmittedLeg names the leg whose caller the tier gate and the
	// role gate both admit, walking the planted staff roles by tier. The
	// sync pair carries no staff gate at all (either bearer passes its
	// class leg), so any staff leg proves the staff class there.
	xrStaffAdmittedLeg := func(t *testing.T, route apigen.Route) string {
		t.Helper()
		if route.XRole == "scope-member" {
			return "staff tier 1 global"
		}
		for _, leg := range xrStaffLegs {
			tierOK := false
			switch route.XRole {
			case "requireStaff":
				tierOK = true
			case "requireManager":
				tierOK = leg.tier <= identity.TierManager
			case "requireAdmin":
				tierOK = leg.tier <= identity.TierLeadership
			default:
				t.Fatalf("bearer probe on x-role %q names no staff family", route.XRole)
			}
			if tierOK && xrMatrixAdmits(t, cfg, route, leg.role) {
				return leg.legName
			}
		}
		t.Fatalf("%s: no planted staff caller is admitted by the tier gate and the matrix together; plant the role whose row reaches this route", route.OperationID)
		return ""
	}
	for _, pattern := range fx.registered {
		route, ok := census[pattern]
		if !ok {
			t.Fatalf("registered route %s is not in the document; the parity test owns this direction", pattern)
		}
		t.Run(route.OperationID, func(t *testing.T) {
			admitted := func(legName string) {
				t.Helper()
				code, body := fx.probeLeg(t, route, xrLegByName(t, legName))
				for _, familyBody := range [][]byte{authz.BodyUnauthorized, authz.BodyForbidden, authz.BodyUnavailable} {
					if code == http.StatusServiceUnavailable && body == string(authz.BodyUnavailable) {
						t.Fatalf("bearer class %q: leg %q answered the gate's operational refusal: %s", route.Bearer, legName, body)
					}
					if body == string(familyBody) {
						t.Fatalf("bearer class %q: leg %q refused (%d %q); the gate's bearer requirement does not equal the census",
							route.Bearer, legName, code, body)
					}
				}
			}
			refused := func(legName string) {
				t.Helper()
				code, body := fx.probeLeg(t, route, xrLegByName(t, legName))
				if code != http.StatusForbidden || body != string(authz.BodyForbidden) {
					t.Fatalf("bearer class %q: leg %q answered %d %q, want the RBAC-403 family refusal", route.Bearer, legName, code, body)
				}
			}
			switch route.Bearer {
			case "staff":
				admitted(xrStaffAdmittedLeg(t, route))
				refused("tenant account admin")
			case "tenant":
				admitted("tenant account admin")
				refused("staff tier 1 global")
			case "staff-or-tenant":
				admitted(xrStaffAdmittedLeg(t, route))
				admitted("tenant account admin")
			case "public":
				admitted("anonymous")
			default:
				t.Fatalf("census Bearer %q names no bearer class the guard constructors define", route.Bearer)
			}
		})
	}
	t.Logf("bearer class asserted for %d registered operations against the census column", len(fx.registered))
}
