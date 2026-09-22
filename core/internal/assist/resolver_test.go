package assist

// resolver_test.go, DIB-478. The four outcomes, pinned by name.
//
// MUTATION TARGETS, recorded in NOTES-FIX-478.md and named here so a reader of this file knows which
// line each test is standing guard over:
//
//	M1  resolver guesses on ambiguity instead of asking      → TestAmbiguityIsAskedAboutNotGuessedAt dies
//	M2  a follow-up drops the carried entity                 → TestEntityCarriesForwardAcrossTurns dies
//	M3  a missing referent is fabricated from the newest hit → TestNoReferentIsRefusedNotFabricated dies
//	M4  a resolved follow-up skips the registry gate         → TestRBACRefusalSurvivesAConversation dies
//	M5  the range shift keeps the old window's end date      → TestRangeShiftReRunsTheSameQuestion dies

import (
	"context"
	"strings"
	"testing"
	"time"
)

// -- (a) ENTITIES CARRY FORWARD -----------------------------------------------------------------

// TestEntityCarriesForwardAcrossTurns is the ticket's headline example, written as the two sentences
// a person actually says.
//
// "show me Brussels Block stock" → stock.lookup{product}
// "what about Kingston?"          → stock.lookup{product FROM TURN 1, branch FROM TURN 2}
//
// The product is the assertion. A resolver that answered with branch alone would be answering a
// question nobody asked ("what is at Kingston?"), and it would look right on screen, a table of
// numbers under a plausible sentence, which is why this is pinned by name.
func TestEntityCarriesForwardAcrossTurns(t *testing.T) {
	d := seededDeep()
	deps := deepDeps(seededReaders(), d)
	tools := toolsFor(deps, frontline())
	r := deepResolver(d)

	w := NewWindow([]Message{
		{Role: RoleUser, Text: "show me Brussels Block stock"},
		turn("stock.lookup", Args{"product": "Brussels Block"}),
	})
	res := r.Resolve(context.Background(), frontline(), tools, w, "what about Kingston?")

	if res.Kind != ResolveCall {
		t.Fatalf("want a resolved call, got kind=%v refusal=%q", res.Kind, res.Refusal)
	}
	call := res.Calls[0]
	if call.Command != "stock.lookup" {
		t.Fatalf("the follow-up changed command to %q, the INTENT must carry too", call.Command)
	}
	if got := call.Args["product"]; got != "Brussels Block" {
		t.Fatalf("the carried product was dropped: product=%q (this is mutation M2)", got)
	}
	if got := call.Args["branch"]; got != "Kingston" {
		t.Fatalf("the new branch did not land in the branch slot: branch=%q", got)
	}
	// And the answer SAYS what it carried, because a silently carried entity is how a person reads
	// somebody else's number as their own.
	if !containsAny(strings.ToLower(strings.Join(res.Carried, " ")), []string{"brussels block"}) {
		t.Fatalf("the answer must name what it carried, got %v", res.Carried)
	}
}

// TestEntityCarriesForwardThreeTurnsLater proves the memory is the WINDOW's, not the last turn's:
// a product named three questions ago is still the subject of "when's it arriving?".
func TestEntityCarriesForwardThreeTurnsLater(t *testing.T) {
	d := seededDeep()
	deps := deepDeps(seededReaders(), d)
	tools := toolsFor(deps, frontline())
	r := deepResolver(d)

	w := NewWindow([]Message{
		{Role: RoleUser, Text: "stock for TB-BLU60"},
		turn("stock.lookup", Args{"product": "TB-BLU60"}),
		{Role: RoleUser, Text: "what about Kingston?"},
		turn("stock.lookup", Args{"product": "TB-BLU60", "branch": "Kingston"}),
	})
	res := r.Resolve(context.Background(), frontline(), tools, w, "when's more arriving?")

	if res.Kind != ResolveCall {
		t.Fatalf("want a resolved call, got kind=%v refusal=%q", res.Kind, res.Refusal)
	}
	if res.Calls[0].Command != "stock.incoming" {
		t.Fatalf("an arriving question must resolve to stock.incoming, got %q", res.Calls[0].Command)
	}
	if got := res.Calls[0].Args["product"]; got != "TB-BLU60" {
		t.Fatalf("the subject was lost two turns back: product=%q", got)
	}
	if got := res.Calls[0].Args["branch"]; got != "Kingston" {
		t.Fatalf("the branch in play must narrow the new question too: branch=%q", got)
	}
}

