package dashboard

// gallery_test.go, the "Add widget" gallery's gates.
//
// The house shape for a byte-absence assertion is used verbatim, as it is in widget_test.go: marshal
// the ALLOWED caller FIRST as a positive control and assert the words ARE present, then the refused
// caller and assert the words, the digits AND the literal string "null" are all absent. A test that
// only asserts absence passes just as happily against an empty response, which is how a broken gate
// ships.

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

// fakeGalleryStore is an in-memory GalleryStore that RECORDS WRITES, for fakeWidgetStore's reason:
// the forged-write test asserts zero rows landed, and "the fake was never called" is the only way to
// prove that without a database.
type fakeGalleryStore struct {
	rows   map[string][]GalleryEntry
	writes int
	err    error
}

func newFakeGalleryStore() *fakeGalleryStore {
	return &fakeGalleryStore{rows: map[string][]GalleryEntry{}}
}

func (f *fakeGalleryStore) ProfileGallery(_ context.Context, p string) ([]GalleryEntry, error) {
	return f.rows[p], f.err
}

func (f *fakeGalleryStore) SaveProfileGallery(_ context.Context, p string, rows []GalleryEntry, _ string) error {
	f.writes++
	f.rows[p] = rows
	return f.err
}

func galleryService(t *testing.T) (*Service, *fakeGalleryStore) {
	t.Helper()
	gs := newFakeGalleryStore()
	s := NewService(newFakeStore(), nil).
		WithMetricStore(&fakeCuration{rows: map[string][]CuratedMetric{}}).
		WithWidgetStore(newFakeWidgetStore()).
		WithGalleryStore(gs)
	return s, gs
}

// entry is a fixture gallery entry with the window its measure actually offers, for `widget`'s
// reason: a test about the MEASURE gate that tripped the RANGE gate would report the wrong refusal.
func entry(key, shape, measure, split, size string, pos int) GalleryEntry {
	return GalleryEntry{Key: key, Shape: shape, Measure: measure, Split: split,
		Range: naturalRange(measure), Size: size, Title: "Fixture", Blurb: "A fixture entry.",
		Position: pos, Visible: true}
}

func entryKeys(cs []GalleryCard) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Key
	}
	return out
}

// -- the read gate: byte-absence -----------------------------------------------------------------

// TestACostBearingGalleryEntryIsByteAbsentOnACountersRead is the read gate, and the reason it is
// sharper here than on the layout read is worth stating: a WIDGET the gate drops leaves a Home one
// card shorter, which is quiet. An OFFER that vanishes is worse, the person who saw it yesterday
// cannot tell whether they imagined it, so it must never have been offered at all.
func TestACostBearingGalleryEntryIsByteAbsentOnACountersRead(t *testing.T) {
	s, gs := galleryService(t)
	// A dealer has curated a cost-bearing entry onto the counter shape. That is a legal thing for an
	// admin to do, curation hides things and never grants anything, and the Go gate is what decides
	// who actually receives it.
	gs.rows[ProfileCounter] = []GalleryEntry{
		entry("orders.count", WidgetStat, MetricOrdersOpen, SplitNone, SizeS, 0),
		entry("inventory.branch", WidgetBars, MetricInventoryValue, DimBranch, SizeM, 1),
		entry("margin.branch", WidgetBars, MetricGrossMargin, DimBranch, SizeM, 2),
	}
	ctx := context.Background()

	// THE POSITIVE CONTROL FIRST. A manager-up caller, filed on the same shape, receives all three -
	// so an absence below is the gate and not an empty store.
	allowed := s.GalleryFor(ctx, Scope{UserID: "u-mgr", Profile: ProfileCounter, CostAllowed: true})
	if got := entryKeys(allowed.Entries); len(got) != 3 {
		t.Fatalf("the allowed caller must receive every entry: %v", got)
	}
	okBody, err := json.Marshal(allowed)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{MetricInventoryValue, MetricGrossMargin, "Inventory value", "Gross margin"} {
		if !strings.Contains(string(okBody), want) {
			t.Fatalf("the positive control does not carry %q, so absence below would prove nothing: %s", want, okBody)
		}
	}

	// THE REFUSED CALLER. A counter clerk: no cost.
	denied := s.GalleryFor(ctx, Scope{UserID: "u-counter", Profile: ProfileCounter, CostAllowed: false})
	if got := entryKeys(denied.Entries); len(got) != 1 || got[0] != "orders.count" {
		t.Fatalf("a counter clerk is offered only what they may see: %v", got)
	}
	body, err := json.Marshal(denied)
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{
		MetricInventoryValue, MetricGrossMargin, "Inventory value", "Gross margin",
		"inventory.branch", "margin.branch", "null",
	} {
		if strings.Contains(string(body), gone) {
			t.Fatalf("the payload still carries %q, absence means ABSENT, not greyed and not null: %s", gone, body)
		}
	}
	// POSITIONS ARE RENUMBERED OVER THE SURVIVORS. A gallery is a grid the browser lays out in order;
	// 0, 2 with a hole at 1 is a question it should not have to answer.
	if denied.Entries[0].Position != 0 {
		t.Fatalf("the surviving entry must be renumbered to 0, got %d", denied.Entries[0].Position)
	}
}

