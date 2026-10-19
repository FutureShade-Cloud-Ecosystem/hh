package messaging

import (
	"context"

	"log/slog"
	"strings"
	"testing"
)

// - DIB-462 -
//
// The client asked for three targeted notifications on top of the DIB-449 rollup. The tests below pin
// the four claims the feature makes, each named so a mutation says which one it broke:
//
//	THE DEFAULT  , nobody is notified until somebody switches a rule on. (the whole safety of it)
//	THE MATCHERS , online / cross-branch / own-order each match ONE thing and refuse the near-miss.
//	THE ROLLUP   , is not touched by any of it.
//	THE CRITICAL , forced-ack path never enters this code.

const (
	uRepSam    = "u-sam" // the assigned rep on SO-1041
	uClerkAvi  = "u-avi" // an AR clerk who opts into ONLINE
	uOtherPat  = "u-pat" // opts into nothing, ever
	fkBrKgn    = "br-kingston"
	custFerris = "cust-ferris"
	ordSO1041  = "ord-1041"
)

// ruleFixture is the rollup fixture plus the DIB-462 facts: one payment, resolvable to an order at
// Trenton owned by Sam, taken wherever the test says.
func ruleFixture(t *testing.T) (*Consumer, *fakeStore) {
	t.Helper()
	c, f := rollupFixture(t)
	f.yard[fkBrKgn] = "yard-kingston"
	f.slugs["payments-kingston"] = "ch-payments-kingston"
	// Sam is the rep on the ORDER (the DIB-273 per-order override).
	if _, err := f.UpsertRepAssignment(context.Background(), RepAssignment{
		CustomerID: custFerris, OrderID: ordSO1041, RepUserID: uRepSam, Source: AssignManual,
	}); err != nil {
		t.Fatalf("seed rep assignment: %v", err)
	}
	// The fence's defaults: the three staffers are global and hold the payment trail, so the rule tests
	// below prove the rules; payment_fence_test.go proves the fence by taking one of those away.
	trail := &fakeTrail{holds: map[string]bool{}}
	for _, u := range []string{uRepSam, uClerkAvi, uOtherPat} {
		f.setRecipient(u, PaymentRecipient{PlatformUserID: "p-" + u, OrgIDs: []string{"org-1"}, Global: true})
		trail.holds["p-"+u] = true
	}
	c.svc.WithPaymentAccess(trail)
	return c, f
}

// counterFacts is a payment somebody took at a Trenton till, on Sam's Trenton order.
func counterFacts() PaymentFacts {
	return PaymentFacts{
		Found:         true,
		TakenBranchID: fkBrTrenton, TakenBranchName: "Trenton", TakenAtCounter: true,
		OrderID: ordSO1041, OrderNo: "SO-1041", OrderBranchID: fkBrTrenton, OrderBranch: "Trenton",
		CustomerID: custFerris, CustomerName: "Ferris Landscaping", AmountCents: 124000,
		OnStatement: true,
	}
}

// payWithID routes one payment carrying a payment_id, through the real render()/route() path.
func payWithID(t *testing.T, c *Consumer, key, paymentID string, cents int64, branch string) {
	t.Helper()
	p := eventPayload{
		EventID: key, PaymentID: paymentID, BranchID: branch,
		OrderNo: "SO-1041", AmountText: dollars(cents), AmountCents: cents,
	}
	if err := c.route(context.Background(), TopicPaymentReceived, p); err != nil {
		t.Fatalf("route payment %s: %v", key, err)
	}
}

// noticesFor returns the messages delivered into one user's DM channel.
func noticesFor(f *fakeStore, userID string) []Message {
	return f.rootsIn("dm-" + userID)
}

// noticeRefFor returns the structured ref the fake captured alongside the FIRST notice written into a
// user's DM channel. The double records refs parallel to events (f.eventRefs), so this reads the same
// pair the shipped code wrote rather than re-marshalling anything.
func noticeRefFor(t *testing.T, f *fakeStore, userID string) EventRef {
	t.Helper()
	for i := range f.events {
		if f.events[i].ChannelID == "dm-"+userID {
			return f.eventRefs[i]
		}
	}
	t.Fatalf("no notice was written into %s's DM channel", userID)
	return EventRef{}
}

