package inventory

// util.go, tiny package-local helpers (nullable text binding + payload marshalling) shared by the repository
// and service. Mirrors the nullStr helper the sibling modules use so an empty string binds as SQL NULL.

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
)

// nullStr binds "" as SQL NULL (so an optional FK / actor column stores NULL, not an empty string that fails a
// UUID cast).
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// marshalJSON marshals an outbox payload to bytes.
func marshalJSON(v any) ([]byte, error) { return json.Marshal(v) }

// round rounds a float to the nearest integer cent (half away from zero), matching SQL ROUND().
func round(f float64) int64 {
	if f < 0 {
		return int64(f - 0.5)
	}
	return int64(f + 0.5)
}

// - exact money arithmetic (HH-183) -
//
// The stock ledger's values are money: integer cents times a DECIMAL(19,4) quantity, never a float
// product. A float64 product of a quantity and cents can land below an exact half (0.29 x 750 is
// exactly 217.5 but evaluates 217.49999999999997), and rounding that product then stored a cent
// below the SQL ROUND(received_base * unit_cost_cents) leg of the same receipt. Everything below
// works from the column's exact scaled integer instead, the way customerquote.MultiplyCents does,
// with half-away-from-zero rounding so a negative product (a landed-cost reversal can hand
// capitalizeLandedSnap a negative numerator) stays consistent with SQL ROUND() too. The quantities
// and unit costs on the receipt, ship, restock and count paths are non-negative by the guards at
// each caller; the symmetric sign handling is for the reversal legs, not decoration.

// qtyScale is the grain of every quantity column this module reads: DECIMAL(19,4) holds four
// fractional digits, so a quantity is an integer count of 1/10000 units. fxScale is the grain of
// purchase_orders.fx_rate: NUMERIC(12,6), six fractional digits.
const (
	qtyScale = int64(10000)
	fxScale  = int64(1000000)
)

// maxScaledFloatInput is the bound at and below which a decimal column value that arrived as a
// float64 (pgx scans DECIMAL into float64) still carries every fractional digit the column holds:
// while |f x scale| stays below 2^51 the float product sits within half a unit of the true column
// integer (a float64 carries 53 significand bits, ~2^-52 relative error), so math.Round(f x scale)
// recovers that integer EXACTLY. DECIMAL(19,4) quantities up to ~225 billion units and every
// NUMERIC(12,6) rate are far inside it; a value beyond it has already lost its decimal grain to
// the float, and the arithmetic must refuse it rather than compute a confident wrong cent.
const maxScaledFloatInput = float64(int64(1) << 51)

// scaledFromFloat converts a decimal column value that arrived as a float64 into its exact integer
// representation at the column's scale (1/10000 units for a quantity, 1/1000000 for an fx rate).
// ok=false when the magnitude is beyond maxScaledFloatInput; NaN and Inf fail the same comparison.
func scaledFromFloat(f float64, scale int64) (int64, bool) {
	if !(math.Abs(f)*float64(scale) < maxScaledFloatInput) {
		return 0, false
	}
	return int64(math.Round(f * float64(scale))), true
}

// mulScaledHalfUp multiplies an exact scaled decimal (a count of 1/scale units) by integer cents
// and rounds to whole cents, half away from zero, entirely in integer arithmetic so an exact half
// never inherits the float's last bit. The product of two int64 values can overflow int64 long
// before the rounded RESULT does (the scale division brings it back down: a result of at most
// ~9.2e18 cents implies an intermediate up to 9.2e22), so the multiply and divide run in big.Int
// and ok=false only when the rounded result itself cannot be represented in int64 cents.
func mulScaledHalfUp(scaled, cents, scale int64) (int64, bool) {
	n := new(big.Int).Mul(big.NewInt(scaled), big.NewInt(cents))
	return roundBigHalfUp(n, scale)
}

