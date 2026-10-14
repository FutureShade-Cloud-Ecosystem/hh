package messagingprovider

import (
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// policy_test.go, the send window and the throttle, tested with an INJECTED CLOCK and no sleeps.
//
// A sleep-based test of "no texts after 8pm" would have to run for twelve hours to be honest, and the
// cheap version of it, sleep a second, assert something, fails in CI at 20:00:01 on somebody else's
// machine in another timezone. Every test here moves a variable instead, so the boundary is asserted
// at 07:59:59 and 08:00:00 exactly, in microseconds, identically everywhere.

// fakeClock is a movable time source.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// toronto is the business zone; every wall-clock assertion below is written in it.
func toronto(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(DefaultZone)
	if err != nil {
		t.Skipf("tzdata unavailable in this container: %v", err)
	}
	return loc
}

func at(t *testing.T, loc *time.Location, y int, m time.Month, d, hh, mm int) time.Time {
	t.Helper()
	return time.Date(y, m, d, hh, mm, 0, 0, loc)
}

// TestQuietHours_WindowBoundariesAreExact pins the two minutes that matter. 07:59 is silence and 08:00
// is a send; nothing in between is left to a >= vs > that nobody checked.
func TestQuietHours_WindowBoundariesAreExact(t *testing.T) {
	loc := toronto(t)
	q := NewQuietHours(DefaultQuietStartMin, DefaultQuietEndMin, loc)

	cases := []struct {
		name string
		when time.Time
		want bool
	}{
		{"one minute before the window opens", at(t, loc, 2026, time.August, 5, 7, 59), false},
		{"the minute it opens", at(t, loc, 2026, time.August, 5, 8, 0), true},
		{"the middle of the afternoon", at(t, loc, 2026, time.August, 5, 14, 30), true},
		{"the last minute of the window", at(t, loc, 2026, time.August, 5, 19, 59), true},
		{"the minute it closes", at(t, loc, 2026, time.August, 5, 20, 0), false},
		{"the middle of the night", at(t, loc, 2026, time.August, 5, 3, 15), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := q.Allows(tc.when); got != tc.want {
				t.Fatalf("Allows(%s) = %v, want %v, the send window is 08:00-20:00 local",
					tc.when.Format("15:04"), got, tc.want)
			}
		})
	}
}

// TestQuietHours_IsEvaluatedInTheCustomersZone_NotUTC is the DST-shaped bug this exists to prevent:
// 03:00 UTC in August is 23:00 the previous evening in Toronto. A window evaluated in UTC would text
// somebody at eleven at night.
func TestQuietHours_IsEvaluatedInTheCustomersZone_NotUTC(t *testing.T) {
	loc := toronto(t)
	q := NewQuietHours(DefaultQuietStartMin, DefaultQuietEndMin, loc)

	utcMidMorning := time.Date(2026, time.August, 6, 3, 0, 0, 0, time.UTC) // 23:00 Aug 5 in Toronto
	if q.Allows(utcMidMorning) {
		t.Fatalf("03:00 UTC is 23:00 in Toronto, Allows must be false, or the window is being read in UTC")
	}
	utcAfternoon := time.Date(2026, time.August, 5, 18, 0, 0, 0, time.UTC) // 14:00 Toronto
	if !q.Allows(utcAfternoon) {
		t.Fatalf("18:00 UTC is 14:00 in Toronto, Allows must be true")
	}
}

// TestQuietHours_NextOpen_HoldsUntilTheMorning is what a scheduled notice's not_before is computed
// from: an evening decision is held to 08:00 the NEXT day, and a decision inside the window is not
// delayed at all.
func TestQuietHours_NextOpen_HoldsUntilTheMorning(t *testing.T) {
	loc := toronto(t)
	q := NewQuietHours(DefaultQuietStartMin, DefaultQuietEndMin, loc)

	evening := at(t, loc, 2026, time.August, 5, 21, 30)
	got := q.NextOpen(evening).In(loc)
	want := at(t, loc, 2026, time.August, 6, 8, 0)
	if !got.Equal(want) {
		t.Fatalf("NextOpen(21:30 Aug 5) = %s, want %s (08:00 the next morning)",
			got.Format(time.RFC3339), want.Format(time.RFC3339))
	}

	preDawn := at(t, loc, 2026, time.August, 5, 5, 0)
	got = q.NextOpen(preDawn).In(loc)
	want = at(t, loc, 2026, time.August, 5, 8, 0)
	if !got.Equal(want) {
		t.Fatalf("NextOpen(05:00) = %s, want 08:00 the SAME day", got.Format(time.RFC3339))
	}

	inWindow := at(t, loc, 2026, time.August, 5, 11, 0)
	if got := q.NextOpen(inWindow); !got.Equal(inWindow) {
		t.Fatalf("NextOpen inside the window must not delay: got %s, want %s",
			got.Format(time.RFC3339), inWindow.Format(time.RFC3339))
	}
}

