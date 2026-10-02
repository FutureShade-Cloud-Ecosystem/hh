package pos

// glance_config_test.go, the till's cost and margin gate reading BOTH dealer
// knobs, the configuration's cost and margin list (X3) and the 0170 policy's
// margin_visibility, and the branch wall on the two counter reads (X2). The
// margin glance policy ruling: the list decides who MAY be considered, the
// policy's margin_visibility decides whether the answer leaves the server,
// and both must allow. This file pins what the demo composition runs; a
// branch the caller's resolved authority does not reach is refused with the
// wall's 403 on both the glance and the standing read (row 342).

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// salesCaller is the counter clerk exactly as the guards resolve the demo
// Sales login: frontline, granted one branch.
func salesCaller() identity.StaffContext {
	return identity.StaffContext{UserID: "u-sales", RoleCode: "INSIDE_SALES",
		Tier: identity.TierFrontline, Scope: identity.ScopeBranch, Branches: map[string]bool{"branch-trt": true}}
}

// salesManagerCaller is the demo Sales manager login at the same branch.
func salesManagerCaller() identity.StaffContext {
	return identity.StaffContext{UserID: "u-manager", RoleCode: "SALES_MANAGER",
		Tier: identity.TierManager, Scope: identity.ScopeBranch, Branches: map[string]bool{"branch-trt": true}}
}

// defaultConfig loads the decided default the composition wires.
func defaultConfig(t *testing.T) *roleconfig.Config {
	t.Helper()
	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatalf("roleconfig.Default: %v", err)
	}
	return cfg
}