// -- (b) INTENT CARRIES FORWARD -----------------------------------------------------------------

// TestRangeShiftReRunsTheSameQuestion is "and last week?".
//
// TWO ASSERTIONS AND THE SECOND IS THE SUBTLE ONE. The window shifts to 10-16 August (Monday-start,
// dates.go, against the pinned Tuesday 18th). And the command CHANGES, payments.today is the
// rollup, which knows only about a day, so the same QUESTION over a week is payments.range. Carrying
// the command code rather than the question would have produced "today" for last week.
func TestRangeShiftReRunsTheSameQuestion(t *testing.T) {
	d := seededDeep()
	deps := deepDeps(seededReaders(), d)
	tools := toolsFor(deps, manager())
	r := deepResolver(d)

	w := NewWindow([]Message{
		{Role: RoleUser, Text: "how much came in today?"},
		turn("payments.today", Args{}),
	})
	res := r.Resolve(context.Background(), manager(), tools, w, "and last week?")

	if res.Kind != ResolveCall {
		t.Fatalf("want a resolved call, got kind=%v refusal=%q", res.Kind, res.Refusal)
	}
	call := res.Calls[0]
	if call.Command != "payments.range" {
		t.Fatalf("a week is not a day, want payments.range, got %q", call.Command)
	}
	if call.Args["from"] != "2026-08-10" || call.Args["to"] != "2026-08-16" {
		t.Fatalf("last week from the pinned Tuesday 18 Aug is Mon 10 - Sun 16; got %s..%s (mutation M5)",
			call.Args["from"], call.Args["to"])
	}
}

// TestRangeShiftReplacesTheWindowRatherThanWideningIt is mutation M5's real teeth: merging the new
// dates onto the old ones instead of replacing them silently widens a range nobody asked to widen.
func TestRangeShiftReplacesTheWindowRatherThanWideningIt(t *testing.T) {
	d := seededDeep()
	deps := deepDeps(seededReaders(), d)
	tools := toolsFor(deps, manager())
	r := deepResolver(d)

	w := NewWindow([]Message{
		{Role: RoleUser, Text: "payments last month"},
		turn("payments.range", Args{"from": "2026-07-01", "to": "2026-07-31", "branch": "Kingston"}),
	})
	res := r.Resolve(context.Background(), manager(), tools, w, "and yesterday?")
	if res.Kind != ResolveCall {
		t.Fatalf("want a resolved call, got %v", res.Kind)
	}
	call := res.Calls[0]
	if call.Args["from"] != "2026-08-17" || call.Args["to"] != "2026-08-17" {
		t.Fatalf("yesterday is one day, got %s..%s", call.Args["from"], call.Args["to"])
	}
	// The BRANCH is not a date and must survive: the person is still asking about Kingston.
	if call.Args["branch"] != "Kingston" {
		t.Fatalf("a range shift dropped the branch: %v", call.Args)
	}
}

// TestCompareRunsBothSides is "compare that to Trenton", a comparative answer shows both.
func TestCompareRunsBothSides(t *testing.T) {
	d := seededDeep()
	deps := deepDeps(seededReaders(), d)
	tools := toolsFor(deps, frontline())
	r := deepResolver(d)

	w := NewWindow([]Message{
		{Role: RoleUser, Text: "stock for TB-BLU60 at Kingston"},
		turn("stock.lookup", Args{"product": "TB-BLU60", "branch": "Kingston"}),
	})
	res := r.Resolve(context.Background(), frontline(), tools, w, "compare that to Trenton")

	if res.Kind != ResolveCompare {
		t.Fatalf("want a comparison, got kind=%v", res.Kind)
	}
	if len(res.Calls) != 2 {
		t.Fatalf("a comparison needs both sides, got %d call(s)", len(res.Calls))
	}
	if res.Calls[0].Args["branch"] != "Kingston" || res.Calls[1].Args["branch"] != "Trenton" {
		t.Fatalf("both sides, in the order they were said: %v then %v", res.Calls[0].Args, res.Calls[1].Args)
	}
	if res.Calls[1].Args["product"] != "TB-BLU60" {
		t.Fatalf("the compared side lost the product: %v", res.Calls[1].Args)
	}
}

