//go:build integration

package dashboard

import (
	"context"
	"testing"
)

// manager_home_profile_integration_test.go, the per-manager-role profiles as SEEDED ROWS (decision
// 5, migration 0381). The unit half pins the vocabulary; this half pins what a login actually
// reads, for the reason TestIntegration_0203_TheDispatcherFilesUnderTheDock gives: the thing being
// pinned is a row, and the failure this guards is a later migration or a hand edit silently
// handing a manager somebody else's Home.
//
// The ruling this file carries, in one sentence each:
//   - every manager role answers its OWN profile, seeded from the role matrix (default.json) and
//     the dashboard's existing profile shape (the base shape's rows, copied);
//   - the yard manager's Home is the first per-role landing: the picks board and the deliveries
//     board (0201's catalog), not the controller's book-first "manager" set;
//   - the dispatcher ruling (0203) keeps its SUBSTANCE: the logistics manager's own profile leads
//     with the day's deliveries, exactly as the dock shape it was born from.

// TestIntegration_0381_EveryManagerRoleAnswersItsOwnProfile pins the role to profile rows for all
// eight manager roles the matrix files under manager-home (default.json: work or manage). Six were
// filed under the shared "manager" shape, the yard manager under "yard", the general manager under
// "admin"; each now has their own.
func TestIntegration_0381_EveryManagerRoleAnswersItsOwnProfile(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	want := map[string]string{
		"BRANCH_MANAGER":       "manager.branch",
		"SALES_MANAGER":        "manager.sales",
		"YARD_MANAGER":         "manager.yard",
		"LOGISTICS_MANAGER":    "manager.logistics",
		"PROCUREMENT_MANAGER":  "manager.procurement",
		"PAYABLES_RECEIVABLES": "manager.payables",
		"FINANCIAL_CONTROLLER": "manager.controller",
		"GENERAL_MANAGER":      "manager.general",
	}
	for role, profile := range want {
		var got string
		if err := pool.QueryRow(ctx,
			`SELECT profile FROM dashboard_role_profiles WHERE role_code = $1`, role).
			Scan(&got); err != nil {
			t.Fatalf("%s has no profile row at all: %v", role, err)
		}
		if got != profile {
			t.Fatalf("%s files under %q, want their own profile %q", role, got, profile)
		}
	}
	// The shapes nobody moved: the counter lead, the yard team, the driver and the platform admin
	// keep the job shapes 0140 filed them under. A migration that re-files a frontline role while
	// building manager profiles would be the 0203 failure again, in the other direction.
	for role, profile := range map[string]string{
		"INSIDE_SALES": ProfileCounter,
		"YARD_TEAM":    ProfileYard,
		"DRIVERS":      ProfileDriver,
		"SUPER_ADMIN":  ProfileAdmin,
	} {
		var got string
		if err := pool.QueryRow(ctx,
			`SELECT profile FROM dashboard_role_profiles WHERE role_code = $1`, role).
			Scan(&got); err != nil {
			t.Fatalf("%s has no profile row: %v", role, err)
		}
		if got != profile {
			t.Errorf("%s was re-filed to %q; the manager profiles must not move a frontline role",
				role, got)
		}
	}
}