// modifiedDefault rewrites one dealer setting of the default document and
// validates it, the round the exit check names: the answer changes with no
// code change.
func modifiedDefault(t *testing.T, setting string, value any) *roleconfig.Config {
	t.Helper()
	var doc roleconfig.Document
	if err := json.Unmarshal(roleconfig.DefaultData(), &doc); err != nil {
		t.Fatalf("unmarshal default document: %v", err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal setting: %v", err)
	}
	doc.Settings[setting] = raw
	cfg, err := roleconfig.Validate(&doc)
	if err != nil {
		t.Fatalf("validate modified document: %v", err)
	}
	return cfg
}

func marginInputAtBranch() MarginGlanceInput {
	return MarginGlanceInput{BranchID: "branch-trt", CustomerType: "RETAIL",
		Lines: []LineInput{{ProductID: "prod-paver", QtyEntered: 10, UOMEntered: "SF"}}}
}

// The decided default puts the sales roles on the cost and margin list, so
// both demo logins are CONSIDERED; the 0170 policy decides whether the
// answer leaves the server. Its column default (MANAGER_UP) holds the
// glance from the frontline clerk the list considers while the manager
// carries it, and the dealer flipping the switch to ALL_STAFF moves the
// clerk's answer with no code change: both knobs must allow.
func TestMarginGlance_Configured_BothKnobsMustAllow(t *testing.T) {
	h := newHarness()
	h.svc = h.svc.WithRoleConfig(defaultConfig(t))
	h.caller = salesCaller()
	h.store.branchCosts = map[string]int64{"prod-paver": 60}

	// The core bite: the served policy is MANAGER_UP, so the Sales caller,
	// on the list but not a manager, receives the byte-absent glance. (The
	// harness policy row's own empty value fails closed to the same
	// manager-only answer; the leg pins the named default explicitly.)
	p := h.store.policies[""]
	p.MarginVisibility = MarginVisibilityManagerUp
	h.store.policies[""] = p
	out, err := h.svc.MarginGlance(context.Background(), marginInputAtBranch())
	if err != nil {
		t.Fatalf("glance as Sales under MANAGER_UP: %v", err)
	}
	raw, _ := json.Marshal(out)
	if string(raw) != "{}" {
		t.Fatalf("MANAGER_UP must hold the glance from a frontline caller the list considers: %s", raw)
	}

	// The same policy carries the answer for the manager the list considers.
	h.caller = salesManagerCaller()
	out, err = h.svc.MarginGlance(context.Background(), marginInputAtBranch())
	if err != nil {
		t.Fatalf("glance as Sales manager under MANAGER_UP: %v", err)
	}
	if len(out.Lines) != 1 || out.Lines[0].MarginBps == nil {
		t.Fatalf("MANAGER_UP must carry margin to the listed manager: %+v", out)
	}

	// The flip: the same Sales caller under ALL_STAFF receives margin, the
	// dealer's knob moving the answer with no code change.
	h.caller = salesCaller()
	p.MarginVisibility = MarginVisibilityAllStaff
	h.store.policies[""] = p
	out, err = h.svc.MarginGlance(context.Background(), marginInputAtBranch())
	if err != nil {
		t.Fatalf("glance as Sales under ALL_STAFF: %v", err)
	}
	if len(out.Lines) != 1 || out.Lines[0].MarginBps == nil {
		t.Fatalf("ALL_STAFF must carry margin to the listed frontline caller: %+v", out)
	}
}

// The policy is the EFFECTIVE row: a branch override beats the org
// default, the same resolution the policy read serves, so the same caller
// can carry margin at one branch and the byte-absent glance at another.
func TestMarginGlance_Configured_BranchOverrideBeatsTheOrgDefault(t *testing.T) {
	h := newHarness()
	h.svc = h.svc.WithRoleConfig(defaultConfig(t))
	h.store.branchCosts = map[string]int64{"prod-paver": 60}
	// A listed frontline caller whose grants reach both branches, so the
	// same caller can be judged at each.
	bothBranchClerk := identity.StaffContext{UserID: "u-sales-2", RoleCode: "INSIDE_SALES",
		Tier: identity.TierFrontline, Scope: identity.ScopeGlobal}
	bothBranchManager := identity.StaffContext{UserID: "u-mgr-2", RoleCode: "SALES_MANAGER",
		Tier: identity.TierManager, Scope: identity.ScopeGlobal}
	atKingston := MarginGlanceInput{BranchID: "branch-kgt", CustomerType: "RETAIL",
		Lines: []LineInput{{ProductID: "prod-paver", QtyEntered: 10, UOMEntered: "SF"}}}

	// Org default MANAGER_UP, a Trenton override ALL_STAFF: the clerk's
	// glance carries margin at the override branch and is byte-absent at a
	// branch the org default governs.
	h.store.policies[""] = PosPolicy{MarginVisibility: MarginVisibilityManagerUp}
	h.store.policies["branch-trt"] = PosPolicy{BranchID: "branch-trt", MarginVisibility: MarginVisibilityAllStaff}
	h.caller = bothBranchClerk
	out, err := h.svc.MarginGlance(context.Background(), marginInputAtBranch())
	if err != nil {
		t.Fatalf("glance as the clerk at the override branch: %v", err)
	}
	if len(out.Lines) != 1 || out.Lines[0].MarginBps == nil {
		t.Fatalf("the ALL_STAFF override must carry margin to the listed clerk at its branch: %+v", out)
	}
	out, err = h.svc.MarginGlance(context.Background(), atKingston)
	if err != nil {
		t.Fatalf("glance as the clerk at the org-default branch: %v", err)
	}
	raw, _ := json.Marshal(out)
	if string(raw) != "{}" {
		t.Fatalf("the org default MANAGER_UP must hold the glance from the clerk at the other branch: %s", raw)
	}

	// The mirror: org default ALL_STAFF, a Trenton override HIDDEN hides a
	// listed manager at that branch only.
	h.store.policies[""] = PosPolicy{MarginVisibility: MarginVisibilityAllStaff}
	h.store.policies["branch-trt"] = PosPolicy{BranchID: "branch-trt", MarginVisibility: MarginVisibilityHidden}
	h.caller = bothBranchManager
	out, err = h.svc.MarginGlance(context.Background(), marginInputAtBranch())
	if err != nil {
		t.Fatalf("glance as the manager at the override branch: %v", err)
	}
	raw, _ = json.Marshal(out)
	if string(raw) != "{}" {
		t.Fatalf("the HIDDEN override must hide the glance from a listed manager at its branch: %s", raw)
	}
	out, err = h.svc.MarginGlance(context.Background(), atKingston)
	if err != nil {
		t.Fatalf("glance as the manager at the org-default branch: %v", err)
	}
	if len(out.Lines) != 1 || out.Lines[0].MarginBps == nil {
		t.Fatalf("the org default ALL_STAFF must carry margin to the listed manager at the other branch: %+v", out)
	}
}

// HIDDEN is the dealer switching the surface off: nobody receives margin, a
// listed manager included.
func TestMarginGlance_Configured_HiddenHidesEvenAListedManager(t *testing.T) {
	h := newHarness()
	h.svc = h.svc.WithRoleConfig(defaultConfig(t))
	h.caller = salesManagerCaller()
	p := h.store.policies[""]
	p.MarginVisibility = MarginVisibilityHidden
	h.store.policies[""] = p
	h.store.branchCosts = map[string]int64{"prod-paver": 60}

	out, err := h.svc.MarginGlance(context.Background(), marginInputAtBranch())
	if err != nil {
		t.Fatalf("glance as Sales manager under HIDDEN: %v", err)
	}
	raw, _ := json.Marshal(out)
	if string(raw) != "{}" {
		t.Fatalf("HIDDEN must hide the glance from a listed manager too: %s", raw)
	}
}

// An empty or unknown switch value fails closed to the manager-only answer
// (the purchasing module's cost gate resolves unknowns the same way): a
// listed frontline caller receives nothing, a listed manager margin.
func TestMarginGlance_Configured_EmptyOrUnknownSwitchFailsClosed(t *testing.T) {
	h := newHarness()
	h.svc = h.svc.WithRoleConfig(defaultConfig(t))
	h.store.branchCosts = map[string]int64{"prod-paver": 60}

	for _, tc := range []struct{ name, visibility string }{
		{"empty", ""},
		{"unknown", "MANAGERS_ONLY_PLEASE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := h.store.policies[""]
			p.MarginVisibility = tc.visibility
			h.store.policies[""] = p

			h.caller = salesCaller()
			out, err := h.svc.MarginGlance(context.Background(), marginInputAtBranch())
			if err != nil {
				t.Fatalf("glance as Sales: %v", err)
			}
			raw, _ := json.Marshal(out)
			if string(raw) != "{}" {
				t.Fatalf("%s switch value must fail closed for a listed frontline caller: %s", tc.name, raw)
			}

			h.caller = salesManagerCaller()
			out, err = h.svc.MarginGlance(context.Background(), marginInputAtBranch())
			if err != nil {
				t.Fatalf("glance as Sales manager: %v", err)
			}
			if len(out.Lines) != 1 || out.Lines[0].MarginBps == nil {
				t.Fatalf("a %s switch value fails closed to the manager-only answer: %+v", tc.name, out)
			}
		})
	}
}

