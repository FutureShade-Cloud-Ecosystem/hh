package dashboard

import (
	"context"
	"testing"
)

// -- DIB-500: payments-today is DEFAULT-PRESENT for leadership, and only for leadership ----------
//
// The mechanism under test is ApplyDefaultOn, the one narrow exception to "a tile the layer does
// not name is appended hidden". These tests pin its three load-bearing properties: silence shows
// the card (for the two leadership shapes), an explicit removal stays removed, and nobody outside
// those shapes is affected on any path.

// The catalog declaration itself: leadership shapes only. A test on data, because this map IS the
// role gate for the defaulting and a typo here would hand the card to the yard.
func TestCatalog_PaymentsTodayDefaultOnIsLeadershipOnly(t *testing.T) {
	meta := CatalogByID[TilePaymentsToday]
	if !meta.DefaultOnProfiles[ProfileManager] || !meta.DefaultOnProfiles[ProfileAdmin] {
		t.Fatal("payments.today must be default-on for manager and admin, the owner asked for it every day, without setup")
	}
	for _, p := range []string{ProfileCounter, ProfileYard, ProfileDriver} {
		if meta.DefaultOnProfiles[p] {
			t.Fatalf("payments.today must NOT be default-on for %q", p)
		}
	}
	// No OTHER tile may quietly adopt the mechanism: default-on overrides a person's saved layout,
	// which is only justified per-ruling, per-tile. Widening this list is a decision, and this test
	// is where it gets made on purpose.
	for _, m := range Catalog {
		if m.ID != TilePaymentsToday && len(m.DefaultOnProfiles) > 0 {
			t.Fatalf("tile %q declares DefaultOnProfiles without a ruling, see DIB-500 before widening", m.ID)
		}
	}
}

// THE TICKET'S CENTRE: a manager's personal override saved BEFORE the card shipped, a stored
// layout that cannot name it, gets the card appended VISIBLE, not hidden.
func TestApplyDefaultOn_SilentLayerShowsTheCardForLeadership(t *testing.T) {
	preCard := []LayoutRow{
		{TileID: TileSalesToday, Position: 0, Visible: true},
		{TileID: TileARAging, Position: 1, Visible: true},
	}
	slots, source := ResolveLayout(Catalog, CatalogByID, nil, preCard)
	if source != SourceUser {
		t.Fatalf("precondition: the user layer must be decisive, got %q", source)
	}
	slots = ApplyDefaultOn(slots, DecisiveLayer(nil, preCard), ProfileManager)
	assertTileVisible(t, slots, TilePaymentsToday, true,
		"a leadership layout that is SILENT about the card must get it visible")
	// And it must not have dragged any OTHER unnamed tile into view with it.
	assertTileVisible(t, slots, TileStalePicks, false,
		"default-on is per-tile, an unnamed tile without the declaration stays hidden")
}

// EXPLICIT REMOVAL WINS AND PERSISTS: a stored row with visible=false is a decision, and the
// defaulting never touches a named row. This is the property that makes the card removable at all.
func TestApplyDefaultOn_AnExplicitRemovalStaysRemoved(t *testing.T) {
	removed := []LayoutRow{
		{TileID: TileSalesToday, Position: 0, Visible: true},
		{TileID: TilePaymentsToday, Position: 1, Visible: false}, // the person switched it OFF
	}
	slots, _ := ResolveLayout(Catalog, CatalogByID, nil, removed)
	slots = ApplyDefaultOn(slots, DecisiveLayer(nil, removed), ProfileManager)
	assertTileVisible(t, slots, TilePaymentsToday, false,
		"a row naming the card hidden is an explicit removal and must be honoured")
}