// - THE DEFAULT -

// MUTATION TARGET: make any rule fire for a user who has not opted in and this test dies.
//
// This is the load-bearing test of the whole ticket. DIB-449's fix was "stop pushing routine payments
// at people"; DIB-462 is only safe to add on top of it because a rule reaches EXACTLY the people who
// asked. A payment that matches all three rules, with nobody opted in, must notify nobody at all.
func TestPaymentRules_NoRuleRowsMeansNobodyIsNotified(t *testing.T) {
	c, f := ruleFixture(t)
	facts := counterFacts()
	facts.TakenAtCounter = false  // matches ONLINE
	facts.TakenBranchID = fkBrKgn // matches CROSS_BRANCH
	facts.TakenBranchName = "Kingston"
	f.setPaymentFacts("pay-1", facts) // and it is Sam's order → would match OWN_ORDER

	payWithID(t, c, "e1", "pay-1", 124000, fkBrKgn)

	for _, u := range []string{uRepSam, uClerkAvi, uOtherPat} {
		if n := len(noticesFor(f, u)); n != 0 {
			t.Fatalf("nobody has opted in: %s must have ZERO notices, got %d", u, n)
		}
	}
}

// An EXPLICIT off is as quiet as no row at all. The stored FALSE row exists to remember the decision,
// never to behave differently from its absence.
func TestPaymentRules_ExplicitOptOutIsAsQuietAsNoRow(t *testing.T) {
	c, f := ruleFixture(t)
	f.optOut(uClerkAvi, RulePaymentOnline)
	facts := counterFacts()
	facts.TakenAtCounter = false
	f.setPaymentFacts("pay-1", facts)

	payWithID(t, c, "e1", "pay-1", 124000, fkBrTrenton)

	if n := len(noticesFor(f, uClerkAvi)); n != 0 {
		t.Fatalf("an explicit opt-out must notify nobody, got %d notices", n)
	}
}

// - THE MATCHERS: online -

// MUTATION TARGET: define ONLINE by method/gateway instead of "nobody took it", and the counter-CARD
// case below starts matching.
func TestPaymentRules_OnlineMatcher(t *testing.T) {
	online := counterFacts()
	online.TakenAtCounter = false
	counter := counterFacts() // till session / received_by stamped

	if !matchesOnline(online) {
		t.Fatal("a payment nobody took at a counter is the ONLINE case")
	}
	if matchesOnline(counter) {
		t.Fatal("a payment a staffer took at a counter is NOT online, even a CARD one")
	}
	if matchesOnline(PaymentFacts{}) {
		t.Fatal("a payment we could not resolve must match nothing")
	}
}

// The online notice reaches the opted-in clerk, and says what happened rather than what setting let it
// through. This is the "I don't have to call them again" case, end to end.
func TestPaymentRules_OnlinePaymentNotifiesTheOptedInUser(t *testing.T) {
	c, f := ruleFixture(t)
	f.optIn(uClerkAvi, RulePaymentOnline)
	facts := counterFacts()
	facts.TakenAtCounter = false
	f.setPaymentFacts("pay-1", facts)

	payWithID(t, c, "e1", "pay-1", 124000, fkBrTrenton)

	got := noticesFor(f, uClerkAvi)
	if len(got) != 1 {
		t.Fatalf("the opted-in clerk must get exactly ONE notice, got %d", len(got))
	}
	body := got[0].Body
	for _, want := range []string{"$1,240.00", "SO-1041", "Ferris Landscaping", "Paid online"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the notice must state the fact (%q missing), got %q", want, body)
		}
	}
	if n := len(noticesFor(f, uOtherPat)); n != 0 {
		t.Fatalf("a user who opted into nothing must get nothing, got %d", n)
	}
}

// - THE MATCHERS: cross-branch -

