package quoteassist

// scope_test.go, DIB-67's named invariants.
//
// THE THREE THAT ARE LOAD-BEARING, and the mutation each one kills (NOTES-FIX-6667.md):
//
//	TestScope_NoRules_NoFindings              M3, a rule is hardcoded into the evaluator
//	TestScope_AdviceIsTheRowsOwnWords         M3, the sentence is generated instead of read
//	TestScope_DisabledRuleProducesNoFinding   M4, the `enabled` check is dropped
//
// Every test in this file runs against Evaluate, which is PURE: no context, no database, no clock.
// That is what makes "the rules are data" checkable rather than claimed, hand the evaluator no
// rules and it can have nothing to say, whatever is on the quote.

import (
	"context"
	"strings"
	"testing"
)

// A paver-only quote: the shape that should trip four of the seven seeded rules.
func paverOnlyQuote() []ScopeLine {
	return []ScopeLine{
		{LineNo: 1, Name: "Techo-Bloc Blu 60 Slab", Application: "Patio"},
		{LineNo: 2, Name: "Unilock Brussels Block", Application: "Paver"},
	}
}

// -- 1 · the rules ARE the data ------------------------------------------------------------------

// TestScope_NoRules_NoFindings is the whole "config, not hardcode" claim in four lines.
//
// A paver quote with every possible gap, and an EMPTY rule set. If any finding comes back, a rule
// lives in Go, which is mutation M3, and this is the test that dies under it.
func TestScope_NoRules_NoFindings(t *testing.T) {
	if got := Evaluate(nil, paverOnlyQuote()); len(got) != 0 {
		t.Fatalf("findings = %v with NO rules configured, a rule is hardcoded in the evaluator", findingCodes(got))
	}
	if got := Evaluate([]ScopeRule{}, paverOnlyQuote()); len(got) != 0 {
		t.Fatalf("findings = %v for an empty rule slice", findingCodes(got))
	}
}

// TestScope_AdviceIsTheRowsOwnWords: byte equality between the row and the finding.
//
// The temptation is to compose the sentence from the categories ("this quote has PAVER but no
// JOINTING"). It cannot be made polite, it cannot be corrected by the yard, and it makes the admin
// listing a lie about what reps actually see. So the finding copies the row, and this test pins it
// with a rule whose words could not possibly have been generated.
func TestScope_AdviceIsTheRowsOwnWords(t *testing.T) {
	rule := ScopeRule{
		Code:       "YARD_LOCAL_RULE",
		Title:      "Dave says always ask about the downspout",
		Advice:     "Ask where the downspout lands before you price the base, twice now we've had callbacks.",
		WhenApps:   []string{"PATIO"},
		ExpectApps: []string{"DRAINAGE"},
		Enabled:    true,
	}
	got := Evaluate([]ScopeRule{rule}, paverOnlyQuote())
	if len(got) != 1 {
		t.Fatalf("findings = %d, want 1", len(got))
	}
	if got[0].Title != rule.Title {
		t.Errorf("title = %q, want the row's own %q", got[0].Title, rule.Title)
	}
	if got[0].Advice != rule.Advice {
		t.Errorf("advice = %q, want the row's own %q", got[0].Advice, rule.Advice)
	}
	if got[0].RuleCode != rule.Code {
		t.Errorf("rule_code = %q, want %q", got[0].RuleCode, rule.Code)
	}
}

// TestScope_DisabledRuleProducesNoFinding is the admin's switch, end to end through the evaluator.
//
// Same quote, same rule, one boolean. Under mutation M4 (the `if !r.Enabled` line removed) the
// second half of this test fails by name, and the failure message is the whole argument for why the
// switch exists.
func TestScope_DisabledRuleProducesNoFinding(t *testing.T) {
	rules := seededRules()
	quote := paverOnlyQuote()

	before := findingCodes(Evaluate(rules, quote))
	if !contains(before, "SURFACE_NEEDS_JOINTING") {
		t.Fatalf("the jointing rule did not fire on a paver-only quote (%v), this test is measuring nothing", before)
	}

	// The yard sells pedestal-set porcelain and does not want to be told about jointing sand.
	for i := range rules {
		if rules[i].Code == "SURFACE_NEEDS_JOINTING" {
			rules[i].Enabled = false
		}
	}
	after := findingCodes(Evaluate(rules, quote))
	if contains(after, "SURFACE_NEEDS_JOINTING") {
		t.Fatalf("a DISABLED rule still produced a suggestion (%v), an administrator's switch does nothing", after)
	}
	// And the others are untouched: switching one rule off must not silence the panel.
	if len(after) != len(before)-1 {
		t.Errorf("findings went from %d to %d, disabling one rule changed more than one rule", len(before), len(after))
	}
}

