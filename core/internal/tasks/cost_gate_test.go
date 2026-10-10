package tasks

// cost_gate_test.go, the yard fence's money half through the real handlers: the held ticket's
// still owed figure and the skid answer's deposit pair ride only for a role on the cost and margin
// list, byte-absent for every other role, with the ticket's words and the skid count kept.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// moneyRoles is the role mix the fence is proven for: the roles off the default list and the roles
// on it. The default list is read from the real configuration, not restated here.
var (
	rolesOffTheList = []string{"YARD_TEAM", "DRIVERS", "HR"}
	rolesOnTheList  = []string{"YARD_MANAGER", "BRANCH_MANAGER", "GENERAL_MANAGER"}
)

// heldTicketSvc seeds a pick task whose ticket is held for payment with a figure owed, behind a
// stub pick source, and returns the handler wired with the real cost rule.
func heldTicketSvc(t *testing.T, roles CostRoles) (*Handler, string) {
	t.Helper()
	svc, taskID := pickRecordSvc(t)
	svc.WithYardOrderChanges(&fakeYardOrder{skidResult: &YardSkidResult{
		CountAfter: 2, DepositBeforeCents: 15000, DepositAfterCents: 10000, Changed: true}})
	held := PickTicket{
		OrderID: "aaaaaaaa-0000-4000-8000-000000000001", BranchID: branchA, Source: "STUB",
		Lines:           []PickLine{{LineNo: 1, Description: "Armour stone", QtyOrdered: "5", UOM: "EA"}},
		HeldFromPicking: true, HoldWords: "Balance due", StillOwedCents: 12345,
	}
	svc.WithPickSource(NewStrictStubPickSource().Seed(held))
	h := NewHandler(svc, nil)
	if roles != nil {
		h.WithCostRoles(roles)
	}
	return h, taskID
}

func getAs(t *testing.T, role, path string, do func(http.ResponseWriter, *http.Request)) string {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.SetPathValue("id", "")
	sc := yardCaller(role, identity.ScopeBranch, branchA)
	r = r.WithContext(identity.ContextWithStaff(r.Context(), sc))
	w := httptest.NewRecorder()
	do(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("%s as %s: %d %s", path, role, w.Code, w.Body.String())
	}
	return w.Body.String()
}

func TestHeldTicketStillOwedFollowsTheCostList(t *testing.T) {
	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatal(err)
	}
	h, taskID := heldTicketSvc(t, cfg)
	routes := map[string]func(http.ResponseWriter, *http.Request){
		"/api/v1/yard/pick-queue?branch_id=" + branchA:      func(w http.ResponseWriter, r *http.Request) { h.pickQueue(w, r, guards.StaffCaller{}) },
		"/api/v1/yard/pending-tickets?branch_id=" + branchA: func(w http.ResponseWriter, r *http.Request) { h.pendingTickets(w, r, guards.StaffCaller{}) },
		"/api/v1/yard/picks/" + taskID: func(w http.ResponseWriter, r *http.Request) {
			r.SetPathValue("id", taskID)
			h.pickDetail(w, r, guards.StaffCaller{})
		},
	}
	for path, do := range routes {
		for _, role := range rolesOffTheList {
			body := getAs(t, role, path, do)
			if strings.Contains(body, "still_owed_cents") || strings.Contains(body, "12345") {
				t.Errorf("%s as %s must not carry the owed figure: %s", path, role, body)
			}
			if !strings.Contains(body, `"hold_words":"Balance due"`) || !strings.Contains(body, `"held_from_picking":true`) {
				t.Errorf("%s as %s keeps the hold mark and its words: %s", path, role, body)
			}
		}
		for _, role := range rolesOnTheList {
			if body := getAs(t, role, path, do); !strings.Contains(body, `"still_owed_cents":12345`) {
				t.Errorf("%s as %s is on the list and keeps the figure: %s", path, role, body)
			}
		}
	}
}

// The list is the configuration's, not a role name: a double that names the yard manager off the
// list holds the figure from the yard manager, and no rule wired holds it from everyone.
func TestYardMoneyFailsClosed(t *testing.T) {
	none := costRolesFunc(func(string) (bool, error) { return false, nil })
	broken := costRolesFunc(func(string) (bool, error) { return true, context.DeadlineExceeded })
	for name, roles := range map[string]CostRoles{"off the list": none, "unreadable": broken, "unwired": nil} {
		h, taskID := heldTicketSvc(t, roles)
		body := getAs(t, "YARD_MANAGER", "/api/v1/yard/picks/"+taskID, func(w http.ResponseWriter, r *http.Request) {
			r.SetPathValue("id", taskID)
			h.pickDetail(w, r, guards.StaffCaller{})
		})
		if strings.Contains(body, "still_owed_cents") {
			t.Errorf("%s: %s", name, body)
		}
	}
}

type costRolesFunc func(string) (bool, error)

func (f costRolesFunc) SeesCostAndMargin(role string) (bool, error) { return f(role) }
