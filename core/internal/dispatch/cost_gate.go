package dispatch

// cost_gate.go is the door money fence: the figure a payment hold carries
// (what the order still owes before its material may leave the yard) and the
// gate's own sentence, which may name that figure, ride only to a caller on
// the configuration's cost and margin list. Under Cash On Demand a driver
// never collects at the door, so a driver is told the hold in words and never
// a figure; the dispatch roles on the list keep both, to chase the payment
// before release.
//
// The rule is the one that decides cost visibility everywhere: the dealer's
// role configuration's cost and margin list, the same X3 rule the stock reads
// (inventory/cost_gate.go) and the yard payloads (tasks/cost_gate.go) answer
// through. This module reads it through the seam below, wired at the
// composition root with the same configuration reader, so no second rule can
// drift.
//
// THE SHAPE IS ABSENCE, NOT ZERO: a caller off the list receives the figure
// byte-absent, never a zero that reads as "nothing owed". The hold itself
// stays for every caller, its mark, its machine code and its words; only the
// figure and the gate's sentence go. The fence sits in the service, where
// this module keeps every authority decision: the payment hold reads and the
// dispatch refusal are the three places the gate's answer meets a caller.

import (
	"context"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// doorMoneyWords is what a caller the rule refuses is told instead of the
// gate's own sentence: the hold in words a driver can act on at a customer's
// gate, never a figure.
const doorMoneyWords = "payment needed before release, call the office"

// CostRoles is the one predicate that decides cost visibility: whether the
// role is on the configuration's cost and margin list (X3).
type CostRoles interface {
	SeesCostAndMargin(role string) (bool, error)
}

// WithCostRoles wires the door money fence's rule. Nil is legal and fail
// closed: with no rule wired no caller receives the figure or the gate's
// sentence.
func (s *Service) WithCostRoles(roles CostRoles) *Service { s.costRoles = roles; return s }

// CostRolesWired answers whether the rule is wired, the leg the composition
// root's boot fail-fast reads.
func (s *Service) CostRolesWired() bool { return s.costRoles != nil }

// moneyAllowed answers whether the caller may receive the door money figure.
// Fail closed on every leg but a resolved yes: an unwired rule, an unstamped
// caller, a role the configuration does not name, or an unreadable
// configuration receives no figure.
func (s *Service) moneyAllowed(ctx context.Context) bool {
	if s.costRoles == nil {
		return false
	}
	c, ok := s.callerFrom(ctx)
	if !ok {
		return false
	}
	role, ok := identity.CanonicalForERPCode(c.RoleCode)
	if !ok || role == "" {
		return false
	}
	allowed, err := s.costRoles.SeesCostAndMargin(role)
	return err == nil && allowed
}

// blankHoldMoney holds the figure and the gate's sentence out of one hold for
// a caller the rule refuses: the figure marshals byte-absent, never a zero
// that reads as nothing owed, and the reason becomes the words a driver can
// act on. The mark, the order and the machine code stay.
func blankHoldMoney(hold *StopPaymentHold) {
	hold.AmountDueCents = 0
	hold.Reason = doorMoneyWords
}

// holdWordsFor answers the sentence about a hold this caller may read: the
// gate's own for a caller on the list, the words for every other caller (the
// gate's sentence may name the figure, and the figure is fenced).
func (s *Service) holdWordsFor(ctx context.Context, reason string) string {
	if s.moneyAllowed(ctx) {
		return reason
	}
	return doorMoneyWords
}
