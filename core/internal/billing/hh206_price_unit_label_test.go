package billing

import "testing"

// TestUnitPriceLabel (HH-206): the printed unit price names its unit exactly when the line was priced
// in a different unit from the one it was sold in. Unit level on purpose: the integration test that
// also covers it lives in a package the CI integration job does not run.
func TestUnitPriceLabel(t *testing.T) {
	for _, tc := range []struct {
		name       string
		uom, price string
		cents      int64
		want       string
	}{
		{"priced per SF, sold in PC", "PC", "SF", 1476, "CA$14.76 / SF"},
		{"same unit", "SF", "SF", 1476, "CA$14.76"},
		{"units differ only in case", "pc", "PC", 1476, "CA$14.76"},
		{"empty price unit is a guard, not a label", "PC", "", 1476, "CA$14.76"},
		{"priced per M2, sold in SF", "SF", "M2", 99, "CA$0.99 / M2"},
	} {
		got := unitPriceLabel(DetailLine{UOMEntered: tc.uom, PriceUOM: tc.price, UnitPriceCents: tc.cents})
		if got != tc.want {
			t.Errorf("%s: label = %q, want %q", tc.name, got, tc.want)
		}
	}
}
