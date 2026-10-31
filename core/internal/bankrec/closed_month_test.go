package bankrec

import (
	"testing"
	"time"
)

// closedMonthDay answers a day inside the month before now's month, for the legs that plant a bank line in a
// month that has reached its end. It steps back from the first of now's month, never from now's own day of month:
// on the 31st after a 30 day month that day normalizes into the current month (September 31 is October 1).
func closedMonthDay(now time.Time) time.Time {
	u := now.UTC()
	return time.Date(u.Year(), u.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 14)
}

// The month before is the month before on every day of the month, the 31st after a 30 day month and the 29th,
// 30th and 31st after February among them.
func TestClosedMonthDay_IsInTheMonthBeforeOnEveryDay(t *testing.T) {
	cases := []struct {
		now  time.Time
		want time.Month
	}{
		{time.Date(2026, time.October, 31, 12, 0, 0, 0, time.UTC), time.September},
		{time.Date(2026, time.March, 31, 0, 0, 0, 0, time.UTC), time.February},
		{time.Date(2026, time.March, 29, 0, 0, 0, 0, time.UTC), time.February},
		{time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC), time.December},
		{time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC), time.June},
	}
	for _, c := range cases {
		got := closedMonthDay(c.now)
		if got.Month() != c.want {
			t.Errorf("closedMonthDay(%s) = %s, want a day in %s", c.now.Format("2006-01-02"), got.Format("2006-01-02"), c.want)
		}
	}
}
