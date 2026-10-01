package main

// The role gate's composition tests (D48, D49, plan item RG): the served
// surface, not the document. Three assertions live here. First, the
// completeness walk: every registered staff route carries a surface and a
// level, read from the census the wrapping registrar reads, so a route
// registered in Go and annotated nowhere fails here (and the registrar's
// own attach-time check panics the registration first). Second, the
// matrix at the counter: as inside_sales a till sale, a return and a
// stock read are admitted and a dispatch read and a price rule write are
// refused; as sales_manager a dispatch read and a price rule write are
// admitted and a delivery date commit is refused. Third, the per dealer
// configuration: with the configuration giving inside_sales dispatch at
// Read, built by roleconfig.Validate on a modified default document, that
// read is admitted with no code change. The fixture is the composition
// harness (compfixture_test.go): the real registerRoutes over the
// identity environment map and the gate database.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// The matrix legs' principals: the four planted staff roles whose rows of
// the dealer's configuration reach routes the three tier legs' roles do
// not (the money desk's admin cells, the dispatch board's work cells,
// buying and stock's manage cells, the yard pick surface). Fixed ids and
// ON CONFLICT DO NOTHING, the testkit fixture's own discipline: a second
// run against the same scratch database inserts nothing.
const (
	xrSubFinancialController = "fxstaff-fin-controller-rg"
	xrSubBranchManager       = "fxstaff-branch-manager-rg"
	xrSubProcurementManager  = "fxstaff-proc-manager-rg"
	xrSubYardTeam            = "fxstaff-yard-team-rg"

	xrUserFinancialControllerID = "3ef10000-0000-4000-8000-000000001311"
	xrUserBranchManagerID       = "3ef10000-0000-4000-8000-000000001312"
	xrUserProcurementManagerID  = "3ef10000-0000-4000-8000-000000001313"
	xrUserYardTeamID            = "3ef10000-0000-4000-8000-000000001314"

	xrGrantFinancialControllerID = "3ef10000-0000-4000-8000-000000001321"
	xrGrantBranchManagerID       = "3ef10000-0000-4000-8000-000000001322"
	xrGrantProcurementManagerID  = "3ef10000-0000-4000-8000-000000001323"
	xrGrantYardTeamID            = "3ef10000-0000-4000-8000-000000001324"
)

// plantMatrixStaff inserts the four principals' users rows, staff role
// grants and directory memberships. Idempotent, never cleaned: the rows
// are fixture rows of the same standing as the testkit builder's, keyed
// by fixed id so a second run against the same scratch database is a
// no-op.
func plantMatrixStaff(t *testing.T, env *compEnv, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, display_name, status, platform_user_id, org_id) VALUES
		($1, 'fxstaff.fin.controller.rg@fixture.test', 'Fixture Financial Controller RG', 'ACTIVE', $5, $9),
		($2, 'fxstaff.branch.manager.rg@fixture.test', 'Fixture Branch Manager RG',       'ACTIVE', $6, $9),
		($3, 'fxstaff.proc.manager.rg@fixture.test',   'Fixture Procurement Manager RG',  'ACTIVE', $7, $9),
		($4, 'fxstaff.yard.team.rg@fixture.test',      'Fixture Yard Team RG',            'ACTIVE', $8, $9)
		ON CONFLICT (id) DO NOTHING`,
		xrUserFinancialControllerID, xrUserBranchManagerID, xrUserProcurementManagerID, xrUserYardTeamID,
		xrSubFinancialController, xrSubBranchManager, xrSubProcurementManager, xrSubYardTeam,
		testkit.OrgNorthID); err != nil {
		t.Fatalf("plant matrix users rows: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO staff_role_grant (id, platform_user_id, role_id, org_id, branch_id) VALUES
		($1, $5, 'financial_controller', $9, NULL),
		($2, $6, 'branch_manager',       $9, $10),
		($3, $7, 'procurement_manager',  $9, $10),
		($4, $8, 'yard_team',            $9, $10)
		ON CONFLICT (id) DO NOTHING`,
		xrGrantFinancialControllerID, xrGrantBranchManagerID, xrGrantProcurementManagerID, xrGrantYardTeamID,
		xrSubFinancialController, xrSubBranchManager, xrSubProcurementManager, xrSubYardTeam,
		testkit.OrgNorthID, testkit.BranchNorthAID); err != nil {
		t.Fatalf("plant matrix staff role grants: %v", err)
	}
	orgNorth := "org:" + testkit.OrgNorthSlug
	for _, sub := range []string{xrSubFinancialController, xrSubBranchManager, xrSubProcurementManager, xrSubYardTeam} {
		env.appwrite.setTeam(sub, orgNorth)
	}
}

