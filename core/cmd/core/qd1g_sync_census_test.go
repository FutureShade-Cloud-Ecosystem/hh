package main

// qd1g_sync_census_test.go is the sync door census (round 4 part B, decision
// row 745): ONE table driven test over every method of both sync stores (the
// quotes store and the approvals store) and every role of the thirteen in the
// dealer's configuration, asserting the same allow or refuse as the REST route
// the method must match, both driven through the real registered mux and
// gates as the same caller.
//
// The census cannot go stale quietly:
//   - the methods are ENUMERATED by reflection over both store types, and the
//     sync core's Store interface; an exported method with no row in the table
//     fails the test naming it, so a door added later fails until it is ruled;
//   - the roles are read from the role configuration; a fourteenth role fails
//     until the census plants a caller for it;
//   - the rows must equal the table in core/internal/quote/SYNC-DOOR-CENSUS.md,
//     which a reviewer reads, in both directions.
//
// Where a sync method has no REST twin (a batch door, the withdraw, the note)
// its row states the REST rule it must match and the leg asserts that rule
// explicitly, per role.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	syncpkg "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// qd1gRole is one of the thirteen roles and the planted principal that holds
// exactly it (at the migration planted branch A, or dealer wide for the roles
// the matrix plants that way).
type qd1gRole struct {
	role string
	sub  string
	user string // the linked ERP users.id, filled when planted
}

// qd1gRoleSubs maps each canonical role to a principal. The first eight are
// the matrix principals other suites plant; the rest the census plants.
func qd1gRoleSubs() []qd1gRole {
	return []qd1gRole{
		{role: "inside_sales", sub: testkit.SubInsideSales},
		{role: "sales_manager", sub: testkit.SubSalesManager},
		{role: "outside_sales", sub: "fxstaff-qd1g-outside_sales"},
		{role: "yard_team", sub: xrSubYardTeam},
		{role: "yard_manager", sub: "fxstaff-qd1g-yard_manager"},
		{role: "logistics_manager", sub: "fxstaff-qd1g-logistics_manager"},
		{role: "driver", sub: "fxstaff-qd1g-driver"},
		{role: "branch_manager", sub: xrSubBranchManager},
		{role: "procurement_manager", sub: xrSubProcurementManager},
		{role: "payables_receivables", sub: "fxstaff-qd1g-payables_receivables"},
		{role: "financial_controller", sub: xrSubFinancialController},
		{role: "general_manager", sub: testkit.SubGeneralMgr},
		{role: "hr", sub: xrSubHR},
	}
}

// qd1gGlobalRoles are the roles whose scope is dealer wide.
var qd1gGlobalRoles = map[string]bool{
	"payables_receivables": true, "financial_controller": true, "general_manager": true, "hr": true,
}

// qd1gCensus is one census run's shared state.
type qd1gCensus struct {
	q      *qd1bFixture
	roles  []qd1gRole
	seller string // a plain seller no caller is: the owner of "someone else's" rows
	seq    int
}

func (c *qd1gCensus) next() int { c.seq++; return c.seq }

// qd1gPlantStaff plants the census principals the matrix does not, and one
// plain seller user, then resolves every role's linked user id.
func qd1gPlantStaff(t *testing.T, q *qd1bFixture) *qd1gCensus {
	t.Helper()
	ctx := context.Background()
	c := &qd1gCensus{q: q, roles: qd1gRoleSubs()}
	orgNorth := "org-" + testkit.OrgNorthSlug
	plant := func(sub, role string) {
		if _, err := q.pool.Exec(ctx, `
			INSERT INTO users (email, display_name, status, platform_user_id, org_id)
			VALUES ($1, $2, 'ACTIVE', $3, $4)
			ON CONFLICT (platform_user_id) WHERE platform_user_id IS NOT NULL DO UPDATE SET status = 'ACTIVE'`,
			sub+"@census.invalid", "Census "+role, sub, testkit.OrgNorthID); err != nil {
			t.Fatalf("plant the %s census user: %v", role, err)
		}
		if role == "" {
			return
		}
		// A dealer wide role is granted with no branch (the branch rule), a
		// branch role at branch A.
		var branch any = testkit.BranchNorthAID
		if qd1gGlobalRoles[role] {
			branch = nil
		}
		if _, err := q.pool.Exec(ctx, `
			INSERT INTO staff_role_grant (platform_user_id, role_id, org_id, branch_id)
			VALUES ($1, $2, $3, $4::uuid) ON CONFLICT DO NOTHING`,
			sub, role, testkit.OrgNorthID, branch); err != nil {
			t.Fatalf("plant the %s census grant: %v", role, err)
		}
		q.fx.env.appwrite.setTeam(sub, orgNorth)
		t.Cleanup(func() {
			_, _ = q.pool.Exec(context.Background(),
				`DELETE FROM staff_role_grant WHERE platform_user_id = $1 AND role_id = $2`, sub, role)
		})
	}
	for _, r := range c.roles {
		if strings.HasPrefix(r.sub, "fxstaff-qd1g-") {
			plant(r.sub, r.role)
		}
	}
	plant("fxstaff-qd1g-seller", "")
	t.Cleanup(func() {
		_, _ = q.pool.Exec(context.Background(),
			`DELETE FROM users WHERE platform_user_id LIKE 'fxstaff-qd1g-%'`)
	})
	c.seller = qd1bUserID(t, q.pool, "fxstaff-qd1g-seller")
	var branchUsers []string
	for i := range c.roles {
		c.roles[i].user = qd1bUserID(t, q.pool, c.roles[i].sub)
		if !qd1gGlobalRoles[c.roles[i].role] {
			branchUsers = append(branchUsers, c.roles[i].user)
		}
	}
	// A branch scoped caller reaches the branches of its user_branch_grants:
	// the testkit plants them for Inside Sales and the Sales manager only, so
	// every other branch role is given branch A for the census and loses it
	// when the census ends.
	if _, err := q.pool.Exec(ctx, `
		INSERT INTO user_branch_grants (user_id, branch_id, org_id)
		SELECT u, $2::uuid, $3::uuid FROM unnest($1::uuid[]) AS u
		ON CONFLICT DO NOTHING`, branchUsers, testkit.BranchNorthAID, testkit.OrgNorthID); err != nil {
		t.Fatalf("plant the census branch grants: %v", err)
	}
	t.Cleanup(func() {
		_, _ = q.pool.Exec(context.Background(), `
			DELETE FROM user_branch_grants
			 WHERE user_id = ANY($1::uuid[]) AND user_id NOT IN ($2::uuid, $3::uuid)`,
			branchUsers, testkit.UserInsideSalesID, testkit.UserSalesMgrID)
	})
	return c
}

// qd1gPlantsEveryRole asserts the census principals are exactly the roles of
// the dealer's configuration, each holding its own role.
func qd1gPlantsEveryRole(t *testing.T, c *qd1gCensus) {
	t.Helper()
	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatalf("role configuration: %v", err)
	}
	cfgRoles := map[string]bool{}
	for _, cell := range cfg.Cells() {
		cfgRoles[cell.Role] = true
	}
	if len(cfgRoles) != 13 {
		t.Errorf("the configuration holds %d roles; the census was written for thirteen and must be extended", len(cfgRoles))
	}
	have := map[string]bool{}
	for _, r := range c.roles {
		have[r.role] = true
		if !cfgRoles[r.role] {
			t.Errorf("the census names role %q, which the configuration does not hold", r.role)
		}
		var got string
		if err := c.q.pool.QueryRow(context.Background(),
			`SELECT string_agg(role_id, ',') FROM staff_role_grant WHERE platform_user_id = $1 AND deleted_at IS NULL`, r.sub).Scan(&got); err != nil {
			t.Fatalf("read the grants of %s: %v", r.sub, err)
		}
		if !strings.Contains(got, r.role) {
			t.Errorf("the census principal %s holds %q, not %s", r.sub, got, r.role)
		}
	}
	for role := range cfgRoles {
		if !have[role] {
			t.Errorf("the configuration holds role %q and the census has no caller for it: plant one and rule every door for it", role)
		}
	}
}

// qd1gRestAllowed folds a REST answer to allow or refuse: a 2xx admits, a 4xx
// refuses, anything else is a fault in the census itself.
func qd1gRestAllowed(t *testing.T, label string, code int, body string) bool {
	t.Helper()
	switch {
	case code >= 200 && code < 300:
		return true
	case code >= 400 && code < 500:
		return false
	}
	t.Fatalf("%s: REST answered %d %s", label, code, body)
	return false
}

// qd1gFenceAdmits reports whether the sync write fence lists the role as a
// writer of the scope (the registry is the oracle; the pinned table in the
// sync package holds the literal lists).
func qd1gFenceAdmits(scope, role string) bool {
	info, ok := syncpkg.Lookup(scope)
	return ok && info.AllowsRole(role)
}

// qd1gFencedReplay settles a replay answer that may be the fence's 403: a
// role off the scope's writer list is refused before any replay lookup (true,
// skip the leg), but a refusal of a listed writer is a defect and fails.
func qd1gFencedReplay(t *testing.T, scope string, who qd1gRole, code int, body string) bool {
	t.Helper()
	if !qd1bFenced(code, body) {
		return false
	}
	if qd1gFenceAdmits(scope, who.role) {
		t.Fatalf("the fence refused %s, a listed writer of %s: %s", who.role, scope, body)
	}
	return true
}

