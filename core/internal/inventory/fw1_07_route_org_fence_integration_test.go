//go:build integration

package inventory

// fw1_07_route_org_fence_integration_test.go, round 3 (FROM-PRINCIPAL 184). The Controller
// exceptions route's read is fenced by the caller's org FIRST, then by branch reach for a scoped
// caller: GLOBAL means every branch of the caller's OWN org, never another org's. Round 2's route
// held a scoped caller to the branches they reach but let a GLOBAL caller read every org's rows,
// on the consolidated read and on a named branch of another org. The route legs here drive the real
// route table behind the real guards, the way the tax module's FW1-10 fence legs do, so the fence
// the middleware and the handler compose is the thing under test, not the service read alone.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	auth "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fw107r3BranchB is an invented branch id for the second org's branch, planted for the legs (the
// seeded branches carry no org of their own and a populated box may stamp them, so the second org's
// branch is always planted, never borrowed).
const fw107r3BranchB = "88a00000-0000-4000-8000-0000000003b2"

// fw107r3Exception plants one provisional pre receipt SHIP move at a branch on its own product and
// answers the product: the row the exceptions read lists (move_type SHIP, metadata provisional_cost
// true, no receipt has trued it up). The fence legs read the fence, not the fallback rule, so the
// row is planted, not sold.
func fw107r3Exception(t *testing.T, pool *pgxpool.Pool, branchID string, ref string) string {
	t.Helper()
	ctx := context.Background()
	p := mkProduct(t, pool, "FW107-R3-"+ref)
	if _, err := pool.Exec(ctx, `
		INSERT INTO stock_moves (product_id, from_location_id, qty_base, qty_entered, uom_entered,
		                         move_type, unit_cost_cents, value_cents, source_type, source_ref_id,
		                         branch_id, metadata)
		VALUES ($1, $2, 2, 2, 'EA', 'SHIP', 900, 1800, 'order', $3, $2,
		        jsonb_build_object('provisional_cost', 'true', 'fallback_source', 'LAST_KNOWN_COST'))`,
		p, branchID, ref); err != nil {
		t.Fatalf("plant the provisional ship move: %v", err)
	}
	return p
}

// fw107r3PlantBranchB plants the second org's branch and takes it away with the leg.
func fw107r3PlantBranchB(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO locations (id, branch_id, parent_id, loc_type, code, name, address)
		VALUES ($1, $1, NULL, 'BRANCH', 'FW107R3B', 'FW1-07 round 3 second org branch', '{}')
		ON CONFLICT (id) DO NOTHING`, fw107r3BranchB)
	if err != nil {
		t.Fatalf("plant the second org's branch: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM locations WHERE id = $1`, fw107r3BranchB) })
}

// fw107r3Mount mounts the real inventory route table behind the real guards and answers one GET of
// the controller exceptions route: the caller's authority is what the harness stub resolves, the
// caller's org is the slug the request names resolved through the harness resolver (north slug to
// the first org, south slug to the second), the same shape the tax module's fence legs use.
func fw107r3Mount(t *testing.T, pool *pgxpool.Pool, authority auth.StaffAuthority, xOrg, query string) (int, []PreReceiptException) {
	t.Helper()
	sg := testkit.NewStaffGuards(t)
	sg.Harness.Resolver.Set(testkit.OrgNorthSlug, ux88OrgA)
	sg.Harness.Resolver.Set(testkit.OrgSouthSlug, ux88OrgB)
	sg.Harness.Membership.SetMember(testkit.StaffSubAny, testkit.OrgSouthSlug, true)
	sg.SetAuthority(authority)

	h := NewHandler(newIntSvc(pool), nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/inventory/controller-exceptions"+query, nil)
	r.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
	r.Header.Set("X-Org", xOrg)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	var body struct {
		Exceptions []PreReceiptException `json:"exceptions"`
	}
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("the body is not the exceptions list: %v (%s)", err, rec.Body.String())
		}
	}
	return rec.Code, body.Exceptions
}

func fw107r3Has(rows []PreReceiptException, product string) bool {
	for _, r := range rows {
		if r.ProductID == product {
			return true
		}
	}
	return false
}

