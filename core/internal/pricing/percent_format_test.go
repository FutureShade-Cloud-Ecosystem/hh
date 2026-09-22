package pricing

import (
	"strings"
	"testing"
)

// DIB-445 sweep, breakdown DETAIL strings are prose for a human and must not carry the storage unit.
// The ticket is about the Check Quote surface; this is the same defect in the pricing package's own
// copy, which renders as the DETAIL line under a priced line in the POS breakdown.

func TestPercentFromBps_SpeaksPercentNotBasisPoints(t *testing.T) {
	cases := []struct {
		bps  int64
		want string
	}{
		{1500, "15%"},
		{1200, "12%"},
		{0, "0%"},
		{10000, "100%"},
		{2250, "22.5%"}, // the ticket's own example
		{625, "6.25%"},  // full basis-point precision survives
		{1, "0.01%"},    // one basis point is not rounded away
		{-500, "-5%"},   // a surcharge-shaped negative keeps its sign
		{-50, "-0.5%"},  // …including when the whole part is zero
		{-1, "-0.01%"},
	}
	for _, c := range cases {
		if got := PercentFromBps(c.bps); got != c.want {
			t.Errorf("PercentFromBps(%d) = %q, want %q", c.bps, got, c.want)
		}
	}
}

func TestPercentFromBps_NeverEmitsTheUnitWord(t *testing.T) {
	// The property, stated over a wide range rather than a hand-picked list.
	for bps := int64(-10000); bps <= 10000; bps += 7 {
		out := PercentFromBps(bps)
		if strings.Contains(strings.ToLower(out), "bps") {
			t.Fatalf("PercentFromBps(%d) = %q still says bps", bps, out)
		}
		if !strings.HasSuffix(out, "%") {
			t.Fatalf("PercentFromBps(%d) = %q is not a percentage", bps, out)
		}
	}
}

func TestOverrideDetail_DiscountReadsAsPercent(t *testing.T) {
	bps := int64(1500)
	got := overrideDetail(Override{DiscountBps: &bps})
	if want := "Account override: 15% off list"; got != want {
		t.Fatalf("overrideDetail = %q, want %q", got, want)
	}
	if strings.Contains(strings.ToLower(got), "bps") {
		t.Fatalf("overrideDetail still leaks basis points: %q", got)
	}
}

func TestOverrideDetail_NotesStillWinAndAreNotRewritten(t *testing.T) {
	// An operator's own note is copy they wrote; the formatter must not touch it.
	bps := int64(1500)
	note := "Contract pricing per the 2026 supply agreement, 1500 bps"
	if got := overrideDetail(Override{Notes: note, DiscountBps: &bps}); got != note {
		t.Fatalf("overrideDetail rewrote an operator note: %q", got)
	}
}

func TestOverrideDetail_NoDiscountIsUnchanged(t *testing.T) {
	if got := overrideDetail(Override{}); got != "Account override" {
		t.Fatalf("overrideDetail = %q", got)
	}
}
