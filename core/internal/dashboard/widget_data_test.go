package dashboard

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// widget_data_test.go, measuring a widget by its key.
//
// The three defects this route closes, each with its own case below: a client synthesising server
// ids, two widgets sharing one number, and a window that reached no query.

// recordingSeries answers a fixed series and records the Scope it was handed, so a test can assert
// what the SERVER decided to measure rather than what the client asked for.
// recordingSeries answers a fixed series and records the Scope it was handed, so a test can assert
// what the provider saw. WidgetDataFor queries providers concurrently, so the recording is guarded:
// an unsynchronized fake is a data race that loses calls under load and fails CI at random.
type recordingSeries struct {
	mu     sync.Mutex
	points []Point
	err    error
	calls  int
	scopes []Scope
	dims   []string
}

func (r *recordingSeries) Series(_ context.Context, sc Scope, dim string) ([]Point, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.scopes = append(r.scopes, sc)
	r.dims = append(r.dims, dim)
	if r.err != nil {
		return nil, r.err
	}
	return r.points, nil
}

// dataService is a widget service with a fixed clock and a person whose layout is theirs to state.
func dataService(t *testing.T, userRows []Widget) (*Service, *fakeWidgetStore) {
	t.Helper()
	s, ws := widgetService(t)
	at, err := time.Parse(time.RFC3339, "2026-08-12T09:15:00-04:00")
	if err != nil {
		t.Fatal(err)
	}
	s.WithClock(func() time.Time { return at })
	ws.user["u1"] = userRows
	return s, ws
}

func dataScope() Scope {
	return Scope{UserID: "u1", Profile: ProfileManager, CostAllowed: true}
}

func TestWidgetData_MeasuresFromTheStoredRowAndKeysTheAnswerByWidget(t *testing.T) {
	s, _ := dataService(t, []Widget{
		widget("hero.sales", WidgetStat, MetricSalesByBranch, SplitNone, SizeL, 0),
		widget("orders.now", WidgetStat, MetricOrdersOpen, SplitNone, SizeM, 1),
	})
	sales := &recordingSeries{points: []Point{{Label: "Trenton", Number: 400}, {Label: "Belleville", Number: 350}}}
	if err := s.RegisterSeries(MetricSalesByBranch, sales); err != nil {
		t.Fatal(err)
	}

	got := s.WidgetDataFor(context.Background(), dataScope(), []string{"hero.sales", "orders.now"})
	if len(got) != 2 {
		t.Fatalf("got %d data, want 2: %+v", len(got), got)
	}
	if got[0].Key != "hero.sales" || got[0].State != StateOK {
		t.Fatalf("the measured widget: %+v", got[0])
	}
	if got[0].Total == nil || *got[0].Total != 750 {
		t.Fatalf("the total is the SERVER's arithmetic: %+v", got[0])
	}
	// A widget with no provider is PRESENT and unwired, a different answer from absent, which is
	// what lets a client draw an em-dash rather than a hole.
	if got[1].Key != "orders.now" || got[1].State != StateUnwired || got[1].Total != nil {
		t.Fatalf("an unwired widget must be present with no number: %+v", got[1])
	}
	// The window each number was measured over is echoed back, because the client's copy of the row
	// may name one the measure could not answer and the read repairs it.
	if got[0].Range != RangeToday {
		t.Errorf("the echoed window is %q, want the one it was measured over", got[0].Range)
	}
}

// THE FORGED KEY. A key that is not the caller's own is not refused, it is not there. The lookup is
// against their RESOLVED layout, which is read from the store by the context-derived user id, so
// there is nothing to index into rather than something to say no to.
func TestWidgetData_AKeyThatIsNotYoursIsAbsentRatherThanRefused(t *testing.T) {
	s, ws := dataService(t, []Widget{widget("hero.sales", WidgetStat, MetricSalesByBranch, SplitNone, SizeL, 0)})
	ws.user["someone-else"] = []Widget{widget("their.margin", WidgetBars, MetricGrossMargin, DimBranch, SizeM, 0)}
	if err := s.RegisterSeries(MetricGrossMargin, &recordingSeries{points: []Point{{Label: "x", Number: 9}}}); err != nil {
		t.Fatal(err)
	}

	got := s.WidgetDataFor(context.Background(), dataScope(), []string{"their.margin", "hero.sales"})
	if len(got) != 1 || got[0].Key != "hero.sales" {
		t.Fatalf("another person's widget key produced %+v", got)
	}
	for _, d := range got {
		if d.Key == "their.margin" {
			t.Fatal("a forged key was measured")
		}
	}
}

