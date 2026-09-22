package landedcost

// alloc.go, the pure landed-cost math: USD→CAD conversion (the DX-19 scoped exception), the 3-basis
// apportionment (weight / value / quantity, largest-remainder exact), and the retro capitalize-vs-sold-through
// split. These functions are DB-free and seam-free so the ACs (allocation exactness, USD isolation,
// exclude-special-buys, retro split) unit-test directly. moneyutil.AllocateLargestRemainder is the single
// rounding authority, the per-line shares sum EXACTLY to the total with no penny lost or invented.

import (
	"math"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/moneyutil"
)

// convertToCAD converts an amount in the document currency to CAD cents via the manual fx rate
// (USD_cents × fx_rate = CAD_cents). A CAD document (fx 1.0) is a byte-identical pass-through. A non-positive
// rate is treated as 1.0 (defence in depth; the 0041 CHECK already enforces fx_rate > 0).
func convertToCAD(cents int64, fxRate float64) int64 {
	if fxRate <= 0 {
		fxRate = 1.0
	}
	return int64(math.Round(float64(cents) * fxRate))
}

// basisIntWeight is the integer largest-remainder weight for a received line under the basis. Float bases
// (weight kg, quantity) are scaled to integers preserving the DECIMAL(19,4) precision; the scale is
// immaterial to exactness (largest-remainder sums to the total for any weights).
func basisIntWeight(basis string, l ReceivedLine) int64 {
	switch basis {
	case BasisWeight:
		return int64(math.Round(l.WeightKg * 10000))
	case BasisQuantity:
		return int64(math.Round(l.QtyBase * 10000))
	default: // BasisValue
		return l.ValueCents
	}
}

// basisFloatWeight is the human-readable basis magnitude persisted on the allocation row (basis_weight):
// the kg for WEIGHT, the base qty for QUANTITY, the extended value dollars-as-cents for VALUE.
func basisFloatWeight(basis string, l ReceivedLine) float64 {
	switch basis {
	case BasisWeight:
		return l.WeightKg
	case BasisQuantity:
		return l.QtyBase
	default:
		return float64(l.ValueCents)
	}
}

// includedLines drops the special-buy lines when the doc excludes them (special buys do NOT absorb landed
// cost, the gl_policy default). The returned slice preserves order so allocation is deterministic.
func includedLines(lines []ReceivedLine, excludeSpecial bool) []ReceivedLine {
	if !excludeSpecial {
		return lines
	}
	out := make([]ReceivedLine, 0, len(lines))
	for _, l := range lines {
		if l.IsSpecialBuy {
			continue
		}
		out = append(out, l)
	}
	return out
}

// allocate apportions totalCad across the (non-excluded) received lines by basis, largest-remainder exact.
// It returns one Allocation per included line; excluded special-buy lines are omitted entirely (they receive
// no share and absorb no cost). Σ(AllocatedCadCents) == totalCad exactly whenever at least one line is
// included and carries weight.
func allocate(totalCad int64, basis string, lines []ReceivedLine, excludeSpecial bool) []Allocation {
	included := includedLines(lines, excludeSpecial)
	weights := make([]int64, len(included))
	for i, l := range included {
		weights[i] = basisIntWeight(basis, l)
	}
	parts := moneyutil.AllocateLargestRemainder(totalCad, weights)
	out := make([]Allocation, len(included))
	for i, l := range included {
		out[i] = Allocation{
			POLineID:          l.POLineID,
			ProductID:         l.ProductID,
			BasisWeight:       basisFloatWeight(basis, l),
			AllocatedCadCents: parts[i],
		}
	}
	return out
}

// splitSoldThrough splits one line's allocated landed cost into the still-on-hand portion (capitalized into
// inventory, so it flows to the NEXT COGS via the weighted-average cost) and the sold-through portion (already
// left inventory, so it expenses straight to COGS now). onHandBase is clamped to [0, receivedBase]; a zero or
// unknown receivedBase capitalizes the whole amount (nothing has demonstrably sold through). The two parts sum
// EXACTLY to allocatedCad (the sold portion absorbs the rounding remainder).
func splitSoldThrough(allocatedCad int64, onHandBase, receivedBase float64) (onHandCad, soldCad int64) {
	if receivedBase <= 0 {
		return allocatedCad, 0
	}
	if onHandBase < 0 {
		onHandBase = 0
	}
	if onHandBase > receivedBase {
		onHandBase = receivedBase
	}
	onHandCad = int64(math.Round(float64(allocatedCad) * onHandBase / receivedBase))
	soldCad = allocatedCad - onHandCad
	return onHandCad, soldCad
}

// previewLines projects an allocation preview across a basis, including the special-buy lines (flagged) so the
// allocate screen can show WHY an excluded line gets zero. Included lines carry their apportioned share.
func previewLines(totalCad int64, basis string, lines []ReceivedLine, excludeSpecial bool) []PreviewLine {
	allocs := allocate(totalCad, basis, lines, excludeSpecial)
	// index the apportioned shares by po_line_id (falling back to product_id) for the reverse map.
	share := make(map[string]int64, len(allocs))
	for _, a := range allocs {
		share[previewKey(a.POLineID, a.ProductID)] = a.AllocatedCadCents
	}
	out := make([]PreviewLine, len(lines))
	for i, l := range lines {
		out[i] = PreviewLine{
			ProductID:         l.ProductID,
			POLineID:          l.POLineID,
			BasisWeight:       basisFloatWeight(basis, l),
			AllocatedCadCents: share[previewKey(l.POLineID, l.ProductID)],
			IsSpecialBuy:      l.IsSpecialBuy,
		}
	}
	return out
}

func previewKey(poLineID, productID string) string {
	if poLineID != "" {
		return "l:" + poLineID
	}
	return "p:" + productID
}
