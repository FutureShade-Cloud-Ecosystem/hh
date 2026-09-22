//go:build integration

// Integration cover for the quick-action visibility store (migration 0151) against a real migrated
// Postgres. Run with:
//
//	DATABASE_URL=... go test -tags integration -count=1 ./internal/dashboard/ -run QuickAction
//
// What only a database can prove, and is therefore proved here rather than against the fake:
//   - 0151's SEED is the ruling, in SQL: counter/manager/admin true, yard/driver false, and the
//     action globally on. A Go map would only prove that I can type;
//   - SaveConfig REPLACES the per-profile matrix rather than merging it, so a profile the admin
//     removed from the form does not keep overriding the default from a row nobody can see;
//   - the CHECK constraint refuses a profile outside the five job shapes, so the closed set
//     survives a hand-written INSERT that never goes through the service.
//
// FIXTURES RESTORE WHAT THEY TOUCH: every test snapshots the seeded rows for its action and puts
// them back on cleanup, so this file is order-independent and safe next to the layout suite.

package dashboard

import (
	"context"
	"testing"
)

// restoreQuickAction snapshots an action's stored config and puts it back on cleanup.
func restoreQuickAction(t *testing.T, repo *QuickActionRepository, actionID string) {
	t.Helper()
	ctx := context.Background()
	before, err := repo.Configs(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	cfg, existed := before[actionID]
	t.Cleanup(func() {
		if !existed {
			_, _ = repo.pool.Exec(ctx, `DELETE FROM quick_action_settings WHERE action_id = $1`, actionID)
			_, _ = repo.pool.Exec(ctx, `DELETE FROM quick_action_profile_visibility WHERE action_id = $1`, actionID)
			return
		}
		if err := repo.SaveConfig(ctx, cfg, ""); err != nil {
			t.Errorf("restore %s: %v", actionID, err)
		}
	})
}

func TestIntegration_QuickAction_SeedIsTheRuling(t *testing.T) {
	pool := testPool(t)
	repo := NewQuickActionRepository(pool)

	cfgs, err := repo.Configs(context.Background())
	if err != nil {
		t.Fatalf("Configs: %v", err)
	}
	cfg, ok := cfgs[ActionNewSale]
	if !ok {
		t.Fatalf("0151 did not seed %q, the action would fall back to the code default and no admin could see it", ActionNewSale)
	}
	if !cfg.Enabled {
		t.Error("the shipped default is ON")
	}
	want := map[string]bool{
		ProfileCounter: true, ProfileManager: true, ProfileAdmin: true,
		ProfileYard: false, ProfileDriver: false,
	}
	for profile, expect := range want {
		got, present := cfg.Profiles[profile]
		if !present {
			t.Errorf("profile %q has no seeded row", profile)
			continue
		}
		if got != expect {
			t.Errorf("profile %q: want visible=%v, got %v", profile, expect, got)
		}
	}
	// The resolver over the real rows: a yard login sees nothing, a counter login sees the button.
	if got := ResolveVisible(ActionCatalog, cfgs, ProfileYard); len(got) != 0 {
		t.Errorf("yard must see no quick action against the seeded rows; got %+v", got)
	}
	if got := ResolveVisible(ActionCatalog, cfgs, ProfileCounter); len(got) != 1 {
		t.Errorf("counter must see exactly one quick action against the seeded rows; got %d", len(got))
	}
}

func TestIntegration_QuickAction_SaveReplacesTheMatrix(t *testing.T) {
	pool := testPool(t)
	repo := NewQuickActionRepository(pool)
	ctx := context.Background()
	restoreQuickAction(t, repo, ActionNewSale)

	// Save a matrix that names only ONE shape. The four seeded rows must be gone, not merged.
	if err := repo.SaveConfig(ctx, ActionConfig{
		ActionID: ActionNewSale, Enabled: true,
		Profiles: map[string]bool{ProfileManager: true},
	}, ""); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	cfgs, err := repo.Configs(ctx)
	if err != nil {
		t.Fatalf("Configs: %v", err)
	}
	cfg := cfgs[ActionNewSale]
	if len(cfg.Profiles) != 1 {
		t.Fatalf("the submitted matrix is the WHOLE matrix; %d rows survived: %+v", len(cfg.Profiles), cfg.Profiles)
	}
	// The counter shape now resolves to its CATALOG default (true), not to a stale stored row -
	// which is the difference between "the admin removed the override" and "the admin hid it".
	if got := ResolveVisible(ActionCatalog, cfgs, ProfileCounter); len(got) != 1 {
		t.Errorf("counter should fall back to the catalog default after its row was removed; got %d", len(got))
	}
}

func TestIntegration_QuickAction_GlobalOffHidesEveryShape(t *testing.T) {
	pool := testPool(t)
	repo := NewQuickActionRepository(pool)
	ctx := context.Background()
	restoreQuickAction(t, repo, ActionNewSale)

	// The admin toggle, through the real table: off globally, per-profile rows untouched.
	if err := repo.SaveConfig(ctx, ActionConfig{
		ActionID: ActionNewSale, Enabled: false,
		Profiles: map[string]bool{ProfileCounter: true, ProfileManager: true, ProfileAdmin: true},
	}, ""); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	cfgs, err := repo.Configs(ctx)
	if err != nil {
		t.Fatalf("Configs: %v", err)
	}
	for _, p := range Profiles {
		if got := ResolveVisible(ActionCatalog, cfgs, p); len(got) != 0 {
			t.Errorf("global OFF must hide the action for every shape; %q still saw %+v", p, got)
		}
	}
}

func TestIntegration_QuickAction_CheckRefusesAnUnknownProfile(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx,
		`INSERT INTO quick_action_profile_visibility (action_id, profile, visible)
		 VALUES ('sale.new', 'warehouse', true)`)
	if err == nil {
		_, _ = pool.Exec(ctx,
			`DELETE FROM quick_action_profile_visibility WHERE action_id='sale.new' AND profile='warehouse'`)
		t.Fatal("0151's CHECK let a profile outside the five job shapes in, the closed set has to hold in SQL too")
	}
}
