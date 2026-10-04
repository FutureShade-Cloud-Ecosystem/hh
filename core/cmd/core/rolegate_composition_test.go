package main

// The role gate's composition tests (D48, D49, plan item RG, PR 44 review
// rulings): the served surface, not the document. The census half of the
// completeness contract is split into TestCensusStaffRoutesCarrySurfaceAndLevel
// (census_test.go), which needs no database and runs in the repository's
// CI; what remains here is the registered half. Three assertions live in
// this file. First, the registered walk: every route registerRoutes
// registers that the census calls staff carries a surface and a level in
// roleconfig's vocabulary; it leans on the registrar's attach-time panic
// (rolegate_registrar_test.go proves the wrapper really wraps) and it
// feeds on the same census the plain test walks. Second, the matrix at
// the counter: as inside_sales a till sale, a return, a stock read, the
// card rail's status and an approval filing are admitted, and a dispatch
// read and a price rule write are refused; as sales_manager a dispatch
// read and a price rule write are admitted and a delivery date commit is
// refused; a caller holding general_manager and inside_sales rings a sale
// on the second grant (X10's whole row); hr is refused the price writes
// the pricing_editors list gates. Third, the per dealer configuration:
// with the configuration giving inside_sales dispatch at Read and the
// branch manager a pricing editor's seat and cell, built by
// roleconfig.Validate on a modified default document, the dispatch read
// and the price write are admitted, and a dispatch work route is still
// refused under the read cell, with no code change. The fixture is the
// composition harness (compfixture_test.go): the real registerRoutes over
// the identity environment map and the gate database.

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

// The matrix legs' principals: the planted staff roles whose rows of the
// dealer's configuration reach routes the three tier legs' roles do not
// (the money desk's admin cells, the dispatch board's work cells, buying
// and stock's manage cells, the yard pick surface), hr for the
// pricing_editors wall, and a two-grant caller for X10's whole row. Fixed
// ids and ON CONFLICT DO NOTHING, the testkit fixture's own discipline: a
// second run against the same scratch database inserts nothing.
const (
	xrSubFinancialController = "fxstaff-fin-controller-rg"
	xrSubBranchManager       = "fxstaff-branch-manager-rg"
	xrSubProcurementManager  = "fxstaff-proc-manager-rg"
	xrSubYardTeam            = "fxstaff-yard-team-rg"
	xrSubHR                  = "fxstaff-hr-rg"
	xrSubTwoGrants           = "fxstaff-two-grants-rg"

	xrUserFinancialControllerID = "3ef10000-0000-4000-8000-000000001311"
	xrUserBranchManagerID       = "3ef10000-0000-4000-8000-000000001312"
	xrUserProcurementManagerID  = "3ef10000-0000-4000-8000-000000001313"
	xrUserYardTeamID            = "3ef10000-0000-4000-8000-000000001314"
	xrUserHRID                  = "3ef10000-0000-4000-8000-000000001315"
	xrUserTwoGrantsID           = "3ef10000-0000-4000-8000-000000001316"

	xrGrantFinancialControllerID = "3ef10000-0000-4000-8000-000000001321"
	xrGrantBranchManagerID       = "3ef10000-0000-4000-8000-000000001322"
	xrGrantProcurementManagerID  = "3ef10000-0000-4000-8000-000000001323"
	xrGrantYardTeamID            = "3ef10000-0000-4000-8000-000000001324"
	xrGrantHRID                  = "3ef10000-0000-4000-8000-000000001325"
	xrGrantTwoGrantsMgrID        = "3ef10000-0000-4000-8000-000000001326"
	xrGrantTwoGrantsSalesID      = "3ef10000-0000-4000-8000-000000001327"
)

