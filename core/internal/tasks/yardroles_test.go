package tasks

// yardroles_test.go, the yard's recording doors through the real handlers: the caller must reach the
// task's branch, a yard manager is held to the ordered quantity exactly as the yard team is, and a
// refused request changes nothing.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

func yardCaller(role string, scope identity.Scope, branches ...string) identity.StaffContext {
	b := map[string]bool{}
	for _, id := range branches {
		b[id] = true
	}
	return identity.StaffContext{UserID: "eeeeeeee-0000-4000-8000-000000000001", RoleCode: role,
		Tier: identity.TierFrontline, Scope: scope, Status: identity.StatusActive, Branches: b}
}

func yardRequest(t *testing.T, taskID, body string, sc identity.StaffContext) (*http.Request, *httptest.ResponseRecorder) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/yard/picks/"+taskID, bytes.NewBufferString(body))
	r.SetPathValue("id", taskID)
	r.Header.Set("Content-Type", "application/json")
	return r.WithContext(identity.ContextWithStaff(r.Context(), sc)), httptest.NewRecorder()
}

const (
	otherBranch = "99999999-9999-4999-8999-999999999999"
	shortBody   = `{"lines":[{"order_line_no":1,"qty_ordered":"5","qty_picked":"3","uom":"EA","short_reason":"CONDENSED_SKIDS"}]}`
	raiseBody   = `{"lines":[{"order_line_no":1,"qty_ordered":"5","qty_picked":"6","uom":"EA","short_reason":"COUNT_CORRECTION"}]}`
	skidBody    = `{"skid_count":2,"reason":"CONDENSED_SKIDS"}`
)

func TestYardHandlers_AnotherBranchsCallerIsRefusedAndNothingChanges(t *testing.T) {
	svc, taskID, fy := yardShortSvc(t)
	h := NewHandler(svc, nil)
	for _, c := range []struct {
		name string
		do   func(*http.Request, *httptest.ResponseRecorder)
		body string
	}{
		{"lines", func(r *http.Request, w *httptest.ResponseRecorder) { h.recordPick(w, r, guards.StaffCaller{}) }, shortBody},
		{"skids", func(r *http.Request, w *httptest.ResponseRecorder) { h.recordSkids(w, r, guards.StaffCaller{}) }, skidBody},
	} {
		r, w := yardRequest(t, taskID, c.body, yardCaller("YARD_TEAM", identity.ScopeBranch, otherBranch))
		c.do(r, w)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: a yard caller who cannot reach the task's branch must be refused, got %d %s", c.name, w.Code, w.Body.String())
		}
	}
	if len(fy.picks) != 0 || len(fy.skids) != 0 {
		t.Fatalf("a refused caller changes nothing: %+v %+v", fy.picks, fy.skids)
	}
	if got, _ := svc.ListPickedLines(t.Context(), taskID); len(got) != 0 {
		t.Fatalf("and records nothing: %+v", got)
	}
}

func TestYardHandlers_TheTasksBranchAndGlobalCallersAreAdmitted(t *testing.T) {
	svc, taskID, _ := yardShortSvc(t)
	h := NewHandler(svc, nil)
	for _, sc := range []identity.StaffContext{
		yardCaller("YARD_TEAM", identity.ScopeBranch, branchA),
		yardCaller("YARD_MANAGER", identity.ScopeBranch, branchA),
		yardCaller("YARD_MANAGER", identity.ScopeGlobal),
	} {
		r, w := yardRequest(t, taskID, shortBody, sc)
		h.recordPick(w, r, guards.StaffCaller{})
		if w.Code != http.StatusOK {
			t.Errorf("%s (%v) must record a short pick, got %d %s", sc.RoleCode, sc.Scope, w.Code, w.Body.String())
		}
		r, w = yardRequest(t, taskID, skidBody, sc)
		h.recordSkids(w, r, guards.StaffCaller{})
		if w.Code != http.StatusOK {
			t.Errorf("%s (%v) must record a skid count, got %d %s", sc.RoleCode, sc.Scope, w.Code, w.Body.String())
		}
	}
}

func TestYardHandlers_NobodyRaisesAboveOrderedNotEvenTheYardManager(t *testing.T) {
	svc, taskID, fy := yardShortSvc(t)
	h := NewHandler(svc, nil)
	for _, role := range []string{"YARD_TEAM", "YARD_MANAGER"} {
		r, w := yardRequest(t, taskID, raiseBody, yardCaller(role, identity.ScopeBranch, branchA))
		h.recordPick(w, r, guards.StaffCaller{})
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s raising 5 to 6 must be refused with 400, got %d %s", role, w.Code, w.Body.String())
		}
	}
	// A doctored ticket quantity does not help: the order's own quantity decides.
	fy.err = ErrAboveOrdered
	doctored := `{"lines":[{"order_line_no":1,"qty_ordered":"50","qty_picked":"6","uom":"EA","short_reason":"COUNT_CORRECTION"}]}`
	r, w := yardRequest(t, taskID, doctored, yardCaller("YARD_MANAGER", identity.ScopeBranch, branchA))
	h.recordPick(w, r, guards.StaffCaller{})
	if w.Code != http.StatusBadRequest {
		t.Errorf("a ticket quantity of 50 cannot talk past the order's 5: got %d %s", w.Code, w.Body.String())
	}
	if got, _ := svc.ListPickedLines(t.Context(), taskID); len(got) != 0 {
		t.Fatalf("a refused raise records nothing: %+v", got)
	}
}
