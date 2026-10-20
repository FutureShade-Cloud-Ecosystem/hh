package assist

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

const (
	cbA = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	cbB = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
)

// roleList is the dealer's cost and margin list: the one predicate every module's screen asks
// (inventory.CostRoles, quote seesCostAndMargin, tasks, dispatch, pricing).
type roleList map[string]bool

func (r roleList) SeesCostAndMargin(role string) (bool, error) {
	v, ok := r[role]
	if !ok {
		return false, errors.New("unknown role")
	}
	return v, nil
}

type branchesFor []NamedBranch

func (b branchesFor) Branches(context.Context, Caller) ([]NamedBranch, error) { return b, nil }

func costCaller(t *testing.T, h *Handler, sc identity.StaffContext) Caller {
	t.Helper()
	r := asStaff(httptest.NewRequest("POST", "/api/v1/assist/chat", nil), sc)
	c, err := h.callerFor(r, guards.StaffCaller{})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// The claim's cost fields are decided by the same predicate the inventory screens ask, in Go, from the
// resolved caller, never from the request.
func TestCallerFor_CostBranchesFollowTheRoleListThatTheScreensUse(t *testing.T) {
	list := roleList{"sales_manager": true, "counter_clerk": false, "general_manager": true}
	all := branchesFor{{BranchID: cbA}, {BranchID: cbB}}
	h := NewHandler(nil, nil).WithCostGate(list, all)

	mgr := costCaller(t, h, staffCtx("u1", "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, cbA))
	if !mgr.Margin || strings.Join(mgr.CostBranchIDs, ",") != cbA {
		t.Errorf("a scoped manager on the list: margin=%v cost branches=%v, want true and [a]", mgr.Margin, mgr.CostBranchIDs)
	}
	gm := costCaller(t, h, staffCtx("u2", "GENERAL_MANAGER", identity.TierLeadership, identity.ScopeGlobal))
	if !gm.Margin || strings.Join(gm.CostBranchIDs, ",") != cbA+","+cbB {
		t.Errorf("a global caller on the list: margin=%v cost branches=%v, want every branch", gm.Margin, gm.CostBranchIDs)
	}
	clerk := costCaller(t, h, staffCtx("u3", "INSIDE_SALES", identity.TierFrontline, identity.ScopeBranch, cbA))
	if clerk.Margin || len(clerk.CostBranchIDs) != 0 {
		t.Errorf("a role off the list: margin=%v cost branches=%v", clerk.Margin, clerk.CostBranchIDs)
	}
	// Fail closed on every unknown: no gate wired, an unknown role, an unreadable list, a role code outside the vocabulary.
	for name, h2 := range map[string]*Handler{
		"no gate wired":   NewHandler(nil, nil),
		"unreadable list": NewHandler(nil, nil).WithCostGate(roleList{}, all),
	} {
		c := costCaller(t, h2, staffCtx("u4", "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, cbA))
		if c.Margin || len(c.CostBranchIDs) != 0 {
			t.Errorf("%s: margin=%v cost branches=%v, want none", name, c.Margin, c.CostBranchIDs)
		}
	}
	odd := costCaller(t, h, staffCtx("u5", "NOT_A_ROLE", identity.TierManager, identity.ScopeBranch, cbA))
	if odd.Margin || len(odd.CostBranchIDs) != 0 {
		t.Errorf("a role outside the vocabulary: %+v", odd)
	}
}

func TestReadClaimsCarriesTheTierAndTheCostFieldsFromTheCaller(t *testing.T) {
	c := Caller{UserID: "u", Tier: 2, BranchIDs: []string{cbA, cbB}, CostBranchIDs: []string{cbA}, Margin: true}
	got, err := c.ReadClaims()
	if err != nil {
		t.Fatal(err)
	}
	if got.Tier != 2 || strings.Join(got.CostBranchIDs, ",") != cbA || !got.Margin {
		t.Errorf("claims = %+v", got)
	}
	got.CostBranchIDs[0] = cbB
	if c.CostBranchIDs[0] != cbA {
		t.Error("the claim aliases the caller's cost branches")
	}
	if _, err := (Caller{UserID: "u", Tier: 2, CostBranchIDs: []string{"not-a-uuid"}}).ReadClaims(); err == nil {
		t.Error("a cost branch that is not a uuid was accepted")
	}
	// What it signs is what the database reads.
	var _ readtx.Claims = got
}

const cbC = "cccccccc-cccc-cccc-cccc-cccccccccccc"

type poPolicies map[string]string

func (p poPolicies) CostVisibility(_ context.Context, branchID string) string { return p[branchID] }

// Purchase cost follows the per branch purchasing policy, the vocabulary the purchasing screens use
// (costLensFor): ALL_STAFF allows, HIDDEN refuses, anything else allows a manager or better (tier 1 or 2). It
// does not ask the role list, so a role off the cost list still has purchase cost at a MANAGER_UP branch.
func TestCallerFor_PurchaseCostBranchesFollowThePerBranchPolicyNotTheRoleList(t *testing.T) {
	pol := poPolicies{cbA: "MANAGER_UP", cbB: "ALL_STAFF", cbC: "HIDDEN"}
	all := branchesFor{{BranchID: cbA}, {BranchID: cbB}, {BranchID: cbC}}
	list := roleList{"sales_manager": true, "hr": false, "inside_sales": true, "general_manager": true}
	h := NewHandler(nil, nil).WithCostGate(list, all).WithPurchaseCostPolicy(pol)

	mgr := costCaller(t, h, staffCtx("u1", "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, cbA, cbB, cbC))
	if strings.Join(mgr.POCostBranchIDs, ",") != cbA+","+cbB {
		t.Errorf("a manager: purchase cost branches = %v, want a and b (c is HIDDEN)", mgr.POCostBranchIDs)
	}
	clerk := costCaller(t, h, staffCtx("u2", "INSIDE_SALES", identity.TierFrontline, identity.ScopeBranch, cbA, cbB, cbC))
	if strings.Join(clerk.POCostBranchIDs, ",") != cbB {
		t.Errorf("a frontline caller: purchase cost branches = %v, want only the ALL_STAFF branch b", clerk.POCostBranchIDs)
	}
	hr := costCaller(t, h, staffCtx("u3", "HR", identity.TierManager, identity.ScopeBranch, cbA))
	if hr.Margin || len(hr.CostBranchIDs) != 0 || strings.Join(hr.POCostBranchIDs, ",") != cbA {
		t.Errorf("a manager off the role list: margin=%v stock cost=%v purchase cost=%v, want purchase cost at a only", hr.Margin, hr.CostBranchIDs, hr.POCostBranchIDs)
	}
	gm := costCaller(t, h, staffCtx("u4", "GENERAL_MANAGER", identity.TierLeadership, identity.ScopeGlobal))
	if strings.Join(gm.POCostBranchIDs, ",") != cbA+","+cbB {
		t.Errorf("a global caller: purchase cost branches = %v", gm.POCostBranchIDs)
	}
	// Anything the policy cannot name resolves to MANAGER_UP, as PolicyFor does.
	odd := NewHandler(nil, nil).WithPurchaseCostPolicy(poPolicies{cbA: "SOMETHING_NEW", cbB: ""})
	if c := costCaller(t, odd, staffCtx("u5", "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, cbA, cbB)); strings.Join(c.POCostBranchIDs, ",") != cbA+","+cbB {
		t.Errorf("unknown policy words for a manager: %v", c.POCostBranchIDs)
	}
	if c := costCaller(t, odd, staffCtx("u6", "INSIDE_SALES", identity.TierFrontline, identity.ScopeBranch, cbA, cbB)); len(c.POCostBranchIDs) != 0 {
		t.Errorf("unknown policy words for a frontline caller: %v", c.POCostBranchIDs)
	}
	// No policy wired, no role: nothing.
	none := costCaller(t, NewHandler(nil, nil), staffCtx("u7", "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, cbA))
	if len(none.POCostBranchIDs) != 0 {
		t.Errorf("no policy wired: %v", none.POCostBranchIDs)
	}
}

func TestReadClaimsCarriesThePurchaseCostBranches(t *testing.T) {
	got, err := (Caller{UserID: "u", Tier: 2, POCostBranchIDs: []string{cbA}}).ReadClaims()
	if err != nil || strings.Join(got.POCostBranchIDs, ",") != cbA {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := (Caller{UserID: "u", Tier: 2, POCostBranchIDs: []string{"x"}}).ReadClaims(); err == nil {
		t.Error("a purchase cost branch that is not a uuid was accepted")
	}
}

// A purchase cost column is held through the purchase policy branches and not through the stock cost branches,
// and the other way round.
func TestCatalogueHoldsEachCostColumnThroughItsOwnVocabulary(t *testing.T) {
	stock := ColumnDef{Name: "avg_cost_cents", Class: ClassCost}
	po := ColumnDef{Name: "unit_cost_cents", Class: ClassCost, Gate: GatePurchasePolicy}
	onlyRoleList := Caller{UserID: "u", Tier: 2, CostBranchIDs: []string{cbA}}
	onlyPolicy := Caller{UserID: "u", Tier: 2, POCostBranchIDs: []string{cbA}}
	if !columnHeld(onlyRoleList, stock) || columnHeld(onlyRoleList, po) {
		t.Error("a caller with stock cost only: wrong columns held")
	}
	if columnHeld(onlyPolicy, stock) || !columnHeld(onlyPolicy, po) {
		t.Error("a caller with purchase cost only: wrong columns held")
	}
}
