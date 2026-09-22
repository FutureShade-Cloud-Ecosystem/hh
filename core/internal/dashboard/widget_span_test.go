package dashboard

// widget_span_test.go, the v3.5 grid: the x3 mapping that made 0207 free, and the free span.
//
// THE ONE FACT THIS FILE EXISTS TO PROTECT is the ratio. Home v3.5 re-ruled the bento from four
// columns to twelve, and it cost no data migration for exactly one reason: the new preset table is
// the old one scaled by three, and a stored row holds a LETTER rather than a span. Break the ratio
// and nothing fails loudly, every Home in the building silently re-proportions, thirty seeded role
// defaults and every saved personal layout at once, and the first report is somebody saying their
// screen looks wrong.

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

// -- the x3 mapping ------------------------------------------------------------------------------

// preV35Span is the FOUR-COLUMN grid, frozen as history. It is written out rather than derived
// because it is the thing being compared against: deriving it from SpanOf would make this test
// assert that a function equals itself.
var preV35Span = map[string][2]int{
	SizeS:  {1, 1},
	SizeM:  {2, 1},
	SizeL:  {2, 2},
	SizeXL: {4, 1},
}

// TestTheX3MappingMovesEverySeededDefaultWithoutLoss is the load-bearing gate of the whole wave.
//
// It reads the THIRTY role defaults 0201 seeds, pins the count on both sides of the remap, and
// asserts that every one of them lands on the twelve-column grid at the identical proportion it held
// on the four-column one, same fraction of the width, same number of rows.
//
// COUNT PINNED BEFORE AND AFTER, and the "after" is not a second reading of the same list: it counts
// the rows that came out of the mapping with a real span. A row whose size no longer resolves would
// map to 0x0 and be a widget with no footprint, which is how a layout loses a card without anybody
// deleting one. 0207 is an UPDATE of preset letters and touches no key, so the two counts are equal
// by construction, and this is what makes that sentence checkable rather than a claim in a header.
func TestTheX3MappingMovesEverySeededDefaultWithoutLoss(t *testing.T) {
	seeded := seededWidgets(t)
	const before = 30 // 0201 §4: admin 9 · manager 8 · yard 7 · counter 6
	if len(seeded) != before {
		t.Fatalf("0201 seeds %d role defaults; it seeded %d when this gate was written, if a row was "+
			"added on purpose, raise the pin and say so here", len(seeded), before)
	}

	after := 0
	for _, w := range seeded {
		old, known := preV35Span[w.size]
		if !known {
			// 0201's own literals are frozen, a committed migration never moves, so every size in
			// that file is one of the original four, forever. A fifth would mean the parse drifted.
			t.Fatalf("0201 seeds size %q, which the four-column grid never had: %+v", w.size, w)
		}
		cols, rows := SpanOf(w.size)
		if cols == 0 || rows == 0 {
			t.Fatalf("%s/%s (size %q) has NO span on the v3.5 grid, the remap lost it", w.profile, w.key, w.size)
		}
		if cols != old[0]*3 || rows != old[1] {
			t.Fatalf("%s/%s (size %q) was %dx%d and is %dx%d; the x3 map wants %dx%d, every stored "+
				"layout just changed proportion", w.profile, w.key, w.size,
				old[0], old[1], cols, rows, old[0]*3, old[1])
		}
		// The proportion itself, stated the way a person sees it: a card that was a quarter of the
		// width is still a quarter of the width.
		if cols*4 != old[0]*12 {
			t.Fatalf("%s/%s occupies %d/12 of the width and used to occupy %d/4", w.profile, w.key, cols, old[0])
		}
		after++
	}
	if after != before {
		t.Fatalf("%d role defaults went into the remap and %d came out with a footprint", before, after)
	}
}

