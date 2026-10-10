package inventory

// cost_gate.go is the X3 half of the stock reads: the cost legs a read
// carries (the branch weighted average, and the figures derived from
// it) are held out by role, through the ONE predicate that decides cost
// visibility everywhere, the dealer's role configuration's cost and
// margin list. The quote module's stock view and the till's glance
// answer through the same rule (quote/floor.go seesCostAndMargin,
// pos/rev8.go marginAllowed); this module's reads reach it through the
// narrow seam below, wired at the composition root with the same
// configuration reader, so no module reads another module's tables and
// no second rule can drift.
//
// THE SHAPE IS ABSENCE, NOT ZERO (row 343 and the house rule a confident
// wrong number is worse than an honest blank): a caller off the list
// receives the cost legs byte-absent (the json tags are omitempty and
// the gate writes the zero value), the same shape quote's
// QuoteBranchStock has always carried. A caller on the list keeps the
// figure; a cost of zero on file (no cost row) reads absent for
// everybody, which is the honest placeholder for "not known", never a
// free product.

import (
	"net/http"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// CostRoles is the one predicate that decides cost visibility: whether
// the role is on the configuration's cost and margin list (X3). The
// composition root wires the roleconfig reader behind it; tests inject
// roleconfig.Default() or a double.
type CostRoles interface {
	SeesCostAndMargin(role string) (bool, error)
}

// WithCostRoles wires the cost visibility rule the stock reads' marshal
// layer gates through. Nil is legal and fail closed: with no rule wired
// no caller receives a cost leg.
func (h *Handler) WithCostRoles(roles CostRoles) *Handler {
	h.costRoles = roles
	return h
}

// CostRolesWired answers whether the cost rule is wired, the leg the
// composition root's boot fail-fast reads.
func (h *Handler) CostRolesWired() bool { return h.costRoles != nil }

// costAllowed answers whether the request's caller may receive the cost
// legs of a stock read. Fail closed on every leg but a resolved yes: an
// unwired rule, an unstamped caller, a role the configuration does not
// name, or an unreadable configuration receives no cost.
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

// blankRowCost holds the cost legs out of one stock read for a caller
// the cost rule refuses: the legs marshal byte-absent, never a zero
// that reads as free.
func blankRowCost(rows []BrowseRow) {
	for i := range rows {
		rows[i].AvgCostCents = 0
	}
}

// blankBranchStockCost is blankRowCost over the four-bucket view.
func blankBranchStockCost(rows []BranchStock) {
	for i := range rows {
		rows[i].AvgCostCents = 0
	}
}

// blankOnHandCost is blankRowCost over the on-hand read model.
func blankOnHandCost(rows []OnHand) {
	for i := range rows {
		rows[i].AvgCostCents = 0
	}
}

// blankShortfallCost holds both cost legs out of the replenishment
// worklist: the branch average AND the suggested value derived from it
// (suggested value over suggested quantity names the average), so a
// refused caller cannot derive the figure either.
func blankShortfallCost(rows []ShortfallRow) {
	for i := range rows {
		rows[i].AvgCostCents = 0
		rows[i].SuggestedValueCents = 0
	}
}

// blankMoveCost holds the unit cost out of the stock move ledger for a
// caller the cost rule refuses.
func blankMoveCost(rows []StockMove) {
	for i := range rows {
		rows[i].UnitCostCents = 0
	}
}

// blankTransferCost holds the line unit cost (the ship move prices a
// line at the branch average) out of a transfer for a caller the cost
// rule refuses. Transfer.Lines is a slice shared with the service's
// answer, so the lines are copied before they are written.
func blankTransferCost(t *Transfer) {
	lines := make([]TransferLine, len(t.Lines))
	copy(lines, t.Lines)
	for i := range lines {
		lines[i].UnitCostCents = 0
	}
	t.Lines = lines
}
