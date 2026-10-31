//go:build integration

package main

// saved_reports_routes_integration_test.go, W3-RW2 fix round: the seven saved report routes over the served
// surface (registeredSurface), so a later edit to a handler or a census row cannot drop the owner gate or
// the role gate unnoticed. It covers the stranger's 404s, malformed ids, the inside sales 403, a body owner
// ignored, reach moved in user_branch_grants (not the role grant's branch) and the role moved. Every row it
// makes is deleted, and every restore fails the test loudly.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestSavedReportsRoutes_OwnerGateReachAndRoleAtTheServedSurface(t *testing.T) {
	env := newCompEnv(t)
	rec, graph := registeredSurface(t, env)
	ctx := context.Background()
	const gm, sm, is = testkit.SubGeneralMgr, testkit.SubSalesManager, testkit.SubInsideSales
	base := "/api/v1/reporting/saved-reports"
	const tag = "RW2HTTP "

	do := func(sub, method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+env.minter.StaffToken(t, sub))
		req.Header.Set(authz.HeaderXOrg, testkit.OrgNorthSlug)
		req.Header.Set("Content-Type", "application/json")
		rsp := httptest.NewRecorder()
		rec.inner.ServeHTTP(rsp, req)
		return rsp
	}
	want := func(label string, r *httptest.ResponseRecorder, code int) {
		t.Helper()
		if r.Code != code {
			t.Fatalf("%s: status %d, want %d: %s", label, r.Code, code, strings.TrimSpace(r.Body.String()))
		}
	}
	created := func(label string, r *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		if r.Code != http.StatusCreated && r.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", label, r.Code, r.Body.String())
		}
		var m map[string]any
		if err := json.Unmarshal(r.Body.Bytes(), &m); err != nil {
			t.Fatalf("%s: body: %v", label, err)
		}
		return m
	}
	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := graph.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	t.Cleanup(func() {
		bg := context.Background()
		steps := []struct {
			what, sql string
			args      []any
		}{
			{"staff_role_grant", `UPDATE staff_role_grant SET role_id='sales_manager', branch_id=$2::uuid WHERE id=$1::uuid`,
				[]any{testkit.GrantSalesMgrID, testkit.BranchNorthAID}},
			{"user_branch_grants restore", `UPDATE user_branch_grants SET deleted_at=NULL WHERE user_id=$1::uuid AND branch_id=$2::uuid`,
				[]any{testkit.UserSalesMgrID, testkit.BranchNorthAID}},
			{"user_branch_grants remove B", `DELETE FROM user_branch_grants WHERE user_id=$1::uuid AND branch_id=$2::uuid`,
				[]any{testkit.UserSalesMgrID, testkit.BranchNorthBID}},
			{"report_saved_reports", `DELETE FROM report_saved_reports WHERE name LIKE $1`, []any{tag + "%"}},
		}
		for _, s := range steps {
			if _, err := graph.pool.Exec(bg, s.sql, s.args...); err != nil {
				t.Errorf("cleanup %s: %v", s.what, err)
			}
		}
	})

	// The sales manager's reach starts at North A only; the test moves it later.
	var reachRows int
	if err := graph.pool.QueryRow(ctx, `SELECT count(*) FROM user_branch_grants WHERE user_id=$1::uuid AND branch_id=$2::uuid AND deleted_at IS NULL`,
		testkit.UserSalesMgrID, testkit.BranchNorthAID).Scan(&reachRows); err != nil || reachRows != 1 {
		t.Fatalf("the fixture must give the sales manager reach to North A: rows=%d err=%v", reachRows, err)
	}

	// A body owner and a chosen id are ignored: the caller owns it, under a fresh id.
	gmRep := created("gm create", do(gm, "POST", base,
		`{"name":"`+tag+`gm","dataset":"ar","columns":["customer_name","branch_id","total_cents"],"owner_user_id":"`+
			testkit.UserSalesMgrID+`","id":"00000000-0000-4000-8000-000000000001"}`))
	id, _ := gmRep["id"].(string)
	if id == "" || id == "00000000-0000-4000-8000-000000000001" {
		t.Fatalf("the id is the store's, not the body's: %q", id)
	}
	if owner, _ := gmRep["owner_user_id"].(string); owner != testkit.UserGeneralMgrID {
		t.Fatalf("the owner is the caller %s, got %q", testkit.UserGeneralMgrID, owner)
	}

	// The stranger: 404 on all seven routes, and the owner's report is untouched.
	r := do(sm, "GET", base, "")
	want("stranger list", r, http.StatusOK)
	if strings.Contains(r.Body.String(), id) {
		t.Fatalf("the stranger's list must not hold the owner's report: %s", r.Body.String())
	}
	want("stranger get", do(sm, "GET", base+"/"+id, ""), http.StatusNotFound)
	want("stranger put", do(sm, "PUT", base+"/"+id, `{"name":"`+tag+`hijack","dataset":"ar","columns":["customer_name"]}`), http.StatusNotFound)
	want("stranger run", do(sm, "POST", base+"/"+id+"/run", ""), http.StatusNotFound)
	want("stranger export", do(sm, "GET", base+"/"+id+"/export.csv", ""), http.StatusNotFound)
	want("stranger delete", do(sm, "DELETE", base+"/"+id, ""), http.StatusNotFound)
	own := do(gm, "GET", base+"/"+id, "")
	want("owner get after the stranger's attempts", own, http.StatusOK)
	if !strings.Contains(own.Body.String(), tag+"gm") || strings.Contains(own.Body.String(), "hijack") {
		t.Fatalf("the owner's report is unchanged: %s", own.Body.String())
	}

	// Malformed and guessed ids all answer 404 on get and run.
	for _, bad := range []string{"not-a-uuid", "00000000-0000-4000-8000-00000000000g", "1%27%20OR%20%271%27=%271",
		"00000000-0000-4000-8000-000000000002", "%00"} {
		want("get bad id "+bad, do(gm, "GET", base+"/"+bad, ""), http.StatusNotFound)
		want("run bad id "+bad, do(gm, "POST", base+"/"+bad+"/run", ""), http.StatusNotFound)
	}

	// Inside sales (tier 3) is refused the list and the create.
	want("inside sales list", do(is, "GET", base, ""), http.StatusForbidden)
	want("inside sales create", do(is, "POST", base, `{"name":"`+tag+`is","dataset":"ar","columns":["customer_name"]}`), http.StatusForbidden)

	// The owner runs, exports, deletes; a second delete and a run after are 404.
	want("owner run", do(gm, "POST", base+"/"+id+"/run", ""), http.StatusOK)
	exp := do(gm, "GET", base+"/"+id+"/export.csv", "")
	want("owner export", exp, http.StatusOK)
	if !strings.HasPrefix(exp.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("export content type: %q", exp.Header().Get("Content-Type"))
	}
	want("owner delete", do(gm, "DELETE", base+"/"+id, ""), http.StatusNoContent)
	want("owner delete again", do(gm, "DELETE", base+"/"+id, ""), http.StatusNotFound)
	want("run after delete", do(gm, "POST", base+"/"+id+"/run", ""), http.StatusNotFound)

	// Reach: a saved report filtered to North A, then the reach moved in user_branch_grants.
	aFilter := `"filters":[{"column":"branch_id","op":"eq","values":["` + testkit.BranchNorthAID + `"]}]`
	smRep := created("sm create A", do(sm, "POST", base,
		`{"name":"`+tag+`sm","dataset":"ar","columns":["customer_name","branch_id","total_cents"],`+aFilter+`}`))
	smID, _ := smRep["id"].(string)
	want("sm run own at A", do(sm, "POST", base+"/"+smID+"/run", ""), http.StatusOK)
	want("sm export own at A", do(sm, "GET", base+"/"+smID+"/export.csv", ""), http.StatusOK)
	smRep2 := created("sm create ap", do(sm, "POST", base,
		`{"name":"`+tag+`sm2","dataset":"ap","columns":["vendor_name","branch_id","open_cents"]}`))
	smID2, _ := smRep2["id"].(string)

	exec("soft delete reach A", `UPDATE user_branch_grants SET deleted_at=now() WHERE user_id=$1::uuid AND branch_id=$2::uuid`,
		testkit.UserSalesMgrID, testkit.BranchNorthAID)
	exec("add reach B", `INSERT INTO user_branch_grants (user_id, branch_id, org_id) VALUES ($1::uuid,$2::uuid,$3::uuid)
		ON CONFLICT (user_id, branch_id) DO UPDATE SET deleted_at=NULL`, testkit.UserSalesMgrID, testkit.BranchNorthBID, testkit.OrgNorthID)
	want("run the saved A report with reach at B", do(sm, "POST", base+"/"+smID+"/run", ""), http.StatusForbidden)
	want("export the saved A report with reach at B", do(sm, "GET", base+"/"+smID+"/export.csv", ""), http.StatusForbidden)
	direct := func(branch string) *httptest.ResponseRecorder {
		return do(sm, "POST", "/api/v1/reporting/datasets/ar/read",
			`{"columns":["customer_name"],"filters":[{"column":"branch_id","op":"eq","values":["`+branch+`"]}]}`)
	}
	want("direct read at A with reach at B", direct(testkit.BranchNorthAID), http.StatusForbidden)
	want("direct read at B with reach at B", direct(testkit.BranchNorthBID), http.StatusOK)
	want("create filtered to A with reach at B", do(sm, "POST", base,
		`{"name":"`+tag+`smA2","dataset":"ar","columns":["customer_name"],`+aFilter+`}`), http.StatusForbidden)

	// Role: the same caller moved to a manager tier role the money desk refuses.
	exec("move role", `UPDATE staff_role_grant SET role_id='logistics_manager' WHERE id=$1::uuid`, testkit.GrantSalesMgrID)
	want("list own as logistics manager", do(sm, "GET", base, ""), http.StatusOK)
	want("get own as logistics manager", do(sm, "GET", base+"/"+smID, ""), http.StatusOK)
	want("run own ar as logistics manager", do(sm, "POST", base+"/"+smID+"/run", ""), http.StatusForbidden)
	want("export own ar as logistics manager", do(sm, "GET", base+"/"+smID+"/export.csv", ""), http.StatusForbidden)
	want("run own ap as logistics manager", do(sm, "POST", base+"/"+smID2+"/run", ""), http.StatusForbidden)
	want("direct read ar as logistics manager", do(sm, "POST", "/api/v1/reporting/datasets/ar/read", `{"columns":["customer_name"]}`), http.StatusForbidden)
	want("create ar as logistics manager", do(sm, "POST", base, `{"name":"`+tag+`lm","dataset":"ar","columns":["customer_name"]}`), http.StatusForbidden)
	want("replace own to stock as logistics manager", do(sm, "PUT", base+"/"+smID2,
		`{"name":"`+tag+`sm2","dataset":"stock","columns":["product_id","unit_cost_cents"]}`), http.StatusForbidden)
}
