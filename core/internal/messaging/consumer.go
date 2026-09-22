package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

// - FR-3: system events as first-class messages -
//
// The messaging consumer maps the transactional-outbox events for the TOP 6 P1 topics to system
// EVENT messages in the right channel, reusing the EXISTING notify.Dispatcher (no new broker).
//
// Topic ownership vs observation: the dispatcher enforces one OWNER (settling handler) per topic and
// PANICS on a duplicate. The raw domain topics (payment.received, po.received, …) are already owned
// for settlement by the GL/inventory posting consumers, so messaging does NOT contend for ownership.
// Instead it does two things:
//   - OWNS its own disjoint msg.event.<topic> family (an explicit producer that emits one of these
//     is settled here), kept for forward-compat and for events with no raw producer yet; and
//   - OBSERVES the raw domain topics (RegisterObserver) so FR-3 fires on events the ERP ALREADY
//     emits, posting a system EVENT message as a non-owning side-effect that can never fail, retry,
//     or dead-letter the financial row.
// This is the sanctioned "thin relay" (0051 comment) done once via the dispatcher's observer seam -
// reusing the existing outbox/dispatcher, no new broker, no per-emit-site producer edits.
//
// The six P1 topics (PRD §7 / §9 Q2):
//   msg.event.order.released     → #yard-<branch>   "Order #SO-… released, pick list ready"
//   msg.event.order.blocked      → #yard-<branch>   "Order #SO-… blocked, <reason>"
//   msg.event.stock.received     → #yard-<branch>   "Stock received, <sku> ×<qty>"
//   msg.event.delivery.scheduled → #yard-<branch>   "Delivery scheduled, run #… on <date>"
//   msg.event.payment.received   → #finance          "Payment received, $… on <order>"  (COD/awaiting)
//   msg.event.credit.hold        → #finance          "Credit hold, <customer> (NSF/limit)"  (nsf/credit-hold)

// The messaging topic family. Exported so producers/tests reference the exact strings.
const (
	TopicOrderReleased     = "msg.event.order.released"
	TopicOrderBlocked      = "msg.event.order.blocked"
	TopicStockReceived     = "msg.event.stock.received"
	TopicDeliveryScheduled = "msg.event.delivery.scheduled"
	TopicPaymentReceived   = "msg.event.payment.received"
	TopicCreditHold        = "msg.event.credit.hold"
)

// MessagingTopics is the frozen P1 set this consumer owns exclusively (disjoint from gl.PostedTopics
// and inventory.InventoryTopics, so no dispatcher collision at boot). A producer MAY emit these
// directly (an explicit msg.event.* row), but P1 does not depend on that: the raw domain topics
// below are OBSERVED so FR-3 fires on events the ERP already emits, with no producer changes.
var MessagingTopics = []string{
	TopicOrderReleased,
	TopicOrderBlocked,
	TopicStockReceived,
	TopicDeliveryScheduled,
	TopicPaymentReceived,
	TopicCreditHold,
}

// - Raw domain topics OBSERVED for FR-3 -
//
// The BLOCKING gap the reviews caught: no producer emits msg.event.*, so FR-3 never fired in
// production. The fix is the dispatcher's observer seam, messaging OBSERVES the raw domain topics
// the ERP already emits (owned for SETTLEMENT by GL/inventory) and posts a system EVENT message as a
// non-owning side-effect. This is the "thin relay" the 0051 comment specified, implemented once here
// rather than at every emit site.
//
// The raw→P1 map (only topics with a real producer in this repo are wired; the others stay available
// via the explicit msg.event.* family for when their producers land):
//
//	payment.received → #finance   (COD/AR receipt)            → TopicPaymentReceived
//	payment.bounced  → #finance   (NSF → credit hold)         → TopicCreditHold
//	po.received      → #yard-<br>  (stock landed at the yard)  → TopicStockReceived
const (
	RawPaymentReceived = "payment.received"
	RawPaymentBounced  = "payment.bounced"
	RawPOReceived      = "po.received"
)

