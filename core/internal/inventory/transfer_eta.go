package inventory

// transfer_eta.go, DIB-472: the promised arrival date on a stock transfer, and the admin-maintained
// per-branch-pair transit time that supplies its default.
//
// - WHAT CHANGED, AND WHAT DID NOT -
//
// DIB-460 refused to invent an ETA, and it was right to (inventory/incoming.go says so at length): a
// date derived from a guessed transit time is a date nothing in the database supports, and people plan
// around dates. Ruling D-083 does not overturn that rule, it satisfies it. The date is now:
//
//	CAPTURED, not derived at read time. It is a stored column, written once, by a person who was
//	looking at it, at the moment the truck was loaded.
//	DEFAULTED FROM CONFIGURATION, not from a constant. The transit time per branch pair is a row an
//	admin owns (branch_transit_days, 0158), with the date it was last reviewed on it.
//	OPTIONAL, ALWAYS. A pair nobody has configured pre-fills nothing, stores NULL, and the surface
//	goes on saying "shipped Aug 12". There is no fallback number anywhere in this file.
//
// - THE CAPTURE POINT IS SHIP, AND WHY NOT CREATE -
//
// A draft transfer is a plan (transfer.go's own words) and a plan has no departure date, so an eta
// stamped at create would be a promise about a truck nobody has committed to loading. Ship is the
// moment the source branch commits: it is already the moment the overdraft guard fires, the moment the
// cost is frozen onto the lines, and the moment shipped_at is stamped. The eta is a fact about that
// same event, so it is captured in that same transaction, one write, one commit, no window in which a
// transfer is IN_TRANSIT with an eta the clerk has not seen yet.
//
// - PRECEDENCE: WHAT THE CLERK SAW IS WHAT IS STORED -
//
// The ship request carries a ShipETA whose two states are deliberately distinguishable:
//
//	not supplied → the caller expressed no opinion. The server defaults from the branch pair (and
//	               from NOTHING when the pair has no row). This is the path the shipped API contract
//	               takes, a POST with no eta field behaves exactly as it did before 0158, except the
//	               configured pair now fills the column in.
//	supplied     → the caller edited the field. The value is stored verbatim, and an EMPTY value is
//	               an ANSWER ("no promised date"), not an omission, a clerk who clears the box has
//	               told us something and the server must not re-default over the top of it.
//
// The browser pre-fills the same arithmetic from the same table (inventory-stock-logic.ts,
// defaultShipETA) and posts what it displayed. So when the two could disagree, a ship pressed within
// a minute of local midnight, the stored date is the one the person was looking at, which is the only
// one they can be held to.

import (
	"context"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/jackc/pgx/v5"
)

// etaZone is the house zone. Declared here rather than imported for the reason messaging/rollup.go
// gives for its own copy: the module keeps its modular-monolith boundary.
//
// The transit arithmetic MUST be done in local time. A ship pressed at 8pm in Trenton is 00:00 UTC the
// next day, so a UTC "today + 1" would promise a truck that leaves tomorrow morning arrives the day
// after, off by one, on a date the whole ticket exists to make trustworthy.
const etaZone = "America/Toronto"

// etaLocation resolves the house zone, falling back to UTC if the image carries no tzdata, the same
// trade the rollup makes: a date one midnight out is a cosmetic defect, a refused shipment is a real
// one, and the truck is loaded either way.
func etaLocation() *time.Location {
	loc, err := time.LoadLocation(etaZone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// etaDayFmt is the one date spelling on this wire: YYYY-MM-DD, matching purchasing's expected_at, so
// the two sentences DIB-460 puts side by side are formatted by the same browser helper.
const etaDayFmt = "2006-01-02"

// ShipETA is the eta decision a ship request carries. See the precedence note in the file header:
// Supplied=false is "no opinion, use the pair's default"; Supplied=true with an empty Value is the
// clerk having cleared the field, which is a promise deliberately withheld.
type ShipETA struct {
	Supplied bool
	Value    string
}

// gradeETA turns a hand-typed date into either a canonical YYYY-MM-DD or a 400 in plain words.
//
// THE productIDParam RULE, APPLIED TO A DATE (handler.go, DIB-443/451). eta is a `date` column, so
// "aug 20" or "2026-13-40" reaches the driver as an uncastable literal, comes back as an untyped pgx
// error, and writeErr, correctly, given what it was handed, calls it "internal error" and answers
// 500. The user mistyped and the system says it broke. Grading here turns the whole class into a 400
// that names the field and what it wants, and keeps 500 meaning "the server actually failed".
//
// time.Parse with the exact layout does BOTH halves of the grade in one pass: it rejects the wrong
// shape AND the impossible day, so 2026-02-31, which a regexp on digits would wave through, and which
// Postgres would then refuse from inside the ship transaction, rolling back a truck that was loaded -
// is refused at the door.
func gradeETA(raw string) (string, error) { return gradeETAIn(houseday.DefaultZone(), raw) }

// gradeETAIn is gradeETA in a named dealer's zone: the date is read as the dealer's calendar day.
func gradeETAIn(zone houseday.Zone, raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		// Not an error. An empty eta is the honest "no promised date", and this function's callers
		// treat it as such; it is spelled out here so nobody adds a "required" branch by accident.
		return "", nil
	}
	d, err := time.ParseInLocation(etaDayFmt, trimmed, zone.Location())
	if err != nil {
		return "", badReq("inventory: expected arrival must be a date like 2026-08-20, leave it blank if there is no promised date")
	}
	return d.Format(etaDayFmt), nil
}

// defaultETA is the pre-fill arithmetic: the ship day plus the pair's transit days, in the house zone.
//
// AddDate on a zoned time walks the CALENDAR, so a transfer shipped the day before a DST change still
// lands on the day the yard means (adding 24h of wall clock would slide it by an hour and, twice a
// year, by a day).
func defaultETA(shippedAt time.Time, transitDays int) string {
	return defaultETAIn(houseday.DefaultZone(), shippedAt, transitDays)
}

// defaultETAIn is defaultETA in a named dealer's zone. The zero houseday.Zone is America/Toronto,
// the etaZone a dealer with no configuration is filed under.
func defaultETAIn(zone houseday.Zone, shippedAt time.Time, transitDays int) string {
	return shippedAt.In(zone.Location()).AddDate(0, 0, transitDays).Format(etaDayFmt)
}

// resolveShipETA decides the eta a ship writes, inside the ship transaction.
//
// It returns "" for "no promised date", which the repository binds as SQL NULL. Nothing in here can
// produce a date the caller did not either type or configure: the only source of a number is the
// branch_transit_days row, and a missing row returns the empty string rather than reaching for a
// default. That is the property the ticket's own worry is about, so it is a straight line with no
// else-branch that guesses.
func (s *Service) resolveShipETA(ctx context.Context, tx pgx.Tx, zone houseday.Zone, from, to string, in ShipETA, shippedAt time.Time) (string, error) {
	if in.Supplied {
		// Graded even when it came from our own pre-fill: the field is a text input a person can type
		// into, and the server is the only place a grade is load-bearing.
		return gradeETAIn(zone, in.Value)
	}
	days, ok, err := s.repo.TransitDaysForPair(ctx, tx, from, to)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}
	return defaultETAIn(zone, shippedAt, days), nil
}