// qd1gPushOne drives one pushed operation through the registered sync route
// as one principal and folds the answer to allow or refuse (an applied result
// admits, a rejected one refuses, a conflict is a fault in the census).
func qd1gPushOne(t *testing.T, q *qd1bFixture, scope, sub, op string, payload map[string]any, baseSeq *int) (bool, string) {
	t.Helper()
	item := map[string]any{"client_id": uuid.NewString(), "op": op, "payload": payload}
	if baseSeq != nil {
		item["base_seq"] = *baseSeq
	}
	code, body := qd1bCall(t, q, http.MethodPost, "/api/v1/sync/"+scope, sub, map[string]any{"ops": []any{item}})
	if qd1bFenced(code, body) {
		return false, body // the sync write fence refused the role: a refusal
	}
	if code != http.StatusOK {
		t.Fatalf("push %s %s as %s: %d %s", scope, op, sub, code, body)
	}
	var resp struct {
		Results []qd1bPushResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil || len(resp.Results) != 1 {
		t.Fatalf("decode the %s push answer: %v (%s)", scope, err, body)
	}
	switch resp.Results[0].Status {
	case "applied":
		return true, body
	case "rejected":
		return false, body
	}
	t.Fatalf("push %s %s as %s answered %q: %s", scope, op, sub, resp.Results[0].Status, body)
	return false, body
}

// qd1gAnswer is one asked question and its answers, kept so the census can
// prove it was not vacuous (every door both admits and refuses somebody) and
// can write the document's matrices (QD1G_DUMP names the file).
type qd1gAnswer struct {
	Door string `json:"door"`
	Role string `json:"role"`
	Leg  string `json:"leg"`
	Rest bool   `json:"rest"`
	Sync bool   `json:"sync"`
	Rule bool   `json:"rule"` // true: no REST twin, Rest is the rule's answer
}

var qd1gAnswers []qd1gAnswer

// qd1gSame is the census assertion: both doors answered alike.
func qd1gSame(t *testing.T, door string, who qd1gRole, leg string, rest, syncs bool) {
	t.Helper()
	qd1gAnswers = append(qd1gAnswers, qd1gAnswer{door, who.role, leg, rest, syncs, false})
	verb := map[bool]string{true: "allows", false: "refuses"}
	if rest != syncs {
		t.Errorf("CENSUS MISMATCH %s as %s (%s): REST %s but the sync door %s", door, who.role, leg, verb[rest], verb[syncs])
	}
}

// qd1gMust asserts a ruled outcome where the row has no REST twin.
func qd1gMust(t *testing.T, door string, who qd1gRole, leg string, got, want bool) {
	t.Helper()
	qd1gAnswers = append(qd1gAnswers, qd1gAnswer{door, who.role, leg, want, got, true})
	verb := map[bool]string{true: "allows", false: "refuses"}
	if got != want {
		t.Errorf("CENSUS RULE %s as %s (%s): the sync door %s, the rule is that it %s", door, who.role, leg, verb[got], verb[want])
	}
}

// ---- quotes plants -------------------------------------------------------

// qd1gQuote plants one quote of the north org, owned by the salesperson, with
// a real seq (so the pull sees it) and an optional idempotency key.
func qd1gQuote(t *testing.T, c *qd1gCensus, branchID, status, owner, key string) (id, no string) {
	t.Helper()
	if err := c.q.pool.QueryRow(context.Background(), `
		WITH qseq AS (
			INSERT INTO scope_sequences (scope) VALUES ('quotes')
			ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
			RETURNING next_value
		)
		INSERT INTO quotes (quote_no, customer_id, branch_id, org_id, status, valid_until,
		                    subtotal_cents, tax_cents, total_cents, salesperson_id, created_by, seq, idempotency_key)
		VALUES ('#Q-QD1G-' || nextval('quote_no_seq'), $1::uuid, $2::uuid, $3::uuid, $4, $6::date,
		        2000, 0, 2000, $5::uuid, $5::uuid, (SELECT next_value FROM qseq), NULLIF($7,'')::uuid)
		RETURNING id::text, quote_no`,
		testkit.TenantAlphaID, branchID, testkit.OrgNorthID, status, owner, qd1cStanding(), key).Scan(&id, &no); err != nil {
		t.Fatalf("plant a census quote: %v", err)
	}
	t.Cleanup(func() { _, _ = c.q.pool.Exec(context.Background(), `DELETE FROM quotes WHERE id = $1`, id) })
	return id, no
}

// qd1gQuoteCase is one planted-quote shape the ladder is asked about.
type qd1gQuoteCase struct {
	name   string
	branch string
	own    bool // owned by the caller, else by the plain seller
}

var qd1gQuoteCases = []qd1gQuoteCase{
	{"own quote in branch A", testkit.BranchNorthAID, true},
	{"another seller's quote in branch A", testkit.BranchNorthAID, false},
	{"own quote in branch B", testkit.BranchNorthBID, true},
	{"another seller's quote in branch B", testkit.BranchNorthBID, false},
}

func (c *qd1gCensus) ownerFor(who qd1gRole, own bool) string {
	if own {
		return who.user
	}
	return c.seller
}

// qd1gRestQuoteList answers whether REST's list shows each planted quote. It
// follows next_cursor to the end, so a long lived gate with many quotes
// cannot push a planted one off the first page.
func qd1gRestQuoteList(t *testing.T, c *qd1gCensus, who qd1gRole, ids []string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	var all strings.Builder
	cursor := ""
	for page := 0; ; page++ {
		path := "/api/v1/quotes?limit=200"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		code, body := qd1bCall(t, c.q, http.MethodGet, path, who.sub, nil)
		if code == http.StatusForbidden {
			return out
		}
		if code != http.StatusOK {
			t.Fatalf("REST quote list as %s: %d %s", who.role, code, body)
		}
		all.WriteString(body)
		var env struct {
			Next *string `json:"next_cursor"`
		}
		if err := json.Unmarshal([]byte(body), &env); err != nil {
			t.Fatalf("decode the REST quote page as %s: %v", who.role, err)
		}
		if env.Next == nil || *env.Next == "" {
			break
		}
		if page > 1000 {
			t.Fatalf("REST quote list as %s did not end", who.role)
		}
		cursor = *env.Next
	}
	for _, id := range ids {
		out[id] = strings.Contains(all.String(), id)
	}
	return out
}

// ---- approvals plants ----------------------------------------------------

// qd1gPlain files one plain (credit) request through the rail.
func qd1gPlain(t *testing.T, c *qd1gCensus, requester, branchID string) (id, marker string) {
	t.Helper()
	marker = "qd1g plain " + uuid.NewString()
	rec, err := identity.NewApprovals(identity.NewApprovalRepository(c.q.pool)).
		CreateApproval(context.Background(), requester, branchID, identity.PolicyCredit, marker, "")
	if err != nil {
		t.Fatalf("plant a plain request: %v", err)
	}
	t.Cleanup(func() {
		_, _ = c.q.pool.Exec(context.Background(), `DELETE FROM permission_approval_requests WHERE id = $1`, rec.ID)
	})
	return rec.ID, marker
}

// qd1gFloor files one pending floor request through the rail's floor door,
// carrying a figure the margin card holds.
func qd1gFloor(t *testing.T, c *qd1gCensus, requester, branchID string) (id, marker string) {
	t.Helper()
	id = qd1bPlantFloor(t, c.q, requester, branchID, fmt.Sprintf("0000007%05x", c.next()))
	marker = "qd1g bps against the floor " + uuid.NewString()
	if _, err := c.q.pool.Exec(context.Background(),
		`UPDATE permission_approval_requests SET detail = $2 WHERE id = $1`, id, marker); err != nil {
		t.Fatalf("mark the floor request: %v", err)
	}
	return id, marker
}

// qd1gMove puts a planted request in the state the rail would leave it in.
func qd1gMove(t *testing.T, c *qd1gCensus, id, status string, used bool) {
	t.Helper()
	if _, err := c.q.pool.Exec(context.Background(), `
		UPDATE permission_approval_requests
		   SET status = $2, data = CASE WHEN $3 THEN data || '{"used": true}'::jsonb ELSE data END
		 WHERE id = $1`, id, status, used); err != nil {
		t.Fatalf("move the request to %s: %v", status, err)
	}
}

func qd1gTombstoned(t *testing.T, c *qd1gCensus, id string) bool {
	t.Helper()
	var deleted bool
	if err := c.q.pool.QueryRow(context.Background(),
		`SELECT deleted_at IS NOT NULL FROM permission_approval_requests WHERE id = $1`, id).Scan(&deleted); err != nil {
		t.Fatalf("read the request back: %v", err)
	}
	return deleted
}

// qd1gRestApprovalList answers whether REST's list shows each id, and whether
// the list route admitted the caller at all (its wall).
func qd1gRestApprovalList(t *testing.T, c *qd1gCensus, who qd1gRole, ids []string) (out map[string]bool, wall bool) {
	t.Helper()
	code, body := qd1bCall(t, c.q, http.MethodGet, "/api/v1/staff/approvals?limit=200", who.sub, nil)
	out = map[string]bool{}
	if code == http.StatusForbidden {
		return out, false
	}
	if code != http.StatusOK {
		t.Fatalf("REST approvals list as %s: %d %s", who.role, code, body)
	}
	for _, id := range ids {
		out[id] = strings.Contains(body, id)
	}
	return out, true
}

// qd1gSyncPull answers the bodies of one scope's pull, every page joined: it
// follows next_cursor (the resume is since=<cursor>) to the end.
func qd1gSyncPull(t *testing.T, c *qd1gCensus, scope string, who qd1gRole) string {
	t.Helper()
	var all strings.Builder
	since := "0"
	for page := 0; ; page++ {
		code, body := qd1bCall(t, c.q, http.MethodGet, "/api/v1/sync/"+scope+"?since="+since+"&limit=500", who.sub, nil)
		if code == http.StatusForbidden {
			return ""
		}
		if code != http.StatusOK {
			t.Fatalf("pull %s as %s: %d %s", scope, who.role, code, body)
		}
		all.WriteString(body)
		var env struct {
			Next *string `json:"next_cursor"`
		}
		if err := json.Unmarshal([]byte(body), &env); err != nil {
			t.Fatalf("decode the %s pull page as %s: %v", scope, who.role, err)
		}
		if env.Next == nil || *env.Next == "" {
			break
		}
		if page > 1000 {
			t.Fatalf("pull %s as %s did not end", scope, who.role)
		}
		since = *env.Next
	}
	return all.String()
}

// ---- the doors -----------------------------------------------------------

// qd1gDoor is one ruled row: the REST rule the method must match (the words
// the census document carries) and the leg that proves it for one role.
type qd1gDoor struct {
	rest string
	run  func(t *testing.T, c *qd1gCensus, who qd1gRole)
}

// qd1gNotDoor lists exported methods that are not doors (they take no caller
// and touch no row): a reason, not a leg.
var qd1gNotDoor = map[string]map[string]string{
	"quotes": {"WithNow": "pins the expiry clock of the store; takes no caller, reads and writes no row"},
	"approvals": {"WithJEDecider": "attaches the withdrawal bridge the store voids a withdrawn credit memo's park with; " +
		"takes no caller, reads and writes no row"},
}

func qd1gDoors() map[string]map[string]qd1gDoor {
	return map[string]map[string]qd1gDoor{
		"quotes": {
			"Create":            {"POST /api/v1/quotes", qd1gQuotesCreate},
			"Write":             {"PATCH /api/v1/quotes/{id}", qd1gQuotesWrite},
			"Delete":            {"the PATCH ladder, DRAFT only (no REST delete)", qd1gQuotesDelete},
			"Pull":              {"GET /api/v1/quotes (the list)", qd1gQuotesPull},
			"Load":              {"GET /api/v1/quotes (the list), by id at the conflict envelope", qd1gQuotesLoad},
			"FindApplied":       {"none: a replay answers status and seq only", qd1gQuotesFindApplied},
			"BoundEntityExists": {"none: the bound key gate's own existence read", qd1gQuotesBoundEntityExists},
			"ValidateCreate":    {"the POST /api/v1/quotes body's own validation (the refusal the route answers 400 with)", qd1gQuotesValidateCreate},
		},
		"approvals": {
			"Create":            {"POST /api/v1/staff/approvals", qd1gApprovalsCreate},
			"Write":             {"POST /api/v1/staff/approvals/{id}/decide, and the requester's note (no REST twin)", qd1gApprovalsWrite},
			"Delete":            {"the requester's own withdrawal unless consumed (no REST twin)", qd1gApprovalsDelete},
			"Pull":              {"GET /api/v1/staff/approvals (the list)", qd1gApprovalsPull},
			"Load":              {"GET /api/v1/staff/approvals (the list), by id at the conflict envelope", qd1gApprovalsLoad},
			"FindApplied":       {"none: a replay answers status and seq only", qd1gApprovalsFindApplied},
			"BoundEntityExists": {"none: the bound key gate's own existence read", qd1gApprovalsBoundEntityExists},
			"ValidateCreate":    {"the POST /api/v1/staff/approvals body's own validation (the refusal the route answers 400 with)", qd1gApprovalsValidateCreate},
		},
	}
}

func qd1gStores() map[string]reflect.Type {
	return map[string]reflect.Type{
		"quotes":    reflect.TypeOf((*quote.SyncStore)(nil)),
		"approvals": reflect.TypeOf((*identity.ApprovalsSyncStore)(nil)),
	}
}

// ---- quotes legs ---------------------------------------------------------

func qd1gQuotesCreate(t *testing.T, c *qd1gCensus, who qd1gRole) {
	for _, branch := range []struct{ name, id string }{{"branch A", testkit.BranchNorthAID}, {"branch B", testkit.BranchNorthBID}} {
		code, body := qd1bCall(t, c.q, http.MethodPost, "/api/v1/quotes", who.sub,
			map[string]any{"customer_id": testkit.TenantAlphaID, "branch_id": branch.id})
		rest := qd1gRestAllowed(t, "REST quote create", code, body)
		if rest {
			var made struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal([]byte(body), &made); err == nil && made.ID != "" {
				t.Cleanup(func() { _, _ = c.q.pool.Exec(context.Background(), `DELETE FROM quotes WHERE id = $1`, made.ID) })
			}
		}
		id := uuid.NewString()
		t.Cleanup(func() { _, _ = c.q.pool.Exec(context.Background(), `DELETE FROM quotes WHERE id = $1`, id) })
		syncs, _ := qd1gPushOne(t, c.q, "quotes", who.sub, "create", map[string]any{
			"id": id, "customer_id": testkit.TenantAlphaID, "branch_id": branch.id}, nil)
		qd1gSame(t, "quotes.Create", who, "create in "+branch.name, rest, syncs)
	}
}

func qd1gQuotesWrite(t *testing.T, c *qd1gCensus, who qd1gRole) {
	for _, k := range qd1gQuoteCases {
		owner := c.ownerFor(who, k.own)
		restID, _ := qd1gQuote(t, c, k.branch, "DRAFT", owner, "")
		syncID, _ := qd1gQuote(t, c, k.branch, "DRAFT", owner, "")
		code, body := qd1bCall(t, c.q, http.MethodPatch, "/api/v1/quotes/"+restID, who.sub,
			map[string]any{"valid_until": qd1cMoved()})
		rest := qd1gRestAllowed(t, "REST quote edit", code, body)
		syncs, _ := qd1gPushOne(t, c.q, "quotes", who.sub, "update",
			map[string]any{"id": syncID, "valid_until": qd1cMoved()}, nil)
		qd1gSame(t, "quotes.Write", who, "edit "+k.name, rest, syncs)
	}
}

// qd1gQuotesDelete: there is no REST delete of a quote. The rule the door
// must match is the edit ladder (withdrawing is an edit), asserted as the
// PATCH twin's answer on a DRAFT, and the status rule (only a DRAFT is
// withdrawn through sync) asserted for every role on a SENT quote it owns.
func qd1gQuotesDelete(t *testing.T, c *qd1gCensus, who qd1gRole) {
	for _, k := range qd1gQuoteCases {
		owner := c.ownerFor(who, k.own)
		restID, _ := qd1gQuote(t, c, k.branch, "DRAFT", owner, "")
		syncID, _ := qd1gQuote(t, c, k.branch, "DRAFT", owner, "")
		code, body := qd1bCall(t, c.q, http.MethodPatch, "/api/v1/quotes/"+restID, who.sub,
			map[string]any{"valid_until": qd1cMoved()})
		rest := qd1gRestAllowed(t, "REST quote edit (the ladder twin)", code, body)
		syncs, _ := qd1gPushOne(t, c.q, "quotes", who.sub, "delete", map[string]any{"id": syncID}, nil)
		qd1gSame(t, "quotes.Delete", who, "withdraw "+k.name, rest, syncs)
	}
	sent, _ := qd1gQuote(t, c, testkit.BranchNorthAID, "SENT", who.user, "")
	got, _ := qd1gPushOne(t, c.q, "quotes", who.sub, "delete", map[string]any{"id": sent}, nil)
	qd1gMust(t, "quotes.Delete", who, "withdraw its own SENT quote", got, false)
}

func qd1gQuotesPull(t *testing.T, c *qd1gCensus, who qd1gRole) {
	var ids []string
	for _, k := range qd1gQuoteCases {
		id, _ := qd1gQuote(t, c, k.branch, "SENT", c.ownerFor(who, k.own), "")
		ids = append(ids, id)
	}
	rest := qd1gRestQuoteList(t, c, who, ids)
	body := qd1gSyncPull(t, c, "quotes", who)
	for i, k := range qd1gQuoteCases {
		qd1gSame(t, "quotes.Pull", who, "read "+k.name, rest[ids[i]], strings.Contains(body, ids[i]))
	}
}

// qd1gLoad calls one store's Load directly as the caller (the engine reaches
// Load on a stale update, a delete and an update, but the quotes scope's
// field merge answers its envelope only after an authorised write, so the
// projection's answer is observable on the store itself). The store is built
// with the production linker's shape: platform subject to linked ERP user.
// The answer is the sync door's: the row is visible to the caller, found and
// carrying its projection. A row the caller may not read is not found at all
// (UX-108), so the answer is false, the absent row's answer.
func qd1gLoad(t *testing.T, c *qd1gCensus, scope string, who qd1gRole, id string) (visible bool) {
	t.Helper()
	link := func(ctx context.Context, sub string) (string, bool) {
		var uid string
		if err := c.q.pool.QueryRow(ctx,
			`SELECT id::text FROM users WHERE platform_user_id = $1`, sub).Scan(&uid); err != nil {
			return "", false
		}
		return uid, true
	}
	f := syncpkg.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: who.sub}
	var row syncpkg.RowState
	var found bool
	var err error
	switch scope {
	case "quotes":
		row, found, err = quote.NewSyncStore(c.q.pool, link).Load(context.Background(), f, id)
	default:
		row, found, err = identity.NewApprovalsSyncStore(c.q.pool, link).Load(context.Background(), f, id)
	}
	if err != nil {
		t.Fatalf("%s Load as %s: %v", scope, who.role, err)
	}
	return found && row.Data != nil
}