// TestMigration0207ChangesPresetLettersAndNeverTheKeySet is the other half of "without loss", asked
// of the migration TEXT rather than of the arithmetic.
//
// The x3 map is only free if 0207 does not also add, remove or re-file rows. A migration that
// re-authored a layout by deleting two widgets and inserting three would keep every assertion above
// true and still lose somebody a card, so the file is read and held to what its header promises: an
// UPDATE of preset letters, one row, nothing else.
func TestMigration0207ChangesPresetLettersAndNeverTheKeySet(t *testing.T) {
	b, err := os.ReadFile("../../db/migrations/0207_widget_free_spans.sql")
	if err != nil {
		t.Fatalf("0207 must be readable from the package: %v", err)
	}
	sql := string(b)
	// Statements only: the header argues ABOUT inserts and deletes at length, and a scan that could
	// not tell the explanation from the defect would force the argument to go unwritten. Same ruling
	// window_test.go makes about parsing without comments, for the same reason.
	statements := regexp.MustCompile(`(?m)^\s*--.*$`).ReplaceAllString(sql, "")
	upper := strings.ToUpper(statements)
	for _, forbidden := range []string{"INSERT INTO DASHBOARD_PROFILE_WIDGETS", "DELETE FROM DASHBOARD_PROFILE_WIDGETS",
		"INSERT INTO DASHBOARD_USER_WIDGETS", "DELETE FROM DASHBOARD_USER_WIDGETS", "DROP COLUMN"} {
		if strings.Contains(upper, forbidden) {
			t.Fatalf("0207 contains %q, the wave's whole claim is that no row moves and no column goes", forbidden)
		}
	}
	// NOT ONE ROW OF THE PERSONAL LAYER IS WRITTEN. A person who arranged their own Home chose those
	// sizes, and a migration that re-sized them because the grid got wider would be doing to them what
	// 0191's reset did, changing a choice and leaving no way to tell.
	if strings.Contains(upper, "UPDATE DASHBOARD_USER_WIDGETS") {
		t.Fatal("0207 updates somebody's personal layout; the re-author is role defaults only")
	}
	// The one re-authored row, named outright so a second one cannot be slipped in beside it.
	updates := regexp.MustCompile(`(?is)UPDATE\s+dashboard_profile_widgets`).FindAllString(statements, -1)
	if len(updates) != 1 {
		t.Fatalf("0207 re-authors role defaults in %d statements; the header says one", len(updates))
	}
	for _, want := range []string{"'yard'", "'picks.gauge'", "size = 'T'"} {
		if !strings.Contains(statements, want) {
			t.Fatalf("0207's re-author does not mention %s, it is addressing a row this build does not have", want)
		}
	}
	// And the widening it exists for, both tables.
	for _, want := range []string{
		"dashboard_profile_widgets_size_check", "dashboard_user_widgets_size_check",
		"'S', 'T', 'M', 'L', 'XL'",
	} {
		if !strings.Contains(statements, want) {
			t.Fatalf("0207 never names %s, the sixth preset cannot be stored", want)
		}
	}
}

// TestTheReAuthoredYardGaugeIsALegalWidgetInThisBuild guards the one row 0207 changes.
//
// The committed seed test reads 0201's literals, which are frozen history and still say 'S'. Nothing
// checked the row a dealer ACTUALLY has after 0207 runs, which is the only one a login reads, so
// this asserts the re-authored value against the catalog the same way, and against the doctrine's
// stated reason for the preset (a gauge at Tall renders as a ring; the flat meter is the S form).
func TestTheReAuthoredYardGaugeIsALegalWidgetInThisBuild(t *testing.T) {
	rows := seededWidgets(t)
	var gauge *seededWidget
	for i := range rows {
		if rows[i].profile == ProfileYard && rows[i].key == "picks.gauge" {
			gauge = &rows[i]
		}
	}
	if gauge == nil {
		t.Fatal("0201 no longer seeds yard/picks.gauge, 0207 re-authors a row that is not there")
	}
	// THE ROW AS THE MIGRATIONS LEAVE IT, WHICH IS NOT 0201's LITERAL. A committed migration never
	// moves, so 0201 still says time_range 'today' for this row and always will; 0205 corrected it to
	// the measure's own window and 0207 changes only the size. The state a dealer actually has is all
	// three applied, and only that state is worth asserting about, the same ruling
	// TestMigration0205AgreesWithTheCatalogMeasureForMeasure makes one file over.
	after := Widget{Key: gauge.key, Shape: gauge.shape, Measure: gauge.measure, Split: gauge.split,
		Range: naturalWindow(gauge.measure), Size: SizeT, Position: gauge.position, Visible: true}
	if !IsWidgetSize(after.Size) {
		t.Fatalf("the re-authored size %q is outside the closed set", after.Size)
	}
	if err := ValidateWidgets([]Widget{after}, MetricByID, NoBranch); err != nil {
		t.Fatalf("0207 leaves yard/picks.gauge in a state this build refuses: %v", err)
	}
	if c, r := SpanFor(after); c != 3 || r != 2 {
		t.Fatalf("Tall is 3x2; the re-authored gauge is %dx%d", c, r)
	}
	// It is still a GAUGE, which is the entire argument for moving it. A re-author that changed the
	// shape would be a redesign wearing the migration's clothes.
	if after.Shape != WidgetGauge {
		t.Fatalf("the re-authored row is a %q; the preset was taken up because it is a gauge", after.Shape)
	}
}

