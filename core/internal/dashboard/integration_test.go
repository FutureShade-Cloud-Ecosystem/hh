//go:build integration

// Integration cover for the cockpit layout store (migrations 0140/0141) against a real migrated
// Postgres. Run with:
//
//	DATABASE_URL=... go test -tags integration -count=1 ./internal/dashboard/
//
// What only a database can prove, and is therefore proved here rather than against the fake:
//   - the 0140 seed actually maps every shipped role code to a profile, a Go map would only prove
//     that I can type;
//   - SaveUserTiles REPLACES rather than merges, so a shorter layout does not leave a resurrected
//     tile behind (the failure mode an upsert would have);
//   - ClearUserTiles, reset-to-role, lands on the byte-identical role layout, in SQL;
//   - the 0141 CHECK refuses a takeover with no landing unit, so the rule survives a hand-written
//     INSERT that never goes through the service.
//
// FIXTURES RESTORE WHAT THEY TOUCH. The user tests use a synthetic user row this file creates and
// deletes; the profile test snapshots the profile's rows and puts them back on cleanup. No seeded
// row is left altered, so this file is order-independent and safe next to another suite.

package dashboard

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping dashboard integration tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return pool
}

// syntheticUser creates a throwaway ACTIVE user and removes it (and anything FK-cascaded from it)
// on cleanup, so no seeded person's layout is ever written by a test.
func syntheticUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()
	var id string
	err := pool.QueryRow(ctx, `
		INSERT INTO users (email, display_name, status)
		VALUES ('w6a-dashboard-fixture-' || gen_random_uuid() || '@example.invalid', 'W6A Fixture', 'ACTIVE')
		RETURNING id::text`).Scan(&id)
	if err != nil {
		t.Fatalf("create fixture user: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1::uuid`, id); err != nil {
			t.Errorf("fixture cleanup (user %s): %v", id, err)
		}
	})
	return id
}

// Every role the platform ships must resolve to a profile. An unmapped role still WORKS (the
// service falls back to counter) but it means somebody added a role and nobody decided what their
// home screen is, which is a decision that should be made on purpose.
func TestIntegration_EveryShippedRoleMapsToAProfile(t *testing.T) {
	pool := testPool(t)
	rows, err := pool.Query(context.Background(), `
		SELECT r.code::text, p.profile
		  FROM roles r LEFT JOIN dashboard_role_profiles p ON p.role_code = r.code
		 WHERE r.is_active
		 ORDER BY r.code`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var unmapped []string
	seen := 0
	for rows.Next() {
		var code string
		var profile *string
		if err := rows.Scan(&code, &profile); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen++
		if profile == nil {
			unmapped = append(unmapped, code)
			continue
		}
		if !IsProfile(*profile) {
			t.Fatalf("role %s maps to %q, which is not a known profile", code, *profile)
		}
	}
	if seen == 0 {
		t.Fatal("no active roles found, is this database migrated and seeded?")
	}
	if len(unmapped) > 0 {
		t.Fatalf("roles with no dashboard profile: %v", unmapped)
	}
}

func TestIntegration_SaveUserTilesReplacesRatherThanMerges(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	uid := syntheticUser(t, pool)

	if err := repo.SaveUserTiles(ctx, uid, []LayoutRow{
		{TileID: TileSalesToday, Position: 0, Visible: true},
		{TileID: TileOrdersOpen, Position: 1, Visible: true},
		{TileID: TileARAging, Position: 2, Visible: true},
	}); err != nil {
		t.Fatalf("first save: %v", err)
	}
	// A SHORTER layout. An upsert would leave ar.aging behind and the tile would reappear on the
	// user's home screen after they removed it, the exact bug this shape is chosen to prevent.
	if err := repo.SaveUserTiles(ctx, uid, []LayoutRow{
		{TileID: TileOrdersOpen, Position: 0, Visible: true},
	}); err != nil {
		t.Fatalf("second save: %v", err)
	}
	got, err := repo.UserTiles(ctx, uid)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(got) != 1 || got[0].TileID != TileOrdersOpen {
		t.Fatalf("layout after replace = %+v, want exactly [orders.open]", got)
	}
}

func TestIntegration_ResetToRoleLandsOnTheRoleLayout(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo, nil)
	ctx := context.Background()
	uid := syntheticUser(t, pool)

	roleLayout, err := svc.Resolve(ctx, Scope{UserID: uid, Profile: ProfileYard})
	if err != nil {
		t.Fatalf("resolve role layout: %v", err)
	}
	if roleLayout.Source != SourceRole {
		t.Fatalf("a fresh user resolved as %q, want %q", roleLayout.Source, SourceRole)
	}

	if _, err := svc.SaveMyLayout(ctx, uid, []LayoutRow{
		{TileID: TileMessagingUnread, Position: 0, Visible: true},
		{TileID: TilePickQueue, Position: 1, Visible: false},
	}); err != nil {
		t.Fatalf("SaveMyLayout: %v", err)
	}
	mine, err := svc.Resolve(ctx, Scope{UserID: uid, Profile: ProfileYard})
	if err != nil {
		t.Fatalf("resolve override: %v", err)
	}
	if mine.Source != SourceUser || mine.Tiles[0].ID != TileMessagingUnread {
		t.Fatalf("override did not take: source=%q first=%q", mine.Source, mine.Tiles[0].ID)
	}

	n, err := svc.ResetMyLayout(ctx, uid)
	if err != nil {
		t.Fatalf("ResetMyLayout: %v", err)
	}
	if n != 2 {
		t.Fatalf("reset cleared %d rows, want 2", n)
	}
	back, err := svc.Resolve(ctx, Scope{UserID: uid, Profile: ProfileYard})
	if err != nil {
		t.Fatalf("resolve after reset: %v", err)
	}
	if back.Source != SourceRole {
		t.Fatalf("after reset source = %q, want %q", back.Source, SourceRole)
	}
	if len(back.Tiles) != len(roleLayout.Tiles) {
		t.Fatalf("after reset %d tiles, want %d", len(back.Tiles), len(roleLayout.Tiles))
	}
	for i := range roleLayout.Tiles {
		if back.Tiles[i].ID != roleLayout.Tiles[i].ID || back.Tiles[i].Visible != roleLayout.Tiles[i].Visible {
			t.Fatalf("tile %d after reset = %s/%v, want %s/%v", i,
				back.Tiles[i].ID, back.Tiles[i].Visible,
				roleLayout.Tiles[i].ID, roleLayout.Tiles[i].Visible)
		}
	}
}