// TestARetiredGalleryEntryIsAbsentRatherThanGreyed. `visible = false` is RETIREMENT: the admin editor
// still sees it (ProfileGalleryFor reads it as stored, so it can be switched back on), and a person
// picking a widget never does.
func TestARetiredGalleryEntryIsAbsentRatherThanGreyed(t *testing.T) {
	s, gs := galleryService(t)
	retired := entry("warranty.list", WidgetList, MetricWarrantyOpen, SplitNone, SizeM, 1)
	retired.Visible = false
	gs.rows[ProfileYard] = []GalleryEntry{
		entry("picks.gauge", WidgetGauge, MetricPickQueue, SplitNone, SizeT, 0),
		retired,
	}
	ctx := context.Background()

	got := s.GalleryFor(ctx, Scope{UserID: "u-yard", Profile: ProfileYard})
	if keys := entryKeys(got.Entries); len(keys) != 1 || keys[0] != "picks.gauge" {
		t.Fatalf("a retired entry must not be offered: %v", keys)
	}
	body, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "warranty.list") {
		t.Fatalf("the retired entry is still on the wire: %s", body)
	}
	// AND THE ADMIN READ STILL SEES IT, or retirement would be indistinguishable from deletion and
	// nobody could switch it back on.
	stored, err := s.ProfileGalleryFor(ctx, ProfileYard)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 {
		t.Fatalf("the admin editor reads the gallery AS STORED, retired entries included: %+v", stored)
	}
}

// TestTheGalleryDegradesToEmptyRatherThanFailing, a build ahead of 0208, and a store that will not
// answer, both give an empty gallery. "Add widget" then falls straight through to "Build your own",
// which is the whole builder, unchanged, and exactly what everybody had before this wave.
func TestTheGalleryDegradesToEmptyRatherThanFailing(t *testing.T) {
	ctx := context.Background()

	unwired := NewService(newFakeStore(), nil).
		WithMetricStore(&fakeCuration{rows: map[string][]CuratedMetric{}})
	got := unwired.GalleryFor(ctx, Scope{UserID: "u1", Profile: ProfileAdmin})
	if got.Entries == nil {
		t.Fatal("an empty gallery is an empty ARRAY, never null")
	}
	if len(got.Entries) != 0 || got.Profile != ProfileAdmin {
		t.Fatalf("a build ahead of 0208 has a working Home with no gallery: %+v", got)
	}

	broken, gs := galleryService(t)
	gs.err = errors.New("gallery table unreadable")
	if got := broken.GalleryFor(ctx, Scope{UserID: "u1", Profile: ProfileAdmin}); len(got.Entries) != 0 {
		t.Fatalf("an unreadable store must still answer: %+v", got)
	}

	// The WRITE is the one path that says so out loud, for ErrNoWidgetStore's reason: an admin whose
	// curation silently went nowhere would have no way to find out.
	if _, err := unwired.SaveProfileGallery(ctx, ProfileAdmin,
		[]GalleryEntry{entry("orders.count", WidgetStat, MetricOrdersOpen, SplitNone, SizeS, 0)}, "u1"); !errors.Is(err, ErrNoGalleryStore) {
		t.Fatalf("an unwired write must say so: %v", err)
	}
}

