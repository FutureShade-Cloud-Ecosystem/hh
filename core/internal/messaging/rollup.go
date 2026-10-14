package messaging

import (
	"context"
	"fmt"
	"time"
)

// - DIB-449: THE ROLLUP -
//
// A ROUTINE event does not get a message. It gets a LINE in the day's message.
//
//	#payments-trenton
//	  System  Payments today: 14 · $12,340.00                        [14 replies]
//	    - Payment received, $1,240.00 on SO-1041 (CARD).
//	    - Payment received, $3,880.50 on SO-1042 (CASH).
//	    - …
//
// The channel surface is ONE line per day. Every individual payment is still there, in full, as a
// THREAD REPLY under that day's line, nothing is removed, nothing is summarised away, and the drill
// -down is the threads MSG-01 already shipped. History stays browsable (DIB-449 non-goal 5).
//
// - Why this is the supersede machinery, not a second mechanism -
//
// Wave 5 ruled that event state is LAST-WRITE-WINS: the newest fact about a record is the truth, and
// the older presentation of it stops standing (0138). A day's payment total is exactly that shape -
// "Payments today: 13" is not history to be preserved beside "Payments today: 14", it is a stale fact
// presented as current, which is the precise failure DIB-217 named. So the rollup does not append and
// then hide; it UPDATES THE ONE MESSAGE IN PLACE, and the day's identity is enforced by the SHIPPED
// (channel_id, event_key) unique index rather than by application discipline.
//
//	A DAY IS ONE MESSAGE. Pinned by TestRollup_DayIsOneMessage_NotAnAppendingLog.
//
// The root is created through postEventToChannelID, the single write path every event in this module
// goes through, so the supersede sweep and the realtime fan-out cannot be forgotten here and honoured
// elsewhere. Updates re-enter the same fan-out.
//
// - Why the body is recomputed, not incremented -
//
// The outbox is at-least-once. An incrementing counter turns one redelivered payment into two, and a
// money total that is quietly wrong is worse than a noisy one that is right. The body is therefore a
// FOLD over the durable replies: count them, sum them, render. Redelivery re-derives the same numbers.
// The reply's own event_key makes the redelivery a no-op before that even matters, belt and braces,
// because this is the surface an AR clerk reconciles against.

// rollupZone is the house zone (mirrors messagingprovider.DefaultZone, Dibbits is Ontario). The
// rollup day MUST be local: a UTC day boundary cuts the Ontario working day at 8pm EDT and files the
// evening's receipts under tomorrow, on the one surface whose entire job is "what came in today".
// Declared here rather than imported so the messaging module keeps its modular-monolith boundary.
const rollupZone = "America/Toronto"