// -- the free span -------------------------------------------------------------------------------

func TestTheSnapSetIsTheDivisorsOfTwelveThatSurviveSixColumns(t *testing.T) {
	for _, c := range WidgetColSet {
		if 12%c != 0 && c != 8 && c != 9 {
			t.Fatalf("%d columns leaves a remainder no other card in the vocabulary fits", c)
		}
	}
	for _, off := range []int{1, 2, 5, 7, 10, 11, 13} {
		if IsWidgetSpan(off, 1) {
			t.Fatalf("%d columns is off the snap set and must not be storable", off)
		}
	}
	for _, r := range []int{1, 2, 3} {
		if !IsWidgetSpan(6, r) {
			t.Fatalf("6x%d is on the grid", r)
		}
	}
	for _, r := range []int{4, 5, 99} {
		if IsWidgetSpan(6, r) {
			t.Fatalf("6x%d is %d rows of one card, which is a page", r, r)
		}
	}
	// ZERO/ZERO IS THE COMMON CASE and always legal: "the preset decides" is what every row in both
	// tables carries today. A gate that refused it would refuse every widget the product ships, which
	// is precisely the trap IsBranchID documents about the empty string.
	if !IsWidgetSpan(0, 0) {
		t.Fatal("0x0 is 'the preset decides' and is what every shipped row carries")
	}
	// Half a pair is not a narrower answer, it is an incomplete one.
	if IsWidgetSpan(6, 0) || IsWidgetSpan(0, 2) {
		t.Fatal("half a span must not be storable, the preset already answers both halves")
	}
}

// TestAHalfSpanIsClearedToThePresetRatherThanRefused is the OMISSION half of the split, and it is the
// half that protects an older client.
//
// A body carrying `cols` without `rows` is what a browser that predates 0207 sends, or a script, or a
// hand-built PUT. That is an omission, and this package has already paid once for refusing one: the
// blanket `today` fill made a layout that omitted `range` come back 422, described at length in
// naturalWindow's header. So the pair goes back to "the preset decides" and the save proceeds.
func TestAHalfSpanIsClearedToThePresetRatherThanRefused(t *testing.T) {
	in := []Widget{
		{Key: "a", Shape: WidgetStat, Measure: MetricOrdersOpen, Split: SplitNone,
			Range: RangeOpen, Size: SizeM, Cols: 6, Position: 0, Visible: true},
		{Key: "b", Shape: WidgetStat, Measure: MetricOrdersOpen, Split: SplitNone,
			Range: RangeOpen, Size: SizeS, Rows: 2, Position: 1, Visible: true},
		{Key: "c", Shape: WidgetStat, Measure: MetricOrdersOpen, Split: SplitNone,
			Range: RangeOpen, Size: SizeS, Cols: -3, Rows: -1, Position: 2, Visible: true},
	}
	out := NormalizeWidgets(in)
	if len(out) != 3 {
		t.Fatalf("a half-pair must not cost somebody a widget; %d of 3 survived: %+v", len(out), out)
	}
	for _, w := range out {
		if w.Cols != 0 || w.Rows != 0 {
			t.Fatalf("%s kept a half-span %dx%d; it must fall back to the preset", w.Key, w.Cols, w.Rows)
		}
	}
	// THE POSITIVE CONTROL. A complete, on-grid pair survives normalisation untouched, or this test
	// would pass just as well against a Normalize that cleared every span it saw.
	kept := NormalizeWidgets([]Widget{
		{Key: "d", Shape: WidgetStat, Measure: MetricOrdersOpen, Split: SplitNone,
			Range: RangeOpen, Size: SizeM, Cols: 8, Rows: 2, Position: 0, Visible: true}})
	if len(kept) != 1 || kept[0].Cols != 8 || kept[0].Rows != 2 {
		t.Fatalf("a real span must survive normalisation: %+v", kept)
	}
}