// TestQuietHours_NextOpen_SurvivesTheSpringForwardDay. Adding 24h to a local wall time across the DST
// boundary lands on 09:00, not 08:00. Constructing the next day from the calendar date does not.
func TestQuietHours_NextOpen_SurvivesTheSpringForwardDay(t *testing.T) {
	loc := toronto(t)
	q := NewQuietHours(DefaultQuietStartMin, DefaultQuietEndMin, loc)

	// 2026-03-07 21:00 local; the clocks go forward overnight on 2026-03-08.
	evening := at(t, loc, 2026, time.March, 7, 21, 0)
	got := q.NextOpen(evening).In(loc)
	if h, m := got.Hour(), got.Minute(); h != 8 || m != 0 {
		t.Fatalf("NextOpen across the spring-forward night = %s (%02d:%02d local), want 08:00, "+
			"the next window must be built from the calendar date, not by adding 24h",
			got.Format(time.RFC3339), h, m)
	}
}

// TestQuietHours_BadConfigFallsBackToTheHouseRule. An inverted or degenerate window must never become
// "always open", the failure direction of a messaging window is not symmetric.
func TestQuietHours_BadConfigFallsBackToTheHouseRule(t *testing.T) {
	loc := toronto(t)
	for _, tc := range []struct{ start, end int }{{-1, 1200}, {480, 5000}, {600, 600}} {
		q := NewQuietHours(tc.start, tc.end, loc)
		if q.StartMin != DefaultQuietStartMin || q.EndMin != DefaultQuietEndMin {
			t.Fatalf("NewQuietHours(%d,%d) = %d-%d, want the house rule %d-%d",
				tc.start, tc.end, q.StartMin, q.EndMin, DefaultQuietStartMin, DefaultQuietEndMin)
		}
		if q.Allows(at(t, loc, 2026, time.August, 5, 3, 0)) {
			t.Fatalf("a bad window config must not permit a 03:00 send")
		}
	}
}

// TestLoadZone_UnknownZoneFallsBackToTheBusinessZone_NotUTC. A typo in a customer row must not widen
// the window by five hours.
func TestLoadZone_UnknownZoneFallsBackToTheBusinessZone_NotUTC(t *testing.T) {
	loc, err := LoadZone("Mars/Olympus_Mons")
	if err == nil {
		t.Fatalf("an unknown zone must return an error alongside the fallback so it can be logged")
	}
	if loc == nil || loc.String() == "UTC" {
		t.Fatalf("unknown zone fell back to %v, want %s, UTC would shift the window by five hours", loc, DefaultZone)
	}
}

// TestSendPolicy_ClosedWindowAllowsNothing is the gate the outbox drain asks. Outside the window the
// answer is zero AND a reason an operator can read.
func TestSendPolicy_ClosedWindowAllowsNothing(t *testing.T) {
	loc := toronto(t)
	clock := &fakeClock{t: at(t, loc, 2026, time.August, 5, 22, 15)}
	p := NewSendPolicy().WithWindow(NewQuietHours(DefaultQuietStartMin, DefaultQuietEndMin, loc)).WithClock(clock.now)

	n, why := p.Allowance(10)
	if n != 0 {
		t.Fatalf("Allowance at 22:15 = %d, want 0, nothing may be sent outside the window", n)
	}
	if why == "" {
		t.Fatalf("a refusal must say why; an operator reading a silent queue needs the reason")
	}

	// Move the clock into the morning window: the same policy now allows sending.
	clock.t = at(t, loc, 2026, time.August, 6, 9, 0)
	if n, why := p.Allowance(10); n != 10 {
		t.Fatalf("Allowance at 09:00 = %d (%s), want 10", n, why)
	}
}

