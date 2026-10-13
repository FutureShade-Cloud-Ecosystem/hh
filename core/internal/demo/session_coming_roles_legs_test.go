//go:build demo

package demo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// The coming roles cycle's legs over the real serve role (decision log row
// 1199): the Manager Home profile each manager role opens, then the four
// common acceptance legs (L-card, L-wall, L-branch, L-cost). Every
// expectation is the server's own answer over the seeded and populated
// world, never a copy of a document.
//
// The world is built the way a box builds it: the frozen migrations, the
// first demoseed apply, the populate, and a second apply that adds the quote
// and controller worlds (the parked journal and the floor request). One
// subtest per role, added one signed off commit at a time. The mints use a
// visitor cookie of their own each, so no role spends another's allowance.
func TestDemoModule_ComingRolesLegs(t *testing.T) {
	ctx := context.Background()
	pool, dsn := freshMigratedDB(t, "comingroles")
	bin := buildDemoCore(t)
	seedAndPopulate(t, bin, dsn)
	runStep(t, bin, []string{"demoseed", "-apply", "-now", seedClock.Format(time.RFC3339)}, subEnv(t, populateEnv(dsn)))
	base := startDemoServe(t, bin, dsn)
	client := &http.Client{Timeout: 20 * time.Second}

	var trentonID, kingstonID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE code = 'TRT'`).Scan(&trentonID); err != nil {
		t.Fatalf("the Trenton branch: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE code = 'KGN'`).Scan(&kingstonID); err != nil {
		t.Fatalf("the Kingston branch: %v", err)
	}

	type minted struct {
		headers map[string]string
		user    struct {
			UserID   string `json:"user_id"`
			RoleCode string `json:"role_code"`
			RoleName string `json:"role_name"`
			Tier     int    `json:"tier"`
			Scope    string `json:"scope"`
		}
	}
	// mint is the card tap: one POST with a visitor cookie of its own.
	mint := func(t *testing.T, flow string) minted {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, base+"/api/v1/demo/session", strings.NewReader(`{"flow":"`+flow+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Cookie", "demo_visitor="+fmt.Sprintf("%032x", uint64(time.Now().UnixNano())))
		rsp, err := client.Do(req)
		if err != nil {
			t.Fatalf("the %s mint: %v", flow, err)
		}
		raw, _ := io.ReadAll(rsp.Body)
		rsp.Body.Close()
		if rsp.StatusCode != http.StatusOK {
			t.Fatalf("the %s mint: status %d, want 200 (body: %s)", flow, rsp.StatusCode, raw)
		}
		var out struct {
			Token string `json:"token"`
			User  struct {
				UserID   string `json:"user_id"`
				RoleCode string `json:"role_code"`
				RoleName string `json:"role_name"`
				Tier     int    `json:"tier"`
				Scope    string `json:"scope"`
			} `json:"user"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("the %s session body does not decode: %v", flow, err)
		}
		return minted{headers: map[string]string{"Authorization": "Bearer " + out.Token, "X-Org": "hh-demo"}, user: out.User}
	}
	do := func(t *testing.T, method, path string, headers map[string]string, body string) (int, string) {
		t.Helper()
		req, err := http.NewRequest(method, base+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rsp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		raw, _ := io.ReadAll(rsp.Body)
		rsp.Body.Close()
		return rsp.StatusCode, string(raw)
	}
	get := func(t *testing.T, path string, m minted) (int, string) {
		t.Helper()
		return do(t, http.MethodGet, path, m.headers, "")
	}
	branchCodes := func(t *testing.T, m minted) []string {
		t.Helper()
		status, raw := get(t, "/api/v1/branches", m)
		if status != http.StatusOK {
			t.Fatalf("GET /api/v1/branches: status %d, want 200 (body: %s)", status, raw)
		}
		var rows []struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal([]byte(raw), &rows); err != nil {
			t.Fatalf("the branches body does not decode: %v", err)
		}
		codes := make([]string, 0, len(rows))
		for _, r := range rows {
			codes = append(codes, r.Code)
		}
		return codes
	}
	contains := func(list []string, want string) bool {
		for _, v := range list {
			if v == want {
				return true
			}
		}
		return false
	}
	sameSet := func(got, want []string) bool {
		if len(got) != len(want) {
			return false
		}
		seen := map[string]bool{}
		for _, g := range got {
			seen[g] = true
		}
		for _, w := range want {
			if !seen[w] {
				return false
			}
		}
		return true
	}
	// wantWall holds a route refused by the role gate in its shared body.
	wantWall := func(t *testing.T, what, path string, m minted) {
		t.Helper()
		status, raw := get(t, path, m)
		if status != http.StatusForbidden || strings.TrimSpace(raw) != `{"error":"Forbidden"}` {
			t.Errorf("%s: status %d body %s, want the gate's 403", what, status, raw)
		}
	}
	wantOK := func(t *testing.T, what, path string, m minted) string {
		t.Helper()
		status, raw := get(t, path, m)
		if status != http.StatusOK {
			t.Errorf("%s: status %d, want 200 (body: %s)", what, status, raw)
		}
		return raw
	}

	// marginGlance asks the till's margin glance about ten of the demo paver at
	// Trenton and returns the status and body: the cost and margin question the
	// matrix answers per role (the list, then the policy word, both must allow).
	marginGlance := func(t *testing.T, m minted) (int, string) {
		t.Helper()
		status, raw := get(t, "/api/v1/products/counter-search?q=DEMO-PAV-HOLLAND", m)
		if status != http.StatusOK {
			return status, raw
		}
		var found struct {
			Items []struct {
				ProductID string `json:"product_id"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(raw), &found); err != nil || len(found.Items) == 0 {
			t.Fatalf("the counter search does not answer a product: %v (body: %s)", err, raw)
		}
		return do(t, http.MethodPost, "/api/v1/pos/margin-glance", m.headers,
			`{"branch_id":"`+trentonID+`","customer_id":"","customer_type":"RETAIL","lines":[{"product_id":"`+found.Items[0].ProductID+`","qty_entered":10,"uom_entered":"PC"}]}`)
	}

	t.Run("general-manager", func(t *testing.T) {
		gm := mint(t, "general-manager")
		// The card's credential is the flow table's account, a leadership
		// login with no branch fence.
		if gm.user.UserID != "de5b2000-0000-4000-9000-000000000001" || gm.user.RoleCode != "general_manager" ||
			gm.user.RoleName != "General Manager" || gm.user.Tier != 1 || gm.user.Scope != "GLOBAL" {
			t.Errorf("the credential is %+v, want the General Manager at tier 1 GLOBAL", gm.user)
		}
		// Manager Home opens the role's own profile (migration 0381's manager.general,
		// born from the admin shape) and its needs queue leads with the approvals
		// waiting on the role. No profile is invented here: the dashboard module
		// carries it.
		var dash struct {
			Profile string `json:"profile"`
			Source  string `json:"source"`
		}
		if err := json.Unmarshal([]byte(wantOK(t, "the Manager Home landing", "/api/v1/dashboard", gm)), &dash); err != nil {
			t.Fatalf("the landing does not decode: %v", err)
		}
		if dash.Profile != "manager.general" || dash.Source != "role" {
			t.Errorf("the landing is profile %q from %q, want the role's own manager.general", dash.Profile, dash.Source)
		}
		var needs struct {
			Profile string `json:"profile"`
			Rows    []struct {
				SourceKey string `json:"source_key"`
				Count     int    `json:"count"`
			} `json:"rows"`
		}
		if err := json.Unmarshal([]byte(wantOK(t, "the needs queue", "/api/v1/dashboard/needs", gm)), &needs); err != nil {
			t.Fatalf("the needs queue does not decode: %v", err)
		}
		if needs.Profile != "manager.general" {
			t.Errorf("the needs queue is profile %q, want manager.general", needs.Profile)
		}
		waiting := 0
		for _, r := range needs.Rows {
			if r.SourceKey == "approvals.waiting" {
				waiting = r.Count
			}
		}
		if waiting == 0 {
			t.Errorf("the needs queue holds no approvals waiting row for the role that decides the exceptions: %+v", needs.Rows)
		}

		// L-card: the bearer the card minted reaches the surfaces the role holds
		// (Manager Home above; the till's policy is a read cell).
		wantOK(t, "the till's policy (counter-till, read)", "/api/v1/pos/policy", gm)
		// L-branch: a global role reaches every branch.
		if codes := branchCodes(t, gm); !sameSet(codes, []string{"TRT", "KGN"}) {
			t.Errorf("the General Manager reaches %v, want both branches", codes)
		}
		wantOK(t, "the Money Desk's credit holds (money-desk, manage)", "/api/v1/ar/credit-holds", gm)
		wantOK(t, "the dispatch board at Kingston", "/api/v1/dispatch/trucks?branch_id="+kingstonID, gm)
		wantOK(t, "the dispatch board at Trenton", "/api/v1/dispatch/trucks?branch_id="+trentonID, gm)
		// L-wall: Yard Pick and HH Drive sit at None for the role.
		wantWall(t, "the yard pick queue (yard-pick, None)", "/api/v1/yard/pick-queue?branch_id="+trentonID, gm)
		wantWall(t, "the driver's runs (drive-run, None)", "/api/v1/dispatch/my-runs?date="+seedClock.UTC().Format("2006-01-02"), gm)
		// L-cost: the cost and margin list names the role and Dealer Admin is its admin cell.
		wantOK(t, "the pricing audit (cost and margin list)", "/api/v1/pricing/audit", gm)
		cfg, err := roleconfig.Default()
		if err != nil {
			t.Fatalf("roleconfig.Default: %v", err)
		}
		if !contains(cfg.CostVisibility(), "general_manager") {
			t.Errorf("the decided matrix does not list the General Manager on the cost and margin list: %v", cfg.CostVisibility())
		}

		// Decide legs: the kinds the original gave the role (Margin, Credit, COD,
		// Journal entry, Freight above quote), only where the server decides them.
		// Each kind's list answers 200 for the role and 403 for a login that does
		// not decide the kind; the kinds the seed files are then decided.
		sales := mint(t, "sales")
		for _, kind := range []string{"MARGIN_BELOW_MINIMUM", "CREDIT_LIMIT_EXCEEDED", "COD_CONSTRAINT", "JE_OVER_THRESHOLD", "FREIGHT_ABOVE_QUOTE"} {
			wantOK(t, "the "+kind+" list for the role that decides it", "/api/v1/staff/approvals?status=PENDING&kind="+kind, gm)
			if status, raw := get(t, "/api/v1/staff/approvals?status=PENDING&kind="+kind, sales); status != http.StatusForbidden && kind != "MARGIN_BELOW_MINIMUM" {
				t.Errorf("the %s list for the counter clerk: status %d body %s, want 403", kind, status, raw)
			}
		}
		var pending struct {
			Items []struct {
				ID     string `json:"id"`
				Policy string `json:"policy_type"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(wantOK(t, "the pending approvals", "/api/v1/staff/approvals?status=PENDING", gm)), &pending); err != nil {
			t.Fatalf("the approvals page does not decode: %v", err)
		}
		decided := map[string]bool{}
		for _, want := range []string{"MARGIN_BELOW_MINIMUM", "JE_OVER_THRESHOLD", "FREIGHT_ABOVE_QUOTE"} {
			id := ""
			for _, it := range pending.Items {
				if it.Policy == want {
					id = it.ID
					break
				}
			}
			if id == "" {
				t.Errorf("the role's queue holds no pending %s request the seed files", want)
				continue
			}
			status, raw := do(t, http.MethodPost, "/api/v1/staff/approvals/"+id+"/decide", gm.headers, `{"approve":true,"reason":"demo decision"}`)
			if status != http.StatusOK {
				t.Errorf("the role deciding a %s request: status %d, want 200 (body: %s)", want, status, raw)
				continue
			}
			decided[want] = true
		}
		if len(decided) != 3 {
			t.Errorf("the role decided %v of the three seeded kinds", decided)
		}
	})
	t.Run("hr", func(t *testing.T) {
		hr := mint(t, "hr")
		// L-card: the flow table's account, a leadership credential with no branch
		// fence, and a bearer that reaches the two surfaces the role holds above
		// None (Dealer Admin at admin, House Messages at work).
		if hr.user.UserID != "de5b2000-0000-4000-9000-000000000002" || hr.user.RoleCode != "hr" ||
			hr.user.RoleName != "HR" || hr.user.Tier != 1 || hr.user.Scope != "GLOBAL" {
			t.Errorf("the credential is %+v, want HR at tier 1 GLOBAL", hr.user)
		}
		wantOK(t, "Dealer Admin (admin): the pricing rules read", "/api/v1/pricing/rules", hr)
		wantOK(t, "House Messages (work): the channel list", "/api/v1/messaging/channels", hr)
		// L-branch: a global role reaches every branch.
		if codes := branchCodes(t, hr); !sameSet(codes, []string{"TRT", "KGN"}) {
			t.Errorf("HR reaches %v, want both branches", codes)
		}
		// L-wall: every other surface sits at None for the role, Manager Home
		// included (HR holds no Manager Home), and the approvals queue is not HR's:
		// no kind is decided by the role.
		wantWall(t, "Manager Home (manager-home, None)", "/api/v1/dashboard", hr)
		wantWall(t, "the needs queue (manager-home, None)", "/api/v1/dashboard/needs", hr)
		wantWall(t, "the till's policy (counter-till, None)", "/api/v1/pos/policy", hr)
		wantWall(t, "the Money Desk (money-desk, None)", "/api/v1/ar/credit-holds", hr)
		wantWall(t, "the dispatch board (dispatch-board, None)", "/api/v1/dispatch/trucks?branch_id="+trentonID, hr)
		wantWall(t, "the yard pick queue (yard-pick, None)", "/api/v1/yard/pick-queue?branch_id="+trentonID, hr)
		for _, kind := range []string{"MARGIN_BELOW_MINIMUM", "CREDIT_LIMIT_EXCEEDED", "COD_CONSTRAINT", "JE_OVER_THRESHOLD", "FREIGHT_ABOVE_QUOTE"} {
			if status, raw := get(t, "/api/v1/staff/approvals?status=PENDING&kind="+kind, hr); status != http.StatusForbidden {
				t.Errorf("HR listing %s approvals: status %d body %s, want 403 (HR decides no kind)", kind, status, raw)
			}
		}
		// L-cost: Dealer Admin is at admin for the role, yet the pricing audit
		// carries cost and margin figures and HR is off the cost and margin list,
		// so it is refused beside the level.
		wantWall(t, "the pricing audit (cost and margin list)", "/api/v1/pricing/audit", hr)
		cfg, err := roleconfig.Default()
		if err != nil {
			t.Fatalf("roleconfig.Default: %v", err)
		}
		if contains(cfg.CostVisibility(), "hr") {
			t.Errorf("the decided matrix lists HR on the cost and margin list: %v", cfg.CostVisibility())
		}
	})
	t.Run("branch-manager", func(t *testing.T) {
		bm := mint(t, "branch-manager")
		if bm.user.UserID != "de5b2000-0000-4000-9000-000000000003" || bm.user.RoleCode != "branch_manager" ||
			bm.user.RoleName != "Branch Manager" || bm.user.Tier != 2 || bm.user.Scope != "BRANCH" {
			t.Errorf("the credential is %+v, want the Branch Manager at tier 2 BRANCH", bm.user)
		}
		// Manager Home opens the role's own profile (migration 0381's manager.branch,
		// born from the shared manager shape) and its needs queue counts the branch's
		// own work: the parked ticket at Trenton, not the one parked at Kingston. No
		// profile is invented here: the dashboard module carries it.
		var dash struct {
			Profile string `json:"profile"`
			Source  string `json:"source"`
		}
		if err := json.Unmarshal([]byte(wantOK(t, "the Manager Home landing", "/api/v1/dashboard", bm)), &dash); err != nil {
			t.Fatalf("the landing does not decode: %v", err)
		}
		if dash.Profile != "manager.branch" || dash.Source != "role" {
			t.Errorf("the landing is profile %q from %q, want the role's own manager.branch", dash.Profile, dash.Source)
		}
		var needs struct {
			Profile string `json:"profile"`
			Rows    []struct {
				SourceKey string `json:"source_key"`
				Count     int    `json:"count"`
			} `json:"rows"`
		}
		if err := json.Unmarshal([]byte(wantOK(t, "the needs queue", "/api/v1/dashboard/needs", bm)), &needs); err != nil {
			t.Fatalf("the needs queue does not decode: %v", err)
		}
		if needs.Profile != "manager.branch" {
			t.Errorf("the needs queue is profile %q, want manager.branch", needs.Profile)
		}
		parked := -1
		for _, r := range needs.Rows {
			if r.SourceKey == "pos.parked" {
				parked = r.Count
			}
		}
		if parked != 1 {
			t.Errorf("the needs queue counts %d parked tickets, want the one at Trenton (rows: %+v)", parked, needs.Rows)
		}

		// L-card: the card's bearer reaches the surfaces the role holds: the till
		// (manage), the Quote Desk (work), the dispatch board (manage).
		wantOK(t, "the till's policy (counter-till, manage)", "/api/v1/pos/policy", bm)
		wantOK(t, "the Quote Desk's quotes (quote-desk, work)", "/api/v1/quotes", bm)
		// L-branch: the role's own branch only. The branch list leaves Kingston
		// out and the server fences a read named at it.
		if codes := branchCodes(t, bm); !sameSet(codes, []string{"TRT"}) {
			t.Errorf("the Branch Manager reaches %v, want Trenton alone", codes)
		}
		wantOK(t, "the dispatch board at Trenton", "/api/v1/dispatch/trucks?branch_id="+trentonID, bm)
		if status, raw := get(t, "/api/v1/dispatch/trucks?branch_id="+kingstonID, bm); status != http.StatusForbidden {
			t.Errorf("the dispatch board at Kingston: status %d body %s, want the branch fence's 403", status, raw)
		}
		// L-wall: Yard Pick (a Trenton read) and Dealer Admin sit at None for the role.
		wantWall(t, "the yard pick queue (yard-pick, None)", "/api/v1/yard/pick-queue?branch_id="+trentonID, bm)
		wantWall(t, "the pricing rules (dealer-admin, None)", "/api/v1/pricing/rules", bm)
		// L-cost: the role is on the cost and margin list and tier two clears the
		// policy word (MANAGER_UP), so the till's glance answers a margin figure.
		cfg, err := roleconfig.Default()
		if err != nil {
			t.Fatalf("roleconfig.Default: %v", err)
		}
		if !contains(cfg.CostVisibility(), "branch_manager") {
			t.Errorf("the decided matrix does not list the Branch Manager on the cost and margin list: %v", cfg.CostVisibility())
		}
		if status, raw := marginGlance(t, bm); status != http.StatusOK || !strings.Contains(raw, "margin_bps") {
			t.Errorf("the margin glance for the Branch Manager: status %d body %s, want a margin figure", status, raw)
		}
		// Approvals: the decided matrix names the Sales manager alone as the
		// branch's floor approver (quote_floor_branch_approvers) and the Financial
		// Controller and General Manager for the other four kinds, so the Branch
		// Manager decides no kind at all. (Manager Home's decider words and the
		// coming roles notes say a branch manager decides Margin in its own
		// branch; the server does not, and the server is what this leg pins.)
		for _, kind := range []string{"MARGIN_BELOW_MINIMUM", "CREDIT_LIMIT_EXCEEDED", "COD_CONSTRAINT", "JE_OVER_THRESHOLD", "FREIGHT_ABOVE_QUOTE"} {
			if status, raw := get(t, "/api/v1/staff/approvals?status=PENDING&kind="+kind, bm); status != http.StatusForbidden {
				t.Errorf("the Branch Manager listing %s approvals: status %d body %s, want 403", kind, status, raw)
			}
		}
	})
	t.Run("logistics-manager", func(t *testing.T) {
		lm := mint(t, "logistics-manager")
		if lm.user.UserID != "de5b2000-0000-4000-9000-000000000004" || lm.user.RoleCode != "logistics_manager" ||
			lm.user.RoleName != "Logistics Manager" || lm.user.Tier != 2 || lm.user.Scope != "BRANCH" {
			t.Errorf("the credential is %+v, want the Logistics Manager at tier 2 BRANCH", lm.user)
		}
		// Manager Home opens the role's own profile (migration 0381's manager.logistics,
		// born from the dock shape so the day's deliveries lead, as 0203 ruled). No
		// profile is invented here: the dashboard module carries it.
		var dash struct {
			Profile string `json:"profile"`
			Source  string `json:"source"`
		}
		if err := json.Unmarshal([]byte(wantOK(t, "the Manager Home landing", "/api/v1/dashboard", lm)), &dash); err != nil {
			t.Fatalf("the landing does not decode: %v", err)
		}
		if dash.Profile != "manager.logistics" || dash.Source != "role" {
			t.Errorf("the landing is profile %q from %q, want the role's own manager.logistics", dash.Profile, dash.Source)
		}
		var widgets struct {
			Profile string `json:"profile"`
			Widgets []struct {
				Measure  string `json:"measure"`
				Position int    `json:"position"`
			} `json:"widgets"`
		}
		if err := json.Unmarshal([]byte(wantOK(t, "the widgets", "/api/v1/dashboard/widgets", lm)), &widgets); err != nil {
			t.Fatalf("the widgets do not decode: %v", err)
		}
		if widgets.Profile != "manager.logistics" || len(widgets.Widgets) == 0 ||
			widgets.Widgets[0].Position != 0 || widgets.Widgets[0].Measure != "deliveries.today" {
			t.Errorf("the widgets are %+v, want the day's deliveries leading on manager.logistics", widgets)
		}
		// The needs queue answers on the same profile. In the seeded world nothing
		// at Trenton is unassigned, due to commit or stale, so the queue is empty
		// and says so; what the profile carries is its three dock sources, read
		// from the policy table.
		var needs struct {
			Profile string `json:"profile"`
			Rows    []struct {
				SourceKey string `json:"source_key"`
			} `json:"rows"`
		}
		if err := json.Unmarshal([]byte(wantOK(t, "the needs queue", "/api/v1/dashboard/needs", lm)), &needs); err != nil {
			t.Fatalf("the needs queue does not decode: %v", err)
		}
		if needs.Profile != "manager.logistics" {
			t.Errorf("the needs queue is profile %q, want manager.logistics", needs.Profile)
		}
		rows, err := pool.Query(context.Background(),
			`SELECT source_key FROM dashboard_needs_sources WHERE profile = 'manager.logistics' AND visible ORDER BY position`)
		if err != nil {
			t.Fatalf("the needs policy read: %v", err)
		}
		var sources []string
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				t.Fatal(err)
			}
			sources = append(sources, key)
		}
		rows.Close()
		want := []string{"dispatch.unassigned", "dispatch.due_to_commit", "tasks.stale_picks"}
		if strings.Join(sources, ",") != strings.Join(want, ",") {
			t.Errorf("the profile's needs sources are %v, want the dock's %v", sources, want)
		}

		// L-card: the card's bearer reaches the surfaces the role holds: the
		// dispatch board (manage) and House Messages (work).
		wantOK(t, "the dispatch board at Trenton (dispatch-board, manage)", "/api/v1/dispatch/trucks?branch_id="+trentonID, lm)
		wantOK(t, "House Messages (work): the channel list", "/api/v1/messaging/channels", lm)
		// L-branch: the role's own branch only.
		if codes := branchCodes(t, lm); !sameSet(codes, []string{"TRT"}) {
			t.Errorf("the Logistics Manager reaches %v, want Trenton alone", codes)
		}
		if status, raw := get(t, "/api/v1/dispatch/trucks?branch_id="+kingstonID, lm); status != http.StatusForbidden {
			t.Errorf("the dispatch board at Kingston: status %d body %s, want the branch fence's 403", status, raw)
		}
		// L-wall: the till, the Quote Desk, Yard Pick, Buying and Stock, the Money
		// Desk and Dealer Admin all sit at None for the role.
		wantWall(t, "the till's policy (counter-till, None)", "/api/v1/pos/policy", lm)
		wantWall(t, "the Quote Desk's quotes (quote-desk, None)", "/api/v1/quotes", lm)
		wantWall(t, "the yard pick queue (yard-pick, None)", "/api/v1/yard/pick-queue?branch_id="+trentonID, lm)
		wantWall(t, "the vendors (buying-and-stock, None)", "/api/v1/purchasing/vendors", lm)
		wantWall(t, "the Money Desk (money-desk, None)", "/api/v1/ar/credit-holds", lm)
		wantWall(t, "the pricing rules (dealer-admin, None)", "/api/v1/pricing/rules", lm)
		// L-cost: the cost and margin list names the role, but every route that
		// carries a cost or margin figure sits on a surface the role holds at None,
		// so none answers: the glance (counter-till) and the pricing audit
		// (dealer-admin) are both refused.
		cfg, err := roleconfig.Default()
		if err != nil {
			t.Fatalf("roleconfig.Default: %v", err)
		}
		if !contains(cfg.CostVisibility(), "logistics_manager") {
			t.Errorf("the decided matrix does not list the Logistics Manager on the cost and margin list: %v", cfg.CostVisibility())
		}
		if status, raw := marginGlance(t, lm); status != http.StatusForbidden {
			t.Errorf("the margin glance for the Logistics Manager: status %d body %s, want 403 (counter-till at None)", status, raw)
		}
		wantWall(t, "the pricing audit (dealer-admin, None)", "/api/v1/pricing/audit", lm)
		// Approvals: the role decides no kind.
		for _, kind := range []string{"MARGIN_BELOW_MINIMUM", "CREDIT_LIMIT_EXCEEDED", "COD_CONSTRAINT", "JE_OVER_THRESHOLD", "FREIGHT_ABOVE_QUOTE"} {
			if status, raw := get(t, "/api/v1/staff/approvals?status=PENDING&kind="+kind, lm); status != http.StatusForbidden {
				t.Errorf("the Logistics Manager listing %s approvals: status %d body %s, want 403", kind, status, raw)
			}
		}
	})
	t.Run("outside-sales", func(t *testing.T) {
		os := mint(t, "outside-sales")
		// L-card: the flow table's account, a frontline credential bound to a
		// branch, and a bearer that reaches the surfaces the role holds: the Quote
		// Desk (work), House Messages (work) and the dispatch board (read).
		if os.user.UserID != "de5b2000-0000-4000-9000-000000000005" || os.user.RoleCode != "outside_sales" ||
			os.user.RoleName != "Outside Sales" || os.user.Tier != 3 || os.user.Scope != "BRANCH" {
			t.Errorf("the credential is %+v, want Outside Sales at tier 3 BRANCH", os.user)
		}
		wantOK(t, "the Quote Desk's quotes (quote-desk, work)", "/api/v1/quotes", os)
		wantOK(t, "House Messages (work): the channel list", "/api/v1/messaging/channels", os)
		wantOK(t, "the dispatch board at Trenton (dispatch-board, read)", "/api/v1/dispatch/trucks?branch_id="+trentonID, os)
		// L-branch: the role's own branch only.
		if codes := branchCodes(t, os); !sameSet(codes, []string{"TRT"}) {
			t.Errorf("Outside Sales reaches %v, want Trenton alone", codes)
		}
		if status, raw := get(t, "/api/v1/dispatch/trucks?branch_id="+kingstonID, os); status != http.StatusForbidden {
			t.Errorf("the dispatch board at Kingston: status %d body %s, want the branch fence's 403", status, raw)
		}
		// L-wall: the till (Outside Sales has no till), Manager Home, Yard Pick,
		// HH Drive and Dealer Admin sit at None for the role, and the Money Desk
		// is a read cell: its credit holds need the manager tier beside it.
		wantWall(t, "the till's policy (counter-till, None)", "/api/v1/pos/policy", os)
		wantWall(t, "Manager Home (manager-home, None)", "/api/v1/dashboard", os)
		wantWall(t, "the needs queue (manager-home, None)", "/api/v1/dashboard/needs", os)
		wantWall(t, "the yard pick queue (yard-pick, None)", "/api/v1/yard/pick-queue?branch_id="+trentonID, os)
		wantWall(t, "the pricing rules (dealer-admin, None)", "/api/v1/pricing/rules", os)
		wantWall(t, "the Money Desk's credit holds (money-desk, read, manager tier)", "/api/v1/ar/credit-holds", os)
		for _, kind := range []string{"MARGIN_BELOW_MINIMUM", "CREDIT_LIMIT_EXCEEDED", "COD_CONSTRAINT", "JE_OVER_THRESHOLD", "FREIGHT_ABOVE_QUOTE"} {
			if status, raw := get(t, "/api/v1/staff/approvals?status=PENDING&kind="+kind, os); status != http.StatusForbidden {
				t.Errorf("Outside Sales listing %s approvals: status %d body %s, want 403", kind, status, raw)
			}
		}
		// L-cost: the role is on the cost and margin list, so a quote the role owns,
		// with a line on it, answers the Quote Desk's margin glance with the
		// line's cost and margin (cost is SEEN, as the decided matrix says); the
		// till's glance is refused with the till itself (counter-till at None).
		cfg, err := roleconfig.Default()
		if err != nil {
			t.Fatalf("roleconfig.Default: %v", err)
		}
		if !contains(cfg.CostVisibility(), "outside_sales") {
			t.Errorf("the decided matrix does not list Outside Sales on the cost and margin list: %v", cfg.CostVisibility())
		}
		var custID, productID string
		if err := pool.QueryRow(context.Background(), `SELECT id::text FROM customers WHERE account_no = 'DEMO-C02'`).Scan(&custID); err != nil {
			t.Fatalf("the demo contractor: %v", err)
		}
		if err := pool.QueryRow(context.Background(), `SELECT id::text FROM products WHERE sku = 'DEMO-PAV-HOLLAND'`).Scan(&productID); err != nil {
			t.Fatalf("the demo paver: %v", err)
		}
		status, raw := do(t, http.MethodPost, "/api/v1/quotes", os.headers, `{"customer_id":"`+custID+`","branch_id":"`+trentonID+`"}`)
		if status != http.StatusCreated {
			t.Fatalf("Outside Sales starting a quote: status %d, want 201 (body: %s)", status, raw)
		}
		var quote struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(raw), &quote); err != nil || quote.ID == "" {
			t.Fatalf("the quote does not decode: %v (body: %s)", err, raw)
		}
		if status, raw := do(t, http.MethodPost, "/api/v1/quotes/"+quote.ID+"/lines", os.headers,
			`{"product_id":"`+productID+`","qty_entered":"10","uom_entered":"PC"}`); status != http.StatusOK {
			t.Fatalf("Outside Sales adding a line: status %d, want 200 (body: %s)", status, raw)
		}
		if status, raw := get(t, "/api/v1/quotes/"+quote.ID+"/margin-glance", os); status != http.StatusOK ||
			!strings.Contains(raw, "cost_cents") || !strings.Contains(raw, "margin_bps") {
			t.Errorf("the quote's margin glance for Outside Sales: status %d body %s, want the line's cost and margin", status, raw)
		}
		if status, raw := marginGlance(t, os); status != http.StatusForbidden {
			t.Errorf("the till's margin glance for Outside Sales: status %d body %s, want 403 (counter-till at None)", status, raw)
		}
	})
}
