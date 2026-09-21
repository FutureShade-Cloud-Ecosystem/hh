// Package moneyutil holds integer-cent money helpers shared across modules (no float money, ever).
package moneyutil

import "sort"

// AllocateLargestRemainder splits totalCents across len(weights) buckets in proportion to weights, using the
// largest-remainder (Hamilton) method so the parts sum EXACTLY to totalCents with no penny lost or invented.
// It is the single rounding authority for both the M5 per-line tax split and the A-16 landed-cost allocation.
//
// Contract:
//   - The returned slice has the same length as weights (nil weights → nil).
//   - Σ(result) == totalCents exactly (including when totalCents is negative, a reversal/credit allocates the
//     same way with signs flipped).
//   - A zero total distributes zero to every bucket.
//   - When every weight is zero (no basis to apportion by) the total is spread as evenly as possible,
//     lowest-index buckets absorbing the leftover pennies, so nothing is dropped.
//   - Negative weights are treated as zero (a weight is a non-negative basis: qty, value, line total).
func AllocateLargestRemainder(totalCents int64, weights []int64) []int64 {
	n := len(weights)
	if n == 0 {
		return nil
	}
	out := make([]int64, n)
	if totalCents == 0 {
		return out
	}

	var totalWeight int64
	w := make([]int64, n)
	for i, wi := range weights {
		if wi < 0 {
			wi = 0
		}
		w[i] = wi
		totalWeight += wi
	}

	// Degenerate basis: no weight to apportion by → spread evenly, leftover to the lowest indices.
	if totalWeight == 0 {
		base := totalCents / int64(n)
		rem := totalCents - base*int64(n) // signed remainder, |rem| < n
		step := int64(1)
		if rem < 0 {
			step = -1
			rem = -rem
		}
		for i := range out {
			out[i] = base
		}
		for i := int64(0); i < rem; i++ {
			out[i] += step
		}
		return out
	}

	// Floor share + fractional remainder per bucket. The floors sum to <= totalCents (for a positive total);
	// the leftover pennies go to the buckets with the largest remainders. Works symmetrically for a negative
	// total because integer division truncates toward zero, leaving a same-signed remainder to distribute.
	type frac struct {
		idx int
		rem int64 // numerator of the fractional part: (totalCents*w[i]) mod totalWeight, sign-carrying
	}
	fracs := make([]frac, n)
	var allocated int64
	for i := range w {
		prod := totalCents * w[i]
		share := prod / totalWeight
		out[i] = share
		allocated += share
		fracs[i] = frac{idx: i, rem: prod - share*totalWeight}
	}

	leftover := totalCents - allocated // number of pennies still to hand out (sign = direction)
	step := int64(1)
	if leftover < 0 {
		step = -1
		leftover = -leftover
	}
	// Largest |remainder| first; ties broken by lowest index for determinism.
	sort.SliceStable(fracs, func(a, b int) bool {
		ra, rb := fracs[a].rem, fracs[b].rem
		if ra < 0 {
			ra = -ra
		}
		if rb < 0 {
			rb = -rb
		}
		if ra != rb {
			return ra > rb
		}
		return fracs[a].idx < fracs[b].idx
	})
	for i := int64(0); i < leftover && int(i) < n; i++ {
		out[fracs[i].idx] += step
	}
	return out
}
