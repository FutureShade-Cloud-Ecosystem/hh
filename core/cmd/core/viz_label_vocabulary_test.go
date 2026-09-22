package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dashboard"
)

// viz_label_vocabulary_test.go, THE THIRD DOOR from our database vocabulary to a dealer's screen.
//
// Item 3 closed two. This is the one it could not see:
//
//	app/src        an enum interpolated into a template   -> _counter-vocabulary.test.mjs (zero-tolerance)
//	the database   an enum stored as a row                -> 0199 + gate/stored_copy_vocabulary
//	A SERIES LABEL an enum SELECTed AS the label          -> nothing, until this file
//
// `SELECT status::text, COUNT(*) … GROUP BY status` makes the column's own value the point's LABEL,
// and a chart draws it verbatim. Home's deliveries widget drew a bar labelled "READY"; the
// open-orders split drew "PARTIALLY_FULFILLED". No client gate could see it, the words are not in
// app/src, they arrive over the wire, and no Go gate could either, because nothing was
// interpolating an enum into a sentence. The enum simply WAS the answer.
//
// Found by driving the real Home in a browser, which is the only place it was visible.

// A GROUP BY over a bare enum column, as the label. Deliberately narrow: it matches a SELECT whose
// FIRST projected column is a bare `<col>::text`, which is exactly the shape that makes a column
// the label, and not a join that projects a name (`l.code::text` is a branch code, already a word).
var rawEnumLabelSQL = regexp.MustCompile(`SELECT\s+(status|phase|state|kind|priority|direction|move_type|reason_code|commit_state)::text`)

// The four label maps this file's subject declares, by the constant name each adapter passes.
var wordedLabelMaps = []string{
	"vizStopStatusWords", "vizTaskStatusWords", "vizOrderStatusWords", "vizClaimPhaseWords",
}

func vizAdapterSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("viz_adapters.go")
	if err != nil {
		t.Fatalf("cannot read viz_adapters.go: %v", err)
	}
	return string(b)
}

// EVERY query that makes an enum column its label must be run through vizWordedPoints. The scan is
// per-QUERY rather than per-file, so a fifth adapter added next year is covered without anybody
// remembering this test exists.
func TestVizLabels_EveryEnumLabelledSeriesIsWorded(t *testing.T) {
	src := vizAdapterSource(t)

	// GUARD THE GUARD, twice. The regex must still find the shape it was written for, and the file
	// must still be the file, an empty read or a renamed helper passes every assertion below.
	raw := rawEnumLabelSQL.FindAllString(src, -1)
	if len(raw) < 4 {
		t.Fatalf("found %d enum-labelled queries; there were four when this gate was written. Either "+
			"the scan has drifted or the queries moved, check before assuming this is good news", len(raw))
	}
	if !strings.Contains(src, "func vizWordedPoints(") {
		t.Fatal("vizWordedPoints is gone, the words have no door to come through")
	}

	// Every `const byStatus = ` query in the file is a labelled series. Count how many are consumed
	// by the worded helper against how many are consumed at all.
	worded := strings.Count(src, "vizWordedPoints(ctx, pool, byStatus")
	bare := strings.Count(src, "vizPoints(ctx, pool, byStatus")
	if bare != 0 {
		t.Errorf("%d status-split series still call vizPoints directly, so their bars are labelled "+
			"with the database column. Route them through vizWordedPoints with a word map:\n%s",
			bare, "  grep -n 'vizPoints(ctx, pool, byStatus' backend/cmd/server/viz_adapters.go")
	}
	if worded < 4 {
		t.Errorf("only %d status-split series are worded; %d enum-labelled queries exist", worded, len(raw))
	}
}

