//go:build integration

// cost_gate_w3_integration_test.go, the door money fence through the real
// handlers on both payment hold routes, per role: a driver and every role off
// the cost and margin list receive the hold in words with the figure
// byte-absent, and the dispatch roles on the list keep the figure and the
// gate's own sentence. The gate denies through a figure in cents and in words
// (denyGate names both), so the legs prove neither shape of the figure rides.
//
//	DATABASE_URL=... go test -tags integration -count=1 -run DoorMoney ./internal/dispatch/

package dispatch

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// doorHoldWorld is the fence's proof world: one held order's stop on one run,
// behind a gate that refuses it naming a figure in cents and in words, and
// the service wired with the real cost rule.
func doorHoldWorld(t *testing.T, wireRule bool) (*Handler, string, string) {
	t.Helper()
	h, ctx, orderID := w3Harness(t)
	stop := w3Schedule(t, h, ctx, orderID, tomorrow())
	run := seedRun(t, h, ctx, branchTRT, tomorrow(), true)
	if _, err := h.svc.AssignStop(ctx, stop.ID, AssignInput{RunID: run.ID}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	h.svc.WithARGate(denyGate{orders: map[string]bool{orderID: true}})
	if wireRule {
		cfg, err := roleconfig.Default()
		if err != nil {
			t.Fatal(err)
		}
		h.svc.WithCostRoles(cfg)
	}
	return NewHandler(h.svc, nil), stop.ID, run.ID
}

// doorHoldGet drives one payment hold route as one role, the guard's stamp
// already on the request the way h.staff puts it there.
func doorHoldGet(t *testing.T, h *Handler, role, path, id string, do func(http.ResponseWriter, *http.Request)) string {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.SetPathValue("id", id)
	sc := identity.StaffContext{
		UserID: "eeeeeeee-0000-4000-8000-000000000001", RoleCode: role,
		Tier: identity.TierFrontline, Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	}
	r = r.WithContext(identity.ContextWithStaff(r.Context(), sc))
	w := httptest.NewRecorder()
	do(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("%s as %s: %d %s", path, role, w.Code, w.Body.String())
	}
	return w.Body.String()
}

func TestDoorMoney_BothHoldRoutesFollowTheCostList(t *testing.T) {
	h, stopID, runID := doorHoldWorld(t, true)
	routes := map[string]struct {
		path string
		id   string
		do   func(http.ResponseWriter, *http.Request)
	}{
		"the stop's hold": {
			"/api/v1/dispatch/stops/" + stopID + "/payment-hold", stopID,
			func(w http.ResponseWriter, r *http.Request) { h.stopPaymentHold(w, r, guards.StaffCaller{}) },
		},
		"the run's holds": {
			"/api/v1/dispatch/runs/" + runID + "/payment-holds", runID,
			func(w http.ResponseWriter, r *http.Request) { h.runPaymentHolds(w, r, guards.StaffCaller{}) },
		},
	}
	for name, route := range routes {
		for _, role := range doorRolesOffTheList {
			body := doorHoldGet(t, h, role, route.path, route.id, route.do)
			for _, leak := range []string{"amount_due_cents", "124000", "1,240", "COD balance"} {
				if strings.Contains(body, leak) {
					t.Errorf("%s as %s must not carry the figure in any shape (%q): %s", name, role, leak, body)
				}
			}
			for _, want := range []string{doorMoneyWords, `"held":true`, `"code":"COD_UNPAID"`} {
				if !strings.Contains(body, want) {
					t.Errorf("%s as %s keeps the hold, its code and its words (%q): %s", name, role, want, body)
				}
			}
		}
		for _, role := range doorRolesOnTheList {
			body := doorHoldGet(t, h, role, route.path, route.id, route.do)
			if !strings.Contains(body, `"amount_due_cents":124000`) || !strings.Contains(body, "COD balance") {
				t.Errorf("%s as %s is on the list and keeps the figure and the gate's sentence: %s", name, role, body)
			}
		}
	}
}

// No rule wired fails closed: the dispatch role itself is told the words and
// carries no figure, the posture the composition root's boot fail-fast keeps
// out of production.
func TestDoorMoney_BothHoldRoutesFailClosed(t *testing.T) {
	h, stopID, runID := doorHoldWorld(t, false)
	routes := map[string]struct {
		path string
		id   string
		do   func(http.ResponseWriter, *http.Request)
	}{
		"the stop's hold": {
			"/api/v1/dispatch/stops/" + stopID + "/payment-hold", stopID,
			func(w http.ResponseWriter, r *http.Request) { h.stopPaymentHold(w, r, guards.StaffCaller{}) },
		},
		"the run's holds": {
			"/api/v1/dispatch/runs/" + runID + "/payment-holds", runID,
			func(w http.ResponseWriter, r *http.Request) { h.runPaymentHolds(w, r, guards.StaffCaller{}) },
		},
	}
	for name, route := range routes {
		body := doorHoldGet(t, h, "LOGISTICS_MANAGER", route.path, route.id, route.do)
		if strings.Contains(body, "amount_due_cents") || strings.Contains(body, "COD balance") {
			t.Errorf("%s with no rule wired must hold the figure from everyone: %s", name, body)
		}
		if !strings.Contains(body, doorMoneyWords) {
			t.Errorf("%s with no rule wired still tells the hold in words: %s", name, body)
		}
	}
}
