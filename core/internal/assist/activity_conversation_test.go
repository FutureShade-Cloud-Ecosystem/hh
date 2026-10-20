package assist

// activity_conversation_test.go, DIB-494's conversational surface, driven exactly as the
// frontend drives it (the convo helper threads each answer's structured turn back into the next
// request). The scenario under every test here is the Aug-18 sync's sentence:
//
//	"Hey, something went wrong with this order, can you tell me the activity on it?"
//
// and the properties are the DIB-478 ones, inherited rather than reinvented: the referent is
// resolved by code over the structured history, a missing referent is admitted rather than guessed
// at, and the questions the trail words must NOT claim (the status line, the account's activity)
// keep their existing answers.

import (
	"context"
	"strings"
	"testing"
	"time"
)

// activityService is deepService plus the DIB-494 seam.
func activityService(rd *fakeReaders, d *fakeDeep, fa *fakeActivity) *Service {
	deps := deepDeps(rd, d)
	deps.Activity = fa
	return NewService(NewRegistry(deps, nil), NewStubProvider().WithClock(func() time.Time { return testNow }),
		&fakeLedger{}, newFakeLimits(), StaticSettings{}, nil).
		WithClock(func() time.Time { return testNow }).
		WithResolver(deepResolver(d))
}

// The sync's exact conversation: find the order, then ask for the activity on "it". The second
// turn must resolve onto the ORDER's trail, not the account's recent activity, and not a refusal -
// with the carried order named on screen.
func TestTheSyncPhraseCarriesTheOrderIntoItsTrail(t *testing.T) {
	rd, d, fa := seededReaders(), seededDeep(), seededActivity()
	svc := activityService(rd, d, fa)

	answers := convo(t, svc, frontline(),
		"where is SO-4?",
		"something went wrong with this order, can you tell me the activity on it?",
	)
	first, second := answers[0], answers[1]
	if len(first.Ran) != 1 || first.Ran[0].Command != "order.lookup" {
		t.Fatalf("turn 1 ran %+v, want order.lookup", first.Ran)
	}
	if len(second.Ran) != 1 || second.Ran[0].Command != "order.activity" {
		t.Fatalf("turn 2 ran %+v, want order.activity", second.Ran)
	}
	if got := second.Ran[0].Args["order_no"]; got != "SO-4" {
		t.Fatalf("the trail question ran for order %q, want the one the conversation is about", got)
	}
	if fa.gotRef != "SO-4" {
		t.Fatalf("the seam was asked about %q", fa.gotRef)
	}
	// Rule 4: a value the person did not type, in an answer they will act on, is named on screen.
	if !strings.Contains(strings.Join(second.Carried, " · "), "SO-4") {
		t.Fatalf("carried = %v, the carried order must be named", second.Carried)
	}
	if !strings.Contains(second.Text, "recorded changes on SO-4") {
		t.Fatalf("turn 2 spoke %q", second.Text)
	}
}

// "who changed it?" three turns after the order was named, the entity memory, not the last
// invocation, is what carries.
func TestWhoChangedItReachesBackThroughTheWindow(t *testing.T) {
	rd, d, fa := seededReaders(), seededDeep(), seededActivity()
	svc := activityService(rd, d, fa)

	answers := convo(t, svc, frontline(),
		"where is SO-4?",
		"how many TB-BLU60 have we got?", // the subject goes sideways…
		"who changed it?",                // …and the trail question still finds the order
	)
	last := answers[2]
	if len(last.Ran) != 1 || last.Ran[0].Command != "order.activity" || last.Ran[0].Args["order_no"] != "SO-4" {
		t.Fatalf("turn 3 ran %+v, want order.activity on SO-4", last.Ran)
	}
}

// The referent is resolved by CODE, not by whichever provider is plugged in: with a scripted
// provider that never asks for a tool, "who changed it?" still resolves onto the order, the
// provider is consulted exactly once, for the PHRASING, after the calls are already decided. This
// is DIB-478's determinism property carried onto the trail question, and it is what a real vendor
// provider inherits for free.
func TestWhoChangedItResolvesWithoutConsultingAProviderForTheReferent(t *testing.T) {
	rd, d, fa := seededReaders(), seededDeep(), seededActivity()
	deps := deepDeps(rd, d)
	deps.Activity = fa
	p := &scriptedProvider{replies: []Reply{{Text: "(the provider's own phrasing)"}}}
	svc := NewService(NewRegistry(deps, nil), p, &fakeLedger{}, newFakeLimits(), StaticSettings{}, nil).
		WithClock(func() time.Time { return testNow }).
		WithResolver(deepResolver(d))
	svc.withTestMeter()

	history := []Message{
		{Role: RoleUser, Text: "where is SO-4?"},
		{Role: RoleAssistant, Text: "SO-4 is picking.", Ran: []Invocation{{Command: "order.lookup", Args: Args{"order_no": "SO-4"}}}},
	}
	ans, err := svc.Ask(context.Background(), frontline(), "who changed it?", history)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if len(ans.Ran) != 1 || ans.Ran[0].Command != "order.activity" || ans.Ran[0].Args["order_no"] != "SO-4" {
		t.Fatalf("ran %+v, want order.activity on SO-4", ans.Ran)
	}
	// One provider round only, the summarising one. A provider consulted for the REFERENT would
	// appear here as a first round with no tool results in it.
	if len(p.seen) != 1 {
		t.Fatalf("provider consulted %d times, want exactly 1 (the phrasing round)", len(p.seen))
	}
	if len(toolResults(p.seen[0].Messages)) == 0 {
		t.Fatal("the provider's one round must be the phrasing round, with the results already decided")
	}
}