// -- 2 · the domain behaviour --------------------------------------------------------------------

// TestScope_PaverQuoteMissingJointingSand is the ticket's own example, on the seeded rule set.
func TestScope_PaverQuoteMissingJointingSand(t *testing.T) {
	got := Evaluate(seededRules(), paverOnlyQuote())
	codes := findingCodes(got)
	// Four rules are in play for a bare paved surface and none of them is satisfied.
	want := []string{"SURFACE_NEEDS_BASE", "SURFACE_NEEDS_BEDDING", "SURFACE_NEEDS_JOINTING", "SURFACE_NEEDS_EDGING"}
	if len(codes) != len(want) {
		t.Fatalf("findings = %v, want %v", codes, want)
	}
	for i := range want {
		if codes[i] != want[i] {
			// Order is sort_order, and it is asserted because a suggestion list that reshuffles
			// between two reads of the same quote reads as a flickering bug.
			t.Errorf("finding %d = %q, want %q (sort_order must decide)", i, codes[i], want[i])
		}
	}
	// The jointing finding names the lines that put it in play, in line order.
	var jointing *Finding
	for i := range got {
		if got[i].RuleCode == "SURFACE_NEEDS_JOINTING" {
			jointing = &got[i]
		}
	}
	if jointing == nil {
		t.Fatal("no jointing finding")
	}
	if len(jointing.TriggeredBy) != 2 {
		t.Fatalf("triggered_by = %+v, want both paved lines", jointing.TriggeredBy)
	}
	if jointing.TriggeredBy[0].LineNo != 1 || jointing.TriggeredBy[1].LineNo != 2 {
		t.Errorf("triggered_by out of line order: %+v", jointing.TriggeredBy)
	}
	if jointing.TriggeredBy[0].Name != "Techo-Bloc Blu 60 Slab" {
		t.Errorf("triggered_by names %q, a finding must name the line the way the QUOTE names it", jointing.TriggeredBy[0].Name)
	}
	if jointing.SuggestQuery != "polymeric sand" {
		t.Errorf("suggest_query = %q, want the row's own term", jointing.SuggestQuery)
	}
}

// TestScope_SatisfiedRuleIsSilent: add the jointing sand and the suggestion goes away. Nothing else
// changes, the other three gaps are still real.
func TestScope_SatisfiedRuleIsSilent(t *testing.T) {
	lines := append(paverOnlyQuote(), ScopeLine{LineNo: 3, Name: "Alliance Gator Polymeric Sand", Application: "Jointing"})
	codes := findingCodes(Evaluate(seededRules(), lines))
	if contains(codes, "SURFACE_NEEDS_JOINTING") {
		t.Fatalf("the jointing suggestion survived the jointing sand: %v", codes)
	}
	for _, still := range []string{"SURFACE_NEEDS_BASE", "SURFACE_NEEDS_BEDDING", "SURFACE_NEEDS_EDGING"} {
		if !contains(codes, still) {
			t.Errorf("%s stopped firing when an unrelated line was added: %v", still, codes)
		}
	}
}