// ObservedTopics are the raw domain topics messaging attaches to as a secondary observer. They are
// OWNED (settled) by GL/inventory; messaging never contends for ownership, so there is no dispatcher
// collision. Kept as a var so the wiring test can assert the exact set.
var ObservedTopics = []string{
	RawPaymentReceived,
	RawPaymentBounced,
	RawPOReceived,
}

// rawPayload is the union of fields across the observed raw producers (payments/till/inventory). Each
// producer fills its own subset; normalizeRaw maps it onto the internal eventPayload + P1 topic.
type rawPayload struct {
	PaymentID   string `json:"payment_id"`
	CustomerID  string `json:"customer_id"`
	BranchID    string `json:"branch_id"`
	AmountCents int64  `json:"amount_cents"`
	Method      string `json:"method"`
	FeeCents    int64  `json:"fee_cents"`
	POID        string `json:"po_id"`
	ReceiptID   string `json:"receipt_id"`
	ValueCents  int64  `json:"value_cents"`
}

// normalizeRaw translates a raw domain event into the internal (P1 topic, eventPayload) the existing
// handle() pipeline consumes. It returns ok=false for an unmapped topic. The event key is the outbox
// row id (raw producers carry no event_id), so redelivery of the same raw row stays idempotent.
func normalizeRaw(rawTopic string, raw rawPayload, outboxID string) (topic string, p eventPayload, ok bool) {
	p = eventPayload{EventID: outboxID, BranchID: raw.BranchID, CustomerID: raw.CustomerID}
	switch rawTopic {
	case RawPaymentReceived:
		p.AmountText = dollars(raw.AmountCents)
		p.AmountCents = raw.AmountCents // DIB-449: the rollup totals the day in cents, not in prose
		p.PaymentID = raw.PaymentID     // DIB-462: the key the rule matchers resolve their facts from
		if raw.Method != "" {
			p.Reason = raw.Method // e.g. "CASH"/"CARD", surfaced as context on the finance post
		}
		return TopicPaymentReceived, p, true
	case RawPaymentBounced:
		p.AmountText = dollars(raw.AmountCents)
		p.Reason = "NSF, payment bounced"
		return TopicCreditHold, p, true
	case RawPOReceived:
		if raw.ReceiptID != "" {
			p.SKU = "receipt " + raw.ReceiptID // no per-line SKU on the header event; name the receipt
		}
		return TopicStockReceived, p, true
	default:
		return "", eventPayload{}, false
	}
}

// dollars renders integer cents as a $#,###.## string for a human message body. Empty for 0 (the
// producer omitted the amount) so the body degrades gracefully.
func dollars(cents int64) string {
	if cents == 0 {
		return ""
	}
	neg := cents < 0
	// Take the magnitude in uint64 so math.MinInt64 does not overflow: -math.MinInt64 is not
	// representable as int64 (it wraps back to a negative), which produced a corrupt "-$-..." string.
	// uint64(-x) via two's complement yields the correct magnitude for every negative int64.
	var mag uint64
	if neg {
		mag = uint64(-(cents + 1)) + 1 // == uint64(-cents) but safe at math.MinInt64
	} else {
		mag = uint64(cents)
	}
	d := mag / 100
	c := mag % 100
	// Group the dollar part with thousands separators.
	ds := fmt.Sprintf("%d", d)
	var grouped []byte
	for i, ch := range []byte(ds) {
		if i > 0 && (len(ds)-i)%3 == 0 {
			grouped = append(grouped, ',')
		}
		grouped = append(grouped, ch)
	}
	sign := ""
	if neg {
		sign = "-"
	}
	return fmt.Sprintf("%s$%s.%02d", sign, string(grouped), c)
}

