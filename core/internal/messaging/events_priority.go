package messaging

import "time"

// - DIB-217: MSG-EVENTS, the high-priority tier, forced-ack bubbles, and the
//    LAST-WRITE-WINS state-conflict ruling -
//
// DIB-275 built the forced-ack schema ONCE (requires_ack + msg_acks, migration 0071). It has TWO
// consumers and this file is the second one:
//
//	consumer 1, the KIOSK screen-takeover (<hos-kiosk-takeover>): polls UnackedRequiresAck and
//	             blocks the shared tablet until someone taps "I got it".
//	consumer 2, the EVENTS bubble (this file, rendered by <hos-messaging-ack-bubble>): a persistent
//	             in-hub bubble on the individual's screen that will not dismiss until THEY ack.
//
// Neither forks a flag or a table: both read requires_ack and write msg_acks. The ONLY thing DIB-217
// adds to that schema is the state-conflict machinery below (0138), because a bubble that persists is
// a bubble that can go stale, which the kiosk's short-lived takeover never had to worry about.
//
// - THE RULING: state conflicts are LAST-WRITE-WINS -
//
// A forced-ack event asserts a FACT about a record ("Order #SO-1042 is blocked, credit hold"). The
// record can change again before anyone acks (the hold clears, the order releases). The un-acked
// bubble is then a STALE FACT PRESENTED AS CURRENT, the single worst thing a forced-ack surface can
// do, because the whole point of forcing an ack is that the user is asserting "I have read and
// accepted this as true right now".
//
//	RULING: the newest event about a record wins. When a newer event arrives for the same state_key,
//	every OLDER un-acked forced-ack message for that key is AUTO-RESOLVED (superseded) and stops
//	blocking. A user NEVER acks a stale fact presented as current.
//
// Consequences, all enforced below and named in tests:
//   - Supersession RESOLVES, it does not ACK. msg_acks stays the honest record of who actually saw
//     something; a superseded message has no ack row, and that is the truthful answer to "did anyone
//     read it?", no.
//   - An ALREADY-ACKED message is never superseded: there is nothing left to resolve, and rewriting
//     it would corrupt ack history.
//   - A message never supersedes itself (redelivery of the same event is the existing event_key
//     idempotency no-op).
//   - Supersession is keyed on the RECORD (state_key), not the topic, that is what makes
//     "order released" resolve an "order blocked" bubble for the SAME order.

// Message priorities (mirror the msg_messages.priority CHECK, 0138).
const (
	PriorityNormal = "NORMAL"
	PriorityHigh   = "HIGH"
)

// escalationSlug is the un-mutable ESCALATION-tier channel (0137). Every HIGH-priority event is
// MIRRORED here in addition to its home channel, this is the "feeds from the priority tier of
// DIB-217" wiring for DIB-269. The mirror is a separate message with its own event key so the two
// posts dedup independently and a failure to mirror can never lose the home post.
const escalationSlug = "escalations"

// escalationMirrorKey derives the mirror post's event key from the source event key, keeping the
// mirror idempotent per redelivery without colliding with the home channel's row.
func escalationMirrorKey(eventKey string) string { return eventKey + ":esc" }

// EventOpts carries the per-insert flags an EVENT message can be written with. It exists so adding a
// flag (0138 added three) does not keep widening InsertEventMessage's parameter list, the shipped
// 5-arg PostEventToChannelSlug seam stays byte-compatible for its existing callers (portal bridge,
// task/quote notifiers wired in main.go) and only the new priority path passes opts.
type EventOpts struct {
	// RequiresAck flags the forced-ack post the kiosk AND the bubble block on (0071's shared flag).
	RequiresAck bool
	// Priority is the automation's classification (NORMAL | HIGH). HIGH implies RequiresAck.
	Priority string
	// StateKey names the RECORD this event asserts a fact about ("order:<id>"). Empty for an event that
	// is not a state assertion, such an event never supersedes and is never superseded.
	StateKey string
	// Mirror marks this post as a COPY of a fact that was already asserted elsewhere (the #escalations
	// mirror of a HIGH-priority event), rather than a NEW assertion about the record.
	//
	// This flag exists because of a bug the Chromium walkthrough caught and no unit test had: the
	// mirror carried the same state_key as its source and was written a moment LATER, so the
	// LAST-WRITE-WINS sweep treated it as a newer fact and auto-resolved the original, the escalation
	// copy silently dismissed the very alert it existed to amplify. A forced-ack bubble vanished
	// without anyone acknowledging it, which is the exact failure requires_ack exists to prevent.
	//
	// The rule this encodes: a COPY never supersedes its ORIGINAL. The state key is still stored (it is
	// true, and useful for querying an escalation copy by record), but the sweep is skipped.
	Mirror bool
}