// - the admin-maintained pairs -

// TransitDaysPair is one configured route, as the admin surface reads and writes it.
//
// The branch NAMES and CODES ride along because the surface lists routes, and a table of UUID pairs is
// not something a person can maintain, which would defeat the entire point of the row being editable.
type TransitDaysPair struct {
	FromBranchID string `json:"from_branch_id"`
	FromCode     string `json:"from_branch_code"`
	FromName     string `json:"from_branch_name"`
	ToBranchID   string `json:"to_branch_id"`
	ToCode       string `json:"to_branch_code"`
	ToName       string `json:"to_branch_name"`
	TransitDays  int    `json:"transit_days"`
	// Configured=false is a branch pair with NO row: the surface offers it as an empty field, and a
	// ship between those yards promises nothing. It is a distinct fact from transit_days = 0, which is
	// somebody having said "same day", and the two must never render identically.
	Configured bool `json:"configured"`
	// UpdatedAt is the last time a person reviewed this number, '' when never. The surface shows it
	// because the ticket's worry is staleness: an unmaintained default is worse than no default, and a
	// date is the cheapest thing that makes "nobody has looked at this since March" visible.
	UpdatedAt string `json:"updated_at"`
}

// maxTransitDays mirrors the 0158 CHECK. Graded here too, so a bad number is a 400 in plain words
// rather than a constraint violation the taxonomy can only call "internal error".
const maxTransitDays = 60

// TransitDaysConfig lists EVERY ordered pair of branches, configured or not.
//
// Not just the stored rows. An admin's question is "what does each route promise", and a route with no
// row is exactly the one they need to see, it is the one shipping with no eta. Listing only what is
// stored would hide the gap the surface exists to close.
func (s *Service) TransitDaysConfig(ctx context.Context) ([]TransitDaysPair, error) {
	return s.repo.ListTransitDays(ctx)
}

// SetTransitDaysInput is one admin edit.
type SetTransitDaysInput struct {
	FromBranchID string `json:"from_branch_id"`
	ToBranchID   string `json:"to_branch_id"`
	// TransitDays is the days added to a ship date. 0 is legal and means same-day.
	TransitDays int `json:"transit_days"`
	// Clear removes the row instead of storing a number, "we do not promise arrivals on this route".
	// It is a separate flag rather than a magic negative day count, because "unset" and "zero days" are
	// different answers and the schema keeps them different.
	Clear     bool   `json:"clear"`
	UpdatedBy string `json:"-"`
}

// SetTransitDays stores (or clears) one route's transit time.
//
// The branch ids are graded before they can reach a uuid cast, for the DIB-451 reason: a mistyped id
// is the user's typo and must read as one, not as the server failing.
func (s *Service) SetTransitDays(ctx context.Context, in SetTransitDaysInput) (TransitDaysPair, error) {
	from := strings.TrimSpace(in.FromBranchID)
	to := strings.TrimSpace(in.ToBranchID)
	if from == "" || to == "" {
		return TransitDaysPair{}, badReq("inventory: from_branch_id and to_branch_id are required")
	}
	if from == to {
		return TransitDaysPair{}, badReq("inventory: a transit time needs two different branches")
	}
	if in.Clear {
		if err := s.repo.DeleteTransitDays(ctx, from, to); err != nil {
			return TransitDaysPair{}, err
		}
		return s.repo.TransitDaysPair(ctx, from, to)
	}
	if in.TransitDays < 0 || in.TransitDays > maxTransitDays {
		return TransitDaysPair{}, badReq("inventory: transit days must be a whole number of days from 0 to %d", maxTransitDays)
	}
	if err := s.repo.UpsertTransitDays(ctx, from, to, in.TransitDays, in.UpdatedBy); err != nil {
		return TransitDaysPair{}, err
	}
	return s.repo.TransitDaysPair(ctx, from, to)
}