// MUTATION TARGET: make the cross-branch matcher return true for a SAME-branch payment (drop the
// inequality, or compare the wrong pair) and this test dies.
func TestPaymentRules_CrossBranchMatcher(t *testing.T) {
	same := counterFacts() // taken at Trenton, order is Trenton's
	if matchesCrossBranch(same) {
		t.Fatal("a payment taken at the order's OWN branch is not cross-branch")
	}

	cross := counterFacts()
	cross.TakenBranchID, cross.TakenBranchName = fkBrKgn, "Kingston"
	if !matchesCrossBranch(cross) {
		t.Fatal("Kingston taking money on a Trenton order IS the cross-branch case")
	}

	// "We could not tell" is not "they differ": an unknown branch on either side must not match.
	unknownTaken := cross
	unknownTaken.TakenBranchID = ""
	if matchesCrossBranch(unknownTaken) {
		t.Fatal("an unknown TAKING branch must not match, we cannot claim a difference we cannot see")
	}
	unknownOrder := cross
	unknownOrder.OrderBranchID = ""
	if matchesCrossBranch(unknownOrder) {
		t.Fatal("an unknown ORDER branch must not match")
	}

	// No order at all: there is no branch for the money to have landed away from.
	noOrder := cross
	noOrder.OrderID, noOrder.OrderNo, noOrder.OrderBranchID = "", "", ""
	if matchesCrossBranch(noOrder) {
		t.Fatal("a payment with no order cannot be cross-branch")
	}
}

func TestPaymentRules_CrossBranchPaymentNotifiesTheOptedInUser(t *testing.T) {
	c, f := ruleFixture(t)
	f.optIn(uClerkAvi, RulePaymentCrossBranch)
	facts := counterFacts()
	facts.TakenBranchID, facts.TakenBranchName = fkBrKgn, "Kingston"
	f.setPaymentFacts("pay-1", facts)

	payWithID(t, c, "e1", "pay-1", 124000, fkBrKgn)

	got := noticesFor(f, uClerkAvi)
	if len(got) != 1 {
		t.Fatalf("cross-branch must notify the opted-in user once, got %d", len(got))
	}
	if !strings.Contains(got[0].Body, "Taken at Kingston") || !strings.Contains(got[0].Body, "Trenton") {
		t.Fatalf("the notice must name BOTH branches, got %q", got[0].Body)
	}
}

// A same-branch payment notifies the cross-branch subscriber NOT AT ALL. The negative case is the one
// a broken matcher gets wrong, and it is the one that would make the feature noise.
func TestPaymentRules_SameBranchPaymentDoesNotNotifyTheCrossBranchSubscriber(t *testing.T) {
	c, f := ruleFixture(t)
	f.optIn(uClerkAvi, RulePaymentCrossBranch)
	f.setPaymentFacts("pay-1", counterFacts()) // Trenton till, Trenton order

	payWithID(t, c, "e1", "pay-1", 124000, fkBrTrenton)

	if n := len(noticesFor(f, uClerkAvi)); n != 0 {
		t.Fatalf("a same-branch payment must not notify a cross-branch subscriber, got %d", n)
	}
}

// - THE MATCHERS: own order -

// MUTATION TARGET: notify the whole opted-in population for OWN_ORDER (treat it as a broadcast rule)
// and the second half of this test dies.
func TestPaymentRules_OwnOrderMatcher(t *testing.T) {
	f := counterFacts()
	if !matchesOwnOrder(f, uRepSam, uRepSam) {
		t.Fatal("the order's rep owns the order")
	}
	if matchesOwnOrder(f, uRepSam, uOtherPat) {
		t.Fatal("somebody else's order is not yours")
	}
	noOrder := f
	noOrder.OrderID = ""
	if matchesOwnOrder(noOrder, uRepSam, uRepSam) {
		t.Fatal("a payment with no order cannot be 'your order', account-level is a gated 4th rule")
	}
	if matchesOwnOrder(f, "", uRepSam) {
		t.Fatal("an unowned order matches nobody")
	}
}