// TestSendPolicy_RateCapThrottlesAndRefillsOnTheClock. The bucket is spent, refills at the configured
// rate as the injected clock moves, and NEVER banks more than one minute of burst, a runner idle
// overnight must not empty the queue in one carrier-filtering burst at 08:00.
func TestSendPolicy_RateCapThrottlesAndRefillsOnTheClock(t *testing.T) {
	loc := toronto(t)
	clock := &fakeClock{t: at(t, loc, 2026, time.August, 5, 10, 0)}
	p := NewSendPolicy().
		WithWindow(NewQuietHours(DefaultQuietStartMin, DefaultQuietEndMin, loc)).
		WithRate(10).
		WithClock(clock.now)

	if n, _ := p.Allowance(100); n != 10 {
		t.Fatalf("first Allowance = %d, want the 10/min cap", n)
	}
	p.Consume(10)
	if n, why := p.Allowance(100); n != 0 {
		t.Fatalf("Allowance immediately after spending the bucket = %d (%s), want 0", n, why)
	}

	// Half a minute later, half the bucket is back.
	clock.advance(30 * time.Second)
	if n, _ := p.Allowance(100); n != 5 {
		t.Fatalf("Allowance after 30s at 10/min = %d, want 5", n)
	}

	// Eight hours later it is still 10, not 4,800.
	clock.advance(8 * time.Hour)
	if n, _ := p.Allowance(10000); n != 10 {
		t.Fatalf("Allowance after an 8h idle = %d, want 10, the bucket must not bank a burst", n)
	}
}

// TestSendPolicy_AllowanceNeverExceedsWhatWasAsked, the drain asks for a batch size and must not be
// handed a larger one.
func TestSendPolicy_AllowanceNeverExceedsWhatWasAsked(t *testing.T) {
	loc := toronto(t)
	clock := &fakeClock{t: at(t, loc, 2026, time.August, 5, 12, 0)}
	p := NewSendPolicy().WithWindow(NewQuietHours(DefaultQuietStartMin, DefaultQuietEndMin, loc)).WithClock(clock.now)
	if n, _ := p.Allowance(3); n != 3 {
		t.Fatalf("Allowance(3) = %d, want 3", n)
	}
}

// TestSMSPrefs_SendableIsTheWholeConsentRule. Each single fact being wrong is enough to refuse.
func TestSMSPrefs_SendableIsTheWholeConsentRule(t *testing.T) {
	base := SMSPrefs{PhoneE164: "+16135551001", Verified: true, OptedOut: false}
	if !base.Sendable() {
		t.Fatalf("a verified, opted-in, E.164 number must be sendable")
	}
	optedOut := base
	optedOut.OptedOut = true
	if optedOut.Sendable() {
		t.Fatalf("an OPTED-OUT customer must never be sendable, that is the kill switch")
	}
	unverified := base
	unverified.Verified = false
	if unverified.Sendable() {
		t.Fatalf("an unverified number must not be sendable, having a number is not consent")
	}
	malformed := base
	malformed.PhoneE164 = "613-555-0101"
	if malformed.Sendable() {
		t.Fatalf("a non-E.164 number must not be sendable")
	}
}

// TestSMSPrefs_QuietHoursUsesTheCustomersOwnWindow.
func TestSMSPrefs_QuietHoursUsesTheCustomersOwnWindow(t *testing.T) {
	loc := toronto(t)
	p := SMSPrefs{QuietStartMin: 10 * 60, QuietEndMin: 16 * 60, TimeZone: DefaultZone}
	q := p.QuietHours()
	if q.Allows(at(t, loc, 2026, time.August, 5, 9, 0)) {
		t.Fatalf("09:00 is outside a customer-set 10:00-16:00 window")
	}
	if !q.Allows(at(t, loc, 2026, time.August, 5, 11, 0)) {
		t.Fatalf("11:00 is inside a customer-set 10:00-16:00 window")
	}
}

