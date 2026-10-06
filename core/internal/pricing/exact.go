package pricing

// exact.go, the resolver's exact money arms (decision log row 820, option A widened).
//
// AGENTS.md: money is integer cents, computed by the core, never floats. The arms that
// multiply a price by a quantity that is not a whole number of the price's unit (a converted
// quantity, a decimal quantity, a unit factor) now compute integer cents times an exact
// rational and round ONCE, half away from zero. That is the module's ONE rounding mode:
// roundCents applies it to a float input and mulDivRoundHalfUp to an int ratio (its negative
// branch rounds away from zero); this file applies the same decision to a math/big.Rat input,
// so a half cent that a binary float would drag below the boundary (410 / 100 x 15 landing at
// 61.499999999999993) rounds the way the decimal arithmetic a customer does in their head
// rounds. No second rounding mode is introduced anywhere.
//
// THE SOURCES OF THE RATIONALS. A unit factor arrives from the product module as the exact
// DECIMAL(19,8) text of product_uoms.qty_in_base (ConvertUOMExact); the entered quantity
// arrives as a float64 off the wire, and exactDecimal recovers the decimal the wire carried
// (the shortest string that round-trips to the float). When no exact seam is wired (DB-free
// fakes), the factor falls back through the same recovery from the float64 ConvertUOM value.

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"strconv"
)

// UOMConverterExact is the optional twin of UOMConverter: a converter that can hand over the
// factor as the exact decimal text the product row carries. *product.Service satisfies it;
// WithUOMConverter wires it automatically when the injected converter has it, so production
// (wire_money.go) needs no extra call and DB-free fakes simply leave it off.
type UOMConverterExact interface {
	ConvertUOMExact(ctx context.Context, productID, fromUOM string) (string, error)
}

// uomFactorExactFor returns the qty_in_base factor for (productID, uom) as an exact rational,
// the twin of uomFactorFor for the money arms. Semantics match: uom=="" is 1 (dimensionless),
// no converter wired is 1 (degrade to same-UOM), a missing factor is errNoConversion. A
// negative or unparsable factor is an internal error, never a price.
func (s *Service) uomFactorExactFor(ctx context.Context, productID, uom string) (*big.Rat, error) {
	if uom == "" {
		return new(big.Rat).SetInt64(1), nil
	}
	if s.uomExact != nil {
		text, err := s.uomExact.ConvertUOMExact(ctx, productID, uom)
		if err != nil {
			if isNoConversion(err) {
				return nil, errNoConversion
			}
			return nil, err
		}
		q, ok := new(big.Rat).SetString(text)
		if !ok {
			// The column is DECIMAL(19,8); anything unparsable there is data corruption, so
			// refuse loudly rather than guess (the float path's "bad factor" treatment).
			return nil, fmt.Errorf("uom factor %q for %q: bad factor %q", uom, productID, text)
		}
		return q, nil
	}
	if s.uom == nil {
		return new(big.Rat).SetInt64(1), nil
	}
	f, err := s.uom.ConvertUOM(ctx, productID, uom)
	if err != nil {
		if isNoConversion(err) {
			return nil, errNoConversion
		}
		return nil, err
	}
	return exactDecimal(f), nil
}

// exactDecimal recovers, exactly, the decimal a float64 carried: the shortest string that
// round-trips to it, parsed as a rational. float64(4.1) recovers 41/10, float64(100) recovers
// 100. Every decimal of up to about 15 significant digits round-trips through float64, which
// covers the DECIMAL(19,8) factor column at the scale factors are set and every entered
// quantity; a value that needs more digits cannot have come off the float64 wire intact in
// the first place.
func exactDecimal(f float64) *big.Rat {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		// Not a factor a line can be priced with; answer zero so the caller's zero-factor
		// guard refuses the line instead of pricing off garbage.
		return new(big.Rat)
	}
	q, ok := new(big.Rat).SetString(strconv.FormatFloat(f, 'f', -1, 64))
	if !ok {
		return new(big.Rat).SetFloat64(f)
	}
	return q
}

// roundCentsExact rounds the exact rational to integer cents ONCE, half away from zero: the
// exact twin of roundCents (float) and of mulDivRoundHalfUp's away-from-zero negative branch.
// A magnitude beyond int64 clamps, the arithmetic below can never silently wrap.
func roundCentsExact(x *big.Rat) int64 {
	n := new(big.Int).Lsh(new(big.Int).Abs(x.Num()), 1) // floor((2|num| + den) / (2den)) = floor(|x| + 1/2)
	den := x.Denom()
	n.Add(n, den)
	n.Quo(n, new(big.Int).Lsh(den, 1))
	if x.Sign() < 0 {
		n.Neg(n)
	}
	if !n.IsInt64() {
		if n.Sign() > 0 {
			return math.MaxInt64
		}
		return math.MinInt64
	}
	return n.Int64()
}

// centsTimesFactor is cost money converted across a unit factor: cents x factor, exact, with
// the rounding decided once at the line by roundCentsExact.
func centsTimesFactor(cents int64, factor *big.Rat) int64 {
	return roundCentsExact(new(big.Rat).Mul(new(big.Rat).SetInt64(cents), factor))
}