// TestScope_CompleteQuoteIsCompletelySilent, the whole panel renders nothing. Not a green tick: a
// reassurance printed on every quote is a thing people stop reading, and the day it matters they
// will not read it either.
func TestScope_CompleteQuoteIsCompletelySilent(t *testing.T) {
	lines := []ScopeLine{
		{LineNo: 1, Name: "Techo-Bloc Blu 60 Slab", Application: "Patio"},
		{LineNo: 2, Name: "Limestone Screenings", Application: "Base"},
		{LineNo: 3, Name: "HPB Clear Stone", Application: "Bedding"},
		{LineNo: 4, Name: "Alliance Gator Polymeric Sand", Application: "Jointing"},
		{LineNo: 5, Name: "Aluminium Edge Restraint", Application: "Edging"},
	}
	if got := Evaluate(seededRules(), lines); len(got) != 0 {
		t.Fatalf("a complete paver quote produced %v", findingCodes(got))
	}
}

// TestScope_EmptyQuoteIsSilent, an empty quote has no paved surface, so no rule is in play. The
// alternative (suggesting a base course for a quote with nothing on it) would put a suggestion panel
// in front of a rep before they had typed anything.
func TestScope_EmptyQuoteIsSilent(t *testing.T) {
	if got := Evaluate(seededRules(), nil); len(got) != 0 {
		t.Fatalf("an empty quote produced %v", findingCodes(got))
	}
	if got := Evaluate(seededRules(), []ScopeLine{}); len(got) != 0 {
		t.Fatalf("a zero-line quote produced %v", findingCodes(got))
	}
}

// TestScope_WallQuoteGetsWallRulesAndNotPaverRules, a retaining-wall quote must not be told about
// jointing sand. A completeness checker that flags irrelevant things is a checker people dismiss.
func TestScope_WallQuoteGetsWallRulesAndNotPaverRules(t *testing.T) {
	lines := []ScopeLine{{LineNo: 1, Name: "Techo-Bloc Mini-Creta Wall", Application: "Wall"}}
	codes := findingCodes(Evaluate(seededRules(), lines))
	want := []string{"WALL_NEEDS_BASE", "WALL_NEEDS_DRAINAGE"}
	if len(codes) != 2 || codes[0] != want[0] || codes[1] != want[1] {
		t.Fatalf("findings = %v, want %v", codes, want)
	}
}

// TestScope_ExpectSetIsSatisfiedByANY, SURFACE_NEEDS_EDGING accepts EDGING or RESTRAINT, because two
// catalogues spell one product two ways. Read as "all of these", the rule would be unsatisfiable.
func TestScope_ExpectSetIsSatisfiedByANY(t *testing.T) {
	base := paverOnlyQuote()
	for _, app := range []string{"Edging", "Restraint"} {
		lines := append(append([]ScopeLine(nil), base...), ScopeLine{LineNo: 3, Name: "some restraint", Application: app})
		if contains(findingCodes(Evaluate(seededRules(), lines)), "SURFACE_NEEDS_EDGING") {
			t.Errorf("application %q did not satisfy the edging rule", app)
		}
	}
}

// TestScope_ApplicationComparisonIsCaseAndSpaceInsensitive.
//
// products.application is free text. It reads 'Paver' in today's seed and there is nothing stopping
// the next catalogue import from writing ' PAVER '. A rule set that quietly stopped firing after an
// import would be this feature's worst failure mode: silent, invisible, and discovered by a customer.
func TestScope_ApplicationComparisonIsCaseAndSpaceInsensitive(t *testing.T) {
	for _, spelling := range []string{"Paver", "PAVER", "paver", "  Paver  "} {
		lines := []ScopeLine{{LineNo: 1, Name: "a paver", Application: spelling}}
		if !contains(findingCodes(Evaluate(seededRules(), lines)), "SURFACE_NEEDS_JOINTING") {
			t.Errorf("application %q did not trigger the jointing rule", spelling)
		}
	}
}

// TestScope_UnclassifiedLineNeitherTriggersNorSatisfies, a product nobody classified is not a
// wildcard. Treating a blank application as a match would make every unclassified product satisfy
// every expectation, which would silence the whole panel for a catalogue with gaps in it.
func TestScope_UnclassifiedLineNeitherTriggersNorSatisfies(t *testing.T) {
	if got := Evaluate(seededRules(), []ScopeLine{{LineNo: 1, Name: "Landed-Cost Demo Product 1", Application: ""}}); len(got) != 0 {
		t.Fatalf("an unclassified line triggered %v", findingCodes(got))
	}
	lines := append(paverOnlyQuote(), ScopeLine{LineNo: 3, Name: "mystery", Application: "  "})
	if !contains(findingCodes(Evaluate(seededRules(), lines)), "SURFACE_NEEDS_JOINTING") {
		t.Fatal("an unclassified line satisfied the jointing rule")
	}
}

