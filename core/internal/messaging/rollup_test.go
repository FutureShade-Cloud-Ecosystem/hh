package messaging

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// - DIB-449 -
//
// The tester asked: "will we be receiving messages, everytime a payment is recieved? I would think
// that is not necessary." The tests below pin the two halves of the answer:
//
//	the ROUTER  , a ROUTINE event does not become a message (routeRoutine, not post)
//	the ROLLUP  , a day is ONE message, updated in place, never an appending log
//
// Both are named mutation targets: flip the router to per-event and the first block fails; flip the
// rollup to append-instead-of-update and the second block fails.

// rollupFixture wires a consumer over the shared double with the shipped 0152 classification and the
// #payments channels resolvable by slug.
func rollupFixture(t *testing.T) (*Consumer, *fakeStore) {
	t.Helper()
	f := newFakeStore()
	f.seedDefaultClasses()
	f.slugs["payments-trenton"] = fkPayTrenton
	f.slugs["payments"] = fkPayGlobal
	f.slugs[financeSlug] = fkFinance
	f.slugs[escalationSlug] = fkEscalations
	f.yard[fkBrTrenton] = "yard-trenton"
	svc := svcWith(f)
	return NewConsumer(svc, slog.New(slog.DiscardHandler)), f
}

const (
	fkPayTrenton  = "ch-payments-trenton"
	fkPayGlobal   = "ch-payments"
	fkFinance     = "ch-finance"
	fkEscalations = "ch-escalations"
	fkBrTrenton   = "br-trenton"
)

// onlyRoot returns THE single channel-surface message in a channel, failing the test if there is not
// exactly one.
//
// It exists because of what the M1 mutation run did without it: every assertion below reaches for
// `rootsIn(ch)[0]`, and a router that stops rolling up leaves that slice EMPTY, so the index panicked
// , and a panic does not fail one test, it aborts the whole test binary. Under -shuffle=on that
// silently discarded every test ordered after it, including the two that name this exact regression.
// The guard that is supposed to prove the fix must not be able to destroy the evidence that it broke.
func onlyRoot(t *testing.T, f *fakeStore, channelID string) Message {
	t.Helper()
	roots := f.rootsIn(channelID)
	if len(roots) != 1 {
		t.Fatalf("expected exactly ONE channel-surface message in %s, got %d: %v", channelID, len(roots), roots)
	}
	return roots[0]
}

// payEvent renders one payment through the real render() + route() path, the same code the outbox
// consumer runs, so these tests exercise routing, not a hand-built call.
func payEvent(t *testing.T, c *Consumer, key, orderNo string, cents int64, branch string) {
	t.Helper()
	p := eventPayload{EventID: key, BranchID: branch, OrderNo: orderNo, AmountText: dollars(cents), AmountCents: cents}
	if err := c.route(context.Background(), TopicPaymentReceived, p); err != nil {
		t.Fatalf("route payment %s: %v", key, err)
	}
}

// - THE ROUTER -

// MUTATION TARGET: route ROUTINE payments to the per-event post() path (the pre-DIB-449 behaviour)
// and this test fails, three payments would produce three channel-surface messages instead of one.
//
// This is the tester's complaint, stated as an assertion: the SURFACE of the payments channel after
// three payments is ONE line, and it says what all three said.
func TestRoutineEvent_ThreePaymentsProduceOneChannelMessage(t *testing.T) {
	c, f := rollupFixture(t)
	payEvent(t, c, "p1", "SO-1041", 124000, fkBrTrenton)
	payEvent(t, c, "p2", "SO-1042", 388050, fkBrTrenton)
	payEvent(t, c, "p3", "SO-1043", 92075, fkBrTrenton)

	roots := f.rootsIn(fkPayTrenton)
	if len(roots) != 1 {
		bodies := []string{}
		for _, m := range roots {
			bodies = append(bodies, m.Body)
		}
		t.Fatalf("three payments must collapse to ONE channel message, got %d: %v", len(roots), bodies)
	}
	if !strings.Contains(roots[0].Body, "3") || !strings.Contains(roots[0].Body, "$6,041.25") {
		t.Fatalf("the day's line must state the count AND the money, got %q", roots[0].Body)
	}
}

