package messaging

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

// - render(): the event→message body + ref mapping (pure) -

func TestRender_TopSixBodies(t *testing.T) {
	cases := []struct {
		topic   string
		p       eventPayload
		wantSub string // substring the body must contain
	}{
		{TopicOrderReleased, eventPayload{OrderNo: "SO-214"}, "#SO-214 released"},
		{TopicOrderBlocked, eventPayload{OrderNo: "SO-9", Reason: "credit hold"}, "blocked, credit hold"},
		{TopicStockReceived, eventPayload{SKU: "DBS-PAV-01", Qty: "12"}, "DBS-PAV-01 ×12"},
		{TopicDeliveryScheduled, eventPayload{RunNo: "R-7", Date: "2026-07-15"}, "run R-7 on 2026-07-15"},
		{TopicPaymentReceived, eventPayload{AmountText: "$3,000.00", OrderNo: "SO-100"}, "Payment received, $3,000.00 on SO-100"},
		{TopicCreditHold, eventPayload{Customer: "Quinte Hardscapes", Reason: "NSF"}, "Credit hold, Quinte Hardscapes (NSF)"},
	}
	for _, c := range cases {
		body, ref := render(c.topic, c.p)
		if !strings.Contains(body, c.wantSub) {
			t.Errorf("%s body %q missing %q", c.topic, body, c.wantSub)
		}
		if ref.Summary != body {
			t.Errorf("%s: ref.Summary should mirror the body", c.topic)
		}
	}
}

func TestRender_OrderLabelFallbacks(t *testing.T) {
	if got := orderLabel(eventPayload{OrderID: "abc"}); got != "abc" {
		t.Errorf("order id fallback: %q", got)
	}
	if got := orderLabel(eventPayload{}); got != "(unknown)" {
		t.Errorf("empty order label: %q", got)
	}
}

// - handle(): routing to the right channel -

func mkEvent(t *testing.T, topic string, p eventPayload) notify.Event {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return notify.Event{EventID: "outbox-1", Topic: topic, Payload: b}
}

func TestConsumer_YardEventRoutesToBranchChannel(t *testing.T) {
	f := newFakeStore()
	f.yard["branch-trt"] = "yard-trenton"
	f.slugs["yard-trenton"] = "ch-yard-trt"
	c := NewConsumer(svcWith(f), nil)

	ev := mkEvent(t, TopicOrderReleased, eventPayload{EventID: "e1", BranchID: "branch-trt", OrderNo: "SO-214"})
	if err := c.handle(context.Background(), ev); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(f.events) != 1 || f.events[0].ChannelID != "ch-yard-trt" {
		t.Fatalf("order.released should land in the branch yard channel, got %+v", f.events)
	}
	if !strings.Contains(f.events[0].Body, "released") {
		t.Fatalf("body wrong: %q", f.events[0].Body)
	}
}

func TestConsumer_MoneyEventRoutesToFinance(t *testing.T) {
	f := newFakeStore()
	f.slugs["finance"] = "ch-finance"
	c := NewConsumer(svcWith(f), nil)

	ev := mkEvent(t, TopicPaymentReceived, eventPayload{EventID: "e2", AmountText: "$500.00", OrderNo: "SO-1"})
	if err := c.handle(context.Background(), ev); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(f.events) != 1 || f.events[0].ChannelID != "ch-finance" {
		t.Fatalf("payment.received should land in #finance, got %+v", f.events)
	}
}

func TestConsumer_CreditHoldRoutesToFinance(t *testing.T) {
	f := newFakeStore()
	f.slugs["finance"] = "ch-finance"
	c := NewConsumer(svcWith(f), nil)
	ev := mkEvent(t, TopicCreditHold, eventPayload{EventID: "e3", Customer: "Acme", Reason: "over limit"})
	if err := c.handle(context.Background(), ev); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(f.events) != 1 || f.events[0].ChannelID != "ch-finance" {
		t.Fatalf("credit.hold should land in #finance, got %+v", f.events)
	}
}

func TestConsumer_YardEventNoBranchIsDroppedNotRetried(t *testing.T) {
	f := newFakeStore()
	c := NewConsumer(svcWith(f), nil)
	// A yard event with no branch_id cannot be routed, it must be treated as delivered (nil error,
	// no retry loop), NOT posted anywhere.
	ev := mkEvent(t, TopicStockReceived, eventPayload{EventID: "e4", SKU: "X"})
	if err := c.handle(context.Background(), ev); err != nil {
		t.Fatalf("unroutable event should be dropped (nil), got %v", err)
	}
	if len(f.events) != 0 {
		t.Fatalf("nothing should be posted for a branchless yard event")
	}
}

