//go:build integration

// platform_hotkeys_integration_test.go, D-111 against a real migrated Postgres.
//
//	DATABASE_URL=... go test -tags integration -count=1 -run PlatformHotkeys ./internal/settings/
//
// What only a database can prove, and is therefore proved here rather than against a fake:
//   - 0191 actually seeded both rosters, spelled the way each owner's grammar spells them, so the
//     migration and the two code-side floors cannot drift apart without a red test;
//   - the whole-roster write really is whole, an action dropped from the submitted list is GONE,
//     which an UPSERT would have quietly left behind, and unbinding a key would then not happen;
//   - SWAPPING TWO ACTIONS' KEYS SUCCEEDS. This is the one the transaction exists for: the unique
//     index on (surface, binding) would refuse the intermediate state in which both rows hold the
//     same key, so a write that inserted before deleting would fail on the most ordinary edit an
//     administrator can make;
//   - the unique index is really there, so a hand-written row cannot do what the editor refuses;
//   - editing one surface leaves the other alone, the two substrates stay two, in SQL.
//
// FIXTURES RESTORE WHAT THEY TOUCH, per this package's convention: every test that writes snapshots
// the surface's rows first and puts them back on cleanup, so the seeded rosters other suites read
// are unaltered and this file is safe beside them.

package settings

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping settings integration tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// snapshotRoster reads one surface's rows and restores them on cleanup, see the file header.
func snapshotRoster(t *testing.T, store *Store, surface string) {
	t.Helper()
	before, err := store.ReadPlatformHotkeys(context.Background(), surface)
	if err != nil {
		t.Fatalf("snapshot %s: %v", surface, err)
	}
	t.Cleanup(func() {
		if err := store.ReplacePlatformHotkeys(context.Background(), surface, before, ""); err != nil {
			t.Fatalf("restore %s: %v", surface, err)
		}
	})
}

// The 0191 seed, read back through the store. The shell roster must be the five the code floor
// carries and the counter roster the BisTrack five, spelled in each owner's own grammar, which is
// the fact a Go constant could never prove.
func TestIntegration_PlatformHotkeys_TheSeedIsTheTwoShippedFloors(t *testing.T) {
	store := NewStore(intPool(t))
	ctx := context.Background()

	shell, err := store.ReadPlatformHotkeys(ctx, PlatformSurfaceShell)
	if err != nil {
		t.Fatalf("read shell: %v", err)
	}
	wantShell := []PlatformHotkey{
		{Action: "sale.cash.new", Binding: "F1", Position: 0},
		{Action: "sales.tab.new", Binding: "F2", Position: 1},
		{Action: "order.quick.open", Binding: "F3", Position: 2},
		{Action: "search.focus", Binding: "F4", Position: 3},
		{Action: "assist.open", Binding: "F9", Position: 4},
	}
	if !reflect.DeepEqual(shell, wantShell) {
		t.Fatalf("the 0191 app.shell seed = %#v; want the code floor %#v", shell, wantShell)
	}

	counter, err := store.ReadPlatformHotkeys(ctx, PlatformSurfaceCounter)
	if err != nil {
		t.Fatalf("read counter: %v", err)
	}
	wantCounter := []PlatformHotkey{
		{Action: "step-next", Binding: "f4", Position: 0},
		{Action: "step-back", Binding: "shift-f4", Position: 1},
		{Action: "pay", Binding: "f12", Position: 2},
		{Action: "park", Binding: "f9", Position: 3},
		{Action: "search", Binding: "f2", Position: 4},
	}
	if !reflect.DeepEqual(counter, wantCounter) {
		t.Fatalf("the 0191 pos.counter seed = %#v; want the BisTrack floor %#v", counter, wantCounter)
	}

	// F8 IS UNBOUND ON BOTH, at the shipped default, which is the ruling stated in SQL. It used to
	// ring a sale; rebinding it to some other action would turn a trained reflex into the wrong
	// action, and an unbound key teaches nothing while a rebound one teaches a lie.
	for _, row := range append(append([]PlatformHotkey{}, shell...), counter...) {
		if row.Binding == "F8" || row.Binding == "f8" {
			t.Fatalf("F8 is bound to %s, the retired ring key must be left unbound", row.Action)
		}
	}
}