// rollupLocation resolves the house zone, falling back to UTC if the tzdata is missing from the image
// rather than failing the post. A rollup filed against the wrong midnight is a cosmetic defect; a
// payment that never landed anywhere is a real one.
func rollupLocation() *time.Location {
	loc, err := time.LoadLocation(rollupZone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// rollupDay renders the local calendar day a routine event belongs to (YYYY-MM-DD).
func rollupDay(at time.Time) string {
	return at.In(rollupLocation()).Format("2006-01-02")
}

// rollupKey is the day's message identity within its channel. The shipped uq_msg_messages_event_key
// (0050) makes it unique per channel, which is what enforces "a day is ONE message" in the SCHEMA.
func rollupKey(class, day string) string { return "rollup:" + class + ":" + day }

// rollupStateKey names the record the rollup asserts a fact about, the (channel, day) pair, so the
// day's line participates in the same LAST-WRITE-WINS keyspace as every other event in the module.
func rollupStateKey(slug, class, day string) string {
	return "rollup:" + slug + ":" + class + ":" + day
}

// rollupTopicPayment is the ref topic stamped on a payments rollup root. It is what the hub reads to
// label the drill-down ("14 payments") and what a future rollup class would vary.
const rollupTopicPayment = "msg.rollup.payment.received"

// paymentsSlug is the routine payments channel for a branch. It follows the house convention the 0076
// branch team channels set (#sales-trenton) via the same locations.code lookup as the #yard-<branch>
// map. An empty branch resolves to the GLOBAL #payments channel: the raw payment.received payload
// carries no branch (gl/module.go), and routing a branchless payment back to #finance would rebuild the
// exact noise this ticket removes.
func paymentsSlug(yardSlug string) string {
	if yardSlug == "" {
		return "payments"
	}
	// yard-trenton → payments-trenton. Reusing the resolved yard slug rather than re-deriving the city
	// keeps ONE branch→channel-name mapping in the codebase; a second one would drift.
	if len(yardSlug) > len("yard-") && yardSlug[:len("yard-")] == "yard-" {
		return "payments-" + yardSlug[len("yard-"):]
	}
	return "payments"
}

// rollupStore is the DIB-449 persistence seam (discovered by type assertion off the base store, like
// priorityStore and classStore, so the shipped `store` interface stays unchanged).
type rollupStore interface {
	// FindEventMessage returns an existing EVENT message by (channel, event_key). errNotFound when absent.
	FindEventMessage(ctx context.Context, channelID, eventKey string) (Message, error)
	// InsertEventReply writes a system EVENT message as a REPLY under rootID, idempotent on event_key.
	InsertEventReply(ctx context.Context, channelID, rootID, eventKey, body string, ref EventRef) (Message, bool, error)
	// RollupAggregate folds the live replies under a root into (count, total cents).
	RollupAggregate(ctx context.Context, rootID string) (int, int64, error)
	// UpdateEventBody rewrites an EVENT message's body + ref IN PLACE (the last-write-wins update).
	UpdateEventBody(ctx context.Context, messageID, body string, ref EventRef) (Message, error)
}

func (s *Service) rollups() (rollupStore, error) {
	if rs, ok := s.repo.(rollupStore); ok {
		return rs, nil
	}
	return nil, errNoDB
}

// RollupResult reports what one routine event did, so the consumer can log it and a test can assert on
// it without reading the DB. Inserted is false for a redelivery (the reply already existed).
type RollupResult struct {
	RootID   string
	ReplyID  string
	Inserted bool
	Count    int
	Cents    int64
	Body     string
}

// PostRoutineEvent is the DIB-449 producer seam and the INTERNAL post path for routine events, the
// W5 PostPriorityEvent precedent, deliberately: the behaviour is reached from inside the service, not
// by registering a second dispatcher topic against a handler map that is frozen at boot.
//
// Three steps, in this order, each individually idempotent:
//
//  1. ENSURE the day's rollup root exists in the channel (one row per (channel, day), the schema's job).
//  2. APPEND the individual event as a thread REPLY under it (idempotent on its own event key).
//  3. RECOMPUTE the root's body from its replies and UPDATE IT IN PLACE, then fan out.
//
// Step 3 runs even on a redelivery whose reply was a no-op: it is a fold over durable rows, so re-running
// it is free and it self-heals a body that a crash between steps 2 and 3 would have left one short.
func (s *Service) PostRoutineEvent(ctx context.Context, slug, eventKey, body string, ref EventRef, amountCents int64, at time.Time) (RollupResult, error) {
	rs, err := s.rollups()
	if err != nil {
		return RollupResult{}, err
	}
	channelID, err := s.repo.ChannelIDBySlug(ctx, slug)
	if err != nil {
		return RollupResult{}, err
	}
	day, err := s.rollupDayFor(ctx, at)
	if err != nil {
		return RollupResult{}, err
	}

	// - 1. the day's ONE message -
	root, err := s.ensureRollupRoot(ctx, rs, channelID, slug, day)
	if err != nil {
		return RollupResult{}, err
	}

	// - 2. the individual payment, as a thread reply -
	// The reply carries the money on its ref so the recompute is a fold over structured facts rather
	// than a parse of the rendered sentence. amount_cents is omitempty, so every message this module
	// wrote before DIB-449 still serializes byte-identically. The caller's own topic/entity/deep-link
	// stay on the reply, drilling into a day must reach the SAME payment record the per-event message
	// used to link to.
	ref.AmountCents = amountCents
	reply, inserted, err := rs.InsertEventReply(ctx, channelID, root.ID, eventKey, body, ref)
	if err != nil {
		return RollupResult{}, fmt.Errorf("rollup reply: %w", err)
	}

	// - 3. recompute + update in place -
	count, cents, err := rs.RollupAggregate(ctx, root.ID)
	if err != nil {
		return RollupResult{}, fmt.Errorf("rollup aggregate: %w", err)
	}
	newBody := renderRollupBody(count, cents, day)
	updated, err := rs.UpdateEventBody(ctx, root.ID, newBody, rollupRootRef(slug, day, count, cents))
	if err != nil {
		return RollupResult{}, fmt.Errorf("rollup update: %w", err)
	}
	// Re-publish the ROOT so an open hub repaints the day's line without waiting for the poll. The
	// REPLY is deliberately not fanned out on its own: a routine event must never produce a
	// notification-shaped realtime event, which is the entire point of the class.
	s.fanout(channelID, updated)

	return RollupResult{
		RootID: root.ID, ReplyID: reply.ID, Inserted: inserted,
		Count: count, Cents: cents, Body: newBody,
	}, nil
}

// ensureRollupRoot returns the day's rollup message, creating it on first use. The create goes through
// postEventToChannelID, the module's single event-write path, so the supersede sweep and the realtime
// fan-out apply to the rollup exactly as they do to every other event.
//
// The lost-race path is explicit rather than assumed: two payments landing in the same tick both try to
// create the day, one loses the unique index and gets inserted=false, and MUST then read the winner's
// row instead of proceeding with a zero-valued Message (whose empty ID would attach the reply to no
// thread at all, scattering payments back into the channel surface, the bug this ticket exists to fix).
func (s *Service) ensureRollupRoot(ctx context.Context, rs rollupStore, channelID, slug, day string) (Message, error) {
	key := rollupKey(ClassRoutine, day)
	if m, err := rs.FindEventMessage(ctx, channelID, key); err == nil && m.ID != "" {
		return m, nil
	}
	seed := renderRollupBody(0, 0, day)
	m, inserted, err := s.postEventToChannelID(ctx, channelID, key, seed,
		rollupRootRef(slug, day, 0, 0),
		EventOpts{Priority: PriorityNormal, StateKey: rollupStateKey(slug, ClassRoutine, day)})
	if err != nil {
		return Message{}, fmt.Errorf("rollup root: %w", err)
	}
	if inserted && m.ID != "" {
		return m, nil
	}
	// Lost the race (or a redelivery): the winner's row is the day.
	existing, err := rs.FindEventMessage(ctx, channelID, key)
	if err != nil {
		return Message{}, fmt.Errorf("rollup root reread: %w", err)
	}
	return existing, nil
}

// rollupRootRef is the structured ref on a day's rollup line. Count/AmountCents are the machine-readable
// twin of the rendered body, so a client (or a test) never has to parse the sentence.
func rollupRootRef(slug, day string, count int, cents int64) EventRef {
	return EventRef{
		Topic:       rollupTopicPayment,
		Entity:      "rollup:" + slug + ":" + day,
		Summary:     renderRollupSummary(count, cents),
		Count:       count,
		AmountCents: cents,
		RollupDay:   day,
	}
}

// renderRollupBody is the day's line. It is deliberately a COMPLETE SENTENCE OF FACT, the day, the
// count and the money, because the whole justification for collapsing fourteen messages into one is
// that the one says everything the fourteen said that a person actually needed at a glance.
//
// - Why the STORED line names the date instead of saying "today" -
//
// It said "Payments today" first, and the DB-backed test caught what that means: a message body is
// written once and only rewritten when another payment lands on that same day. So yesterday's line
// would go on saying "Payments today: 14" forever, a stale fact presented as current, on a surface
// whose only job is to be scanned quickly and trusted. That is the precise failure the Wave-5
// supersede ruling exists to prevent, and it would have shipped inside the fix for it.
//
// The row therefore stores what is permanently TRUE ("Payments 2026-08-11: 14 · $12,340.00"), and the
// hub humanises it to "Payments today" from ref.rollup_day at paint time, when "today" can be
// evaluated honestly (app/src/_messaging-rollup.ts). Immutable fact in the row, mutable presentation
// in the view.
func renderRollupBody(count int, cents int64, day string) string {
	if count == 0 {
		return "Payments " + day + ": none yet."
	}
	return fmt.Sprintf("Payments %s: %d · %s", day, count, rollupMoney(cents))
}

// rollupMoney renders the day's total. It differs from dollars() in ONE way that matters here:
// dollars(0) returns "" so a per-event body degrades gracefully when a producer omitted the amount,
// but a ROLLUP with a count and a blank total ("Payments today: 3 · ") reads as a broken line rather
// than a fact. A rollup always states a number.
func rollupMoney(cents int64) string {
	if cents == 0 {
		return "$0.00"
	}
	return dollars(cents)
}

// renderRollupSummary is the short ref summary (deep-link/preview surfaces read this).
func renderRollupSummary(count int, cents int64) string {
	if count == 0 {
		return "No payments yet today."
	}
	unit := "payments"
	if count == 1 {
		unit = "payment"
	}
	return fmt.Sprintf("%d %s · %s", count, unit, rollupMoney(cents))
}