// -- (c) AMBIGUITY IS ASKED ABOUT ---------------------------------------------------------------

// TestAmbiguityIsAskedAboutNotGuessedAt, MUTATION TARGET M1.
//
// Two products match 'Blu'. The only honest answers are "which one?" and nothing else: picking the
// first would give a confident wrong number, and "I couldn't find that" would be false, because two
// were found.
func TestAmbiguityIsAskedAboutNotGuessedAt(t *testing.T) {
	d := seededDeep()
	d.catalog = []CatalogueHit{
		{ProductID: "p-1", SKU: "TB-BLU60", Name: "Techo-Bloc Blu 60", MatchReason: "name"},
		{ProductID: "p-2", SKU: "TB-BLU80", Name: "Techo-Bloc Blu 80", MatchReason: "name"},
	}
	deps := deepDeps(seededReaders(), d)
	tools := toolsFor(deps, frontline())
	r := deepResolver(d)

	w := NewWindow([]Message{
		{Role: RoleUser, Text: "stock for TB-BLU60"},
		turn("stock.lookup", Args{"product": "TB-BLU60"}),
	})
	res := r.Resolve(context.Background(), frontline(), tools, w, "what about Blu?")

	if res.Kind != ResolveClarify {
		t.Fatalf("two matches must produce a QUESTION, not a guess, got kind=%v calls=%v (mutation M1)", res.Kind, res.Calls)
	}
	if len(res.Clarify.Options) != 2 {
		t.Fatalf("both candidates must be offered, got %d", len(res.Clarify.Options))
	}
	if res.Clarify.Command != "stock.lookup" || res.Clarify.Arg != "product" {
		t.Fatalf("the clarify must carry the pending invocation: %+v", res.Clarify)
	}
	if !strings.Contains(res.Clarify.Question, "Blu") {
		t.Fatalf("the question must quote what was ambiguous: %q", res.Clarify.Question)
	}
	// The OPTION VALUES are SKUs, not the ambiguous words. Sending the label back would run the same
	// term through the same matcher and ask the same question forever.
	for _, o := range res.Clarify.Options {
		if !strings.HasPrefix(o.Value, "TB-BLU") {
			t.Fatalf("an option value must be unambiguous, got %q", o.Value)
		}
	}
}

// TestAnExactMatchIsNotAmbiguous is the other half of M1: a clarify raised when there is an exact hit
// is a dialog people learn to click through, which is how a confirmation stops being read.
func TestAnExactMatchIsNotAmbiguous(t *testing.T) {
	d := seededDeep()
	d.catalog = []CatalogueHit{
		{ProductID: "p-1", SKU: "TB-BLU60", Name: "Techo-Bloc Blu 60"},
		{ProductID: "p-2", SKU: "TB-BLU60-X", Name: "Techo-Bloc Blu 60 Textured"},
	}
	r := deepResolver(d)
	cl := r.CheckAmbiguousProduct(context.Background(), frontline(), ToolCall{
		Command: "stock.lookup", Args: Args{"product": "Techo-Bloc Blu 60"},
	})
	if cl != nil {
		t.Fatalf("an exact NAME match is not ambiguous; got a clarify: %+v", cl)
	}
}

// TestPickingAClarifyOptionCompletesTheInvocation is the second half of the exchange: the option
// feeds the next turn and the answer arrives, with nothing re-derived.
func TestPickingAClarifyOptionCompletesTheInvocation(t *testing.T) {
	d := seededDeep()
	deps := deepDeps(seededReaders(), d)
	tools := toolsFor(deps, frontline())
	r := deepResolver(d)

	pending := &Clarify{
		Question: `2 products match "Blu", which one?`,
		Command:  "stock.lookup",
		Args:     Args{"branch": "Kingston"},
		Arg:      "product",
		Options: []ClarifyOption{
			{Label: "Techo-Bloc Blu 60 (TB-BLU60)", Value: "TB-BLU60"},
			{Label: "Techo-Bloc Blu 80 (TB-BLU80)", Value: "TB-BLU80"},
		},
	}
	w := NewWindow([]Message{
		{Role: RoleUser, Text: "what about Blu?"},
		{Role: RoleAssistant, Text: pending.Question, Clarify: pending},
	})

	for _, reply := range []string{"TB-BLU80", "Techo-Bloc Blu 80 (TB-BLU80)", "the second one", "blu 80"} {
		res := r.Resolve(context.Background(), frontline(), tools, w, reply)
		if res.Kind != ResolveCall {
			t.Fatalf("reply %q did not complete the clarify: kind=%v", reply, res.Kind)
		}
		if res.Calls[0].Args["product"] != "TB-BLU80" {
			t.Fatalf("reply %q filled the argument with %q", reply, res.Calls[0].Args["product"])
		}
		// The arguments the clarify already held are still there, that is what "half-built" means.
		if res.Calls[0].Args["branch"] != "Kingston" {
			t.Fatalf("reply %q lost the pending branch: %v", reply, res.Calls[0].Args)
		}
	}
}

