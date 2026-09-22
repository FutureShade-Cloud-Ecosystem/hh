package dashboard

// widget_test.go, the widget substrate's gates, each one guarding itself.
//
// The house shape for a byte-absence assertion is used verbatim below: marshal the ALLOWED caller
// FIRST as a positive control and assert the words ARE present, then the refused caller and assert
// the words, the digits AND the literal string "null" are all absent. A test that only asserts
// absence passes just as happily against an empty response, which is how a broken gate ships.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
)

// -- fakes ---------------------------------------------------------------------------------------

// fakeWidgetStore is an in-memory WidgetStore that RECORDS WRITES. The recording is the point: the
// forged-write twin test asserts zero rows landed, and "the fake was never called" is the only way
// to prove that without a database.
type fakeWidgetStore struct {
	profile map[string][]Widget
	user    map[string][]Widget
	writes  int
	err     error
}

func newFakeWidgetStore() *fakeWidgetStore {
	return &fakeWidgetStore{profile: map[string][]Widget{}, user: map[string][]Widget{}}
}

func (f *fakeWidgetStore) ProfileWidgets(_ context.Context, p string) ([]Widget, error) {
	return f.profile[p], f.err
}
func (f *fakeWidgetStore) UserWidgets(_ context.Context, u string) ([]Widget, error) {
	return f.user[u], f.err
}
func (f *fakeWidgetStore) SaveUserWidgets(_ context.Context, u string, rows []Widget) error {
	f.writes++
	f.user[u] = rows
	return f.err
}
func (f *fakeWidgetStore) ClearUserWidgets(_ context.Context, u string) (int, error) {
	n := len(f.user[u])
	delete(f.user, u)
	return n, f.err
}
func (f *fakeWidgetStore) SaveProfileWidgets(_ context.Context, p string, rows []Widget, _ string) error {
	f.writes++
	f.profile[p] = rows
	return f.err
}

// fakeNeedsPolicy is an in-memory NeedsPolicyStore.
type fakeNeedsPolicy struct {
	rows map[string][]NeedsPolicy
	err  error
}

func (f *fakeNeedsPolicy) NeedsSources(_ context.Context, p string) ([]NeedsPolicy, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.rows[p], nil
}

func widgetService(t *testing.T) (*Service, *fakeWidgetStore) {
	t.Helper()
	ws := newFakeWidgetStore()
	s := NewService(newFakeStore(), nil).
		WithMetricStore(&fakeCuration{rows: map[string][]CuratedMetric{}}).
		WithWidgetStore(ws)
	return s, ws
}

// naturalRange is the window a measure actually offers, its catalog entry's first, which is the
// only one any provider can honour today.
//
// THE FIXTURE ASKS THE CATALOG ON PURPOSE. It used to hardcode RangeToday for every measure, which
// was fine while range was ungated and became a trap the moment it was not: a test about the MEASURE
// gate would fail on the RANGE gate, report the wrong refusal, and read as the measure gate having
// broken. Deriving it here keeps each test failing for its own reason. The range gate itself is
// proved separately, with literal windows and no derivation anywhere near it.
func naturalRange(measure string) string {
	if measure == MeasureNeedsYou {
		return needsMeasureMeta.Ranges[0]
	}
	if m, ok := MetricByID[measure]; ok && len(m.Ranges) > 0 {
		return m.Ranges[0]
	}
	return RangeToday
}

func widget(key, shape, measure, split, size string, pos int) Widget {
	return Widget{Key: key, Shape: shape, Measure: measure, Split: split,
		Range: naturalRange(measure), Size: size, Position: pos, Visible: true}
}

// widgetRanged is `widget` with the window named outright, for the cases that are ABOUT the window.
func widgetRanged(key, shape, measure, split, size, rng string, pos int) Widget {
	w := widget(key, shape, measure, split, size, pos)
	w.Range = rng
	return w
}

// withBranch narrows a fixture widget to one branch.
func withBranch(w Widget, branch string) Widget {
	w.BranchID = branch
	return w
}

func widgetKeys(vs []WidgetView) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.Key
	}
	return out
}

// -- the closed sets -----------------------------------------------------------------------------

// -- A TEST WHOSE MEANING CHANGED, AND THE NAME IT KEEPS ----------------------------------------
//
// THIS TEST IS NOW ABOUT SIX SHAPES AND *FIVE* SIZES. Home v3.5 ruled a twelve-column grid with a
// sixth preset, `T` for Tall, 3x2, so the assertion this function has carried since 0201 ("four
// sizes, not %d") is no longer the doctrine and has been rewritten below.
//
// THE NAME STAYS WRONG ON PURPOSE. Renaming it to ...SixAndFive would be one PASS name lost and one
// gained, and this session's gate runs `comm -23` over the PASS list: a lost name fails the run. That
// is the right trade, a stale word in a test name is cheap and a gate that stops noticing a deleted
// test is not, but it has to be written down where the next reader meets it, which is here. The
// session's NOTES carry the same entry.
//
// The old numbers are not merely replaced, they are ASSERTED AGAINST as history: the x3 mapping below
// is what let 0207 move thirty seeded role defaults and every saved layout with no data migration, so
// it is the load-bearing fact of the whole wave and the one thing that would be silently expensive to
// break.
func TestWidgetClosedSetsAreExactlyTheDoctrinesSixAndFour(t *testing.T) {
	if len(WidgetShapeSet) != 6 {
		t.Fatalf("six shapes, not %d: %v", len(WidgetShapeSet), WidgetShapeSet)
	}
	if len(WidgetSizeSet) != 5 {
		t.Fatalf("five sizes on the v3.5 grid (S · T · M · L · XL), not %d: %v",
			len(WidgetSizeSet), WidgetSizeSet)
	}
	// The doctrine's own table: S 3x1 · Tall 3x2 · M 6x1 · L 6x2 · Wide 12x1.
	for size, want := range map[string][2]int{
		SizeS: {3, 1}, SizeT: {3, 2}, SizeM: {6, 1}, SizeL: {6, 2}, SizeXL: {12, 1},
	} {
		c, r := SpanOf(size)
		if c != want[0] || r != want[1] {
			t.Fatalf("span of %s = %dx%d, want %dx%d", size, c, r, want[0], want[1])
		}
	}
	// THE x3 MAPPING, PINNED. The four presets that existed before v3.5 each scale by exactly three
	// columns with their row count untouched, which is the whole reason 0207 needed no data migration:
	// every stored row holds a LETTER, so re-ruling SpanOf moved every layout at identical
	// proportions. A change here that broke the ratio would silently re-proportion every Home in the
	// building.
	for size, oldCols := range map[string]int{SizeS: 1, SizeM: 2, SizeL: 2, SizeXL: 4} {
		oldRows := map[string]int{SizeS: 1, SizeM: 1, SizeL: 2, SizeXL: 1}[size]
		c, r := SpanOf(size)
		if c != oldCols*3 || r != oldRows {
			t.Fatalf("%s was %dx%d on the four-column grid and is %dx%d on the twelve; the x3 map "+
				"wants %dx%d", size, oldCols, oldRows, c, r, oldCols*3, oldRows)
		}
	}
	if c, r := SpanOf("XXL"); c != 0 || r != 0 {
		t.Fatalf("a size outside the set has no span: %dx%d", c, r)
	}
	// T is the one preset with no ancestor: a narrow card two rows deep could not be said at all on a
	// four-column bento, which is why it is the only genuinely new entry in the vocabulary.
	if c, r := SpanOf(SizeT); c != 3 || r != 2 {
		t.Fatalf("Tall is 3x2, not %dx%d", c, r)
	}
}