// normalizePriority defaults an empty priority to NORMAL and clamps an unknown value to NORMAL rather
// than letting it reach the CHECK. A HIGH priority always implies RequiresAck: the two cannot
// disagree, because "high priority" is DEFINED as "forces an ack" in this system.
func (o EventOpts) normalize() EventOpts {
	if o.Priority != PriorityHigh {
		o.Priority = PriorityNormal
	}
	if o.Priority == PriorityHigh {
		o.RequiresAck = true
	}
	return o
}

// eventOptsFor builds the insert options for one routed FR-3 event. It is the SINGLE place topic →
// (priority, forced-ack) is decided, so the priority tier cannot drift between the ack flag, the
// escalation mirror, and the bubble.
//
// requiresAck(topic) is the 0071 allowlist (credit hold / order blocked) and is deliberately NOT
// widened here: DIB-217 consumes that allowlist rather than forking a second, competing definition of
// "high priority". Widening the tier is one edit, in one map, in consumer.go.
func eventOptsFor(topic string, p eventPayload) EventOpts {
	o := EventOpts{StateKey: stateKeyFor(p)}
	if requiresAck(topic) {
		o.Priority = PriorityHigh
	}
	return o.normalize()
}

// stateKeyFor derives the RECORD identity an event is a fact about. Order identity wins over customer
// identity because an order-scoped fact is the narrower claim: a credit hold posted with both an order
// and a customer is a fact about that ORDER's releasability. Falling back to the customer lets a
// pure-customer fact (a credit hold with no order) still supersede its own older bubble.
//
// The internal order id is preferred over the order NUMBER so two events for the same order can never
// fail to collide because one carried the id and the other the human number. When only the number is
// known it is used, a consistent key is what matters, not which form it takes.
//
// An empty key means "not a state assertion": such an event supersedes nothing and is never
// superseded. That is the safe default, a spurious supersede would silently dismiss a live alert.
func stateKeyFor(p eventPayload) string {
	switch {
	case p.OrderID != "":
		return "order:" + p.OrderID
	case p.OrderNo != "":
		return "order#" + p.OrderNo
	case p.CustomerID != "":
		return "customer:" + p.CustomerID
	default:
		return ""
	}
}

// - Ack arithmetic (DIB-275) -

// AckProgress is the per-message ack arithmetic: how many people MUST ack (the channel's members at
// the time of the read), how many HAVE, and whether that is everyone. Acked counts DISTINCT users -
// the msg_acks (message_id, user_id) primary key makes a re-ack an UPDATE, never a second row, so
// re-tapping "I got it" can never inflate the count.
type AckProgress struct {
	MessageID string `json:"message_id"`
	Required  int    `json:"required"`
	Acked     int    `json:"acked"`
	Remaining int    `json:"remaining"`
	Complete  bool   `json:"complete"`
	Acks      []Ack  `json:"acks"`
}

// computeAckProgress is the arithmetic, extracted pure so the edge cases are pinned by unit test
// rather than inferred from SQL:
//
//   - Remaining is FLOORED AT ZERO. Acked can legitimately exceed Required, a member acks, then
//     leaves the channel (or their branch grant is revoked and the derived membership prunes), which
//     drops Required while their ack row remains. A negative "remaining" rendered in the UI as
//     "-1 left" is the bug this floor prevents.
//   - Complete requires Required > 0. A message in a channel with no members is not "fully acked by
//     everyone"; it is un-acknowledgeable, and reporting it complete would be a false all-clear.
func computeAckProgress(messageID string, required, acked int, acks []Ack) AckProgress {
	if required < 0 {
		required = 0
	}
	if acked < 0 {
		acked = 0
	}
	remaining := required - acked
	if remaining < 0 {
		remaining = 0
	}
	if acks == nil {
		acks = []Ack{}
	}
	return AckProgress{
		MessageID: messageID,
		Required:  required,
		Acked:     acked,
		Remaining: remaining,
		Complete:  required > 0 && acked >= required,
		Acks:      acks,
	}
}

// Supersession is the record of one auto-resolve: which message was resolved, by which newer one, and
// when. Returned by the supersede sweep so the caller can log/emit it and a test can assert on it.
type Supersession struct {
	MessageID    string    `json:"message_id"`
	SupersededBy string    `json:"superseded_by"`
	StateKey     string    `json:"state_key"`
	At           time.Time `json:"at"`
}