// -- the write gate ------------------------------------------------------------------------------

// TestAForgedGalleryEntryNamingANonCatalogMeasureIsRefused, the write gate, and the twin assertion
// that ZERO ROWS LAND.
//
// Byte-absence gates the DISPLAY; it was never going to gate the ACT, and a forged PUT does not go
// anywhere near the read path. The whole document is refused, exactly as a forged widget layout is: a
// save that stored the legal five entries of a submission whose sixth was forged would curate a
// gallery nobody asked for and report success.
func TestAForgedGalleryEntryNamingANonCatalogMeasureIsRefused(t *testing.T) {
	s, gs := galleryService(t)
	ctx := context.Background()

	forged := []GalleryEntry{
		entry("orders.count", WidgetStat, MetricOrdersOpen, SplitNone, SizeS, 0),
		{Key: "payroll", Shape: WidgetStat, Measure: "payroll.total", Split: SplitNone,
			Range: RangeOpen, Size: SizeM, Title: "Payroll", Position: 1, Visible: true},
	}
	_, err := s.SaveProfileGallery(ctx, ProfileAdmin, forged, "u-admin")
	if !errors.Is(err, ErrMeasureNotOffered) {
		t.Fatalf("an entry naming a measure this build does not have must be refused, got %v", err)
	}
	if gs.writes != 0 || len(gs.rows[ProfileAdmin]) != 0 {
		t.Fatalf("the store was written for a refused save: %d writes, %+v", gs.writes, gs.rows[ProfileAdmin])
	}

	// A shape that measure does not declare is refused by the same gate, because ValidateGallery IS
	// ValidateWidgets, one implementation, so the gallery and the layout cannot drift into
	// disagreeing about what a legal entry is.
	if _, err := s.SaveProfileGallery(ctx, ProfileAdmin,
		[]GalleryEntry{entry("margin.gauge", WidgetGauge, MetricGrossMargin, SplitNone, SizeT, 0)},
		"u-admin"); !errors.Is(err, ErrShapeNotOffered) {
		t.Fatalf("gross margin offers no gauge; want the shape refusal, got %v", err)
	}
	// And a span off the grid, likewise.
	off := entry("orders.count", WidgetStat, MetricOrdersOpen, SplitNone, SizeS, 0)
	off.Cols, off.Rows = 7, 1
	if _, err := s.SaveProfileGallery(ctx, ProfileAdmin, []GalleryEntry{off}, "u-admin"); !errors.Is(err, ErrSpanNotOffered) {
		t.Fatalf("a gallery entry on a grid line that does not exist must be refused, got %v", err)
	}
	if gs.writes != 0 {
		t.Fatalf("the store was written %d times across three refused saves", gs.writes)
	}

	// THE POSITIVE CONTROL. The legal entry alone saves, so this is a guard and not a wall.
	saved, err := s.SaveProfileGallery(ctx, ProfileAdmin,
		[]GalleryEntry{entry("orders.count", WidgetStat, MetricOrdersOpen, SplitNone, SizeS, 0)}, "u-admin")
	if err != nil || len(saved) != 1 {
		t.Fatalf("a legal gallery must save: %v %+v", err, saved)
	}
}

// TestAGalleryIsCuratedAgainstTheFullCatalogBecauseNamingIsNotSeeing, SaveProfileWidgets' sentence,
// asked of the gallery.
//
// An admin may curate a cost-bearing entry onto any shape. It is not a grant: every read intersects
// the entry's measure with the CALLER's catalog, so a person who may not see it receives a gallery
// without it. Curation hides things; it never grants anything.
func TestAGalleryIsCuratedAgainstTheFullCatalogBecauseNamingIsNotSeeing(t *testing.T) {
	s, gs := galleryService(t)
	ctx := context.Background()

	cost := entry("inventory.branch", WidgetBars, MetricInventoryValue, DimBranch, SizeM, 0)
	if _, err := s.SaveProfileGallery(ctx, ProfileCounter, []GalleryEntry{cost}, "u-admin"); err != nil {
		t.Fatalf("an admin may curate a cost-bearing entry onto any shape: %v", err)
	}
	if len(gs.rows[ProfileCounter]) != 1 {
		t.Fatalf("the entry must be stored: %+v", gs.rows[ProfileCounter])
	}
	// AND IT REACHES NOBODY WHO MAY NOT SEE IT. The row exists and the counter clerk's gallery is
	// empty, which is the entire point of the sentence.
	if got := s.GalleryFor(ctx, Scope{UserID: "u-counter", Profile: ProfileCounter}); len(got.Entries) != 0 {
		t.Fatalf("curation granted something: %+v", got.Entries)
	}
}

