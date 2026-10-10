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
	"strings"
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
	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatalf("role configuration: %v", err)
	}
	return newQD1BFixtureWith(t, cfg)
}

// newQD1BFixtureWith is newQD1BFixture over a given role configuration, the
// floor approver ruling's setting on and off legs.
func newQD1BFixtureWith(t *testing.T, cfg *roleconfig.Config) *qd1bFixture {
	t.Helper()
	env := newCompEnv(t)
	rec := &recordingRegistrar{inner: http.NewServeMux()}
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

// qd1bPlantUnboundMargin plants one LEGACY margin card: a PENDING
// MARGIN_BELOW_MINIMUM row with no line_id in its data and a subject and
// detail of the filer's choosing, the shape a live database carries from
// before the quote desk's floor door bound margin filings to a line. No
// door files one anymore (the create route and the sync door both refuse
// margin), so the row is planted the way the ones already sitting there
// sit, and it is cleaned by id.
func qd1bPlantUnboundMargin(t *testing.T, pool *pgxpool.Pool, requester, branchID string) string {
	t.Helper()
	var id string
	// The seq allocation mirrors the approvalsSeqCTE every rail door runs:
	// the sync projection scans the seq column, so a hand-planted row
	// without one breaks the door's load rather than testing its rule.
	if err := pool.QueryRow(context.Background(), `
		WITH aseq AS (
			INSERT INTO scope_sequences (scope) VALUES ('approvals')
			ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
			RETURNING next_value
		)
		INSERT INTO permission_approval_requests (requested_by, branch_id, policy_type, detail, subject_ref, status, seq)
		VALUES ($1::uuid, $2::uuid, 'MARGIN_BELOW_MINIMUM', 'a legacy margin card, no line binding',
		        'a subject of the filers choosing', 'PENDING', (SELECT next_value FROM aseq))
		RETURNING id::text`, requester, branchID).Scan(&id); err != nil {
		t.Fatalf("plant the legacy margin card: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM permission_approval_requests WHERE id = $1`, id)
	})
	return id
}

// TestQD1B_UnboundMarginCardStaysRefusedToTheSalesManager is P2-C's route
// leg: a margin card filed before the floor door bound margin filings to a
// line carries no line_id, a subject and detail of the filer's choosing,
// and the branch Sales manager's widened reach must not decide it. The
// dealer wide floor set keeps the card: the Financial Controller's reach
// is unchanged by the branch rule.
func TestQD1B_UnboundMarginCardStaysRefusedToTheSalesManager(t *testing.T) {
	q := newQD1BFixture(t)
	clerk := qd1bUserID(t, q.pool, testkit.SubInsideSales)

	legacy := qd1bPlantUnboundMargin(t, q.pool, clerk, testkit.BranchNorthAID)
	code, body := qd1bCall(t, q, http.MethodPost, qd1bDecidePath(legacy), testkit.SubSalesManager,
		map[string]any{"approve": true})
	if code != http.StatusForbidden || body == string(authz.BodyForbidden) {
		t.Fatalf("the Sales manager decides an unbound margin card over the widened route: %d %s, want the service's refusal, not the gate's",
			code, body)
	}
	if !strings.Contains(body, "no line binding") {
		t.Fatalf("the refusal must name the line-bound rule, got %s", body)
	}
	// The refused decision decided nothing: the card stays PENDING.
	var status string
	if err := q.pool.QueryRow(context.Background(),
		`SELECT status FROM permission_approval_requests WHERE id = $1`, legacy).Scan(&status); err != nil {
		t.Fatalf("read the legacy card back: %v", err)
	}
	if status != string(identity.ApprovalPending) {
		t.Fatalf("the refused legacy card is %s, want PENDING", status)
	}

	// The dealer wide floor set keeps the card.
	wide := qd1bPlantUnboundMargin(t, q.pool, clerk, testkit.BranchNorthAID)
	code, body = qd1bCall(t, q, http.MethodPost, qd1bDecidePath(wide), xrSubFinancialController,
		map[string]any{"approve": true})
	if code != http.StatusOK {
		t.Fatalf("the Financial Controller keeps the dealer wide floor authority over an unbound card: %d %s", code, body)
	}
}

// TestQD1B_SalesManagerRefusedTheirOwnFloorRequestOverTheRoute is four
// eyes by request for the Sales manager (the review's P3): the manager
// reaches the decide route and holds the branch arm for the request's
// branch, so the only thing refusing the decision is that they filed the
// request. The service's refusal, not the gate's, and the card stays
// PENDING.
func TestQD1B_SalesManagerRefusedTheirOwnFloorRequestOverTheRoute(t *testing.T) {
	q := newQD1BFixture(t)
	manager := qd1bUserID(t, q.pool, testkit.SubSalesManager)
	own := qd1bPlantFloor(t, q, manager, testkit.BranchNorthAID, "000000000b09")

	code, body := qd1bCall(t, q, http.MethodPost, qd1bDecidePath(own), testkit.SubSalesManager,
		map[string]any{"approve": true})
	if code != http.StatusForbidden || body == string(authz.BodyForbidden) {
		t.Fatalf("the Sales manager decides their own floor request: %d %s, want the service's refusal, not the gate's", code, body)
	}
	if !strings.Contains(body, "separation of duties") {
		t.Fatalf("the refusal must be the four eyes rule's, got %s", body)
	}
	var status string
	if err := q.pool.QueryRow(context.Background(),
		`SELECT status FROM permission_approval_requests WHERE id = $1`, own).Scan(&status); err != nil {
		t.Fatalf("read the request back: %v", err)
	}
	if status != string(identity.ApprovalPending) {
		t.Fatalf("the refused request is %s, want PENDING", status)
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

// TestQD1B_SalesManagerRefusedTheDealerWideMultiAcceptSwitch is the CP0-5
// route test for the dealer wide switch (the review's P3): the gate lets
// the Sales manager REACH the write (the gate sits at read so the General
// Manager does), so the rule runs in the service, in words, and the Sales
// manager is refused there, not at the gate. Reverting SetAllowMultiAccept
// to settingsAdmin lets the Sales manager flip what every branch reads, and
// this test goes red.
func TestQD1B_SalesManagerRefusedTheDealerWideMultiAcceptSwitch(t *testing.T) {
	q := newQD1BFixture(t)
	const path = "/api/v1/quotes/settings/acceptance"

	read := func(sub string) string {
		t.Helper()
		code, body := qd1bCall(t, q, http.MethodGet, path, sub, nil)
		if code != http.StatusOK {
			t.Fatalf("read the switch as %s: %d %s", sub, code, body)
		}
		return body
	}
	before := read(testkit.SubGeneralMgr)

	code, body := qd1bCall(t, q, http.MethodPut, path, testkit.SubSalesManager,
		map[string]any{"allow_multi_accept": true})
	if code != http.StatusForbidden || body == string(authz.BodyForbidden) {
		t.Fatalf("the Sales manager flips the dealer wide switch: %d %s, want the service's refusal, not the gate's", code, body)
	}
	if !strings.Contains(body, "only the general manager changes the dealer default quote settings") {
		t.Fatalf("the refusal must be the service's words naming who may act, got %s", body)
	}
	if strings.Contains(body, "SALES_MANAGER") || strings.Contains(strings.ToLower(body), "tech admin") {
		t.Fatalf("the refusal must carry no role code and no tech admin words, got %s", body)
	}
	if after := read(testkit.SubGeneralMgr); after != before {
		t.Fatalf("a refused flip changed the switch: %s then %s", before, after)
	}
}

// TestQD1B_SettingsGrantMatchesTheRefusingRoutesPerRole is round 14 (a)'s
// per role table over the served surface: for each role the grant read's three
// answers equal what the refusing route itself answers for the same caller.
// The acceptance read and the scope rules read are real GETs (admitted is a
// 200, refused a 403); the policy write is probed with a body the validator
// rejects (a 400 means the role gate admitted the caller, a 403 means it
// refused), so nothing is written. The policy write has two rows, the dealer
// default (the General Manager alone) and a branch row (a Manage caller in a
// branch it reaches), so can_edit_policy must equal "either row admits".
func TestQD1B_SettingsGrantMatchesTheRefusingRoutesPerRole(t *testing.T) {
	q := newQD1BFixture(t)
	tooLong := strings.Repeat("x", 4096)
	for _, tc := range []struct {
		name                            string
		sub                             string
		acceptance, scopeRules, editing bool
	}{
		{"inside sales", testkit.SubInsideSales, false, false, false},
		{"sales manager", testkit.SubSalesManager, true, false, true},
		{"general manager", testkit.SubGeneralMgr, true, true, true},
		{"branch manager", xrSubBranchManager, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := qd1bCall(t, q, http.MethodGet, "/api/v1/quotes/settings-grant", tc.sub, nil)
			if code != http.StatusOK {
				t.Fatalf("the grant read: %d %s", code, body)
			}
			var grant struct {
				CanReadAcceptance bool `json:"can_read_acceptance"`
				CanReadScopeRules bool `json:"can_read_scope_rules"`
				CanEditPolicy     bool `json:"can_edit_policy"`
			}
			if err := json.Unmarshal([]byte(body), &grant); err != nil {
				t.Fatalf("grant body %q: %v", body, err)
			}
			if grant.CanReadAcceptance != tc.acceptance || grant.CanReadScopeRules != tc.scopeRules || grant.CanEditPolicy != tc.editing {
				t.Fatalf("the grant for %s = %+v, want acceptance %v scope rules %v policy edit %v",
					tc.name, grant, tc.acceptance, tc.scopeRules, tc.editing)
			}

			admits := func(method, path string, payload any, admitted int) bool {
				t.Helper()
				c, b := qd1bCall(t, q, method, path, tc.sub, payload)
				switch c {
				case admitted:
					return true
				case http.StatusForbidden:
					return false
				}
				t.Fatalf("%s %s as %s answered %d %s, neither %d nor the 403 refusal", method, path, tc.name, c, b, admitted)
				return false
			}
			if got := admits(http.MethodGet, "/api/v1/quotes/settings/acceptance", nil, http.StatusOK); got != grant.CanReadAcceptance {
				t.Errorf("can_read_acceptance %v but the acceptance route admits %v", grant.CanReadAcceptance, got)
			}
			if got := admits(http.MethodGet, "/api/v1/quote-assist/scope-rules", nil, http.StatusOK); got != grant.CanReadScopeRules {
				t.Errorf("can_read_scope_rules %v but the scope rules route admits %v", grant.CanReadScopeRules, got)
			}
			dealer := admits(http.MethodPut, "/api/v1/quotes/policy", map[string]any{"quantity_notice": tooLong}, http.StatusBadRequest)
			branch := admits(http.MethodPut, "/api/v1/quotes/policy",
				map[string]any{"branch_id": testkit.BranchNorthAID, "quantity_notice": tooLong}, http.StatusBadRequest)
			if got := dealer || branch; got != grant.CanEditPolicy {
				t.Errorf("can_edit_policy %v but the policy write admits dealer %v branch %v", grant.CanEditPolicy, dealer, branch)
			}
		})
	}
}

// TestQD1B_SettingsGrantRoleWithoutTheDeskIsRefusedAtTheGate: the Financial
// Controller holds no cell on the Quote Desk, so the grant read is refused at
// the gate with the other desk reads, before the service answers. The unit
// table's Financial Controller row (scope rules true) is the service's answer
// to a caller the gate never lets through, and the desk never asks it.
func TestQD1B_SettingsGrantRoleWithoutTheDeskIsRefusedAtTheGate(t *testing.T) {
	q := newQD1BFixture(t)
	for _, path := range []string{"/api/v1/quotes/settings-grant", "/api/v1/quotes/settings/acceptance"} {
		code, body := qd1bCall(t, q, http.MethodGet, path, xrSubFinancialController, nil)
		if code != http.StatusForbidden || body != string(authz.BodyForbidden) {
			t.Fatalf("%s as the financial controller: %d %s, want the gate's 403", path, code, body)
		}
	}
}

// TestQD1B_PolicyReadAnswersTheChipSetAsAnArray is the served surface leg of round 14 (a)'s
// qs-policy-unread: the policy read as Inside Sales answers 200, and quick_chip_set is an array
// (empty when no set was authored), never null, as the contract types it.
func TestQD1B_PolicyReadAnswersTheChipSetAsAnArray(t *testing.T) {
	q := newQD1BFixture(t)
	for _, branch := range []string{"", "?branch_id=" + testkit.BranchNorthAID} {
		code, body := qd1bCall(t, q, http.MethodGet, "/api/v1/quotes/policy"+branch, testkit.SubInsideSales, nil)
		if code != http.StatusOK {
			t.Fatalf("the policy read as Inside Sales%s: %d %s", branch, code, body)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(body), &raw); err != nil {
			t.Fatalf("policy body %q: %v", body, err)
		}
		if got := strings.TrimSpace(string(raw["quick_chip_set"])); !strings.HasPrefix(got, "[") {
			t.Fatalf("quick_chip_set%s = %s, want an array", branch, got)
		}
	}
}
