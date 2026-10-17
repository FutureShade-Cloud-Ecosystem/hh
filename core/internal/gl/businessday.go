package gl

import (
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// businessday.go, WHICH DAY THE LEDGER THINKS IT IS.
//
// - THE DEFECT THIS CLOSES -
//
// A journal's fiscal period was resolved with `d.OccurredAt.Year(), d.OccurredAt.Month()`, which
// reads the calendar of whatever LOCATION the timestamp happens to carry, and `OccurredAt` defaults
// to `s.clock()`, i.e. `time.Now()`, i.e. the timezone of the process. So the month a posting landed
// in was decided by how the server happened to be configured, and nobody had chosen that.
//
// On a server running UTC, which is nearly all of them, UTC midnight is 8pm in Ontario. A sale rung
// at 9pm on the last day of a month was therefore filed into the NEXT month's books, and a sale rung
// at 9pm on any other day was filed into tomorrow. Nothing failed, nothing warned; the revenue was
// simply in the wrong period, and the only way to notice was to go looking.
//
// It surfaced sideways: on a development host at UTC−7, three integration tests began failing every
// day after 17:00 local, because the Go half of each test and the Postgres half disagreed about the
// date. Those tests were the symptom. This is the cause.
//
// - THE HOUSE HAD ALREADY DECIDED THIS, THREE TIMES -
//
// This file invents no policy. It applies the one this product already holds, in writing, everywhere
// the question has been asked before:
//
//   messaging/rollup.go:50           const rollupZone = "America/Toronto"
//   inventory/transfer_eta.go:59     const etaZone    = "America/Toronto"
//   messagingprovider/policy.go:40   const DefaultZone = "America/Toronto"
//
// And the argument is already written, one table over, at messaging/rollup.go:46-49:
//
//     "The rollup day MUST be local: a UTC day boundary cuts the Ontario working day at 8pm EDT and
//      files the evening's receipts under tomorrow, on the one surface whose entire job is 'what came
//      in today'."
//
// That sentence is about a message board. It is far more true of a general ledger, which is the one
// surface whose entire job is WHICH PERIOD THIS BELONGS TO. The ledger was simply the place that
// never got the memo.
//
// - WHY A LOCAL CONSTANT AND NOT AN IMPORT -
//
// Declared here rather than imported from messaging, for the reason rollup.go gives for doing the
// same: each module keeps its own modular-monolith boundary, and a shared constant would couple the
// ledger to the message board over a fact they merely agree about. Three copies that agree is the
// house posture; `payments_today.go:40` names the zone in prose for the same reason.
//
// WHEN THE DEALER'S ZONE CHANGES, a second yard in another province, a franchise, this constant is
// the one place the ledger's answer lives, and the change is a considered one with a migration behind
// it (every posted journal already carries the period it was filed into, so the past does not move).
// That is the correct shape for a decision of this weight: visible, single, and argued.

// houseZone is the dealer's own zone. Dibbits is in Ontario, and a business day ends when the yard
// closes, not when Greenwich rolls over.
const houseZone = "America/Toronto"

// houseLocation resolves the house zone, falling back to UTC if the tzdata is missing from the image
// rather than failing the post.
//
// The fallback is the same trade rollup.go makes and it is worth stating plainly, because here the
// stakes are higher: a journal filed against the wrong midnight is a reporting defect somebody can
// find and correct with a reclass. A journal that never posted at all is a sale with no accounting
// entry, which is a hole in the ledger. So if the zone cannot be loaded, the posting still happens.
//
// Distroless and scratch images have no tzdata. If this ever silently falls back in production the
// ledger's boundary moves to UTC, so the deployment must either ship tzdata or import
// `time/tzdata`, and TestHouseLocation_ResolvesRatherThanFallingBack fails loudly on a machine where
// it cannot resolve, which is how a build that lost it gets caught before a month-end does.
func houseLocation() *time.Location {
	loc, err := time.LoadLocation(houseZone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// fiscalYearMonth is the (year, month) a posting belongs to, read in the HOUSE zone.
//
// This is the whole fix, and it is deliberately one function rather than an inlined `.In(...)`: the
// ledger has exactly one answer to "which period is this?", and a second call site that forgot the
// conversion would be invisible in review. Anything that needs the ledger's month calls this.
func fiscalYearMonth(at time.Time) (int, int) {
	local := at.In(houseLocation())
	return local.Year(), int(local.Month())
}

// fiscalYearMonthIn is fiscalYearMonth in a named dealer's zone: the dealer's own midnight closes its
// period, so a Los Angeles dealer's 9pm posting on the 31st is that month's, not the next. The zero
// houseday.Zone is America/Toronto, so a dealer with no configuration is exactly fiscalYearMonth.
// This file's houseZone stays as the Ontario default a dealer with no zone is filed under; the zone
// each dealer actually closes its books in is injected (ZoneTxSource), never a process wide setting.
func fiscalYearMonthIn(zone houseday.Zone, at time.Time) (int, int) {
	local := at.In(zone.Location())
	return local.Year(), int(local.Month())
}