// unreadablePolicyStore is the shipped fake with every policy read broken,
// the database hiccup shape (the preImageFailsStore embed idiom).
type unreadablePolicyStore struct {
	*fakeStore
}

func (f *unreadablePolicyStore) ReadPosPolicy(context.Context, string) (PosPolicy, error) {
	return PosPolicy{}, errors.New("pos: policy row unreadable")
}

// An unreadable policy row fails CLOSED and SILENTLY: the glance answers the
// byte-absent zero value with no error, never a 500, for a listed frontline
// caller and a listed manager alike. The wall keeps its own refusal beside
// this: a branch the caller does not reach is the 403 error, never this blank.
func TestMarginGlance_UnreadablePolicy_FailsClosedSilently(t *testing.T) {
	h := newHarness()
	h.svc = h.svc.WithRoleConfig(defaultConfig(t))
	h.store.branchCosts = map[string]int64{"prod-paver": 60}
	h.svc.store = &unreadablePolicyStore{fakeStore: h.store}

	h.caller = salesCaller()
	out, err := h.svc.MarginGlance(context.Background(), marginInputAtBranch())
	if err != nil {
		t.Fatalf("an unreadable policy must fail closed silently, got %v", err)
	}
	raw, _ := json.Marshal(out)
	if string(raw) != "{}" {
		t.Fatalf("an unreadable policy must answer the byte-absent glance: %s", raw)
	}

	h.caller = salesManagerCaller()
	out, err = h.svc.MarginGlance(context.Background(), marginInputAtBranch())
	if err != nil {
		t.Fatalf("an unreadable policy must fail closed silently for a manager too, got %v", err)
	}
	raw, _ = json.Marshal(out)
	if string(raw) != "{}" {
		t.Fatalf("an unreadable policy must answer the byte-absent glance for a manager too: %s", raw)
	}
}

// Hiding cost from inside_sales in the configuration is the dealer's edit
// and the glance obeys it with no code change: the payload comes back the
// byte-absent zero value. The switch admits the staff (ALL_STAFF), so the
// edited list is the only gate that can blank the clerk and the leg bites
// on the list alone.
func TestMarginGlance_Configured_HidingCostFromTheRoleHidesTheColumn(t *testing.T) {
	h := newHarness()
	h.store.policies[""] = PosPolicy{MarginVisibility: MarginVisibilityAllStaff}
	h.svc = h.svc.WithRoleConfig(modifiedDefault(t, roleconfig.SettingCostVisibility,
		[]string{"sales_manager", "branch_manager", "general_manager", "financial_controller", "outside_sales"}))
	h.caller = salesCaller()
	h.store.branchCosts = map[string]int64{"prod-paver": 60}

	out, err := h.svc.MarginGlance(context.Background(), marginInputAtBranch())
	if err != nil {
		t.Fatalf("glance: %v", err)
	}
	if len(out.Lines) != 0 || out.MarginBps != nil || out.EstProfitCents != nil {
		t.Fatalf("a role off the configured list must receive the byte-absent glance: %+v", out)
	}
}

