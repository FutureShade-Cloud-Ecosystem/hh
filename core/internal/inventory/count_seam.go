package inventory

// count_seam.go, SCAFFOLD-owned seam inventory exposes to W6-A15 (internal/invcount): the snapshot on-hand a
// count sheet freezes against, the weighted-avg valuation of a variance, and the COUNT adjustment move a
// posted variance writes (inside the caller's tx). FROZEN signatures; A-15 consumes these via an interface it
// declares (main.go wires the adapter). Bodies are errNotImplemented stubs INV-CORE + A-15 fill.

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// CountSnapshot is a frozen on-hand + weighted-avg reading for one product/location at freeze time.
type CountSnapshot struct {
	ProductID    string  `json:"product_id"`
	LocationID   string  `json:"location_id"`
	OnHandBase   float64 `json:"on_hand_base"`
	AvgCostCents int64   `json:"avg_cost_cents"`
}

// SnapshotOnHand returns the frozen on-hand + weighted-avg for every product at a branch (the count-sheet
// freeze basis). FROZEN signature; INV-CORE fills the body.
func (s *Service) SnapshotOnHand(ctx context.Context, branchID string) ([]CountSnapshot, error) {
	return s.db().SnapshotOnHand(ctx, branchID)
}

// WeightedAvgCostCents returns the current weighted-average unit cost for a product at a branch (the variance
// valuation). FROZEN signature; INV-CORE fills the body.
func (s *Service) WeightedAvgCostCents(ctx context.Context, productID, branchID string) (int64, error) {
	avg, _, err := s.db().BranchCostCents(ctx, productID, branchID)
	return avg, err
}

// WeightedAvgCostCentsTx is WeightedAvgCostCents inside the caller's transaction: the count post holds a pool
// connection for its whole transaction, so it reads the cost through that connection rather than asking the
// shared pool for a second one (nil tx reads the pool).
func (s *Service) WeightedAvgCostCentsTx(ctx context.Context, tx pgx.Tx, productID, branchID string) (int64, error) {
	avg, _, err := s.db().GetBranchCost(ctx, tx, productID, branchID)
	return avg, err
}

// PostCountAdjustmentTx writes the COUNT adjustment stock move for a counted variance inside the caller's tx
// and returns the move id (the GL leg is posted separately via the reason-coded rule). qtyDelta is signed
// (counted − snapshot). FROZEN signature; INV-CORE fills the body.
func (s *Service) PostCountAdjustmentTx(ctx context.Context, tx pgx.Tx, productID, locationID, branchID string, qtyDelta float64, unitCostCents int64, reason string) (moveID string, err error) {
	return s.postCountAdjustment(ctx, tx, productID, locationID, branchID, qtyDelta, unitCostCents, reason)
}