// plantMatrixStaff inserts the principals' users rows, staff role grants
// and directory memberships. The two-grant caller holds general_manager
// (global) and inside_sales (branch A): the resolver's winner is the
// general manager, and X10 says the sales grant's work cell must still
// count. Idempotent, never cleaned: the rows are fixture rows of the same
// standing as the testkit builder's, keyed by fixed id so a second run
// against the same scratch database is a no-op.
func plantMatrixStaff(t *testing.T, env *compEnv, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, display_name, status, platform_user_id, org_id) VALUES
		($1, 'fxstaff.fin.controller.rg@fixture.test', 'Fixture Financial Controller RG', 'ACTIVE', $7, $11),
		($2, 'fxstaff.branch.manager.rg@fixture.test', 'Fixture Branch Manager RG',       'ACTIVE', $8, $11),
		($3, 'fxstaff.proc.manager.rg@fixture.test',   'Fixture Procurement Manager RG',  'ACTIVE', $9, $11),
		($4, 'fxstaff.yard.team.rg@fixture.test',      'Fixture Yard Team RG',            'ACTIVE', $10, $11),
		($5, 'fxstaff.hr.rg@fixture.test',             'Fixture HR RG',                   'ACTIVE', $12, $11),
		($6, 'fxstaff.two.grants.rg@fixture.test',     'Fixture Two Grants RG',           'ACTIVE', $13, $11)
		ON CONFLICT (id) DO NOTHING`,
		xrUserFinancialControllerID, xrUserBranchManagerID, xrUserProcurementManagerID, xrUserYardTeamID,
		xrUserHRID, xrUserTwoGrantsID,
		xrSubFinancialController, xrSubBranchManager, xrSubProcurementManager, xrSubYardTeam,
		testkit.OrgNorthID, xrSubHR, xrSubTwoGrants); err != nil {
		t.Fatalf("plant matrix users rows: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO staff_role_grant (id, platform_user_id, role_id, org_id, branch_id) VALUES
		($1, $8,  'financial_controller', $14, NULL),
		($2, $9,  'branch_manager',       $14, $15),
		($3, $10, 'procurement_manager',  $14, $15),
		($4, $11, 'yard_team',            $14, $15),
		($5, $12, 'hr',                   $14, NULL),
		($6, $13, 'general_manager',      $14, NULL),
		($7, $13, 'inside_sales',         $14, $15)
		ON CONFLICT (id) DO NOTHING`,
		xrGrantFinancialControllerID, xrGrantBranchManagerID, xrGrantProcurementManagerID, xrGrantYardTeamID,
		xrGrantHRID, xrGrantTwoGrantsMgrID, xrGrantTwoGrantsSalesID,
		xrSubFinancialController, xrSubBranchManager, xrSubProcurementManager, xrSubYardTeam,
		xrSubHR, xrSubTwoGrants,
		testkit.OrgNorthID, testkit.BranchNorthAID); err != nil {
		t.Fatalf("plant matrix staff role grants: %v", err)
	}
	orgNorth := "org:" + testkit.OrgNorthSlug
	for _, sub := range []string{xrSubFinancialController, xrSubBranchManager, xrSubProcurementManager, xrSubYardTeam, xrSubHR, xrSubTwoGrants} {
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

// TestRegisteredStaffRoutesCarrySurfaceAndLevel is the registered half of
// the completeness assertion: every route the binary registers that the
// census calls staff carries a surface and a level in roleconfig's
// vocabulary, and no registered route the census does not call staff
// carries an annotation. This test leans on two things it cannot see: the
// census half (all 595 document operations annotated, including the 31
// ledgered routes not yet registered) is the plain TestCensusStaffRoutes-
// CarrySurfaceAndLevel beside this file, and the fact that the walk's
// passing means the gate is truly in the chain is the registrar's own
// suite (TestRegistrarAttachesTheGate), because this walk reads the
// census, not the handler chain. The walk is over the registered routes,
// not the document alone, so a route registered in Go and annotated
// nowhere fails here; the registrar's attach-time panic is the belt to
// this brace, refusing the same defect at registration.
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
			if len(route.Surfaces) != 0 || route.Level != "" || route.List != "" {
				t.Fatalf("non-staff route %s (%s) carries an x-access annotation: surfaces %v level %q list %q", pattern, route.OperationID, route.Surfaces, route.Level, route.List)
			}
		}
	}
	t.Logf("surface and level asserted for %d registered staff routes (the census half is the plain census test)", staff)
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
	if route.List != "" && route.List != roleconfig.SettingPricingEditors && route.List != roleconfig.SettingCostVisibility {
		t.Fatalf("staff route %s (%s) names list %q, which no gate reader knows", pattern, route.OperationID, route.List)
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
// the served surface, plus the review's additions: as inside_sales a till
// sale, a return, a stock read, the card rail's status read (P1-1: the
// till's card notice is built on it) and an approval filing (P2-3: the
// salesperson files) are admitted, and a dispatch read and a price rule
// write are refused; as sales_manager a dispatch read and a price rule
// write are admitted and a delivery date commit is refused; the
// two-grant caller rings a sale on its second grant (P2-1, X10); hr is
// refused the price writes the named list gates (P2-2, X6).
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
	code, body = rgProbe(t, fx, http.MethodGet, "/api/v1/payments/config", sales)
	rgAdmitted(t, "inside sales reads the card rail's status", code, body)
	// The read serves the dealer-admin readers too: hr's admin cell reads
	// what hr's seat could already write.
	code, body = rgProbe(t, fx, http.MethodGet, "/api/v1/payments/config", rgHeaders(t, fx, xrSubHR))
	rgAdmitted(t, "hr reads the card rail's status", code, body)
	code, body = rgProbe(t, fx, http.MethodPost, "/api/v1/staff/approvals", sales)
	rgAdmitted(t, "inside sales files an approval request", code, body)
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

	// X10's whole row: the resolver's winner is the general manager (tier
	// 1), whose counter-till cell is read, and the sale needs work; the
	// admission comes from the second grant's inside_sales work cell.
	two := rgHeaders(t, fx, xrSubTwoGrants)
	code, body = rgProbe(t, fx, http.MethodPost, "/api/v1/pos/sales", two)
	rgAdmitted(t, "the two-grant caller rings a sale on the second grant", code, body)

	// X6 through the gate: hr's dealer-admin cell is admin, so the level
	// alone would admit it; the pricing_editors list refuses it.
	hr := rgHeaders(t, fx, xrSubHR)
	code, body = rgProbe(t, fx, http.MethodPost, "/api/v1/pricing/rules", hr)
	rgRefused(t, "hr is held off the price writes the list gates", code, body)
}

