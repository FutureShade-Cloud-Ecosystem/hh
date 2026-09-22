//go:build integration

// Integration cover for the widget substrate (0201) and the composed-chart carry-over (0202)
// against a real migrated Postgres. Run with:
//
//	DATABASE_URL=... go test -tags integration -count=1 ./internal/dashboard/
//
// What only a database can prove, and is therefore proved here rather than against the fake:
//   - 0201 actually seeded four role defaults, and they are the layouts this build considers legal
//     (a Go map would only prove that I can type);
//   - 0202's ACCEPTANCE CLAUSE, nobody loses a chart they built, as a BEFORE/AFTER COUNT over
//     seeded composed tiles, with the migrated widget ids named one by one;
//   - 0202 leaves an unparseable composed id exactly where it is rather than guessing at it;
//   - 0202 is a no-op on a second run, which is what makes the count above re-assertable;
//   - the CHECK constraints refuse a shape and a size outside the closed sets even when the write
//     never goes through the service;
//   - reset-to-role-default is a DELETE, in SQL, landing on the byte-identical role layout.
//
// FIXTURES RESTORE WHAT THEY TOUCH, per this package's integration convention: every test uses a
// synthetic user whose rows cascade away with them, and nothing here writes a seeded profile's rows.

package dashboard

import (
	"context"
	"os"
	"strings"
	"testing"
)