// TestIntegration_0381_TheYardManagersHomeIsItsOwn is the red test the brief names: the yard
// manager's home answers its own profile, whose widgets are the picks board and the deliveries by branch
// bars (0201's catalog), NOT the shared "manager" shape's book-first set.
func TestIntegration_0381_TheYardManagersHomeIsItsOwn(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	boards := map[string]bool{}
	rows, err := pool.Query(ctx, `
		SELECT shape, measure FROM dashboard_profile_widgets
		 WHERE profile = $1 ORDER BY position`, "manager.yard")
	if err != nil {
		t.Fatalf("read the yard manager's default: %v", err)
	}
	defer rows.Close()
	var shapes []string
	for rows.Next() {
		var shape, measure string
		if err := rows.Scan(&shape, &measure); err != nil {
			t.Fatalf("scan: %v", err)
		}
		shapes = append(shapes, shape)
		if shape == WidgetBoard {
			boards[measure] = true
		}
	}
	if len(shapes) < 6 {
		t.Fatalf("the yard manager's Home has %d widgets; the doctrine asks 6-9 on a role landing",
			len(shapes))
	}
	// The doctrine's two rules: a hero first, Needs-you second.
	lead, err := pool.Query(ctx, `
		SELECT measure, size FROM dashboard_profile_widgets
		 WHERE profile = $1 AND position IN (0, 1) ORDER BY position`, "manager.yard")
	if err != nil {
		t.Fatalf("read the landing pair: %v", err)
	}
	defer lead.Close()
	var heroMeasure string
	pos := 0
	for lead.Next() {
		var measure, size string
		if err := lead.Scan(&measure, &size); err != nil {
			t.Fatalf("scan: %v", err)
		}
		switch pos {
		case 0:
			if size != SizeL {
				t.Fatalf("the yard manager's hero is size %q; a hero is the first widget at L", size)
			}
			heroMeasure = measure
		case 1:
			if measure != string(MeasureNeedsYou) {
				t.Fatalf("position 1 is %q; Home leads with what needs the person", measure)
			}
		}
		pos++
	}
	if heroMeasure != MetricDeliveriesToday {
		t.Fatalf("the yard manager's hero is %q; their first fact is the day going out, the deliveries",
			heroMeasure)
	}
	// The picks board is the yard manager's board widget; the deliveries widget is the branch split.
	if !boards[MetricPickQueue] {
		t.Error("the yard manager's Home carries no picks board (measure picks.queue, shape board)")
	}
	// The deliveries widget is the branch split the board draws (see the whole-set test below).
	if boards[MetricDeliveriesToday] {
		t.Error("the yard manager's Home carries a status board of deliveries; the board draws the branch split")
	}
}

// TestIntegration_0381_TheOtherManagersKeepTheirShape is the control half: a manager profile is
// born from its base shape's rows, so no manager wakes up on a changed screen. The branch manager
// (born from "manager") keeps the controller's widget set under their own name; the logistics
// manager (born from the dock, 0203's ruling) still leads with the day's deliveries.
func TestIntegration_0381_TheOtherManagersKeepTheirShape(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	sameWidgets := func(t *testing.T, profile, base string) {
		t.Helper()
		var a, b int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM dashboard_profile_widgets w
			   JOIN dashboard_profile_widgets b2
			     ON b2.profile = $2 AND b2.widget_key = w.widget_key
			    AND b2.shape = w.shape AND b2.measure = w.measure AND b2.position = w.position
			 WHERE w.profile = $1`, profile, base).Scan(&a); err != nil {
			t.Fatalf("join %s to %s: %v", profile, base, err)
		}
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM dashboard_profile_widgets WHERE profile = $1`, profile).Scan(&b); err != nil {
			t.Fatalf("count %s: %v", profile, err)
		}
		if a != b || b == 0 {
			t.Fatalf("%s holds %d widget rows of which %d match its base shape %s; the copy must be whole",
				profile, b, a, base)
		}
	}
	sameWidgets(t, "manager.branch", ProfileManager)
	sameWidgets(t, "manager.general", ProfileAdmin)
	// 0203's substance, restated under the new filing: the dispatcher's own profile IS the dock.
	var lead string
	if err := pool.QueryRow(ctx, `
		SELECT measure FROM dashboard_profile_widgets
		 WHERE profile = $1 AND position = 0`, "manager.logistics").Scan(&lead); err != nil {
		t.Fatalf("the logistics manager has no hero: %v", err)
	}
	if lead != MetricDeliveriesToday {
		t.Fatalf("the logistics manager's hero is %q; 0203 ruled the dock leads with the day's "+
			"deliveries, and the per-role filing must not undo that ruling", lead)
	}
	// Tabs ride along: a manager's pinned strip is born from their base shape too, so the landing
	// changes WHO curates it, not WHAT it says on the first morning.
	for _, tc := range []struct{ profile, base string }{
		{"manager.yard", ProfileYard},
		{"manager.branch", ProfileManager},
		{"manager.controller", ProfileManager},
		{"manager.general", ProfileAdmin},
	} {
		var tabs, baseTabs int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM dashboard_profile_tabs WHERE profile = $1`, tc.profile).Scan(&tabs); err != nil {
			t.Fatalf("count tabs %s: %v", tc.profile, err)
		}
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM dashboard_profile_tabs WHERE profile = $1`, tc.base).Scan(&baseTabs); err != nil {
			t.Fatalf("count tabs %s: %v", tc.base, err)
		}
		if tabs != baseTabs {
			t.Errorf("%s pins %d tabs, its base shape %s pins %d; the strip must be born whole",
				tc.profile, tabs, tc.base, baseTabs)
		}
	}
}