// TestAnAmbiguousReplyToAClarifyLeavesItStanding: answering "blu" to "which Blu?" is not an answer,
// and resolving it to the first option would be the original guess with an extra click in front.
func TestAnAmbiguousReplyToAClarifyLeavesItStanding(t *testing.T) {
	cl := &Clarify{
		Command: "stock.lookup", Arg: "product", Args: Args{},
		Options: []ClarifyOption{
			{Label: "Techo-Bloc Blu 60", Value: "TB-BLU60"},
			{Label: "Techo-Bloc Blu 80", Value: "TB-BLU80"},
		},
	}
	if opt, ok := cl.Match("blu"); ok {
		t.Fatalf("an ambiguous reply resolved to %q, it must not", opt.Value)
	}
}

// -- (d) NEVER FABRICATE ------------------------------------------------------------------------

// TestNoReferentIsRefusedNotFabricated, MUTATION TARGET M3.
//
// "When's it arriving?" as the FIRST thing said in a conversation has no referent. The tempting
// implementation picks the most recent product from anywhere it can find one; the honest one says it
// does not know, and says what would fix it.
func TestNoReferentIsRefusedNotFabricated(t *testing.T) {
	d := seededDeep()
	deps := deepDeps(seededReaders(), d)
	tools := toolsFor(deps, frontline())
	r := deepResolver(d)

	res := r.Resolve(context.Background(), frontline(), tools, NewWindow(nil), "when's it arriving?")

	if res.Kind != ResolveNoReferent {
		t.Fatalf("an empty window must produce an honest refusal, got kind=%v calls=%v (mutation M3)", res.Kind, res.Calls)
	}
	if len(res.Calls) != 0 {
		t.Fatalf("nothing may run when there is no referent: %v", res.Calls)
	}
	low := strings.ToLower(res.Refusal)
	if !strings.Contains(low, "which product") {
		t.Fatalf("the refusal must name WHAT is missing: %q", res.Refusal)
	}
	if !strings.Contains(low, "tell me") && !strings.Contains(low, "name one") {
		t.Fatalf("the refusal must name what would fix it: %q", res.Refusal)
	}
}

// TestAConversationAboutStockDoesNotInventACustomer is the same rule across slots: a window full of
// products has no customer in it, and "what do they owe?" must not borrow one.
func TestAConversationAboutStockDoesNotInventACustomer(t *testing.T) {
	d := seededDeep()
	deps := deepDeps(seededReaders(), d)
	tools := toolsFor(deps, manager())
	r := deepResolver(d)

	w := NewWindow([]Message{
		{Role: RoleUser, Text: "stock for TB-BLU60"},
		turn("stock.lookup", Args{"product": "TB-BLU60"}),
	})
	res := r.Resolve(context.Background(), manager(), tools, w, "what do they owe us?")
	if res.Kind != ResolveNoReferent {
		t.Fatalf("no customer in the window, want a refusal, got kind=%v calls=%v", res.Kind, res.Calls)
	}
	if !strings.Contains(strings.ToLower(res.Refusal), "which customer") {
		t.Fatalf("the refusal must name the missing slot: %q", res.Refusal)
	}
}

