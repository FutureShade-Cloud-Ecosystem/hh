package billing

// customer_ar_workspace_access.go, the two fences on the customer account workspace (UX-93).
//
// The payment list is the payment trail ruling 87 holds to money desk WORK, so a caller the trail
// reader does not admit gets the workspace without it: the list is absent (payments_withheld says
// so), not blanked, the same way the register's invoice detail leaves the payment entries out of its
// timeline. The credit notes and the aging are not part of the trail and are unchanged.
//
// The customer is held to the caller's branch reach by the customer module's own rule
// (customer.Service.GetAccount): an account homed to a branch the caller cannot reach is not found,
// never forbidden, so the route is no existence oracle for another branch's accounts.

import (
	"context"
	"errors"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
)

// WorkspaceReach says whether the caller on the context can reach one customer account.
type WorkspaceReach interface {
	CustomerReachable(ctx context.Context, customerID string) (bool, error)
}

// accountReader is the customer module's by-id read, which carries the reach rule.
type accountReader interface {
	GetAccount(ctx context.Context, customerID string) (customer.Account, error)
}

// CustomerModuleReach answers WorkspaceReach with the customer module's own by-id read, so the
// workspace and the customer read cannot disagree about which accounts a caller reaches.
type CustomerModuleReach struct{ Accounts accountReader }

// NewCustomerModuleReach binds the customer module's read.
func NewCustomerModuleReach(a accountReader) CustomerModuleReach {
	return CustomerModuleReach{Accounts: a}
}

// CustomerReachable is true when the customer read finds the account for the stamped caller.
func (c CustomerModuleReach) CustomerReachable(ctx context.Context, customerID string) (bool, error) {
	if c.Accounts == nil {
		return false, nil
	}
	_, err := c.Accounts.GetAccount(ctx, customerID)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, customer.ErrNotFound), errors.Is(err, customer.ErrUnauthenticated):
		// A caller with no linked staff row has no branch reach to hold the account to: not found,
		// never a 500 and never an unfenced read.
		return false, nil
	default:
		return false, err
	}
}

// WithPaymentTrail binds the reader that says whether a caller holds the payment trail reads.
// Unbound, the workspace withholds the payment list from every caller.
func (h *WorkspaceHandler) WithPaymentTrail(t PaymentTrailReader) *WorkspaceHandler {
	h.trail = t
	return h
}

// WithCustomerReach binds the customer reach rule. Unbound, no customer is reachable.
func (h *WorkspaceHandler) WithCustomerReach(r WorkspaceReach) *WorkspaceHandler {
	h.reach = r
	return h
}

// reachable asks the reach rule; false also when none is bound.
func (h *WorkspaceHandler) reachable(ctx context.Context, customerID string) (bool, error) {
	if h.reach == nil {
		return false, nil
	}
	return h.reach.CustomerReachable(ctx, customerID)
}

// holdsTrail asks the payment trail reader; false also when none is bound.
func (h *WorkspaceHandler) holdsTrail(ctx context.Context, platformUserID, orgID string) (bool, error) {
	if h.trail == nil {
		return false, nil
	}
	return h.trail.HoldsPaymentTrail(ctx, platformUserID, orgID)
}
