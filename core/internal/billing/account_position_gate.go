package billing

// account_position_gate.go, the ONE seam through which every staff read outside the register asks
// the dealer's account position policy (UX-97). It is accountAllowed (register_service.go) with the
// policy read in front of it, so the register, the customer reads and the customer workspace cannot
// decide differently.
//
// FAIL CLOSED: a policy that cannot be read refuses, exactly as an unreadable or unknown value does
// inside accountAllowed.

import (
	"context"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// registerPolicyReader is the one store read the gate needs. Real impl: *Repository.
type registerPolicyReader interface {
	RegisterPolicyFor(ctx context.Context, branchID string) (RegisterPolicy, error)
}

// AccountPositionGate answers whether the staff caller on the context may receive the account
// position (credit limit, open balance, available credit, overdue) for an account of a branch.
type AccountPositionGate struct {
	policy     registerPolicyReader
	callerFrom func(context.Context) (identity.StaffContext, bool)
}

// NewAccountPositionGate binds the gate over the policy store.
func NewAccountPositionGate(policy registerPolicyReader) *AccountPositionGate {
	return &AccountPositionGate{policy: policy, callerFrom: identity.StaffContextFromContext}
}

// AccountPositionAllowed is true only when the branch's policy (the dealer default when branchID is
// empty or has no row of its own) admits this caller. False on any error, never a guess.
func (g *AccountPositionGate) AccountPositionAllowed(ctx context.Context, branchID string) bool {
	if g == nil || g.policy == nil {
		return false
	}
	pol, err := g.policy.RegisterPolicyFor(ctx, branchID)
	if err != nil {
		return false
	}
	return accountAllowed(ctx, g.callerFrom, pol.AccountPositionVisibility)
}
