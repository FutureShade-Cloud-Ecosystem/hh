package pricing

import (
	"net/http"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// cost_gate.go is the X3 half of the floor preview: the figures that name a cost (the margin each
// product would carry, and the margin it carries today) are held out by role, through the ONE
// predicate that decides cost visibility everywhere, the dealer's role configuration's cost and
// margin list. The pricing editors list decides who may SAVE a price; it does not decide who may see
// cost, and a dealer who narrows the cost list without narrowing the editors meets this gate.
// Skeleton: the gate itself lands with the fix.

// CostRoles is the one predicate that decides cost visibility: whether the role is on the
// configuration's cost and margin list (X3). The composition root wires the roleconfig reader behind
// it; tests inject a double.
type CostRoles interface {
	SeesCostAndMargin(role string) (bool, error)
}

// WithCostRoles wires the cost visibility rule the floor preview's marshal layer gates through. Nil is
// legal and fail closed: with no rule wired no caller receives a margin.
func (h *Handler) WithCostRoles(roles CostRoles) *Handler {
	h.costRoles = roles
	return h
}

// CostRolesWired answers whether the cost rule is wired, the leg the composition root's boot
// fail-fast reads.
func (h *Handler) CostRolesWired() bool { return h.costRoles != nil }

// costAllowed answers whether the request's caller may receive the margin legs of a floor preview.
// Fail closed on every leg but a resolved yes: an unwired rule, an unstamped caller, a role the bridge
// cannot name or an unreadable configuration receives no margin.
func (h *Handler) costAllowed(r *http.Request) bool {
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
