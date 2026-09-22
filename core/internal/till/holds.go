package till

// holds.go, W5 A-05. HasActiveHold answers whether a customer currently has an ACTIVE credit hold (0035
// credit_holds, guarded by ux_credit_holds_one_active). It backs BOTH the AR console and the hold-aware
// credit gate below, which the POS Sale consults through the widened pos.CreditGate seam so an ACCOUNT
// (credit-terms) tender is denied, fail closed, while a hold is active; cash/card are always allowed.

import (
	"context"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// HasActiveHold reports whether the customer has an ACTIVE credit hold. Empty customer id → false (a
// cash walk-in with no account cannot carry a hold).
func (r *Repository) HasActiveHold(ctx context.Context, customerID string) (bool, error) {
	if customerID == "" {
		return false, nil
	}
	if r.pool == nil {
		return false, errNoDB
	}
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM credit_holds WHERE customer_id = $1 AND status = 'ACTIVE')`,
		customerID,
	).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

// HoldChecker is the narrow seam the hold-aware credit gate depends on (*till.Repository satisfies it).
type HoldChecker interface {
	HasActiveHold(ctx context.Context, customerID string) (bool, error)
}

// HoldAwareCreditGate is the W5-A05 credit gate: it wraps the identity credit decision (unchanged) and
// additionally DENIES an ACCOUNT (credit-terms) tender while the customer carries an ACTIVE credit hold.
// It structurally satisfies pos.CreditGate (same Check signature) so main.go wires it via posSvc.
// WithCreditGate WITHOUT any edit to pos/service.go, and without importing pos here (no import cycle:
// pos already imports till). Cash/card sales (termsAreCredit=false) are never blocked by a hold.
//
// FAIL CLOSED: if the hold lookup errors on an ACCOUNT tender, the sale is denied rather than let through.
type HoldAwareCreditGate struct {
	holds HoldChecker
}

// NewHoldAwareCreditGate builds the gate over a hold checker (the till Repository in production).
func NewHoldAwareCreditGate(holds HoldChecker) *HoldAwareCreditGate {
	return &HoldAwareCreditGate{holds: holds}
}

// Check denies an ACCOUNT tender under an active hold, else defers to the identity credit decision (the
// exact W4 behaviour). ctx + customerID are the widened-seam inputs the identity-only gate ignores.
func (g *HoldAwareCreditGate) Check(ctx context.Context, customerID string, orderTotalCents, openBalanceCents, creditLimitCents int64, termsAreCredit bool, branchID string, hasApprovedOverride bool) *identity.ActionBlocked {
	// Only a credit-terms (ACCOUNT) sale draws on the account, so only it can be blocked by a hold. An
	// approved override still cannot buy through an active hold, a hold is a hard stop until released.
	if termsAreCredit && customerID != "" {
		held, err := g.holds.HasActiveHold(ctx, customerID)
		if err != nil {
			// Fail closed: cannot confirm the account is clear → deny the ACCOUNT tender.
			return &identity.ActionBlocked{
				Policy:   identity.PolicyCOD,
				BranchID: branchID,
				Detail:   "credit hold status unavailable; account tender denied (cash or card only)",
			}
		}
		if held {
			return &identity.ActionBlocked{
				Policy:   identity.PolicyCOD,
				BranchID: branchID,
				Detail:   "customer is on a credit hold; account tender denied (cash or card only)",
			}
		}
	}
	return identity.CheckCredit(orderTotalCents, openBalanceCents, creditLimitCents, termsAreCredit, branchID, hasApprovedOverride)
}

// compile-time guard: the gate must expose exactly the widened credit-gate shape.
var _ interface {
	Check(ctx context.Context, customerID string, orderTotalCents, openBalanceCents, creditLimitCents int64, termsAreCredit bool, branchID string, hasApprovedOverride bool) *identity.ActionBlocked
} = (*HoldAwareCreditGate)(nil)