func TestIntegration_SaveProfileTilesReplacesAndRestores(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	before, err := repo.ProfileTiles(ctx, ProfileDriver)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	t.Cleanup(func() {
		if err := repo.SaveProfileTiles(context.Background(), ProfileDriver, before, ""); err != nil {
			t.Errorf("restore driver profile: %v", err)
		}
	})

	if err := repo.SaveProfileTiles(ctx, ProfileDriver, []LayoutRow{
		{TileID: TileWeightPending, Position: 0, Visible: true},
	}, ""); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := repo.ProfileTiles(ctx, ProfileDriver)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(got) != 1 || got[0].TileID != TileWeightPending {
		t.Fatalf("profile rows = %+v, want exactly [weight.pending]", got)
	}
}

// The 0141 CHECK is the copy of the takeover rule that survives a hand-written INSERT, a driver's
// phone must never land on a blank screen because someone bypassed the service.
func TestIntegration_TakeoverWithoutALandingUnitIsRefusedByTheSchema(t *testing.T) {
	pool := testPool(t)
	_, err := pool.Exec(context.Background(), `
		INSERT INTO dashboard_profile_home (profile, landing_unit, takeover)
		VALUES ('counter', NULL, true)
		ON CONFLICT (profile) DO UPDATE SET landing_unit = NULL, takeover = true`)
	if err == nil {
		// Undo it if the constraint is missing, so a failure here does not poison the database.
		_, _ = pool.Exec(context.Background(),
			`UPDATE dashboard_profile_home SET takeover = false, landing_unit = NULL WHERE profile = 'counter'`)
		t.Fatal("the schema accepted a takeover with no landing unit")
	}
}

func TestIntegration_HomeConfigRoundTrips(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	before, err := repo.Home(ctx, ProfileYard)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	t.Cleanup(func() {
		if err := repo.SaveHome(context.Background(), ProfileYard, before, ""); err != nil {
			t.Errorf("restore yard home: %v", err)
		}
	})

	want := HomeConfig{LandingUnit: "yard", LandingModule: "yard", Takeover: true, MobileOnly: false}
	if err := repo.SaveHome(ctx, ProfileYard, want, ""); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := repo.Home(ctx, ProfileYard)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.LandingUnit != want.LandingUnit || got.LandingModule != want.LandingModule ||
		got.Takeover != want.Takeover || got.MobileOnly != want.MobileOnly {
		t.Fatalf("home = %+v, want %+v", got, want)
	}
}

