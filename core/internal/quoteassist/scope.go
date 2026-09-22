package quoteassist

// scope.go, DIB-67 (SP-04): quote completeness, evaluated deterministically over CONFIG ROWS.
//
// -----------------------------------------------------------------------------------------------
// ZERO AI. NOT "AI OFF BY DEFAULT", NONE.
// -----------------------------------------------------------------------------------------------
// The ticket calls this "AI quote completeness checking". There is no model in this file and no
// seam for one, because there is no question here a model would answer better. "A paver field needs
// jointing sand" is not an inference; it is the yard's installation knowledge, and the honest way
// to hold it is a row somebody can read, correct and switch off (migration 0159). Evaluate is a
// pure function of (rules, lines), same inputs, same findings, forever, with no key, no network
// and no token.
//
// -----------------------------------------------------------------------------------------------
// A GAP IS ADVICE, NOT A FAULT
// -----------------------------------------------------------------------------------------------
// The rep may know something the rule does not: the crew is supplying their own base, the porcelain
// is going on pedestals, the customer already has three pallets of sand in their yard. Every one of
// those is a NORMAL, CORRECT quote that this evaluator will flag. So:
//
//   - the surface renders findings as role="status", never role="alert" (the DIB-425 distinction,
//     gated by _quote-assist.test.mjs). Nothing is wrong. Nothing failed.
//   - every finding is dismissible, and dismissing one is not an admission of anything.
//   - a quote with no gaps renders NOTHING. Not a green tick, not "looks complete", a reassurance
//     printed on every quote is a thing people stop reading, and the day it matters they will not
//     read it either (the DIB-428 argument, which applies here word for word).
//
// -----------------------------------------------------------------------------------------------
// THE ONE PLACE `enabled` IS HONOURED
// -----------------------------------------------------------------------------------------------
// Here, in Evaluate, and nowhere else. RuleStore.Rules deliberately returns disabled rules too (the
// admin listing needs them), so there is exactly one line of code between an administrator's switch
// and a rep's screen. Delete it and TestScope_DisabledRuleProducesNoFinding fails by name, that is
// mutation M4 in NOTES-FIX-6667.md.

import "strings"

// ScopeRule is one row of quote_scope_rules.
//
// The two application sets are the rule's whole logic and the two strings are its whole voice.
// Nothing about a rule is computed: an evaluator that generated the sentence from the categories
// could not be made polite, and a suggestion that is not polite gets dismissed on sight.
type ScopeRule struct {
	Code   string `json:"code"`
	Title  string `json:"title"`
	Advice string `json:"advice"`
	// WhenApps: the quote has a line whose product application is any of these → the rule applies.
	WhenApps []string `json:"when_apps"`
	// ExpectApps: the quote must also have a line whose application is any of these.
	ExpectApps []string `json:"expect_apps"`
	// SuggestQuery is the search term the one-click add drops into the product picker. It is
	// allowed to match nothing, see the migration's note on SURFACE_NEEDS_EDGING.
	SuggestQuery string `json:"suggest_query"`
	Enabled      bool   `json:"enabled"`
	SortOrder    int    `json:"sort_order"`
}

// Finding is one gap, ready to render.
//
// It carries the rule's OWN title and advice, copied through unedited, and the lines that triggered
// it. TriggeredBy exists because "this quote is missing jointing sand" is an assertion a rep should
// be able to check in one glance: naming the paver lines that put the rule in play is the difference
// between advice and an oracle.
type Finding struct {
	RuleCode string `json:"rule_code"`
	Title    string `json:"title"`
	Advice   string `json:"advice"`
	// TriggeredBy names the lines whose application matched WhenApps, line numbers and product
	// names, so the panel can say "because of line 1, Techo-Bloc Blu 60 Slab".
	TriggeredBy []FindingLine `json:"triggered_by"`
	// Missing is the expected application set, upper-cased, for a surface that wants to say what
	// kind of thing is absent.
	Missing []string `json:"missing"`
	// SuggestQuery is the term the "add it" affordance searches for. Empty means the rule offers no
	// one-click add and the panel shows none, never a button that does nothing.
	SuggestQuery string `json:"suggest_query"`
}

// FindingLine is one triggering quote line.
type FindingLine struct {
	LineNo int    `json:"line_no"`
	Name   string `json:"name"`
}