// THE COST GATE REACHES THIS DOOR TOO. A measure byte-absent from the caller's catalog produces no
// entry, even for a key that IS in their own stored rows, a role default may legally name a cost
// measure the reading person may not see.
func TestWidgetData_ACostMeasureIsAbsentForACallerWhoMayNotSeeIt(t *testing.T) {
	rows := []Widget{
		widget("hero.sales", WidgetStat, MetricSalesByBranch, SplitNone, SizeL, 0),
		widget("margin.branch", WidgetBars, MetricGrossMargin, DimBranch, SizeM, 1),
	}
	s, _ := dataService(t, rows)
	margin := &recordingSeries{points: []Point{{Label: "Trenton", Number: 2200}}}
	if err := s.RegisterSeries(MetricGrossMargin, margin); err != nil {
		t.Fatal(err)
	}

	sc := dataScope()
	sc.CostAllowed = false
	got := s.WidgetDataFor(context.Background(), sc, []string{"hero.sales", "margin.branch"})
	for _, d := range got {
		if d.Key == "margin.branch" {
			t.Fatalf("a cost measure reached a caller who may not see it: %+v", d)
		}
	}
	// AND THE PROVIDER WAS NEVER CALLED. Byte-absence gates the display; this proves it also did not
	// gate the act by accident, the measurement never ran, so nothing was computed to be dropped.
	if margin.calls != 0 {
		t.Fatalf("the cost provider ran %d times for a caller who may not see it", margin.calls)
	}
	// THE POSITIVE CONTROL: the same widget for a caller who MAY see it.
	got = s.WidgetDataFor(context.Background(), dataScope(), []string{"margin.branch"})
	if len(got) != 1 || got[0].State != StateOK {
		t.Fatalf("an allowed caller must get the number: %+v", got)
	}
}

// THE DEFECT THE COMPOSED ID COULD NOT EXPRESS. Its grammar is measure-dimension-shape, so two
// widgets that differed only in some OTHER field collapsed to one id, the client de-duplicated them,
// and both drew whichever answer came back. Keyed by widget, they are two measurements.
//
// A NOTE ON WHAT THIS CANNOT YET SHOW, because the honest version of this test is smaller than the
// one I first wrote. I set out to prove two windows on one measure, and could not: the range gate
// admits exactly one window per measure today, so a second row asking for `wtd` on sales.today is
// REPAIRED to `today` on the way out and the two are identical by the time they reach here. The
// structural fix is real and provable, two rows, two queries, each carrying its own resolved
// window, and the day a provider learns a second window this test starts asserting two different
// ones without a line changing.
func TestWidgetData_TwoWidgetsOnOneMeasureAreTwoMeasurements(t *testing.T) {
	rows := []Widget{
		{Key: "sales.hero", Shape: WidgetStat, Measure: MetricSalesByBranch, Split: SplitNone,
			Range: RangeToday, Size: SizeM, Position: 0, Visible: true},
		{Key: "sales.trend", Shape: WidgetTrend, Measure: MetricSalesByBranch, Split: SplitNone,
			Range: RangeToday, Size: SizeM, Position: 1, Visible: true},
	}
	s, _ := dataService(t, rows)
	rec := &recordingSeries{points: []Point{{Label: "Trenton", Number: 1}}}
	if err := s.RegisterSeries(MetricSalesByBranch, rec); err != nil {
		t.Fatal(err)
	}

	got := s.WidgetDataFor(context.Background(), dataScope(), []string{"sales.hero", "sales.trend"})
	if len(got) != 2 {
		t.Fatalf("got %d, want two separate measurements: %+v", len(got), got)
	}
	if rec.calls != 2 {
		t.Fatalf("the provider ran %d times for two widgets on one measure; under the composed id "+
			"they shared ONE query and both drew its answer", rec.calls)
	}
	// EACH CARRIES ITS OWN RESOLVED WINDOW, per widget rather than per measure. 12 August 2026 is a
	// Wednesday, so `today` is the 12th to the 13th in the house zone.
	for i, sc := range rec.scopes {
		if sc.Window.IsZero() {
			t.Fatalf("call %d reached the provider with no window", i)
		}
		if sc.Window.Since.Day() != 12 || sc.Window.Until.Day() != 13 {
			t.Errorf("call %d got %v..%v, want the 12th to the 13th", i, sc.Window.Since, sc.Window.Until)
		}
		if rec.dims[i] != DimBranch {
			t.Errorf("call %d measured dimension %q; sales.today offers only branch", i, rec.dims[i])
		}
	}
	// And each answer says which widget it belongs to, which is the whole reason for the key.
	byKey := map[string]string{}
	for _, d := range got {
		byKey[d.Key] = d.Range
	}
	if byKey["sales.hero"] != RangeToday || byKey["sales.trend"] != RangeToday {
		t.Errorf("the echoed windows are %+v", byKey)
	}
}