// 0191 lifts the untouched 0164/0171 seed out of the dealer's table, so a fresh deployment has NO
// dealer curation and therefore inherits the platform roster live, which is the ruling's whole
// promise. A row a human wrote (updated_by set) would have survived; there are none, because 0164
// records that no edit endpoint was ever built.
func TestIntegration_PlatformHotkeys_AFreshDeploymentHasNoDealerOverride(t *testing.T) {
	pool := intPool(t)
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM dealer_hotkeys`).Scan(&n); err != nil {
		t.Fatalf("count dealer_hotkeys: %v", err)
	}
	if n != 0 {
		t.Fatalf("dealer_hotkeys holds %d rows on a fresh database, the shipped defaults belong to the platform tier now", n)
	}
}

// WHOLE-OR-ABSENT, IN SQL. An action left out of the submitted list is gone; an UPSERT would have
// left it behind, and unbinding a key would then quietly not happen.
func TestIntegration_PlatformHotkeys_AWriteReplacesTheRosterWholeRatherThanMerging(t *testing.T) {
	store := NewStore(intPool(t))
	ctx := context.Background()
	snapshotRoster(t, store, PlatformSurfaceCounter)

	if err := store.ReplacePlatformHotkeys(ctx, PlatformSurfaceCounter,
		[]PlatformHotkey{{Action: "pay", Binding: "f12", Position: 0}}, ""); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := store.ReadPlatformHotkeys(ctx, PlatformSurfaceCounter)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Action != "pay" {
		t.Fatalf("the write must replace the roster whole; got %#v", got)
	}
}

// THE TEST THE TRANSACTION EXISTS FOR. Swapping two actions' keys is the most ordinary edit an
// administrator makes, and it is precisely the edit the unique index on (surface, binding) would
// refuse if the write inserted before deleting: for one instant both rows would hold the same key.
// Delete-then-insert inside one transaction means that instant never exists.
func TestIntegration_PlatformHotkeys_TwoActionsMaySWAPTheirKeysInOneWrite(t *testing.T) {
	store := NewStore(intPool(t))
	ctx := context.Background()
	snapshotRoster(t, store, PlatformSurfaceCounter)

	if err := store.ReplacePlatformHotkeys(ctx, PlatformSurfaceCounter, []PlatformHotkey{
		{Action: "step-next", Binding: "f4", Position: 0},
		{Action: "park", Binding: "f9", Position: 1},
	}, ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Now swap them. If this errors, the write is not transactional and the editor is broken for
	// the commonest edit there is.
	if err := store.ReplacePlatformHotkeys(ctx, PlatformSurfaceCounter, []PlatformHotkey{
		{Action: "step-next", Binding: "f9", Position: 0},
		{Action: "park", Binding: "f4", Position: 1},
	}, ""); err != nil {
		t.Fatalf("swapping two actions' keys must succeed in one write: %v", err)
	}
	got, err := store.ReadPlatformHotkeys(ctx, PlatformSurfaceCounter)
	if err != nil {
		t.Fatal(err)
	}
	want := []PlatformHotkey{
		{Action: "step-next", Binding: "f9", Position: 0},
		{Action: "park", Binding: "f4", Position: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("after the swap = %#v; want %#v", got, want)
	}
}

// The unique index is the floor under the service's in-words refusal: a hand-written row cannot do
// what the editor refuses to do, because a roster where two actions claim one key resolves by row
// order and drops the loser silently.
func TestIntegration_PlatformHotkeys_TheDatabaseRefusesTwoActionsOnOneKey(t *testing.T) {
	pool := intPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	snapshotRoster(t, store, PlatformSurfaceCounter)

	if err := store.ReplacePlatformHotkeys(ctx, PlatformSurfaceCounter,
		[]PlatformHotkey{{Action: "pay", Binding: "f12", Position: 0}}, ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO platform_hotkeys (surface, action_id, binding, position) VALUES ($1, 'park', 'f12', 1)`,
		PlatformSurfaceCounter)
	if err == nil {
		t.Fatal("the unique index must refuse a second action on one key, however the row arrives")
	}
	// And an unknown surface cannot be smuggled in either, the CHECK keeps the substrate closed.
	if _, err := pool.Exec(ctx,
		`INSERT INTO platform_hotkeys (surface, action_id, binding, position) VALUES ('quotes.builder', 'x', 'F7', 0)`,
	); err == nil {
		t.Fatal("the surface CHECK must refuse a roster this feature does not own")
	}
}

// THE TWO SUBSTRATES STAY TWO, IN SQL. Editing the counter's roster must not touch the app-wide
// map, which is why the screen writes them separately and the store scopes every statement to a
// surface.
func TestIntegration_PlatformHotkeys_EditingOneSurfaceLeavesTheOtherAlone(t *testing.T) {
	store := NewStore(intPool(t))
	ctx := context.Background()
	snapshotRoster(t, store, PlatformSurfaceCounter)

	before, err := store.ReadPlatformHotkeys(ctx, PlatformSurfaceShell)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReplacePlatformHotkeys(ctx, PlatformSurfaceCounter,
		[]PlatformHotkey{{Action: "pay", Binding: "f11", Position: 0}}, ""); err != nil {
		t.Fatalf("write: %v", err)
	}
	after, err := store.ReadPlatformHotkeys(ctx, PlatformSurfaceShell)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("the shell roster changed when the counter's was written:\n before %#v\n after  %#v", before, after)
	}
}