// ScopeGaps is the whole answer for one quote.
//
// Empty is a first-class state twice over: a quote with no lines and a quote with no gaps both
// return zero findings, and neither is an error. RulesEvaluated is carried so the surface (and the
// admin) can tell "nothing to suggest" from "no rules are switched on", which look identical from
// the outside and mean completely different things.
type ScopeGaps struct {
	QuoteID  string    `json:"quote_id"`
	Findings []Finding `json:"findings"`
	// LinesChecked is how many quote lines were evaluated. Zero on an empty quote, and an empty
	// quote is why the panel stays silent rather than suggesting a base course for nothing.
	LinesChecked int `json:"lines_checked"`
	// RulesEvaluated is how many ENABLED rules ran. Zero means an administrator has switched them
	// all off, which is a legitimate configuration and is reported as one.
	RulesEvaluated int `json:"rules_evaluated"`
}

// normaliseApp is the ONE definition of how an application string is compared.
//
// products.application is free text, 'Paver', 'Jointing', or blank. It reads title-case in the seed
// and there is nothing stopping the next catalogue import from writing 'PAVER' or ' paver '. A rule
// set that quietly stopped firing after an import would be the worst possible failure mode for this
// feature, silent, invisible, and only discovered by a customer, so both sides of every comparison
// go through here.
func normaliseApp(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

// normaliseApps normalises a set, dropping blanks. A blank application is a product nobody
// classified; it can neither trigger a rule nor satisfy one, and treating it as a match would make
// every unclassified product satisfy every expectation.
func normaliseApps(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if n := normaliseApp(s); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// Evaluate reports the gaps in one quote's lines under one rule set.
//
// PURE. No context, no database, no clock, no I/O. That is what lets the whole of SP-04's behaviour
// be pinned by unit tests with no fixtures, and it is what makes "the rules are data" checkable:
// hand it an empty rule slice and it returns nothing, whatever the lines are
// (TestScope_NoRules_NoFindings). Under a hardcoded rule that test fails by name, mutation M3.
func Evaluate(rules []ScopeRule, lines []ScopeLine) []Finding {
	// The set of applications this quote already covers, and the lines behind each one.
	present := map[string][]FindingLine{}
	for _, l := range lines {
		app := normaliseApp(l.Application)
		if app == "" {
			continue
		}
		present[app] = append(present[app], FindingLine{LineNo: l.LineNo, Name: l.Name})
	}

	ordered := append([]ScopeRule(nil), rules...)
	SortRules(ordered)

	findings := make([]Finding, 0)
	for _, r := range ordered {
		// -- the one place `enabled` is honoured --
		if !r.Enabled {
			continue
		}
		trigger := triggeringLines(present, r.WhenApps)
		if len(trigger) == 0 {
			continue // the rule is not in play for this quote
		}
		if satisfied(present, r.ExpectApps) {
			continue // the complement is already on the quote
		}
		findings = append(findings, Finding{
			RuleCode: r.Code,
			// Copied through verbatim. A finding whose words were assembled here rather than read
			// from the row would make the admin listing a lie about what reps actually see -
			// TestScope_AdviceIsTheRowsOwnWords pins the byte equality.
			Title:        r.Title,
			Advice:       r.Advice,
			TriggeredBy:  trigger,
			Missing:      normaliseApps(r.ExpectApps),
			SuggestQuery: strings.TrimSpace(r.SuggestQuery),
		})
	}
	return findings
}

// triggeringLines returns the quote lines that put a rule in play, in line order.
func triggeringLines(present map[string][]FindingLine, whenApps []string) []FindingLine {
	seen := map[int]bool{}
	out := make([]FindingLine, 0, 2)
	for _, want := range normaliseApps(whenApps) {
		for _, l := range present[want] {
			if seen[l.LineNo] {
				continue
			}
			seen[l.LineNo] = true
			out = append(out, l)
		}
	}
	// Line order, because a finding that names "line 4, line 1" reads as a bug even when it is not.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].LineNo < out[j-1].LineNo; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// satisfied reports whether ANY of the expected applications is already on the quote.
//
// ANY, not ALL. ExpectApps is a set of ways to satisfy one need, SURFACE_NEEDS_EDGING accepts
// either EDGING or RESTRAINT because two catalogues spell the same product two ways, and reading
// it as "all of these must be present" would make a rule with two spellings impossible to satisfy.
func satisfied(present map[string][]FindingLine, expectApps []string) bool {
	for _, want := range normaliseApps(expectApps) {
		if len(present[want]) > 0 {
			return true
		}
	}
	return false
}

// CountEnabled is how many rules would actually run. Reported on the answer so "no suggestions"
// and "no rules switched on" are distinguishable from the outside.
func CountEnabled(rules []ScopeRule) int {
	n := 0
	for _, r := range rules {
		if r.Enabled {
			n++
		}
	}
	return n
}