// THE WINDOW IS PER WIDGET AND NOT PER REQUEST, which the case above cannot show while every measure
// offers one window. Two widgets on DIFFERENT measures, one a period and one a population: if the
// window were resolved once for the request, both would get the same thing.
func TestWidgetData_TheWindowIsResolvedPerWidgetNotPerRequest(t *testing.T) {
	s, _ := dataService(t, []Widget{
		widget("hero.sales", WidgetStat, MetricSalesByBranch, SplitNone, SizeL, 0),
		widget("ar.aging", WidgetBars, MetricARBalance, DimBucket, SizeL, 1),
	})
	sales := &recordingSeries{points: []Point{{Label: "Trenton", Number: 1}}}
	book := &recordingSeries{points: []Point{{Label: "Current", Number: 2}}}
	if err := s.RegisterSeries(MetricSalesByBranch, sales); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterSeries(MetricARBalance, book); err != nil {
		t.Fatal(err)
	}

	if got := s.WidgetDataFor(context.Background(), dataScope(),
		[]string{"hero.sales", "ar.aging"}); len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	if len(sales.scopes) != 1 || sales.scopes[0].Window.IsZero() {
		t.Fatalf("sales.today names a period and must arrive with one: %+v", sales.scopes)
	}
	if len(book.scopes) != 1 || !book.scopes[0].Window.IsZero() {
		t.Fatalf("the book names a population and must arrive with NO window: %+v", book.scopes)
	}
}

// A WINDOW THAT NAMES A POPULATION REACHES THE PROVIDER AS NOTHING, so a measure with no date
// predicate is not silently handed bounds it would then have to ignore.
func TestWidgetData_APopulationWindowArrivesAsNoWindowAtAll(t *testing.T) {
	s, _ := dataService(t, []Widget{widget("ar.aging", WidgetBars, MetricARBalance, DimBucket, SizeL, 0)})
	rec := &recordingSeries{points: []Point{{Label: "Current", Number: 5}}}
	if err := s.RegisterSeries(MetricARBalance, rec); err != nil {
		t.Fatal(err)
	}
	if got := s.WidgetDataFor(context.Background(), dataScope(), []string{"ar.aging"}); len(got) != 1 {
		t.Fatalf("%+v", got)
	}
	if rec.calls != 1 {
		t.Fatalf("calls = %d", rec.calls)
	}
	if !rec.scopes[0].Window.IsZero() {
		t.Fatalf("the book was handed a window %v..%v; `open` names a population, not a period",
			rec.scopes[0].Window.Since, rec.scopes[0].Window.Until)
	}
	if rec.dims[0] != DimBucket {
		t.Errorf("the widget's split must reach the provider as its dimension, got %q", rec.dims[0])
	}
}

func TestWidgetData_AFailedProviderIsAGapAndNeverThePage(t *testing.T) {
	s, _ := dataService(t, []Widget{widget("hero.sales", WidgetStat, MetricSalesByBranch, SplitNone, SizeL, 0)})
	if err := s.RegisterSeries(MetricSalesByBranch, &recordingSeries{err: errors.New("pool is down")}); err != nil {
		t.Fatal(err)
	}
	got := s.WidgetDataFor(context.Background(), dataScope(), []string{"hero.sales"})
	if len(got) != 1 || got[0].State != StateError {
		t.Fatalf("a failed measurement must be a state, not an absence: %+v", got)
	}
	if got[0].Total != nil {
		t.Errorf("an errored widget must carry NO number, null is not zero: %+v", got[0])
	}
	// The public note says nothing about the module's error, which is in the log instead.
	if got[0].Note != "Temporarily unavailable" {
		t.Errorf("note = %q", got[0].Note)
	}
}

