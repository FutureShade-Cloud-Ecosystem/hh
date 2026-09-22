//go:build integration

// Integration cover for the v3.5 grid (0207) and the "Add widget" gallery (0208) against a real
// migrated Postgres. Run with:
//
//	DATABASE_URL=... go test -tags integration -count=1 ./internal/dashboard/
//
// What only a database can prove, and is therefore proved here rather than against the fake:
//   - the CHECK constraints 0207 adds actually refuse an off-snap span and an out-of-range row count,
//     even when the write never goes through the service, the last gate, and the only one a hand
//     -written psql session meets;
//   - the size CHECK now ADMITS 'T' in both tables, which is the whole point of the swap, and still
//     refuses a value outside the five;
//   - 0207 left the seeded role defaults at thirty rows with every widget_key intact, and moved
//     exactly one preset letter, the "count pinned before and after" claim, asked of the rows a
//     login actually reads rather than of the migration's text;
//   - the free span round-trips as NULL/absent through the repository, in both directions, so a
//     person who never touched the grip stores no span and one who did gets theirs back;
//   - 0208 seeded six entries for each of the four bento shapes and none for the driver.
//
// FIXTURES RESTORE WHAT THEY TOUCH, per this package's integration convention. The one test that
// WRITES a gallery writes the DRIVER's, seeded empty on purpose, and deletes it again, so no
// shape whose gallery a person can open is ever disturbed.

package dashboard

import (
	"context"
	"strings"
	"testing"
)

// -- 0207 · the CHECK constraints ----------------------------------------------------------------

