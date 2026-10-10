//go:build integration

// cost_gate_sheet_integration_test.go: the X3 cost gate over the count
// sheet detail, the read a counter at the yard opens to count against a
// frozen sheet. The lines carry the branch average they were frozen at
// and the value of each variance, and the summary rolls those values
// up; variance value over variance quantity names the average cost, so
// every one of those legs is held out for a caller the cost rule
// refuses (byte-absent, never a zero that reads as free).
package invcount

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

func TestIntegration_CostGate_SheetDetailHoldsCostOutByRole(t *testing.T) {
	pool := intPool(t)
	stock := &fakeStockSeam{avg: 350, snaps: []Snapshot{{ProductID: prodP1, LocationID: branchTRT, OnHandBase: 100, AvgCostCents: 350}}}
	stock.cleanupMoves(t, pool)
	svc := realSvc(pool, stock)
	roles, err := roleconfig.Default()
	if err != nil {
		t.Fatalf("roleconfig.Default: %v", err)
	}
	h := NewHandler(svc, nil).WithCostRoles(roles)

	// A posted sheet, so the lines carry a frozen average AND a variance
	// value, and the summary carries both rolled-up values.
	sheet, err := svc.CreateSheet(ctxAs(userMaker), CreateSheetRequest{BranchID: branchTRT})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.FreezeSheet(ctxAs(userMaker), sheet.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SubmitCounts(ctxAs(userMaker), sheet.ID, SubmitCountsRequest{
		Lines: []CountLineInput{{ProductID: prodP1, LocationID: branchTRT, CountedQtyBase: 97, ReasonCode: "DAMAGE"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApproveSheet(ctxAs(userCheck), sheet.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.PostSheet(ctxAs(userCheck), sheet.ID); err != nil {
		t.Fatal(err)
	}

	read := func(role string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/counts/sheets/"+sheet.ID, nil)
		req.SetPathValue("id", sheet.ID)
		req = req.WithContext(identity.ContextWithStaff(context.Background(), identity.StaffContext{
			RoleCode: role, Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
			Status: identity.StatusActive, Branches: map[string]bool{branchTRT: true},
		}))
		w := httptest.NewRecorder()
		h.sheetDetail(w, req, guards.StaffCaller{})
		if w.Code != http.StatusOK {
			t.Fatalf("sheet detail answered %d: %s", w.Code, w.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		return body
	}
	lineLegs := []string{"unit_cost_cents", "variance_value_cents"}
	summaryLegs := []string{"net_variance_value_cents", "abs_variance_value_cents"}
	has := func(m map[string]any, key string) bool { _, ok := m[key]; return ok }
	firstLine := func(body map[string]any) map[string]any {
		raw, _ := body["lines"].([]any)
		if len(raw) == 0 {
			t.Fatalf("no lines in %v", body)
		}
		m, _ := raw[0].(map[string]any)
		return m
	}

	yard := read("YARD_TEAM")
	for _, k := range lineLegs {
		if has(firstLine(yard), k) {
			t.Errorf("the yard caller's sheet line carries %s: %v", k, firstLine(yard))
		}
	}
	for _, k := range summaryLegs {
		if sum, _ := yard["summary"].(map[string]any); has(sum, k) {
			t.Errorf("the yard caller's variance summary carries %s: %v", k, sum)
		}
	}
	clerk := read("INSIDE_SALES")
	for _, k := range lineLegs {
		if !has(firstLine(clerk), k) {
			t.Errorf("the counter caller's sheet line lost %s: %v", k, firstLine(clerk))
		}
	}
	for _, k := range summaryLegs {
		if sum, _ := clerk["summary"].(map[string]any); !has(sum, k) {
			t.Errorf("the counter caller's variance summary lost %s: %v", k, sum)
		}
	}
}