// xrKnownLevel reports whether s names one of the five levels the
// configuration's vocabulary holds.
func xrKnownLevel(s string) bool {
	switch roleconfig.Level(s) {
	case roleconfig.LevelNone, roleconfig.LevelRead, roleconfig.LevelWork, roleconfig.LevelManage, roleconfig.LevelAdmin:
		return true
	}
	return false
}

// TestRegisteredStaffRoutesCarrySurfaceAndLevel is the completeness
// assertion: every registered staff route carries a surface and a level
// in roleconfig's vocabulary, and no non-staff route carries an
// annotation at all. The walk is over the registered routes, not the
// document, so a route registered in Go and annotated nowhere fails here
// even though the document and the census agree with each other; the
// registrar's attach-time check is the belt to this brace, refusing the
// same defect at registration. The census side is asserted beside it:
// the document's 594 staff operations all carry the annotation, so the
// ledgered (not yet registered) routes are covered too.
func TestRegisteredStaffRoutesCarrySurfaceAndLevel(t *testing.T) {
	fx := newXRoleFixture(t)
	census := xrCensusByPattern(t)
	if len(fx.registered) == 0 {
		t.Fatal("registerRoutes registered nothing; the assertion surface is empty")
	}
	staff := 0
	for _, pattern := range fx.registered {
		route, ok := census[pattern]
		if !ok {
			t.Fatalf("registered route %s is not in the census; the parity test owns this direction, and the role gate attached no annotation", pattern)
		}
		switch {
		case route.XRole == "requireStaff" || route.XRole == "requireManager" || route.XRole == "requireAdmin":
			staff++
			assertRouteAnnotated(t, pattern, route)
		default:
			if len(route.Surfaces) != 0 || route.Level != "" {
				t.Fatalf("non-staff route %s (%s) carries an x-access annotation: surfaces %v level %q", pattern, route.OperationID, route.Surfaces, route.Level)
			}
		}
	}
	censusStaff := 0
	for _, route := range apigen.Routes {
		switch route.XRole {
		case "requireStaff", "requireManager", "requireAdmin":
			censusStaff++
			assertRouteAnnotated(t, route.Method+" "+route.Path, route)
		default:
			if len(route.Surfaces) != 0 || route.Level != "" {
				t.Fatalf("non-staff census route %s (%s) carries an annotation", route.OperationID, route.XRole)
			}
		}
	}
	if censusStaff != 594 {
		t.Fatalf("census staff routes: %d, want the document's 594", censusStaff)
	}
	t.Logf("surface and level asserted for %d registered staff routes and all %d census staff routes", staff, censusStaff)
}

// assertRouteAnnotated holds one staff route to the annotation contract.
func assertRouteAnnotated(t *testing.T, pattern string, route apigen.Route) {
	t.Helper()
	if len(route.Surfaces) == 0 {
		t.Fatalf("staff route %s (%s) carries no surface", pattern, route.OperationID)
	}
	for _, s := range route.Surfaces {
		if !roleconfig.IsSurface(s) {
			t.Fatalf("staff route %s (%s) names unknown surface %q", pattern, route.OperationID, s)
		}
	}
	if !xrKnownLevel(route.Level) {
		t.Fatalf("staff route %s (%s) names unknown level %q", pattern, route.OperationID, route.Level)
	}
}

// rgAdmitted observes the gate's pass: the answer wears none of the three
// authz family refusal bodies, the x-role suite's own convention (the
// served handlers answer their own validation and not-found families).
func rgAdmitted(t *testing.T, name string, code int, body string) {
	t.Helper()
	for _, familyBody := range [][]byte{authz.BodyUnauthorized, authz.BodyForbidden, authz.BodyUnavailable} {
		if body == string(familyBody) {
			t.Fatalf("%s: the answer wears a gate refusal body (%d %s)", name, code, body)
		}
	}
}

// rgRefused observes the RBAC-403: authenticated, a gate refused, one
// byte-identical body.
func rgRefused(t *testing.T, name string, code int, body string) {
	t.Helper()
	if code != http.StatusForbidden || body != string(authz.BodyForbidden) {
		t.Fatalf("%s: status %d body %s, want the RBAC-403 family", name, code, body)
	}
}

