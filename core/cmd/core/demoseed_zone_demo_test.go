//go:build demo

package main

import "testing"

func TestSeedDay_DemoBuildReadsThePacificDealersDay(t *testing.T) {
	if !seedBuildIsDemo {
		t.Fatal("the demo build must say so")
	}
	b := &builder{in: planInput{Now: zoneClock}}
	if got := b.dayStamp(); got != "20260702" {
		t.Fatalf("the Pacific dealer's day at 21:30 is the second, not tomorrow's third: got %s", got)
	}
	if got := seedLocation().String(); got != glsDealerConfig.BusinessZone {
		t.Fatalf("the seed reads the day in the demo dealer's configured zone %s, got %s", glsDealerConfig.BusinessZone, got)
	}
}
