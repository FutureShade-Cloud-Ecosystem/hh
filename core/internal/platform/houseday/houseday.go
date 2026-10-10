// Package houseday answers "which day is it?" for business dates: the dealer's day, in the house
// zone, never the server's zone and never the database session's (CURRENT_DATE). Code that needs
// a business date asks here and binds the answer, so the decision is made in Go where the gates
// can read it.
package houseday

import (
	"sync"
	"time"
)

// houseZone is the dealer's own zone: Dibbits sells out of Ontario yards, and UTC rolls over in
// the evening there. TestHouseZone_EveryCopyInTheBackendNamesTheSameZone reads this constant.
const houseZone = "America/Toronto"

// zoneOnce resolves houseZone once for the process. LoadLocation re-reads and re-parses the
// tzdata on every call, and every business-date decision in the backend asks this package, so
// the parse is paid once and every caller shares one resolved zone.
var (
	zoneOnce sync.Once
	zoneLoc  *time.Location
)

// Location resolves the house zone, degrading to UTC rather than refusing work if the image ships
// no tzdata, the same trade the module copies of the zone make.
func Location() *time.Location {
	zoneOnce.Do(func() {
		loc, err := time.LoadLocation(houseZone)
		if err != nil {
			loc = time.UTC
		}
		zoneLoc = loc
	})
	return zoneLoc
}

// Today is the house zone's YYYY-MM-DD for the given clock (time.Now when nil).
func Today(now func() time.Time) string {
	if now == nil {
		now = time.Now
	}
	return dayText(now())
}

// AddDays is the house zone's day a whole calendar days from the day of the given clock (time.Now
// when nil), the shape a payment terms or a due-on-issue rule needs. The shift is applied inside
// the house zone, so it is a calendar day and not a 24 hour multiple: a term counted across a
// daylight saving change lands on the same calendar day the wall clock would name.
func AddDays(now func() time.Time, days int) string {
	if now == nil {
		now = time.Now
	}
	return now().In(Location()).AddDate(0, 0, days).Format("2006-01-02")
}

// dayText is one instant's date in the house zone.
func dayText(at time.Time) string {
	return at.In(Location()).Format("2006-01-02")
}

// JustAfterUTCDayChange is the one pinned instant every date test in the house-day work runs at:
// half past UTC midnight on the given day, the moment the UTC day has rolled over but the house
// zone (four or five hours behind) is still on the previous evening. Any date read that follows
// the UTC day is one ahead of the dealer's day at this instant, so a test pinned here is red on
// code that reads the day at Greenwich whatever the wall clock says when the suite runs. day is
// any instant on the wanted UTC day.
func JustAfterUTCDayChange(day time.Time) time.Time {
	d := day.UTC()
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 30, 0, 0, time.UTC)
}

// ParseDay reads a YYYY-MM-DD as that day in the house zone: the instant its house midnight begins.
// A bare time.Parse gives midnight at Greenwich, which every house-day reader then takes for the
// previous evening, so a date a caller names must come through here.
func ParseDay(s string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02", s, Location())
}

// EndOfDay is the last instant of the named house day (a microsecond before the next house
// midnight, the finest grain the database keeps). An as-of that names a day inclusively cuts both
// a date column and a posting instant here: the house day of the answer is the day asked for, and
// every instant of that day is at or before it.
func EndOfDay(s string) (time.Time, error) {
	d, err := ParseDay(s)
	if err != nil {
		return time.Time{}, err
	}
	return d.AddDate(0, 0, 1).Add(-time.Microsecond), nil
}