// -- DIB-433 · the pinned strip, against real SQL (0149) ----------------------------------------

// Every profile the platform ships must have a pinned strip seeded, and the ADMIN strip must be the
// LEANEST of the desk profiles. That second assertion is the ruling, encoded: privilege is not
// workload, and the failure mode it guards against, "the GM has the most rights so give them every
// tab", is the one a future edit will reach for by instinct.
func TestIntegration_SeededStripsAreLeanBySeniority(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	counts := map[string]int{}
	for _, p := range Profiles {
		pins, err := repo.ProfileTabs(ctx, p)
		if err != nil {
			t.Fatalf("ProfileTabs(%s): %v", p, err)
		}
		if len(pins) == 0 {
			t.Fatalf("profile %q has no seeded pinned tabs, every job shape gets a strip", p)
		}
		for _, pin := range pins {
			if !ValidTabKey(pin.TabKey) {
				t.Fatalf("profile %q pins %q, which is not a deep-link body", p, pin.TabKey)
			}
		}
		counts[p] = len(pins)
	}
	if counts[ProfileAdmin] > counts[ProfileCounter] {
		t.Fatalf("admin strip (%d pins) is bigger than the counter strip (%d), "+
			"privilege is not workload; the counter is the role that lives in three screens all day",
			counts[ProfileAdmin], counts[ProfileCounter])
	}
	if counts[ProfileAdmin] > 4 {
		t.Fatalf("admin strip has %d pins; the ruling caps leadership at three or four", counts[ProfileAdmin])
	}
}

func TestIntegration_SaveUserTabsReplacesRatherThanMerges(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	uid := syntheticUser(t, pool)

	if err := repo.SaveUserTabs(ctx, uid, []TabPin{
		{TabKey: "sales/pos", Position: 0, Visible: true},
		{TabKey: "sales/catalogue", Position: 1, Visible: true},
		{TabKey: "warranty/warranty", Position: 2, Visible: true},
	}); err != nil {
		t.Fatalf("first save: %v", err)
	}
	// A SHORTER set. An upsert would leave warranty behind and the tab would reappear in the strip
	// after the person unpinned it.
	if err := repo.SaveUserTabs(ctx, uid, []TabPin{{TabKey: "sales/pos", Position: 0, Visible: true}}); err != nil {
		t.Fatalf("second save: %v", err)
	}
	got, err := repo.UserTabs(ctx, uid)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(got) != 1 || got[0].TabKey != "sales/pos" {
		t.Fatalf("pins after replace = %+v, want exactly [sales/pos]", got)
	}
}

func TestIntegration_ResetToRoleLandsOnTheRoleStrip(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo, nil)
	ctx := context.Background()
	uid := syntheticUser(t, pool)

	role, err := repo.ProfileTabs(ctx, ProfileCounter)
	if err != nil {
		t.Fatalf("role strip: %v", err)
	}
	if _, err := svc.SaveMyTabs(ctx, uid, []TabPin{{TabKey: "warranty/warranty", Visible: true}}); err != nil {
		t.Fatalf("SaveMyTabs: %v", err)
	}
	if _, err := svc.ResetMyTabs(ctx, uid); err != nil {
		t.Fatalf("ResetMyTabs: %v", err)
	}
	strip, err := svc.TabsFor(ctx, ProfileCounter, uid)
	if err != nil {
		t.Fatalf("TabsFor: %v", err)
	}
	if strip.Source != SourceRole {
		t.Fatalf("source = %q after reset, want %q", strip.Source, SourceRole)
	}
	if len(strip.Pins) != len(role) {
		t.Fatalf("reset landed on %d pins, want the role's %d", len(strip.Pins), len(role))
	}
	for i := range role {
		if strip.Pins[i].TabKey != role[i].TabKey {
			t.Fatalf("pin %d = %q, want %q", i, strip.Pins[i].TabKey, role[i].TabKey)
		}
	}
}

// The CHECK is the copy of the shape rule the database can enforce on its own, for a row that
// never went through the service.
func TestIntegration_MalformedTabKeyIsRefusedBySQL(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	uid := syntheticUser(t, pool)
	_, err := pool.Exec(ctx,
		`INSERT INTO dashboard_user_tabs (user_id, tab_key, position) VALUES ($1::uuid, $2, 0)`,
		uid, "#sales/pos/nonsense")
	if err == nil {
		t.Fatal("a malformed tab_key was accepted by the database")
	}
}