// TestIntegration_0381_TheYardManagersNeedsSourcesAreDeclared pins the three sources the roadmap
// names for the yard manager's Needs-you (open shortages raised by the yard team, stale picks,
// receiving differences). The rows are policy; the adapters that count them land with the sources
// themselves, and a source nobody has wired renders nothing (0201's posture), so this asserts the
// DECLARATION, not the count.
func TestIntegration_0381_TheYardManagersNeedsSourcesAreDeclared(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	rows, err := pool.Query(ctx, `
		SELECT source_key, position FROM dashboard_needs_sources
		 WHERE profile = $1 ORDER BY position`, "manager.yard")
	if err != nil {
		t.Fatalf("read the yard manager's needs policy: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var key string
		var position int
		if err := rows.Scan(&key, &position); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, key)
	}
	want := []string{"tasks.open_shortages", "tasks.stale_picks", "purchasing.receiving_differences"}
	if len(got) != len(want) {
		t.Fatalf("the yard manager declares %d needs sources (%v), want the three the roadmap names (%v)",
			len(got), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("needs source %d is %q, want %q", i, got[i], want[i])
		}
	}
}

// TestIntegration_0381_ManagerNeedsPolicyMatchesTheGoFallback keeps the table and its code copy
// from drifting for the refinements, the way the 0201 drift gate does for the job shapes.
func TestIntegration_0381_ManagerNeedsPolicyMatchesTheGoFallback(t *testing.T) {
	repo := widgetRepo(t)
	for _, profile := range managerProfiles {
		want := defaultNeedsFor(profile)
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

// TestIntegration_0381_EveryProfileKeyedTableCarriesEveryManagerProfile pins the §6 copies: no
// manager opens a screen whose curated items, quick actions or gallery have gone missing.
func TestIntegration_0381_EveryProfileKeyedTableCarriesEveryManagerProfile(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	for _, table := range []string{
		"dashboard_profile_tiles", "dashboard_profile_tabs", "dashboard_profile_widgets",
		"dashboard_gallery_entries", "surface_role_items", "quick_action_profile_visibility",
	} {
		for _, p := range managerProfiles {
			var n int
			if err := pool.QueryRow(ctx,
				`SELECT count(*) FROM `+table+` WHERE profile = $1`, p).Scan(&n); err != nil {
				t.Fatalf("count %s for %s: %v", table, p, err)
			}
			if n == 0 {
				t.Errorf("%s holds no rows for %s", table, p)
			}
		}
	}
}

// TestIntegration_0381_TheYardManagersWidgetsAreTheBoardsList pins the whole set, in order, to what
// the yard manager's board draws (design-refs role-boards, manager-home__yard-manager-light): the
// deliveries hero, the pick queue, the deliveries split BY BRANCH as bars, the queue depth gauge,
// open orders and open claims, with Needs you beside the hero. The board's "Unread messages" has no
// measure in 0201's catalog, so it is left to the UI item.
func TestIntegration_0381_TheYardManagersWidgetsAreTheBoardsList(t *testing.T) {
	pool := testPool(t)
	rows, err := pool.Query(context.Background(), `
		SELECT shape, measure, split FROM dashboard_profile_widgets
		 WHERE profile = 'manager.yard' ORDER BY position`)
	if err != nil {
		t.Fatalf("read the yard manager's widgets: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var shape, measure, split string
		if err := rows.Scan(&shape, &measure, &split); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, shape+" "+measure+" "+split)
	}
	want := []string{
		"stat deliveries.today none",
		"list needs.you none",
		"board picks.queue status",
		"bars deliveries.today branch",
		"gauge picks.queue none",
		"stat orders.open none",
		"stat warranty.open_claims none",
	}
	if len(got) != len(want) {
		t.Fatalf("the yard manager's Home holds %d widgets %v, the board draws %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("widget %d is %q, the board draws %q", i, got[i], want[i])
		}
	}
}

// needsOf reads one profile's seeded Needs-you source keys in order.
func needsOf(t *testing.T, profile string) []string {
	t.Helper()
	repo := widgetRepo(t)
	rows, err := repo.NeedsSources(context.Background(), profile)
	if err != nil {
		t.Fatalf("NeedsSources(%s): %v", profile, err)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.SourceKey)
	}
	return out
}

func wantKeys(t *testing.T, profile string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s declares %v, want %v", profile, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s source %d is %q, want %q", profile, i, got[i], want[i])
		}
	}
}

