package houseday

import (
	"testing"
	"time"
)

func TestToday_IsTheHouseZonesDayWhateverZoneTheClockIsRead(t *testing.T) {
	base := time.Date(2026, 3, 2, 0, 30, 0, 0, time.UTC)
	for _, name := range []string{"UTC", "Pacific/Auckland", "America/Los_Angeles"} {
		loc, err := time.LoadLocation(name)
		if err != nil {
			t.Fatal(err)
		}
		at := base.In(loc)
		if got := Today(func() time.Time { return at }); got != "2026-03-01" {
			t.Errorf("clock read in %s: got %s, want the house zone's earlier day", name, got)
		}
	}
}
