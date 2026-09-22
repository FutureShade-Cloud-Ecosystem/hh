package dashboard

import "testing"

// A miniature catalog, so these tests assert the RULE rather than the current tile list. If a wave
// adds a tile, none of this has to change.
var testCatalog = []TileMeta{
	{ID: "a", Label: "A", Module: "alpha", DefaultVisible: true},
	{ID: "b", Label: "B", Module: "bravo", DefaultVisible: true},
	{ID: "c", Label: "C", Module: "charlie", DefaultVisible: false},
}

func testByID() map[string]TileMeta {
	m := map[string]TileMeta{}
	for _, t := range testCatalog {
		m[t.ID] = t
	}
	return m
}

func ids(slots []Slot) []string {
	out := make([]string, len(slots))
	for i, s := range slots {
		out[i] = s.Meta.ID
	}
	return out
}

func visibleIDs(slots []Slot) []string {
	var out []string
	for _, s := range slots {
		if s.Visible {
			out = append(out, s.Meta.ID)
		}
	}
	return out
}

func eq(t *testing.T, got, want []string, what string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s: got %v, want %v", what, got, want)
		}
	}
}

func TestResolveLayout_NoRowsFallsBackToCatalogOrderAndDefaults(t *testing.T) {
	slots, source := ResolveLayout(testCatalog, testByID(), nil, nil)
	eq(t, ids(slots), []string{"a", "b", "c"}, "order")
	eq(t, visibleIDs(slots), []string{"a", "b"}, "visible")
	if source != SourceCatalog {
		t.Fatalf("source = %q, want %q", source, SourceCatalog)
	}
}

func TestResolveLayout_ProfileRowsDecideOrderAndVisibility(t *testing.T) {
	profile := []LayoutRow{
		{TileID: "b", Position: 0, Visible: true},
		{TileID: "a", Position: 1, Visible: false},
		{TileID: "c", Position: 2, Visible: true},
	}
	slots, source := ResolveLayout(testCatalog, testByID(), profile, nil)
	eq(t, ids(slots), []string{"b", "a", "c"}, "order")
	eq(t, visibleIDs(slots), []string{"b", "c"}, "visible")
	if source != SourceRole {
		t.Fatalf("source = %q, want %q", source, SourceRole)
	}
}

// DIB-353's central rule: a user override is WHOLE. The profile rows must not leak back in, in any
// form, not as extra tiles, not as an ordering hint for tiles the user did not name.
func TestResolveLayout_UserOverrideIsWholeAndSuppressesTheProfile(t *testing.T) {
	profile := []LayoutRow{
		{TileID: "a", Position: 0, Visible: true},
		{TileID: "b", Position: 1, Visible: true},
		{TileID: "c", Position: 2, Visible: true},
	}
	user := []LayoutRow{
		{TileID: "c", Position: 0, Visible: true},
		{TileID: "a", Position: 1, Visible: false},
	}
	slots, source := ResolveLayout(testCatalog, testByID(), profile, user)
	if source != SourceUser {
		t.Fatalf("source = %q, want %q", source, SourceUser)
	}
	// c then a from the override; b was not named by the override, so it appends HIDDEN, the
	// profile's opinion about b does not leak through, and neither does the catalog's.
	eq(t, ids(slots), []string{"c", "a", "b"}, "order")
	eq(t, visibleIDs(slots), []string{"c"}, "visible")
}

// Reset-to-role is a DELETE of the user rows and nothing else. Proving it at this layer is what
// makes the repository's single DELETE sufficient: with no user rows, resolution is byte-identical
// to the role default it started from.
func TestResolveLayout_ResetToRoleReturnsExactlyTheRoleLayout(t *testing.T) {
	profile := []LayoutRow{
		{TileID: "b", Position: 0, Visible: true},
		{TileID: "c", Position: 1, Visible: true},
		{TileID: "a", Position: 2, Visible: false},
	}
	user := []LayoutRow{{TileID: "a", Position: 0, Visible: true}}

	before, _ := ResolveLayout(testCatalog, testByID(), profile, nil)
	overridden, _ := ResolveLayout(testCatalog, testByID(), profile, user)
	afterReset, source := ResolveLayout(testCatalog, testByID(), profile, nil)

	if eqSlice(ids(before), ids(overridden)) {
		t.Fatal("the override must actually change the layout, else this test proves nothing")
	}
	eq(t, ids(afterReset), ids(before), "reset order")
	eq(t, visibleIDs(afterReset), visibleIDs(before), "reset visibility")
	if source != SourceRole {
		t.Fatalf("after reset source = %q, want %q", source, SourceRole)
	}
}

