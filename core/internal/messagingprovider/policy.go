package messagingprovider

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// policy.go, WHEN a text may go out, and HOW FAST.
//
// Two rules, both of which exist because a text message is an interruption of a person rather than a
// row in a queue:
//
//  1. SEND WINDOW. Nothing this program sends is urgent enough to wake somebody. Notices are held
//     outside 08:00-20:00 in the recipient's local time (America/Toronto by default, Dibbits'
//     branches, and the great majority of its customers, are in Ontario). The window is applied in a
//     real IANA zone, not a fixed offset, so it follows the DST change instead of quietly becoming
//     07:00-19:00 for five months of the year.
//
//  2. RATE CAP. Carriers police throughput, and a burst that looks like a spam run gets the SENDING
//     NUMBER filtered, not the individual message. A cap of a few messages a second is not a
//     performance decision, it is what keeps the number deliverable at all.
//
// -------------------------------------------------------------------------------------------------
// TIME IS INJECTED. NO TEST IN THIS FILE SLEEPS.
// Every function here takes the instant it is reasoning about, or reads it from a Clock the caller
// supplies. A sleep-based test of a send window would need to be twelve hours long to be honest and
// is otherwise a coin flip that fails in CI at 20:01; a fake clock tests the boundary at 07:59:59 and
// 08:00:00 exactly, in a microsecond, on any machine in any timezone.
// -------------------------------------------------------------------------------------------------

// Clock returns "now". Production passes time.Now; tests pass a function over a variable they move.
type Clock func() time.Time

// DefaultZone is the business's operating timezone. Everything customer-facing that has an hour on it
// is reckoned here unless a customer said otherwise (customer_sms_prefs.time_zone, 0109).
const DefaultZone = "America/Toronto"

// The default window: 08:00 to 20:00 local, as minutes past midnight. These are the same numbers the
// 0109 defaults carry, deliberately, the per-customer preference starts as the house rule.
const (
	DefaultQuietStartMin = 8 * 60  // 480
	DefaultQuietEndMin   = 20 * 60 // 1200
)

// DefaultRatePerMinute is the carrier-friendly ceiling: 30 messages a minute (one every two seconds).
// Long-code A2P throughput in Canada is ~1 message/second before filtering; half of that is a
// deliberately conservative default for a yard that sends dozens of notices a day, not thousands.
const DefaultRatePerMinute = 30

// PolicyFromEnv builds the send policy from the environment, defaulting to the house rule.
//
// WHY THIS IS CONFIGURABLE AT ALL. The window is a business decision, not a physical constant: a yard
// that opens at 06:00 for contractors loading before a pour has a legitimate reason to widen it, and a
// branch that gets complaints has a legitimate reason to narrow it. Hard-coding 08:00-20:00 would mean
// the only way to change it is a deploy, which in practice means it never changes and somebody
// eventually disables the whole policy instead.
//
// The names, and what they do NOT do:
//
//	SMS_WINDOW_START_MIN  local minutes past midnight the window opens (default 480 = 08:00)
//	SMS_WINDOW_END_MIN    local minutes past midnight it closes        (default 1200 = 20:00)
//	SMS_WINDOW_ZONE       IANA zone the window is read in              (default America/Toronto)
//	SMS_RATE_PER_MIN      carrier-friendly cap                         (default 30)
//
// A value that is out of range, inverted or degenerate falls back to the house rule rather than being
// clamped or accepted, see NewQuietHours. There is no value of any of these that turns the policy
// OFF, deliberately: "send at any hour" is not a configuration this program offers.
//
// ZERO IS NOT A PAUSE. SMS_RATE_PER_MIN=0 is refused and the default of 30 applies, exactly like a
// negative or unparseable value; it does not hold sends. Every such fallback is recorded (Fallbacks)
// and the boot path logs it, so an operator who typed 0 during a carrier incident is told the cap
// is still 30 instead of finding out from the carrier.
func PolicyFromEnv() *SendPolicy {
	loc, zerr := LoadZone(env("SMS_WINDOW_ZONE", ""))
	r := &policyRead{raw: map[string]string{}}
	start, end := intEnv("SMS_WINDOW_START_MIN", DefaultQuietStartMin, 0, r), intEnv("SMS_WINDOW_END_MIN", DefaultQuietEndMin, 0, r)
	win := NewQuietHours(start, end, loc)
	// NewQuietHours replaces an out of range or degenerate pair as a whole, which can discard a value
	// that parsed fine; record each variable the operator set whose value did not survive.
	if win.StartMin != start || win.EndMin != end {
		r.recordWindowPair()
	}
	p := NewSendPolicy().
		WithWindow(win).
		WithRate(intEnv("SMS_RATE_PER_MIN", DefaultRatePerMinute, 1, r))
	p.zoneErr = zerr
	p.fallbacks = r.fbs
	return p
}

// PolicyFallback is one SMS_* policy variable whose value was refused and replaced by its default.
// Value is the text read from the environment (a plain number or the junk the operator typed, never
// a secret), Default is the number applied instead.
type PolicyFallback struct {
	Var     string
	Value   string
	Default int
}

