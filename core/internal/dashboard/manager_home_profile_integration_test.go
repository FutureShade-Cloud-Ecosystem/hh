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
// manager's home answers its own profile, whose widgets are the picks board and the deliveries
// board (0201's catalog), NOT the shared "manager" shape's book-first set.
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
	// The brief's own words: its widgets are the picks board and the deliveries board.
	if !boards[MetricPickQueue] {
		t.Error("the yard manager's Home carries no picks board (measure picks.queue, shape board)")
	}
	if !boards[MetricDeliveriesToday] {
		t.Error("the yard manager's Home carries no deliveries board (measure deliveries.today, shape board)")
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