// TestAnEmptyGalleryIsAllowedWhereAnEmptyLayoutIsNot pins the one place the gallery deliberately
// parts company with the layout.
//
// An empty personal LAYOUT is refused because stored it is indistinguishable from no override, so the
// person's next login would hand them the role default back and their deliberate act would look
// undone. A gallery has no second layer to be confused with: "this shape is offered nothing
// ready-made, everybody builds their own" is a real decision, it is what the driver ships with, and
// refusing it would leave an admin unable to clear a gallery they had decided against.
func TestAnEmptyGalleryIsAllowedWhereAnEmptyLayoutIsNot(t *testing.T) {
	s, gs := galleryService(t)
	ctx := context.Background()
	gs.rows[ProfileYard] = []GalleryEntry{entry("picks.gauge", WidgetGauge, MetricPickQueue, SplitNone, SizeT, 0)}

	got, err := s.SaveProfileGallery(ctx, ProfileYard, nil, "u-admin")
	if err != nil {
		t.Fatalf("clearing a gallery is a decision, not an error: %v", err)
	}
	if len(got) != 0 || got == nil {
		t.Fatalf("an empty gallery is an empty slice: %+v", got)
	}
	if len(gs.rows[ProfileYard]) != 0 {
		t.Fatalf("the clear must reach the store: %+v", gs.rows[ProfileYard])
	}
	// The layout's rule is unchanged and is asserted next door
	// (TestAnEmptyPersonalLayoutIsRefusedRatherThanStored); this only pins that the two differ on
	// purpose rather than by omission.
	ws := newFakeWidgetStore()
	layout := NewService(newFakeStore(), nil).
		WithMetricStore(&fakeCuration{rows: map[string][]CuratedMetric{}}).WithWidgetStore(ws)
	if _, err := layout.SaveMyWidgets(ctx, Scope{UserID: "u1", Profile: ProfileYard}, nil); !errors.Is(err, ErrEmptyWidgets) {
		t.Fatalf("an empty personal LAYOUT is still refused: %v", err)
	}
}

// TestNormalizeGalleryBoundsTheBlurbAndKeepsTheSurvivorsOwn. The blurb is the one field an entry has
// that a widget does not, so it is the one field NormalizeWidgets cannot bound for it.
func TestNormalizeGalleryBoundsTheBlurbAndKeepsTheSurvivorsOwn(t *testing.T) {
	long := entry("orders.count", WidgetStat, MetricOrdersOpen, SplitNone, SizeS, 0)
	long.Blurb = strings.Repeat("é", 200) // multi-byte on purpose: the CHECK counts characters
	out := NormalizeGallery([]GalleryEntry{long})
	if len(out) != 1 {
		t.Fatalf("an over-long blurb is trimmed, not a dropped entry: %+v", out)
	}
	if n := len([]rune(out[0].Blurb)); n != maxBlurbRune {
		t.Fatalf("the blurb is bounded at %d runes, got %d", maxBlurbRune, n)
	}

	// A DUPLICATE KEY COLLAPSES TO ITS FIRST OCCURRENCE, NormalizeWidgets' rule, and the blurb that
	// comes back must be the SURVIVOR's, not the discarded twin's.
	first := entry("dupe", WidgetStat, MetricOrdersOpen, SplitNone, SizeS, 0)
	first.Blurb = "the survivor"
	second := entry("dupe", WidgetStat, MetricOrdersOpen, SplitNone, SizeM, 1)
	second.Blurb = "the twin"
	got := NormalizeGallery([]GalleryEntry{second, first}) // submitted out of order, deliberately
	if len(got) != 1 {
		t.Fatalf("a duplicate key collapses to one entry: %+v", got)
	}
	if got[0].Blurb != "the survivor" {
		t.Fatalf("the blurb must follow the surviving row, got %q", got[0].Blurb)
	}
}