// TestScope_FindingOrderIsDeterministicUnderEqualSortOrder, code breaks the tie, so two rules an
// admin gave the same sort_order do not swap places between reads.
func TestScope_FindingOrderIsDeterministicUnderEqualSortOrder(t *testing.T) {
	rules := []ScopeRule{
		{Code: "ZEBRA", Title: "z", Advice: "z", WhenApps: []string{"PAVER"}, ExpectApps: []string{"NOPE"}, Enabled: true, SortOrder: 5},
		{Code: "ALPHA", Title: "a", Advice: "a", WhenApps: []string{"PAVER"}, ExpectApps: []string{"NOPE"}, Enabled: true, SortOrder: 5},
	}
	for i := 0; i < 5; i++ {
		if got := findingCodes(Evaluate(rules, paverOnlyQuote())); got[0] != "ALPHA" || got[1] != "ZEBRA" {
			t.Fatalf("order = %v, want [ALPHA ZEBRA] every time", got)
		}
	}
}

// TestScope_EvaluateDoesNotMutateTheRulesItWasGiven, the sort happens on a copy, so a caller that
// reuses its rule slice (the admin listing does) does not see it re-ordered underneath it.
func TestScope_EvaluateDoesNotMutateTheRulesItWasGiven(t *testing.T) {
	rules := []ScopeRule{
		{Code: "B", Title: "b", Advice: "b", WhenApps: []string{"PAVER"}, ExpectApps: []string{"NOPE"}, Enabled: true, SortOrder: 9},
		{Code: "A", Title: "a", Advice: "a", WhenApps: []string{"PAVER"}, ExpectApps: []string{"NOPE"}, Enabled: true, SortOrder: 1},
	}
	_ = Evaluate(rules, paverOnlyQuote())
	if rules[0].Code != "B" {
		t.Fatalf("Evaluate re-ordered its caller's slice: %v", []string{rules[0].Code, rules[1].Code})
	}
}

// TestScope_CountEnabledDistinguishesQuietFromSwitchedOff, "no suggestions" and "no rules are on"
// look identical from the outside and mean completely different things.
func TestScope_CountEnabledDistinguishesQuietFromSwitchedOff(t *testing.T) {
	rules := seededRules()
	if got := CountEnabled(rules); got != 7 {
		t.Fatalf("CountEnabled = %d, want 7", got)
	}
	for i := range rules {
		rules[i].Enabled = false
	}
	if got := CountEnabled(rules); got != 0 {
		t.Fatalf("CountEnabled = %d with everything off, want 0", got)
	}
}

// -- 3 · the service around it -------------------------------------------------------------------

// TestScopeGaps_ReportsRulesEvaluatedSoSilenceIsExplainable.
func TestScopeGaps_ReportsRulesEvaluatedSoSilenceIsExplainable(t *testing.T) {
	lines := &fakeLines{lines: paverOnlyQuote(), found: true}
	rules := &fakeRules{rules: seededRules()}
	svc := NewService(nil, lines, rules, nil)

	got, found, err := svc.ScopeGaps(context.Background(), "q-1")
	if err != nil || !found {
		t.Fatalf("ScopeGaps: err=%v found=%v", err, found)
	}
	if got.LinesChecked != 2 {
		t.Errorf("lines_checked = %d, want 2", got.LinesChecked)
	}
	if got.RulesEvaluated != 7 {
		t.Errorf("rules_evaluated = %d, want 7", got.RulesEvaluated)
	}
	if got.QuoteID != "q-1" {
		t.Errorf("quote_id = %q", got.QuoteID)
	}
	if lines.gotID != "q-1" {
		t.Errorf("the quote id reaching the seam was %q", lines.gotID)
	}
}

