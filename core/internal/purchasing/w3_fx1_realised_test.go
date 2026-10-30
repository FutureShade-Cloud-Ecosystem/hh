package purchasing

import "testing"

func TestW3FX1_ProRataHalfToEven(t *testing.T) {
	cases := []struct {
		name               string
		total, part, whole int64
		want               int64
	}{
		// 135000 * 30 / 100000 = 40.5 -> half-to-even rounds to 40 (even).
		{"exact half rounds down to even", 135_000, 30, 100_000, 40},
		// 135000 * 50 / 100000 = 67.5 -> half-to-even rounds to 68 (even).
		{"exact half rounds up to even", 135_000, 50, 100_000, 68},
		// 135000 * 33333 / 100000 = 44999.55 -> 45000.
		{"above half rounds up", 135_000, 33_333, 100_000, 45_000},
		// 135000 * 33332 / 100000 = 44998.20 -> 44998.
		{"below half rounds down", 135_000, 33_332, 100_000, 44_998},
		// Full USD 1,000.00 at 1.38 -> 138,000 CAD cents.
		{"full payment at 1.38", 100_000, 1_380_000, 1_000_000, 138_000},
		// Full USD 1,000.00 at 1.32 -> 132,000 CAD cents.
		{"full payment at 1.32", 100_000, 1_320_000, 1_000_000, 132_000},
	}
	for _, tc := range cases {
		if got := proRataHalfToEven(tc.total, tc.part, tc.whole); got != tc.want {
			t.Errorf("%s: proRataHalfToEven(%d, %d, %d) = %d, want %d",
				tc.name, tc.total, tc.part, tc.whole, got, tc.want)
		}
	}
}
