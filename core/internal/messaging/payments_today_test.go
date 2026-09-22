package messaging

import (
	"context"
	"testing"
	"time"
)

// - DIB-463 -
//
// "Same underlying data as the #payments channel, one source, two surfaces." Every test here exists
// to hold that sentence to account. The tile is not allowed to be *approximately* the rollup.

// - ONE CLOCK -
//
// Every instant in this file is DERIVED from time.Now(), never pinned to a calendar date. The rollup
// writer stamps its rows with the real clock (PostRoutineEvent takes time.Now() from the consumer),
// so a fixture that pinned "2026-08-11" and then asked for that day's total would assert nothing the
// moment the two drifted apart, the DIB-427 / W6D-7 dated-time-bomb failure, enforced by
// internal/testaudit. Derived instants keep the fixture and the code under test on the same clock.

// utcMidnightCrossing is an instant just after a UTC midnight, which is therefore the PREVIOUS
// evening in Ontario (20:30 EDT / 19:30 EST). It is the one instant where "which day is this?" has
// two different honest answers, which is exactly what the house-day test needs, and it is computed
// from the real clock, so it is true every day rather than until one.
func utcMidnightCrossing() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 30, 0, 0, time.UTC).AddDate(0, 0, 1)
}

// - THE ONE-SOURCE PROPERTY -

// MUTATION TARGET: make the tile recompute the day from payments instead of reading the rollup and
// this test dies.
//
// The double deliberately holds TWO answers: the rollup roots the channel renders, and a "raw ledger"
// count that disagrees with them. Nothing in the shipped code can reach the second one, which is the
// point. If the tile ever grew its own aggregate, this fixture is the shape that catches it: the
// numbers on the card must be the numbers on the day line, character for character, and never a
// second opinion about the same day.
func TestPaymentsToday_ReadsTheRollupNotThePayments(t *testing.T) {
	c, f := rollupFixture(t)
	// The raw ledger says something else entirely, a voided payment, a row the outbox never
	// delivered, a different day boundary. A recomputing tile would report THIS.
	f.setRawPayments(99, 9_999_99)

	payEvent(t, c, "p1", "SO-1041", 124000, fkBrTrenton)
	payEvent(t, c, "p2", "SO-1042", 388050, fkBrTrenton)
	payEvent(t, c, "p3", "SO-1043", 92075, fkBrTrenton)

	root := onlyRoot(t, f, fkPayTrenton)
	got, err := svcWith(f).PaymentsTodayFor(context.Background(), []string{fkBrTrenton}, false, time.Now())
	if err != nil {
		t.Fatalf("payments today: %v", err)
	}
	if got.Count != 3 || got.Cents != 604125 {
		t.Fatalf("the tile must report the ROLLUP's numbers (3 · $6,041.25), got %d · %d", got.Count, got.Cents)
	}
	// And they must be the SAME numbers the day line is showing, not merely equal-looking ones.
	if want := renderRollupBody(got.Count, got.Cents, got.Day); want != root.Body {
		t.Fatalf("tile and channel must render the same day:\n  tile → %q\n  line → %q", want, root.Body)
	}
}

// The day the tile reports is the ROLLUP's day (America/Toronto), not the server's UTC date. A tile
// that says "today" has to be able to say which today it means, and it must be the same one the day
// line was filed under.
func TestPaymentsToday_UsesTheHouseDayNotUTC(t *testing.T) {
	f := newFakeStore()
	// Just after a UTC midnight is still the previous evening in Ontario, the evening's takings.
	at := utcMidnightCrossing()
	got, err := svcWith(f).PaymentsTodayFor(context.Background(), nil, true, at)
	if err != nil {
		t.Fatalf("payments today: %v", err)
	}
	if got.Day != rollupDay(at) {
		t.Fatalf("the tile's day must be the rollup's day, got %q want %q", got.Day, rollupDay(at))
	}
	if got.Day == at.Format("2006-01-02") {
		t.Skip("machine tzdata missing: rollupDay fell back to UTC, so this assertion cannot discriminate")
	}
}