// rgProbe issues one request against the fixture's registered surface
// with the probe path's parameters substituted the x-role suite's way.
func rgProbe(t *testing.T, fx *xrFixture, method, path string, headers map[string]string) (int, string) {
	t.Helper()
	return fx.probe(t, method, xrProbePath(path), headers)
}

// rgHeaders is one demo login's request headers, a staff bearer with the
// org selection.
func rgHeaders(t *testing.T, fx *xrFixture, sub string) map[string]string {
	t.Helper()
	return map[string]string{
		"Authorization":  "Bearer " + fx.env.minter.StaffToken(t, sub),
		authz.HeaderXOrg: xrXOrg,
	}
}

// TestRoleGateMatrixAtTheCounter is the exit test's admission legs over
// the served surface: as inside_sales a till sale, a return and a stock
// read are admitted and a dispatch read and a price rule write are
// refused; as sales_manager a dispatch read and a price rule write are
// admitted and a delivery date commit is refused.
func TestRoleGateMatrixAtTheCounter(t *testing.T) {
	fx := newXRoleFixture(t)
	sales := rgHeaders(t, fx, testkit.SubInsideSales)
	manager := rgHeaders(t, fx, testkit.SubSalesManager)

	code, body := rgProbe(t, fx, http.MethodPost, "/api/v1/pos/sales", sales)
	rgAdmitted(t, "inside sales rings a till sale", code, body)
	code, body = rgProbe(t, fx, http.MethodPost, "/api/v1/pos/returns", sales)
	rgAdmitted(t, "inside sales takes a return", code, body)
	code, body = rgProbe(t, fx, http.MethodGet, "/api/v1/inventory/on-hand", sales)
	rgAdmitted(t, "inside sales reads stock", code, body)
	code, body = rgProbe(t, fx, http.MethodGet, "/api/v1/dispatch/board", sales)
	rgRefused(t, "inside sales is held off the dispatch board", code, body)
	code, body = rgProbe(t, fx, http.MethodPost, "/api/v1/pricing/rules", sales)
	rgRefused(t, "inside sales is held off price rule writes", code, body)

	code, body = rgProbe(t, fx, http.MethodGet, "/api/v1/dispatch/board", manager)
	rgAdmitted(t, "sales manager reads the dispatch board", code, body)
	code, body = rgProbe(t, fx, http.MethodPost, "/api/v1/pricing/rules", manager)
	rgAdmitted(t, "sales manager writes a price rule", code, body)
	code, body = rgProbe(t, fx, http.MethodPost, "/api/v1/dispatch/stops/{id}/commit", manager)
	rgRefused(t, "sales manager is held off the delivery date commit", code, body)
}

// TestRoleGateDealerConfigurationAtTheCounter is the exit test's
// configuration leg: the same construction over a configuration whose
// document gives inside_sales dispatch at Read, built by
// roleconfig.Validate with no code change, admits the dispatch read the
// default refuses.
func TestRoleGateDealerConfigurationAtTheCounter(t *testing.T) {
	env := newCompEnv(t)
	var doc roleconfig.Document
	if err := json.Unmarshal(roleconfig.DefaultData(), &doc); err != nil {
		t.Fatalf("decode the default document: %v", err)
	}
	changed := false
	for i := range doc.Cells {
		if doc.Cells[i].Role == "inside_sales" && doc.Cells[i].Surface == "dispatch-board" {
			doc.Cells[i].Level = roleconfig.LevelRead
			changed = true
		}
	}
	if !changed {
		t.Fatal("the default document holds no inside_sales dispatch-board cell")
	}
	cfg, err := roleconfig.Validate(&doc)
	if err != nil {
		t.Fatalf("Validate on the modified document: %v", err)
	}
	rec := &recordingRegistrar{inner: http.NewServeMux()}
	graph, err := registerRoutesWithRoleConfig(rec, env.getenv, cfg)
	if err != nil {
		t.Fatalf("registerRoutesWithRoleConfig: %v", err)
	}
	t.Cleanup(graph.pool.Close)
	fx := &xrFixture{env: env, reg: rec, registered: rec.patterns}

	code, body := rgProbe(t, fx, http.MethodGet, "/api/v1/dispatch/board", rgHeaders(t, fx, testkit.SubInsideSales))
	rgAdmitted(t, "inside sales reads dispatch under the configured cell", code, body)
}
