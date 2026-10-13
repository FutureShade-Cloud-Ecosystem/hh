//go:build demo

package demo

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
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
	_, dsn := freshMigratedDB(t, "comingroles")
	bin := buildDemoCore(t)
	seedAndPopulate(t, bin, dsn)
	runStep(t, bin, []string{"demoseed", "-apply", "-now", seedClock.Format(time.RFC3339)}, subEnv(t, populateEnv(dsn)))
	base := startDemoServe(t, bin, dsn)
	client := &http.Client{Timeout: 20 * time.Second}

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
	})
}