// TestIntegration_TheSpanCHECKRefusesAnOffSnapPair is the last gate.
//
// Go refuses an off-grid span with a 422 (ErrSpanNotOffered) and that is where a person meets it. The
// CHECK is what refuses it to everything ELSE, a psql session, a repair script, a future repository
// method that forgot to normalise, and a constraint nobody exercises is a constraint that might have
// been written wrong.
func TestIntegration_TheSpanCHECKRefusesAnOffSnapPair(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	user := syntheticUser(t, pool)

	insert := func(cols, rows any) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO dashboard_user_widgets
			  (user_id, widget_key, shape, measure, split, time_range, size, cols, rows, title, position, visible)
			VALUES ($1::uuid, 'span.probe', 'stat', 'orders.open', 'none', 'open', 'M', $2, $3, '', 0, true)
			ON CONFLICT (user_id, widget_key) DO UPDATE SET cols = EXCLUDED.cols, rows = EXCLUDED.rows`,
			user, cols, rows)
		return err
	}

	// THE POSITIVE CONTROL FIRST, so a refusal below is the constraint and not a broken INSERT.
	if err := insert(8, 2); err != nil {
		t.Fatalf("8x2 is a real grid rectangle and must store: %v", err)
	}
	if err := insert(nil, nil); err != nil {
		t.Fatalf("NULL/NULL is 'the preset decides' and is what every shipped row carries: %v", err)
	}

	for _, tc := range []struct {
		what       string
		cols, rows any
	}{
		{"five columns leaves a seven-column remainder nothing fits", 5, 1},
		{"seven columns, likewise", 7, 2},
		{"eleven columns", 11, 1},
		{"thirteen columns is off the grid entirely", 13, 1},
		{"a fourth row is 560px of one card, which is a page", 6, 4},
		{"zero rows is not a card", 6, 0},
	} {
		if err := insert(tc.cols, tc.rows); err == nil {
			t.Fatalf("the CHECK admitted %v x %v, %s", tc.cols, tc.rows, tc.what)
		}
	}
}

// TestIntegration_TheSizeCHECKAdmitsTallInBothTables. 0207 drops an auto-named constraint and re-adds
// it under the same name; if either half were mistyped the symptom would be a save that works in the
// tests and fails on a real database, which is the worst place to find out.
func TestIntegration_TheSizeCHECKAdmitsTallInBothTables(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	user := syntheticUser(t, pool)

	if _, err := pool.Exec(ctx, `
		INSERT INTO dashboard_user_widgets
		  (user_id, widget_key, shape, measure, split, time_range, size, title, position, visible)
		VALUES ($1::uuid, 'tall.probe', 'gauge', 'picks.queue', 'none', 'open', 'T', '', 0, true)`,
		user); err != nil {
		t.Fatalf("the sixth preset must be storable on a person's layout: %v", err)
	}
	// And still refuses anything outside the five, or the widening would have widened too far.
	if _, err := pool.Exec(ctx, `
		INSERT INTO dashboard_user_widgets
		  (user_id, widget_key, shape, measure, split, time_range, size, title, position, visible)
		VALUES ($1::uuid, 'xxl.probe', 'stat', 'orders.open', 'none', 'open', 'XXL', '', 1, true)`,
		user); err == nil {
		t.Fatal("the size CHECK admitted a value outside the five presets")
	}

	// The profile table's copy of the same constraint, exercised on a throwaway key of a seeded shape
	// and removed again, the row is deleted in cleanup, so no role default is left changed.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM dashboard_profile_widgets WHERE widget_key = 'tall.probe'`)
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO dashboard_profile_widgets
		  (profile, widget_key, shape, measure, split, time_range, size, title, position, visible)
		VALUES ('driver', 'tall.probe', 'gauge', 'picks.queue', 'none', 'open', 'T', '', 0, true)`); err != nil {
		t.Fatalf("the sixth preset must be storable on a role default: %v", err)
	}
}

// TestIntegration_TheRoleDefaultsSurvived0207WithEveryKeyIntact is "count pinned before and after",
// asked of the rows rather than of the arithmetic.
//
// The x3 map is only free if nothing moved, and the only place that is finally true or false is the
// database after every migration has run. Thirty rows, four shapes, every stored size legal in this
// build, every stored span legal, and exactly one preset letter different from 0201's own literal.
func TestIntegration_TheRoleDefaultsSurvived0207WithEveryKeyIntact(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	var total int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM dashboard_profile_widgets`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 30 {
		t.Fatalf("0201 seeds thirty role defaults and 0207 moves none of them; the table holds %d", total)
	}

	repo := widgetRepo(t)
	changed := 0
	for _, profile := range []string{ProfileAdmin, ProfileManager, ProfileYard, ProfileCounter} {
		rows, err := repo.ProfileWidgets(ctx, profile)
		if err != nil {
			t.Fatalf("ProfileWidgets(%s): %v", profile, err)
		}
		for _, w := range rows {
			if !IsWidgetSize(w.Size) {
				t.Fatalf("%s/%s stores size %q, outside the closed set", profile, w.Key, w.Size)
			}
			if !IsWidgetSpan(w.Cols, w.Rows) {
				t.Fatalf("%s/%s stores span %dx%d, off the grid", profile, w.Key, w.Cols, w.Rows)
			}
			if c, r := SpanFor(w); c == 0 || r == 0 {
				t.Fatalf("%s/%s has no footprint on the v3.5 grid", profile, w.Key)
			}
			// Every seeded row is a PRESET, still. 0207 adds the free-span columns and fills none of
			// them: a default authored in spans could not follow the doctrine when it re-rules a size.
			if w.Cols != 0 || w.Rows != 0 {
				t.Fatalf("%s/%s was seeded with a free span %dx%d; role defaults speak presets",
					profile, w.Key, w.Cols, w.Rows)
			}
			if w.Size == SizeT {
				changed++
				if profile != ProfileYard || w.Key != "picks.gauge" {
					t.Fatalf("0207 re-authors ONE row and it is yard/picks.gauge; found %s/%s at Tall",
						profile, w.Key)
				}
				if w.Shape != WidgetGauge {
					t.Fatalf("the Tall row must be the gauge the preset was taken up for, got %q", w.Shape)
				}
			}
		}
		// The committed gates still hold on the migrated rows: a hero at 0 size L, Needs-you at 1.
		if len(rows) < 6 || len(rows) > 9 {
			t.Fatalf("%s has %d widgets; the doctrine's range is 6-9", profile, len(rows))
		}
		if rows[0].Size != SizeL {
			t.Fatalf("%s must still open with a hero at size L: %+v", profile, rows[0])
		}
		if err := ValidateWidgets(rows, MetricByID, AnyBranch); err != nil {
			t.Fatalf("%s seeds a widget this build refuses after 0207: %v", profile, err)
		}
	}
	if changed != 1 {
		t.Fatalf("0207 re-authors exactly one preset letter; %d rows are at Tall", changed)
	}
}