// financeSlug is where the money events land (a GLOBAL team channel, seeded in 0052).
const financeSlug = "finance"

// - Feature 1 (0071): forced-ack allowlist -
// ackRequiredTopics is the SMALL allowlist of genuinely high-priority EVENT topics whose channel post
// is force-acknowledged: the message is written with requires_ack=TRUE so the kiosk screen-takeover
// (<hos-kiosk-takeover>) blocks until a staffer taps "I got it". Deliberately NOT blanket-set, only a
// credit hold (money at risk / NSF) and a blocked order (a release halted, someone is waiting) qualify
// today; every other top-6 topic stays an ordinary, non-blocking post. Widen this set ONLY with an
// explicit priority decision.
//
// COORDINATION (DIB-217): requires_ack / msg_acks is the SHARED forced-ack schema (see the 0071
// migration header). This allowlist is the P1 PRODUCER of it; the P1 lane should consume this rather
// than fork its own flag.
var ackRequiredTopics = map[string]bool{
	TopicCreditHold:   true,
	TopicOrderBlocked: true,
}

// requiresAck reports whether an EVENT for topic must be force-acknowledged (the Feature 1 allowlist).
func requiresAck(topic string) bool { return ackRequiredTopics[topic] }

// eventPayload is the common shape the top-6 producers put on their msg.event.* rows. Every field is
// optional; the consumer degrades gracefully (a missing branch_id routes money events to #finance,
// a yard event with no branch is dropped with a warning rather than mis-routed). event_id makes the
// message idempotent across outbox redelivery even before the dispatcher's own de-dup.
type eventPayload struct {
	EventID    string `json:"event_id"`
	BranchID   string `json:"branch_id"`
	OrderNo    string `json:"order_no"`
	OrderID    string `json:"order_id"`
	SKU        string `json:"sku"`
	Qty        string `json:"qty"`
	CustomerID string `json:"customer_id"`
	Customer   string `json:"customer"`
	AmountText string `json:"amount"`
	// AmountCents (DIB-449) is the same money as a NUMBER. The rollup totals a day's payments, and a
	// total folded out of rendered strings is a total waiting to be wrong. Optional: eventCents falls
	// back to parsing AmountText for the shipped producers that only send the rendered form.
	AmountCents int64 `json:"amount_cents"`
	// PaymentID (DIB-462) is the LEDGER KEY of a payment event. The rule matchers resolve branch,
	// order and ownership from the payments row itself, never from this payload, so this id is the
	// only thing the event has to carry for them to work. Optional: an event without one still rolls
	// up exactly as DIB-449 shipped it, and simply matches no rule.
	PaymentID string `json:"payment_id"`
	Reason    string `json:"reason"`
	Date      string `json:"date"`
	RunNo     string `json:"run_no"`
	DeepLink  string `json:"deep_link"`
}

// Consumer maps msg.event.* outbox events to channel posts. It holds the service (for the idempotent
// event-message write + channel routing) and a logger.
type Consumer struct {
	svc *Service
	log *slog.Logger
}

// NewConsumer builds the FR-3 consumer.
func NewConsumer(svc *Service, log *slog.Logger) *Consumer {
	if log == nil {
		log = slog.Default()
	}
	return &Consumer{svc: svc, log: log}
}

// RegisterHandlers wires FR-3 onto the dispatcher. Called once at boot from main.go, AFTER the
// gl/inventory registrations. Two kinds of wiring:
//   - Register: the six msg.event.* topics this module OWNS (disjoint family → no panic). A producer
//     that emits an explicit msg.event.* row is settled here.
//   - RegisterObserver: the raw domain topics (payment.received, payment.bounced, po.received) the
//     ERP already emits. Messaging OBSERVES them (GL/inventory remain the owners) and posts a system
//     EVENT message as a non-owning side-effect, this is what makes FR-3 actually fire in
//     production without touching any producer.
func (c *Consumer) RegisterHandlers(d *notify.Dispatcher) {
	for _, t := range MessagingTopics {
		d.Register(t, c.handle)
	}
	for _, t := range ObservedTopics {
		d.RegisterObserver(t, c.handleRaw)
	}
}

