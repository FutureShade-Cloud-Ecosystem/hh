package dashboard

import "sort"

// ResolveLayout decides WHICH tiles a caller sees and in WHAT order, from three inputs: the code
// catalog, the stored profile (role) rows, and the stored per-user rows.
//
// Pure. No context, no store, no clock, this is the part of the cockpit that has to be provably
// right, so it is testable without a database.
//
// THE OVERRIDE IS WHOLE OR ABSENT. If the user has any stored rows at all, those rows are the
// layout and the profile rows are not consulted. That is what makes "reset to role" a DELETE of
// the user's rows and nothing else: there is no per-tile merge to unwind, no half-inherited
// ordering, no way for a reset to leave a stale fragment behind. The client always submits the
// complete list when saving, and the API rejects a partial one (see Handler.saveMyLayout).
//
// A TILE THE LAYER DOES NOT NAME IS APPENDED HIDDEN. A stored layout, a role default or a personal
// override, is a CURATION: "these tiles, in this order". A tile it does not mention is a tile
// somebody decided against, so it appears in the editor (switchable on in one click) and not on the
// home screen.
//
// This is a deliberate reversal of the first cut of this function, which appended at the catalog's
// DefaultVisible so a newly-shipped tile would "reach people". The walkthrough showed what that
// actually produces: a YARD_TEAM login whose role layout is the pick queue, stale picks and weights
// was also handed AR aging and Today's sales, because those tiles default to visible and the yard
// profile simply does not list them. A role layout that cannot exclude anything is not a role
// layout. The cost is real and accepted: a tile added in a future wave waits for an admin to switch
// it on per profile, which is the right way round for a screen people are meant to trust.
//
// DefaultVisible still decides the CATALOG FALLBACK, the layout for a profile with no stored rows
// at all, where there is no curation to respect and something sensible has to be shown.
//
// A stored row naming a tile that is no longer in the catalog is DROPPED, a deleted tile does not
// leave a hole, and a hand-edited row cannot smuggle an unknown id into the response.
func ResolveLayout(catalog []TileMeta, byID map[string]TileMeta, profileRows, userRows []LayoutRow) ([]Slot, string) {
	layer := userRows
	source := SourceUser
	if len(layer) == 0 {
		layer, source = profileRows, SourceRole
	}
	if len(layer) == 0 {
		source = SourceCatalog
	}

	ordered := make([]LayoutRow, len(layer))
	copy(ordered, layer)
	// Stable sort on position, then tile id, so two rows sharing a position (possible from a
	// hand-written PUT) resolve deterministically instead of by map iteration luck.
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Position != ordered[j].Position {
			return ordered[i].Position < ordered[j].Position
		}
		return ordered[i].TileID < ordered[j].TileID
	})

	out := make([]Slot, 0, len(catalog))
	seen := make(map[string]bool, len(catalog))
	for _, r := range ordered {
		meta, ok := byID[r.TileID]
		if !ok || seen[r.TileID] {
			continue // unknown or duplicate tile id, dropped, never rendered
		}
		seen[r.TileID] = true
		out = append(out, Slot{Meta: meta, Position: len(out), Visible: r.Visible})
	}
	// A stored layer curates; the absence of one does not.
	curated := len(layer) > 0
	for _, meta := range catalog {
		if seen[meta.ID] {
			continue
		}
		seen[meta.ID] = true
		visible := meta.DefaultVisible
		if curated {
			visible = false
		}
		out = append(out, Slot{Meta: meta, Position: len(out), Visible: visible})
	}
	return out, source
}

// ApplyDefaultOn (DIB-500) is the one narrow exception to "a tile the layer does not name is
// appended hidden": a tile whose catalog entry declares DefaultOnProfiles for THIS caller's job
// shape, and which the decisive layer does not mention, flips to visible.
//
// WHY THIS EXISTS. The payments-today card is role-gated through stored profile rows (0157), which
// covers a leadership login with no personal layout. But a personal override is WHOLE OR ABSENT,
// so an owner who rearranged their tiles before the card shipped has a stored layout that cannot
// name it, and the append-hidden rule, correct for every tile nobody asked for, would keep the
// one card they explicitly asked for off their screen forever. Default-on says: for these job
// shapes, SILENCE about this tile means "show it", while an actual row, visible or hidden, is
// a decision and is honoured exactly as stored.
//
// EXPLICIT REMOVAL THEREFORE WINS AND PERSISTS, with no special case: the layout editor always
// submits the complete list, so the first save a person makes after this ships materialises the
// card as a named row, and switching it off stores visible=false, a row this function never
// touches. Only a layer that is SILENT about the tile (one saved before the tile existed, or a
// hand-written partial) is defaulted.
//
// `layer` is the DECISIVE layer, the same one ResolveLayout resolved with (user rows if any,
// else profile rows, else nil). Pure, like everything else in this file.
func ApplyDefaultOn(slots []Slot, layer []LayoutRow, profile string) []Slot {
	named := make(map[string]bool, len(layer))
	for _, r := range layer {
		named[r.TileID] = true
	}
	out := make([]Slot, len(slots))
	copy(out, slots)
	for i := range out {
		if out[i].Visible || named[out[i].Meta.ID] {
			continue
		}
		if out[i].Meta.DefaultOnProfiles[profile] {
			out[i].Visible = true
		}
	}
	return out
}

// DecisiveLayer names the layer ResolveLayout actually resolved with, so ApplyDefaultOn asks
// "did the layer that decided this layout mention the tile?" about the right layer. Mirrors the
// first three lines of ResolveLayout on purpose, if the precedence there ever changes, this must
// change with it, and the shared test in defaulton_test.go pins the two together.
func DecisiveLayer(profileRows, userRows []LayoutRow) []LayoutRow {
	if len(userRows) > 0 {
		return userRows
	}
	return profileRows
}

// NormalizeRows cleans a submitted layout for storage: unknown tile ids dropped, duplicates
// collapsed to their first occurrence, positions renumbered 0..n-1 in the submitted order. The
// stored rows are therefore always a valid layout even if the client sent sparse or colliding
// positions, and a later ResolveLayout has nothing left to defend against.
func NormalizeRows(byID map[string]TileMeta, rows []LayoutRow) []LayoutRow {
	out := make([]LayoutRow, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	sorted := make([]LayoutRow, len(rows))
	copy(sorted, rows)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Position != sorted[j].Position {
			return sorted[i].Position < sorted[j].Position
		}
		return sorted[i].TileID < sorted[j].TileID
	})
	for _, r := range sorted {
		if _, ok := byID[r.TileID]; !ok || seen[r.TileID] {
			continue
		}
		seen[r.TileID] = true
		out = append(out, LayoutRow{TileID: r.TileID, Position: len(out), Visible: r.Visible})
	}
	return out
}