func TestWidgetData_TheReservedQueueIsOmittedRatherThanReportedUnwired(t *testing.T) {
	s, _ := dataService(t, []Widget{
		widget("needs.you", WidgetList, MeasureNeedsYou, SplitNone, SizeL, 0),
		widget("hero.sales", WidgetStat, MetricSalesByBranch, SplitNone, SizeL, 1),
	})
	got := s.WidgetDataFor(context.Background(), dataScope(), []string{"needs.you", "hero.sales"})
	if len(got) != 1 || got[0].Key != "hero.sales" {
		t.Fatalf("needs.you carries no catalog measure and no series, RegisterSeries would refuse "+
			"it, and its body comes from the Needs read. Reporting it `unwired` would tell a client "+
			"to draw an em-dash over a queue that is working: %+v", got)
	}
}

func TestWidgetData_DuplicateKeysAreMeasuredOnce(t *testing.T) {
	s, _ := dataService(t, []Widget{widget("hero.sales", WidgetStat, MetricSalesByBranch, SplitNone, SizeL, 0)})
	rec := &recordingSeries{points: []Point{{Label: "Trenton", Number: 1}}}
	if err := s.RegisterSeries(MetricSalesByBranch, rec); err != nil {
		t.Fatal(err)
	}
	got := s.WidgetDataFor(context.Background(), dataScope(),
		[]string{"hero.sales", "hero.sales", "hero.sales"})
	if len(got) != 1 || rec.calls != 1 {
		t.Fatalf("a repeated key measured %d times into %d results", rec.calls, len(got))
	}
}

func TestSeriesDimension_ASplitIsNotADimension(t *testing.T) {
	ar := MetricByID[MetricARBalance] // dimensions: bucket, branch
	if got := seriesDimension(ar, SplitNone); got != DimBucket {
		t.Errorf("a `none` split falls back to the measure's FIRST dimension, got %q", got)
	}
	if got := seriesDimension(ar, DimBranch); got != DimBranch {
		t.Errorf("a split the measure offers is used verbatim, got %q", got)
	}
	// A split the measure does not offer as a dimension falls back rather than reaching a query that
	// would return nothing.
	if got := seriesDimension(ar, DimStatus); got != DimBucket {
		t.Errorf("an unoffered split must fall back, got %q", got)
	}
	if got := seriesDimension(MetricMeta{}, DimBranch); got != "" {
		t.Errorf("a measure with no dimensions composes nothing, got %q", got)
	}
}

// -- the branch narrowing at MEASUREMENT: it can only ever narrow --------------------------------

func TestNarrowToBranch_TheFourCasesAndTheOneThatMustNeverWiden(t *testing.T) {
	trenton := "5d1b3f6e-0000-4000-9000-000000000001"
	belleville := "5d1b3f6e-0000-4000-9000-000000000002"

	// (a) no narrowing: "" is not "no branch", it is "the branches I may see", exactly the scope the
	// auth guard already resolved, so it is handed back untouched.
	caller := Scope{BranchIDs: []string{trenton, belleville}}
	got, ok := narrowToBranch(caller, "")
	if !ok || len(got.BranchIDs) != 2 || got.AllBranches {
		t.Fatalf("an empty branch must not change the scope: %+v ok=%v", got, ok)
	}

	// (b) a branch the caller holds: exactly that one.
	got, ok = narrowToBranch(caller, trenton)
	if !ok || len(got.BranchIDs) != 1 || got.BranchIDs[0] != trenton || got.AllBranches {
		t.Fatalf("want [trenton] with AllBranches false, got %+v ok=%v", got, ok)
	}

	// (c) THE LINE THAT MATTERS MOST. A Tier-1 global caller narrowing to one branch must LOSE
	// AllBranches. If it survived, w6aBranchScope sends SQL NULL, the branch predicate drops out
	// entirely, and the widget measures the whole company under a caption naming one yard.
	got, ok = narrowToBranch(Scope{AllBranches: true}, trenton)
	if !ok {
		t.Fatal("a global caller holds every branch and may narrow to any of them")
	}
	if got.AllBranches {
		t.Fatal("AllBranches SURVIVED a narrowing, the branch filter would be dropped and the " +
			"widget would measure every branch in the company")
	}
	if len(got.BranchIDs) != 1 || got.BranchIDs[0] != trenton {
		t.Fatalf("want [trenton], got %+v", got.BranchIDs)
	}

	// (d) a branch the caller does NOT hold: not measured at all. Never their own branches, that
	// answers a different question, and never an empty grant list either, which would return zero
	// points and draw a confident "$0.00" for a question they were not allowed to ask.
	if _, ok := narrowToBranch(Scope{BranchIDs: []string{belleville}}, trenton); ok {
		t.Fatal("a branch the caller does not hold must not be measured")
	}
	// A caller with NO grants at all is the same answer, not an accidental pass.
	if _, ok := narrowToBranch(Scope{}, trenton); ok {
		t.Fatal("a caller with no grants may narrow to nothing")
	}
}