// Every catalog entry must declare widget splits and shapes that are internally coherent, the drift
// gate for the catalog extension itself.
func TestEveryCatalogMeasureDeclaresCoherentWidgetChoices(t *testing.T) {
	for _, m := range MetricCatalog {
		if len(m.Splits) == 0 || m.Splits[0] != SplitNone {
			t.Fatalf("measure %q must offer %q first, a stat is most of a Home: %v", m.ID, SplitNone, m.Splits)
		}
		if len(m.Splits) != len(m.Dimensions)+1 {
			t.Fatalf("measure %q: splits must be %q + its dimensions, got %v for %v",
				m.ID, SplitNone, m.Splits, m.Dimensions)
		}
		for i, d := range m.Dimensions {
			if m.Splits[i+1] != d {
				t.Fatalf("measure %q: split %d = %q, want dimension %q", m.ID, i+1, m.Splits[i+1], d)
			}
		}
		if len(m.WidgetShapes) == 0 {
			t.Fatalf("measure %q offers no widget shape, so no widget can draw it", m.ID)
		}
		if !containsString(m.WidgetShapes, WidgetStat) {
			t.Fatalf("measure %q must offer %q: every measure is at least one number", m.ID, WidgetStat)
		}
		for _, sh := range m.WidgetShapes {
			if !IsWidgetShape(sh) {
				t.Fatalf("measure %q offers unknown widget shape %q", m.ID, sh)
			}
		}
	}
}

// -- resolution: whole or absent -----------------------------------------------------------------

func TestResolveWidgetsPrefersTheUserLayerWholeOrAbsent(t *testing.T) {
	role := []Widget{widget("hero.sales", WidgetStat, MetricSalesByBranch, SplitNone, SizeL, 0),
		widget("needs.you", WidgetList, MeasureNeedsYou, SplitNone, SizeL, 1)}
	user := []Widget{widget("orders.trend", WidgetTrend, MetricOrdersOpen, SplitNone, SizeM, 0)}

	got, src := ResolveWidgets(role, user)
	if src != SourceUser || len(got) != 1 || got[0].Key != "orders.trend" {
		t.Fatalf("a personal layout is the WHOLE layout: %v / %s", got, src)
	}
	got, src = ResolveWidgets(role, nil)
	if src != SourceRole || len(got) != 2 {
		t.Fatalf("no personal rows means the role default answers: %v / %s", got, src)
	}
	got, src = ResolveWidgets(nil, nil)
	if src != SourceCatalog || len(got) != 0 {
		t.Fatalf("neither layer is an empty layout, never null: %v / %s", got, src)
	}
}

func TestResolveWidgetsRenumbersDeterministicallyOnAPositionTie(t *testing.T) {
	rows := []Widget{
		widget("b.two", WidgetStat, MetricOrdersOpen, SplitNone, SizeS, 3),
		widget("a.one", WidgetStat, MetricPickQueue, SplitNone, SizeS, 3),
		widget("c.zero", WidgetStat, MetricDeliveriesToday, SplitNone, SizeS, 0),
	}
	got, _ := ResolveWidgets(rows, nil)
	if got[0].Key != "c.zero" || got[1].Key != "a.one" || got[2].Key != "b.two" {
		t.Fatalf("a position tie resolves by key, every time: %+v", got)
	}
	for i, w := range got {
		if w.Position != i {
			t.Fatalf("positions are renumbered 0..n-1: %+v", got)
		}
	}
}

// -- the read gate: byte-absence -----------------------------------------------------------------

func TestWidgetNamingAMeasureOutsideTheCatalogIsNotReturned(t *testing.T) {
	rows := []Widget{
		widget("margin.branch", WidgetBars, MetricGrossMargin, DimBranch, SizeM, 0),
		widget("orders.open", WidgetStat, MetricOrdersOpen, SplitNone, SizeS, 1),
		widget("gone", WidgetStat, "retired.measure", SplitNone, SizeS, 2),
	}
	allowed := map[string]MetricMeta{MetricOrdersOpen: MetricByID[MetricOrdersOpen]}
	got := WidgetViewsFor(rows, allowed)
	if len(got) != 1 || got[0].Key != "orders.open" {
		t.Fatalf("a widget whose measure is absent is NOT RETURNED, never rendered empty: %v", widgetKeys(got))
	}
	if got[0].Position != 0 {
		t.Fatal("the surviving widgets are renumbered, so a dropped one leaves no hole in the bento")
	}
}

func TestNeedsYouIsAllowedForEveryCallerWithoutBeingInTheMetricCatalog(t *testing.T) {
	if _, inCatalog := MetricByID[MeasureNeedsYou]; inCatalog {
		t.Fatal("the reserved measure must NOT be in the chart composer's catalog, it is not a series")
	}
	got := WidgetViewsFor(
		[]Widget{widget("needs.you", WidgetList, MeasureNeedsYou, SplitNone, SizeL, 0)},
		map[string]MetricMeta{}) // an empty catalog: the strictest caller there is
	if len(got) != 1 || got[0].Label != "Needs you" {
		t.Fatalf("Needs-you is never removed from a Home: %+v", got)
	}
}

func TestAnUntitledWidgetGetsItsTitleFromTheMeasureAndSplit(t *testing.T) {
	got := WidgetViewsFor(
		[]Widget{widget("orders.status", WidgetBars, MetricOrdersOpen, DimStatus, SizeM, 0)},
		map[string]MetricMeta{MetricOrdersOpen: MetricByID[MetricOrdersOpen]})
	if got[0].Title != "Open orders by status" {
		t.Fatalf("auto title = %q", got[0].Title)
	}
}