func TestConsumer_MissingSeedChannelRetries(t *testing.T) {
	f := newFakeStore()
	f.yard["branch-x"] = "yard-x" // resolvable branch...
	// ...but no channel with slug "yard-x" is seeded, so the post should ERROR (outbox row retries),
	// which is the recoverable-setup case.
	c := NewConsumer(svcWith(f), nil)
	ev := mkEvent(t, TopicDeliveryScheduled, eventPayload{EventID: "e5", BranchID: "branch-x", RunNo: "R1"})
	if err := c.handle(context.Background(), ev); err == nil {
		t.Fatalf("a missing seed channel should error so the event retries")
	}
}

func TestConsumer_BadPayloadDropped(t *testing.T) {
	f := newFakeStore()
	c := NewConsumer(svcWith(f), nil)
	ev := notify.Event{EventID: "o", Topic: TopicOrderReleased, Payload: []byte("not json")}
	if err := c.handle(context.Background(), ev); err != nil {
		t.Fatalf("undecodable payload should be dropped (nil), got %v", err)
	}
}

func TestConsumer_EventKeyFallsBackToOutboxID(t *testing.T) {
	f := newFakeStore()
	f.slugs["finance"] = "ch-finance"
	c := NewConsumer(svcWith(f), nil)
	// No event_id in the payload → the outbox row id is used as the dedup key.
	ev := notify.Event{EventID: "outbox-77", Topic: TopicPaymentReceived, Payload: []byte(`{"amount":"$1.00"}`)}
	if err := c.handle(context.Background(), ev); err != nil {
		t.Fatalf("handle: %v", err)
	}
	// Redeliver the SAME outbox row, must be idempotent.
	if err := c.handle(context.Background(), ev); err != nil {
		t.Fatalf("redeliver: %v", err)
	}
	if len(f.events) != 1 {
		t.Fatalf("redelivery with the same outbox id must not duplicate, got %d", len(f.events))
	}
}

// MessagingTopics must stay disjoint from the gl/inventory posted topics or the dispatcher panics at
// boot. This pins the invariant (the exact foreign sets are asserted in the integration wiring test;
// here we at least prove our own family is msg.event.* and has the expected size).
func TestMessagingTopics_FamilyAndSize(t *testing.T) {
	if len(MessagingTopics) != 6 {
		t.Fatalf("P1 owns exactly the top-6 topics, got %d", len(MessagingTopics))
	}
	for _, tp := range MessagingTopics {
		if !strings.HasPrefix(tp, "msg.event.") {
			t.Errorf("topic %q must be in the msg.event.* family to avoid collisions", tp)
		}
	}
}

// - FR-3 observed raw topics: the fix that makes events actually fire -

// ObservedTopics are the RAW domain topics (not msg.event.*), owned by GL/inventory. Messaging must
// only OBSERVE topics that have a real producer, and must never observe its own family.
func TestObservedTopics_AreRawDomainTopics(t *testing.T) {
	if len(ObservedTopics) == 0 {
		t.Fatal("FR-3 must observe at least one real domain topic or events never fire")
	}
	for _, tp := range ObservedTopics {
		if strings.HasPrefix(tp, "msg.event.") {
			t.Errorf("observed topic %q must be a raw domain topic (owned elsewhere), not our own family", tp)
		}
	}
}

func TestHandleRaw_PaymentReceivedPostsToFinance(t *testing.T) {
	f := newFakeStore()
	f.slugs["finance"] = "ch-finance"
	c := NewConsumer(svcWith(f), nil)
	ev := notify.Event{EventID: "ob-1", Topic: RawPaymentReceived,
		Payload: []byte(`{"payment_id":"p1","customer_id":"c1","amount_cents":300000,"method":"CASH"}`)}
	if err := c.handleRaw(context.Background(), ev); err != nil {
		t.Fatalf("handleRaw: %v", err)
	}
	if len(f.events) != 1 || f.events[0].ChannelID != "ch-finance" {
		t.Fatalf("payment.received should post to #finance, got %+v", f.events)
	}
	if !strings.Contains(f.events[0].Body, "$3,000.00") {
		t.Fatalf("amount should render as dollars, got %q", f.events[0].Body)
	}
}

