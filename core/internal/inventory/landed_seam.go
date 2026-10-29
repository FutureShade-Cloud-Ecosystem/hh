package inventory

// landed_seam.go, SCAFFOLD-owned seam inventory exposes to W6-A16 (internal/landedcost): the received PO
// lines a landed cost allocates across, and the capitalization of an allocated landed cost into the weighted-
// average cost (inside the caller's tx). FROZEN signatures; A-16 consumes these via an interface it declares.
// Bodies are errNotImplemented stubs INV-CORE + A-16 fill. All amounts CAD (the USD→CAD conversion happens in
// landedcost before it reaches here, GL/valuation stay CAD).

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// ReceivedLine is one received PO line a landed cost apportions over (the allocation basis inputs).
type ReceivedLine struct {
	POLineID     string  `json:"po_line_id"`
	ProductID    string  `json:"product_id"`
	QtyBase      float64 `json:"qty_base"`
	ValueCents   int64   `json:"value_cents"` // extended received value (VALUE basis)
	WeightKg     float64 `json:"weight_kg"`   // total weight (WEIGHT basis)
	IsSpecialBuy bool    `json:"is_special_buy"`
}

// ReceivedLinesForPO returns the received lines of a PO for landed-cost allocation. FROZEN signature; INV-CORE
// fills the body.
func (s *Service) ReceivedLinesForPO(ctx context.Context, poID string) ([]ReceivedLine, error) {
	return s.db().ReceivedLinesForPO(ctx, poID)
}

// CapitalizeLandedCostTx raises the weighted-average cost of a product at a branch by allocatedCadCents worth
// of landed cost (freight/duty/handling), inside the caller's tx, no stock qty changes, only unit cost, and
// returns the value the subledger actually absorbed. The caller (landedcost) books the GL 1200 debit from that
// return, routing the un-absorbed remainder (no on-hand, or integer-cent rounding) to COGS so the GL never
// drifts from the perpetual subledger (S3 W6 B1/B2). Signature widened from the scaffold stub during W6
// remediation, internal seam, both sides ours.
//
// `at` and `sourceRefID` name the EVENT: the instant the capitalization happened and the landed-cost document it
// belongs to. The capitalization books a VALUE ONLY move (0453, W2-5B) carrying the exact cents the caller's GL
// 1200 debit books, at `at`, so the close pre flight's stock tie and account 1200 cut the same event at the same
// instant. The caller reads its own clock ONCE per operation and passes that instant here and to its journal.
func (s *Service) CapitalizeLandedCostTx(ctx context.Context, tx pgx.Tx, productID, branchID string, allocatedCadCents int64, at time.Time, sourceRefID string) (int64, error) {
	return s.capitalizeLanded(ctx, tx, productID, branchID, allocatedCadCents, at, sourceRefID)
}

// CapitalizeLandedCostSnapTx is CapitalizeLandedCostTx plus the exact pre-Apply average it capitalized over, the
// snapshot the landedcost Void path persists (0058 landed_cost_allocations.pre_apply_avg_unit_cost) and restores via
// RestoreLandedCostTx so an Apply/Void round-trip reproduces the WAC to the cent even for sub-unit fractional on-hand
// (L8 MAJOR). Internal seam, both sides ours. Returns (absorbed, snapAvg, hadCostRow).
func (s *Service) CapitalizeLandedCostSnapTx(ctx context.Context, tx pgx.Tx, productID, branchID string, allocatedCadCents int64, at time.Time, sourceRefID string) (int64, int64, bool, error) {
	return s.capitalizeLandedSnap(ctx, tx, productID, branchID, allocatedCadCents, at, sourceRefID)
}

// RestoreLandedCostTx reverses a landed-cost capitalization on Void by removing EXACTLY `absorbed`, the value this
// allocation raised the perpetual subledger by at Apply, and returns the realized subledger value delta (negative -
// the value drained from inventory) the caller books as the CR 1200 leg. Removing the exact capitalized amount is the
// GENERAL cent-exact inverse for every intervening scenario (nothing / receipt / sale) and never emits a negative leg;
// snapAvg is used only for the drift-0 restore when on-hand is unchanged since Apply. Internal seam, both sides ours.
func (s *Service) RestoreLandedCostTx(ctx context.Context, tx pgx.Tx, productID, branchID string, snapAvg, absorbed int64, at time.Time, sourceRefID string) (int64, error) {
	return s.restoreLandedCost(ctx, tx, productID, branchID, snapAvg, absorbed, at, sourceRefID)
}
