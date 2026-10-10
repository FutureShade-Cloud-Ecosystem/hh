package houseday

// evening_guard_test.go, the proof that the CI evening guard really moved the wall clock.
//
// The guard step runs the date sensitive packages with HH_EVENING_GUARD=1, the clock overlay
// (scripts/evening-guard/prepare.sh) and a database clock shifted by the same amount. If the overlay
// stopped applying (a toolchain change, a dropped flag) the step would pass in daylight while
// guarding nothing, so this leg fails the step instead. Without the variable the leg skips, so
// ordinary runs are untouched.

import (
	"os"
	"testing"
	"time"
)

func TestEveningGuard_TheClockTheCodeReadsIsInTheWindow(t *testing.T) {
	if os.Getenv("HH_EVENING_GUARD") != "1" {
		t.Skip("not the evening guard run")
	}
	now := time.Now()
	if utc, house := now.UTC().Format("2006-01-02"), Today(nil); utc == house {
		t.Fatalf("the clock reads %s: the UTC day %s and the house day %s are the same, so the guard pinned nothing", now.UTC().Format(time.RFC3339), utc, house)
	}
	if h := now.UTC().Hour(); h > 3 {
		t.Fatalf("the clock reads %s, want just after the UTC day change", now.UTC().Format(time.RFC3339))
	}
}