// runMigration executes a migration file's body against the pool. The runner wraps each file in its
// own transaction and this does not, which is fine, and is exactly why these files carry no
// BEGIN/COMMIT of their own.
func runMigration(t *testing.T, name string) {
	t.Helper()
	b, err := os.ReadFile("../../db/migrations/" + name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if _, err := testPool(t).Exec(context.Background(), string(b)); err != nil {
		t.Fatalf("apply %s: %v", name, err)
	}
}

func widgetRepo(t *testing.T) *WidgetRepository { return NewWidgetRepository(testPool(t)) }

// -- 0201 ----------------------------------------------------------------------------------------

// The seeded role defaults, read back through the repository: four job shapes, each opening with a
// hero and Needs-you, each 6-9 widgets, each row legal in this build. The pure test asserts the
// same facts about the migration TEXT; this asserts them about the rows Postgres actually holds,
// which is the only thing a login reads.
func TestIntegration_WidgetRoleDefaultsAreSeededAndLegal(t *testing.T) {
	repo := widgetRepo(t)
	ctx := context.Background()
	for _, profile := range []string{ProfileAdmin, ProfileManager, ProfileYard, ProfileCounter} {
		rows, err := repo.ProfileWidgets(ctx, profile)
		if err != nil {
			t.Fatalf("ProfileWidgets(%s): %v", profile, err)
		}
		if len(rows) < 6 || len(rows) > 9 {
			t.Fatalf("%s has %d seeded widgets; the doctrine's range is 6-9", profile, len(rows))
		}
		if rows[0].Position != 0 || rows[0].Size != SizeL {
			t.Fatalf("%s must open with a hero at position 0, size L: %+v", profile, rows[0])
		}
		if rows[1].Measure != MeasureNeedsYou {
			t.Fatalf("%s must carry Needs-you at position 1: %+v", profile, rows[1])
		}
		if err := ValidateWidgets(rows, MetricByID, AnyBranch); err != nil {
			t.Fatalf("%s seeds a widget this build refuses: %v", profile, err)
		}
	}
	// The driver's Home is a takeover (0141), so it has no bento and that is deliberate.
	rows, err := repo.ProfileWidgets(ctx, ProfileDriver)
	if err != nil {
		t.Fatalf("ProfileWidgets(driver): %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("the driver's Home is the run sheet, not a bento: %+v", rows)
	}
}

func TestIntegration_NeedsSourcePolicyIsSeededForEveryShapeWithAHome(t *testing.T) {
	repo := widgetRepo(t)
	for profile, want := range DefaultNeedsSources {
		got, err := repo.NeedsSources(context.Background(), profile)
		if err != nil {
			t.Fatalf("NeedsSources(%s): %v", profile, err)
		}
		if len(got) != len(want) {
			t.Fatalf("%s: %d seeded sources, %d shipped", profile, len(got), len(want))
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%s row %d: database %+v, code %+v", profile, i, got[i], want[i])
			}
		}
	}
}

// A CHECK constraint is only worth having if it survives a write that never goes through the
// service. These two do.
func TestIntegration_TheSubstrateRefusesAShapeAndASizeOutsideTheClosedSets(t *testing.T) {
	pool := testPool(t)
	user := syntheticUser(t, pool)
	ctx := context.Background()
	for _, tc := range []struct{ name, shape, size string }{
		{"a shape nobody built a component for", "scatter", SizeM},
		{"a size that is not a bento span", WidgetStat, "XXL"},
	} {
		_, err := pool.Exec(ctx, `
			INSERT INTO dashboard_user_widgets (user_id, widget_key, shape, measure, size, position)
			VALUES ($1::uuid, 'forged.one', $2, 'orders.open', $3, 0)`, user, tc.shape, tc.size)
		if err == nil {
			t.Fatalf("%s must be refused by the schema itself", tc.name)
		}
	}
}

// -- 0202 · the acceptance clause, as a before/after count ---------------------------------------

// THE PROOF THE LANE ASKS FOR. Seed N composed tiles, migrate, assert N widget rows and NAME THE
// IDS. Two of the seeded ids are deliberately unparseable: they are not migrated, they are not
// guessed at, and they are still in surface_user_items afterwards.
func TestIntegration_0202CarriesEveryComposedChartAndLosesNothing(t *testing.T) {
	pool := testPool(t)
	user := syntheticUser(t, pool)
	ctx := context.Background()

	// N = 3 well-formed composed tiles. The third is HIDDEN, because a chart somebody switched off
	// is still a chart they built and its visibility must carry across too.
	carried := []string{
		"viz.ar.aging-bucket-split",
		"viz.orders.open-status-bar",
		"viz.picks.queue-branch-list",
	}
	// ...and three rows that must NOT become widgets: a dimension the metric does not offer, a
	// metric this build does not have, and somebody else's panel id entirely.
	leftAlone := []string{
		"viz.ar.aging-customer-split",
		"viz.nope.metric-branch-bar",
		"notes.pinned",
	}
	for i, id := range append(append([]string{}, carried...), leftAlone...) {
		visible := id != "viz.picks.queue-branch-list"
		if _, err := pool.Exec(ctx, `
			INSERT INTO surface_user_items (user_id, surface_id, item_kind, item_id, position, visible)
			VALUES ($1::uuid, 'home.dashboard', 'panel', $2, $3, $4)`, user, id, i, visible); err != nil {
			t.Fatalf("seed composed tile %s: %v", id, err)
		}
	}

	var before int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM dashboard_user_widgets WHERE user_id = $1::uuid`, user).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before != 0 {
		t.Fatalf("BEFORE: a fresh person has %d widget rows, want 0", before)
	}

	runMigration(t, "0202_widget_carryover.sql")

	var afterCharts int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM dashboard_user_widgets
		 WHERE user_id = $1::uuid AND widget_key LIKE 'viz.%'`, user).Scan(&afterCharts); err != nil {
		t.Fatal(err)
	}
	if afterCharts != len(carried) {
		t.Fatalf("AFTER: %d composed tiles in, %d widget rows out, a chart was lost",
			len(carried), afterCharts)
	}

	// NAME THE IDS. A count alone would pass if the migration invented three widgets of its own.
	rows, err := NewWidgetRepository(pool).UserWidgets(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]Widget{}
	for _, w := range rows {
		byKey[w.Key] = w
	}
	for i, id := range carried {
		w, ok := byKey[id]
		if !ok {
			t.Fatalf("the chart %q did not become a widget", id)
		}
		if w.Position != i {
			t.Fatalf("%q landed at position %d, want %d, migrated charts open the layout", id, w.Position, i)
		}
		if w.Shape != WidgetBars || w.Size != SizeM || w.Range != RangeOpen || w.Title != "" {
			t.Fatalf("%q carried across wrong: %+v", id, w)
		}
		if !strings.HasPrefix(id, VizPrefix+w.Measure+"-"+w.Split+"-") {
			t.Fatalf("%q kept the wrong measure/split: %+v", id, w)
		}
	}
	if byKey["viz.picks.queue-branch-list"].Visible {
		t.Fatal("a chart somebody had switched off must arrive switched off")
	}
	for _, id := range leftAlone {
		if _, migrated := byKey[id]; migrated {
			t.Fatalf("%q does not parse into the closed sets and must NOT have been migrated", id)
		}
	}

	// ...and the ids that were not migrated are still exactly where they were. Nothing is deleted.
	var stillThere int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM surface_user_items
		 WHERE user_id = $1::uuid AND surface_id = 'home.dashboard' AND item_kind = 'panel'`,
		user).Scan(&stillThere); err != nil {
		t.Fatal(err)
	}
	if stillThere != len(carried)+len(leftAlone) {
		t.Fatalf("0202 removed %d source rows; it must delete nothing",
			len(carried)+len(leftAlone)-stillThere)
	}

	// NOBODY LOSES THEIR NEEDS-YOU EITHER. The role default is materialised beneath the charts, once.
	if _, ok := byKey["needs.you"]; !ok {
		t.Fatalf("the role default must land beneath the charts: %d rows, keys %v", len(rows), keysOf(rows))
	}
	if len(rows) <= len(carried) {
		t.Fatalf("only the charts survived; the hero and the queue were lost: %v", keysOf(rows))
	}

	// RE-RUN IS A NO-OP. This is what lets the count above be re-asserted on a database that has
	// already been migrated, and it is the property the runner's exactly-once bookkeeping should not
	// be the only thing guaranteeing.
	runMigration(t, "0202_widget_carryover.sql")
	again, err := NewWidgetRepository(pool).UserWidgets(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != len(rows) {
		t.Fatalf("a second run added %d rows; it must add none", len(again)-len(rows))
	}
}

func keysOf(ws []Widget) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.Key
	}
	return out
}

// -- reset, in SQL -------------------------------------------------------------------------------

// Reset-to-role-default is a DELETE and the role default answers again, proved on rows rather than
// on a map, because the failure this guards against (a reset that MATERIALISES the defaults into the
// person's level) is invisible in the response and only visible in the table.
func TestIntegration_WidgetResetIsADeleteAndLandsOnTheRoleDefault(t *testing.T) {
	pool := testPool(t)
	repo := NewWidgetRepository(pool)
	user := syntheticUser(t, pool)
	ctx := context.Background()

	svc := NewService(NewRepository(pool), nil).WithWidgetStore(repo)
	scope := Scope{UserID: user, Profile: ProfileCounter, CostAllowed: false}

	roleDefault := svc.WidgetsFor(ctx, scope)
	if roleDefault.Source != SourceRole || len(roleDefault.Widgets) == 0 {
		t.Fatalf("a person with no rows gets the role default: %+v", roleDefault.Source)
	}

	if _, err := svc.SaveMyWidgets(ctx, scope, []Widget{
		widget("orders.trend", WidgetTrend, MetricOrdersOpen, SplitNone, SizeM, 0),
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	mine := svc.WidgetsFor(ctx, scope)
	if mine.Source != SourceUser || len(mine.Widgets) != 1 {
		t.Fatalf("the personal layout is the WHOLE layout: %+v", mine)
	}

	n, err := svc.ResetMyWidgets(ctx, user)
	if err != nil || n != 1 {
		t.Fatalf("reset cleared %d rows (err %v), want 1", n, err)
	}
	var left int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM dashboard_user_widgets WHERE user_id = $1::uuid`, user).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("RESET IS A DELETE: %d rows were written into the person's level (0191's one-way door)", left)
	}
	back := svc.WidgetsFor(ctx, scope)
	if back.Source != SourceRole || len(back.Widgets) != len(roleDefault.Widgets) {
		t.Fatalf("the role default must answer again, byte for byte: %+v", back)
	}
	for i := range back.Widgets {
		if back.Widgets[i] != roleDefault.Widgets[i] {
			t.Fatalf("widget %d differs after reset: %+v vs %+v", i, back.Widgets[i], roleDefault.Widgets[i])
		}
	}
}