// The Controller's own org legs: the Controller of the first org reads its own org's rows and
// nothing of the second org's, on the consolidated read and on a named branch. A GLOBAL caller of
// the second org reads none of the first org's rows. The named branch of another org is refused so
// the read cannot tell it from a branch that does not exist.
func TestFW1_07R3_ControllerExceptionsRouteIsFencedToTheCallersOrg(t *testing.T) {
	pool := intPool(t)
	fw107r3PlantBranchB(t, pool)
	stampBranchOrgs(t, pool, map[string]string{branch1: ux88OrgA, fw107r3BranchB: ux88OrgB})
	own := fw107r3Exception(t, pool, branch1, "ORGAREF")
	other := fw107r3Exception(t, pool, fw107r3BranchB, "ORGBREF")

	controllerA := auth.StaffAuthority{RoleID: "FINANCIAL_CONTROLLER", Tier: auth.TierLeadership, Scope: auth.ScopeGlobal}
	controllerB := controllerA

	// The Controller of the first org, the consolidated read: its own org's rows only.
	code, rows := fw107r3Mount(t, pool, controllerA, testkit.OrgNorthSlug, "")
	if code != http.StatusOK || !fw107r3Has(rows, own) {
		t.Fatalf("the Controller's consolidated read answers %d with its own org's row present = %v, want 200 and yes", code, fw107r3Has(rows, own))
	}
	if fw107r3Has(rows, other) {
		t.Fatalf("the Controller's consolidated read carries the other org's row: a GLOBAL caller read every org's rows")
	}

	// The Controller of the first org naming the second org's branch: refused.
	if code, rows = fw107r3Mount(t, pool, controllerA, testkit.OrgNorthSlug, "?branch_id="+fw107r3BranchB); code == http.StatusOK && fw107r3Has(rows, other) {
		t.Fatalf("naming another org's branch answers %d with that branch's rows: a GLOBAL caller read another org's branch", code)
	}

	// A GLOBAL caller of the second org, the consolidated read: none of the first org's rows.
	code, rows = fw107r3Mount(t, pool, controllerB, testkit.OrgSouthSlug, "")
	if code != http.StatusOK {
		t.Fatalf("the second org's GLOBAL caller answers %d, want 200", code)
	}
	if fw107r3Has(rows, own) || fw107r3Has(rows, other) {
		t.Fatalf("the second org's GLOBAL caller read rows of the first org (%v) or missed its own (%v)", fw107r3Has(rows, own), !fw107r3Has(rows, other))
	}
}

// A branch scoped manager sees only its branches' rows: every branch it reaches, and no branch it
// does not, even inside its own org. A branch inside the org but outside the caller's reach stays a
// refusal.
func TestFW1_07R3_BranchScopedManagerSeesOnlyItsBranchesRows(t *testing.T) {
	pool := intPool(t)
	stampBranchOrgs(t, pool, map[string]string{branch1: ux88OrgA, branch2: ux88OrgA})
	own := fw107r3Exception(t, pool, branch1, "TRENTREF")
	second := fw107r3Exception(t, pool, branch2, "KINGSREF")

	twoBranches := auth.StaffAuthority{RoleID: "BRANCH_MANAGER", Tier: auth.TierManager, Scope: auth.ScopeBranch,
		Branches: []string{branch1, branch2}}
	oneBranch := auth.StaffAuthority{RoleID: "BRANCH_MANAGER", Tier: auth.TierManager, Scope: auth.ScopeBranch,
		Branches: []string{branch1}}

	// Both branches granted, the consolidated read: both branches' rows.
	code, rows := fw107r3Mount(t, pool, twoBranches, testkit.OrgNorthSlug, "")
	if code != http.StatusOK || !fw107r3Has(rows, own) || !fw107r3Has(rows, second) {
		t.Fatalf("a manager of two branches reads %d with both rows present = %v/%v, want 200 and both (first branch only is the round 2 shape)", code, fw107r3Has(rows, own), fw107r3Has(rows, second))
	}

	// Both branches granted, a named granted branch: that branch's rows.
	if code, rows = fw107r3Mount(t, pool, twoBranches, testkit.OrgNorthSlug, "?branch_id="+branch2); code != http.StatusOK || !fw107r3Has(rows, second) {
		t.Fatalf("a granted named branch answers %d with the row present = %v, want 200 and yes", code, fw107r3Has(rows, second))
	}

	// One branch granted, the other branch of the same org named: refused.
	if code, _ = fw107r3Mount(t, pool, oneBranch, testkit.OrgNorthSlug, "?branch_id="+branch2); code != http.StatusForbidden {
		t.Fatalf("a branch outside the caller's reach answers %d, want 403", code)
	}
}

// A clerk is refused the route: the Controller's exceptions list is a manager read.
func TestFW1_07R3_ClerkIsRefusedTheControllerExceptionsRoute(t *testing.T) {
	pool := intPool(t)
	stampBranchOrgs(t, pool, map[string]string{branch1: ux88OrgA, branch2: ux88OrgA})
	fw107r3Exception(t, pool, branch1, "CLERKREF")

	clerk := auth.StaffAuthority{RoleID: "INSIDE_SALES", Tier: auth.TierFrontline, Scope: auth.ScopeBranch,
		Branches: []string{branch1}}
	if code, _ := fw107r3Mount(t, pool, clerk, testkit.OrgNorthSlug, ""); code != http.StatusForbidden {
		t.Fatalf("a clerk reads the Controller exceptions route as %d, want 403", code)
	}
}
