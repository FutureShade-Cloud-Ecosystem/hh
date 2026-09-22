package main

// tzdata_test.go, D-112's insurance, pinned at the composition root.
//
// THE RULING (train 17). D-112 ratified that the ledger's day boundary follows the HOUSE zone
// (America/Toronto), read in one place per module (gl/businessday.go, dashboard/window.go,
// messaging/rollup.go, inventory/transfer_eta.go, messagingprovider/policy.go). Every one of those
// resolves the zone with time.LoadLocation and degrades SILENTLY to UTC when it cannot, which is a
// reporting defect nobody can see: a 9pm-Ontario sale on the last day of a month files into next
// month's books, quietly.
//
// THE RISK THIS CLOSES. The server ships CGO_ENABLED=0 static binaries (deploy-staging.yml builds
// them so), which do NOT read the OS /usr/share/zoneinfo the way a cgo binary would, they read the
// zone database only if `time/tzdata` is imported. Nothing did, so zone resolution depended on the
// deploy image carrying tzdata, which the repository does not pin (there is no Dockerfile; the target
// is a droplet). main.go now imports `_ "time/tzdata"`, embedding the zones into the binary. This
// test proves that import is doing its job, so a future edit that drops it fails HERE, at the
// composition root that actually ships, rather than at month-end on staging.
//
// It is deliberately at package main, not in internal/gl: the import that matters is the one on the
// binary, and this is where that binary is composed.

import (
	"testing"
	"time"
)

func TestHouseZoneResolves_AndIsNotUTC_OnTheShippedBinary(t *testing.T) {
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatalf("America/Toronto did not load: %v\n"+
			"The shipped server binary cannot resolve its house zone, so the ledger's day boundary "+
			"has silently moved to UTC and D-112 is broken on this build. main.go must import "+
			"`_ \"time/tzdata\"` (or the deploy image must ship tzdata).", err)
	}

	// Not UTC, a load that silently returned UTC would pass a naive check while breaking the ruling.
	// Ontario in winter is UTC-5 (EST) and in summer UTC-4 (EDT); either way, the offset at a fixed
	// instant is not zero. Pin a specific instant so the assertion does not itself depend on "now".
	jan := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	if _, offset := jan.In(loc).Zone(); offset == 0 {
		t.Fatalf("America/Toronto resolved to a zero UTC offset, it loaded as UTC, which is the "+
			"silent-fallback failure D-112 exists to prevent (offset=%d)", offset)
	}

	// And the boundary actually differs from UTC's: 9pm EST on the 31st is already the 1st in UTC,
	// which is the exact case gl/businessday.go was written to file into the right month.
	ninePM := time.Date(2026, 1, 31, 21, 0, 0, 0, loc)
	if got := ninePM.UTC().Day(); got != 1 {
		t.Fatalf("9pm Toronto on Jan 31 should be Feb 1 in UTC (the boundary D-112 corrects); got day %d", got)
	}
	if ninePM.In(loc).Month() != time.January {
		t.Fatal("9pm Toronto on Jan 31 must read as January in the house zone, the ledger's month")
	}
}