// The role-items DELETE that makes "Reset to the platform roster" a real reset. Scoped to a KIND,
// because ReplaceRoleItems' contract is per-kind whole-or-absent: a DELETE that ignored kind would
// take the hot list and the panel defaults with the keys.
func TestIntegration_PlatformHotkeys_ResettingTheKeysLeavesTheRestOfTheSurfaceCurated(t *testing.T) {
	store := NewStore(intPool(t))
	ctx := context.Background()
	const surface = "pos.counter.itemtest"
	const profile = "counter"
	t.Cleanup(func() {
		for _, kind := range []string{KindAction, KindChip} {
			_ = store.DeleteRoleItems(ctx, surface, profile, kind)
		}
	})

	if err := store.ReplaceRoleItems(ctx, surface, profile, []Item{
		{Kind: KindAction, ID: "pay.f10", Position: 0, Visible: true},
		{Kind: KindChip, ID: "sku-1", Position: 0, Visible: true},
	}, ""); err != nil {
		t.Fatalf("seed curation: %v", err)
	}
	if err := store.DeleteRoleItems(ctx, surface, profile, KindAction); err != nil {
		t.Fatalf("reset: %v", err)
	}
	got, err := store.ReadRoleItems(ctx, surface, profile)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != KindChip {
		t.Fatalf("the reset must clear the keys and leave every other kind alone; got %#v", got)
	}
}

// - item 4c · the effective roster, against the real curation table -

// The cross-tier refusal is the one rule in this feature that reads TWO substrates in one breath -
// the platform roster and the dealer's own counter curation, and a fake can only prove that the
// rule is written correctly, never that the id encoding it decodes is the one the counter editor
// actually writes. So this seeds a row in the shape pos/hotkeys.go documents (`<action>.<key>`), on
// the REAL pos.counter surface, and asks the service the question a platform operator's keypress
// asks.
func TestIntegration_PlatformHotkeys_ADealerOverrideIsSeenByThePlatformRule(t *testing.T) {
	pool := intPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	const profile = "counter"

	// Restore whatever this dealer had, per this file's convention, other suites read these rows.
	before, err := store.ReadRoleItems(ctx, PlatformSurfaceCounter, profile)
	if err != nil {
		t.Fatalf("snapshot curation: %v", err)
	}
	t.Cleanup(func() {
		_ = store.DeleteRoleItems(ctx, PlatformSurfaceCounter, profile, KindAction)
		if len(before) > 0 {
			_ = store.ReplaceRoleItems(ctx, PlatformSurfaceCounter, profile, before, "")
		}
	})

	if err := store.ReplaceRoleItems(ctx, PlatformSurfaceCounter, profile, []Item{
		{Kind: KindAction, ID: "pay.f10", Position: 0, Visible: true},
	}, ""); err != nil {
		t.Fatalf("seed the dealer override: %v", err)
	}

	svc := platformSvc(store, superAdmin())
	dealer, err := svc.DealerRosterFor(ctx, PlatformSurfaceCounter)
	if err != nil {
		t.Fatalf("DealerRosterFor: %v", err)
	}
	found := false
	for _, d := range dealer {
		if d.Action == "pay" && d.Binding == "f10" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the dealer's own key must be visible to the platform rule; got %+v", dealer)
	}

	// And the write that would give F10 to a DIFFERENT action is refused, in words, naming the key -
	// before the store is touched, which the roster read below confirms.
	err = svc.PutPlatformHotkeys(ctx, PlatformSurfaceCounter, []PlatformHotkey{
		{Action: "park", Binding: "f10"},
	})
	if err == nil {
		t.Fatal("a platform binding that fights the dealer's own key must be refused")
	}
	if !strings.Contains(err.Error(), "f10") {
		t.Fatalf("the refusal must name the key; got %q", err.Error())
	}
	keys, err := svc.PlatformRosterFor(ctx, PlatformSurfaceCounter)
	if err != nil {
		t.Fatalf("PlatformRosterFor: %v", err)
	}
	if len(keys) != 5 {
		t.Fatalf("the shipped roster must be untouched by a refused write; got %+v", keys)
	}

	// The SHELL roster is unaffected by the counter's dealer tier, the two substrates stay two even
	// through a rule that reads both tables.
	shellDealer, err := svc.DealerRosterFor(ctx, PlatformSurfaceShell)
	if err != nil {
		t.Fatalf("DealerRosterFor(shell): %v", err)
	}
	if len(shellDealer) != 0 {
		t.Fatalf("the counter's curation must not leak into the shell tier; got %+v", shellDealer)
	}
}
