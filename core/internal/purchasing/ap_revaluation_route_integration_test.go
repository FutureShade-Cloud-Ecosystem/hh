//go:build integration

package purchasing

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	erpidentity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// W3 FX-2b: the Controller's routes. The gate is the leadership tier narrowed to the Controller
// roles (HR is leadership but not a Controller), the press is recorded with who pressed it, and
// the list reads the branch's runs.
func TestIntegration_W3FX2B_ControllerRoutes(t *testing.T) {
	rig := fx2Rig(t)
	fx2World(t, rig)
	rig.svc.withCaller(identity.StaffContextFromContext)

	sg := testkit.NewStaffGuards(t)
	mux := http.NewServeMux()
	NewHandler(rig.svc, slog.Default()).WithUserLinker(func(context.Context, string) (string, bool) {
		return rig.user, true
	}).RegisterRoutes(mux, sg.Guards)

	call := func(auth erpidentity.StaffAuthority, method, path, body string) *httptest.ResponseRecorder {
		sg.SetAuthority(auth)
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
		req.Header.Set("X-Org", testkit.OrgNorthSlug)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	controller := erpidentity.StaffAuthority{RoleID: "FINANCIAL_CONTROLLER", Tier: erpidentity.TierLeadership, Scope: erpidentity.ScopeGlobal}
	hr := erpidentity.StaffAuthority{RoleID: "HR", Tier: erpidentity.TierLeadership, Scope: erpidentity.ScopeGlobal}
	branchMgr := erpidentity.StaffAuthority{RoleID: "BRANCH_MANAGER", Tier: erpidentity.TierManager, Scope: erpidentity.ScopeBranch, Branches: []string{branchTRT}}
	clerk := erpidentity.StaffAuthority{RoleID: "COUNTER_SALES", Tier: erpidentity.TierFrontline, Scope: erpidentity.ScopeBranch, Branches: []string{branchTRT}}

	runBody := `{"branch_id":"` + branchTRT + `","month":"` + fx2Month + `","fx_rate":1.4}`
	runPath := "/api/v1/purchasing/ap-revaluations"
	revPath := runPath + "/" + fx2Month + "/reversal"
	revBody := `{"branch_id":"` + branchTRT + `"}`
	listPath := runPath + "?branch_id=" + branchTRT

	// The gate: only the Controller roles press, reverse or read.
	for name, who := range map[string]erpidentity.StaffAuthority{"clerk": clerk, "branch manager": branchMgr, "hr": hr} {
		for _, c := range []struct{ method, path, body string }{{"POST", runPath, runBody}, {"POST", revPath, revBody}, {"GET", listPath, ""}} {
			if rec := call(who, c.method, c.path, c.body); rec.Code != http.StatusForbidden {
				t.Fatalf("W3-FX2b red: %s on %s %s = %d (%s), want 403", name, c.method, c.path, rec.Code, rec.Body.String())
			}
		}
	}
	if n := fx2JournalCount(t, rig, "purchasing.ap.revaluation:"+branchTRT+":"+fx2Month); n != 0 {
		t.Fatalf("W3-FX2b red: a refused caller left %d journals", n)
	}
	if row := fx2bRun(t, rig, branchTRT, fx2Month); row.Found {
		t.Fatalf("W3-FX2b red: a refused caller left a run record: %+v", row)
	}

	// The Controller presses the month with its rate.
	rec := call(controller, "POST", runPath, runBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("W3-FX2b red: the Controller's press = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var run RevalueAPRun
	if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil || !run.Posted || run.DifferenceCents != fx2RunDiff || run.JournalID == "" {
		t.Fatalf("W3-FX2b red: the press answered %+v (%v), want the posted run with difference %d", run, err, fx2RunDiff)
	}
	// The press is recorded with who pressed it.
	row := fx2bRun(t, rig, branchTRT, fx2Month)
	if !row.Found || row.CreatedBy != rig.user || row.JournalID != run.JournalID {
		t.Fatalf("W3-FX2b red: the run record = %+v, want it pressed by %s with journal %s", row, rig.user, run.JournalID)
	}
	// A bad rate and a spent key answer in words.
	if rec := call(controller, "POST", runPath, `{"branch_id":"`+branchTRT+`","month":"`+fx2Month+`","fx_rate":1.41}`); rec.Code != http.StatusConflict {
		t.Fatalf("W3-FX2b red: another rate on the spent month = %d (%s), want 409", rec.Code, rec.Body.String())
	}
	if rec := call(controller, "POST", runPath, `{"branch_id":"`+branchTRT+`","month":"`+fx2Month+`"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("W3-FX2b red: a press without a rate = %d (%s), want 400", rec.Code, rec.Body.String())
	}

	// Read the branch's runs.
	rec = call(controller, "GET", listPath, "")
	var list struct {
		Items []APRevaluationRunRecord `json:"items"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &list) != nil || len(list.Items) != 1 ||
		list.Items[0].Month != fx2Month || list.Items[0].DifferenceCents != fx2RunDiff || list.Items[0].CreatedBy != rig.user || list.Items[0].ReversalJournalID != "" {
		t.Fatalf("W3-FX2b red: the list = %d %s, want one unreversed run for %s", rec.Code, rec.Body.String(), fx2Month)
	}
	if rec := call(controller, "GET", runPath, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("W3-FX2b red: a list without a branch = %d (%s), want 400", rec.Code, rec.Body.String())
	}

	// Press the reversal; the record takes it.
	rec = call(controller, "POST", revPath, revBody)
	var rev APRevaluationReversal
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &rev) != nil || rev.ReversalJournalID == "" || rev.DifferenceCents != fx2RunDiff {
		t.Fatalf("W3-FX2b red: the reversal press = %d (%s), want 200 with the reversal journal", rec.Code, rec.Body.String())
	}
	row = fx2bRun(t, rig, branchTRT, fx2Month)
	if row.ReversalID != rev.ReversalJournalID || row.ReversedBy != rig.user || !row.HasReversed {
		t.Fatalf("W3-FX2b red: after the reversal press the record = %+v, want it reversed by %s", row, rig.user)
	}
	rec = call(controller, "GET", listPath, "")
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &list) != nil || len(list.Items) != 1 || list.Items[0].ReversalJournalID != rev.ReversalJournalID {
		t.Fatalf("W3-FX2b red: the list after the reversal = %d %s", rec.Code, rec.Body.String())
	}
}