// An unknown role is on no list: the glance answers nothing rather than
// guessing (fail closed).
func TestMarginGlance_Configured_UnknownRoleReceivesNothing(t *testing.T) {
	h := newHarness()
	h.svc = h.svc.WithRoleConfig(defaultConfig(t))
	h.caller = identity.StaffContext{UserID: "u-x", RoleCode: "SUPER_ADMIN",
		Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}
	h.store.branchCosts = map[string]int64{"prod-paver": 60}

	out, err := h.svc.MarginGlance(context.Background(), marginInputAtBranch())
	if err != nil {
		t.Fatalf("glance: %v", err)
	}
	if len(out.Lines) != 0 || out.MarginBps != nil {
		t.Fatalf("a role outside the vocabulary must receive the byte-absent glance: %+v", out)
	}
}

// The nil gate fails closed (the lead's P1-1 ruling): a service built with
// no configuration refuses the gated reads instead of answering them
// permissively, the glance among them.
func TestMarginGlance_NilConfig_Refuses(t *testing.T) {
	h := newHarness()
	h.svc = h.svc.WithRoleConfig(nil)
	_, err := h.svc.MarginGlance(context.Background(), marginInputAtBranch())
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 503 {
		t.Fatalf("the unwired glance must refuse operationally, got %v", err)
	}
}

// The same nil gate on the return path: even an under-amount return
// refuses on an unwired service, the refund rule's question is
// unanswerable without the configuration.
func TestReturn_NilConfig_Refuses(t *testing.T) {
	h := newHarness()
	seedSaleWorth(h, 20000, 0)
	h.svc = h.svc.WithRoleConfig(nil)
	h.caller = salesCaller()
	_, err := h.svc.Return(context.Background(), wholeReturnInput())
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 503 {
		t.Fatalf("the unwired return path must refuse operationally, got %v", err)
	}
}

// The branch wall: a branch the caller's grants do not reach is refused on
// the glance, not blanked.
func TestMarginGlance_UnreachableBranch_RefusedNotBlanked(t *testing.T) {
	h := newHarness()
	h.svc = h.svc.WithRoleConfig(defaultConfig(t))
	h.caller = salesCaller() // granted Trenton only

	_, err := h.svc.MarginGlance(context.Background(), MarginGlanceInput{BranchID: "branch-kgt", CustomerType: "RETAIL",
		Lines: []LineInput{{ProductID: "prod-paver", QtyEntered: 10, UOMEntered: "SF"}}})
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 403 {
		t.Fatalf("want the wall's 403 on the glance, got %v", err)
	}
}

// The same wall on the account standing read: another branch's customer
// positions are refused, never answered with a card that looks current.
func TestStanding_UnreachableBranch_RefusedNotBlanked(t *testing.T) {
	h := newHarness()
	h.caller = salesCaller()

	_, err := h.svc.Standing(context.Background(), AccountStandingInput{BranchID: "branch-kgt", CustomerID: "cust-abc"})
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 403 {
		t.Fatalf("want the wall's 403 on the standing read, got %v", err)
	}
}

// The standing read keeps its policy gate for a reachable branch: the wall
// adds a refusal, it does not remove the dealer's balance switch.
func TestStanding_ReachableBranch_PolicyStillDecidesTheBytes(t *testing.T) {
	h := newHarness()
	h.caller = salesCaller()
	h.store.policies["branch-trt"] = PosPolicy{BranchID: "branch-trt", BalanceVisibility: BalanceVisibilityHidden}

	out, err := h.svc.Standing(context.Background(), AccountStandingInput{BranchID: "branch-trt", CustomerID: "cust-abc"})
	if err != nil {
		t.Fatalf("standing: %v", err)
	}
	if out.PastDueCents != 0 || out.OldestBucket != "" {
		t.Fatalf("a hidden balance must stay byte-absent, got %+v", out)
	}
}

// emptyCostVisibilityConfig is a dealer's configuration whose cost and
// margin list names nobody: the column is hidden from everyone.
func emptyCostVisibilityConfig(t *testing.T) *roleconfig.Config {
	t.Helper()
	return modifiedDefault(t, roleconfig.SettingCostVisibility, []string{})
}