// A trail question with NOTHING to refer to is admitted, in a sentence that names both what is
// missing and what would fix it, never answered by picking a plausible order.
func TestAnUnanchoredTrailQuestionIsAnsweredHonestly(t *testing.T) {
	rd, d, fa := seededReaders(), seededDeep(), seededActivity()
	svc := activityService(rd, d, fa)

	ans, err := svc.Ask(context.Background(), frontline(), "who changed it?", nil)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if len(ans.Ran) != 0 || fa.calls != 0 {
		t.Fatalf("an unanchored pronoun ran a command: ran=%v calls=%d", ans.Ran, fa.calls)
	}
	if !strings.Contains(ans.Text, "which order") || !strings.Contains(ans.Text, "SO-4") {
		t.Fatalf("refusal = %q, it must name the missing thing and the fix (an order number, like SO-4)", ans.Text)
	}
}

// The discriminator, both ways. An order number plus a trail word is the trail; an order number
// alone is still the status line it has always been.
func TestATrailWordBesideTheNumberDecidesWhichOrderQuestionItIs(t *testing.T) {
	rd, d, fa := seededReaders(), seededDeep(), seededActivity()
	svc := activityService(rd, d, fa)

	ans, err := svc.Ask(context.Background(), frontline(), "what happened on SO-4?", nil)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if len(ans.Ran) != 1 || ans.Ran[0].Command != "order.activity" || ans.Ran[0].Args["order_no"] != "SO-4" {
		t.Fatalf("ran %+v, want order.activity on SO-4", ans.Ran)
	}

	ans2, err := svc.Ask(context.Background(), frontline(), "where is SO-4?", nil)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if len(ans2.Ran) != 1 || ans2.Ran[0].Command != "order.lookup" {
		t.Fatalf("ran %+v, the plain status question must stay order.lookup", ans2.Ran)
	}
}

// The complete-sentence path: "what's happened on this order?" is not elliptical, so it goes to the
// provider, and the required order_no fills from the window (FillMissingFromWindow rule set), with
// the fill declared in Carried.
func TestWhatsHappenedOnThisOrderFillsFromTheWindow(t *testing.T) {
	rd, d, fa := seededReaders(), seededDeep(), seededActivity()
	svc := activityService(rd, d, fa)

	answers := convo(t, svc, frontline(),
		"where is SO-4?",
		"what's happened on this order?",
	)
	second := answers[1]
	if len(second.Ran) != 1 || second.Ran[0].Command != "order.activity" || second.Ran[0].Args["order_no"] != "SO-4" {
		t.Fatalf("turn 2 ran %+v, want order.activity on SO-4", second.Ran)
	}
	if !strings.Contains(strings.Join(second.Carried, " · "), "SO-4") {
		t.Fatalf("carried = %v, a filled slot must be declared", second.Carried)
	}
}

// The words the trail must NOT steal. The account's activity question, the phrasing the original
// five commands shipped with, keeps its answer, order in the conversation or not.
func TestTheAccountsActivityQuestionIsUntouched(t *testing.T) {
	rd, d, fa := seededReaders(), seededDeep(), seededActivity()
	svc := activityService(rd, d, fa)

	ans, err := svc.Ask(context.Background(), frontline(), "recent activity for Northgate Landscaping?", nil)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if len(ans.Ran) != 1 || ans.Ran[0].Command != "customer.activity" {
		t.Fatalf("ran %+v, want customer.activity, the trail words must not claim the account question", ans.Ran)
	}
	if got := ans.Ran[0].Args["customer"]; got != "Northgate Landscaping" {
		t.Fatalf("customer = %q", got)
	}
	if fa.calls != 0 {
		t.Fatal("the order trail was read for an account question")
	}
}

// RBAC through the conversation: the resolver can change what is ASKED, never what is ALLOWED. A
// caller whose administrator switched order.activity off gets the plain refusal sentence, not a
// trail, even through the pronoun path.
func TestATrailFollowUpNeverOutrunsThePolicyGate(t *testing.T) {
	rd, d, fa := seededReaders(), seededDeep(), seededActivity()
	deps := deepDeps(rd, d)
	deps.Activity = fa
	policy := StaticPolicy{"order.activity": {Enabled: false, MaxTier: 3}}
	svc := NewService(NewRegistry(deps, policy), NewStubProvider().WithClock(func() time.Time { return testNow }),
		&fakeLedger{}, newFakeLimits(), StaticSettings{}, nil).
		WithClock(func() time.Time { return testNow }).
		WithResolver(deepResolver(d))

	answers := convo(t, svc, frontline(),
		"where is SO-4?",
		"can you tell me the activity on it?",
	)
	second := answers[1]
	if len(second.Ran) != 0 || fa.calls != 0 {
		t.Fatalf("a switched-off command ran: ran=%v calls=%d", second.Ran, fa.calls)
	}
}