// A word map that is missing a value its CHECK admits is worse than no map: the mapped values read
// as words and the one that fell through reads as a database column, in the same column of the same
// chart, so it looks like a data error rather than a missing translation.
func TestVizLabels_TheMapsAreCompleteAndTheDefaultArmIsHonest(t *testing.T) {
	for name, m := range map[string]map[string]vizState{
		"vizStopStatusWords":  vizStopStatusWords,
		"vizTaskStatusWords":  vizTaskStatusWords,
		"vizOrderStatusWords": vizOrderStatusWords,
		"vizClaimPhaseWords":  vizClaimPhaseWords,
	} {
		if len(m) < 6 {
			t.Errorf("%s has %d entries, too few to be complete against any of these CHECK sets", name, len(m))
		}
		for raw, st := range m {
			word := st.Word
			if word == "" {
				t.Errorf("%s[%q] is empty", name, raw)
			}
			if strings.Contains(word, "_") {
				t.Errorf("%s[%q] = %q still carries an underscore", name, raw, word)
			}
			if word == strings.ToUpper(word) && len(word) > 3 {
				t.Errorf("%s[%q] = %q is still shouting", name, raw, word)
			}
		}
	}

	// The four values the browser actually caught, by name, so this test fails for the reason it was
	// written rather than for a shape.
	if got := vizStateOf("READY", vizStopStatusWords).Word; got != "Ready" {
		t.Errorf(`a delivery bar labelled "READY" is what started this: got %q`, got)
	}
	if got := vizStateOf("PARTIALLY_FULFILLED", vizOrderStatusWords).Word; got != "Part filled" {
		t.Errorf(`orders split by status drew "PARTIALLY_FULFILLED": got %q`, got)
	}
	if got := vizStateOf("WAITING_ON_DECISION", vizClaimPhaseWords).Word; got != "Awaiting decision" {
		t.Errorf("got %q", got)
	}
	if got := vizStateOf("IN_PROGRESS", vizTaskStatusWords).Word; got != "Being picked" {
		t.Errorf("got %q", got)
	}

	// AND THE DEFAULT ARM. A value added to a CHECK later must read as strange, never as SHOUTING and
	// never as a confident wrong word.
	if got := vizStateOf("SOME_NEW_STATE", vizStopStatusWords).Word; got != "some new state" {
		t.Fatalf("the default arm returned %q, it must lowercase and de-underscore, inventing nothing", got)
	}
	if got := vizStateOf("", vizStopStatusWords).Word; got != "" {
		t.Fatalf("an empty value produced %q", got)
	}
}

// -- the severity half: a status board is toned by what the status MEANS -------------------------
//
// Ruled 1 Sep 2026. boardTone painted a row by (value/max) against the doctrine's 80/95 thresholds,
// and the fullest bucket is 100% BY CONSTRUCTION, so the largest bucket on every status board came
// out `stop` and labelled Urgent whatever it was. Measured before the fix: buckets of 9/4/1 gave
// stop/good/good, one bucket gave stop, two equal buckets gave both stop.

// THE RESTRAINT IS THE RULE. A board painted a colour per row says nothing, and the screen doctrine
// reserves good/watch/stop for state. If this ratio ever drifts toward "most things are coloured",
// the boards have stopped meaning anything and this test is where that shows up.
func TestVizSeverity_ALMOST_EVERYTHING_IS_NEUTRAL(t *testing.T) {
	maps := map[string]map[string]vizState{
		"vizStopStatusWords":  vizStopStatusWords,
		"vizTaskStatusWords":  vizTaskStatusWords,
		"vizOrderStatusWords": vizOrderStatusWords,
		"vizClaimPhaseWords":  vizClaimPhaseWords,
	}
	total, toned := 0, 0
	for name, m := range maps {
		for raw, st := range m {
			total++
			if st.Tone == "" {
				continue
			}
			toned++
			switch st.Tone {
			case dashboard.ToneGood, dashboard.ToneWatch, dashboard.ToneStop:
			default:
				t.Errorf("%s[%q] carries tone %q, which is not one of the three", name, raw, st.Tone)
			}
		}
	}
	if total < 25 {
		t.Fatalf("the four maps hold %d states between them; there were 31 when this gate was "+
			"written, the scan has drifted and the ratio below means nothing", total)
	}
	if toned*3 > total {
		t.Errorf("%d of %d states carry a colour. More than a third of a board being coloured is a "+
			"board that says nothing, the doctrine reserves good/watch/stop for STATE, and most "+
			"states are stages", toned, total)
	}
	if toned == 0 {
		t.Error("no state carries a tone at all, then a failed delivery looks like a loaded one")
	}
}

