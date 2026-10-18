package messaging

import (
	"errors"
	"regexp"
	"strconv"
	"time"
)

// - Walk change 7, slice S7: per-person quiet hours + desktop-push gate (0401) -
//
// The desktop-push gate answers ONE question for the SSE/desktop-push path's per-recipient
// fan-out (the Feature A / DIB-217 follow-through, the same seam shouldNotify occupies today):
// given one user's per-channel notification prefs, a per-category event-class toggle, the user's
// quiet-hours window and the message facts (forced-ack, channel tier, DM sender), should this
// message raise a desktop notification for that user, right now?
//
// The shape is the same defensive read 0069 used for the un-mutable escalation tier: two
// independent guards on the forced-ack / escalation break-through, so no legacy row or a future
// endpoint that forgets one half can silence an alert. The brief pins both halves:
//
//   * "forced-acknowledgement notices (`requiresAck(topic)`) always reach the recipient in-app
//     and push regardless of preferences"
//   * "escalation rooms (`TierEscalation` / `TierFirehose` with override) and forced
//     acknowledgement notices ... break through quiet hours; everything else waits"
//
// The whole rule matrix is provable without a database (every input is a value type or a
// notification-prefs struct); the integration tests then prove the SQL that persists the rows.

// hhmmRegex is the 24-hour HH:MM gate. The CHECK in 0401 enforces it on the column, so this regex
// is the in-memory counterpart the predicate runs after an in-memory default or a service-level
// patch merge. Two captures: hours (00..23) and minutes (00..59), used to build a time-of-day for
// ActiveAt.
var hhmmRegex = regexp.MustCompile(`^([01][0-9]|2[0-3]):([0-5][0-9])$`)

// parseHHMM splits "HH:MM" into hours and minutes. ok is false for a malformed string (the
// handler validates the body before it ever gets here, so a parse failure is a defence-in-depth
// path, not a normal case).
func parseHHMM(s string) (int, int, bool) {
	m := hhmmRegex.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, false
	}
	h, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	return h, mi, true
}

// ErrBadHHMM is a HH:MM string that does not pass the 0401 CHECK pattern (caller fault → 400).
// Surfaced so the handler can map it without writing a Postgres CHECK violation → 500.
var ErrBadHHMM = errors.New("messaging: time must be in HH:MM 24-hour form")

// ErrBadTimezone is a timezone name time.LoadLocation cannot resolve (caller fault → 400). Caught
// at the service boundary so a bad value never reaches ActiveAt as a Go panic.
var ErrBadTimezone = errors.New("messaging: unknown timezone")

// ActiveAt reports whether `now` falls inside the user's quiet-hours window. Supported shapes:
//
//   - qh.Enabled == false: never active (the shipped default, no quiet window).
//   - same-day windows (start <= end, e.g. 09:00 to 17:00): active iff start <= now < end, in qh.time.
//   - overnight windows (start > end, e.g. 18:00 to 06:00): active iff now >= start OR now < end,
//     in qh.time, so a 22:00 and a 03:00 are both inside but the 12:00 is outside.
//
// The predicate falls back to UTC on a bad timezone (the handler validates first; this is the
// defence-in-depth path that keeps a bad row from permanently pinching a user out of notifications).
//
// A timezone the system genuinely does not know (e.g. "Mars/Olympus") falls back to UTC rather
// than refusing, because the row is on disk and the user expects a window to work; a future TODO'd
// audit pass will surface the fall-back, deliberately deferred so the v1 ships quiet-by-default.
func (qh QuietHours) ActiveAt(now time.Time) bool {
	if !qh.Enabled {
		return false
	}
	startH, startM, ok := parseHHMM(qh.StartLocal)
	if !ok {
		return false
	}
	endH, endM, ok := parseHHMM(qh.EndLocal)
	if !ok {
		return false
	}
	loc, err := time.LoadLocation(qh.Timezone)
	if err != nil || loc == nil {
		loc = time.UTC // defence-in-depth: a bad timezone falls back to UTC, never a panic.
	}
	local := now.In(loc)
	startMin := startH*60 + startM
	endMin := endH*60 + endM
	nowMin := local.Hour()*60 + local.Minute()
	if startMin <= endMin {
		// Same-day window (e.g. 09:00 to 17:00). Inclusive of start, exclusive of end.
		return nowMin >= startMin && nowMin < endMin
	}
	// Overnight window (e.g. 18:00 to 06:00). Active if we are AT or AFTER the start, OR if we
	// are STRICTLY BEFORE the end; the start inclusive boundary is what the brief's regression
	// tests pin (22:00 is active, 03:00 is active, 12:00 is not).
	return nowMin >= startMin || nowMin < endMin
}

// containsUserID reports whether `s` is one of the bypass user ids. The list is small (DMs only),
// so a linear scan is cheaper than a map for the typical length; nil-safe for an absent list.
func containsUserID(haystack []string, needle string) bool {
	if needle == "" {
		return false
	}
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// ShouldDeliverPush is the desktop-push gate. Pure (no DB, no clock), so the whole rule matrix
// is provable in service_test.go and lives beside the existing shouldNotify predicate the SSE /
// desktop-push path consumes.
//
// The decision walks the brief's clauses in order; each clause that returns short-circuits the
// rest. The forced-ack / escalation break-through (clause a) runs FIRST so a critical alert is
// never silenced by a stale toggle, a quiet window, or a bypass list the user did not name. The
// per-channel mute (clause b) runs SECOND so the existing shouldNotify decision (0069) is
// honoured without being re-derived. The per-category push toggle (clause c) is the user-set
// gate. The DM bypass (clause d) is the carve-out for "DMs from these people still ring".
func ShouldDeliverPush(now time.Time, pref NotificationPrefs, channelTier string, isForcedAck bool, eventCategory string, classPref PersonEventPref, qh QuietHours, dmSenderID string) bool {
	// (a) forced-ack OR escalation tier OR firehose-with-escalation-override: breaks through every
	// quiet gate. The three triggers are joined with OR because they all mean the same thing for
	// the recipient ("this is the kind of message that MUST reach you"), and joining them means a
	// future fourth trigger (e.g. an "approved" echo) needs only one line to add.
	if isForcedAck || channelTier == TierEscalation || (channelTier == TierFirehose && pref.EscalationOverride) {
		return true
	}
	// (b) the existing per-channel notification decision. A muted channel / a desktop-off toggle
	// suppresses BEFORE the quiet window even gets a vote, exactly like the shipped path.
	if !shouldNotify(pref, channelTier) {
		return false
	}
	// (c) the per-category push toggle. eventCategory == "" means "this delivery has no category"
	// (e.g. a non-event message), and the toggle does not apply (default true at the call site).
	if eventCategory != "" && !classPref.Push {
		return false
	}
	// (d) quiet hours: active iff `now` is in the user's window AND the message is not a DM from a
	// bypass user. The DM carve-out is the brief's "DMs from these people still ring as drawn on
	// the board"; a non-DM message (dmSenderID == "") inside the window is suppressed entirely.
	if qh.ActiveAt(now) {
		if dmSenderID != "" && containsUserID(qh.BypassUserIDs, dmSenderID) {
			return true
		}
		return false
	}
	return true
}

// Tier constants live in model.go beside the channel-tier column default and the
// channel-tier truth (0069). The predicate above references them; duplicating them here would
// mean two definitions that can disagree.
