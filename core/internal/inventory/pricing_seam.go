package inventory

// pricing_seam.go, SCAFFOLD-owned seam inventory exposes to W6-A08 (internal/pricing extension): the current
// cost basis retail derivation anchors off (cost×markup). FROZEN signature; A-08 consumes it via an interface
// it declares (main.go wires the adapter). Body is an errNotImplemented stub INV-CORE fills.

import "context"

// CurrentCostCents returns the current weighted-average cost of a product at a branch, for the A-08 retail
// derivation (cost×1.85 anchor). branchID "" = the engagement-wide default cost. FROZEN signature; INV-CORE
// fills the body.
func (s *Service) CurrentCostCents(ctx context.Context, productID, branchID string) (int64, bool, error) {
	if branchID == "" {
		return s.db().ProductCostCents(ctx, productID)
	}
	return s.db().BranchCostCents(ctx, productID, branchID)
}