// TestIntegration_0389_TheProcurementManagersNeedsAreTheBuyersOwn pins the seeded rows of 2D gap 3:
// shortages and receiving differences, approvals kept, the AR edge and the parked tickets gone.
func TestIntegration_0389_TheProcurementManagersNeedsAreTheBuyersOwn(t *testing.T) {
	wantKeys(t, "manager.procurement", needsOf(t, "manager.procurement"),
		[]string{NeedsOpenShortages, NeedsReceivingDifferences, NeedsApprovals})
}

// TestIntegration_0389_TheBranchManagerCommitsDeliveries pins the seeded rows of 2D gap 6: the two
// dispatch sources lead, the manager shape's three follow.
func TestIntegration_0389_TheBranchManagerCommitsDeliveries(t *testing.T) {
	wantKeys(t, "manager.branch", needsOf(t, "manager.branch"),
		[]string{NeedsDueToCommit, NeedsUnassigned, NeedsApprovals, NeedsAREscalation, NeedsParkedTickets})
}

// widgetsOf reads one profile's seeded widgets in order, and runs them through the write gate: a
// default the gate would refuse is a hole on first login and a "my Home will not save" later.
func widgetsOf(t *testing.T, profile string) []Widget {
	t.Helper()
	rows, err := widgetRepo(t).ProfileWidgets(context.Background(), profile)
	if err != nil {
		t.Fatalf("ProfileWidgets(%s): %v", profile, err)
	}
	if err := ValidateWidgets(rows, MetricByID, NoBranch); err != nil {
		t.Fatalf("%s: the seeded widgets fail the write gate: %v", profile, err)
	}
	return rows
}

// wantWidgets compares key, shape, measure and split position for position, and that the hero is
// the first widget at L with Needs you beside it (the doctrine's two rules).
func wantWidgets(t *testing.T, profile string, want [][4]string) {
	t.Helper()
	got := widgetsOf(t, profile)
	if len(got) != len(want) {
		t.Fatalf("%s holds %d widgets, want %d", profile, len(got), len(want))
	}
	for i, w := range want {
		g := got[i]
		if g.Key != w[0] || g.Shape != w[1] || g.Measure != w[2] || g.Split != w[3] {
			t.Errorf("%s widget %d is %s/%s/%s/%s, want %s/%s/%s/%s", profile, i,
				g.Key, g.Shape, g.Measure, g.Split, w[0], w[1], w[2], w[3])
		}
	}
	if got[0].Size != SizeL || got[1].Measure != string(MeasureNeedsYou) {
		t.Errorf("%s must lead with a hero at L and Needs you second", profile)
	}
}