// handleRaw is the observer entry point for a raw domain event. It normalizes the raw payload onto
// the internal (P1 topic, eventPayload) and reuses the same routing/idempotency pipeline as an
// explicit msg.event.* row. An unmapped topic or malformed payload is a no-op (nil), an observer
// never fails the owner's row.
func (c *Consumer) handleRaw(ctx context.Context, e notify.Event) error {
	var raw rawPayload
	if err := json.Unmarshal(e.Payload, &raw); err != nil {
		c.log.Warn("messaging: undecodable raw event, skipping", "topic", e.Topic, "err", err)
		return nil
	}
	topic, p, ok := normalizeRaw(e.Topic, raw, e.EventID)
	if !ok {
		c.log.Warn("messaging: no mapping for observed topic, skipping", "topic", e.Topic)
		return nil
	}
	return c.route(ctx, topic, p)
}

// handle routes one outbox event to a channel post. Returning an error leaves the outbox row PENDING
// for retry (the dispatcher's retry/dead-letter policy applies), so a transient DB blip re-delivers.
// A permanently-unroutable event (e.g. a yard event with no branch) is treated as delivered (nil) so
// it does not loop forever, it is logged instead.
func (c *Consumer) handle(ctx context.Context, e notify.Event) error {
	var p eventPayload
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		c.log.Warn("messaging: undecodable event payload, dropping", "topic", e.Topic, "err", err)
		return nil // malformed payload will never parse on retry; don't loop
	}
	// event_key ties the message to the source event so redelivery is a no-op. Prefer the payload's
	// event_id; fall back to the outbox row id.
	if p.EventID == "" {
		p.EventID = e.EventID
	}
	return c.route(ctx, e.Topic, p)
}

// route maps a P1 topic + payload to the right channel and posts a system EVENT message, idempotent
// on the payload's event key. Shared by handle (explicit msg.event.* rows) and handleRaw (observed
// raw domain events). A branchless yard event is dropped (nil, no loop); a missing seed channel
// errors so the outbox row retries (recoverable setup). Returns an error ONLY for the retryable
// missing-channel case, every drop is a logged nil.
func (c *Consumer) route(ctx context.Context, topic string, p eventPayload) error {
	eventKey := p.EventID
	body, ref := render(topic, p)
	ref.Topic = topic

	switch topic {
	case TopicOrderReleased, TopicOrderBlocked, TopicStockReceived, TopicDeliveryScheduled:
		// Yard events route to the branch's #yard-<name> channel.
		if p.BranchID == "" {
			c.log.Warn("messaging: yard event with no branch_id, dropping", "topic", topic, "event", eventKey)
			return nil
		}
		slug, err := c.svc.YardSlugForBranch(ctx, p.BranchID)
		if err != nil {
			// Unknown branch is a data problem, not transient, log and consider delivered.
			c.log.Warn("messaging: cannot resolve yard channel for branch, dropping",
				"topic", topic, "branch", p.BranchID, "err", err)
			return nil
		}
		return c.post(ctx, slug, eventKey, body, ref, topic, p)

	case TopicPaymentReceived, TopicCreditHold:
		// - DIB-449: the TIER ROUTER -
		// This is the one branch the whole ticket turns on, so it is stated plainly: a ROUTINE event
		// does NOT get a message of its own. It goes to the day's rollup in the payments channel, as a
		// thread reply under one line. Everything else keeps the shipped behaviour exactly, NOTABLE
		// and CRITICAL both post one message per event, and the CRITICAL path (forced-ack, the
		// #escalations mirror, DIB-217/269/275) is untouched by design.
		//
		// The class is read per event rather than cached at boot, because it is admin-tunable config
		// and an operator who turns payments back into per-event messages must see it take effect
		// without a deploy.
		if c.svc.ClassOf(ctx, topic) == ClassRoutine {
			return c.routeRoutine(ctx, topic, eventKey, body, ref, p)
		}
		// Money events route to the #finance team channel.
		return c.post(ctx, financeSlug, eventKey, body, ref, topic, p)

	default:
		// Registered only for MessagingTopics + the mapped raw topics, so this is unreachable.
		return fmt.Errorf("messaging: no route for topic %q", topic)
	}
}