func TestPaymentRules_OwnOrderNotifiesOnlyTheAssignedRep(t *testing.T) {
	c, f := ruleFixture(t)
	f.optIn(uRepSam, RulePaymentOwnOrder)
	f.optIn(uOtherPat, RulePaymentOwnOrder) // opted in, but it is not Pat's order
	f.setPaymentFacts("pay-1", counterFacts())

	payWithID(t, c, "e1", "pay-1", 124000, fkBrTrenton)

	sam := noticesFor(f, uRepSam)
	if len(sam) != 1 {
		t.Fatalf("the assigned rep must be told about money on their own order, got %d", len(sam))
	}
	if !strings.Contains(sam[0].Body, "On your order") {
		t.Fatalf("the notice must say why it arrived, got %q", sam[0].Body)
	}
	if n := len(noticesFor(f, uOtherPat)); n != 0 {
		t.Fatalf("OWN_ORDER is a claim about a PERSON: Pat must get nothing, got %d", n)
	}
}

// The DIB-273 resolution order is not re-decided here: a per-order override beats the customer-level
// rep, exactly as resolveAssignment rules it. One expression of ownership in the codebase, not two.
func TestPaymentRules_OwnOrderFollowsTheRepAssignmentRuling(t *testing.T) {
	c, f := ruleFixture(t)
	// Pat holds the CUSTOMER-level book; Sam holds the ORDER override (seeded in the fixture).
	if _, err := f.UpsertRepAssignment(context.Background(), RepAssignment{
		CustomerID: custFerris, RepUserID: uOtherPat, Source: AssignManual,
	}); err != nil {
		t.Fatalf("seed customer-level assignment: %v", err)
	}
	f.optIn(uRepSam, RulePaymentOwnOrder)
	f.optIn(uOtherPat, RulePaymentOwnOrder)
	f.setPaymentFacts("pay-1", counterFacts())

	payWithID(t, c, "e1", "pay-1", 124000, fkBrTrenton)

	if n := len(noticesFor(f, uRepSam)); n != 1 {
		t.Fatalf("the ORDER override owns this order: Sam wants 1 notice, got %d", n)
	}
	if n := len(noticesFor(f, uOtherPat)); n != 0 {
		t.Fatalf("the customer-level rep is overridden for THIS order: Pat wants 0, got %d", n)
	}
}

// - ONE PERSON, ONE MESSAGE -

// A payment matching two of a person's rules produces ONE notice naming both reasons. Two messages
// for one payment would rebuild, for the person who opted in, the exact pile DIB-449 removed.
func TestPaymentRules_TwoRulesForOnePersonProduceOneNotice(t *testing.T) {
	c, f := ruleFixture(t)
	f.optIn(uRepSam, RulePaymentOnline)
	f.optIn(uRepSam, RulePaymentOwnOrder)
	facts := counterFacts()
	facts.TakenAtCounter = false
	f.setPaymentFacts("pay-1", facts)

	payWithID(t, c, "e1", "pay-1", 124000, fkBrTrenton)

	got := noticesFor(f, uRepSam)
	if len(got) != 1 {
		t.Fatalf("two matching rules must still be ONE message, got %d", len(got))
	}
	if !strings.Contains(got[0].Body, "Paid online") || !strings.Contains(got[0].Body, "On your order") {
		t.Fatalf("the one notice must carry BOTH reasons, got %q", got[0].Body)
	}
	ref := noticeRefFor(t, f, uRepSam)
	if len(ref.Rules) != 2 || ref.Rules[0] != RulePaymentOnline || ref.Rules[1] != RulePaymentOwnOrder {
		t.Fatalf("the ref must name the rules in canonical order, got %v", ref.Rules)
	}
	// A notice is a single payment, not a day: it must not look like a rollup root to the client.
	if ref.Count != 0 || ref.RollupDay != "" {
		t.Fatalf("a targeted notice must not carry rollup fields, got count=%d day=%q", ref.Count, ref.RollupDay)
	}
}

// Redelivery of the same payment writes nothing new, the same at-least-once discipline as every other
// event in this module, keyed per (event, recipient).
func TestPaymentRules_RedeliveryDoesNotDuplicateTheNotice(t *testing.T) {
	c, f := ruleFixture(t)
	f.optIn(uClerkAvi, RulePaymentOnline)
	facts := counterFacts()
	facts.TakenAtCounter = false
	f.setPaymentFacts("pay-1", facts)

	payWithID(t, c, "e1", "pay-1", 124000, fkBrTrenton)
	payWithID(t, c, "e1", "pay-1", 124000, fkBrTrenton) // the outbox re-delivers

	if n := len(noticesFor(f, uClerkAvi)); n != 1 {
		t.Fatalf("a redelivered payment must not produce a second notice, got %d", n)
	}
}