// qd1gQuotesLoad: Load's projection travels in the conflict envelope. It must
// carry the quote only when REST's LIST would show it to the caller. This is
// the one deliberate difference from REST GET by id (a Work caller reads any
// quote in its branch by id over REST); the sync door is a list door.
func qd1gQuotesLoad(t *testing.T, c *qd1gCensus, who qd1gRole) {
	var ids []string
	for _, k := range qd1gQuoteCases {
		id, _ := qd1gQuote(t, c, k.branch, "SENT", c.ownerFor(who, k.own), "")
		ids = append(ids, id)
	}
	rest := qd1gRestQuoteList(t, c, who, ids)
	for i, k := range qd1gQuoteCases {
		qd1gSame(t, "quotes.Load", who, "projection of "+k.name, rest[ids[i]], qd1gLoad(t, c, "quotes", who, ids[i]))
	}
}

// qd1gQuotesFindApplied: a replay of a key answers applied with the row's seq
// and no projection (the engine never reads Data on this path), for every
// role, and applies nothing: the rule is that no data leaves the door.
func qd1gQuotesFindApplied(t *testing.T, c *qd1gCensus, who qd1gRole) {
	for _, k := range qd1gQuoteCases {
		key := uuid.NewString()
		id, no := qd1gQuote(t, c, k.branch, "SENT", c.ownerFor(who, k.own), key)
		code, body := qd1bCall(t, c.q, http.MethodPost, "/api/v1/sync/quotes", who.sub, map[string]any{"ops": []any{
			map[string]any{"client_id": key, "op": "update", "payload": map[string]any{"id": id, "valid_until": qd1cMoved()}}}})
		if qd1gFencedReplay(t, "quotes", who, code, body) {
			continue // off the writer list: refused before any replay lookup
		}
		if code != http.StatusOK {
			t.Fatalf("replay as %s: %d %s", who.role, code, body)
		}
		if strings.Contains(body, no) || strings.Contains(body, "total_cents") {
			t.Errorf("CENSUS RULE quotes.FindApplied as %s (%s): the replay answer carries the quote: %s", who.role, k.name, body)
		}
		var validity string
		if err := c.q.pool.QueryRow(context.Background(),
			`SELECT COALESCE(valid_until::text,'') FROM quotes WHERE id = $1`, id).Scan(&validity); err != nil {
			t.Fatalf("read the quote back: %v", err)
		}
		if validity != qd1cStanding() {
			t.Errorf("CENSUS RULE quotes.FindApplied as %s (%s): the replay moved the quote's validity to %s", who.role, k.name, validity)
		}
	}
}

