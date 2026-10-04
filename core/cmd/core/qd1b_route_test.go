package main

// qd1b_route_test.go is the grant items' served-surface legs (review round
// 1 part B, the principal's grants at decision log rows 739 and 740): the
// Sales manager's decide route, widened through the real gate, decided at
// the service by request. The fixture is the composition harness the
// x-role suite drives (compfixture_test.go): the full registerRoutes over
// the gate database, every guard real, every caller a planted principal
// whose row of the dealer's default configuration the gate reads. The
// planted approval rows ride the rail's own doors (the floor filing door,
// the caller-facing create door) and are cleaned by id, so the shared
// scratch database keeps its baseline for the suites that count rows.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// qd1bFixture is the registered surface plus the pool its graph holds, so
// a test can plant approval rows through the rail's own doors and then
// probe the routes that decide them.
type qd1bFixture struct {
	fx   *xrFixture
	pool *pgxpool.Pool
}

// newQD1BFixture boots the composition surface over the dealer's decided
// default configuration and plants the matrix principals (the branch
// manager and the financial controller legs ride plantMatrixStaff).
func newQD1BFixture(t *testing.T) *qd1bFixture {
	t.Helper()
	env := newCompEnv(t)
	rec := &recordingRegistrar{inner: http.NewServeMux()}
	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatalf("role configuration: %v", err)
	}
	graph, err := registerRoutesWithRoleConfig(rec, env.getenv, cfg)
	if err != nil {
		t.Fatalf("registerRoutesWithRoleConfig: %v", err)
	}
	t.Cleanup(graph.pool.Close)
	plantMatrixStaff(t, env, graph.pool)
	return &qd1bFixture{
		fx:   &xrFixture{env: env, reg: rec, registered: rec.patterns},
		pool: graph.pool,
	}
}

// qd1bCall drives one JSON request through the registered surface as one
// planted staff principal with the org selection, the decide legs' own
// probe (the x-role skeleton's body is the acceptance pair's, not a
// decision's).
func qd1bCall(t *testing.T, q *qd1bFixture, method, path, sub string, body any) (int, string) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode %s: %v", path, err)
		}
		rdr = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rdr)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+q.fx.env.minter.StaffToken(t, sub))
	req.Header.Set(authz.HeaderXOrg, xrXOrg)
	rec := httptest.NewRecorder()
	q.fx.reg.inner.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// qd1bUserID reads one planted principal's linked ERP users.id (the