// routeRoutine posts a ROUTINE event into the day's rollup instead of as its own message (DIB-449).
//
// Routing: the payment's OWN channel, per branch (#payments-trenton), resolved from the same branch
// map as the yard channels; a branchless payment (the raw payment.received payload carries no branch)
// lands in the GLOBAL #payments channel. Either way it does NOT land in #finance, "other channels
// carry zero payment messages" is the ruling, and a fallback that quietly re-used #finance would
// rebuild the noise for exactly the events whose payload is thinnest.
//
// Failure policy matches the rest of this consumer: a missing seed channel is recoverable setup and
// returns an error so the outbox row retries; anything unroutable is a logged nil so it cannot loop.
func (c *Consumer) routeRoutine(ctx context.Context, topic, eventKey, body string, ref EventRef, p eventPayload) error {
	slug := paymentsSlug(c.yardSlugOrEmpty(ctx, p.BranchID))
	res, err := c.svc.PostRoutineEvent(ctx, slug, eventKey, body, ref, eventCents(p), time.Now())
	if err != nil {
		return fmt.Errorf("rollup %s to #%s: %w", topic, slug, err) // retry
	}
	if res.Inserted {
		c.log.Debug("messaging: routine event folded into the day's rollup",
			"topic", topic, "channel", slug, "event", eventKey, "count", res.Count)
	}
	c.notifyPaymentRules(ctx, topic, eventKey, ref, p)
	return nil
}

// notifyPaymentRules is the DIB-462 step, and it runs AFTER the rollup on purpose.
//
// ORDER IS THE DESIGN. The rollup is the ledger fact and it lands first, unconditionally; a rule is an
// extra notice for one person on top of a day line that already exists. Nothing here can suppress,
// delay or alter the rollup, which is what makes it true that deleting every rule row restores DIB-449
// exactly.
//
// A failure is LOGGED, NOT RETURNED, and the reason is worth stating because it is the opposite of the
// policy two functions up. A missing seed channel makes the routine event retry, because without it the
// payment is recorded NOWHERE. Here the payment is already recorded, in the day's rollup, in full,
// with its thread reply, so the worst case is that one person misses one notice they opted into.
// Returning an error instead would put the outbox row back in PENDING and re-run the rollup write on
// every retry, so a permanently broken rule store (a missing table on a half-migrated box) would spin
// the outbox forever over an optional garnish. Degrade the garnish, keep the meal.
func (c *Consumer) notifyPaymentRules(ctx context.Context, topic, eventKey string, ref EventRef, p eventPayload) {
	if topic != TopicPaymentReceived || p.PaymentID == "" {
		return
	}
	res, err := c.svc.NotifyPaymentRules(ctx, eventKey, p.PaymentID, ref)
	if err != nil {
		c.log.Warn("messaging: payment notification rules did not run, the rollup is unaffected",
			"payment", p.PaymentID, "event", eventKey, "err", err)
		return
	}
	for _, n := range res.Notices {
		if n.Inserted {
			c.log.Debug("messaging: targeted payment notice delivered",
				"payment", p.PaymentID, "user", n.UserID, "rules", n.Rules)
		}
	}
}