// The counter/yard/driver shapes are untouched: silence about the card still means hidden.
func TestApplyDefaultOn_NonLeadershipShapesAreUnaffected(t *testing.T) {
	yard := []LayoutRow{{TileID: TilePickQueue, Position: 0, Visible: true}}
	for _, profile := range []string{ProfileCounter, ProfileYard, ProfileDriver} {
		slots, _ := ResolveLayout(Catalog, CatalogByID, yard, nil)
		slots = ApplyDefaultOn(slots, DecisiveLayer(yard, nil), profile)
		assertTileVisible(t, slots, TilePaymentsToday, false,
			"profile "+profile+" must not gain the card by default")
	}
}

// DecisiveLayer must mirror ResolveLayout's precedence exactly, or ApplyDefaultOn would ask
// "was the tile named?" about a layer that did not decide the layout. The failure that guards
// against: a user override AND a profile row both exist, the profile names the card hidden, the
// user layer is silent, the user layer decided, so the card defaults ON.
func TestApplyDefaultOn_AsksTheLayerThatDecided(t *testing.T) {
	profile := []LayoutRow{
		{TileID: TileSalesToday, Position: 0, Visible: true},
		{TileID: TilePaymentsToday, Position: 1, Visible: false}, // an admin hid it at role level…
	}
	user := []LayoutRow{{TileID: TileSalesToday, Position: 0, Visible: true}} // …but MY layout decided, and is silent
	slots, source := ResolveLayout(Catalog, CatalogByID, profile, user)
	if source != SourceUser {
		t.Fatalf("precondition: the user layer must be decisive, got %q", source)
	}
	slots = ApplyDefaultOn(slots, DecisiveLayer(profile, user), ProfileManager)
	assertTileVisible(t, slots, TilePaymentsToday, true,
		"the profile row belongs to a layer that did not decide this layout, silence in MY layer defaults the card on")

	// And the mirror image: no user rows → the profile layer decided, its hidden row is explicit.
	slots2, _ := ResolveLayout(Catalog, CatalogByID, profile, nil)
	slots2 = ApplyDefaultOn(slots2, DecisiveLayer(profile, nil), ProfileManager)
	assertTileVisible(t, slots2, TilePaymentsToday, false,
		"when the profile layer decided, its explicit hide is honoured")
}

// End to end through the service: the resolved home a pre-card manager override actually gets.
// (The fake maps the role code to 'manager' the way 0140's table does for the owner roles.)
func TestResolve_LeadershipOverridePredatingTheCardStillGetsIt(t *testing.T) {
	f := newFakeStore()
	f.roleProfile["GENERAL_MANAGER"] = ProfileManager
	f.profile[ProfileManager] = []LayoutRow{
		{TileID: TileSalesToday, Position: 0, Visible: true},
		{TileID: TilePaymentsToday, Position: 1, Visible: true}, // 0157's seed
	}
	// The owner personalised their home before DIB-463 shipped: the override cannot name the card.
	f.user["owner-1"] = []LayoutRow{{TileID: TileSalesToday, Position: 0, Visible: true}}

	svc := NewService(f, nil)
	layout, err := svc.Resolve(context.Background(), Scope{UserID: "owner-1", RoleCode: "GENERAL_MANAGER"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if layout.Source != SourceUser {
		t.Fatalf("precondition: the override must be in force, got %q", layout.Source)
	}
	var got *Tile
	for i := range layout.Tiles {
		if layout.Tiles[i].ID == TilePaymentsToday {
			got = &layout.Tiles[i]
		}
	}
	if got == nil {
		t.Fatal("the card must survive resolution")
	}
	if !got.Visible {
		t.Fatal("a leadership override that predates the card must still get it, that is DIB-500")
	}
	if got.State == StateHidden {
		t.Fatalf("a default-on card must be measured like any visible tile, got state %q", got.State)
	}
}

func assertTileVisible(t *testing.T, slots []Slot, tileID string, want bool, why string) {
	t.Helper()
	for _, s := range slots {
		if s.Meta.ID == tileID {
			if s.Visible != want {
				t.Fatalf("%s: tile %q visible=%v, want %v", why, tileID, s.Visible, want)
			}
			return
		}
	}
	t.Fatalf("%s: tile %q missing from the resolved layout", why, tileID)
}