// TestIntegration_0389_TheSalesManagersHomeIsAboutSellingAndPricing pins 2D gap 5: sales today is
// the hero, the branch split and the trend say how selling is going, margin by branch and the gross
// margin figure are the pricing facts, and the receivables book is gone (the controller's).
func TestIntegration_0389_TheSalesManagersHomeIsAboutSellingAndPricing(t *testing.T) {
	wantWidgets(t, "manager.sales", [][4]string{
		{"hero.sales", WidgetStat, MetricSalesByBranch, SplitNone},
		{"needs.you", WidgetList, string(MeasureNeedsYou), SplitNone},
		{"sales.branch", WidgetBars, MetricSalesByBranch, DimBranch},
		{"margin.branch", WidgetBars, MetricGrossMargin, DimBranch},
		{"sales.trend", WidgetTrend, MetricSalesByBranch, SplitNone},
		{"orders.status", WidgetBars, MetricOrdersOpen, DimStatus},
		{"orders.open", WidgetStat, MetricOrdersOpen, SplitNone},
		{"margin.gross", WidgetStat, MetricGrossMargin, SplitNone},
	})
}

// TestIntegration_0389_TheProcurementManagersHomeIsStock pins the re-seed half of 2D gap 4: the
// hero is the stock on hand, not the receivables book, built from existing measures only (no
// purchase order measure exists yet).
func TestIntegration_0389_TheProcurementManagersHomeIsStock(t *testing.T) {
	wantWidgets(t, "manager.procurement", [][4]string{
		{"hero.inventory", WidgetStat, MetricInventoryValue, SplitNone},
		{"needs.you", WidgetList, string(MeasureNeedsYou), SplitNone},
		{"inventory.branch", WidgetBars, MetricInventoryValue, DimBranch},
		{"orders.status", WidgetBars, MetricOrdersOpen, DimStatus},
		{"picks.board", WidgetBoard, MetricPickQueue, DimStatus},
		{"picks.gauge", WidgetGauge, MetricPickQueue, SplitNone},
		{"orders.open", WidgetStat, MetricOrdersOpen, SplitNone},
		{"warranty.open", WidgetStat, MetricWarrantyOpen, SplitNone},
	})
}

// TestIntegration_0389_NeitherLandingCarriesTheReceivablesBook keeps the two roles off the book.
func TestIntegration_0389_NeitherLandingCarriesTheReceivablesBook(t *testing.T) {
	for _, p := range []string{"manager.sales", "manager.procurement"} {
		for _, w := range widgetsOf(t, p) {
			if w.Measure == MetricARBalance {
				t.Errorf("%s still carries the receivables book (%s)", p, w.Key)
			}
		}
	}
}

// TestIntegration_0389_TheControllerAndPayablesKeepTheBook is the control: the roles that own the
// book keep the manager shape's whole widget set.
func TestIntegration_0389_TheControllerAndPayablesKeepTheBook(t *testing.T) {
	pool := testPool(t)
	for _, p := range []string{"manager.controller", "manager.payables", "manager.branch"} {
		var n int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM dashboard_profile_widgets w
			   JOIN dashboard_profile_widgets b ON b.profile = 'manager' AND b.widget_key = w.widget_key
			    AND b.shape = w.shape AND b.measure = w.measure AND b.position = w.position
			 WHERE w.profile = $1`, p).Scan(&n); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if n != 8 {
			t.Errorf("%s matches the manager shape on %d of 8 widgets", p, n)
		}
	}
}