func TestWidgetData_AWidgetNarrowedToALostBranchIsAbsentNotZero(t *testing.T) {
	trenton := "5d1b3f6e-0000-4000-9000-000000000001"
	belleville := "5d1b3f6e-0000-4000-9000-000000000002"
	// They saved it while they held Belleville; the grant is gone and the row is not, because the
	// layout read deliberately does not rewrite a stored branch.
	rows := []Widget{
		widgetRanged("sales.belleville", WidgetStat, MetricSalesByBranch, SplitNone, SizeM, RangeToday, 0),
		widget("sales.mine", WidgetStat, MetricSalesByBranch, SplitNone, SizeM, 1),
	}
	rows[0].BranchID = belleville
	s, _ := dataService(t, rows)
	rec := &recordingSeries{points: []Point{{Label: "Trenton", Number: 400}}}
	if err := s.RegisterSeries(MetricSalesByBranch, rec); err != nil {
		t.Fatal(err)
	}

	sc := dataScope()
	sc.BranchIDs = []string{trenton}
	got := s.WidgetDataFor(context.Background(), sc, []string{"sales.belleville", "sales.mine"})

	// A FALSE ZERO IS THE ONE ANSWER WORSE THAN NO ANSWER: measuring with an empty grant list would
	// return zero points, TotalOf would sum them to 0, and the card would draw "$0.00" confidently.
	for _, d := range got {
		if d.Key == "sales.belleville" {
			t.Fatalf("a widget narrowed to a lost branch was measured: %+v", d)
		}
	}
	// THE POSITIVE CONTROL: the sibling with no narrowing still measures, so this is absence and not
	// the whole read having failed.
	if len(got) != 1 || got[0].Key != "sales.mine" || got[0].State != StateOK {
		t.Fatalf("the unnarrowed widget must still measure: %+v", got)
	}
	if rec.calls != 1 {
		t.Fatalf("the provider ran %d times; the lost-branch widget must never reach it", rec.calls)
	}
}

func TestWidgetData_AHeldBranchReachesTheProviderAsThatBranchAlone(t *testing.T) {
	trenton := "5d1b3f6e-0000-4000-9000-000000000001"
	belleville := "5d1b3f6e-0000-4000-9000-000000000002"
	rows := []Widget{
		widgetRanged("sales.trenton", WidgetStat, MetricSalesByBranch, SplitNone, SizeM, RangeToday, 0),
		widget("sales.all", WidgetStat, MetricSalesByBranch, SplitNone, SizeM, 1),
	}
	rows[0].BranchID = trenton
	s, _ := dataService(t, rows)
	rec := &recordingSeries{points: []Point{{Label: "Trenton", Number: 400}}}
	if err := s.RegisterSeries(MetricSalesByBranch, rec); err != nil {
		t.Fatal(err)
	}

	sc := dataScope()
	sc.BranchIDs = []string{trenton, belleville}
	if got := s.WidgetDataFor(context.Background(), sc, []string{"sales.trenton", "sales.all"}); len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	if len(rec.scopes) != 2 {
		t.Fatalf("two widgets, %d measurements", len(rec.scopes))
	}
	var narrowed, wide []string
	for _, s := range rec.scopes {
		if len(s.BranchIDs) == 1 {
			narrowed = s.BranchIDs
		} else {
			wide = s.BranchIDs
		}
	}
	if len(narrowed) != 1 || narrowed[0] != trenton {
		t.Fatalf("the narrowed widget reached the provider with %v", narrowed)
	}
	// AND THE SIBLING IS UNTOUCHED, the narrowing is per widget, not per request, so one branch-scoped
	// card must not quietly re-scope the rest of the Home.
	if len(wide) != 2 {
		t.Fatalf("the unnarrowed widget must keep the caller's full grants, got %v", wide)
	}
}