// TestAFreeSpanOffTheGridIsRefusedWithA422 is the INVENTION half, and it asserts the sentinel and the
// status together, either alone is passable and wrong (see the branch-existence file's argument).
func TestAFreeSpanOffTheGridIsRefusedWithA422(t *testing.T) {
	s, ws := widgetService(t)
	off := widget("orders.open", WidgetStat, MetricOrdersOpen, SplitNone, SizeM, 0)
	off.Cols, off.Rows = 5, 1 // five columns leaves a seven-column remainder nothing fits

	_, err := s.SaveMyWidgets(context.Background(),
		Scope{UserID: "u1", Profile: ProfileCounter}, []Widget{off})
	if !errors.Is(err, ErrSpanNotOffered) {
		t.Fatalf("a span off the grid must be refused, got %v", err)
	}
	// AND ZERO ROWS LAND, the posture every sibling refusal in this package holds.
	if ws.writes != 0 || len(ws.user["u1"]) != 0 {
		t.Fatalf("the store was written for a refused save: %d writes, %+v", ws.writes, ws.user["u1"])
	}

	// THE STATUS, through the handler's own mapping: 422, and specifically not 500 or 400. The
	// submission is well-formed and the caller is entitled to be here; what is wrong is the CONTENT
	// of one field, which is what 422 means and what widget_handler.go already argues for its four
	// siblings.
	h := NewHandler(s, nil)
	rec := httptest.NewRecorder()
	h.failWidget(rec, err)
	if rec.Code != 422 {
		t.Fatalf("status = %d, want 422 (the arm its sibling refusals take): %s", rec.Code, rec.Body.String())
	}

	// THE POSITIVE CONTROL. The same widget on a real grid line saves, so this is a guard and not a
	// wall, and it proves the free span reaches the store rather than being quietly dropped.
	on := off
	on.Cols, on.Rows = 8, 2
	saved, err := s.SaveMyWidgets(context.Background(),
		Scope{UserID: "u1", Profile: ProfileCounter}, []Widget{on})
	if err != nil {
		t.Fatalf("a span ON the grid must save: %v", err)
	}
	if len(saved) != 1 || saved[0].Cols != 8 || saved[0].Rows != 2 {
		t.Fatalf("the free span must survive the save: %+v", saved)
	}
}

// TestTheSpanRefusalDoesNotNameTheSnapSet keeps the sibling posture: a refusal that listed what it
// would have accepted is a probe, and the catalog answers that question honestly for callers entitled
// to an answer.
func TestTheSpanRefusalDoesNotNameTheSnapSet(t *testing.T) {
	msg := ErrSpanNotOffered.Error()
	for _, n := range []string{"3", "4", "6", "8", "9", "12"} {
		if strings.Contains(msg, n) {
			t.Fatalf("the refusal names what it would have accepted: %q", msg)
		}
	}
}

// TestAStoredFreeSpanBeatsThePresetOnTheWire proves the override actually overrides, all the way to
// the bytes the browser reads, and that a row without one still gets its preset's footprint.
//
// The client is told the span rather than computing it (WidgetView's own header: "the arithmetic that
// decides whether a bento fits is the server's"), so if SpanFor were not consulted here the browser
// would draw a card the person never dragged and no test below this line would notice.
func TestAStoredFreeSpanBeatsThePresetOnTheWire(t *testing.T) {
	rows := []Widget{
		{Key: "free", Shape: WidgetStat, Measure: MetricOrdersOpen, Split: SplitNone,
			Range: RangeOpen, Size: SizeS, Cols: 9, Rows: 3, Position: 0, Visible: true},
		{Key: "preset", Shape: WidgetStat, Measure: MetricOrdersOpen, Split: SplitNone,
			Range: RangeOpen, Size: SizeL, Position: 1, Visible: true},
	}
	views := WidgetViewsFor(rows, MetricByID)
	if len(views) != 2 {
		t.Fatalf("both rows draw a catalog measure: %+v", views)
	}
	if views[0].SpanCols != 9 || views[0].SpanRows != 3 {
		t.Fatalf("the free span must win over its preset: %dx%d", views[0].SpanCols, views[0].SpanRows)
	}
	// AND THE SIZE IS STILL THERE BESIDE IT. A row that threw the letter away the moment somebody
	// dragged a corner would be frozen at whatever the grid meant that afternoon; 0207's header
	// argues this at length.
	if views[0].Size != SizeS {
		t.Fatalf("the preset letter must survive an override: %q", views[0].Size)
	}
	if views[1].SpanCols != 6 || views[1].SpanRows != 2 {
		t.Fatalf("a row with no override takes its preset's span: %dx%d", views[1].SpanCols, views[1].SpanRows)
	}
}
