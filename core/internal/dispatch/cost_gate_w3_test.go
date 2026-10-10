package dispatch

// cost_gate_w3_test.go, the door money fence's rule, shape and posture: the
// payment hold's figure and the gate's own sentence ride only to a role on
// the cost and margin list, and every other caller is told the hold in words,
// never a figure. The reads themselves (PaymentHoldForStop,
// PaymentHoldsForRun) are driven here through the stopRunSource double, so
// CI's databaseless ci job carries them; the integration leg
// (cost_gate_w3_integration_test.go) proves the same per role through the
// real handlers and queries, and this file also proves the rule against the
// real configuration, the byte-absent shape, the fail-closed posture and the
// dispatch refusal's words without a database.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// doorRoles is the role mix the fence is proven for: the roles off the
// default list (a driver among them, the audience the hold's words are for)
// and the dispatch roles on it. The list is the configuration's, read from
// the real configuration, not restated here.
var (
	doorRolesOffTheList = []string{"DRIVERS", "YARD_TEAM", "HR"}
	doorRolesOnTheList  = []string{"LOGISTICS_MANAGER", "BRANCH_MANAGER", "GENERAL_MANAGER"}
)

// doorCtx stamps the caller the fence reads, one role at a time. Global scope
// on purpose: the branch fence is another rule, proven elsewhere, and mixing
// it in here would let a branch refusal masquerade as a money refusal.
func doorCtx(role string) context.Context {
	return identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: "eeeeeeee-0000-4000-8000-000000000001", RoleCode: role,
		Tier: identity.TierFrontline, Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	})
}

// The rule is the configuration's cost and margin list, not a role name
// hardcoded here: the off list roles are refused, the dispatch roles on the
// list are allowed, reading roleconfig.Default().
func TestDoorMoneyRule_ReadsTheConfigurationCostList(t *testing.T) {
	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range doorRolesOffTheList {
		s := w3Service("2026-08-06").WithCostRoles(cfg)
		if s.moneyAllowed(doorCtx(role)) {
			t.Errorf("%s is off the cost and margin list and may not receive the door money", role)
		}
	}
	for _, role := range doorRolesOnTheList {
		s := w3Service("2026-08-06").WithCostRoles(cfg)
		if !s.moneyAllowed(doorCtx(role)) {
			t.Errorf("%s is on the cost and margin list and keeps the door money", role)
		}
	}
}

// The fence fails closed on every leg but a resolved yes: a rule that names
// nobody, an unreadable rule, no rule wired, an unstamped caller and a caller
// whose role the configuration does not name all refuse.
func TestDoorMoneyFailsClosed(t *testing.T) {
	none := doorCostRoles(func(string) (bool, error) { return false, nil })
	broken := doorCostRoles(func(string) (bool, error) { return true, context.DeadlineExceeded })
	for name, roles := range map[string]CostRoles{
		"a rule that names nobody": none,
		"an unreadable rule":       broken,
		"no rule wired":            nil,
	} {
		s := w3Service("2026-08-06").WithCostRoles(roles)
		if s.moneyAllowed(doorCtx("LOGISTICS_MANAGER")) {
			t.Errorf("%s: the logistics manager received the door money", name)
		}
	}
	if w3Service("2026-08-06").WithCostRoles(none).moneyAllowed(context.Background()) {
		t.Error("an unstamped caller received the door money")
	}
	if w3Service("2026-08-06").WithCostRoles(none).moneyAllowed(doorCtx("NOT_A_ROLE")) {
		t.Error("a caller whose role the configuration does not name received the door money")
	}
}

// The shape is absence, not zero: the refused caller's hold keeps its mark,
// its code and its words, and the figure is byte-absent.
func TestDoorMoneyShape_TheFigureIsAbsentAndTheWordsStay(t *testing.T) {
	hold := StopPaymentHold{
		StopID: "s-1", OrderID: "o-1", Held: true,
		Reason: "COD balance of $1,240.00 is outstanding on this order.",
		Code:   "COD_UNPAID", AmountDueCents: 124000,
	}
	blankHoldMoney(&hold)
	body, err := json.Marshal(hold)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "amount_due_cents") {
		t.Errorf("the figure must be byte-absent, never a zero that reads as nothing owed: %s", body)
	}
	if strings.Contains(string(body), "1,240") {
		t.Errorf("the gate's sentence names the figure and must not ride either: %s", body)
	}
	for _, want := range []string{doorMoneyWords, `"held":true`, `"code":"COD_UNPAID"`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the refused caller's hold keeps %q: %s", want, body)
		}
	}
}

