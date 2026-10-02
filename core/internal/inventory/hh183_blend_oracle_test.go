package inventory

// hh183_blend_oracle_test.go, the review round 2 P2 oracles. A test that predicts the ledger's
// numbers must state the formula itself, never compute it with the service helpers under test:
// an oracle that calls blendAvg or qtyTimesCents cannot catch a change to blendAvg or
// qtyTimesCents, it silently agrees with whatever the service does. The helpers below restate the
// three shapes from first principles in big.Int (the tie-out report's ROUND, the single-rounding
// WAC blend, the capitalization re-derivation), including the tie rule itself so even a change to
// roundBigHalfUp cannot re-tune a prediction.

import (
	"math"
	"math/big"
	"testing"
)

// oracleScaled states the DECIMAL(19,4) grain: a quantity is an integer count of 1/10000 units.
func oracleScaled(t *testing.T, qty float64) int64 {
	t.Helper()
	s := int64(math.Round(qty * 10000))
	if math.Abs(qty-float64(s)/10000) > 1e-9 {
		t.Fatalf("oracle: quantity %v is not on the DECIMAL(19,4) grain", qty)
	}
	return s
}

// oracleRoundHalfUp states SQL ROUND()'s tie rule: divide half away from zero.
func oracleRoundHalfUp(t *testing.T, n *big.Int, d int64) int64 {
	t.Helper()
	if d <= 0 {
		t.Fatalf("oracle: divisor %d is not positive", d)
	}
	neg := n.Sign() < 0
	if neg {
		n = new(big.Int).Neg(n)
	}
	n = new(big.Int).Add(n, new(big.Int).Quo(big.NewInt(d), big.NewInt(2)))
	q := new(big.Int).Quo(n, big.NewInt(d))
	if neg {
		q = new(big.Int).Neg(q)
	}
	if !q.IsInt64() {
		t.Fatalf("oracle: quotient beyond int64 cents")
	}
	return q.Int64()
}

// oracleValue states the W5 gl_tieout report's own number, ROUND(avg x on-hand).
func oracleValue(t *testing.T, onHand float64, avg int64) int64 {
	t.Helper()
	return oracleRoundHalfUp(t, new(big.Int).Mul(big.NewInt(oracleScaled(t, onHand)), big.NewInt(avg)), 10000)
}

// oracleBlend states the single-rounding WAC blend: (scaledOnHand x oldAvg + scaledRecv x unit) /
// (scaledOnHand + scaledRecv), rounded once, half away from zero, every term unrounded. Equal-cost
// stock blends back to exactly its cost, which is the pin TestReceive_HalfCentBlend_RoundsOnce
// holds the service to.
func oracleBlend(t *testing.T, onHand float64, oldAvg int64, recvQty float64, unit int64) int64 {
	t.Helper()
	o := oracleScaled(t, onHand)
	if o < 0 {
		o = 0 // only positive on-hand carries weight
	}
	r := oracleScaled(t, recvQty)
	n := new(big.Int).Mul(big.NewInt(o), big.NewInt(oldAvg))
	n = new(big.Int).Add(n, new(big.Int).Mul(big.NewInt(r), big.NewInt(unit)))
	return oracleRoundHalfUp(t, n, o+r)
}

// oracleDivCents states the capitalization re-derivation, ROUND(value / on-hand).
func oracleDivCents(t *testing.T, cents int64, qty float64) int64 {
	t.Helper()
	s := oracleScaled(t, qty)
	if s <= 0 {
		t.Fatalf("oracle: divisor quantity %v is not positive", qty)
	}
	return oracleRoundHalfUp(t, new(big.Int).Mul(big.NewInt(cents), big.NewInt(10000)), s)
}