// TestScopeGaps_MissingQuoteIsNotFoundNotAnError, one answer for "no such quote" and "not yours",
// because a distinguishable refusal is an existence oracle for a branch the caller cannot reach.
func TestScopeGaps_MissingQuoteIsNotFoundNotAnError(t *testing.T) {
	svc := NewService(nil, &fakeLines{found: false}, &fakeRules{rules: seededRules()}, nil)
	got, found, err := svc.ScopeGaps(context.Background(), "q-nope")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if found {
		t.Fatal("found = true for a quote the seam did not return")
	}
	if len(got.Findings) != 0 {
		t.Errorf("findings on a not-found quote: %v", findingCodes(got.Findings))
	}
}

// TestScopeGaps_UnwiredSeamsAre503Errors, a seam nobody connected is named, not crashed through.
func TestScopeGaps_UnwiredSeamsAre503Errors(t *testing.T) {
	if _, _, err := NewService(nil, nil, &fakeRules{}, nil).ScopeGaps(context.Background(), "q"); err != ErrQuoteLinesUnwired {
		t.Errorf("err = %v, want ErrQuoteLinesUnwired", err)
	}
	if _, _, err := NewService(nil, &fakeLines{found: true}, nil, nil).ScopeGaps(context.Background(), "q"); err != ErrRulesUnwired {
		t.Errorf("err = %v, want ErrRulesUnwired", err)
	}
}

// TestRules_ListingIsOrderedAndNeverNil, the admin listing shows disabled rules too (that is the
// point of a switch) and always as an array.
func TestRules_ListingIsOrderedAndNeverNil(t *testing.T) {
	shuffled := seededRules()
	shuffled[0], shuffled[6] = shuffled[6], shuffled[0]
	shuffled[2].Enabled = false
	svc := NewService(nil, nil, &fakeRules{rules: shuffled}, nil)

	got, err := svc.Rules(context.Background())
	if err != nil {
		t.Fatalf("Rules: %v", err)
	}
	if len(got) != 7 {
		t.Fatalf("rules = %d, want all 7 including the disabled one", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].SortOrder > got[i].SortOrder {
			t.Fatalf("listing is not ordered: %v", ruleCodes(got))
		}
	}
	disabled := 0
	for _, r := range got {
		if !r.Enabled {
			disabled++
		}
	}
	if disabled != 1 {
		t.Errorf("disabled rules in the listing = %d, want 1, an admin cannot switch back on what they cannot see", disabled)
	}

	empty, err := NewService(nil, nil, &fakeRules{}, nil).Rules(context.Background())
	if err != nil {
		t.Fatalf("Rules on an empty table: %v", err)
	}
	if empty == nil {
		t.Error("an empty rule table must list as [] and never as null")
	}
}

// TestSetRuleEnabled_StampsTheActorAndRefusesABlankCode.
func TestSetRuleEnabled_StampsTheActorAndRefusesABlankCode(t *testing.T) {
	rules := &fakeRules{rules: seededRules()}
	svc := NewService(nil, nil, rules, nil)

	got, err := svc.SetRuleEnabled(context.Background(), "SURFACE_NEEDS_JOINTING", false, "user-admin")
	if err != nil {
		t.Fatalf("SetRuleEnabled: %v", err)
	}
	if got.Enabled {
		t.Error("the returned rule still reads enabled")
	}
	if rules.setActor != "user-admin" {
		t.Errorf("actor reaching the store = %q, a config change is somebody's decision", rules.setActor)
	}
	if _, err := svc.SetRuleEnabled(context.Background(), "  ", true, "user-admin"); err != ErrRuleNotFound {
		t.Errorf("err = %v, want ErrRuleNotFound for a blank code", err)
	}
	if _, err := svc.SetRuleEnabled(context.Background(), "NOPE", true, "user-admin"); err != ErrRuleNotFound {
		t.Errorf("err = %v, want ErrRuleNotFound for an unknown code", err)
	}
}

// -- helpers ------------------------------------------------------------------------------------

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

func ruleCodes(rs []ScopeRule) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Code+"/"+strings.TrimSpace(itoa(r.SortOrder)))
	}
	return out
}