// yardSlugOrEmpty resolves a branch to its yard slug, returning "" for an absent or unknown branch.
// The branch is OPTIONAL for a routine event (unlike a yard event, which is dropped without one),
// because the global #payments channel is a correct destination, a payment with no branch is still a
// payment, and dropping it would lose a ledger fact from the only surface that itemises them.
func (c *Consumer) yardSlugOrEmpty(ctx context.Context, branchID string) string {
	if branchID == "" {
		return ""
	}
	slug, err := c.svc.YardSlugForBranch(ctx, branchID)
	if err != nil {
		c.log.Warn("messaging: cannot resolve branch for routine event, using the global channel",
			"branch", branchID, "err", err)
		return ""
	}
	return slug
}

// eventCents is the money on a routine event, in cents, for the rollup's total.
//
// AmountCents is the field a producer SHOULD send and the raw observer path fills directly. The
// AmountText fallback exists because the shipped msg.event.* family carries only the RENDERED amount
// ("$1,240.00"), without the fallback every explicit producer would contribute 0 and the day's total
// would read $0.00 beside a count of fourteen, which is worse than no total at all.
func eventCents(p eventPayload) int64 {
	if p.AmountCents != 0 {
		return p.AmountCents
	}
	return parseDollars(p.AmountText)
}

// parseDollars reads a rendered "$1,240.00" / "-$12.34" back into cents. It is deliberately lenient
// about presentation (currency sign, thousands separators, whitespace) and strict about arithmetic:
// anything it cannot read contributes 0 rather than a guess, because a wrong number on a money total
// is worse than a missing one. A value with no decimal point is read as whole dollars.
func parseDollars(s string) int64 {
	neg := false
	whole, frac := int64(0), int64(0)
	fracDigits := 0
	afterPoint := false
	for _, ch := range s {
		switch {
		case ch == '-':
			neg = true
		case ch >= '0' && ch <= '9':
			if afterPoint {
				if fracDigits < 2 { // ignore sub-cent precision rather than rounding it in
					frac = frac*10 + int64(ch-'0')
					fracDigits++
				}
			} else {
				whole = whole*10 + int64(ch-'0')
			}
		case ch == '.':
			if afterPoint {
				return 0 // two decimal points is not a number we should be guessing at
			}
			afterPoint = true
		case ch == '$' || ch == ',' || ch == ' ' || ch == ' ':
			// presentation, not value
		default:
			return 0 // a character we do not understand: contribute nothing, do not invent a total
		}
	}
	for fracDigits < 2 { // "$12.5" is 12 dollars 50 cents
		frac *= 10
		fracDigits++
	}
	cents := whole*100 + frac
	if neg {
		return -cents
	}
	return cents
}

// post writes the event message and lets a routing miss (unknown channel slug) retry, since a
// missing seed channel is a recoverable setup issue rather than a malformed event.
func (c *Consumer) post(ctx context.Context, slug, eventKey, body string, ref EventRef, topic string, p eventPayload) error {
	// DIB-217: eventOptsFor is the SINGLE place topic+payload → (priority, forced-ack, state key) is
	// decided. requiresAck(topic) remains the 0071 allowlist (credit hold / order blocked), DIB-217
	// CONSUMES that allowlist rather than forking a competing definition of "high priority".
	//
	// The state key is stamped on EVERY routed event, not just the high-priority ones, because the
	// common real conflict is an ORDINARY event resolving a forced-ack one: "order released" arriving
	// for the same order that is still showing a blocked bubble. PostPriorityEvent runs the
	// LAST-WRITE-WINS sweep and mirrors the HIGH tier into the un-mutable #escalations channel.
	opts := eventOptsFor(topic, p)
	_, inserted, err := c.svc.PostPriorityEvent(ctx, slug, eventKey, body, ref, opts)
	if err != nil {
		return fmt.Errorf("post %s to #%s: %w", topic, slug, err) // retry
	}
	if inserted {
		c.log.Debug("messaging: event posted", "topic", topic, "channel", slug, "event", eventKey)
	}
	// Feature A (0069), per-recipient desktop-notification decision point. The message is now PERSISTED
	// to the channel (that is unconditional, the post itself must never be suppressed). What is gated is
	// which MEMBERS get a desktop notification: that fans out per recipient, keyed on each member's prefs
	// (svc.GetNotifyPrefs) and the channel's tier (svc.ChannelTier) run through svc.ShouldNotify -
	// so a FIREHOSE escalation pierces a mute for a user who opted into overrides. The predicate +
	// its seams are implemented and unit-tested (service.go: shouldNotify / ShouldNotify), but wiring the
	// actual per-recipient desktop delivery belongs in the SSE hub / desktop-push path (sse.go), which
	// does not yet load prefs or fan out to individual recipients.
	// TODO(wire per-recipient notify): in the SSE/desktop-push fan-out, for each channel member load
	// prefs + tier and gate the desktop notification on svc.ShouldNotify(prefs, tier). Left here (not in
	// this consumer) because the consumer posts to a CHANNEL, not to individual recipients, deeper
	// wiring is risky until the per-recipient delivery seam exists.
	return nil
}