// The individual payments are NOT lost, they are thread replies under the day, in full, each still
// carrying its own deep-linkable ref. "Drill into a day to see each payment" is the whole bargain that
// makes collapsing them acceptable; without this the fix would be data loss wearing a summary's coat.
func TestRoutineEvent_IndividualPaymentsSurviveAsThreadReplies(t *testing.T) {
	c, f := rollupFixture(t)
	payEvent(t, c, "p1", "SO-1041", 124000, fkBrTrenton)
	payEvent(t, c, "p2", "SO-1042", 388050, fkBrTrenton)

	root := onlyRoot(t, f, fkPayTrenton)
	replies := f.repliesUnder(root.ID)
	if len(replies) != 2 {
		t.Fatalf("each payment must survive as a thread reply, got %d", len(replies))
	}
	if !strings.Contains(replies[0].Body, "$1,240.00") || !strings.Contains(replies[0].Body, "SO-1041") {
		t.Fatalf("a reply must be the FULL payment line, got %q", replies[0].Body)
	}
	var ref EventRef
	if err := json.Unmarshal(replies[0].Ref, &ref); err != nil {
		t.Fatalf("reply ref: %v", err)
	}
	if ref.AmountCents != 124000 {
		t.Fatalf("a reply must carry its money as a number for the fold, got %d", ref.AmountCents)
	}
	if ref.Topic != TopicPaymentReceived {
		t.Fatalf("a reply must keep its own event topic (deep links), got %q", ref.Topic)
	}
}

// ZERO payment messages anywhere else. #finance is the channel the tester was watching fill up, and
// the ruling is explicit that other channels carry none.
func TestRoutineEvent_NoPaymentMessagesInOtherChannels(t *testing.T) {
	c, f := rollupFixture(t)
	payEvent(t, c, "p1", "SO-1041", 124000, fkBrTrenton)
	payEvent(t, c, "p2", "SO-1042", 388050, fkBrTrenton)

	if n := len(f.rootsIn(fkFinance)); n != 0 {
		t.Fatalf("#finance must carry ZERO payment messages, got %d", n)
	}
	if n := len(f.rootsIn(fkEscalations)); n != 0 {
		t.Fatalf("#escalations must carry ZERO payment messages, got %d", n)
	}
}

// A payment with no branch (the raw payment.received payload carries none) lands in the GLOBAL
// #payments channel, never back in #finance, which would rebuild the noise for exactly the events
// whose payload is thinnest, and never dropped, which would lose a ledger fact.
func TestRoutineEvent_BranchlessPaymentLandsInGlobalPaymentsChannel(t *testing.T) {
	c, f := rollupFixture(t)
	payEvent(t, c, "p1", "SO-1060", 5000, "")

	if n := len(f.rootsIn(fkPayGlobal)); n != 1 {
		t.Fatalf("a branchless payment must land in the global #payments channel, got %d messages", n)
	}
	if n := len(f.rootsIn(fkFinance)); n != 0 {
		t.Fatalf("a branchless payment must NOT fall back to #finance, got %d", n)
	}
}

// - THE ROLLUP -

// MUTATION TARGET: make the rollup APPEND a new total instead of superseding/updating the day's
// message and this fails. A day must be ONE message, the Wave-5 LAST-WRITE-WINS ruling applied to a
// running total, where "Payments today: 13" beside "Payments today: 14" is not history, it is a stale
// fact presented as current.
func TestRollup_DayIsOneMessage_NotAnAppendingLog(t *testing.T) {
	c, f := rollupFixture(t)
	for _, p := range []struct {
		key   string
		cents int64
	}{{"p1", 100}, {"p2", 200}, {"p3", 300}, {"p4", 400}, {"p5", 500}} {
		payEvent(t, c, p.key, "SO-x", p.cents, fkBrTrenton)
	}
	roots := f.rootsIn(fkPayTrenton)
	if len(roots) != 1 {
		t.Fatalf("five payments across one day must leave exactly ONE message, got %d", len(roots))
	}
	if !strings.Contains(roots[0].Body, "5") || !strings.Contains(roots[0].Body, "$15.00") {
		t.Fatalf("the ONE message must carry the CURRENT total, got %q", roots[0].Body)
	}
}