// qd1gQuotesValidateCreate rules the create body's shape door (UX-116): the
// engine's bound key gate asks it before the hidden refusal, so it answers
// the body's validation alone, the same refusal Create answers for the same
// body, with no caller wall in the question: a valid body is admitted for
// every caller and an invalid one refused for every caller.
func qd1gQuotesValidateCreate(t *testing.T, c *qd1gCensus, who qd1gRole) {
	store := quote.NewSyncStore(c.q.pool, nil)
	ctx := context.Background()
	f := syncpkg.ScopeFilter{Actor: who.sub}
	allows := func(label string, fields map[string]any) bool {
		t.Helper()
		err := store.ValidateCreate(ctx, f, fields)
		if err != nil && !errors.Is(err, syncpkg.ErrOpRefused) {
			t.Errorf("CENSUS RULE quotes.ValidateCreate as %s (%s): the refusal is not the module's classified one: %v", who.role, label, err)
		}
		return err == nil
	}
	ok := allows("a valid create body", map[string]any{
		"customer_id": testkit.TenantAlphaID, "branch_id": testkit.BranchNorthAID})
	qd1gMust(t, "quotes.ValidateCreate", who, "a valid create body", ok, true)
	qd1gMust(t, "quotes.ValidateCreate", who, "a field the create does not admit",
		allows("a field the create does not admit", map[string]any{
			"customer_id": testkit.TenantAlphaID, "branch_id": testkit.BranchNorthAID, "status": "SENT"}), false)
	qd1gMust(t, "quotes.ValidateCreate", who, "a malformed customer uuid",
		allows("a malformed customer uuid", map[string]any{
			"customer_id": "not-a-uuid", "branch_id": testkit.BranchNorthAID}), false)
}

// qd1gBoundExists asks one store's bound key existence read the way the
// engine's gate asks it: the caller's own filter, the row's id, and nothing
// else.
func qd1gBoundExists(t *testing.T, scope string, c *qd1gCensus, who qd1gRole, id string) bool {
	t.Helper()
	link := func(ctx context.Context, sub string) (string, bool) {
		var uid string
		if err := c.q.pool.QueryRow(ctx,
			`SELECT id::text FROM users WHERE platform_user_id = $1`, sub).Scan(&uid); err != nil {
			return "", false
		}
		return uid, true
	}
	f := syncpkg.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: who.sub}
	var present bool
	var err error
	switch scope {
	case "quotes":
		present, err = quote.NewSyncStore(c.q.pool, link).BoundEntityExists(context.Background(), f, id)
	default:
		present, err = identity.NewApprovalsSyncStore(c.q.pool, link).BoundEntityExists(context.Background(), f, id)
	}
	if err != nil {
		t.Fatalf("%s BoundEntityExists as %s: %v", scope, who.role, err)
	}
	return present
}

// qd1gQuotesBoundEntityExists: the bound key gate's own existence read. The
// rule it must match: the row stands for every caller of the org at once. The
// org predicate alone rides it, never the caller's read rung, so a quote the
// caller may not read still stands (the gate needs to tell a live row from a
// row that is gone); a withdrawn quote is gone for everyone; and a quote of
// another org is never named, so existence never crosses the fence.
func qd1gQuotesBoundEntityExists(t *testing.T, c *qd1gCensus, who qd1gRole) {
	// A live quote of the caller's org, owned by a seller the caller is not:
	// it stands, whatever the caller's read rung says.
	live, _ := qd1gQuote(t, c, testkit.BranchNorthAID, "SENT", c.seller, "")
	qd1gMust(t, "quotes.BoundEntityExists", who, "a live quote of the caller's org",
		qd1gBoundExists(t, "quotes", c, who, live), true)

	// The same quote withdrawn: it is gone for everyone.
	withdrawn, _ := qd1gQuote(t, c, testkit.BranchNorthAID, "SENT", c.seller, "")
	if _, err := c.q.pool.Exec(context.Background(),
		`UPDATE quotes SET deleted_at = now() WHERE id = $1`, withdrawn); err != nil {
		t.Fatalf("withdraw the planted quote: %v", err)
	}
	qd1gMust(t, "quotes.BoundEntityExists", who, "a withdrawn quote of the caller's org",
		qd1gBoundExists(t, "quotes", c, who, withdrawn), false)

	// A quote of another org: never named.
	other := qd1gQuoteOfAnotherOrg(t, c)
	qd1gMust(t, "quotes.BoundEntityExists", who, "a live quote of another org",
		qd1gBoundExists(t, "quotes", c, who, other), false)
}

// qd1gQuoteOfAnotherOrg plants one quote of the south org, so the existence
// read can be asked about a row outside the caller's org.
func qd1gQuoteOfAnotherOrg(t *testing.T, c *qd1gCensus) string {
	t.Helper()
	var id string
	if err := c.q.pool.QueryRow(context.Background(), `
		WITH qseq AS (
			INSERT INTO scope_sequences (scope) VALUES ('quotes')
			ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
			RETURNING next_value
		)
		INSERT INTO quotes (quote_no, customer_id, branch_id, org_id, status, subtotal_cents, tax_cents,
		                    total_cents, salesperson_id, created_by, seq, idempotency_key)
		VALUES ('#Q-QD1G-S-' || nextval('quote_no_seq'), $1::uuid, $2::uuid, $3::uuid, 'SENT',
		        2000, 0, 2000, $4::uuid, $4::uuid, (SELECT next_value FROM qseq), NULL)
		RETURNING id::text`,
		testkit.TenantGammaID, testkit.BranchSouthAID, testkit.OrgSouthID, c.seller).Scan(&id); err != nil {
		t.Fatalf("plant a quote of another org: %v", err)
	}
	t.Cleanup(func() { _, _ = c.q.pool.Exec(context.Background(), `DELETE FROM quotes WHERE id = $1`, id) })
	return id
}

// ---- approvals legs ------------------------------------------------------

