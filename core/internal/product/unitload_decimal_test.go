package product

// unitload_decimal_test.go pins the factor rules the unit doors run
// (HH-LOAD-1 LD-3, the plan's section 8 point 3): a factor beyond scale
// eight is refused at the door with the legacy key named, never rounded
// silently, and every conversion survives a decimal round trip, one sell
// unit to base and back, in exact decimal arithmetic. The records here
// are invented.
//
// These are pure tests: no database, no pool.

import (
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
)

// TestUnitFactorAcceptsTheColumnScale: a factor at scale eight or under,
// positive, within DECIMAL(19,8), parses clean. The column's widest
// exact value is eleven integer digits and eight decimals, nineteen in
// all.
func TestUnitFactorAcceptsTheColumnScale(t *testing.T) {
	for _, f := range []string{"1", "1.0", "60", "0.66", "0.12345678", "9.87654321", "12345678901.23456789"} {
		d, refused := parseUnitFactor(f)
		if refused != nil {
			t.Fatalf("factor %s refused: %v", f, refused)
		}
		if !d.positive() {
			t.Fatalf("factor %s parsed non positive", f)
		}
	}
}

// TestUnitFactorRefusesScaleBeyondEight: the ninth place is refused, the
// refusal naming the field, and the value is never rounded into the
// answer.
func TestUnitFactorRefusesScaleBeyondEight(t *testing.T) {
	_, refused := parseUnitFactor("0.123456789")
	if refused == nil {
		t.Fatalf("a scale nine factor parsed: the door must refuse it, never round it")
	}
	if refused.Code != "factor_scale" {
		t.Fatalf("the refusal code is %q, want factor_scale", refused.Code)
	}
	if refused.Field != "qty_in_base" {
		t.Fatalf("the refusal field is %q, want qty_in_base", refused.Field)
	}
}

// TestUnitFactorRefusesNonPositive: zero and negatives refuse.
func TestUnitFactorRefusesNonPositive(t *testing.T) {
	for _, f := range []string{"0", "0.00000000", "-1", "-0.5"} {
		if _, refused := parseUnitFactor(f); refused == nil {
			t.Fatalf("factor %s parsed: a non positive factor must refuse", f)
		}
	}
}

// TestUnitFactorRefusesJunk: text that names no decimal refuses as a
// record shape refusal.
func TestUnitFactorRefusesJunk(t *testing.T) {
	for _, f := range []string{"", "abc", "1.2.3", "1e5", "NaN", "Inf"} {
		_, refused := parseUnitFactor(f)
		if refused == nil {
			t.Fatalf("factor %q parsed: junk must refuse", f)
		}
		if refused.Code != "record_shape" {
			t.Fatalf("factor %q refused with %q, want record_shape", f, refused.Code)
		}
	}
}

// TestUnitFactorRefusesColumnOverflow: a value wider than the column's
// nineteen digits (eleven integer digits beside the eight decimals)
// refuses rather than landing a truncation.
func TestUnitFactorRefusesColumnOverflow(t *testing.T) {
	for _, f := range []string{"123456789012.1", "1234567890123.45678901"} {
		if _, refused := parseUnitFactor(f); refused == nil {
			t.Fatalf("a factor over the column's digits parsed: it must refuse")
		}
	}
}

// TestDecimalRoundTripHoldsOneSellUnitToBaseAndBack: every conversion the
// door accepts survives one sell unit to base and back, exactly, in
// decimal arithmetic. A float implementation leaves binary noise and this
// test fails it.
func TestDecimalRoundTripHoldsOneSellUnitToBaseAndBack(t *testing.T) {
	for _, f := range []string{"1", "2", "60", "0.66", "0.12345678", "9.87654321"} {
		if refused := decimalRoundTrip(f); refused != nil {
			t.Fatalf("factor %s failed the decimal round trip: %v", f, refused)
		}
	}
}

// TestDecimalRoundTripRefusalCarriesTheField: the round trip refusal is a
// load refusal naming qty_in_base, never a bare error.
func TestDecimalRoundTripRefusalCarriesTheField(t *testing.T) {
	refused := decimalRoundTrip("0")
	if refused == nil {
		t.Fatalf("the zero factor passed the round trip; the factor gate must hold it first")
	}
	var r *load.Refusal
	if !asRefusal(refused, &r) {
		t.Fatalf("the round trip refusal is not a *load.Refusal")
	}
	if r.Field != "qty_in_base" {
		t.Fatalf("the round trip refusal names field %q, want qty_in_base", r.Field)
	}
}

// TestUnitScaleOfCountsDecimalPlaces is the scale ruler the factor gate
// reads: whole numbers scale zero, fractional digits counted exactly.
func TestUnitScaleOfCountsDecimalPlaces(t *testing.T) {
	cases := map[string]int{"1": 0, "60": 0, "0.5": 1, "0.12345678": 8, "0.123456789": 9, "12.3400": 2}
	for f, want := range cases {
		got := decimalScale(f)
		if got != want {
			t.Fatalf("scale of %q answered %d, want %d", f, got, want)
		}
	}
}

// asRefusal is the errors.As walk the writer tests share.
func asRefusal(err error, target **load.Refusal) bool {
	if r, ok := err.(*load.Refusal); ok {
		*target = r
		return true
	}
	return false
}