// The body is a FOLD over durable replies, not a counter. The outbox is at-least-once, so this is the
// difference between a total an AR clerk can reconcile against and one that drifts upward on every
// redelivery.
func TestRollup_RedeliveryDoesNotDoubleCount(t *testing.T) {
	c, f := rollupFixture(t)
	payEvent(t, c, "p1", "SO-1041", 124000, fkBrTrenton)
	payEvent(t, c, "p2", "SO-1042", 388050, fkBrTrenton)
	payEvent(t, c, "p2", "SO-1042", 388050, fkBrTrenton) // the SAME event, redelivered
	payEvent(t, c, "p1", "SO-1041", 124000, fkBrTrenton) // and again, out of order

	root := onlyRoot(t, f, fkPayTrenton)
	if !strings.Contains(root.Body, "2") || !strings.Contains(root.Body, "$5,120.50") {
		t.Fatalf("redelivery must not inflate the day, got %q", root.Body)
	}
	if n := len(f.repliesUnder(root.ID)); n != 2 {
		t.Fatalf("redelivery must not duplicate replies, got %d", n)
	}
}

// Two days are two messages. The rollup collapses a DAY, not all of history, yesterday's line must
// still say what yesterday was, or the surface would rewrite the past every morning.
func TestRollup_SeparateDaysAreSeparateMessages(t *testing.T) {
	f := newFakeStore()
	f.seedDefaultClasses()
	f.slugs["payments-trenton"] = fkPayTrenton
	svc := svcWith(f)
	ctx := context.Background()
	yesterday := time.Now().Add(-26 * time.Hour)

	if _, err := svc.PostRoutineEvent(ctx, "payments-trenton", "y1", "Payment received, $10.00.", EventRef{Topic: TopicPaymentReceived}, 1000, yesterday); err != nil {
		t.Fatalf("yesterday: %v", err)
	}
	if _, err := svc.PostRoutineEvent(ctx, "payments-trenton", "t1", "Payment received, $20.00.", EventRef{Topic: TopicPaymentReceived}, 2000, time.Now()); err != nil {
		t.Fatalf("today: %v", err)
	}
	if n := len(f.rootsIn(fkPayTrenton)); n != 2 {
		t.Fatalf("two days must be two rollup messages, got %d", n)
	}
}

// The rollup root carries the day's facts as NUMBERS on its ref, so a client (or this test) never has
// to parse the rendered sentence to learn what the line says.
func TestRollup_RootRefCarriesStructuredTotals(t *testing.T) {
	c, f := rollupFixture(t)
	payEvent(t, c, "p1", "SO-1041", 124000, fkBrTrenton)
	payEvent(t, c, "p2", "SO-1042", 388050, fkBrTrenton)

	var ref EventRef
	if err := json.Unmarshal(onlyRoot(t, f, fkPayTrenton).Ref, &ref); err != nil {
		t.Fatalf("root ref: %v", err)
	}
	if ref.Count != 2 || ref.AmountCents != 512050 {
		t.Fatalf("root ref must carry count+total, got count=%d cents=%d", ref.Count, ref.AmountCents)
	}
	if ref.Topic != rollupTopicPayment {
		t.Fatalf("the root must be identifiable as a rollup, got topic %q", ref.Topic)
	}
	if ref.RollupDay == "" {
		t.Fatalf("the root must name its day")
	}
}

// The day is the LOCAL day. A UTC boundary would cut the Ontario working day at 8pm EDT and file the
// evening's receipts under tomorrow, on the one surface whose entire job is "what came in today".
func TestRollupDay_UsesTheHouseZoneNotUTC(t *testing.T) {
	// 2026-08-12 01:30 UTC is 2026-08-11 21:30 in Toronto, still the 11th's business day.
	at := time.Date(2026, 8, 12, 1, 30, 0, 0, time.UTC)
	if got := rollupDay(at); got != "2026-08-11" {
		t.Fatalf("an evening receipt must belong to the local day, got %q", got)
	}
}

// - THE CLASS BOUNDARY (and the DIB-269/275 invariant) -