func qd1gApprovalsCreate(t *testing.T, c *qd1gCensus, who qd1gRole) {
	for _, k := range []struct{ name, branch, policy string }{
		{"a credit filing in branch A", testkit.BranchNorthAID, "CREDIT_LIMIT_EXCEEDED"},
		{"a credit filing in branch B", testkit.BranchNorthBID, "CREDIT_LIMIT_EXCEEDED"},
		{"a margin filing (the quote desk's own)", testkit.BranchNorthAID, "MARGIN_BELOW_MINIMUM"},
	} {
		marker := "qd1g filing " + uuid.NewString()
		code, body := qd1bCall(t, c.q, http.MethodPost, "/api/v1/staff/approvals", who.sub,
			map[string]any{"branch_id": k.branch, "policy_type": k.policy, "detail": marker})
		rest := qd1gRestAllowed(t, "REST approval filing", code, body)
		syncs, _ := qd1gPushOne(t, c.q, "approvals", who.sub, "create",
			map[string]any{"branch_id": k.branch, "policy_type": k.policy, "detail": marker}, nil)
		_, _ = c.q.pool.Exec(context.Background(), `DELETE FROM permission_approval_requests WHERE detail = $1`, marker)
		qd1gSame(t, "approvals.Create", who, k.name, rest, syncs)
	}
}

// qd1gDecideCases are the requests a decision is asked of: who filed them and
// where decides who may decide them.
func qd1gDecideCases(t *testing.T, c *qd1gCensus, who qd1gRole) []struct {
	name  string
	plant func(t *testing.T) string
} {
	clerk := qd1bUserID(t, c.q.pool, testkit.SubInsideSales)
	branchMgr := qd1bUserID(t, c.q.pool, xrSubBranchManager)
	return []struct {
		name  string
		plant func(t *testing.T) string
	}{
		{"a credit request of branch A", func(t *testing.T) string { id, _ := qd1gPlain(t, c, c.seller, testkit.BranchNorthAID); return id }},
		{"a credit request of branch B", func(t *testing.T) string { id, _ := qd1gPlain(t, c, c.seller, testkit.BranchNorthBID); return id }},
		{"its own credit request", func(t *testing.T) string { id, _ := qd1gPlain(t, c, who.user, testkit.BranchNorthAID); return id }},
		{"a floor request Inside Sales filed in branch A", func(t *testing.T) string { id, _ := qd1gFloor(t, c, clerk, testkit.BranchNorthAID); return id }},
		{"a floor request Inside Sales filed in branch B", func(t *testing.T) string { id, _ := qd1gFloor(t, c, clerk, testkit.BranchNorthBID); return id }},
		{"a floor request a Branch manager filed in branch A", func(t *testing.T) string { id, _ := qd1gFloor(t, c, branchMgr, testkit.BranchNorthAID); return id }},
	}
}

func qd1gApprovalsWrite(t *testing.T, c *qd1gCensus, who qd1gRole) {
	// The decision, against the decide route.
	for _, k := range qd1gDecideCases(t, c, who) {
		restID, syncID := k.plant(t), k.plant(t)
		code, body := qd1bCall(t, c.q, http.MethodPost, qd1bDecidePath(restID), who.sub, map[string]any{"approve": true})
		rest := qd1gRestAllowed(t, "REST decide", code, body)
		syncs, _ := qd1gPushOne(t, c.q, "approvals", who.sub, "update",
			map[string]any{"id": syncID, "status": "APPROVED"}, nil)
		qd1gSame(t, "approvals.Write", who, "decide "+k.name, rest, syncs)
	}
	// The note has no REST twin; the rule is the requester's own edit of a
	// plain pending request, nothing else, whatever the role.
	cases := []struct {
		name string
		want bool
		make func(t *testing.T) string
	}{
		{"edit its own pending plain request's note", true, func(t *testing.T) string { id, _ := qd1gPlain(t, c, who.user, testkit.BranchNorthAID); return id }},
		{"edit another requester's pending note", false, func(t *testing.T) string { id, _ := qd1gPlain(t, c, c.seller, testkit.BranchNorthAID); return id }},
		{"edit its own decided request's note", false, func(t *testing.T) string {
			id, _ := qd1gPlain(t, c, who.user, testkit.BranchNorthAID)
			qd1gMove(t, c, id, "APPROVED", false)
			return id
		}},
		{"edit the detail of a floor request it filed", false, func(t *testing.T) string {
			id, _ := qd1gFloor(t, c, who.user, testkit.BranchNorthAID)
			return id
		}},
	}
	for _, k := range cases {
		id := k.make(t)
		got, _ := qd1gPushOne(t, c.q, "approvals", who.sub, "update",
			map[string]any{"id": id, "detail": "qd1g revised " + uuid.NewString()}, nil)
		// A requester demoted off every filing role is refused at the fence.
		qd1gMust(t, "approvals.Write", who, k.name, got, k.want && qd1gFenceAdmits("approvals", who.role))
	}
}

// qd1gApprovalsDelete: no REST twin. The rule is the requester's own
// withdrawal in any state but a consumed approval, and nobody else's, ever.
func qd1gApprovalsDelete(t *testing.T, c *qd1gCensus, who qd1gRole) {
	for _, st := range []struct {
		name   string
		status string
		used   bool
	}{{"pending", "PENDING", false}, {"approved-unused", "APPROVED", false}, {"rejected", "REJECTED", false}, {"consumed", "APPROVED", true}} {
		for _, mine := range []bool{true, false} {
			requester, label := c.seller, "another requester's "
			if mine {
				requester, label = who.user, "its own "
			}
			id, _ := qd1gPlain(t, c, requester, testkit.BranchNorthAID)
			qd1gMove(t, c, id, st.status, st.used)
			got, _ := qd1gPushOne(t, c.q, "approvals", who.sub, "delete", map[string]any{"id": id}, nil)
			want := mine && !st.used && qd1gFenceAdmits("approvals", who.role)
			qd1gMust(t, "approvals.Delete", who, "withdraw "+label+st.name+" request", got, want)
			if qd1gTombstoned(t, c, id) != want {
				t.Errorf("CENSUS RULE approvals.Delete as %s (%s%s): tombstone is %v, want %v", who.role, label, st.name, !want, want)
			}
		}
	}
}

// qd1gApprovalCasesRead are the requests a read is asked of.
func qd1gApprovalCasesRead(t *testing.T, c *qd1gCensus, who qd1gRole) (ids []string, markers map[string]string, names map[string]string) {
	t.Helper()
	clerk := qd1bUserID(t, c.q.pool, testkit.SubInsideSales)
	branchMgr := qd1bUserID(t, c.q.pool, xrSubBranchManager)
	markers, names = map[string]string{}, map[string]string{}
	add := func(name string, id, marker string) {
		ids = append(ids, id)
		markers[id] = marker
		names[id] = name
	}
	id, m := qd1gPlain(t, c, c.seller, testkit.BranchNorthAID)
	add("a plain request of branch A", id, m)
	id, m = qd1gPlain(t, c, c.seller, testkit.BranchNorthBID)
	add("a plain request of branch B", id, m)
	id, m = qd1gPlain(t, c, who.user, testkit.BranchNorthAID)
	add("its own plain request", id, m)
	id, m = qd1gFloor(t, c, clerk, testkit.BranchNorthAID)
	add("a floor request Inside Sales filed in branch A", id, m)
	id, m = qd1gFloor(t, c, clerk, testkit.BranchNorthBID)
	add("a floor request Inside Sales filed in branch B", id, m)
	id, m = qd1gFloor(t, c, branchMgr, testkit.BranchNorthAID)
	add("a floor request a Branch manager filed in branch A", id, m)
	id, m = qd1gFloor(t, c, who.user, testkit.BranchNorthAID)
	add("a floor request it filed itself", id, m)
	id, m = qd1gPlain(t, c, who.user, testkit.BranchNorthBID)
	add(qd1gOwnElsewhere, id, m)
	return ids, markers, names
}

// qd1gOwnElsewhere names the one read the sync door adds to the REST list: a
// caller's own filing in a branch it does not reach (the rail does not fence
// the filing branch by reach). It reads it back whenever the list route
// admits it at all.
const qd1gOwnElsewhere = "its own plain request filed in branch B"

// qd1gListWants is the sync answer the REST list implies for one request: the
// list's answer, except that the caller's own filing outside its reach is read
// whenever the list route admits the caller (the wall).
func qd1gListWants(t *testing.T, c *qd1gCensus, who qd1gRole, rest map[string]bool, wall bool, id string) bool {
	t.Helper()
	var requester, branch string
	if err := c.q.pool.QueryRow(context.Background(),
		`SELECT requested_by::text, branch_id::text FROM permission_approval_requests WHERE id = $1`, id).Scan(&requester, &branch); err != nil {
		t.Fatalf("read the request back: %v", err)
	}
	if requester == who.user && branch == testkit.BranchNorthBID {
		return wall
	}
	return rest[id]
}

func qd1gApprovalsPull(t *testing.T, c *qd1gCensus, who qd1gRole) {
	ids, _, names := qd1gApprovalCasesRead(t, c, who)
	rest, wall := qd1gRestApprovalList(t, c, who, ids)
	body := qd1gSyncPull(t, c, "approvals", who)
	for _, id := range ids {
		qd1gSame(t, "approvals.Pull", who, "read "+names[id], qd1gListWants(t, c, who, rest, wall, id), strings.Contains(body, id))
	}
}