// TestAnUnknownWordIsAdmittedNotAssigned: "what about Zorb?" where Zorb is neither a yard the caller
// can see nor a product. The word must not be quietly stuffed into the customer slot and run.
func TestAnUnknownWordIsAdmittedNotAssigned(t *testing.T) {
	d := seededDeep()
	d.catalog = nil
	deps := deepDeps(seededReaders(), d)
	tools := toolsFor(deps, frontline())
	r := deepResolver(d)

	w := NewWindow([]Message{
		{Role: RoleUser, Text: "stock for TB-BLU60"},
		turn("stock.lookup", Args{"product": "TB-BLU60"}),
	})
	res := r.Resolve(context.Background(), frontline(), tools, w, "what about Zorb?")
	if res.Kind != ResolveNoReferent {
		t.Fatalf("an unknown word must be admitted, got kind=%v calls=%v", res.Kind, res.Calls)
	}
	if !strings.Contains(res.Refusal, "Zorb") {
		t.Fatalf("the refusal must quote the word it could not place: %q", res.Refusal)
	}
}

// -- the fence the resolver reads through -------------------------------------------------------

// TestTheBranchVocabularyIsTheCallersOwn: the resolver decides "is Kingston a yard?" from a
// caller-scoped read. If it read an unfenced list, a scoped user could learn the names of yards they
// were never granted by watching which words the assistant recognised.
func TestTheBranchVocabularyIsTheCallersOwn(t *testing.T) {
	d := seededDeep()
	deps := deepDeps(seededReaders(), d)
	tools := toolsFor(deps, frontline())
	r := deepResolver(d)

	w := NewWindow([]Message{turn("stock.lookup", Args{"product": "TB-BLU60"})})
	_ = r.Resolve(context.Background(), frontline(), tools, w, "what about Kingston?")

	if d.gotCaller.UserID != frontline().UserID {
		t.Fatalf("the branch read ran as %q, not as the asking user", d.gotCaller.UserID)
	}
}

// TestAFollowUpNeverResolvesOntoACommandTheCallerLacks: a frontline user whose earlier turn somehow
// names ar.aging (a forged history, a demoted role) gets no resolution at all, the resolver stands
// down and the ordinary path answers, rather than producing a call that is then refused.
func TestAFollowUpNeverResolvesOntoACommandTheCallerLacks(t *testing.T) {
	d := seededDeep()
	deps := deepDeps(seededReaders(), d)
	tools := toolsFor(deps, frontline())
	r := deepResolver(d)

	w := NewWindow([]Message{turn("ar.aging", Args{})})
	res := r.Resolve(context.Background(), frontline(), tools, w, "and last week?")
	if res.Kind == ResolveCall || res.Kind == ResolveCompare {
		t.Fatalf("a frontline caller resolved onto a money command: %v", res.Calls)
	}
}

// -- the window itself --------------------------------------------------------------------------

func TestWindowIsBoundedServerSide(t *testing.T) {
	var msgs []Message
	for i := 0; i < 40; i++ {
		msgs = append(msgs, turn("stock.lookup", Args{"product": "P" + strings.Repeat("x", i)}))
	}
	w := NewWindow(msgs)
	if len(w.Turns) != WindowTurns {
		t.Fatalf("a 40-turn history must be trimmed to %d, got %d", WindowTurns, len(w.Turns))
	}
	// And the trim keeps the NEWEST turns: an implementation that kept the oldest would resolve
	// every follow-up against a conversation that ended half an hour ago.
	if got, _ := w.Entity("product"); !strings.HasSuffix(got, strings.Repeat("x", 39)) {
		t.Fatalf("the window kept the wrong end of the history: %q", got)
	}
}

func TestEmptyArgumentsNeverEraseTheEntityMemory(t *testing.T) {
	w := NewWindow([]Message{
		turn("stock.lookup", Args{"product": "TB-BLU60"}),
		turn("payments.today", Args{}),
		turn("ar.aging", Args{"customer": ""}),
	})
	got, ok := w.Entity("product")
	if !ok || got != "TB-BLU60" {
		t.Fatalf("an argument-less command erased the memory: %q ok=%v", got, ok)
	}
}

func TestAStaleClarifyIsNotLive(t *testing.T) {
	cl := &Clarify{Command: "stock.lookup", Arg: "product", Options: []ClarifyOption{{Label: "A", Value: "A"}}}
	w := NewWindow([]Message{
		{Role: RoleAssistant, Text: "which one?", Clarify: cl},
		{Role: RoleUser, Text: "actually, what came in today?"},
		turn("payments.today", Args{}),
	})
	if _, ok := w.PendingClarify(); ok {
		t.Fatalf("a clarify the person has already talked past must not still be live")
	}
}

