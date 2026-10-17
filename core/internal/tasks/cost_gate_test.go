package tasks

// cost_gate_test.go, the yard fence's money half through the real handlers: the held ticket's
// still owed figure and the skid answer's deposit pair ride only for a role on the cost and margin
// list, byte-absent for every other role, with the ticket's words and the skid count kept.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// iso8601Z matches the RFC 3339 / ISO 8601 timestamp form the standard
// library emits for time.Time (RFC 3339 with a fractional second and the Z
// designator, or a numeric UTC offset). It exists so a substring scan for a
// money figure in a JSON payload does not collide with a timestamp whose
// fractional second happens to spell that figure (PRR-BACKLOG row 2226 / PR
// 302: production first hit ".131234579Z" when time.Now() carried "12345" in
// its nanoseconds). The matched text is replaced with a sentinel shorter than
// any real timestamp, so the rest of the scan proceeds over content that did
// NOT arrive from the clock.
var iso8601Z = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(?:Z|[+-]\d{2}:\d{2})`)

func stripTimestamps(body string) string {
	return iso8601Z.ReplaceAllString(body, "<ts>")
}

// moneyRoles is the role mix the fence is proven for: the roles off the default list and the roles
// on it. The default list is read from the real configuration, not restated here.
var (
	rolesOffTheList = []string{"YARD_TEAM", "DRIVERS", "HR"}
	rolesOnTheList  = []string{"YARD_MANAGER", "BRANCH_MANAGER", "GENERAL_MANAGER"}
)

// heldTicketSvc seeds a pick task whose ticket is held for payment with a figure owed, behind a
// stub pick source, and returns the handler wired with the real cost rule.
//
// The seeded task's CreatedAt and UpdatedAt are stamped with a fractional
// second that carries "12345" in its decimal form (nanoseconds 131234579,
// serialised as ".131234579Z" under RFC 3339 Nano). The yard payload encodes
// both fields verbatim, and a real time.Now() produced the same decimal form
// once on production (PRR-BACKLOG row 2226 / PR 302). The stamp turns that
// intermittent production flake into a deterministic test cover: every
// TestHeldTicketStillOwedFollowsTheCostList run now exercises a timestamp
// substring that the old "12345" body scan would have false-positive on, so
// the strip helpers below stay honest.
func heldTicketSvc(t *testing.T, roles CostRoles) (*Handler, string) {
	t.Helper()
	svc, store, _ := newSvc(t)
	task, err := store.CreateTask(context.Background(), NewTaskInput{
		TaskType: TypePick, OrderID: "aaaaaaaa-0000-4000-8000-000000000001",
		BranchID: branchA, Source: "MANUAL",
	})
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}
	stamped := store.tasks[task.ID]
	stamped.CreatedAt = time.Unix(0, 131234579).UTC()
	stamped.UpdatedAt = stamped.CreatedAt
	store.tasks[task.ID] = stamped
	taskID := task.ID
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
			// The gate's promise is ABSENCE of the owed figure. The original byte scan for the bare
			// digits "12345" flaked on run 38846044358 when a `created_at`/`updated_at` timestamp's
			// fractional seconds collapsed to `.131234579Z`, which contains the digit substring by
			// accident. The still_owed_cents key stays a byte scan (the gate's promise is the key-
			// value pair's absence); the digit scan moves to a body whose RFC 3339 / ISO 8601
			// timestamps have been replaced by a sentinel, so a coincidental digit match in a
			// timestamp can never false-positive again while the leak of `12345` or `123.45` to
			// `rolesOffTheList` (still_owed_cents, hold_words, any other field) still fails the
			// check. Seeded task timestamps now stamp .131234579Z (see heldTicketSvc), so the strip
			// has a permanent cover to defend.
			stripped := stripTimestamps(body)
			if strings.Contains(body, "still_owed_cents") ||
				strings.Contains(stripped, "12345") ||
				strings.Contains(stripped, "123.45") {
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