// TestAGalleryEntryPlacesTheWidgetItPreviews, the preview and the card it becomes must agree, or the
// person taps a 6x2 chart and gets a 3x1 stat.
func TestAGalleryEntryPlacesTheWidgetItPreviews(t *testing.T) {
	e := entry("ar.bucket", WidgetBars, MetricARBalance, DimBucket, SizeL, 0)
	e.Cols, e.Rows = 8, 2

	cards := GalleryCardsFor([]GalleryEntry{e}, MetricByID)
	if len(cards) != 1 {
		t.Fatalf("the entry draws a catalog measure: %+v", cards)
	}
	if cards[0].SpanCols != 8 || cards[0].SpanRows != 2 {
		t.Fatalf("the preview's span is the free span: %dx%d", cards[0].SpanCols, cards[0].SpanRows)
	}
	w := e.AsWidget()
	c, r := SpanFor(w)
	if c != cards[0].SpanCols || r != cards[0].SpanRows {
		t.Fatalf("the placed widget is %dx%d and the preview said %dx%d", c, r, cards[0].SpanCols, cards[0].SpanRows)
	}
	// The placed widget must itself be legal, or tapping a gallery card would 422 the person's layout.
	if err := ValidateWidgets([]Widget{w}, MetricByID, NoBranch); err != nil {
		t.Fatalf("a gallery card places a widget this build refuses: %v", err)
	}
	// It carries NO BRANCH, 0208 has no such column, and NoBranch is what makes that a stated rule
	// rather than an accident of the schema.
	if w.BranchID != "" {
		t.Fatalf("a gallery entry narrows to no branch: %q", w.BranchID)
	}
}

// -- drift gates: 0208 and this build must agree -------------------------------------------------

type seededGalleryEntry struct {
	profile, key, shape, measure, split, timeRange, size, title string
	position                                                    int
}

// seededGalleryEntries reads the rows 0208 seeds. Reading the MIGRATION rather than restating its
// list is this package's own idiom (seededWidgets does the same to 0201, seededMetricIDs to 0182): a
// test carrying its own copy of the data proves only that the copy is consistent.
func seededGalleryEntries(t *testing.T) []seededGalleryEntry {
	t.Helper()
	b, err := os.ReadFile("../../db/migrations/0208_widget_gallery.sql")
	if err != nil {
		t.Fatalf("0208 must be readable from the package: %v", err)
	}
	re := regexp.MustCompile(
		`\(\s*'([a-z]+)',\s*'([a-z0-9._-]+)',\s*'([a-z]+)',\s*'([a-z0-9._]+)',\s*'([a-z]+)',\s*'([a-z0-9_]+)',\s*'(S|T|M|L|XL)',\s*(NULL|\d+),\s*(NULL|\d+),\s*'([^']*)',\s*'((?:[^']|'')*)',\s*(\d+),\s*(true|false)\)`)
	var out []seededGalleryEntry
	for _, m := range re.FindAllStringSubmatch(string(b), -1) {
		pos := 0
		for _, c := range m[12] {
			pos = pos*10 + int(c-'0')
		}
		out = append(out, seededGalleryEntry{
			profile: m[1], key: m[2], shape: m[3], measure: m[4], split: m[5],
			timeRange: m[6], size: m[7], title: m[10], position: pos,
		})
	}
	if len(out) == 0 {
		t.Fatal("0208 seeds no gallery entries, the drift gate would pass vacuously")
	}
	return out
}