// - THE ROLLUP IS UNAFFECTED -

// MUTATION TARGET: let a rule change the routine path, suppress the rollup for a matched payment,
// or post the notice instead of the day line, and this test dies.
//
// DIB-449's answer survives DIB-462 intact: the channel surface is still ONE line for the day, the
// individual payments are still thread replies, and #finance still carries zero payment messages, with
// every rule in the system switched on.
func TestPaymentRules_RollupIsUnaffectedByRules(t *testing.T) {
	c, f := ruleFixture(t)
	for _, r := range PaymentRules {
		f.optIn(uRepSam, r)
		f.optIn(uClerkAvi, r)
	}
	facts := counterFacts()
	facts.TakenAtCounter = false
	facts.TakenBranchID, facts.TakenBranchName = fkBrKgn, "Kingston"
	f.setPaymentFacts("pay-1", facts)
	f.setPaymentFacts("pay-2", facts)

	payWithID(t, c, "e1", "pay-1", 124000, fkBrTrenton)
	payWithID(t, c, "e2", "pay-2", 388050, fkBrTrenton)

	root := onlyRoot(t, f, fkPayTrenton)
	if !strings.Contains(root.Body, "2") || !strings.Contains(root.Body, "$5,120.50") {
		t.Fatalf("the day line must still state the count and the money, got %q", root.Body)
	}
	if n := len(f.repliesUnder(root.ID)); n != 2 {
		t.Fatalf("both payments must still be thread replies, got %d", n)
	}
	if n := len(f.rootsIn(fkFinance)); n != 0 {
		t.Fatalf("#finance must still carry ZERO payment messages, got %d", n)
	}
}

// A broken rule store degrades the NOTICE, never the rollup. The ledger fact is already durable when
// the rules run; returning an error here would spin the outbox forever over an optional garnish.
func TestPaymentRules_ABrokenRuleStoreCannotBreakTheRollup(t *testing.T) {
	c, f := ruleFixture(t)
	f.optIn(uClerkAvi, RulePaymentOnline)
	f.breakPaymentFacts()

	payWithID(t, c, "e1", "pay-1", 124000, fkBrTrenton)

	root := onlyRoot(t, f, fkPayTrenton)
	if !strings.Contains(root.Body, "$1,240.00") {
		t.Fatalf("the rollup must land even when the rules cannot run, got %q", root.Body)
	}
	if n := len(noticesFor(f, uClerkAvi)); n != 0 {
		t.Fatalf("a failed rule evaluation must not invent a notice, got %d", n)
	}
}

// - THE CRITICAL PATH IS UNTOUCHED -

// A credit hold is CRITICAL: forced-ack, #finance, mirrored to #escalations. It never enters
// routeRoutine, so it can never enter the rule engine, no rule can add to it, and none can silence
// it. The 0071 allowlist remains the sole authority.
func TestPaymentRules_CriticalEventsNeverEnterTheRuleEngine(t *testing.T) {
	c, f := ruleFixture(t)
	for _, r := range PaymentRules {
		f.optIn(uRepSam, r)
		f.optIn(uClerkAvi, r)
	}
	f.setPaymentFacts("pay-nsf", counterFacts())

	p := eventPayload{EventID: "e-nsf", PaymentID: "pay-nsf", BranchID: fkBrTrenton,
		Customer: "Ferris Landscaping", AmountText: dollars(124000), Reason: "NSF, payment bounced"}
	if err := c.route(context.Background(), TopicCreditHold, p); err != nil {
		t.Fatalf("route credit hold: %v", err)
	}

	hold := f.rootsIn(fkFinance)
	if len(hold) != 1 {
		t.Fatalf("a credit hold must still post ONE message to #finance, got %d", len(hold))
	}
	if !hold[0].RequiresAck {
		t.Fatal("a credit hold must still be forced-ack (the 0071 allowlist, untouched)")
	}
	if n := len(f.rootsIn(fkEscalations)); n != 1 {
		t.Fatalf("a credit hold must still mirror to #escalations, got %d", n)
	}
	for _, u := range []string{uRepSam, uClerkAvi} {
		if n := len(noticesFor(f, u)); n != 0 {
			t.Fatalf("the rule engine must never see a CRITICAL event: %s got %d notices", u, n)
		}
	}
}