// The forged-write twin, in SQL: a counter caller's hand-crafted save naming a cost measure is
// refused and ZERO rows land in the table. The unit test proves the store is never called; this
// proves the table is empty afterwards, which is the fact that actually matters.
func TestIntegration_AForgedCostWidgetWriteLandsZeroRows(t *testing.T) {
	pool := testPool(t)
	user := syntheticUser(t, pool)
	ctx := context.Background()
	svc := NewService(NewRepository(pool), nil).WithWidgetStore(NewWidgetRepository(pool))

	_, err := svc.SaveMyWidgets(ctx,
		Scope{UserID: user, Profile: ProfileCounter, CostAllowed: false},
		[]Widget{
			widget("orders.open", WidgetStat, MetricOrdersOpen, SplitNone, SizeS, 0),
			widget("margin.branch", WidgetBars, MetricGrossMargin, DimBranch, SizeM, 1),
		})
	if err == nil {
		t.Fatal("a forged cost widget must be refused server-side")
	}
	var landed int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM dashboard_user_widgets WHERE user_id = $1::uuid`, user).Scan(&landed); err != nil {
		t.Fatal(err)
	}
	if landed != 0 {
		t.Fatalf("%d rows landed from a refused save; byte-absence gates the display, this gates the act", landed)
	}
}

// -- 0205: every stored window is one its measure can actually be asked for ---------------------
//
// The unit half (widget_test.go) proves 0205's truth list agrees with the catalog and that 0201's
// seeds are legal once corrected. Neither can see the row that matters most.
//
// 0202's carry-over hard-codes 'open' for EVERY composed chart it converts, including sales.today
// and deliveries.today, whose one window is `today`, and §2 copies each profile default's window
// verbatim into personal rows. But 0202 plants nothing from zero: no migration ever seeds
// surface_user_items with a composed id, so there is no chart for it to carry. Every row it creates
// exists only on a live or staging database, which is exactly the row a from-zero CI run cannot see
// and a dealer looks at every morning.
func TestIntegration_0205_NoStoredWindowIsOneItsMeasureCannotAnswer(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	for _, table := range []string{"dashboard_profile_widgets", "dashboard_user_widgets"} {
		rows, err := pool.Query(ctx,
			`SELECT measure, time_range, COUNT(*) FROM `+table+` GROUP BY 1, 2 ORDER BY 1, 2`)
		if err != nil {
			t.Fatalf("read %s: %v", table, err)
		}
		seen := 0
		for rows.Next() {
			var measure, window string
			var n int
			if err := rows.Scan(&measure, &window, &n); err != nil {
				t.Fatalf("scan: %v", err)
			}
			seen += n
			m, ok := widgetMeasureMeta(measure, MetricByID)
			if !ok {
				// A measure this build has retired. Byte-absent on read, so it is not a lie on any
				// screen, but say so, because it is the other reason a row can look wrong.
				t.Logf("%s carries %d row(s) on retired measure %q", table, n, measure)
				continue
			}
			if !containsString(m.Ranges, window) {
				t.Errorf("%s: %d row(s) measure %q over window %q, which that measure cannot be "+
					"asked for (it offers %v). The number is right and the caption under it is not, "+
					"0205 should have corrected this", table, n, measure, window, m.Ranges)
			}
		}
		rows.Close()
		if table == "dashboard_profile_widgets" && seen < 30 {
			t.Fatalf("%s holds %d rows; 0201 seeds thirty, so this scan has drifted and every "+
				"assertion above would pass vacuously", table, seen)
		}
	}
}

// AND THE ROLE DEFAULTS SAVE BACK. The regression that made this migration non-optional: the admin
// role-default editor reads profile rows RAW, so nothing repairs them on the way out, and its save
// runs the write gate. Read them the way that screen does and put them straight back.
func TestIntegration_0205_ASeededRoleDefaultSurvivesTheEditorsGetThenPut(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	profiles := []string{"admin", "manager", "yard", "counter"}
	for _, profile := range profiles {
		rows, err := pool.Query(ctx,
			`SELECT widget_key, shape, measure, split, branch_id, time_range, size, title, position, visible
			   FROM dashboard_profile_widgets WHERE profile = $1 ORDER BY position`, profile)
		if err != nil {
			t.Fatalf("read %s: %v", profile, err)
		}
		var got []Widget
		for rows.Next() {
			var w Widget
			var branch *string
			if err := rows.Scan(&w.Key, &w.Shape, &w.Measure, &w.Split, &branch,
				&w.Range, &w.Size, &w.Title, &w.Position, &w.Visible); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if branch != nil {
				w.BranchID = *branch
			}
			got = append(got, w)
		}
		rows.Close()
		if len(got) < 6 {
			t.Fatalf("%s has %d seeded widgets; the doctrine asks for 6-9 on a role landing", profile, len(got))
		}
		// THE FULL CATALOG, because that is what SaveProfileWidgets gates a role default against -
		// a role default may legally name a cost measure the reading admin cannot see.
		if err := ValidateWidgets(NormalizeWidgets(got), MetricByID, AnyBranch); err != nil {
			t.Errorf("the %s role default cannot be saved back unchanged (%d rows): %v", profile, len(got), err)
		}
	}
}
