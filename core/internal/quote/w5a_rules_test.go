package quote

// w5a_rules_test.go, the at-quote availability CHIP rule, as a pure function.
//
// The chip is what a counter clerk actually acts on, so its boundaries are worth pinning precisely.
// Three states, not two, because "we have four of the twelve you want" is the answer a quote most
// needs to surface and a boolean cannot express it.

import "testing"

// TestGradeAvailability_ThreeStatesWithTheirBoundaries.
//
// The load-bearing case is the LAST one: on-hand is plentiful but it is all hard-held, so available
// is zero and the chip must read NONE. An implementation that graded against ON-HAND passes every
// other case here and fails that one, which is exactly the oversell DIB-60 exists to stop, and the
// reason this test grades against AvailableBase rather than a convenient single number.
func TestGradeAvailability_ThreeStatesWithTheirBoundaries(t *testing.T) {
	cases := []struct {
		name      string
		qty       float64
		available float64
		want      string
	}{
		{"plenty spare", 5, 100, AvailGreen},
		{"exactly enough", 12, 12, AvailGreen},
		{"one tick short", 12, 11.9999, AvailThin},
		{"some, not enough", 12, 4, AvailThin},
		{"nothing available", 12, 0, AvailNone},
		{"negative available (a hold exceeds on hand)", 12, -40, AvailNone},
		{"a zero-quantity line cannot be graded", 0, 100, AvailUnknown},
	}
	for _, c := range cases {
		if got := gradeAvailability(c.qty, c.available); got != c.want {
			t.Errorf("%s: gradeAvailability(qty=%v, available=%v) = %q, want %q",
				c.name, c.qty, c.available, got, c.want)
		}
	}
}

// TestGradeAvailability_ToleratesDecimalRoundTripNoise. Quantities are DECIMAL(19,4) in Postgres and
// float64 in Go; two values that are equal in the database must compare equal here, or a line for
// exactly the available quantity would flicker between GREEN and THIN depending on the round trip.
func TestGradeAvailability_ToleratesDecimalRoundTripNoise(t *testing.T) {
	if got := gradeAvailability(0.1+0.2, 0.3); got != AvailGreen {
		t.Errorf("gradeAvailability(0.1+0.2, 0.3) = %q, want GREEN, float noise must not flip the chip", got)
	}
}