// - PREFS: the read is always the full set -

func TestGetPaymentRules_ReturnsEveryRuleWithUnsetOnesFalse(t *testing.T) {
	f := newFakeStore()
	f.optIn(uClerkAvi, RulePaymentOnline)
	svc := svcWith(f)

	prefs, err := svc.GetPaymentRules(context.Background(), Caller{UserID: uClerkAvi})
	if err != nil {
		t.Fatalf("get payment rules: %v", err)
	}
	if len(prefs.Rules) != len(PaymentRules) {
		t.Fatalf("the surface must render every rule, got %d of %d", len(prefs.Rules), len(PaymentRules))
	}
	if !prefs.Rules[RulePaymentOnline] {
		t.Fatal("the stored ON must come back on")
	}
	if prefs.Rules[RulePaymentCrossBranch] || prefs.Rules[RulePaymentOwnOrder] {
		t.Fatal("an unstored rule must read as FALSE, 'never decided' and 'off' mean the same thing")
	}
}

func TestSetPaymentRule_RejectsAnUnknownRule(t *testing.T) {
	svc := svcWith(newFakeStore())
	if _, err := svc.SetPaymentRule(context.Background(), Caller{UserID: uClerkAvi}, "EVERYTHING", true); err == nil {
		t.Fatal("an unknown rule id must be refused at the boundary, never written")
	}
}

// A caller with no id cannot read or write anybody's rules, the self-scoping is in the service, not
// only in the handler.
func TestPaymentRules_RequireACaller(t *testing.T) {
	svc := svcWith(newFakeStore())
	if _, err := svc.GetPaymentRules(context.Background(), Caller{}); err == nil {
		t.Fatal("an anonymous caller must not read rules")
	}
	if _, err := svc.SetPaymentRule(context.Background(), Caller{}, RulePaymentOnline, true); err == nil {
		t.Fatal("an anonymous caller must not write rules")
	}
}

// - the notice body -

// The sentence states the fact first and the reason second, and degrades honestly when a fact is
// missing (no order → "on account"; no branch names → generic words, never a blank).
func TestRenderPaymentNotice_StatesTheFactThenTheReason(t *testing.T) {
	f := counterFacts()
	f.TakenAtCounter = false
	got := renderPaymentNotice(f, []string{RulePaymentOnline})
	if !strings.HasPrefix(got, "Payment $1,240.00 on SO-1041, Ferris Landscaping.") {
		t.Fatalf("the fact must come first, got %q", got)
	}

	acct := PaymentFacts{Found: true, AmountCents: 50000, CustomerName: "Ferris Landscaping", OnStatement: true}
	if got := renderPaymentNotice(acct, []string{RulePaymentOnline}); !strings.Contains(got, "on account") {
		t.Fatalf("a payment with no order reads as on account, got %q", got)
	}

	bare := PaymentFacts{Found: true, OrderNo: "SO-1041"}
	if got := paymentReason(bare, RulePaymentCrossBranch); strings.Contains(got, "at ;") || strings.Contains(got, "is 's") {
		t.Fatalf("missing branch names must degrade to words, not to blanks: %q", got)
	}
}

// A consumer with no logger must not panic on the rule path (NewConsumer's nil-logger contract).
func TestPaymentRules_NilLoggerConsumerIsSafe(t *testing.T) {
	f := newFakeStore()
	f.seedDefaultClasses()
	f.slugs["payments"] = fkPayGlobal
	c := NewConsumer(svcWith(f), slog.New(slog.DiscardHandler))
	payWithID(t, c, "e1", "pay-unknown", 1000, "")
}
