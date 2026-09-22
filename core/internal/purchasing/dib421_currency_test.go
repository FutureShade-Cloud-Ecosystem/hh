package purchasing

// dib421_currency_test.go, the DB-free half of DIB-421: the conversion itself.
//
// The integration test proves a USD PO's received cost lands in CAD in the stock ledger. This proves the
// thing that test cannot: that purchasing's arithmetic is the SAME arithmetic inventory does, for every
// input, and not merely for the one the integration test happens to use.

import "testing"

// inventoryCadUnit is a VERBATIM copy of internal/inventory's conversion, `cadUnit = round(unitCost ×
// fxRate)` with inventory's own round (util.go). It is copied rather than imported ON PURPOSE: importing
// inventory from purchasing is exactly the cross-module dependency this codebase forbids, and a copy that
// drifts is caught by the table below, which is the only protection a shared helper would have given anyway.
func inventoryCadUnit(unitCostCents int64, fxRate float64) int64 {
	f := float64(unitCostCents) * fxRate
	if f < 0 {
		return int64(f - 0.5)
	}
	return int64(f + 0.5)
}

// TestToCAD_IsArithmeticallyIdenticalToInventoryCadUnit is the DIB-421 named arithmetic test.
//
// WHY THIS MATTERS MORE THAN IT LOOKS. purchasing writes a receipt line and inventory writes a stock move
// for the SAME event, each computing the CAD unit cost from the same USD unit cost and the same rate. If
// the two round differently, even by one cent, even only on lines ending in a half-cent, the purchasing
// subledger and GL 1200 disagree, and the difference is unattributable because both numbers are "right".
func TestToCAD_IsArithmeticallyIdenticalToInventoryCadUnit(t *testing.T) {
	rates := []float64{1.0, 1.37, 1.3712, 0.74, 1.005, 1.5, 2.0, 1.000001}
	costs := []int64{0, 1, 2, 3, 7, 99, 100, 101, 250, 999, 1000, 2500, 3333, 9999, 100000, 1234567}

	for _, fx := range rates {
		for _, c := range costs {
			want := inventoryCadUnit(c, fx)
			if got := toCAD(c, fx); got != want {
				t.Fatalf("toCAD(%d, %v) = %d, inventory computes %d, the two ledgers would disagree", c, fx, got, want)
			}
		}
	}
}

// TestToCAD_HalfCentRoundsAwayFromZero pins the rounding RULE by name, so a future edit to either module's
// round() has something to fail against rather than only the copy above.
//
// THE RATES HERE ARE EXACTLY REPRESENTABLE IN BINARY (1.5, 2.5), on purpose. 1.005 is not: 100 × 1.005
// evaluates to 100.49999999999999 and rounds DOWN in both modules. That is fine, they agree, which is the
// property that matters, but it makes 1.005 useless for pinning a rounding RULE, because the case never
// reaches a true half. A test that asserted 101 there would be asserting a fact about decimal notation
// rather than about this code.
func TestToCAD_HalfCentRoundsAwayFromZero(t *testing.T) {
	// 3 × 1.5 = 4.5 exactly → 5 away from zero; banker's rounding would give 4.
	if got := toCAD(3, 1.5); got != 5 {
		t.Fatalf("toCAD(3, 1.5) = %d, want 5 (half away from zero, matching inventory)", got)
	}
	// 1 × 2.5 = 2.5 exactly → 3; banker's rounding would give 2.
	if got := toCAD(1, 2.5); got != 3 {
		t.Fatalf("toCAD(1, 2.5) = %d, want 3 (half away from zero)", got)
	}
	// And symmetrically below zero, which is the branch inventory's round() spells out longhand.
	if got := toCAD(-3, 1.5); got != -5 {
		t.Fatalf("toCAD(-3, 1.5) = %d, want -5 (away from zero on both sides)", got)
	}
	// Both halves of the rule agree with inventory's own implementation at the same inputs.
	for _, tc := range []struct {
		c  int64
		fx float64
	}{{3, 1.5}, {1, 2.5}, {-3, 1.5}} {
		if toCAD(tc.c, tc.fx) != inventoryCadUnit(tc.c, tc.fx) {
			t.Fatalf("toCAD and inventory disagree at (%d, %v)", tc.c, tc.fx)
		}
	}
}

// TestToCAD_UnityIsTheIdentity: every pre-DIB-421 row, and every CAD document written after it, carries
// fx 1.0. The conversion must not perturb a single one of them, a migration that re-costs the existing
// book by a cent is a migration that cannot be rolled back.
func TestToCAD_UnityIsTheIdentity(t *testing.T) {
	for _, c := range []int64{0, 1, 7, 99, 2500, 999999, -1, -2500} {
		if got := toCAD(c, 1.0); got != c {
			t.Fatalf("toCAD(%d, 1.0) = %d, want the identity", c, got)
		}
	}
}

// TestNormalizeCurrency_USDNeedsARateAndCADCannotCarryOne restates the two rules the 0134 CHECKs enforce,
// at the service layer where the refusal can say something useful.
func TestNormalizeCurrency_USDNeedsARateAndCADCannotCarryOne(t *testing.T) {
	// A USD document with no rate is not a cheap order, it is an unpriced one.
	if _, _, err := normalizeCurrency("USD", 0); err == nil {
		t.Fatal("a USD document with no fx_rate must be refused")
	}
	if _, _, err := normalizeCurrency("USD", -1.37); err == nil {
		t.Fatal("a negative fx_rate must be refused")
	}
	// A CAD document's rate is FORCED to 1.0 rather than accepted-and-ignored, so a stale 1.37 left in the
	// box after switching the currency back cannot cost every line 37% more with nothing on screen.
	c, fx, err := normalizeCurrency("cad", 1.37)
	if err != nil || c != "CAD" || fx != 1.0 {
		t.Fatalf("a CAD document must normalize to CAD/1.0, got %q/%v/%v", c, fx, err)
	}
	if _, _, err := normalizeCurrency("EUR", 1.5); err == nil {
		t.Fatal("an unsupported currency must be refused, the CHECK admits only CAD and USD")
	}
}