// requester stamp the rail's doors take).
func qd1bUserID(t *testing.T, pool *pgxpool.Pool, sub string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM users WHERE platform_user_id = $1`, sub).Scan(&id); err != nil {
		t.Fatalf("linked user for %s: %v", sub, err)
	}
	return id
}

// qd1bPlantFloor files one pending floor request through the rail's floor
// door (D7: the only path a margin card is filed on), bound to a line row
// shaped id, and deletes it when the test ends.
func qd1bPlantFloor(t *testing.T, q *qd1bFixture, requester, branchID, lineIDSuffix string) string {
	t.Helper()
	rail := identity.NewApprovals(identity.NewApprovalRepository(q.pool))
	rec, err := rail.FileFloorApproval(context.Background(), identity.FloorApprovalFiling{
		RequestedBy: requester,
		BranchID:    branchID,
		SubjectRef:  "3ef10000-0000-4000-8000-00000000a001",
		Detail:      "qd1b fixture: pending floor request",
		LineNo:      1,
		DiscPct:     40,
		LineID:      "11111111-1111-4111-8111-" + lineIDSuffix,
		ProductID:   testkit.ProductID,
	})
	if err != nil {
		t.Fatalf("plant floor request: %v", err)
	}
	t.Cleanup(func() {
		_, _ = q.pool.Exec(context.Background(),
			`DELETE FROM permission_approval_requests WHERE id = $1`, rec.ID)
	})
	return rec.ID
}

// qd1bDecidePath is one request's decide route.
func qd1bDecidePath(id string) string {
	return "/api/v1/staff/approvals/" + id + "/decide"
}

// TestQD1B_SalesManagerDecidesOwnBranchFloorOverTheRoute is the grant's
// first condition through the real gate (CP0-1 B's route half): the
// branch's Sales manager, holding Manage on the Quote Desk, reaches the
// decide route and the service stamps its APPROVAL on a floor request of
// its own branch. Before the widening the gate refused the Sales manager
// at Manager Home Manage, so exit line 1's Sales manager half could not
// be proven by request. The reach legs beside it pin the widening's
// edges over the decided default: the branch manager, the requester and
// hr stay behind the gate, and the General Manager and the Financial
// Controller keep the reach they already held.
func TestQD1B_SalesManagerDecidesOwnBranchFloorOverTheRoute(t *testing.T) {
	q := newQD1BFixture(t)
	clerk := qd1bUserID(t, q.pool, testkit.SubInsideSales)
	own := qd1bPlantFloor(t, q, clerk, testkit.BranchNorthAID, "000000000b01")

	code, body := qd1bCall(t, q, http.MethodPost, qd1bDecidePath(own), testkit.SubSalesManager,
		map[string]any{"approve": true})
	if code != http.StatusOK || !json.Valid([]byte(body)) {
		t.Fatalf("the Sales manager decides its own branch's floor request: %d %s, want 200",
			code, body)
	}

	// The widening stops at the Quote Desk's manage cell: the roles that
	// hold Work there (the branch manager, the requester's own frontline
	// role) and the roles the Quote Desk serves not at all (hr) stay
	// behind the gate, byte-identical family refusals.
	for _, leg := range []struct {
		name string
		sub  string
	}{
		{"branch manager", xrSubBranchManager},
		{"the requester's frontline role", testkit.SubInsideSales},
		{"hr", xrSubHR},
	} {
		id := qd1bPlantFloor(t, q, clerk, testkit.BranchNorthAID, "000000000b02")
		code, body = qd1bCall(t, q, http.MethodPost, qd1bDecidePath(id), leg.sub,
			map[string]any{"approve": true})
		if code != http.StatusForbidden || body != string(authz.BodyForbidden) {
			t.Fatalf("%s reaches the decide route: %d %s, want the gate's refusal", leg.name, code, body)
		}
	}

	// The reach the route already had stays: the Tier-1 set passes the
	// gate and the service accepts their dealer wide floor authority.
	fc := qd1bPlantFloor(t, q, clerk, testkit.BranchNorthAID, "000000000b03")
	code, body = qd1bCall(t, q, http.MethodPost, qd1bDecidePath(fc), xrSubFinancialController,
		map[string]any{"approve": true})
	if code != http.StatusOK {
		t.Fatalf("the Financial Controller keeps the decide route: %d %s", code, body)
	}
	gm := qd1bPlantFloor(t, q, clerk, testkit.BranchNorthBID, "000000000b04")
	code, body = qd1bCall(t, q, http.MethodPost, qd1bDecidePath(gm), testkit.SubGeneralMgr,
		map[string]any{"approve": true})
	if code != http.StatusOK {
		t.Fatalf("the General Manager keeps the decide route: %d %s", code, body)
	}
}

// TestQD1B_SalesManagerHeldToOwnBranchAndOtherKinds is the grant's
// refusal conditions, all past the gate and decided by the service: the
// branch Sales manager's widened reach buys the floor of its own branch
// and nothing else. Another branch's floor request is refused (the
// branch reach), a credit request is refused (a leadership decision, not
// a Quote Desk one), and no role decides its own request (the General
// Manager's own filing, four eyes).
func TestQD1B_SalesManagerHeldToOwnBranchAndOtherKinds(t *testing.T) {
	q := newQD1BFixture(t)
	clerk := qd1bUserID(t, q.pool, testkit.SubInsideSales)

	// Another branch's floor request: the gate passes the Sales manager,
	// the service refuses the branch.
	other := qd1bPlantFloor(t, q, clerk, testkit.BranchNorthBID, "000000000b11")
	code, body := qd1bCall(t, q, http.MethodPost, qd1bDecidePath(other), testkit.SubSalesManager,
		map[string]any{"approve": true})
	if code != http.StatusForbidden || body == string(authz.BodyForbidden) {
		t.Fatalf("another branch's floor request over the widened route: %d %s, want the service's refusal, not the gate's",
			code, body)
	}

	// A credit request of the Sales manager's own branch: the widened
	// route reaches it, the service keeps it a leadership decision.
	code, body = qd1bCall(t, q, http.MethodPost, "/api/v1/staff/approvals", testkit.SubInsideSales,
		map[string]any{
			"branch_id":   testkit.BranchNorthAID,
			"policy_type": "CREDIT_LIMIT_EXCEEDED",
			"detail":      "qd1b fixture: a credit request",
		})
	if code != http.StatusCreated {
		t.Fatalf("the clerk files a credit request: %d %s", code, body)
	}
	var credit struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &credit); err != nil {
		t.Fatalf("decode the filed credit request: %v", err)
	}
	t.Cleanup(func() {
		_, _ = q.pool.Exec(context.Background(),
			`DELETE FROM permission_approval_requests WHERE id = $1`, credit.ID)
	})
	code, body = qd1bCall(t, q, http.MethodPost, qd1bDecidePath(credit.ID), testkit.SubSalesManager,
		map[string]any{"approve": true})
	if code != http.StatusForbidden || body == string(authz.BodyForbidden) {
		t.Fatalf("the Sales manager decides a credit request: %d %s, want the service's refusal, not the gate's",
			code, body)
	}
	// The refused request is still PENDING: a refusal decides nothing.
	var status string
	if err := q.pool.QueryRow(context.Background(),
		`SELECT status FROM permission_approval_requests WHERE id = $1`, credit.ID).Scan(&status); err != nil {
		t.Fatalf("read the refused request back: %v", err)
	}
	if status != string(identity.ApprovalPending) {
		t.Fatalf("the refused credit request is %s, want PENDING", status)
	}

	// Four eyes over the widened route: the General Manager files a credit
	// request of its own and cannot decide it.
	code, body = qd1bCall(t, q, http.MethodPost, "/api/v1/staff/approvals", testkit.SubGeneralMgr,
		map[string]any{
			"branch_id":   testkit.BranchNorthAID,
			"policy_type": "COD_CONSTRAINT",
			"detail":      "qd1b fixture: the General Manager's own request",
		})
	if code != http.StatusCreated {
		t.Fatalf("the General Manager files its own request: %d %s", code, body)
	}
	var own struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &own); err != nil {
		t.Fatalf("decode the General Manager's request: %v", err)
	}
	t.Cleanup(func() {
		_, _ = q.pool.Exec(context.Background(),
			`DELETE FROM permission_approval_requests WHERE id = $1`, own.ID)
	})
	code, body = qd1bCall(t, q, http.MethodPost, qd1bDecidePath(own.ID), testkit.SubGeneralMgr,
		map[string]any{"approve": true})
	if code != http.StatusForbidden || body == string(authz.BodyForbidden) {
		t.Fatalf("the General Manager decides its own request: %d %s, want the service's four eyes refusal, not the gate's",
			code, body)
	}
}

// TestQD1B_DecideRouteReachIsExactlyTheConfiguredSet pins the widening's
// blast radius over the whole decided default, planted roles and
// unplanted ones together: the roles whose rows reach the decide route
// are exactly the three the configuration holds at Manage or Admin on one
// of its two surfaces, and no cell edit hid inside the change. The x-role
// suite observes the same rule live for every planted leg; this half
// covers the roles no probe leg plants.
func TestQD1B_DecideRouteReachIsExactlyTheConfiguredSet(t *testing.T) {
	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatalf("role configuration: %v", err)
	}
	reached := map[string]bool{}
	for _, cell := range cfg.Cells() {
		for _, surface := range []string{"manager-home", "quote-desk"} {
			if cell.Surface != surface {
				continue
			}
			if level := cell.Level; level == roleconfig.LevelManage || level == roleconfig.LevelAdmin {
				reached[cell.Role] = true
			}
		}
	}
	want := map[string]bool{"financial_controller": true, "general_manager": true, "sales_manager": true}
	if len(reached) != len(want) {
		t.Fatalf("the decide route's reach widened past the three configured roles: %v", reached)
	}
	for role := range want {
		if !reached[role] {
			t.Fatalf("the decide route lost the reach of %s: %v", role, reached)
		}
	}
}