// TestBucketFor_TakesTheWorstBucket, a 95-day invoice gets the 90-day message, not the 30-day one,
// and anything under 30 days is not this automation's business at all.
func TestBucketFor_TakesTheWorstBucket(t *testing.T) {
	cases := []struct {
		days int
		want int
		ok   bool
	}{
		{0, 0, false}, {29, 0, false}, {30, 30, true}, {59, 30, true},
		{60, 60, true}, {89, 60, true}, {90, 90, true}, {365, 90, true},
	}
	for _, tc := range cases {
		got, ok := bucketFor(tc.days)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("bucketFor(%d) = (%d,%v), want (%d,%v)", tc.days, got, ok, tc.want, tc.ok)
		}
	}
}

// TestRedactPhone_NeverEchoesTheNumber, an error message about a bad number must not become the place
// the number is logged.
func TestRedactPhone_NeverEchoesTheNumber(t *testing.T) {
	got := redactPhone("+16135551001")
	if got == "+16135551001" || len(got) > 5 {
		t.Fatalf("redactPhone leaked the number: %q", got)
	}
}

// TestPolicyFromEnv_DefaultsToTheHouseRuleAndCannotBeSwitchedOff.
//
// The second half is the important one: there is deliberately no environment value that makes the
// policy permit a 03:00 send. A configuration surface for a courtesy rule is only safe if its failure
// mode is the rule, not the absence of it.
func TestPolicyFromEnv_DefaultsToTheHouseRuleAndCannotBeSwitchedOff(t *testing.T) {
	loc := toronto(t)

	t.Setenv("SMS_WINDOW_START_MIN", "")
	t.Setenv("SMS_WINDOW_END_MIN", "")
	p := PolicyFromEnv()
	if w := p.Window(); w.StartMin != DefaultQuietStartMin || w.EndMin != DefaultQuietEndMin {
		t.Fatalf("unset environment gave %d-%d, want the house rule", w.StartMin, w.EndMin)
	}

	t.Setenv("SMS_WINDOW_START_MIN", "360") // a 06:00 yard opening
	t.Setenv("SMS_WINDOW_END_MIN", "1080")  // closing at 18:00
	p = PolicyFromEnv()
	if w := p.Window(); w.StartMin != 360 || w.EndMin != 1080 {
		t.Fatalf("configured window = %d-%d, want 360-1080", w.StartMin, w.EndMin)
	}
	if p.Window().Allows(at(t, loc, 2026, time.August, 5, 3, 0)) {
		t.Fatalf("a configured window still must not permit a 03:00 send")
	}

	for _, junk := range []string{"nonsense", "-5", "9999"} {
		t.Setenv("SMS_WINDOW_START_MIN", junk)
		t.Setenv("SMS_WINDOW_END_MIN", junk)
		w := PolicyFromEnv().Window()
		if w.StartMin != DefaultQuietStartMin || w.EndMin != DefaultQuietEndMin {
			t.Fatalf("junk value %q gave %d-%d, want the house rule", junk, w.StartMin, w.EndMin)
		}
		if w.Allows(at(t, loc, 2026, time.August, 5, 3, 0)) {
			t.Fatalf("junk configuration %q opened the window at 03:00", junk)
		}
	}
}

// The send window is read in the dealer's zone (item 3b slice 3): 08:00 opens at Los Angeles 08:00, not
// Toronto's, unless the operator named SMS_WINDOW_ZONE, which stands.
func TestSendPolicy_WithDealerZoneMovesTheWindowUnlessTheOperatorNamedOne(t *testing.T) {
	la, err := houseday.NewZone("America/Los_Angeles")
	if err != nil {
		t.Skipf("no tzdata: %v", err)
	}
	t.Setenv("SMS_WINDOW_ZONE", "")
	p := PolicyFromEnv().WithDealerZone(la)
	// 14:30 UTC in August is 10:30 in Toronto and 07:30 in Los Angeles: closed in LA, open in Toronto.
	at := time.Date(2026, time.August, 27, 14, 30, 0, 0, time.UTC)
	if p.window.Allows(at) {
		t.Fatal("a Los Angeles dealer's window was open at 07:30 local")
	}
	if !PolicyFromEnv().window.Allows(at) {
		t.Fatal("the default Ontario window was closed at 10:30 local")
	}

	t.Setenv("SMS_WINDOW_ZONE", "America/Toronto")
	named := PolicyFromEnv().WithDealerZone(la)
	if !named.window.Allows(at) {
		t.Fatal("the operator's SMS_WINDOW_ZONE was overridden by the dealer's zone")
	}
}