// Fallbacks reports every policy variable that fell back to its default, in a stable order, so the
// boot path can log one warning each. Empty when the environment was unset or fully valid.
func (p *SendPolicy) Fallbacks() []PolicyFallback {
	return append([]PolicyFallback(nil), p.fallbacks...)
}

// policyRead accumulates what PolicyFromEnv read: the raw text of each variable that was set and the
// fallbacks taken, so the pair check can name values without reading the environment a second time.
type policyRead struct {
	raw map[string]string
	fbs []PolicyFallback
}

func (r *policyRead) fellBack(k string, def int, v string) {
	r.fbs = append(r.fbs, PolicyFallback{Var: k, Value: v, Default: def})
}

// recordWindowPair adds the window variables the operator set but the pair check discarded. A
// variable already recorded (it failed to parse) is not recorded twice.
func (r *policyRead) recordWindowPair() {
	for _, v := range []struct {
		name string
		def  int
	}{{"SMS_WINDOW_START_MIN", DefaultQuietStartMin}, {"SMS_WINDOW_END_MIN", DefaultQuietEndMin}} {
		raw := r.raw[v.name]
		if raw == "" || r.recorded(v.name) {
			continue
		}
		r.fellBack(v.name, v.def, raw)
	}
}

func (r *policyRead) recorded(name string) bool {
	for _, f := range r.fbs {
		if f.Var == name {
			return true
		}
	}
	return false
}

// ZoneError reports a bad SMS_WINDOW_ZONE so the boot path can log it. The policy is usable either
// way, it fell back to the business zone.
func (p *SendPolicy) ZoneError() error { return p.zoneErr }

// intEnv reads an integer environment variable that must be at least min. Unset gives the default
// silently; a value below min or unparseable gives the default and records a PolicyFallback on r.
func intEnv(k string, def, min int, r *policyRead) int {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def
	}
	r.raw[k] = v
	n, err := strconv.Atoi(v)
	if err != nil || n < min {
		r.fellBack(k, def, v)
		return def
	}
	return n
}

// LoadZone resolves an IANA zone name, falling back to the business default and then to UTC.
//
// A BAD ZONE NAME MUST NOT WIDEN THE WINDOW. If a customer row carries a typo, the fallback is the
// business zone, never "no zone", which would evaluate the window in UTC and text an Ontario
// contractor at 04:00. The error is returned so the caller can log it; the *Location is always usable.
func LoadZone(name string) (*time.Location, error) {
	if name != "" {
		if loc, err := time.LoadLocation(name); err == nil {
			return loc, nil
		}
	}
	loc, err := time.LoadLocation(DefaultZone)
	if err != nil {
		// A container without tzdata. UTC is wrong but deterministic, and the error says so out loud.
		return time.UTC, fmt.Errorf("messagingprovider: timezone %q unavailable and %s could not be loaded: %w", name, DefaultZone, err)
	}
	if name == "" {
		return loc, nil
	}
	return loc, fmt.Errorf("messagingprovider: unknown timezone %q, falling back to %s", name, DefaultZone)
}

// QuietHours is the window during which a message MAY be sent, expressed in local minutes past
// midnight. The name follows the customer-facing term ("quiet hours") even though the fields describe
// the OPEN window, StartMin is when quiet ends, EndMin is when quiet resumes.
type QuietHours struct {
	StartMin int            // inclusive, local minutes past midnight
	EndMin   int            // exclusive
	Loc      *time.Location // never nil after NewQuietHours
}

// NewQuietHours builds a window, defaulting anything out of range to the house rule. It never returns
// a window that permits sending at 03:00 by accident: an inverted or degenerate pair is replaced.
func NewQuietHours(startMin, endMin int, loc *time.Location) QuietHours {
	if loc == nil {
		loc, _ = LoadZone("")
	}
	if startMin < 0 || startMin > 1439 || endMin < 0 || endMin > 1439 || startMin == endMin {
		startMin, endMin = DefaultQuietStartMin, DefaultQuietEndMin
	}
	return QuietHours{StartMin: startMin, EndMin: endMin, Loc: loc}
}

// Allows reports whether t falls inside the send window, evaluated in the window's own zone.
//
// A window whose start is after its end wraps midnight (e.g. 20:00→08:00 would be a night window).
// That is not a configuration this business uses, but the arithmetic is written for it rather than
// silently mis-answering if somebody ever sets it.
func (q QuietHours) Allows(t time.Time) bool {
	local := t.In(q.Loc)
	min := local.Hour()*60 + local.Minute()
	if q.StartMin < q.EndMin {
		return min >= q.StartMin && min < q.EndMin
	}
	return min >= q.StartMin || min < q.EndMin
}

// NextOpen returns the earliest instant at or after t at which the window is open. When t is already
// inside the window it returns t unchanged, so callers can use it unconditionally as "the time this
// may be sent".
//
// This is what a scheduled notice's not_before is computed from: rather than claiming a row at 22:00
// and putting it back (which burns dead-letter attempts on a row that has not failed at anything), the
// producer stamps the row with the next instant the recipient may be disturbed.
func (q QuietHours) NextOpen(t time.Time) time.Time {
	if q.Allows(t) {
		return t
	}
	local := t.In(q.Loc)
	open := q.openOn(local)
	if !open.After(local) {
		// Already past today's opening (i.e. it is evening), the next opening is tomorrow's.
		open = q.openOn(local.AddDate(0, 0, 1))
	}
	return open
}