// TestIntegration_APersonalFreeSpanRoundTripsAsNullWhenAbsent proves the NULLIF/COALESCE pair, in
// both directions. Go's 0 means "the preset decides" and the column's way of saying that is NULL; if
// the two halves disagreed, a person who never touched the grip would store a 0x0 that no CHECK
// admits, and every save would fail on a column they have never heard of.
func TestIntegration_APersonalFreeSpanRoundTripsAsNullWhenAbsent(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := widgetRepo(t)
	user := syntheticUser(t, pool)

	in := []Widget{
		{Key: "preset.only", Shape: WidgetStat, Measure: MetricOrdersOpen, Split: SplitNone,
			Range: RangeOpen, Size: SizeL, Position: 0, Visible: true},
		{Key: "dragged", Shape: WidgetBars, Measure: MetricOrdersOpen, Split: DimStatus,
			Range: RangeOpen, Size: SizeM, Cols: 9, Rows: 3, Position: 1, Visible: true},
		{Key: "tall", Shape: WidgetStat, Measure: MetricOrdersOpen, Split: SplitNone,
			Range: RangeOpen, Size: SizeT, Position: 2, Visible: true},
	}
	if err := repo.SaveUserWidgets(ctx, user, in); err != nil {
		t.Fatalf("SaveUserWidgets: %v", err)
	}

	// The row nobody dragged stores NULL, not zero, asked of the column directly, because "0 came
	// back" would be true either way and only one of the two satisfies the CHECK.
	var nulls int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM dashboard_user_widgets
		 WHERE user_id = $1::uuid AND widget_key IN ('preset.only','tall')
		   AND cols IS NULL AND rows IS NULL`, user).Scan(&nulls); err != nil {
		t.Fatal(err)
	}
	if nulls != 2 {
		t.Fatalf("a widget nobody dragged must store NULL/NULL; %d of 2 did", nulls)
	}

	out, err := repo.UserWidgets(ctx, user)
	if err != nil {
		t.Fatalf("UserWidgets: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("three widgets in, %d out: %+v", len(out), out)
	}
	by := map[string]Widget{}
	for _, w := range out {
		by[w.Key] = w
	}
	if w := by["preset.only"]; w.Cols != 0 || w.Rows != 0 {
		t.Fatalf("NULL must read back as 'the preset decides', got %dx%d", w.Cols, w.Rows)
	}
	if w := by["dragged"]; w.Cols != 9 || w.Rows != 3 {
		t.Fatalf("the dragged span must survive the round trip, got %dx%d", w.Cols, w.Rows)
	}
	if w := by["tall"]; w.Size != SizeT {
		t.Fatalf("the sixth preset must survive the round trip, got %q", w.Size)
	}
	// And the footprints the browser is handed are the ones the grid will actually give them.
	views := WidgetViewsFor(out, MetricByID)
	spans := map[string][2]int{}
	for _, v := range views {
		spans[v.Key] = [2]int{v.SpanCols, v.SpanRows}
	}
	for key, want := range map[string][2]int{
		"preset.only": {6, 2}, "dragged": {9, 3}, "tall": {3, 2},
	} {
		if spans[key] != want {
			t.Fatalf("%s is %v on the wire, want %v", key, spans[key], want)
		}
	}
}

// -- 0208 · the gallery --------------------------------------------------------------------------

// TestIntegration_TheGalleryIsSeededSixPerShapeAndNoneForTheDriver, the rows a first login actually
// reads, and the legality of every one of them in this build.
func TestIntegration_TheGalleryIsSeededSixPerShapeAndNoneForTheDriver(t *testing.T) {
	repo := widgetRepo(t)
	ctx := context.Background()

	for _, profile := range []string{ProfileAdmin, ProfileManager, ProfileYard, ProfileCounter} {
		rows, err := repo.ProfileGallery(ctx, profile)
		if err != nil {
			t.Fatalf("ProfileGallery(%s): %v", profile, err)
		}
		if len(rows) != 6 {
			t.Fatalf("%s has %d gallery entries; the gallery is a glance and the seed is six", profile, len(rows))
		}
		for i, e := range rows {
			if e.Position != i {
				t.Fatalf("%s reads in curated order; entry %d is at position %d", profile, i, e.Position)
			}
			if e.Title == "" || e.Blurb == "" {
				t.Fatalf("%s/%s ships without a name or a sentence: %+v", profile, e.Key, e)
			}
			if !IsWidgetSpan(e.Cols, e.Rows) {
				t.Fatalf("%s/%s stores a span off the grid: %dx%d", profile, e.Key, e.Cols, e.Rows)
			}
		}
		// Every entry must place a widget this build will accept, or tapping a gallery card would
		// come back 422 on somebody's whole Home.
		if err := ValidateGallery(rows, MetricByID); err != nil {
			t.Fatalf("%s seeds a gallery entry this build refuses: %v", profile, err)
		}
	}

	// The driver's Home is a takeover (0141), so there is nothing to add a widget to.
	rows, err := repo.ProfileGallery(ctx, ProfileDriver)
	if err != nil {
		t.Fatalf("ProfileGallery(driver): %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("the driver's Home is the run sheet, not a bento: %+v", rows)
	}

	// The one seeded free span, proving the 0207 columns carry all the way through 0208's seed.
	mgr, err := repo.ProfileGallery(ctx, ProfileManager)
	if err != nil {
		t.Fatal(err)
	}
	free := 0
	for _, e := range mgr {
		if e.Cols != 0 || e.Rows != 0 {
			free++
			if e.Cols != 8 || e.Rows != 2 {
				t.Fatalf("manager/%s carries a free span %dx%d; the seed's one override is 8x2",
					e.Key, e.Cols, e.Rows)
			}
		}
	}
	if free != 1 {
		t.Fatalf("the manager gallery seeds exactly one free span; found %d", free)
	}
}

// TestIntegration_SavingAGalleryReplacesItWholesale, one document, DELETE-then-INSERT, so a save can
// never resurrect an entry the admin just removed.
//
// It writes the DRIVER's gallery, which 0208 seeds empty on purpose, and clears it again. No shape
// whose gallery a person can open is touched.
func TestIntegration_SavingAGalleryReplacesItWholesale(t *testing.T) {
	pool := testPool(t)
	repo := widgetRepo(t)
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM dashboard_gallery_entries WHERE profile = 'driver'`)
	})

	three := []GalleryEntry{
		{Key: "a", Shape: WidgetStat, Measure: MetricOrdersOpen, Split: SplitNone, Range: RangeOpen,
			Size: SizeS, Title: "Open orders", Blurb: "One number.", Position: 0, Visible: true},
		{Key: "b", Shape: WidgetBars, Measure: MetricPickQueue, Split: DimStatus, Range: RangeOpen,
			Size: SizeM, Cols: 8, Rows: 2, Title: "Picks", Blurb: "By status.", Position: 1, Visible: true},
		{Key: "c", Shape: WidgetList, Measure: MetricWarrantyOpen, Split: SplitNone, Range: RangeOpen,
			Size: SizeL, Title: "Claims", Blurb: "Oldest first.", Position: 2, Visible: false},
	}
	if err := repo.SaveProfileGallery(ctx, ProfileDriver, three, ""); err != nil {
		t.Fatalf("SaveProfileGallery: %v", err)
	}
	back, err := repo.ProfileGallery(ctx, ProfileDriver)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 3 {
		t.Fatalf("three entries in, %d out: %+v", len(back), back)
	}
	if back[1].Cols != 8 || back[1].Rows != 2 {
		t.Fatalf("the free span must survive the round trip: %dx%d", back[1].Cols, back[1].Rows)
	}
	// A retired entry is READ by the admin path, it has to be, or nobody could switch it back on.
	if back[2].Visible {
		t.Fatalf("visible=false must survive the round trip: %+v", back[2])
	}

	// REPLACED WHOLESALE. One entry back in, and the other two are gone rather than left behind.
	if err := repo.SaveProfileGallery(ctx, ProfileDriver, three[:1], ""); err != nil {
		t.Fatalf("SaveProfileGallery (second): %v", err)
	}
	back, err = repo.ProfileGallery(ctx, ProfileDriver)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 1 || back[0].Key != "a" {
		t.Fatalf("a save that left rows behind would resurrect a removed entry: %+v", back)
	}

	// And an empty save clears it, which is a real curation decision (see SaveProfileGallery).
	if err := repo.SaveProfileGallery(ctx, ProfileDriver, nil, ""); err != nil {
		t.Fatalf("clearing a gallery: %v", err)
	}
	if back, err = repo.ProfileGallery(ctx, ProfileDriver); err != nil || len(back) != 0 {
		t.Fatalf("the clear must reach the table: %+v %v", back, err)
	}
}

