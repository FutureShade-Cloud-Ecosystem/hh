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
}
