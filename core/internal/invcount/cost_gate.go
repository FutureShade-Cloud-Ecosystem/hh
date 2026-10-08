package invcount

// cost_gate.go is the X3 half of the counts reads: the cost leg a read
// carries (the branch weighted average on the negative-stock worklist)
// is held out by role, through the ONE predicate that decides cost
// visibility everywhere, the dealer's role configuration's cost and
// margin list. The inventory module's stock reads answer through the
// same rule (inventory/cost_gate.go); this module reaches it through
// the same narrow seam shape, wired at the composition root with the
// same configuration reader. The leg is byte-absent for a caller off
// the list (omitempty), never a zero that reads as free.

import (
	"net/http"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// CostRoles is the one predicate that decides cost visibility: whether
// the role is on the configuration's cost and margin list (X3). The
// composition root wires the roleconfig reader behind it.
type CostRoles interface {
	SeesCostAndMargin(role string) (bool, error)
}

// WithCostRoles wires the cost visibility rule the counts reads' marsh
// layer gates through. Nil is legal and fail closed.
func (h *Handler) WithCostRoles(roles CostRoles) *Handler {
	h.costRoles = roles
	return h
}

// CostRolesWired answers whether the cost rule is wired.
func (h *Handler) CostRolesWired() bool { return h.costRoles != nil }

// costAllowed answers whether the request's caller may receive the cost
// leg of a counts read, fail closed on every leg but a resolved yes.
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

// blankNegativeStockCost holds the cost leg out of the negative-stock
// worklist for a caller the cost rule refuses.
func blankNegativeStockCost(rows []NegativeStockRow) {
	for i := range rows {
		rows[i].AvgCostCents = 0
	}
}
