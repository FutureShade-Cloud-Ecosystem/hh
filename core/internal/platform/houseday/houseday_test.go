package houseday

import (
	"testing"
	"time"
)

// The zone is resolved once for the process: LoadLocation re-reads and re-parses the
// tzdata on every call, and every business-date decision in the backend asks this package,
// so an uncached load pays the parse on each ask and hands out a different pointer for
// one and the same zone.
func TestLocation_ResolvesTheHouseZoneOnce(t *testing.T) {
	if Location() != Location() {
		t.Fatal("Location resolved the house zone twice: the zone is loaded once and cached")
	}
}

// The pinned instant the house-day tests run at: half past UTC midnight, when the UTC
// day has changed and the house zone's has not, so the two spellings of "today" disagree
// and a test can tell them apart whatever the wall clock says.
func TestJustAfterUTCDayChange_StillTheHouseZonesPreviousEvening(t *testing.T) {
	at := JustAfterUTCDayChange(time.Date(2026, 1, 5, 15, 0, 0, 0, time.UTC))
	if hour, min, _ := at.UTC().Clock(); hour != 0 || min != 30 {
		t.Fatalf("the pinned instant is half past UTC midnight: got %02d:%02d", hour, min)
	}
	if got := Today(func() time.Time { return at }); got != "2026-01-04" {
		t.Fatalf("the house zone is still on the previous evening at the pinned instant: got day %s", got)
	}
}

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