// The four judgements this ruling actually turns on, by name, so the test fails for its reason.
func TestVizSeverity_TheStatesThatEarnAColour(t *testing.T) {
	for _, tc := range []struct {
		name string
		m    map[string]vizState
		raw  string
		want string
		why  string
	}{
		{"stops", vizStopStatusWords, "FAILED", dashboard.ToneStop,
			"a stop that could not be delivered is the one thing on that board somebody must pick up today"},
		{"stops", vizStopStatusWords, "READY", "",
			"READY is where a stop IS, not a problem, and it was the bucket painted red before this ruling"},
		{"stops", vizStopStatusWords, "EN_ROUTE", "",
			"the state you want MOST of must never read as an alarm"},
		{"claims", vizClaimPhaseWords, "WAITING_ON_DECISION", dashboard.ToneWatch,
			"the one phase where nobody here can move it, it is what a dealer chases"},
		{"claims", vizClaimPhaseWords, "REJECTED", dashboard.ToneWatch,
			"money the yard now eats: worth reading, not an emergency"},
		{"orders", vizOrderStatusWords, "PARTIALLY_FULFILLED", "",
			"part-filling is how hardscape supply works when a pallet is short; flagging it would colour half the board"},
		{"tasks", vizTaskStatusWords, "QUEUED", "",
			"a pick queue's severity is age and depth, never which stage a task is at"},
		{"tasks", vizTaskStatusWords, "IN_PROGRESS", "",
			"being picked is the queue working"},
	} {
		if got := tc.m[tc.raw].Tone; got != tc.want {
			t.Errorf("%s[%s] tone = %q, want %q, %s", tc.name, tc.raw, got, tc.want, tc.why)
		}
	}
	// THE WHOLE PICK BOARD IS NEUTRAL, and that is the answer rather than an oversight: nothing in
	// which STAGE a task sits tells a yard hand anything is wrong.
	for raw, st := range vizTaskStatusWords {
		if st.Tone != "" {
			t.Errorf("vizTaskStatusWords[%q] is %q; no pick STAGE is a severity, a queue's severity is "+
				"its COUNT and its AGE, which is what needs.go SeverityFor scores", raw, st.Tone)
		}
	}
}

