package tasks

// cost_gate_skid_test.go, the skid answer's deposit pair through the real handler: the pair rides
// only for a role on the cost and margin list, byte-absent for every other role, with the count kept.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

func TestSkidAnswerDepositFollowsTheCostList(t *testing.T) {
	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatal(err)
	}
	h, taskID := heldTicketSvc(t, cfg)
	put := func(role string) map[string]any {
		r, w := yardRequest(t, taskID, skidBody, yardCaller(role, identity.ScopeBranch, branchA))
		h.recordSkids(w, r, guards.StaffCaller{})
		if w.Code != http.StatusOK {
			t.Fatalf("skids as %s: %d %s", role, w.Code, w.Body.String())
		}
		var m map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	for _, role := range rolesOffTheList {
		m := put(role)
		for _, k := range []string{"deposit_cents", "previous_deposit_cents"} {
			if _, has := m[k]; has {
				t.Errorf("%s must not receive %s: %v", role, k, m)
			}
		}
		if m["skid_count"] != float64(2) || m["changed"] != true || m["order_no"] == nil {
			t.Errorf("%s keeps the count: %v", role, m)
		}
	}
	for _, role := range rolesOnTheList {
		m := put(role)
		if m["deposit_cents"] != float64(10000) || m["previous_deposit_cents"] != float64(15000) {
			t.Errorf("%s is on the list and keeps the pair: %v", role, m)
		}
	}
}

// A listed role whose deposit is zero is told zero: the pair is a pointer, so zero is an answer.
func TestSkidAnswerZeroDepositIsAnAnswerForAListedRole(t *testing.T) {
	cfg, _ := roleconfig.Default()
	svc, taskID := pickRecordSvc(t)
	svc.WithYardOrderChanges(&fakeYardOrder{skidResult: &YardSkidResult{CountAfter: 0, Changed: true}})
	h := NewHandler(svc, nil).WithCostRoles(cfg)
	r, w := yardRequest(t, taskID, `{"skid_count":0,"reason":"CONDENSED_SKIDS"}`, yardCaller("YARD_MANAGER", identity.ScopeBranch, branchA))
	h.recordSkids(w, r, guards.StaffCaller{})
	if !strings.Contains(w.Body.String(), `"deposit_cents":0`) {
		t.Fatalf("a listed role's zero deposit is an answer: %s", w.Body.String())
	}
}

// No rule wired, an unreadable rule and a rule that names nobody all hold the pair from everyone.
func TestSkidAnswerFailsClosed(t *testing.T) {
	none := costRolesFunc(func(string) (bool, error) { return false, nil })
	broken := costRolesFunc(func(string) (bool, error) { return true, context.DeadlineExceeded })
	for name, roles := range map[string]CostRoles{"off the list": none, "unreadable": broken, "unwired": nil} {
		h, taskID := heldTicketSvc(t, roles)
		r, w := yardRequest(t, taskID, skidBody, yardCaller("YARD_MANAGER", identity.ScopeBranch, branchA))
		h.recordSkids(w, r, guards.StaffCaller{})
		if strings.Contains(w.Body.String(), "deposit_cents") {
			t.Errorf("%s: %s", name, w.Body.String())
		}
	}
}
