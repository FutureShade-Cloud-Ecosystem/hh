package billing

import "testing"

func TestRoundHalfEvenBps(t *testing.T) {
	for _, tc := range []struct{ base, bps, want int64 }{
		{100_000, 1000, 10_000},
		{100_005, 500, 5_000}, // 5,000.25 goes down
		{10, 2500, 2},         // 2.5 to the even 2
		{30, 2500, 8},         // 7.5 to the even 8
		{30, 1000, 3},
		{0, 1000, 0},
		{100_000, 0, 0},
		{-5, 1000, 0},
	} {
		if got := roundHalfEvenBps(tc.base, tc.bps); got != tc.want {
			t.Errorf("roundHalfEvenBps(%d, %d) = %d, want %d", tc.base, tc.bps, got, tc.want)
		}
	}
}

// The parts a credit takes back sum to the invoice's holdback to the cent, however the credits are cut.
func TestReversalHoldbackPartsSumToTheInvoicesHoldback(t *testing.T) {
	const origHold, origSub = 10_001, 100_003
	rem, remSub, taken := int64(origHold), int64(origSub), int64(0)
	for _, credit := range []int64{33_333, 33_333, 33_337} { // the last one finishes the invoice
		h := reversalHoldback(origHold, rem, origSub, remSub, credit)
		taken += h
		rem -= h
		remSub -= credit
	}
	if taken != origHold || rem != 0 {
		t.Fatalf("the credits took %d of %d (left %d)", taken, origHold, rem)
	}
	if got := reversalHoldback(0, 0, 100, 100, 50); got != 0 {
		t.Errorf("an invoice with no holdback gives none back: %d", got)
	}
}