func TestSeededGalleryEntriesAreLegalWidgetsInThisBuild(t *testing.T) {
	byProfile := map[string][]seededGalleryEntry{}
	for _, e := range seededGalleryEntries(t) {
		if !IsProfile(e.profile) {
			t.Fatalf("0208 seeds unknown profile %q", e.profile)
		}
		if !IsWidgetKey(e.key) || !IsWidgetShape(e.shape) || !IsWidgetSize(e.size) ||
			!IsSplit(e.split) || !IsRange(e.timeRange) {
			t.Fatalf("0208 seeds an entry outside the closed sets: %+v", e)
		}
		if e.title == "" {
			t.Fatalf("0208 seeds %s/%s with no title; a gallery of cards deriving their names from "+
				"their measures would list the same words three times", e.profile, e.key)
		}
		m, ok := widgetMeasureMeta(e.measure, MetricByID)
		if !ok {
			t.Fatalf("0208 seeds measure %q which this build does not have", e.measure)
		}
		if !containsString(m.WidgetShapes, e.shape) {
			t.Fatalf("0208 seeds %q as %q, a shape that measure does not offer", e.measure, e.shape)
		}
		if !containsString(m.Splits, e.split) {
			t.Fatalf("0208 seeds %q split by %q, which that measure does not offer", e.measure, e.split)
		}
		// THE WINDOW MUST BE ONE THE PROVIDER CAN HONOUR (0205's ruling). A gallery card captioned
		// with a period the number does not measure is the same lie, one screen earlier.
		if len(m.Ranges) > 0 && !containsString(m.Ranges, e.timeRange) {
			t.Fatalf("0208 seeds %q over %q, a window that measure cannot be asked for (it offers %v)",
				e.measure, e.timeRange, m.Ranges)
		}
		byProfile[e.profile] = append(byProfile[e.profile], e)
	}

	// Four shapes with a bento, six entries each. The driver has none, for 0201's stated reason:
	// 0141 takes their Home over entirely, so a gallery nobody can open would be dead configuration.
	if len(byProfile) != 4 {
		t.Fatalf("four job shapes have a gallery (the driver's Home is a takeover, 0141): %d seeded", len(byProfile))
	}
	if _, ok := byProfile[ProfileDriver]; ok {
		t.Fatal("0208 seeds the driver a gallery; there is no bento to add a widget to")
	}
	for profile, es := range byProfile {
		if len(es) != 6 {
			t.Fatalf("%s has %d gallery entries; the gallery is a glance and the seed is six", profile, len(es))
		}
		seen := map[int]bool{}
		for _, e := range es {
			if seen[e.position] {
				t.Fatalf("%s seeds two entries at position %d", profile, e.position)
			}
			seen[e.position] = true
		}
	}
}

// An entry that names a measure its own people are not OFFERED is worse than a role default that does
// the same: a widget the gate drops leaves a Home one card shorter, quietly, but an OFFER that
// vanishes leaves somebody unable to tell whether they imagined it. So 0208's seed is checked against
// 0182's curation as well as against the catalog, the same gate, and the same reading of 0182, that
// TestSeededRoleDefaultsOnlyNameMeasuresThatShapeIsOffered applies to 0201.
func TestSeededGalleryEntriesOnlyNameMeasuresThatShapeIsOffered(t *testing.T) {
	curated := curatedMetricsByProfile(t)
	if len(curated) == 0 {
		t.Fatal("could not read 0182's curation, the gate would pass vacuously")
	}
	for _, e := range seededGalleryEntries(t) {
		if e.measure == MeasureNeedsYou {
			continue // the reserved measure is not curated and never was
		}
		if !curated[e.profile][e.measure] {
			t.Fatalf("0208 offers %q the measure %q, which 0182 does not give that shape", e.profile, e.measure)
		}
	}
}

// curatedMetricsByProfile reads 0182's per-shape metric curation. Extracted from the identical block
// in TestSeededRoleDefaultsOnlyNameMeasuresThatShapeIsOffered so the two gates cannot drift into
// reading the migration two different ways, which is the whole failure mode a drift gate exists to
// catch, and it would be a poor joke to reintroduce it here.
func curatedMetricsByProfile(t *testing.T) map[string]map[string]bool {
	t.Helper()
	b, err := os.ReadFile("../../db/migrations/0182_home_metric_catalog.sql")
	if err != nil {
		t.Fatalf("0182 must be readable: %v", err)
	}
	curated := map[string]map[string]bool{}
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
	return curated
}