// TestIntegration_TheGalleryCHECKsRefuseWhatTheGoGateRefuses, the last gate again, this time on the
// gallery's own table. A constraint nobody exercises is a constraint that might have been written
// wrong, and this one is copied from two other tables.
func TestIntegration_TheGalleryCHECKsRefuseWhatTheGoGateRefuses(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM dashboard_gallery_entries WHERE profile = 'driver'`)
	})

	insert := func(shape, size string, cols, rows any, blurb string) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO dashboard_gallery_entries
			  (profile, entry_key, shape, measure, split, time_range, size, cols, rows, title, blurb, position, visible)
			VALUES ('driver', 'probe', $1, 'orders.open', 'none', 'open', $2, $3, $4, 'Probe', $5, 0, true)
			ON CONFLICT (profile, entry_key) DO UPDATE
			   SET shape = EXCLUDED.shape, size = EXCLUDED.size, cols = EXCLUDED.cols,
			       rows = EXCLUDED.rows, blurb = EXCLUDED.blurb`,
			shape, size, cols, rows, blurb)
		return err
	}

	// THE POSITIVE CONTROL FIRST.
	if err := insert(WidgetStat, SizeT, nil, nil, "A sentence."); err != nil {
		t.Fatalf("a legal entry must store: %v", err)
	}
	if err := insert(WidgetStat, SizeL, 8, 2, "A sentence."); err != nil {
		t.Fatalf("a legal free span must store: %v", err)
	}

	for _, tc := range []struct {
		what, shape, size string
		cols, rows        any
		blurb             string
	}{
		{"a seventh shape is a component and therefore a release", "sparkline", SizeM, nil, nil, "x"},
		{"a sixth preset letter is not one of the five", WidgetStat, "XXL", nil, nil, "x"},
		{"five columns is off the snap set", WidgetStat, SizeM, 5, 1, "x"},
		{"a fourth row is a page", WidgetStat, SizeM, 6, 4, "x"},
		{"a blurb that scrolls is a form with extra steps", WidgetStat, SizeM, nil, nil, strings.Repeat("x", 121)},
	} {
		if err := insert(tc.shape, tc.size, tc.cols, tc.rows, tc.blurb); err == nil {
			t.Fatalf("the gallery CHECKs admitted %q/%q/%v/%v, %s", tc.shape, tc.size, tc.cols, tc.rows, tc.what)
		}
	}
}