// openOn returns the window's opening instant on the local calendar day of `day`.
//
// IT BUILDS THE WALL-CLOCK TIME DIRECTLY RATHER THAN ADDING A DURATION TO MIDNIGHT, and that is not a
// stylistic preference, it is the spring-forward bug. On 2026-03-08 in Toronto, local midnight is EST
// and 08:00 is EDT, so "midnight plus eight hours" of ELAPSED time lands at 09:00 wall clock: the
// notice held overnight would be released an hour late every March, and an hour early every November.
// time.Date resolves the wall-clock time in the zone, which is what "eight in the morning" means to
// the person receiving the text.
func (q QuietHours) openOn(day time.Time) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), q.StartMin/60, q.StartMin%60, 0, 0, q.Loc)
}

// SendPolicy is the gate the outbox consumer asks before draining: is the window open, and how many
// messages may go out in this tick.
//
// It is a token bucket refilled from the injected clock, NOT a ticker and NOT a sleep. The bucket
// carries at most one minute of allowance, so a runner that was idle overnight does not accumulate
// 43,200 tokens and then empty the queue in one burst at 08:00, which is precisely the traffic shape
// that gets a number filtered.
type SendPolicy struct {
	window QuietHours
	rate   int // messages per minute
	now    Clock

	mu      sync.Mutex
	tokens  float64
	last    time.Time
	zoneErr error

	fallbacks []PolicyFallback
}

// NewSendPolicy builds the house policy: 08:00-20:00 America/Toronto, 30 messages a minute, real time.
func NewSendPolicy() *SendPolicy {
	loc, _ := LoadZone("")
	return &SendPolicy{
		window: NewQuietHours(DefaultQuietStartMin, DefaultQuietEndMin, loc),
		rate:   DefaultRatePerMinute,
		now:    time.Now,
		tokens: float64(DefaultRatePerMinute),
	}
}

// WithClock injects the clock. This is the seam every test in this package uses; production never
// calls it.
func (p *SendPolicy) WithClock(c Clock) *SendPolicy {
	if c != nil {
		p.mu.Lock()
		p.now = c
		p.last = time.Time{} // re-anchor the bucket to the injected clock's first reading
		p.mu.Unlock()
	}
	return p
}

// WithWindow overrides the send window.
func (p *SendPolicy) WithWindow(q QuietHours) *SendPolicy {
	p.mu.Lock()
	p.window = q
	p.mu.Unlock()
	return p
}

// WithRate overrides the per-minute cap (<=0 is ignored, an uncapped SMS sender is not a
// configuration this package offers, and zero is not a pause either; PolicyFromEnv records the
// refusal so the boot path can say so).
func (p *SendPolicy) WithRate(perMinute int) *SendPolicy {
	if perMinute > 0 {
		p.mu.Lock()
		p.rate = perMinute
		p.tokens = float64(perMinute)
		p.mu.Unlock()
	}
	return p
}

// Window exposes the configured send window (producers use it to compute not_before).
func (p *SendPolicy) Window() QuietHours {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.window
}

// Allowance reports how many messages may be sent right now, and, when the answer is zero, why, in
// words an operator can act on. It does NOT consume; Consume does, once the caller knows how many it
// actually sent.
func (p *SendPolicy) Allowance(want int) (int, string) {
	if want <= 0 {
		return 0, ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	now := p.now()
	if !p.window.Allows(now) {
		return 0, "outside the send window (" + p.windowLabel() + ")"
	}
	p.refill(now)
	n := int(p.tokens)
	if n <= 0 {
		return 0, fmt.Sprintf("rate cap reached (%d/min)", p.rate)
	}
	if n > want {
		n = want
	}
	return n, ""
}

// Consume records n messages actually sent against the bucket.
func (p *SendPolicy) Consume(n int) {
	if n <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refill(p.now())
	p.tokens -= float64(n)
	if p.tokens < 0 {
		p.tokens = 0
	}
}

// refill adds the tokens earned since the last reading, capped at one minute's worth. Caller holds mu.
func (p *SendPolicy) refill(now time.Time) {
	if p.last.IsZero() {
		p.last = now
		return
	}
	elapsed := now.Sub(p.last)
	if elapsed <= 0 {
		return
	}
	p.last = now
	p.tokens += elapsed.Minutes() * float64(p.rate)
	if p.tokens > float64(p.rate) {
		p.tokens = float64(p.rate) // never bank more than one minute of burst
	}
}

// windowLabel renders the window for a log line. Caller holds mu.
func (p *SendPolicy) windowLabel() string {
	f := func(m int) string { return fmt.Sprintf("%02d:%02d", m/60, m%60) }
	return f(p.window.StartMin) + "-" + f(p.window.EndMin) + " " + p.window.Loc.String()
}