func qd1gApprovalsLoad(t *testing.T, c *qd1gCensus, who qd1gRole) {
	ids, _, names := qd1gApprovalCasesRead(t, c, who)
	rest, wall := qd1gRestApprovalList(t, c, who, ids)
	for _, id := range ids {
		qd1gSame(t, "approvals.Load", who, "projection of "+names[id], qd1gListWants(t, c, who, rest, wall, id), qd1gLoad(t, c, "approvals", who, id))
	}
}

func qd1gApprovalsFindApplied(t *testing.T, c *qd1gCensus, who qd1gRole) {
	clerk := qd1bUserID(t, c.q.pool, testkit.SubInsideSales)
	for _, k := range []struct {
		name string
		make func(t *testing.T) (string, string)
	}{
		{"a plain request of branch A", func(t *testing.T) (string, string) { return qd1gPlain(t, c, c.seller, testkit.BranchNorthAID) }},
		{"a floor request Inside Sales filed", func(t *testing.T) (string, string) { return qd1gFloor(t, c, clerk, testkit.BranchNorthAID) }},
	} {
		id, marker := k.make(t)
		key := uuid.NewString()
		if _, err := c.q.pool.Exec(context.Background(),
			`UPDATE permission_approval_requests SET idempotency_key = $2::uuid WHERE id = $1`, id, key); err != nil {
			t.Fatalf("stamp the key: %v", err)
		}
		code, body := qd1bCall(t, c.q, http.MethodPost, "/api/v1/sync/approvals", who.sub, map[string]any{"ops": []any{
			map[string]any{"client_id": key, "op": "update", "payload": map[string]any{"id": id, "detail": "qd1g replay words"}}}})
		if qd1gFencedReplay(t, "approvals", who, code, body) {
			continue // off the writer list: refused before any replay lookup
		}
		if code != http.StatusOK {
			t.Fatalf("replay as %s: %d %s", who.role, code, body)
		}
		if strings.Contains(body, marker) {
			t.Errorf("CENSUS RULE approvals.FindApplied as %s (%s): the replay answer carries the request: %s", who.role, k.name, body)
		}
		var detail string
		if err := c.q.pool.QueryRow(context.Background(),
			`SELECT detail FROM permission_approval_requests WHERE id = $1`, id).Scan(&detail); err != nil {
			t.Fatalf("read the request back: %v", err)
		}
		if detail != marker {
			t.Errorf("CENSUS RULE approvals.FindApplied as %s (%s): the replay rewrote the request", who.role, k.name)
		}
	}
	// The dedup leg proper: a request the caller filed itself, plain, so the
	// replayed note edit would be ADMITTED if the lookup missed. The key is
	// already applied, so the replay must answer applied and rewrite nothing.
	// A FindApplied that always misses lets the edit through and fails here.
	id, marker := qd1gPlain(t, c, who.user, testkit.BranchNorthAID)
	key := uuid.NewString()
	if _, err := c.q.pool.Exec(context.Background(),
		`UPDATE permission_approval_requests SET idempotency_key = $2::uuid WHERE id = $1`, id, key); err != nil {
		t.Fatalf("stamp the key: %v", err)
	}
	code, body := qd1bCall(t, c.q, http.MethodPost, "/api/v1/sync/approvals", who.sub, map[string]any{"ops": []any{
		map[string]any{"client_id": key, "op": "update", "payload": map[string]any{"id": id, "detail": "qd1g dedup rewrite"}}}})
	if qd1gFencedReplay(t, "approvals", who, code, body) {
		return // a demoted requester: refused before the dedup lookup
	}
	if code != http.StatusOK {
		t.Fatalf("dedup replay as %s: %d %s", who.role, code, body)
	}
	var detail string
	if err := c.q.pool.QueryRow(context.Background(),
		`SELECT detail FROM permission_approval_requests WHERE id = $1`, id).Scan(&detail); err != nil {
		t.Fatalf("read the request back: %v", err)
	}
	if detail != marker {
		t.Errorf("CENSUS RULE approvals.FindApplied as %s (its own plain request, dedup): the replay of an applied key was applied again (detail is now %q)", who.role, detail)
	}
	var resp struct {
		Results []qd1bPushResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil || len(resp.Results) != 1 || resp.Results[0].Status != "applied" {
		t.Errorf("CENSUS RULE approvals.FindApplied as %s (its own plain request, dedup): the replay did not answer applied: %s", who.role, body)
	}
}

// qd1gApprovalsValidateCreate rules the filing body's shape door (UX-116):
// the engine's bound key gate asks it before the hidden refusal, so it
// answers the body's validation alone, the same refusal Create answers for
// the same body, with no caller wall in the question: a valid body is
// admitted for every caller and an invalid one refused for every caller.
func qd1gApprovalsValidateCreate(t *testing.T, c *qd1gCensus, who qd1gRole) {
	store := identity.NewApprovalsSyncStore(c.q.pool, nil)
	ctx := context.Background()
	f := syncpkg.ScopeFilter{Actor: who.sub}
	allows := func(label string, fields map[string]any) bool {
		t.Helper()
		err := store.ValidateCreate(ctx, f, fields)
		if err != nil && !errors.Is(err, syncpkg.ErrOpRefused) {
			t.Errorf("CENSUS RULE approvals.ValidateCreate as %s (%s): the refusal is not the module's classified one: %v", who.role, label, err)
		}
		return err == nil
	}
	ok := allows("a valid filing body", map[string]any{
		"branch_id": testkit.BranchNorthAID, "policy_type": "CREDIT_LIMIT_EXCEEDED", "detail": "qd1g validate probe"})
	qd1gMust(t, "approvals.ValidateCreate", who, "a valid filing body", ok, true)
	qd1gMust(t, "approvals.ValidateCreate", who, "a margin filing (the quote desk's own)",
		allows("a margin filing (the quote desk's own)", map[string]any{
			"branch_id": testkit.BranchNorthAID, "policy_type": "MARGIN_BELOW_MINIMUM", "detail": "qd1g validate probe"}), false)
	qd1gMust(t, "approvals.ValidateCreate", who, "a field the filing does not admit",
		allows("a field the filing does not admit", map[string]any{
			"branch_id": testkit.BranchNorthAID, "policy_type": "CREDIT_LIMIT_EXCEEDED",
			"detail": "qd1g validate probe", "decided_by": who.sub}), false)
}

// qd1gApprovalsBoundEntityExists: the bound key gate's own existence read.
// The rule it must match: the card stands for every caller of the org at
// once. The org alone rides it, the org riding the card's branch, never the
// pull's reader, so a card the caller may not read still stands (the gate
// needs to tell a live card from a card that is gone); a withdrawn card is
// gone for everyone; and a card of another org is never named, so existence
// never crosses the fence.
func qd1gApprovalsBoundEntityExists(t *testing.T, c *qd1gCensus, who qd1gRole) {
	// A live card of the caller's org, filed by a requester the caller is
	// not: it stands, whatever the pull's reader says.
	live, _ := qd1gPlain(t, c, c.seller, testkit.BranchNorthAID)
	qd1gMust(t, "approvals.BoundEntityExists", who, "a live card of the caller's org",
		qd1gBoundExists(t, "approvals", c, who, live), true)

	// The same card withdrawn: it is gone for everyone.
	withdrawn, _ := qd1gPlain(t, c, c.seller, testkit.BranchNorthAID)
	if _, err := c.q.pool.Exec(context.Background(),
		`UPDATE permission_approval_requests SET deleted_at = now() WHERE id = $1`, withdrawn); err != nil {
		t.Fatalf("withdraw the planted request: %v", err)
	}
	qd1gMust(t, "approvals.BoundEntityExists", who, "a withdrawn card of the caller's org",
		qd1gBoundExists(t, "approvals", c, who, withdrawn), false)

	// A card of another org: never named.
	other := qd1gPlainOfAnotherOrg(t, c)
	qd1gMust(t, "approvals.BoundEntityExists", who, "a live card of another org",
		qd1gBoundExists(t, "approvals", c, who, other), false)
}

// qd1gPlainOfAnotherOrg plants one request of the south org, so the existence
// read can be asked about a row outside the caller's org.
func qd1gPlainOfAnotherOrg(t *testing.T, c *qd1gCensus) string {
	t.Helper()
	marker := "qd1g south " + uuid.NewString()
	var id string
	if err := c.q.pool.QueryRow(context.Background(), `
		INSERT INTO permission_approval_requests
			(requested_by, branch_id, policy_type, detail, data, status, seq, idempotency_key)
		VALUES ($1::uuid, $2::uuid, 'CREDIT_LIMIT_EXCEEDED', $3, '{}'::jsonb, 'PENDING', $4, NULL)
		RETURNING id::text`,
		c.seller, testkit.BranchSouthAID, marker, c.next()).Scan(&id); err != nil {
		t.Fatalf("plant a card of another org: %v", err)
	}
	t.Cleanup(func() {
		_, _ = c.q.pool.Exec(context.Background(), `DELETE FROM permission_approval_requests WHERE id = $1`, id)
	})
	return id
}

// TestQD1G_ApprovalsFindAppliedAnswersOnlyForARequestTheCallerMayRead: the
// replay lookup answers for a request the caller may read only, the same
// reader Load uses. A card the caller may not read is not found at all
// (UX-108), so the engine never reads an unreadable row as visible; a masked
// found would have told it the card was there.
func TestQD1G_ApprovalsFindAppliedAnswersOnlyForARequestTheCallerMayRead(t *testing.T) {
	q := newQD1BFixture(t)
	c := qd1gPlantStaff(t, q)
	clerk := qd1bUserID(t, q.pool, testkit.SubInsideSales)
	id, _ := qd1gFloor(t, c, clerk, testkit.BranchNorthAID)
	key := uuid.NewString()
	if _, err := q.pool.Exec(context.Background(),
		`UPDATE permission_approval_requests SET idempotency_key = $2::uuid WHERE id = $1`, id, key); err != nil {
		t.Fatalf("stamp the key: %v", err)
	}
	link := func(ctx context.Context, sub string) (string, bool) {
		var uid string
		if err := q.pool.QueryRow(ctx, `SELECT id::text FROM users WHERE platform_user_id = $1`, sub).Scan(&uid); err != nil {
			return "", false
		}
		return uid, true
	}
	store := identity.NewApprovalsSyncStore(q.pool, link)
	var readers, hidden int
	for _, who := range c.roles {
		f := syncpkg.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: who.sub}
		row, found, err := store.FindApplied(context.Background(), f, key)
		if err != nil {
			t.Fatalf("FindApplied as %s: %v", who.role, err)
		}
		want := qd1gLoad(t, c, "approvals", who, id)
		if found != want {
			t.Errorf("approvals.FindApplied as %s: answers found %v, but Load (the reader) says %v", who.role, found, want)
		}
		if found && (row.Data != nil) != want {
			t.Errorf("approvals.FindApplied as %s: projection present %v, but Load (the reader) says %v", who.role, row.Data != nil, want)
		}
		if want {
			readers++
		} else {
			hidden++
		}
	}
	if readers == 0 || hidden == 0 {
		t.Errorf("the reader leg is vacuous: %d readers, %d hidden", readers, hidden)
	}
}

