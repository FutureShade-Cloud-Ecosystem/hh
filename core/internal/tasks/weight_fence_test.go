package tasks

// weight_fence_test.go, UX-81c: the weight request door holds the pick door's fences. The caller's branch reach and
// the order's org decide whether an order exists for the caller, and the stored task carries the caller's org.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

func TestRequestWeightHoldsTheCallersOrgAndBranchReach(t *testing.T) {
	const north, south = "3ef10000-0000-4000-8000-000000000001", "3ef10000-0000-4000-8000-000000000002"
	const otherBranch = "99999999-9999-4999-8999-999999999999"
	post := func(sc identity.StaffContext, org, order string) (*fakeStore, int, string) {
		svc, store, _ := newSvc(t)
		svc.WithOrderReader(orgOrderReader{owner: map[string]string{knownOrder: south}})
		h := NewHandler(svc, nil).WithBranchOrgReader(branchesOfOrg{branchA: {south: true, north: true}})
		r := httptest.NewRequest(http.MethodPost, "/api/v1/yard/weight-requests",
			bytes.NewBufferString(`{"order_id":"`+order+`","order_line_no":1}`))
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(identity.ContextWithStaff(r.Context(), sc))
		w := httptest.NewRecorder()
		h.requestWeight(w, r, guards.StaffCaller{StaffScope: authz.StaffScope{OrgID: org}})
		return store, w.Code, strings.TrimSpace(w.Body.String())
	}
	global := identity.StaffContext{UserID: "eeeeeeee-0000-4000-8000-000000000001", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive}
	scoped := func(branch string) identity.StaffContext {
		return identity.StaffContext{UserID: "eeeeeeee-0000-4000-8000-000000000001", RoleCode: "YARD_TEAM", Tier: identity.TierFrontline,
			Scope: identity.ScopeBranch, Status: identity.StatusActive, Branches: map[string]bool{branch: true}}
	}

	_, unknownCode, unknownBody := post(global, south, "33333333-3333-4333-8333-333333333333")
	if unknownCode != http.StatusUnprocessableEntity || !strings.Contains(unknownBody, "order not found") {
		t.Fatalf("the unknown order answer is the pattern: %d %s", unknownCode, unknownBody)
	}
	store, code, body := post(global, north, knownOrder)
	if code != unknownCode || body != unknownBody || len(store.created) != 0 {
		t.Errorf("north naming south's order answers as an unknown order and writes nothing: %d %s, %d rows", code, body, len(store.created))
	}
	store, code, body = post(scoped(otherBranch), south, knownOrder)
	if code != unknownCode || body != unknownBody || len(store.created) != 0 {
		t.Errorf("an out of reach branch answers as an unknown order and writes nothing: %d %s, %d rows", code, body, len(store.created))
	}
	store, code, body = post(scoped(branchA), south, knownOrder)
	if code != http.StatusCreated || len(store.created) != 1 || store.created[0].OrgID != south {
		t.Errorf("the owning org on its own branch creates a task that carries its org: %d %s, %+v", code, body, store.created)
	}
}