// An unheld stop is a paid stop: the gate words a refusal, never a release,
// so the fence must not invent the payment sentence for one. A clear stop
// that says "payment needed before release" tells a driver at a customer's
// gate that a paid order is unpaid, on every clear stop of the run.
func TestDoorMoneyShape_AnUnheldStopCarriesNoWordsForAnyone(t *testing.T) {
	hold := StopPaymentHold{StopID: "s-1", OrderID: "o-1", Held: false}
	blankHoldMoney(&hold)
	body, err := json.Marshal(hold)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"held":false`) {
		t.Errorf("the clear mark stays: %s", body)
	}
	for _, leak := range []string{"amount_due_cents", "reason", "code", doorMoneyWords} {
		if strings.Contains(string(body), leak) {
			t.Errorf("an unheld stop is a paid stop and carries no %q: %s", leak, body)
		}
	}
}

// The dispatch refusal, the third place the gate's answer meets a caller,
// carries the words to a driver and the gate's own sentence to the list.
func TestDoorMoneyRefusal_TheWordsRideTheDispatchRefusal(t *testing.T) {
	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatal(err)
	}
	gate := &fakeGate{deny: map[string]DispatchGateDecision{
		"o-1": {Allow: false, Reason: "COD balance of $1,240.00 is outstanding.", Code: "COD_UNPAID"},
	}}
	s := w3Service("2026-08-06").WithARGate(gate).WithCostRoles(cfg)
	stops := []Stop{{OrderID: "o-1", OrderNo: "#SO-TRT-1"}}

	err = s.assertGateAllowsRun(doorCtx("DRIVERS"), stops)
	if err == nil {
		t.Fatal("a run carrying a held order must be refused")
	}
	if !strings.Contains(err.Error(), doorMoneyWords) {
		t.Fatalf("a driver's refusal carries the words: %v", err)
	}
	if strings.Contains(err.Error(), "COD balance") || strings.Contains(err.Error(), "1,240") {
		t.Fatalf("a driver's refusal must not carry the gate's sentence or its figure: %v", err)
	}

	err = s.assertGateAllowsRun(doorCtx("LOGISTICS_MANAGER"), stops)
	if err == nil {
		t.Fatal("the dispatch refusal stands for the logistics manager")
	}
	if !strings.Contains(err.Error(), "COD balance") {
		t.Fatalf("the logistics manager is on the list and keeps the gate's own sentence: %v", err)
	}
}

// The seam reports its rule wired exactly when one was taken, the composition
// root's boot fail-fast's fact.
func TestCostRolesWired(t *testing.T) {
	if NewService(nil).CostRolesWired() {
		t.Fatal("a bare service reports its cost roles wired")
	}
	wired := NewService(nil).WithCostRoles(doorCostRoles(func(string) (bool, error) { return true, nil }))
	if !wired.CostRolesWired() {
		t.Fatal("a wired rule reports itself unwired")
	}
}

type doorCostRoles func(string) (bool, error)

func (f doorCostRoles) SeesCostAndMargin(role string) (bool, error) { return f(role) }

// doorStopSource is the in-memory double the untagged read legs resolve
// through: one run, its stops, and nothing else. The branch fence in
// guardStop and guardRun still runs on top of it (a global caller passes).
type doorStopSource struct {
	run   Run
	stops []Stop
}

func (d doorStopSource) GetStop(_ context.Context, id string) (Stop, error) {
	for _, st := range d.stops {
		if st.ID == id {
			return st, nil
		}
	}
	return Stop{}, ErrNotFound
}

func (d doorStopSource) GetRun(_ context.Context, id string) (Run, error) {
	if d.run.ID == id {
		return d.run, nil
	}
	return Run{}, ErrNotFound
}

func (d doorStopSource) ListStopsForRun(_ context.Context, runID string) ([]Stop, error) {
	if d.run.ID != runID {
		return nil, ErrNotFound
	}
	return d.stops, nil
}

// doorReadWorld is the reads' proof world: one run carrying a held stop
// (the gate refusing it naming the figure in cents and in words) and a
// clear stop, behind the real cost rule.
func doorReadService(roles CostRoles) *Service {
	gate := &fakeGate{deny: map[string]DispatchGateDecision{
		"o-held": {
			Allow: false, Code: "COD_UNPAID", AmountDueCents: 124000,
			Reason: "COD balance of $1,240.00 is outstanding on this order.",
		},
	}}
	src := doorStopSource{
		run: Run{ID: "r-1", BranchID: "b-1"},
		stops: []Stop{
			{ID: "s-held", OrderID: "o-held", BranchID: "b-1"},
			{ID: "s-clear", OrderID: "o-clear", BranchID: "b-1"},
		},
	}
	return w3Service("2026-08-06").withStopSource(src).WithARGate(gate).WithCostRoles(roles)
}

// marshalHold renders one hold the way the route does, so the legs assert
// the wire shape (byte absence) and not the struct.
func marshalHold(t *testing.T, hold StopPaymentHold) string {
	t.Helper()
	body, err := json.Marshal(hold)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// Both hold reads fence the door money for every off list role and keep it
// for the list, driven through the reads themselves so CI's databaseless ci
// job carries them (the dispatch package is not in CI's integration list;
// the integration legs are local evidence only).
func TestDoorMoney_BothHoldReadsFollowTheCostList(t *testing.T) {
	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range doorRolesOffTheList {
		s := doorReadService(cfg)
		hold, err := s.PaymentHoldForStop(doorCtx(role), "s-held")
		if err != nil {
			t.Fatalf("the stop's hold as %s: %v", role, err)
		}
		body := marshalHold(t, hold)
		for _, leak := range []string{"amount_due_cents", "124000", "1,240", "COD balance"} {
			if strings.Contains(body, leak) {
				t.Errorf("the stop's hold as %s must not carry the figure in any shape (%q): %s", role, leak, body)
			}
		}
		for _, want := range []string{doorMoneyWords, `"held":true`, `"code":"COD_UNPAID"`} {
			if !strings.Contains(body, want) {
				t.Errorf("the stop's hold as %s keeps the hold, its code and its words (%q): %s", role, want, body)
			}
		}
		holds, err := s.PaymentHoldsForRun(doorCtx(role), "r-1")
		if err != nil {
			t.Fatalf("the run's holds as %s: %v", role, err)
		}
		if len(holds) != 2 {
			t.Fatalf("the run's holds as %s: %d holds, want the run's two stops", role, len(holds))
		}
		for _, h := range holds {
			body := marshalHold(t, h)
			if h.StopID == "s-clear" {
				if body != `{"stop_id":"s-clear","order_id":"o-clear","held":false}` {
					t.Errorf("the run's holds as %s: a clear stop is wordless, the gate's own answer: %s", role, body)
				}
				continue
			}
			for _, leak := range []string{"amount_due_cents", "124000", "1,240", "COD balance"} {
				if strings.Contains(body, leak) {
					t.Errorf("the run's holds as %s must not carry the figure in any shape (%q): %s", role, leak, body)
				}
			}
			if !strings.Contains(body, doorMoneyWords) || !strings.Contains(body, `"held":true`) {
				t.Errorf("the run's holds as %s keep the held mark and the words: %s", role, body)
			}
		}
	}
	for _, role := range doorRolesOnTheList {
		s := doorReadService(cfg)
		hold, err := s.PaymentHoldForStop(doorCtx(role), "s-held")
		if err != nil {
			t.Fatalf("the stop's hold as %s: %v", role, err)
		}
		body := marshalHold(t, hold)
		if !strings.Contains(body, `"amount_due_cents":124000`) || !strings.Contains(body, "COD balance") {
			t.Errorf("the stop's hold as %s is on the list and keeps the figure and the gate's sentence: %s", role, body)
		}
		holds, err := s.PaymentHoldsForRun(doorCtx(role), "r-1")
		if err != nil {
			t.Fatalf("the run's holds as %s: %v", role, err)
		}
		for _, h := range holds {
			body := marshalHold(t, h)
			if h.StopID == "s-held" && !strings.Contains(body, `"amount_due_cents":124000`) {
				t.Errorf("the run's holds as %s keep the figure on the held stop: %s", role, body)
			}
		}
	}
}

// No rule wired fails closed on both reads: the dispatch role itself is told
// the words and carries no figure.
func TestDoorMoney_BothHoldReadsFailClosed(t *testing.T) {
	s := doorReadService(nil)
	hold, err := s.PaymentHoldForStop(doorCtx("LOGISTICS_MANAGER"), "s-held")
	if err != nil {
		t.Fatalf("the stop's hold: %v", err)
	}
	body := marshalHold(t, hold)
	if strings.Contains(body, "amount_due_cents") || strings.Contains(body, "COD balance") {
		t.Errorf("the stop's hold with no rule wired must hold the figure from everyone: %s", body)
	}
	if !strings.Contains(body, doorMoneyWords) {
		t.Errorf("the stop's hold with no rule wired still tells the hold in words: %s", body)
	}
	holds, err := s.PaymentHoldsForRun(doorCtx("LOGISTICS_MANAGER"), "r-1")
	if err != nil {
		t.Fatalf("the run's holds: %v", err)
	}
	for _, h := range holds {
		body := marshalHold(t, h)
		if strings.Contains(body, "amount_due_cents") || strings.Contains(body, "COD balance") {
			t.Errorf("the run's holds with no rule wired must hold the figure from everyone: %s", body)
		}
	}
}
