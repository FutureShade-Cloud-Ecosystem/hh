package tasks

// cost_gate.go is the yard fence's money half: the figures a yard payload
// carries about what a customer owes (a held pick ticket's still owed
// amount, the skid answer's deposit before and after) are held out by
// role at the core, in the marshal layer of the one handler that builds
// each payload. An app's projection is never the fence; the payload is.
//
// The rule is the one that decides cost visibility everywhere: the
// dealer's role configuration's cost and margin list. This module reads
// it through the narrow seam below, wired at the composition root with the
// same configuration reader the stock reads answer through
// (inventory/cost_gate.go), so no second rule can drift.
//
// THE SHAPE IS ABSENCE, NOT ZERO: a caller off the list receives the
// figure byte-absent, never a zero that reads as "nothing owed". A held
// ticket keeps its words and its mark; the skid answer keeps its count.
// A caller on the list keeps every figure.

import (
	"net/http"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// CostRoles is the one predicate that decides cost visibility: whether the
// role is on the configuration's cost and margin list.
type CostRoles interface {
	SeesCostAndMargin(role string) (bool, error)
}

// WithCostRoles wires the cost visibility rule the yard payloads' marshal
// layer gates through. Nil is legal and fail closed: with no rule wired no
// caller receives a money figure.
func (h *Handler) WithCostRoles(roles CostRoles) *Handler {
	h.costRoles = roles
	return h
}

// CostRolesWired answers whether the rule is wired, the leg the composition
// root's boot fail-fast reads.
func (h *Handler) CostRolesWired() bool { return h.costRoles != nil }

// moneyAllowed answers whether the request's caller may receive the money
// figures of a yard payload. Fail closed on every leg but a resolved yes: an
// unwired rule, an unstamped caller, a role the configuration does not name,
// or an unreadable configuration receives no figure.
func (h *Handler) moneyAllowed(r *http.Request) bool {
	if h.costRoles == nil {
		return false
	}
	c, ok := identity.StaffContextFromContext(r.Context())
	if !ok {
		return false
	}
	role, ok := identity.CanonicalForERPCode(c.RoleCode)
	if !ok || role == "" {
		return false
	}
	allowed, err := h.costRoles.SeesCostAndMargin(role)
	return err == nil && allowed
}

// blankTicketMoney holds the still owed figure out of one ticket for a
// caller the rule refuses. The ticket is copied before it is written: the
// pointer may be shared with the service's answer. The hold mark and its
// words stay.
func blankTicketMoney(t *PickTicket) *PickTicket {
	if t == nil {
		return nil
	}
	c := *t
	c.StillOwedCents = 0
	return &c
}

// blankEntriesMoney is blankTicketMoney over a page of board entries.
func blankEntriesMoney(items []PickQueueEntry) {
	for i := range items {
		items[i].Ticket = blankTicketMoney(items[i].Ticket)
	}
}

// blankTicketsMoney is blankTicketMoney over the pending feed.
func blankTicketsMoney(items []PickTicket) {
	for i := range items {
		items[i].StillOwedCents = 0
	}
}
