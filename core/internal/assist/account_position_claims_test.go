package assist

import (
	"context"
	"errors"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// accountWords answers the policy word per branch ("" is the dealer default), or an error.
type accountWords struct {
	byBranch map[string]string
	def      string
	failOn   string
}

func (a accountWords) AccountPositionVisibility(_ context.Context, branch string) (string, error) {
	if branch != "" && branch == a.failOn {
		return "", errors.New("policy unreadable")
	}
	if branch == "" {
		return a.def, nil
	}
	if w, ok := a.byBranch[branch]; ok {
		return w, nil
	}
	return a.def, nil // a branch with no row of its own follows the dealer default, as the register's read does
}

// The claim's account position branches are the register's accountAllowed, branch by branch: ALL_STAFF shows it to
// any staff caller, MANAGER_UP to tier 1 or 2, HIDDEN and anything else to no one, an unreadable policy to no one.
func TestCallerFor_AccountPositionBranchesFollowTheRegistersPolicyWordPerBranch(t *testing.T) {
	words := accountWords{def: "MANAGER_UP", byBranch: map[string]string{cbA: "ALL_STAFF", cbB: "HIDDEN"}}
	h := NewHandler(nil, nil).WithAccountPositionPolicy(words).WithCostGate(roleList{}, branchesFor{{BranchID: cbA}, {BranchID: cbB}})

	mgr := costCaller(t, h, staffCtx("u1", "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, cbA, cbB))
	if got := strings.Join(mgr.AccountPositionBranchIDs, ","); got != DealerDefaultBranch+","+cbA {
		t.Errorf("a manager at a (ALL_STAFF) and b (HIDDEN), default MANAGER_UP: %v, want the default and a", mgr.AccountPositionBranchIDs)
	}
	front := costCaller(t, h, staffCtx("u2", "YARD", identity.TierFrontline, identity.ScopeBranch, cbA, cbB))
	if got := strings.Join(front.AccountPositionBranchIDs, ","); got != cbA {
		t.Errorf("a frontline caller: %v, want only a (ALL_STAFF), not the manager only default", front.AccountPositionBranchIDs)
	}
	gm := costCaller(t, h, staffCtx("u3", "GENERAL_MANAGER", identity.TierLeadership, identity.ScopeGlobal))
	if got := strings.Join(gm.AccountPositionBranchIDs, ","); got != DealerDefaultBranch+","+cbA {
		t.Errorf("a global manager: %v, want the default and a, never b (HIDDEN)", gm.AccountPositionBranchIDs)
	}
	claims, err := mgr.ReadClaims()
	if err != nil || strings.Join(claims.AccountPositionBranchIDs, ",") != DealerDefaultBranch+","+cbA {
		t.Errorf("the signed claim carries %v (%v)", claims.AccountPositionBranchIDs, err)
	}
}

func TestCallerFor_AccountPositionFailsClosed(t *testing.T) {
	unwired := NewHandler(nil, nil)
	if c := costCaller(t, unwired, staffCtx("u1", "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, cbA)); len(c.AccountPositionBranchIDs) != 0 {
		t.Errorf("with no policy wired the caller has %v", c.AccountPositionBranchIDs)
	}
	failing := NewHandler(nil, nil).WithAccountPositionPolicy(accountWords{def: "ALL_STAFF", failOn: cbA})
	c := costCaller(t, failing, staffCtx("u1", "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, cbA, cbB))
	if got := strings.Join(c.AccountPositionBranchIDs, ","); got != DealerDefaultBranch+","+cbB {
		t.Errorf("an unreadable policy at a gave %v, want only the default and b", c.AccountPositionBranchIDs)
	}
	unknown := NewHandler(nil, nil).WithAccountPositionPolicy(accountWords{def: "SOMETHING_NEW"})
	if c := costCaller(t, unknown, staffCtx("u1", "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, cbA)); len(c.AccountPositionBranchIDs) != 0 {
		t.Errorf("an unknown policy word gave %v, want none", c.AccountPositionBranchIDs)
	}
	noRole := NewHandler(nil, nil).WithAccountPositionPolicy(accountWords{def: "ALL_STAFF"})
	if c := costCaller(t, noRole, staffCtx("u1", "", 0, identity.ScopeBranch, cbA)); len(c.AccountPositionBranchIDs) != 0 {
		t.Errorf("a caller with no role has %v", c.AccountPositionBranchIDs)
	}
	bad := Caller{UserID: "u", RoleCode: "X", Tier: 2, AccountPositionBranchIDs: []string{"not-a-uuid"}}
	if _, err := bad.ReadClaims(); err == nil {
		t.Error("an account position branch that is not a uuid was signed")
	}
}
