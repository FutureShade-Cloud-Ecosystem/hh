package billing

// finance_charge_test.go, the charge arithmetic and the month's shape, DB-free so the suite pins
// them on every run (wave 3 FC-1).

import (
	"testing"
	"time"
)

func TestFinanceChargeCents(t *testing.T) {
	cases := []struct {
		name      string
		pastDue   int64
		wantCents int64
	}{
		// The exact one and a half percent.
		{"one and a half percent of ten thousand dollars", 1_000_000, 15_000},
		{"one and a half percent of two hundred dollars", 20_000, 300},
		// Rounded half to even, on balances above the minimum: 1999.5 cents rounds to 2000 (the
		// even neighbour up), and 2002.5 rounds DOWN to 2002, which half up would not do.
		{"a half cent rounds to the even neighbour up", 133_300, 2000},
		{"a half cent rounds to the even neighbour down", 133_500, 2002},
		// The two dollar minimum on a balance above zero.
		{"the minimum on one hundred dollars", 10_000, 200},
		{"the minimum on one cent", 1, 200},
		// Nothing above zero stays nothing.
		{"no balance no charge", 0, 0},
		{"no debt is not a credit", -500, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := financeChargeCents(tc.pastDue); got != tc.wantCents {
				t.Fatalf("financeChargeCents(%d) = %d, want %d", tc.pastDue, got, tc.wantCents)
			}
		})
	}
}

func TestParseFinanceChargeMonth(t *testing.T) {
	for _, ok := range []string{"2026-01", "2026-09", "1999-12"} {
		if _, _, err := parseFinanceChargeMonth(ok); err != nil {
			t.Fatalf("parseFinanceChargeMonth(%q) refused a house month: %v", ok, err)
		}
	}
	for _, bad := range []string{"2026-9", "2026-13", "2026-00", "26-09", "2026/09", ""} {
		if _, _, err := parseFinanceChargeMonth(bad); err == nil {
			t.Fatalf("parseFinanceChargeMonth(%q) accepted a shapeless month", bad)
		}
	}
}

func TestFinanceChargeMonthBounds(t *testing.T) {
	loc := time.FixedZone("house", 0)
	start, end, lastDay := financeChargeMonthBounds(2026, 9, loc)
	if got := start.Format("2006-01-02 15:04"); got != "2026-09-01 00:00" {
		t.Fatalf("the month starts at %s, want its first midnight", got)
	}
	if got := lastDay.Format("2006-01-02"); got != "2026-09-30" {
		t.Fatalf("the month's last day is %s, want 2026-09-30", got)
	}
	if got := end.Format("2006-01-02 15:04:05.999999999"); got != "2026-09-30 23:59:59.999999999" {
		t.Fatalf("the month's last instant is %s, want the last nanosecond of its last day", got)
	}
	// The next house day is the first instant of the month after, the day the run waits for.
	next := start.AddDate(0, 1, 0)
	if !end.Before(next) {
		t.Fatal("the month's last instant is not before the next house day")
	}
}