// render builds the human message body + the {entity, deep_link, summary} ref for a topic+payload.
// Kept pure (no ctx/DB) so it is unit-testable in isolation, the event→message mapping test drives
// this directly.
func render(topic string, p eventPayload) (string, EventRef) {
	ref := EventRef{Entity: entityOf(p), DeepLink: p.DeepLink}
	switch topic {
	case TopicOrderReleased:
		body := fmt.Sprintf("Order %s released, pick list ready.", orderLabel(p))
		ref.Summary = body
		return body, ref
	case TopicOrderBlocked:
		body := fmt.Sprintf("Order %s blocked", orderLabel(p))
		if p.Reason != "" {
			body += ", " + p.Reason
		}
		body += "."
		ref.Summary = body
		return body, ref
	case TopicStockReceived:
		body := "Stock received"
		if p.SKU != "" {
			body += ", " + p.SKU
			if p.Qty != "" {
				body += " ×" + p.Qty
			}
		}
		body += "."
		ref.Summary = body
		return body, ref
	case TopicDeliveryScheduled:
		body := "Delivery scheduled"
		if p.RunNo != "" {
			body += ", run " + p.RunNo
		}
		if p.Date != "" {
			body += " on " + p.Date
		}
		body += "."
		ref.Summary = body
		return body, ref
	case TopicPaymentReceived:
		body := "Payment received"
		if p.AmountText != "" {
			body += ", " + p.AmountText
		}
		if p.OrderNo != "" {
			body += " on " + p.OrderNo
		}
		// Surface the captured payment method (CASH/CARD) as context, normalizeRaw stashes it in
		// p.Reason for exactly this, mirroring the TopicCreditHold arm.
		if p.Reason != "" {
			body += " (" + p.Reason + ")"
		}
		body += "."
		ref.Summary = body
		return body, ref
	case TopicCreditHold:
		body := "Credit hold"
		who := p.Customer
		if who == "" {
			who = p.CustomerID
		}
		if who != "" {
			body += ", " + who
		}
		if p.Reason != "" {
			body += " (" + p.Reason + ")"
		}
		body += "."
		ref.Summary = body
		return body, ref
	default:
		return "System event.", ref
	}
}

func orderLabel(p eventPayload) string {
	if p.OrderNo != "" {
		return "#" + p.OrderNo
	}
	if p.OrderID != "" {
		return p.OrderID
	}
	return "(unknown)"
}

func entityOf(p eventPayload) string {
	switch {
	case p.OrderID != "":
		return "order:" + p.OrderID
	case p.OrderNo != "":
		return "order:" + p.OrderNo
	case p.CustomerID != "":
		return "customer:" + p.CustomerID
	case p.SKU != "":
		return "product:" + p.SKU
	default:
		return ""
	}
}