func TestHandleRaw_PaymentBouncedPostsCreditHoldToFinance(t *testing.T) {
	f := newFakeStore()
	f.slugs["finance"] = "ch-finance"
	c := NewConsumer(svcWith(f), nil)
	ev := notify.Event{EventID: "ob-2", Topic: RawPaymentBounced,
		Payload: []byte(`{"payment_id":"p2","customer_id":"c2","amount_cents":45000}`)}
	if err := c.handleRaw(context.Background(), ev); err != nil {
		t.Fatalf("handleRaw: %v", err)
	}
	if len(f.events) != 1 || f.events[0].ChannelID != "ch-finance" {
		t.Fatalf("payment.bounced should post a credit hold to #finance, got %+v", f.events)
	}
	if !strings.Contains(f.events[0].Body, "Credit hold") {
		t.Fatalf("bounced payment should render as a credit hold, got %q", f.events[0].Body)
	}
}

func TestHandleRaw_POReceivedPostsToYard(t *testing.T) {
	f := newFakeStore()
	f.yard["branch-trt"] = "yard-trenton"
	f.slugs["yard-trenton"] = "ch-yard-trt"
	c := NewConsumer(svcWith(f), nil)
	ev := notify.Event{EventID: "ob-3", Topic: RawPOReceived,
		Payload: []byte(`{"po_id":"po1","receipt_id":"r9","branch_id":"branch-trt","value_cents":120000}`)}
	if err := c.handleRaw(context.Background(), ev); err != nil {
		t.Fatalf("handleRaw: %v", err)
	}
	if len(f.events) != 1 || f.events[0].ChannelID != "ch-yard-trt" {
		t.Fatalf("po.received should post to the branch yard channel, got %+v", f.events)
	}
	if !strings.Contains(f.events[0].Body, "Stock received") {
		t.Fatalf("po.received should render as stock received, got %q", f.events[0].Body)
	}
}

// Observed raw events are idempotent on the outbox id: a redelivered raw row posts at most once.
func TestHandleRaw_IdempotentOnOutboxID(t *testing.T) {
	f := newFakeStore()
	f.slugs["finance"] = "ch-finance"
	c := NewConsumer(svcWith(f), nil)
	ev := notify.Event{EventID: "ob-dup", Topic: RawPaymentReceived, Payload: []byte(`{"amount_cents":100}`)}
	_ = c.handleRaw(context.Background(), ev)
	_ = c.handleRaw(context.Background(), ev)
	if len(f.events) != 1 {
		t.Fatalf("redelivered raw event must post at most once, got %d", len(f.events))
	}
}

func TestHandleRaw_UnmappedTopicIsNoOp(t *testing.T) {
	f := newFakeStore()
	c := NewConsumer(svcWith(f), nil)
	ev := notify.Event{EventID: "x", Topic: "some.other.topic", Payload: []byte(`{}`)}
	if err := c.handleRaw(context.Background(), ev); err != nil {
		t.Fatalf("unmapped observed topic must be a no-op, got %v", err)
	}
	if len(f.events) != 0 {
		t.Fatal("nothing should post for an unmapped topic")
	}
}

func TestDollars(t *testing.T) {
	cases := map[int64]string{0: "", 100: "$1.00", 300000: "$3,000.00", 45000: "$450.00", 1234567: "$12,345.67", -100: "-$1.00", -1234567: "-$12,345.67"}
	for cents, want := range cases {
		if got := dollars(cents); got != want {
			t.Errorf("dollars(%d) = %q, want %q", cents, got, want)
		}
	}
}

// TestDollars_MinInt64NoOverflow is the regression for the MinInt64 negation overflow. Pre-fix,
// `cents = -cents` at math.MinInt64 wraps back to the same negative value (−9223372036854775808 has
// no positive int64 counterpart), so the thousands-grouping loop ran over a leading '-' byte and the
// result was a corrupt string like "-$-9,223,...". The uint64-magnitude path renders the true
// magnitude with a single leading sign.
func TestDollars_MinInt64NoOverflow(t *testing.T) {
	got := dollars(math.MinInt64)
	want := "-$92,233,720,368,547,758.08"
	if got != want {
		t.Fatalf("dollars(math.MinInt64) = %q, want %q", got, want)
	}
	// Guard the specific corruption shape: exactly one sign, no digit-adjacent stray '-'.
	if strings.Count(got, "-") != 1 || strings.Contains(got, "$-") {
		t.Fatalf("dollars(math.MinInt64) is corrupt: %q", got)
	}
}
