package pos

// glance_config_test.go, the till's cost and margin gate reading the
// DEALER'S CONFIGURATION (X3) and the branch wall on the two counter reads
// (X2). The rev-8 tests beside this file pin the 0170 margin_visibility
// switch for a service with no configuration wired; this file pins what the
// demo composition runs: the glance answers by the configuration's cost and
// margin list, the switch does not decide, and a branch the caller's
// resolved authority does not reach is refused with the wall's 403 on both
// the glance and the standing read (row 343, row 342).

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
// BOTH demo logins see margin at the till, and the branch policy switch
// does not get a vote anymore.
func TestMarginGlance_Configured_ListDecidesNotThePolicySwitch(t *testing.T) {
	h := newHarness()
	h.svc = h.svc.WithRoleConfig(defaultConfig(t))
	h.caller = salesCaller()
	p := h.store.policies[""]
	p.MarginVisibility = MarginVisibilityHidden // the switch would hide it from everyone
	h.store.policies[""] = p
	h.store.branchCosts = map[string]int64{"prod-paver": 60}

	out, err := h.svc.MarginGlance(context.Background(), marginInputAtBranch())
	if err != nil {
		t.Fatalf("glance as Sales: %v", err)
	}
	if len(out.Lines) != 1 || out.Lines[0].MarginBps == nil {
		t.Fatalf("the configuration's list must show margin to Sales: %+v", out)
	}

	h.caller = salesManagerCaller()
	out, err = h.svc.MarginGlance(context.Background(), marginInputAtBranch())
	if err != nil {
		t.Fatalf("glance as Sales manager: %v", err)
	}
	if len(out.Lines) != 1 || out.Lines[0].MarginBps == nil {
		t.Fatalf("the configuration's list must show margin to the Sales manager: %+v", out)
	}
}

// Hiding cost from inside_sales in the configuration is the dealer's edit
// and the glance obeys it with no code change: the payload comes back the
// byte-absent zero value.
func TestMarginGlance_Configured_HidingCostFromTheRoleHidesTheColumn(t *testing.T) {
	h := newHarness()
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