// NO STATE IS GREEN, and this is the assertion that keeps it that way.
//
// The first cut marked DELIVERED, DONE and both CLOSEDs `good` on the reading that terminal success
// deserves a colour. Every one failed the file's own test, nobody in a yard does anything different
// because a bar is green, every one sat on a value its own query excludes, so all four were dead
// code; and `DONE: good` directly contradicted the yard console, which paints the SAME enum value of
// the SAME column the other way (yard-logic.ts statusTone: STAGED is ready, DONE is dim).
func TestVizSeverity_NoStateIsGreenAndOnlyThreeCarryAColourAtAll(t *testing.T) {
	maps := map[string]map[string]vizState{
		"stop": vizStopStatusWords, "task": vizTaskStatusWords,
		"order": vizOrderStatusWords, "claim": vizClaimPhaseWords,
	}
	got := map[string]string{}
	for name, m := range maps {
		for raw, st := range m {
			if st.Tone == dashboard.ToneGood {
				t.Errorf("%s[%q] is green. Terminal success is not something anybody acts on, and a "+
					"board that goes mostly-green says as little as one that goes mostly-red. `good` "+
					"belongs to the Needs-you queue, which scores on count and age.", name, raw)
			}
			if st.Tone != "" {
				got[name+"."+raw] = st.Tone
			}
		}
	}
	// EXHAUSTIVE, so this fails when a colour is ADDED anywhere, not only when a green one is.
	want := map[string]string{
		"stop.FAILED":               dashboard.ToneStop,
		"claim.WAITING_ON_DECISION": dashboard.ToneWatch,
		"claim.REJECTED":            dashboard.ToneWatch,
	}
	if len(got) != len(want) {
		t.Fatalf("%d states carry a colour, want exactly %d: %v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

// THE DEFAULT ARM INVENTS NOTHING. A state added to a CHECK next year must reach a screen as a
// strange word and NO colour, never as an alarm nobody chose, and never as a reassuring green.
func TestVizSeverity_AnUnknownStateIsNeutralAndSaysSo(t *testing.T) {
	st := vizStateOf("SOME_NEW_STATE", vizStopStatusWords)
	if st.Word != "some new state" {
		t.Errorf("word = %q", st.Word)
	}
	if st.Tone != "" {
		t.Fatalf("an unmapped state was given tone %q, a severity must never be invented", st.Tone)
	}
	if st := vizStateOf("", vizStopStatusWords); st.Word != "" || st.Tone != "" {
		t.Fatalf("an empty state produced %+v", st)
	}
	// And a nil map is the same answer, not a panic.
	if st := vizStateOf("READY", nil); st.Word != "ready" || st.Tone != "" {
		t.Fatalf("nil map produced %+v", st)
	}
}

// A COLOUR CAN NEVER TRAVEL WITHOUT THE WORD THAT NAMED IT, and on a status board that is
// structural rather than a rule somebody has to remember: the label and the tone are read out of
// the SAME map entry in the same statement, so a bar painted for FAILED is also labelled "Failed".
//
// This matters because the screen doctrine reserves colour for state and requires a word or an icon
// beside it, never colour alone. A capacity bar has to satisfy that by adding a severity word,
// because its label is a truck id; a status bar satisfies it by construction, because its label IS
// the state. This test is what stops the two halves being separated later.
func TestVizSeverity_TheWordAndTheColourComeFromTheSameEntry(t *testing.T) {
	src := vizAdapterSource(t)
	if !strings.Contains(src, "points[i].Label, points[i].Tone = st.Word, st.Tone") {
		t.Fatal("the label and the tone are no longer assigned from one vizState. If they are read " +
			"separately, a bar can be painted for a state whose word it does not carry, which is " +
			"colour alone, and the doctrine forbids it")
	}
	// Every state that carries a colour must also carry a word, or the bar is coloured and unnamed.
	for name, m := range map[string]map[string]vizState{
		"stop": vizStopStatusWords, "task": vizTaskStatusWords,
		"order": vizOrderStatusWords, "claim": vizClaimPhaseWords,
	} {
		for raw, st := range m {
			if st.Tone != "" && strings.TrimSpace(st.Word) == "" {
				t.Errorf("%s[%q] carries tone %q and no word", name, raw, st.Tone)
			}
		}
	}
}

// EVERY MAP MUST BE COMPLETE AGAINST ITS CHECK, read out of the migration rather than remembered.
//
// The file has claimed completeness in a comment since item 3 and nothing enforced it: the existing
// gate asserts only `len(m) >= 6`. All four maps happen to be complete, so this is a gap rather than
// a defect, but a tenth order status added backend-first would ship a bar labelled `some new state`
// with no colour and no test would fail, which is exactly the class of thing item 3 existed to close.
func TestVizLabels_EveryMapIsCompleteAgainstItsCHECK(t *testing.T) {
	// EACH CASE IS ANCHORED BY A SENTINEL, because a migration can define several CHECKs on a column
	// called `status` and the first one is not usually the one you want: 0008 defines quotes.status
	// (SENT/ACCEPTED/CONVERTED/EXPIRED) before orders.status, and taking the first match asserted the
	// order map was missing four quote states. The sentinel is a value unique to the intended set, so
	// the gate names the constraint it means rather than counting on file order.
	//
	// The sentinel has to be unique to the intended set, not merely present in it: 0053 carries the
	// claims CHECK (nine phases) AND the checklist-items CHECK (the six non-terminal ones), and both
	// contain WAITING_ON_DECISION. ABANDONED appears only in the first.
	for _, tc := range []struct {
		name, migration, column, sentinel string
		m                                 map[string]vizState
	}{
		{"vizStopStatusWords", "0090_dispatch_core.sql", "status", "EN_ROUTE", vizStopStatusWords},
		{"vizTaskStatusWords", "0095_yard_pick_stage.sql", "status", "STAGED", vizTaskStatusWords},
		{"vizOrderStatusWords", "0008_quotes_orders.sql", "status", "PARTIALLY_FULFILLED", vizOrderStatusWords},
		{"vizClaimPhaseWords", "0053_warranty_claims.sql", "phase", "ABANDONED", vizClaimPhaseWords},
	} {
		b, err := os.ReadFile("../../db/migrations/" + tc.migration)
		if err != nil {
			t.Errorf("%s: cannot read %s: %v", tc.name, tc.migration, err)
			continue
		}
		// Flattened, because every one of these sets is written across two lines.
		flat := regexp.MustCompile(`\s+`).ReplaceAllString(string(b), " ")
		re := regexp.MustCompile(`CHECK \(` + tc.column + ` IN \(([^)]*)\)\)`)
		var checked []string
		for _, m := range re.FindAllStringSubmatch(flat, -1) {
			var vals []string
			for _, v := range regexp.MustCompile(`'([A-Z_]+)'`).FindAllStringSubmatch(m[1], -1) {
				vals = append(vals, v[1])
			}
			for _, v := range vals {
				if v == tc.sentinel {
					checked = vals
				}
			}
		}
		if len(checked) == 0 {
			t.Errorf("%s: no `CHECK (%s IN (...))` in %s contains %q, this gate has gone blind to "+
				"the constraint it is about", tc.name, tc.column, tc.migration, tc.sentinel)
			continue
		}
		for _, v := range checked {
			if _, ok := tc.m[v]; !ok {
				t.Errorf("%s has no entry for %q, which %s admits. It would reach a screen through "+
					"the default arm as a lowercased enum with no colour", tc.name, v, tc.migration)
			}
		}
		if len(tc.m) != len(checked) {
			t.Errorf("%s holds %d entries and %s admits %d (%v), a map WIDER than its CHECK is words "+
				"for states that cannot happen", tc.name, len(tc.m), tc.migration, len(checked), checked)
		}
	}
}