// MUTATION TARGET: let the config table decide forced-ack and this fails. requiresAck(topic) is the
// SOLE authority for the CRITICAL tier (0138 gated operator-configurable priority rules), so a
// corrupted or maliciously-written row cannot downgrade a live alert into a silent rollup line.
func TestEventClass_CodeAllowlistOverridesAConfigDowngrade(t *testing.T) {
	c, f := rollupFixture(t)
	f.setClass(TopicCreditHold, ClassRoutine, false) // the table now LIES about a forced-ack topic
	f.setClass(TopicOrderBlocked, ClassRoutine, false)

	if got := svcWith(f).ClassOf(context.Background(), TopicCreditHold); got != ClassCritical {
		t.Fatalf("a forced-ack topic is CRITICAL whatever the table says, got %q", got)
	}
	p := eventPayload{EventID: "h1", CustomerID: "cust-1", Customer: "Kingsway"}
	if err := c.route(context.Background(), TopicCreditHold, p); err != nil {
		t.Fatalf("route credit hold: %v", err)
	}
	// It must still be a per-event, forced-ack post in #finance, not folded into a rollup.
	held := f.rootsIn(fkFinance)
	if len(held) != 1 || !held[0].RequiresAck {
		t.Fatalf("a credit hold must still post per-event and force an ack, got %+v", held)
	}
	if n := len(f.rootsIn(fkPayTrenton)); n != 0 {
		t.Fatalf("a credit hold must never be folded into the payments rollup, got %d", n)
	}
}

// The un-mutable escalation invariant (DIB-269) is untouched: a HIGH-priority event is still mirrored
// into #escalations. This is the regression this ticket most had to avoid.
func TestEventClass_CriticalStillMirrorsToEscalations(t *testing.T) {
	c, f := rollupFixture(t)
	p := eventPayload{EventID: "h1", CustomerID: "cust-1", Customer: "Kingsway"}
	if err := c.route(context.Background(), TopicCreditHold, p); err != nil {
		t.Fatalf("route credit hold: %v", err)
	}
	if n := len(f.rootsIn(fkEscalations)); n != 1 {
		t.Fatalf("a HIGH event must still mirror into #escalations, got %d", n)
	}
}

// An admin may not reclassify a locked (CRITICAL) row. The refusal is a 409-shaped error naming the
// rule, mirroring DIB-269's un-mutable refusal, an admin who believes they turned a critical alert
// into a rollup, and did not, is the failure this refusal prevents.
func TestSetEventClass_LockedCriticalRowIsRefused(t *testing.T) {
	f := newFakeStore()
	f.seedDefaultClasses()
	svc := svcWith(f)
	for _, topic := range []string{TopicCreditHold, TopicOrderBlocked} {
		if _, err := svc.SetEventClass(context.Background(), uAlice, topic, ClassRoutine); err != ErrEventClassLocked {
			t.Fatalf("%s must refuse reclassification with ErrEventClassLocked, got %v", topic, err)
		}
	}
}

// An unlocked row IS tunable, the knob has to actually turn, or "tech-admin tunable" is a lie too.
// Turning payments back to NOTABLE restores per-event posting without a deploy.
func TestSetEventClass_UnlockedRowIsTunable_AndTheRouterFollowsIt(t *testing.T) {
	c, f := rollupFixture(t)
	svc := svcWith(f)
	if _, err := svc.SetEventClass(context.Background(), uAlice, TopicPaymentReceived, ClassNotable); err != nil {
		t.Fatalf("reclassify payments: %v", err)
	}
	payEvent(t, c, "p1", "SO-1041", 124000, fkBrTrenton)
	if n := len(f.rootsIn(fkFinance)); n != 1 {
		t.Fatalf("reclassified to NOTABLE, a payment must post per-event again, got %d", n)
	}
	if n := len(f.rootsIn(fkPayTrenton)); n != 0 {
		t.Fatalf("reclassified to NOTABLE, nothing should reach the rollup, got %d", n)
	}
}