// divScaledHalfUp divides integer cents by an exact scaled decimal (a count of 1/scale units,
// positive) and rounds to whole cents, half away from zero: the weighted-average re-derivation
// round(value / on-hand) without a float quotient. Same big.Int discipline as mulScaledHalfUp; the
// numerator is multiplied by the scale first, so it overflows int64 even more readily.
func divScaledHalfUp(cents, scaled, scale int64) (int64, bool) {
	if scaled <= 0 {
		return 0, false
	}
	n := new(big.Int).Mul(big.NewInt(cents), big.NewInt(scale))
	return roundBigHalfUp(n, scaled)
}

// roundBigHalfUp divides n by d, half away from zero (SQL ROUND()'s tie rule), in big.Int.
func roundBigHalfUp(n *big.Int, d int64) (int64, bool) {
	// d/2 truncates when d is odd, which is safe: an exact half cannot arise from an odd divisor
	// (2n = (2k+1) x d has an odd right side), so the missing half unit never moves a tie.
	half := new(big.Int).Quo(big.NewInt(d), big.NewInt(2))
	if n.Sign() >= 0 {
		n.Add(n, half)
	} else {
		n.Sub(n, half)
	}
	n.Quo(n, big.NewInt(d)) // big.Int Quo truncates toward zero, matching the sign-adjusted half-up
	if !n.IsInt64() {
		return 0, false
	}
	return n.Int64(), true
}

// qtyTimesCents values a DECIMAL(19,4) quantity at integer cents, half away from zero, the shape
// behind every stock-move value and on-hand valuation this module writes. It refuses a quantity
// beyond the exact grain or a result beyond int64 cents rather than store a rounded guess.
func qtyTimesCents(qty float64, cents int64) (int64, error) {
	scaled, ok := scaledFromFloat(qty, qtyScale)
	if !ok {
		return 0, fmt.Errorf("inventory: quantity %v is beyond the exact DECIMAL(19,4) grain", qty)
	}
	v, ok := mulScaledHalfUp(scaled, cents, qtyScale)
	if !ok {
		return 0, fmt.Errorf("inventory: %v at %d cents exceeds integer cents", qty, cents)
	}
	return v, nil
}

// centsTimesFxRate converts integer cents through the PO fx_rate (NUMERIC(12,6)) at the receipt
// boundary, the USD to CAD leg: exact at the rate's six fractional digits, half away from zero.
// The column CHECKs fx_rate > 0, so only the cents side can carry a sign here.
func centsTimesFxRate(cents int64, fx float64) (int64, error) {
	scaled, ok := scaledFromFloat(fx, fxScale)
	if !ok {
		return 0, fmt.Errorf("inventory: fx rate %v is beyond the exact NUMERIC(12,6) grain", fx)
	}
	v, ok := mulScaledHalfUp(scaled, cents, fxScale)
	if !ok {
		return 0, fmt.Errorf("inventory: %d cents at fx %v exceeds integer cents", cents, fx)
	}
	return v, nil
}

// centsDivQty divides integer cents by a DECIMAL(19,4) quantity, half away from zero: the weighted
// average round(value / on-hand). A quantity at or below the grain (or non-positive) cannot divide
// a value and is refused; callers already guard on-hand > 0, this keeps the arithmetic honest even
// if a future caller's guard drifts.
func centsDivQty(cents int64, qty float64) (int64, error) {
	scaled, ok := scaledFromFloat(qty, qtyScale)
	if !ok || scaled <= 0 {
		return 0, fmt.Errorf("inventory: quantity %v cannot divide an integer-cent value", qty)
	}
	v, ok := divScaledHalfUp(cents, scaled, qtyScale)
	if !ok {
		return 0, fmt.Errorf("inventory: %d cents over %v units exceeds integer cents", cents, qty)
	}
	return v, nil
}

// asClientError is errors.As for the package's clientError, given a name so the handler layer reads
// as intent ("is this a typed client error?") rather than as a generic reflection call.
func asClientError(err error, target *clientError) bool { return errors.As(err, target) }