// ---- the census ----------------------------------------------------------

// qd1gDocRows reads the first column of every table row of the census
// document that names a door (`store.Method`).
func qd1gDocRows(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile("../../internal/quote/SYNC-DOOR-CENSUS.md")
	if err != nil {
		t.Fatalf("read the census document: %v", err)
	}
	rows := map[string]bool{}
	re := regexp.MustCompile("(?m)^\\| `((?:quotes|approvals)\\.[A-Za-z]+)` ")
	for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
		rows[m[1]] = true
	}
	return rows
}

// TestQD1G_SyncDoorCensus is the census: every method of both sync stores
// against every role of the thirteen, the sync answer equal to the REST
// answer of the matching route.
func TestQD1G_SyncDoorCensus(t *testing.T) {
	q := newQD1BFixture(t)
	c := qd1gPlantStaff(t, q)
	doors := qd1gDoors()
	qd1gAnswers = nil

	t.Run("the roles are the configuration's thirteen", func(t *testing.T) { qd1gPlantsEveryRole(t, c) })

	// Enumerate: every exported method of both stores, and every method of the
	// sync core's Store seam, must be ruled.
	enumerated := map[string]bool{}
	storeSeam := reflect.TypeOf((*syncpkg.Store)(nil)).Elem()
	for store, typ := range qd1gStores() {
		for i := 0; i < typ.NumMethod(); i++ {
			name := typ.Method(i).Name
			key := store + "." + name
			if reason, ok := qd1gNotDoor[store][name]; ok {
				if _, alsoDoor := doors[store][name]; alsoDoor {
					t.Errorf("sync census: %s is both ruled as a door and listed as not a door (%s)", key, reason)
				}
				continue
			}
			enumerated[key] = true
			if _, ok := doors[store][name]; !ok {
				t.Errorf("sync census: %s is a new exported method on the %s sync store with no ruled row: "+
					"rule it against the REST route it must match, add its row to the census table and to "+
					"core/internal/quote/SYNC-DOOR-CENSUS.md", key, store)
			}
		}
		for i := 0; i < storeSeam.NumMethod(); i++ {
			name := storeSeam.Method(i).Name
			if _, ok := doors[store][name]; !ok {
				t.Errorf("sync census: the sync core's Store seam has operation %s and the %s store has no ruled row for it", name, store)
			}
		}
		for name := range doors[store] {
			if _, ok := typ.MethodByName(name); !ok {
				t.Errorf("sync census: row %s.%s rules a method that no longer exists on the %s store", store, name, store)
			}
		}
	}

	// The document carries exactly the rows the table does.
	doc := qd1gDocRows(t)
	for key := range enumerated {
		if !doc[key] {
			t.Errorf("sync census: %s has no row in core/internal/quote/SYNC-DOOR-CENSUS.md", key)
		}
	}
	for key := range doc {
		if !enumerated[key] {
			t.Errorf("sync census: the document rules %s, which the stores no longer have", key)
		}
	}

	// Drive: every ruled door, every role.
	var stores []string
	for s := range doors {
		stores = append(stores, s)
	}
	sort.Strings(stores)
	for _, store := range stores {
		var methods []string
		for m := range doors[store] {
			methods = append(methods, m)
		}
		sort.Strings(methods)
		for _, method := range methods {
			door := doors[store][method]
			for _, who := range c.roles {
				t.Run(store+"."+method+"/"+who.role, func(t *testing.T) { door.run(t, c, who) })
			}
		}
	}
	qd1gNotVacuous(t)
	qd1gMatchMatrices(t)
	qd1gMatrixMutants(t)
	qd1gScopesGuard(t)
}

// qd1gNotVacuous runs after the census and requires that
// every ruled door asked its question of every role and that the rule it
// follows both admitted and refused somebody (a census whose every answer is
// "refuse" proves nothing about a door that should admit). It also writes the
// document's matrices when QD1G_DUMP names a file.
func qd1gNotVacuous(t *testing.T) {
	t.Helper()
	if t.Failed() {
		return
	}
	type tally struct {
		allow, refuse int
		roles         map[string]bool
	}
	by := map[string]*tally{}
	for _, a := range qd1gAnswers {
		x := by[a.Door]
		if x == nil {
			x = &tally{roles: map[string]bool{}}
			by[a.Door] = x
		}
		x.roles[a.Role] = true
		if a.Rest {
			x.allow++
		} else {
			x.refuse++
		}
	}
	for store, doors := range qd1gDoors() {
		for method := range doors {
			key := store + "." + method
			x := by[key]
			switch {
			case x == nil:
				// FindApplied asserts a rule with no allow or refuse answer.
				if method != "FindApplied" {
					t.Errorf("census %s asked no question", key)
				}
			case len(x.roles) != 13:
				t.Errorf("census %s asked %d roles, want 13", key, len(x.roles))
			case method != "FindApplied" && (x.allow == 0 || x.refuse == 0):
				t.Errorf("census %s is vacuous: %d allow answers, %d refuse answers", key, x.allow, x.refuse)
			}
		}
	}
	if path := os.Getenv("QD1G_DUMP"); path != "" {
		raw, err := json.MarshalIndent(qd1gAnswers, "", " ")
		if err != nil {
			t.Fatalf("encode the dump: %v", err)
		}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatalf("write the dump: %v", err)
		}
	}
}

// ---- the document's matrices ---------------------------------------------

// qd1gReadDoc reads the census document.
func qd1gReadDoc(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../../internal/quote/SYNC-DOOR-CENSUS.md")
	if err != nil {
		t.Fatalf("read the census document: %v", err)
	}
	return string(raw)
}

// qd1gDocCells reads section 3 of the census document: every matrix row of a
// `### door` block is a role and its A or R cells, cell n answering leg n; the
// numbered lines under a matrix are its column legend, leg n named by line n.
// A problem found while reading (a bad cell, a repeated role row, a repeated
// legend number) is returned, never folded away.
func qd1gDocCells(raw string) (cells, legends map[string]string, problems []string) {
	section := raw
	if i := strings.Index(section, "\n## 3. "); i >= 0 {
		section = section[i:]
	}
	if i := strings.Index(section, "\n## 4. "); i >= 0 {
		section = section[:i]
	}
	cells = map[string]string{}
	legends = map[string]string{}
	rows := map[string]bool{}
	door := ""
	heading := regexp.MustCompile("^### `([a-z]+\\.[A-Za-z]+)`")
	legend := regexp.MustCompile("^([0-9]+)\\. (.+)$")
	for _, line := range strings.Split(section, "\n") {
		if m := heading.FindStringSubmatch(line); m != nil {
			door = m[1]
			continue
		}
		if door == "" {
			continue
		}
		if m := legend.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			key := door + "|" + m[1]
			if _, dup := legends[key]; dup {
				problems = append(problems, fmt.Sprintf("census document: %s legend number %s appears twice", door, m[1]))
			}
			legends[key] = strings.TrimSpace(m[2])
			continue
		}
		if !strings.HasPrefix(line, "| ") {
			continue
		}
		parts := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		role := strings.TrimSpace(parts[0])
		isRole := false
		for _, r := range qd1gRoleSubs() {
			isRole = isRole || r.role == role
		}
		if !isRole {
			continue
		}
		if rows[door+"|"+role] {
			problems = append(problems, fmt.Sprintf("census document: %s has the %s row twice", door, role))
			continue
		}
		rows[door+"|"+role] = true
		for i, cell := range parts[1:] {
			cell = strings.TrimSpace(cell)
			if cell != "A" && cell != "R" {
				problems = append(problems, fmt.Sprintf("census document: %s %s leg %d holds %q, want A or R", door, role, i+1, cell))
				continue
			}
			cells[fmt.Sprintf("%s|%s|%d", door, role, i+1)] = cell
		}
	}
	return cells, legends, problems
}