// A class outside the CHECK set is refused at the service boundary, never as a Postgres constraint
// violation surfacing as a 500.
func TestSetEventClass_UnknownClassIsRejected(t *testing.T) {
	f := newFakeStore()
	f.seedDefaultClasses()
	if _, err := svcWith(f).SetEventClass(context.Background(), uAlice, TopicPaymentReceived, "QUIET"); err != ErrBadEventClass {
		t.Fatalf("unknown class must be ErrBadEventClass, got %v", err)
	}
}

// An unclassified topic degrades to NOTABLE, today's shipped per-event behaviour. Defaulting an
// unknown event to ROUTINE would silently fold a message nobody decided to fold.
func TestEffectiveClass_UnknownTopicDefaultsToNotable(t *testing.T) {
	if got := effectiveClass("msg.event.something.new", ""); got != ClassNotable {
		t.Fatalf("an unclassified topic must default to NOTABLE (seen), got %q", got)
	}
	if got := effectiveClass("msg.event.something.new", "NONSENSE"); got != ClassNotable {
		t.Fatalf("an out-of-range class must clamp to NOTABLE, got %q", got)
	}
}

// - money parsing -

// The shipped msg.event.* producers send only the RENDERED amount, so the fallback parse is what
// keeps the day's total from reading $0.00 beside a count of fourteen. It contributes 0 rather than a
// guess for anything it cannot read, a wrong number on a money line is worse than a missing one.
func TestParseDollars(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"$1,240.00", 124000},
		{"$920.75", 92075},
		{"-$12.34", -1234},
		{"$12.5", 1250},
		{"$0.00", 0},
		{"12", 1200},
		{"", 0},
		{"n/a", 0},
		{"$1.2.3", 0},
	}
	for _, c := range cases {
		if got := parseDollars(c.in); got != c.want {
			t.Fatalf("parseDollars(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// eventCents prefers the NUMBER a producer sent over re-parsing its own prose.
func TestEventCents_PrefersTheNumericField(t *testing.T) {
	p := eventPayload{AmountCents: 999, AmountText: "$1,000.00"}
	if got := eventCents(p); got != 999 {
		t.Fatalf("the numeric field must win, got %d", got)
	}
	if got := eventCents(eventPayload{AmountText: "$10.00"}); got != 1000 {
		t.Fatalf("fallback parse, got %d", got)
	}
}

// A rollup always states a number. dollars(0) is "" so a per-event body degrades gracefully when a
// producer omits the amount, but "Payments today: 3 · " reads as a broken line, not a fact.
func TestRenderRollupBody_AlwaysStatesANumber(t *testing.T) {
	if got := renderRollupBody(3, 0, "2026-08-11"); !strings.Contains(got, "$0.00") {
		t.Fatalf("a counted rollup must state a total, got %q", got)
	}
	if got := renderRollupBody(0, 0, "2026-08-11"); !strings.Contains(got, "none yet") {
		t.Fatalf("an empty day should say so, got %q", got)
	}
}

// REGRESSION (caught by TestIntegration_Rollup_ADayIsOneRow): the stored body must NAME its day, never
// say "today". A body is written once and only rewritten when another payment lands on that same day,
// so a line that said "today" would go on saying it forever, a stale fact presented as current, which
// is the exact failure the supersede ruling this rollup reuses exists to prevent. "Today" is the HUB's
// word, evaluated at paint time when it can be true (app/src/_messaging-rollup.ts).
func TestRenderRollupBody_NamesTheDayAndNeverSaysToday(t *testing.T) {
	today := rollupDay(time.Now())
	for _, day := range []string{today, "2019-03-14"} {
		got := renderRollupBody(4, 125000, day)
		if strings.Contains(got, "today") {
			t.Fatalf("the STORED body must never say today (it outlives the day), got %q", got)
		}
		if !strings.Contains(got, day) {
			t.Fatalf("the stored body must name its day, got %q", got)
		}
	}
}

// paymentsSlug follows the house branch-channel convention and falls back to the global channel.
func TestPaymentsSlug(t *testing.T) {
	cases := map[string]string{
		"yard-trenton":  "payments-trenton",
		"yard-kingston": "payments-kingston",
		"":              "payments",
		"weird":         "payments",
	}
	for in, want := range cases {
		if got := paymentsSlug(in); got != want {
			t.Fatalf("paymentsSlug(%q) = %q, want %q", in, got, want)
		}
	}
}
