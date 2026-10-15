package main

// demoseed_zone_test.go, the seed's calendar day is the dealer's day. The default build seeds the
// Ontario dealer and reads its day in Toronto, as it always has; the demo build seeds the Pacific
// dealer (the demo_test twin of this file) and reads its day in Los Angeles. The clock is 21:30 on
// the second of a month in the Pacific zone, which is already the third in Toronto and in UTC.

import (
	"testing"
	"time"
)

var zoneClock = time.Date(2026, 7, 3, 4, 30, 0, 0, time.UTC)

func TestSeedDay_DefaultBuildReadsTheOntarioDealersDay(t *testing.T) {
	if seedBuildIsDemo {
		t.Skip("the demo build seeds the Pacific dealer; see the demo twin of this test")
	}
	b := &builder{in: planInput{Now: zoneClock}}
	if got := b.dayStamp(); got != "20260703" {
		t.Fatalf("the Ontario dealer's day at this instant is the third, got %s", got)
	}
}