// -- dates --------------------------------------------------------------------------------------

// TestTheWeekStartsOnMonday pins the one decision in dates.go that Go's own Weekday disagrees with.
func TestTheWeekStartsOnMonday(t *testing.T) {
	// Sunday 16 August 2026 belongs to the week that STARTED Monday 10 August.
	sunday := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	got := thisWeek(sunday)
	if got.From != "2026-08-10" {
		t.Fatalf("Sunday's week starts on the preceding Monday; got %s", got.From)
	}
	// Monday 17 August starts its own week.
	monday := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	if got := thisWeek(monday); got.From != "2026-08-17" {
		t.Fatalf("Monday starts its own week; got %s", got.From)
	}
}

func TestRangeLabelsNameTheDatesTheyMean(t *testing.T) {
	rng, ok := parseRange("and last week?", testNow)
	if !ok {
		t.Fatal("last week must parse")
	}
	if !strings.Contains(rng.Label, "10") || !strings.Contains(rng.Label, "16 Aug") {
		t.Fatalf("the label must name the window it resolved to, got %q", rng.Label)
	}
}

func TestAnUnreadableDateIsRefusedNotDefaulted(t *testing.T) {
	if _, ok := parseDay("last tuesday"); ok {
		t.Fatal("an unreadable date must not parse, defaulting it to today is the expensive kind of wrong")
	}
}

// TestAClarifyOptionNeverShowsTheCatalogueSInternalCode is the fourth defect the walkthrough found.
//
// The counter search ranks its hits and labels WHY each one matched with its own vocabulary -
// EXACT, SKU_PREFIX, NAME_PREFIX, NAME, DESCRIPTION, ALIAS, FUZZY (internal/product/counter_search.go).
// Those codes exist to explain a ranking to the code that does the ranking. The clarify builder
// passed one straight through as the option's hint, so the literal string "NAME_PREFIX" appeared
// under all five options in walkthrough-478/06-clarify-keyboard-focus.png.
//
// The hint is the ONLY thing telling five options apart when every label starts "Techo-Bloc", which
// makes it the worst possible place for a word out of a Go file.
func TestAClarifyOptionNeverShowsTheCatalogueSInternalCode(t *testing.T) {
	hits := []CatalogueHit{
		{ProductID: "p-1", SKU: "DBS-TB-BLU60", Name: "Techo-Bloc Blu 60 Slab", MatchReason: "NAME_PREFIX"},
		{ProductID: "p-2", SKU: "DBS-TB-HYDR", Name: "Techo-Bloc Hydr Eco Paver", MatchReason: "ALIAS"},
		{ProductID: "p-3", SKU: "DBS-TB-ABERDEEN", Name: "Techo-Bloc Aberdeen Wall", MatchReason: "FUZZY"},
	}
	cl := ambiguousProduct("Techo", hits, Invocation{Command: "stock.lookup"})
	if cl == nil || len(cl.Options) != 3 {
		t.Fatalf("want three options, got %+v", cl)
	}
	codes := []string{"NAME_PREFIX", "SKU_PREFIX", "DESCRIPTION", "ALIAS", "FUZZY", "EXACT", "NAME"}
	for _, o := range cl.Options {
		for _, code := range codes {
			if strings.Contains(o.Hint, code) {
				t.Errorf("option %q leaks the internal match code %q as its hint: %q", o.Label, code, o.Hint)
			}
		}
	}
	// And it must still SAY something, an empty hint under five near-identical labels is the other
	// way to fail this, and it fails silently.
	if !strings.Contains(cl.Options[0].Hint, "Techo") {
		t.Errorf("the hint must name what matched: %q", cl.Options[0].Hint)
	}
}

// TestAnUnknownMatchCodeSaysNothingRatherThanSomethingWrong: a code this function has never seen
// (a new rank added to the counter search) yields no second line, not a guess at what it means.
func TestAnUnknownMatchCodeSaysNothingRatherThanSomethingWrong(t *testing.T) {
	if got := matchReasonInWords("SOME_NEW_RANK", "Techo"); got != "" {
		t.Fatalf("an unknown code must produce no hint, got %q", got)
	}
	if got := matchReasonInWords("NAME_PREFIX", "Techo"); got == "" {
		t.Fatalf("a known code must produce one")
	}
}
