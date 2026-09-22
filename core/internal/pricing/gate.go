package pricing

import identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"

// identityMarginGate adapts identity.CheckMarginAt (A-20, additive) to the pricing
// MarginGate seam. The resolver computes the effective per-category floor from
// pricing_category_policy and passes it as floorBps, so identity stays the single
// owner of the margin-block decision and the 0016 approval queue, and pricing does
// not reimplement the gate. A below-floor result routes to that existing queue.
type identityMarginGate struct{}

// NewIdentityMarginGate returns the production margin gate backed by identity (A-20).
func NewIdentityMarginGate() MarginGate { return identityMarginGate{} }

func (identityMarginGate) Check(priceCents, costCents int64, branchID string, hasApprovedOverride bool, floorBps int64) *MarginBlock {
	b := identity.CheckMarginAt(priceCents, costCents, branchID, hasApprovedOverride, floorBps)
	if b == nil {
		return nil
	}
	return &MarginBlock{
		Policy:    string(b.Policy),
		Detail:    b.Detail,
		MarginBps: marginBps(priceCents, costCents),
		FloorBps:  floorBps,
	}
}
