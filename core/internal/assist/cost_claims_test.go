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