// THE MUTATION TEST. Positive control first, then the refusal, then `null`.
func TestCostBearingWidgetsAreByteAbsentForACallerWhoMayNotSeeThem(t *testing.T) {
	s, ws := widgetService(t)
	rows := []Widget{
		widget("hero.sales", WidgetStat, MetricSalesByBranch, SplitNone, SizeL, 0),
		widget("needs.you", WidgetList, MeasureNeedsYou, SplitNone, SizeL, 1),
		widget("margin.branch", WidgetBars, MetricGrossMargin, DimBranch, SizeM, 2),
		widget("inventory.value", WidgetStat, MetricInventoryValue, SplitNone, SizeS, 3),
	}
	rows[2].Title = "Gross margin 999999"
	ws.profile[ProfileManager] = rows
	ws.profile[ProfileCounter] = rows

	// -- POSITIVE CONTROL: an allowed caller. The words MUST be here, or the assertion below is
	// measuring an empty response rather than an enforced absence.
	allowed, err := json.Marshal(s.WidgetsFor(context.Background(),
		Scope{Profile: ProfileManager, RoleCode: "FINANCIAL_CONTROLLER", CostAllowed: true}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{MetricGrossMargin, MetricInventoryValue, "Gross margin", "999999"} {
		if !strings.Contains(string(allowed), want) {
			t.Fatalf("positive control failed: %q missing for an ALLOWED caller: %s", want, allowed)
		}
	}

	// -- THE REFUSAL.
	refused, err := json.Marshal(s.WidgetsFor(context.Background(),
		Scope{Profile: ProfileCounter, RoleCode: "INSIDE_SALES", CostAllowed: false}))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		MetricGrossMargin, MetricInventoryValue,
		"margin", "Gross margin", "Inventory value", "cost", "999999",
		"null", // `null` is not absence, three trains here found that out
	} {
		if strings.Contains(string(refused), forbidden) {
			t.Fatalf("byte %q is present for a caller who may not see cost: %s", forbidden, refused)
		}
	}
	// ...and the same payload still carries the operational widgets, so the assertion above is
	// measuring ABSENCE rather than an empty answer.
	for _, want := range []string{"hero.sales", "needs.you", MetricSalesByBranch} {
		if !strings.Contains(string(refused), want) {
			t.Fatalf("the allowed widgets must still be there: %s", refused)
		}
	}
}

// -- the write gate, and its twin ----------------------------------------------------------------

func TestForgedWidgetNamingAnUnofferedMeasureIsRefused(t *testing.T) {
	s, _ := widgetService(t)
	_, err := s.SaveMyWidgets(context.Background(),
		Scope{UserID: "u1", Profile: ProfileCounter, CostAllowed: false},
		[]Widget{
			widget("orders.open", WidgetStat, MetricOrdersOpen, SplitNone, SizeS, 0),
			widget("margin.branch", WidgetBars, MetricGrossMargin, DimBranch, SizeM, 1),
		})
	if !errors.Is(err, ErrMeasureNotOffered) {
		t.Fatalf("a forged measure must be refused server-side, got %v", err)
	}
	if strings.Contains(err.Error(), MetricGrossMargin) || strings.Contains(err.Error(), "margin") {
		t.Fatalf("the refusal must not name what it refused, that is enumeration: %q", err)
	}
}

// THE TWIN. Byte-absence gates the DISPLAY; this proves it also did not gate the ACT by accident -
// the store is never reached, so zero rows landed.
func TestAForgedWidgetWriteLandsZeroRows(t *testing.T) {
	s, ws := widgetService(t)
	_, err := s.SaveMyWidgets(context.Background(),
		Scope{UserID: "u1", Profile: ProfileCounter, CostAllowed: false},
		[]Widget{widget("margin.branch", WidgetBars, MetricGrossMargin, DimBranch, SizeM, 0)})
	if err == nil {
		t.Fatal("the write must be refused")
	}
	if ws.writes != 0 {
		t.Fatalf("the store was written %d times for a refused save, the gate runs BEFORE the store", ws.writes)
	}
	if len(ws.user["u1"]) != 0 {
		t.Fatalf("zero rows must land: %+v", ws.user["u1"])
	}
}

func TestAnAllowedCallerCanSaveACostWidget(t *testing.T) {
	s, ws := widgetService(t)
	got, err := s.SaveMyWidgets(context.Background(),
		Scope{UserID: "u2", Profile: ProfileManager, CostAllowed: true},
		[]Widget{widget("margin.branch", WidgetBars, MetricGrossMargin, DimBranch, SizeM, 0)})
	if err != nil {
		t.Fatalf("an allowed caller saves the same widget: %v", err)
	}
	if len(got) != 1 || len(ws.user["u2"]) != 1 {
		t.Fatalf("one row lands: %+v", ws.user["u2"])
	}
}

func TestASubmittedShapeOrSplitTheMeasureDoesNotOfferIsRefused(t *testing.T) {
	s, ws := widgetService(t)
	ctx := context.Background()
	scope := Scope{UserID: "u3", Profile: ProfileCounter}

	if _, err := s.SaveMyWidgets(ctx, scope,
		[]Widget{widget("k.one", WidgetGauge, MetricOrdersOpen, SplitNone, SizeS, 0)}); !errors.Is(err, ErrShapeNotOffered) {
		t.Fatalf("a shape the measure does not offer must be refused, got %v", err)
	}
	if _, err := s.SaveMyWidgets(ctx, scope,
		[]Widget{widget("k.two", WidgetBars, MetricOrdersOpen, DimBucket, SizeM, 0)}); !errors.Is(err, ErrSplitNotOffered) {
		t.Fatalf("a split the measure does not offer must be refused, got %v", err)
	}
	if ws.writes != 0 {
		t.Fatalf("neither refusal may reach the store: %d writes", ws.writes)
	}
}

func TestSubstrateMalformedRowsAreDroppedNotRefused(t *testing.T) {
	got := NormalizeWidgets([]Widget{
		widget("ok.one", WidgetStat, MetricOrdersOpen, SplitNone, SizeS, 0),
		widget("BAD KEY", WidgetStat, MetricOrdersOpen, SplitNone, SizeS, 1),
		widget("ok.two", "scatter", MetricOrdersOpen, SplitNone, SizeS, 2),
		widget("ok.three", WidgetStat, MetricOrdersOpen, SplitNone, "XXL", 3),
		{Key: "ok.four", Shape: WidgetStat, Measure: MetricOrdersOpen, Split: SplitNone,
			Range: "since_forever", Size: SizeS, Position: 4},
		widget("ok.one", WidgetStat, MetricPickQueue, SplitNone, SizeS, 5), // duplicate key
	})
	if len(got) != 1 || got[0].Key != "ok.one" || got[0].Position != 0 {
		t.Fatalf("only the well-formed row survives, renumbered: %+v", got)
	}
}

func TestNormalizeWidgetsFillsTheRestrictiveDefaults(t *testing.T) {
	got := NormalizeWidgets([]Widget{{Key: "k.one", Shape: WidgetStat, Measure: MetricOrdersOpen}})
	if len(got) != 1 {
		t.Fatalf("a row with only the required fields is well-formed: %+v", got)
	}
	if got[0].Split != SplitNone || got[0].Size != SizeM {
		t.Fatalf("defaults must be the restrictive ones: %+v", got[0])
	}
	// A MEANING CHANGE. This line read RangeToday, one blanket default for every measure, which was
	// harmless while range was ungated and became a 422 the moment it was not: orders.open's one
	// honest window is `open`, so a body that simply omitted `range` had its WHOLE LAYOUT refused.
	// Seven of the nine measures were in that position.
	if got[0].Range != RangeOpen {
		t.Fatalf("a blank range must fill with the MEASURE's window, not a blanket one: %+v", got[0])
	}
}

// THE POSITIVE CONTROL FOR THE ABOVE, and it is the whole point: the fill must differ BY MEASURE, or
// the change is just a different blanket default that happens to suit the measure the test picked.
func TestTheBlankRangeFillIsPerMeasureAndSurvivesTheWriteGate(t *testing.T) {
	for _, tc := range []struct{ measure, want string }{
		{MetricOrdersOpen, RangeOpen},
		{MetricARBalance, RangeOpen},
		{MetricPickQueue, RangeOpen},
		{MeasureNeedsYou, RangeOpen},
		{MetricSalesByBranch, RangeToday},
		{MetricDeliveriesToday, RangeToday},
	} {
		got := NormalizeWidgets([]Widget{{Key: "k.one", Shape: WidgetStat, Measure: tc.measure}})
		if len(got) != 1 {
			t.Fatalf("%s: row dropped: %+v", tc.measure, got)
		}
		if got[0].Range != tc.want {
			t.Errorf("%s filled %q, want %q", tc.measure, got[0].Range, tc.want)
		}
		// AND THE FILLED ROW MUST THEN SAVE. Normalize and Validate run back to back on every write,
		// so a default the gate immediately refuses is worse than no default at all.
		if err := ValidateWidgets(got, MetricByID, AnyBranch); err != nil {
			t.Errorf("%s: the filled default is refused by the gate that follows it: %v", tc.measure, err)
		}
	}
	// An unknown measure keeps the old fallback and is refused for the honest reason, naming a
	// measure that does not exist, rather than for its window.
	got := NormalizeWidgets([]Widget{{Key: "k.one", Shape: WidgetStat, Measure: "nope.gone"}})
	if len(got) != 1 || got[0].Range != RangeToday {
		t.Fatalf("an unknown measure must not invent a window: %+v", got)
	}
	if err := ValidateWidgets(got, MetricByID, AnyBranch); !errors.Is(err, ErrMeasureNotOffered) {
		t.Fatalf("want the measure refusal, got %v", err)
	}
}

// correctedWindows reads 0205's truth list, the (measure -> window) map it rewrites stored rows to.
//
// READING THE MIGRATION, NOT RESTATING IT, is this package's idiom and here it is load bearing: a
// committed migration never moves, so 0201's literals are still the OLD windows and always will be.
// The state a dealer actually has is 0201 as corrected by 0205, and only reading both gives it.
func correctedWindows(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile("../../db/migrations/0205_widget_windows_tell_the_truth.sql")
	if err != nil {
		t.Fatalf("0205 must be readable from the package: %v", err)
	}
	re := regexp.MustCompile(`\('([a-z0-9._]+)',\s*'([a-z0-9_]+)'\)`)
	out := map[string]string{}
	for _, m := range re.FindAllStringSubmatch(string(b), -1) {
		out[m[1]] = m[2]
	}
	if len(out) < 9 {
		t.Fatalf("0205's truth list parsed to %d entries; it named nine measures when this gate was "+
			"written, the scan has drifted and every assertion below would pass vacuously", len(out))
	}
	return out
}

// THE MIGRATION AND THE CATALOG CANNOT SEPARATE. 0205 rewrites stored windows to a list of its own,
// and if that list ever disagrees with MetricMeta.Ranges the database says one thing and the gate
// enforces another, the write gate would refuse rows the migration had just written.
func TestMigration0205AgreesWithTheCatalogMeasureForMeasure(t *testing.T) {
	corrected := correctedWindows(t)
	for _, m := range MetricCatalog {
		got, ok := corrected[m.ID]
		if !ok {
			t.Errorf("0205 names no window for %s, so a stored row on that measure is never corrected", m.ID)
			continue
		}
		if got != m.Ranges[0] {
			t.Errorf("0205 rewrites %s to %q; the catalog offers %v, the migration would write rows "+
				"the write gate then refuses", m.ID, got, m.Ranges)
		}
	}
	if got := corrected[MeasureNeedsYou]; got != needsMeasureMeta.Ranges[0] {
		t.Errorf("0205 rewrites the reserved measure to %q, want %q", got, needsMeasureMeta.Ranges[0])
	}
	// The other direction: 0205 must not name a measure this build has never heard of.
	for measure := range corrected {
		if measure == MeasureNeedsYou {
			continue
		}
		if _, ok := MetricByID[measure]; !ok {
			t.Errorf("0205 corrects %q, which is not a measure in this build", measure)
		}
	}
}

// THE REGRESSION THIS EXISTS FOR: the role default the product itself seeds must survive a
// GET-then-PUT of untouched bytes. The admin editor reads profile rows RAW, ProfileWidgetsFor never
// runs them through WidgetViewsFor, so the read-path repair does not reach that screen, and its
// save runs ValidateWidgets. Landing the range gate without correcting the seeded rows made all four
// shipped role defaults unsaveable, on a fresh install, for a layout no admin wrote.
func TestEverySeededRoleDefaultSurvivesAGetThenPut(t *testing.T) {
	corrected := correctedWindows(t)
	byProfile := map[string][]Widget{}
	fixed := 0
	for _, w := range seededWidgets(t) {
		// 0201 AS 0205 LEAVES IT. This is the row a dealer actually has, and the only one worth
		// asserting about, 0201's own literal is frozen history.
		window := w.timeRange
		if c, ok := corrected[w.measure]; ok {
			if c != window {
				fixed++
			}
			window = c
		}
		byProfile[w.profile] = append(byProfile[w.profile], Widget{
			Key: w.key, Shape: w.shape, Measure: w.measure, Split: w.split,
			Range: window, Size: w.size, Position: w.position, Visible: true})
	}
	if len(byProfile) < 4 {
		t.Fatalf("0201 seeds %d profiles; it seeded four when this gate was written", len(byProfile))
	}
	// GUARD THE GUARD. If 0205 corrected nothing, this test would pass by measuring an unchanged
	// 0201 that happened to be legal, which was never true, and would mean the parse had failed.
	if fixed != 11 {
		t.Fatalf("0205 corrects %d of 0201's seeded rows; it corrected eleven when this gate was "+
			"written. Either the seed moved or the truth list did", fixed)
	}
	for profile, rows := range byProfile {
		if err := ValidateWidgets(NormalizeWidgets(rows), MetricByID, AnyBranch); err != nil {
			t.Errorf("the seeded %q role default (%d rows) cannot be saved back unchanged: %v, an "+
				"admin opening the role-default editor and pressing save gets a 422 on a layout they "+
				"did not write", profile, len(rows), err)
		}
	}
}

func TestAnEmptyPersonalLayoutIsRefusedRatherThanStored(t *testing.T) {
	s, ws := widgetService(t)
	if _, err := s.SaveMyWidgets(context.Background(),
		Scope{UserID: "u4", Profile: ProfileCounter}, nil); !errors.Is(err, ErrEmptyWidgets) {
		t.Fatalf("an empty override is indistinguishable from no override once stored, got %v", err)
	}
	if ws.writes != 0 {
		t.Fatal("and nothing may be written on the way to refusing it")
	}
}

// -- reset is a DELETE ---------------------------------------------------------------------------

func TestResetDeletesThePersonsRowsAndWritesNoDefaults(t *testing.T) {
	s, ws := widgetService(t)
	ctx := context.Background()
	ws.profile[ProfileCounter] = []Widget{
		widget("hero.sales", WidgetStat, MetricSalesByBranch, SplitNone, SizeL, 0),
		widget("needs.you", WidgetList, MeasureNeedsYou, SplitNone, SizeL, 1),
	}
	if _, err := s.SaveMyWidgets(ctx, Scope{UserID: "u5", Profile: ProfileCounter},
		[]Widget{widget("orders.open", WidgetStat, MetricOrdersOpen, SplitNone, SizeS, 0)}); err != nil {
		t.Fatal(err)
	}
	writesBefore := ws.writes

	n, err := s.ResetMyWidgets(ctx, "u5")
	if err != nil || n != 1 {
		t.Fatalf("reset cleared %d rows (err %v), want 1", n, err)
	}
	if ws.writes != writesBefore {
		t.Fatal("RESET IS A DELETE: it must never write the defaults into the person's level (0191)")
	}
	if _, still := ws.user["u5"]; still {
		t.Fatal("the person's rows are gone")
	}
	got := s.WidgetsFor(ctx, Scope{UserID: "u5", Profile: ProfileCounter})
	if got.Source != SourceRole || len(got.Widgets) != 2 {
		t.Fatalf("...and the role default answers again: %s / %v", got.Source, widgetKeys(got.Widgets))
	}
}

// -- degradation ---------------------------------------------------------------------------------

func TestAnUnreadableWidgetStoreStillAnswersARead(t *testing.T) {
	ws := newFakeWidgetStore()
	ws.err = errors.New("table missing")
	s := NewService(newFakeStore(), nil).WithWidgetStore(ws)
	got := s.WidgetsFor(context.Background(), Scope{UserID: "u6", Profile: ProfileCounter})
	if got.Widgets == nil {
		t.Fatal("a read path degrades downward: an empty ARRAY, never null and never an error")
	}
}

func TestNoWidgetStoreServesAnEmptyHomeRatherThanFailing(t *testing.T) {
	s := NewService(newFakeStore(), nil)
	got := s.WidgetsFor(context.Background(), Scope{UserID: "u7", Profile: ProfileCounter})
	if got.Source != SourceCatalog || len(got.Widgets) != 0 {
		t.Fatalf("a build ahead of 0201 has a working cockpit with no widgets: %+v", got)
	}
	if _, err := s.ResetMyWidgets(context.Background(), "u7"); !errors.Is(err, ErrNoWidgetStore) {
		t.Fatalf("a WRITE says so plainly instead of pretending it worked: %v", err)
	}
}

// -- drift gates: the migrations and this build must agree ---------------------------------------

// seededWidgetRows reads the widget rows 0201 seeds. Reading the MIGRATION rather than restating its
// list is this package's own idiom (viz_test.go's seededMetricIDs does the same to 0182): a test
// carrying its own copy of the data proves only that the copy is consistent.
type seededWidget struct {
	profile, key, shape, measure, split, timeRange, size, title string
	position                                                    int
}

func seededWidgets(t *testing.T) []seededWidget {
	t.Helper()
	b, err := os.ReadFile("../../db/migrations/0201_dashboard_widgets.sql")
	if err != nil {
		t.Fatalf("0201 must be readable from the package: %v", err)
	}
	re := regexp.MustCompile(
		`\(\s*'([a-z]+)',\s*'([a-z0-9._-]+)',\s*'([a-z]+)',\s*'([a-z0-9._]+)',\s*'([a-z]+)',\s*'([a-z0-9_]+)',\s*'(S|M|L|XL)',\s*'([^']*)',\s*(\d+),\s*(true|false)\)`)
	var out []seededWidget
	for _, m := range re.FindAllStringSubmatch(string(b), -1) {
		pos := 0
		for _, c := range m[9] {
			pos = pos*10 + int(c-'0')
		}
		out = append(out, seededWidget{
			profile: m[1], key: m[2], shape: m[3], measure: m[4], split: m[5],
			timeRange: m[6], size: m[7], title: m[8], position: pos,
		})
	}
	if len(out) == 0 {
		t.Fatal("0201 seeds no widgets, the drift gate would pass vacuously")
	}
	return out
}

func TestSeededRoleDefaultsAreLegalWidgetsInThisBuild(t *testing.T) {
	byProfile := map[string][]seededWidget{}
	for _, w := range seededWidgets(t) {
		if !IsProfile(w.profile) {
			t.Fatalf("0201 seeds unknown profile %q", w.profile)
		}
		if !IsWidgetKey(w.key) || !IsWidgetShape(w.shape) || !IsWidgetSize(w.size) ||
			!IsSplit(w.split) || !IsRange(w.timeRange) {
			t.Fatalf("0201 seeds a widget outside the closed sets: %+v", w)
		}
		m, ok := widgetMeasureMeta(w.measure, MetricByID)
		if !ok {
			t.Fatalf("0201 seeds measure %q which this build does not have", w.measure)
		}
		if !containsString(m.WidgetShapes, w.shape) {
			t.Fatalf("0201 seeds %q as %q, a shape that measure does not offer", w.measure, w.shape)
		}
		if !containsString(m.Splits, w.split) {
			t.Fatalf("0201 seeds %q split by %q, which that measure does not offer", w.measure, w.split)
		}
		byProfile[w.profile] = append(byProfile[w.profile], w)
	}

	// The doctrine, as an assertion: four seeded shapes, each 6-9 widgets, each opening with a hero
	// at 0 and Needs-you at 1.
	if len(byProfile) != 4 {
		t.Fatalf("four job shapes have a dashboard Home (driver's is a takeover, 0141): %d seeded", len(byProfile))
	}
	for profile, ws := range byProfile {
		if len(ws) < 6 || len(ws) > 9 {
			t.Fatalf("%s has %d widgets; the doctrine's range is 6-9", profile, len(ws))
		}
		var hero, needs *seededWidget
		for i := range ws {
			switch ws[i].position {
			case 0:
				hero = &ws[i]
			case 1:
				needs = &ws[i]
			}
		}
		if hero == nil || hero.size != SizeL {
			t.Fatalf("%s must open with a hero at position 0, size L: %+v", profile, hero)
		}
		if needs == nil || needs.measure != MeasureNeedsYou {
			t.Fatalf("%s must carry Needs-you at position 1: %+v", profile, needs)
		}
	}
}

// A role default that names a measure its own people are not OFFERED renders as a hole on first
// login, so 0201's seed is checked against 0182's curation as well as against the catalog.
func TestSeededRoleDefaultsOnlyNameMeasuresThatShapeIsOffered(t *testing.T) {
	curated := map[string]map[string]bool{}
	b, err := os.ReadFile("../../db/migrations/0182_home_metric_catalog.sql")
	if err != nil {
		t.Fatalf("0182 must be readable: %v", err)
	}
	blocks := regexp.MustCompile(`(?s)FROM \(VALUES (.*?)\) AS p \(profile\)(.*?)ON CONFLICT`).
		FindAllStringSubmatch(string(b), -1)
	metricRe := regexp.MustCompile(`\(\s*'([a-z0-9._]+)'\s*,\s*\d+\s*,\s*true\s*\)`)
	profileRe := regexp.MustCompile(`'([a-z]+)'`)
	for _, blk := range blocks {
		var metrics []string
		for _, m := range metricRe.FindAllStringSubmatch(blk[2], -1) {
			metrics = append(metrics, m[1])
		}
		for _, p := range profileRe.FindAllStringSubmatch(blk[1], -1) {
			set := curated[p[1]]
			if set == nil {
				set = map[string]bool{}
				curated[p[1]] = set
			}
			for _, id := range metrics {
				set[id] = true
			}
		}
	}
	if len(curated) == 0 {
		t.Fatal("could not read 0182's curation, the gate would pass vacuously")
	}
	for _, w := range seededWidgets(t) {
		if w.measure == MeasureNeedsYou {
			continue // the reserved measure is not curated and never was
		}
		if !curated[w.profile][w.measure] {
			t.Fatalf("0201 gives %q the measure %q, which 0182 does not offer that shape", w.profile, w.measure)
		}
	}
}

// 0202's vetted (measure, dimension, widget shape) list must be the catalog's own.
func TestCarryoverPairsAreTheCatalogsOwn(t *testing.T) {
	b, err := os.ReadFile("../../db/migrations/0202_widget_carryover.sql")
	if err != nil {
		t.Fatalf("0202 must be readable from the package: %v", err)
	}
	re := regexp.MustCompile(`\(\s*'([a-z0-9._]+)',\s*'([a-z]+)',\s*'([a-z]+)'\),?\s*\n`)
	pairs := re.FindAllStringSubmatch(string(b), -1)
	if len(pairs) == 0 {
		t.Fatal("0202 vets no (measure, dimension) pairs, the gate would pass vacuously")
	}
	seen := map[string]bool{}
	for _, p := range pairs {
		m, ok := MetricByID[p[1]]
		if !ok {
			t.Fatalf("0202 vets measure %q which this build does not have", p[1])
		}
		if !containsString(m.Dimensions, p[2]) {
			t.Fatalf("0202 vets %q split by %q, which the catalog does not offer", p[1], p[2])
		}
		if !containsString(m.WidgetShapes, p[3]) {
			t.Fatalf("0202 maps %q onto widget shape %q, which that measure does not offer", p[1], p[3])
		}
		seen[p[1]+"-"+p[2]] = true
	}
	// And EVERY composable pair must be vetted, or a chart somebody built would be left behind -
	// which is the one thing 0202 promises cannot happen.
	for _, m := range MetricCatalog {
		for _, d := range m.Dimensions {
			if !seen[m.ID+"-"+d] {
				t.Fatalf("0202 does not carry %q split by %q: somebody's chart would be lost", m.ID, d)
			}
		}
	}
}

// The Go fallback policy and 0201's seed must agree, or an unreadable table would silently change
// which sources a role sees (the DefaultHotkeys drift gate, applied to the needs queue).
func TestDefaultNeedsSourcesMatchTheMigrationSeed(t *testing.T) {
	b, err := os.ReadFile("../../db/migrations/0201_dashboard_widgets.sql")
	if err != nil {
		t.Fatalf("0201 must be readable: %v", err)
	}
	re := regexp.MustCompile(`\(\s*'([a-z]+)',\s*'([a-z0-9._]+)',\s*(\d+),\s*(true|false),\s*(\d+),\s*(\d+),\s*(\d+)\)`)
	got := map[string][]NeedsPolicy{}
	atoi := func(s string) int {
		n := 0
		for _, c := range s {
			n = n*10 + int(c-'0')
		}
		return n
	}
	for _, m := range re.FindAllStringSubmatch(string(b), -1) {
		got[m[1]] = append(got[m[1]], NeedsPolicy{
			SourceKey: m[2], Position: atoi(m[3]), Visible: m[4] == "true",
			WarnAt: atoi(m[5]), StopAt: atoi(m[6]), StaleMins: atoi(m[7]),
		})
	}
	if len(got) == 0 {
		t.Fatal("0201 seeds no needs sources, the gate would pass vacuously")
	}
	if len(got) != len(DefaultNeedsSources) {
		t.Fatalf("0201 seeds %d shapes, Go ships %d", len(got), len(DefaultNeedsSources))
	}
	for profile, rows := range got {
		want := DefaultNeedsSources[profile]
		if len(rows) != len(want) {
			t.Fatalf("%s: 0201 seeds %d sources, Go ships %d", profile, len(rows), len(want))
		}
		for i := range rows {
			if rows[i] != want[i] {
				t.Fatalf("%s row %d: migration %+v, Go %+v", profile, i, rows[i], want[i])
			}
		}
		for _, r := range rows {
			if _, ok := NeedsSourceByKey[r.SourceKey]; !ok {
				t.Fatalf("0201 seeds needs source %q which this build does not have", r.SourceKey)
			}
		}
	}
}

// -- the range gate: a window a measure cannot be asked for --------------------------------------
//
// THE DEFECT THIS CLOSES. Every measure was offered all ten named windows while not one series
// provider took a window at all, SeriesProvider.Series is (ctx, scope, dimension), and no query
// below it carries a caller-supplied date predicate. So "Pick queue · last 14 days" drew the entire
// queue under a caption saying fourteen days, and nine of 0201's thirty role defaults shipped that
// way. The number was never wrong; the sentence under it was.

func TestEveryMeasureDeclaresTheWindowsItsProviderCanActuallyHonour(t *testing.T) {
	if len(MetricCatalog) < 8 {
		t.Fatalf("the catalog holds %d metrics; it held 8 when this gate was written", len(MetricCatalog))
	}
	for _, m := range MetricCatalog {
		if len(m.Ranges) == 0 {
			t.Errorf("%s declares no windows, the builder would offer an empty select and "+
				"WidgetViewsFor would have nothing to repair a stored range to", m.ID)
			continue
		}
		for _, r := range m.Ranges {
			if !IsRange(r) {
				t.Errorf("%s offers %q, which is not one of the substrate's ten windows", m.ID, r)
			}
		}
		// EVERY MEASURE OFFERS EXACTLY ONE TODAY, and that is the honest state of the measurement
		// path rather than an accident. This assertion is meant to be RELAXED, but only by the
		// commit that teaches a provider a second window, which is the point of pinning it: a
		// second value added here without the date predicate to match puts the lie straight back.
		if len(m.Ranges) != 1 {
			t.Errorf("%s offers %d windows: %v. No SeriesProvider takes a window yet, so a second "+
				"value here is a promise the SQL cannot keep, teach the provider first",
				m.ID, len(m.Ranges), m.Ranges)
		}
	}
	// The reserved measure too: it is not in MetricCatalog and would otherwise be missed.
	if got := needsMeasureMeta.Ranges; len(got) != 1 || got[0] != RangeOpen {
		t.Fatalf("needs.you offers %v, want exactly [open], it sits at position 1 of all four "+
			"seeded layouts and is the one widget the doctrine says a person may never lose, so an "+
			"absent window here fails every one of them on the first save", got)
	}
}

func TestAWidgetNamingAWindowItsMeasureCannotAnswerIsRefused(t *testing.T) {
	s, ws := widgetService(t)
	// picks.queue is a status set, its SQL has no date predicate at all, so "today" is not a
	// narrower question, it is a question the query cannot be asked.
	_, err := s.SaveMyWidgets(context.Background(),
		Scope{UserID: "u1", Profile: ProfileYard},
		[]Widget{widgetRanged("picks.queue", WidgetStat, MetricPickQueue, SplitNone, SizeS, RangeToday, 0)})
	if !errors.Is(err, ErrRangeNotOffered) {
		t.Fatalf("a window the measure cannot answer must be refused, got %v", err)
	}
	// AND ZERO ROWS LAND. The gate runs before the store, like its two siblings, a save that
	// refused the tenth widget and stored the other nine would report success for a layout nobody
	// asked for.
	if ws.writes != 0 || len(ws.user["u1"]) != 0 {
		t.Fatalf("the store was written for a refused save: %d writes, %+v", ws.writes, ws.user["u1"])
	}
	// THE POSITIVE CONTROL. The same widget with the window its measure DOES offer must save, or
	// this test would pass just as well against a gate that refused everything.
	if _, err := s.SaveMyWidgets(context.Background(),
		Scope{UserID: "u1", Profile: ProfileYard},
		[]Widget{widgetRanged("picks.queue", WidgetStat, MetricPickQueue, SplitNone, SizeS, RangeOpen, 0)}); err != nil {
		t.Fatalf("the same widget with the window its measure offers must save: %v", err)
	}
}

func TestTheRefusalDoesNotNameTheWindowsAMeasureOffers(t *testing.T) {
	// The same posture as the measure refusal directly above it in ValidateWidgets: an error that
	// listed the legal windows would be a probe for what a caller may not otherwise be told. The
	// catalog answers that question honestly, for callers entitled to an answer.
	if strings.Contains(ErrRangeNotOffered.Error(), RangeOpen) ||
		strings.Contains(ErrRangeNotOffered.Error(), "picks") {
		t.Fatalf("the refusal names what it would have accepted: %q", ErrRangeNotOffered)
	}
}

func TestAStoredWindowAMeasureCannotAnswerIsREPAIREDNotDropped(t *testing.T) {
	// THE ASYMMETRY IS THE POINT, and it is why the two checks live in different functions. A
	// MEASURE the caller may not see is dropped, absence is the whole gate. A WINDOW is not a
	// permission: dropping the widget would punch a hole in a role default no admin chose, and on
	// the write path the client re-submits the whole resolved layout, so the hole would then be
	// SAVED. It is repaired to the measure's own window and the card says something true.
	rows := []Widget{
		{Key: "picks.queue", Shape: WidgetStat, Measure: MetricPickQueue, Split: SplitNone,
			Range: RangeToday, Size: SizeS, Position: 0, Visible: true},
		{Key: "hero.sales", Shape: WidgetStat, Measure: MetricSalesByBranch, Split: SplitNone,
			Range: RangeToday, Size: SizeL, Position: 1, Visible: true},
	}
	got := WidgetViewsFor(rows, MetricByID)
	if len(got) != 2 {
		t.Fatalf("both widgets must survive the read; got %d, a repaired window must never drop a row", len(got))
	}
	if got[0].Range != RangeOpen {
		t.Errorf("the queue's stored `today` must be repaired to %q, got %q", RangeOpen, got[0].Range)
	}
	// AND THE HONEST ONE IS NOT TOUCHED. sales.today really is a single day, so its stored `today`
	// is correct and must survive verbatim, without this the test would pass against a repair that
	// rewrote every range it saw.
	if got[1].Range != RangeToday {
		t.Errorf("sales.today offers `today` and must keep it, got %q", got[1].Range)
	}
}

// -- the branch narrowing: validated on write, intersected at measurement, never rewritten ------
//
// Ruled 1 Sep 2026. branch_id had been stored on every widget and validated by NOTHING: Normalize
// trimmed it, Validate never looked at it, and no provider read it. 0201's own column comment had
// written the intended rule down and nothing implemented it, "A NARROWING filter, never a grant:
// NULL means 'the branches this caller may see', and the read intersects any value here with the
// caller's own grants."
//
// EVERY GATE BELOW IS ZERO-ASSERTION ON SHIPPED DATA: 0 of 0201's 30 seeded rows carry a branch, so
// each one needs its own positive control to prove it is not passing because there is nothing to
// test.

const branchTrenton = "5d1b3f6e-0000-4000-9000-000000000001"
const branchBelleville = "5d1b3f6e-0000-4000-9000-000000000002"

func TestBranchGate_TheEmptyStringIsAlwaysAllowedForEverybody(t *testing.T) {
	// THE CASE EVERY BRANCH CHECK GETS WRONG. "" is not "no branch", it is "the branches this caller
	// may see", which is what all thirty shipped widgets carry. A gate that refused it would refuse
	// every layout in the product on its first save.
	for name, g := range map[string]BranchGate{
		"a caller with grants": {IDs: []string{branchTrenton}},
		"a global caller":      AnyBranch,
		"a role default":       NoBranch,
		"no grants at all":     {},
	} {
		if !g.Allows("") {
			t.Errorf("%s refused the empty branch", name)
		}
	}
	// And the positive controls, so the line above is not just "everything is allowed".
	if (BranchGate{IDs: []string{branchTrenton}}).Allows(branchBelleville) {
		t.Error("a caller granted Trenton may not narrow to Belleville")
	}
	if NoBranch.Allows(branchTrenton) {
		t.Error("a role default may not name a branch at all")
	}
	if !AnyBranch.Allows(branchTrenton) {
		t.Error("a global caller may narrow to any branch")
	}
	if !(BranchGate{IDs: []string{branchTrenton}}).Allows(branchTrenton) {
		t.Error("a caller may narrow to a branch they hold")
	}
}

func TestIsBranchID_ShapeOnlyAndTheEmptyStringIsFine(t *testing.T) {
	// Without this a typo'd id reaches Postgres, the column is `uuid REFERENCES locations(id)`, and
	// comes back as a 22P02 through the handler's default arm, i.e. a 500 for a bad request.
	for _, ok := range []string{"", branchTrenton, "5D1B3F6E-0000-4000-9000-000000000001"} {
		if !IsBranchID(ok) {
			t.Errorf("IsBranchID(%q) = false", ok)
		}
	}
	for _, bad := range []string{"trenton", "5d1b3f6e-0000-4000-9000-00000000000", "not-a-uuid-at-all-x",
		"5d1b3f6e_0000_4000_9000_000000000001", "5d1b3f6e-0000-4000-9000-00000000000g"} {
		if IsBranchID(bad) {
			t.Errorf("IsBranchID(%q) = true", bad)
		}
	}
	// And Normalize drops the row rather than storing it, the same as any other substrate malformity.
	got := NormalizeWidgets([]Widget{
		{Key: "a.one", Shape: WidgetStat, Measure: MetricOrdersOpen, BranchID: "trenton"},
		{Key: "a.two", Shape: WidgetStat, Measure: MetricOrdersOpen, BranchID: branchTrenton},
	})
	if len(got) != 1 || got[0].Key != "a.two" {
		t.Fatalf("only the well-formed row survives: %+v", got)
	}
}

func TestAWidgetNamingABranchTheCallerWasNotGrantedIsRefused(t *testing.T) {
	s, ws := widgetService(t)
	scope := Scope{UserID: "u1", Profile: ProfileManager, BranchIDs: []string{branchTrenton}}
	_, err := s.SaveMyWidgets(context.Background(), scope,
		[]Widget{withBranch(widget("orders.open", WidgetStat, MetricOrdersOpen, SplitNone, SizeS, 0), branchBelleville)})
	if !errors.Is(err, ErrBranchNotOffered) {
		t.Fatalf("want the branch refusal, got %v", err)
	}
	if ws.writes != 0 {
		t.Fatalf("the store was written %d times for a refused save", ws.writes)
	}
	// THE POSITIVE CONTROL, twice: the branch they DO hold, and no branch at all.
	if _, err := s.SaveMyWidgets(context.Background(), scope,
		[]Widget{withBranch(widget("orders.open", WidgetStat, MetricOrdersOpen, SplitNone, SizeS, 0), branchTrenton)}); err != nil {
		t.Fatalf("a branch the caller holds must save: %v", err)
	}
	if _, err := s.SaveMyWidgets(context.Background(), scope,
		[]Widget{widget("orders.open", WidgetStat, MetricOrdersOpen, SplitNone, SizeS, 0)}); err != nil {
		t.Fatalf("no branch at all must save: %v", err)
	}
	// And the refusal does not name the branch, the same posture as its three siblings, so it
	// cannot be used to ask the server which branches exist.
	if strings.Contains(ErrBranchNotOffered.Error(), branchBelleville) {
		t.Fatalf("the refusal names what it refused: %q", ErrBranchNotOffered)
	}
}

func TestAGlobalCallerMayNarrowToAnyBranch(t *testing.T) {
	s, _ := widgetService(t)
	if _, err := s.SaveMyWidgets(context.Background(),
		Scope{UserID: "u9", Profile: ProfileAdmin, AllBranches: true},
		[]Widget{withBranch(widget("orders.open", WidgetStat, MetricOrdersOpen, SplitNone, SizeS, 0), branchBelleville)}); err != nil {
		t.Fatalf("a Tier-1 global caller holds every branch: %v", err)
	}
}

// A REVOKED GRANT MUST NOT MAKE A PERSON'S WHOLE HOME UNSAVEABLE. A layout is one document, so
// without this every drag after a revocation is a 422 naming no widget, with no way back except
// deleting the card.
func TestABranchAlreadyStoredSurvivesAGrantBeingRevoked(t *testing.T) {
	s, ws := widgetService(t)
	// They saved it while they held Belleville.
	ws.user["u1"] = []Widget{
		withBranch(widget("orders.open", WidgetStat, MetricOrdersOpen, SplitNone, SizeS, 0), branchBelleville),
	}
	// The grant is gone; the row is not.
	scope := Scope{UserID: "u1", Profile: ProfileManager, BranchIDs: []string{branchTrenton}}
	if _, err := s.SaveMyWidgets(context.Background(), scope, ws.user["u1"]); err != nil {
		t.Fatalf("re-saving a layout they already had must not be refused: %v", err)
	}
	// BUT IT GRANTS NOTHING NEW. A DIFFERENT branch they never held is still refused, which is the
	// control that separates "you may keep what you have" from "the gate is off".
	_, err := s.SaveMyWidgets(context.Background(), scope,
		[]Widget{withBranch(widget("orders.open", WidgetStat, MetricOrdersOpen, SplitNone, SizeS, 0),
			"5d1b3f6e-0000-4000-9000-000000000003")})
	if !errors.Is(err, ErrBranchNotOffered) {
		t.Fatalf("a branch never held must still be refused, got %v", err)
	}
}

// A ROLE DEFAULT MAY NOT NARROW TO A BRANCH AT ALL. "Naming is not seeing" carries the MEASURE gate,
// because a reader who may not see a measure loses one card cleanly. It does not carry here: one row
// read by many people with different grants has no honest per-viewer answer.
func TestARoleDefaultMayNotNameABranch(t *testing.T) {
	s, ws := widgetService(t)
	rows := []Widget{
		widget("hero.sales", WidgetStat, MetricSalesByBranch, SplitNone, SizeL, 0),
		widget("needs.you", WidgetList, MeasureNeedsYou, SplitNone, SizeL, 1),
	}
	// THE POSITIVE CONTROL FIRST, so the refusal below cannot be "role defaults never save".
	if _, err := s.SaveProfileWidgets(context.Background(), ProfileYard, rows, "admin-1"); err != nil {
		t.Fatalf("a branchless role default must save: %v", err)
	}
	branched := append([]Widget(nil), rows...)
	branched[0] = withBranch(branched[0], branchTrenton)
	before := ws.writes
	if _, err := s.SaveProfileWidgets(context.Background(), ProfileYard, branched, "admin-1"); !errors.Is(err, ErrBranchNotOffered) {
		t.Fatalf("a role default naming a branch must be refused, got %v", err)
	}
	if ws.writes != before {
		t.Fatal("the store was written for a refused role-default save")
	}
}

// THE READ DOES NOT REWRITE THE BRANCH, and that is deliberate: the client re-PUTs the whole
// resolved layout on every drag and emits branch_id verbatim, so a "repair" here would be a silent
// permanent write to somebody's stored row.
func TestTheLayoutReadLeavesAStoredBranchExactlyAsItIs(t *testing.T) {
	rows := []Widget{
		withBranch(widget("orders.open", WidgetStat, MetricOrdersOpen, SplitNone, SizeS, 0), branchBelleville),
	}
	got := WidgetViewsFor(rows, MetricByID)
	if len(got) != 1 {
		t.Fatalf("the widget must not be dropped: %+v", got)
	}
	if got[0].BranchID != branchBelleville {
		t.Fatalf("the stored branch came back as %q; the read must not touch it", got[0].BranchID)
	}
}