// A CURATED layout excludes. A tile the stored layer does not name is appended HIDDEN regardless of
// its catalog default, otherwise a yard role layout of "pick queue, stale picks, weights" silently
// also shows AR aging, and a role layout that cannot exclude anything is not a role layout.
func TestResolveLayout_ACuratedLayoutHidesEverythingItDoesNotName(t *testing.T) {
	saved := []LayoutRow{
		{TileID: "b", Position: 0, Visible: true},
		{TileID: "a", Position: 1, Visible: true},
	}
	slots, _ := ResolveLayout(testCatalog, testByID(), saved, nil)
	eq(t, ids(slots), []string{"b", "a", "c"}, "order")
	eq(t, visibleIDs(slots), []string{"b", "a"}, "visible")
	if slots[2].Visible {
		t.Fatal("a tile the curated layout does not name must arrive hidden, whatever the catalog says")
	}
	// Only tile "a" defaults visible among the unnamed ones here, prove the rule is about
	// curation, not about which particular tile it is.
	onlyC := []LayoutRow{{TileID: "c", Position: 0, Visible: true}}
	slots2, _ := ResolveLayout(testCatalog, testByID(), onlyC, nil)
	eq(t, visibleIDs(slots2), []string{"c"}, "visible with only c named")
}

func TestResolveLayout_UnknownAndDuplicateRowsAreDropped(t *testing.T) {
	rows := []LayoutRow{
		{TileID: "zzz-not-a-tile", Position: 0, Visible: true},
		{TileID: "a", Position: 1, Visible: true},
		{TileID: "a", Position: 2, Visible: false}, // duplicate; first occurrence wins
	}
	slots, _ := ResolveLayout(testCatalog, testByID(), rows, nil)
	eq(t, ids(slots), []string{"a", "b", "c"}, "order")
	if !slots[0].Visible {
		t.Fatal("the FIRST row for a duplicated tile must win")
	}
}

// Two rows at the same position must not resolve by map-iteration luck.
func TestResolveLayout_CollidingPositionsAreDeterministic(t *testing.T) {
	rows := []LayoutRow{
		{TileID: "c", Position: 0, Visible: true},
		{TileID: "b", Position: 0, Visible: true},
	}
	for i := 0; i < 25; i++ {
		slots, _ := ResolveLayout(testCatalog, testByID(), rows, nil)
		eq(t, ids(slots), []string{"b", "c", "a"}, "collision order")
	}
}

func TestNormalizeRows_RenumbersAndFilters(t *testing.T) {
	got := NormalizeRows(testByID(), []LayoutRow{
		{TileID: "c", Position: 40, Visible: false},
		{TileID: "nope", Position: 1, Visible: true},
		{TileID: "a", Position: 9, Visible: true},
	})
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(got), got)
	}
	if got[0].TileID != "a" || got[0].Position != 0 {
		t.Fatalf("row 0 = %+v, want a@0", got[0])
	}
	if got[1].TileID != "c" || got[1].Position != 1 || got[1].Visible {
		t.Fatalf("row 1 = %+v, want c@1 hidden", got[1])
	}
}

// The real catalog must be internally consistent, a duplicated id would silently shadow a tile.
func TestCatalog_IDsAreUniqueAndIndexed(t *testing.T) {
	if len(CatalogByID) != len(Catalog) {
		t.Fatalf("catalog has duplicate tile ids: %d entries, %d unique", len(Catalog), len(CatalogByID))
	}
	for _, tile := range Catalog {
		if tile.Label == "" || tile.Module == "" || tile.Kind == "" {
			t.Fatalf("catalog entry %q is missing label/module/kind", tile.ID)
		}
	}
}

func eqSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