// An empty day is a MEASURED ZERO, not a missing measurement. It is 9am and nobody has paid yet: the
// card says $0.00, because "we have not seen a payment today" is a fact, and an em-dash would claim
// the module is unreachable.
func TestPaymentsToday_NoPaymentsIsAMeasuredZero(t *testing.T) {
	f := newFakeStore()
	got, err := svcWith(f).PaymentsTodayFor(context.Background(), nil, true, time.Now())
	if err != nil {
		t.Fatalf("payments today: %v", err)
	}
	if got.Count != 0 || got.Cents != 0 || got.Channels != 0 {
		t.Fatalf("a day with no payments measures zero, got %+v", got)
	}
}

// The day's identity is the rollup's own key, so yesterday's line can never be read as today's total.
func TestPaymentsToday_YesterdaysRollupIsNotTodaysNumber(t *testing.T) {
	c, f := rollupFixture(t)
	payEvent(t, c, "p1", "SO-1041", 124000, fkBrTrenton)

	tomorrow := time.Now().Add(26 * time.Hour)
	got, err := svcWith(f).PaymentsTodayFor(context.Background(), []string{fkBrTrenton}, false, tomorrow)
	if err != nil {
		t.Fatalf("payments today: %v", err)
	}
	if got.Count != 0 || got.Cents != 0 {
		t.Fatalf("a different day must not inherit yesterday's total, got %d · %d", got.Count, got.Cents)
	}
}

// - BRANCH SCOPING IS THE TILE'S OWN JOB -
//
// The MetricProvider contract says it in as many words: "the dashboard cannot enforce branch scoping
// on a number it did not compute". So the read enforces it, and a manager with no grant for Trenton
// does not see Trenton's takings on their home screen.
func TestPaymentsToday_HonoursTheCallersBranchGrants(t *testing.T) {
	c, f := rollupFixture(t)
	f.setChannelBranch(fkPayTrenton, fkBrTrenton)
	payEvent(t, c, "p1", "SO-1041", 124000, fkBrTrenton)

	svc := svcWith(f)
	blind, err := svc.PaymentsTodayFor(context.Background(), []string{"br-elsewhere"}, false, time.Now())
	if err != nil {
		t.Fatalf("payments today: %v", err)
	}
	if blind.Count != 0 {
		t.Fatalf("a caller with no grant for Trenton must not see Trenton's takings, got %d", blind.Count)
	}
	granted, err := svc.PaymentsTodayFor(context.Background(), []string{fkBrTrenton}, false, time.Now())
	if err != nil {
		t.Fatalf("payments today: %v", err)
	}
	if granted.Count != 1 {
		t.Fatalf("a caller granted Trenton sees Trenton, got %d", granted.Count)
	}
	global, err := svc.PaymentsTodayFor(context.Background(), nil, true, time.Now())
	if err != nil {
		t.Fatalf("payments today: %v", err)
	}
	if global.Count != 1 {
		t.Fatalf("a GLOBAL-scope caller sees every branch, got %d", global.Count)
	}
}

// A branchless payment lands in the GLOBAL #payments channel (0152). It is nobody's branch and
// everybody's business, so it counts for a branch-scoped caller too, excluding it would silently drop
// money out of the only total that claims to be the day's.
func TestPaymentsToday_IncludesTheGlobalPaymentsChannel(t *testing.T) {
	c, f := rollupFixture(t)
	f.setChannelBranch(fkPayGlobal, "") // the GLOBAL channel has no branch
	payEvent(t, c, "p1", "SO-1041", 50000, "")

	got, err := svcWith(f).PaymentsTodayFor(context.Background(), []string{fkBrTrenton}, false, time.Now())
	if err != nil {
		t.Fatalf("payments today: %v", err)
	}
	if got.Count != 1 || got.Cents != 50000 {
		t.Fatalf("a branchless payment must count for a branch-scoped caller, got %d · %d", got.Count, got.Cents)
	}
}