// qd1gRuleNote is the tag the document's legend adds to a leg the census
// asserts by rule; it is not part of the leg's name.
const qd1gRuleNote = " (stated rule, no REST twin)"

// qd1gCheckMatrices requires the document's section 3 to equal what the census
// observed, cell for cell and in both directions, and each matrix's column
// legend to name the leg its column answers. The observed answer is the sync
// door's own (the REST answer, or the stated rule, equals it whenever the
// census is otherwise green), so a rule changed on both sides at once, which
// leaves REST equal to sync, still turns the document stale and fails here.
func qd1gCheckMatrices(raw string, answers []qd1gAnswer) []string {
	verb := map[bool]string{true: "A", false: "R"}
	doc, legends, problems := qd1gDocCells(raw)
	leg := map[string]int{}
	seen := map[string]bool{}
	named := map[string]bool{}
	longest := map[string]int{}
	for _, a := range answers {
		leg[a.Door+"|"+a.Role]++
		n := leg[a.Door+"|"+a.Role]
		if n > longest[a.Door] {
			longest[a.Door] = n
		}
		key := fmt.Sprintf("%s|%s|%d", a.Door, a.Role, n)
		seen[key] = true
		documented, ok := doc[key]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("CENSUS DOCUMENT %s as %s, leg %d (%s): the census observed %s and the document has no cell",
				a.Door, a.Role, n, a.Leg, verb[a.Sync]))
		case documented != verb[a.Sync]:
			problems = append(problems, fmt.Sprintf("CENSUS DOCUMENT %s as %s, leg %d (%s): the document says %s, the census observed %s",
				a.Door, a.Role, n, a.Leg, documented, verb[a.Sync]))
		}
		lkey := fmt.Sprintf("%s|%d", a.Door, n)
		text, ok := legends[lkey]
		text = strings.TrimSuffix(text, qd1gRuleNote)
		if msg := fmt.Sprintf("CENSUS DOCUMENT %s legend %d: the document names %q, the census observed leg %q", a.Door, n, text, a.Leg); (!ok || text != a.Leg) && !named[msg] {
			named[msg] = true
			problems = append(problems, msg)
		}
	}
	for key, cell := range doc {
		if !seen[key] {
			p := strings.Split(key, "|")
			problems = append(problems, fmt.Sprintf("CENSUS DOCUMENT %s as %s, leg %s: the document says %s and the census observed no such cell", p[0], p[1], p[2], cell))
		}
	}
	for key, text := range legends {
		p := strings.Split(key, "|")
		if n, _ := strconv.Atoi(p[1]); n > longest[p[0]] {
			problems = append(problems, fmt.Sprintf("CENSUS DOCUMENT %s legend %s names %q and the census observed no such leg", p[0], p[1], text))
		}
	}
	sort.Strings(problems)
	return problems
}

// qd1gMatchMatrices holds the document to the answers the census observed.
func qd1gMatchMatrices(t *testing.T) {
	t.Helper()
	for _, p := range qd1gCheckMatrices(qd1gReadDoc(t), qd1gAnswers) {
		t.Error(p)
	}
}

// qd1gMatrixMutants proves the document check by mutation: each mutant is a
// copy of the document with one defect the check must name. A legend that
// names another leg than its column answers, a legend line missing or extra,
// and a role row repeated in one matrix (so that the last row would win) each
// stay green against a check that reads the cells alone.
func qd1gMatrixMutants(t *testing.T) {
	t.Helper()
	if t.Failed() {
		return
	}
	raw := qd1gReadDoc(t)
	mutate := func(heading, old, repl string) string {
		i := strings.Index(raw, "### `"+heading+"`")
		if i < 0 {
			t.Fatalf("mutant: the document has no block for %s", heading)
		}
		tail := raw[i:]
		if !strings.Contains(tail, old) {
			t.Fatalf("mutant: %s has no %q to change", heading, old)
		}
		return raw[:i] + strings.Replace(tail, old, repl, 1)
	}
	mutants := []struct{ name, doc, want string }{
		{"two legends swapped",
			mutate("quotes.Create", "1. create in branch A\n2. create in branch B", "1. create in branch B\n2. create in branch A"),
			"legend 1"},
		{"a legend names another leg",
			mutate("approvals.Pull", "1. read a plain request of branch A", "1. read a plain request of branch C"),
			"legend 1"},
		{"a legend line missing",
			mutate("quotes.Write", "\n4. edit another seller's quote in branch B", ""),
			"legend 4"},
		{"a legend line extra",
			mutate("quotes.Write", "4. edit another seller's quote in branch B", "4. edit another seller's quote in branch B\n5. edit a quote nobody asked about"),
			"legend 5"},
		{"a legend number repeated",
			mutate("quotes.Create", "2. create in branch B", "1. create in branch B"),
			"appears twice"},
		{"a role row repeated with other cells first",
			mutate("quotes.Create", "| inside_sales | A | R |", "| inside_sales | R | A |\n| inside_sales | A | R |"),
			"inside_sales row twice"},
		{"a role row repeated with other cells last",
			mutate("quotes.Create", "| inside_sales | A | R |", "| inside_sales | A | R |\n| inside_sales | R | A |"),
			"inside_sales row twice"},
		{"a role row repeated unchanged",
			mutate("approvals.Load", "| hr |", "| hr | R | R | R | R | R | R | R | R |\n| hr |"),
			"hr row twice"},
	}
	for _, m := range mutants {
		problems := qd1gCheckMatrices(m.doc, qd1gAnswers)
		hit := false
		for _, p := range problems {
			hit = hit || strings.Contains(p, m.want)
		}
		if !hit {
			t.Errorf("document mutant %q stayed green (want a problem naming %q, got %q)", m.name, m.want, problems)
		}
	}
}

// ---- the registered scopes -----------------------------------------------

// qd1gScopesOnQuoteTables are the registered sync scopes allowed to touch the
// quote or approval tables, each with the reason (the document repeats them).
// quotes and approvals are the two stores the census drives door by door.
var qd1gScopesOnQuoteTables = map[string]string{
	"quotes":          "the quotes store, every door ruled by this census",
	"approvals":       "the approvals store, every door ruled by this census",
	"customer_quotes": "a separate table (the customer quote record), not the staff quotes table",
	"orders":          "the tenant plan table; reads no quote or approval column",
	"widget_measures": "reads only the layout tables; no quote or approval column",
	"attachments":     "carries owner_kind quote links, fenced by org only, no quote field today",
}

// qd1gScopesClear are the other registered scopes, reviewed as touching
// neither table. A scope on neither list fails the census by name.
var qd1gScopesClear = map[string]bool{
	"aging": true, "catalog": true, "channels": true, "customers": true,
	"cycle_counts": true, "delivery_instructions": true, "delivery_stops": true,
	"drivers": true, "inventory_lookup": true, "invoices": true, "loads": true,
	"parked_tickets": true, "payment_methods": true, "payments": true,
	"pick_tasks": true, "pickups": true, "pod": true, "price_matrix": true,
	"products": true, "projects": true, "purchase_orders": true, "runs": true,
	"scope_items": true, "shortages": true, "stops": true, "team": true,
	"trucks": true, "vendors": true, "warranty_board": true, "warranty_claims": true,
	"weights": true, "will_call": true,
	// The messaging feeds (House Messages core): channels, messages, acks and
	// notify_prefs read and write the msg_* tables only, fenced per caller;
	// no quote or approval column is anywhere in their stores.
	"messages": true, "acks": true, "notify_prefs": true,
}

// qd1gScopesGuard reads every RegisterScope in this package's source and
// requires each scope to be ruled: allowed on the quote and approval tables
// (with a reason, in the census document too) or reviewed clear of them.
func qd1gScopesGuard(t *testing.T) {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("list the cmd/core sources: %v", err)
	}
	re := regexp.MustCompile(`RegisterScope\("([a-z_]+)"`)
	registered := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
			registered[m[1]] = true
		}
	}
	doc, err := os.ReadFile("../../internal/quote/SYNC-DOOR-CENSUS.md")
	if err != nil {
		t.Fatalf("read the census document: %v", err)
	}
	for name := range registered {
		_, touches := qd1gScopesOnQuoteTables[name]
		if !touches && !qd1gScopesClear[name] {
			t.Errorf("sync census: the registered sync scope %q is on neither list: decide whether it touches the quote or approval tables, "+
				"and list it in qd1gScopesOnQuoteTables (with a reason, and in the census document) or qd1gScopesClear", name)
		}
		if touches && qd1gScopesClear[name] {
			t.Errorf("sync census: scope %q is on both lists", name)
		}
	}
	for name := range qd1gScopesOnQuoteTables {
		if !registered[name] {
			t.Errorf("sync census: scope %q is listed as touching quote or approval tables but is no longer registered", name)
		}
		if !strings.Contains(string(doc), "`"+name+"`") {
			t.Errorf("sync census: scope %q has no reason in the census document", name)
		}
	}
	for name := range qd1gScopesClear {
		if !registered[name] {
			t.Errorf("sync census: scope %q is listed as clear but is no longer registered", name)
		}
	}
}