// TestRoleGateDealerConfigurationAtTheCounter is the exit test's
// configuration leg, on both walls the gate reads (P2-1's list beside
// P1-1's cell): the same construction over a configuration whose document
// gives inside_sales dispatch at Read and gives the branch manager the
// pricing editor's seat and cell, built by roleconfig.Validate with no
// code change, admits the dispatch read and the price write the default
// refuses, and still refuses a dispatch work route under the read cell.
// The admissions here lean on the refusal legs of
// TestRoleGateMatrixAtTheCounter for their contrast (this test asserts no
// refusal of its own gate-off shape): a read-only assertion would pass
// with the gate absent, which is why the matrix test's refused legs are
// load-bearing for this one too.
func TestRoleGateDealerConfigurationAtTheCounter(t *testing.T) {
	env := newCompEnv(t)
	var doc roleconfig.Document
	if err := json.Unmarshal(roleconfig.DefaultData(), &doc); err != nil {
		t.Fatalf("decode the default document: %v", err)
	}
	changedCell, changedEditorCell := false, false
	for i := range doc.Cells {
		if doc.Cells[i].Role == "inside_sales" && doc.Cells[i].Surface == "dispatch-board" {
			doc.Cells[i].Level = roleconfig.LevelRead
			changedCell = true
		}
		if doc.Cells[i].Role == "branch_manager" && doc.Cells[i].Surface == "dealer-admin" {
			doc.Cells[i].Level = roleconfig.LevelManage
			changedEditorCell = true
		}
	}
	if !changedCell || !changedEditorCell {
		t.Fatal("the default document lacks a cell the configuration leg edits")
	}
	var editors []string
	if err := json.Unmarshal(doc.Settings[roleconfig.SettingPricingEditors], &editors); err != nil {
		t.Fatalf("decode the default editors list: %v", err)
	}
	editors = append(editors, "branch_manager")
	raw, err := json.Marshal(editors)
	if err != nil {
		t.Fatalf("encode the edited editors list: %v", err)
	}
	doc.Settings[roleconfig.SettingPricingEditors] = raw
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
	// The configured editor's principal: users row, grant and directory
	// membership, the same planting the default-surface fixture does.
	plantMatrixStaff(t, env, graph.pool)
	fx := &xrFixture{env: env, reg: rec, registered: rec.patterns}

	code, body := rgProbe(t, fx, http.MethodGet, "/api/v1/dispatch/board", rgHeaders(t, fx, testkit.SubInsideSales))
	rgAdmitted(t, "inside sales reads dispatch under the configured cell", code, body)
	// The same cell is still only a read: the commit stays refused.
	code, body = rgProbe(t, fx, http.MethodPost, "/api/v1/dispatch/stops/{id}/commit", rgHeaders(t, fx, testkit.SubInsideSales))
	rgRefused(t, "the configured read cell does not carry the delivery date commit", code, body)
	// The configured editor writes prices with no code change.
	code, body = rgProbe(t, fx, http.MethodPost, "/api/v1/pricing/rules", rgHeaders(t, fx, xrSubBranchManager))
	rgAdmitted(t, "the configured editor writes a price rule", code, body)
}
