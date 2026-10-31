package billing

// discount_approval.go, TM-2 item 2: who may approve an early payment discount OUTSIDE its window.
//
// A clerk's late discount is refused in words by the walk (payments/discount.go judgeLateDiscount);
// a manager's approval posts it with the approval recorded. The wall between a clerk and a manager
// is not re-derived here: it is the ROUTE GATE's own answer, read through the role gate the staff
// surface already runs, against the access annotation the money desk's manager exceptions carry
// (the AR write off route's: the money-desk surface at manage level, under the manager tier). The
// composition root binds the gate in through the holder below (the LatePaymentTrail's discipline:
// the register handler is built before the role gate exists, so the seam is a holder bound later).
//
// In the decided default configuration that wall admits, on the money desk's manage cell and the
// manager tier: financial_controller (admin on money-desk) and general_manager (manage), with the
// three platform roles (super_admin, dealer_admin, fb_operator) passing the gate's level check.

import (
	"context"
	"errors"
)

// LateDiscountApprovalSource answers whether one caller holds the approval wall: the gate's own
// admission of that caller to the money desk's manager access.
type LateDiscountApprovalSource interface {
	HoldsLateDiscountApproval(ctx context.Context, platformUserID, orgID string) (bool, error)
}

// LateDiscountApprovals is the bindable holder for the approval source: the register handler is
// wired before the role gate exists, so the holder starts empty and the composition root binds the
// gate's reader into it once built (LatePaymentTrail's pattern). Unbound, an ask is refused in
// words: a wall that cannot be asked does not open.
type LateDiscountApprovals struct {
	src LateDiscountApprovalSource
}

// Bind attaches the approval source. Binding after asks have been refused is fine: asks answer
// from whatever is bound at the moment they are made.
func (l *LateDiscountApprovals) Bind(src LateDiscountApprovalSource) { l.src = src }

// holds answers the approval question for one caller, or the error a deployment that never bound
// the gate deserves: fail closed, in words.
func (l *LateDiscountApprovals) holds(ctx context.Context, platformUserID, orgID string) (bool, error) {
	if l.src == nil {
		return false, errors.New("billing: the late discount approval gate is not wired on this deployment")
	}
	return l.src.HoldsLateDiscountApproval(ctx, platformUserID, orgID)
}
