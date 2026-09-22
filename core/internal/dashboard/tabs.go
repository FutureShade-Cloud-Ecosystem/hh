package dashboard

import (
	"regexp"
	"sort"
)

// DIB-433, the pinned tab strip. The rules live here, pure, for the same reason ResolveLayout
// does: this is the part that has to be provably right, and it must be testable without a database.

// MaxStoredPins bounds ONE stored pin set. It is not the display cap, the client shows at most
// six and folds the rest into a "More" menu (app/src/tab-strip.ts), it is the storage bound that
// stops a hand-written PUT from parking a thousand rows against a user id.
const MaxStoredPins = 24

// tabKeyRE is the SHAPE of a pinned key: the body of a frozen deep link, `unit` or `unit/module`.
//
// Shape ONLY. The registry of surfaces that actually exist is app/src/dashboard-nav.ts and nothing
// else, that single source is what makes the hash contract enumerable and provable. A Go copy of
// it would be a second source for a one-source contract, and it would rot the first time a screen
// shipped without a backend change. So the server refuses malformed keys and the SHELL drops keys
// that name no surface, silently, at render.
var tabKeyRE = regexp.MustCompile(`^[a-z][a-z0-9_]*(/[a-z][a-z0-9_]*)?$`)

// ValidTabKey reports whether k is shaped like a deep-link body. The length bound is belt-and-braces
// against a pathological key that would still match the pattern.
func ValidTabKey(k string) bool {
	return len(k) > 0 && len(k) <= 64 && tabKeyRE.MatchString(k)
}

// ResolveTabs decides WHICH tabs are pinned and in WHAT order, from the stored profile (role) rows
// and the stored per-user rows.
//
// THE OVERRIDE IS WHOLE OR ABSENT, the same rule, in the same words, as ResolveLayout. If the user
// has any stored rows at all, those rows ARE the strip and the role rows are not consulted. That is
// what makes "reset to role" a DELETE and nothing else.
//
// It is also why an unpinned role default is stored visible=false rather than simply left out. A
// person who unpins everything would otherwise store nothing, and "no rows" already means "use the
// role default", their deliberately empty strip would come back as the role's on next login.
//
// There is NO catalog fallback here, and that is the honest difference from ResolveLayout. A tile
// catalog lives in Go, so a profile with no rows can still be shown something sensible; the surface
// registry does not, so a profile with no rows gets no pins and the strip is exactly what it was
// before this ticket, Home and Apps. Empty is a truthful answer; a guess is not.
func ResolveTabs(profileRows, userRows []TabPin) ([]TabPin, string) {
	layer := userRows
	source := SourceUser
	if len(layer) == 0 {
		layer, source = profileRows, SourceRole
	}
	if len(layer) == 0 {
		source = SourceCatalog
	}
	ordered := make([]TabPin, len(layer))
	copy(ordered, layer)
	// Stable sort on position then key, so two rows sharing a position (possible from a
	// hand-written PUT) resolve deterministically instead of by map iteration luck.
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Position != ordered[j].Position {
			return ordered[i].Position < ordered[j].Position
		}
		return ordered[i].TabKey < ordered[j].TabKey
	})
	out := make([]TabPin, 0, len(ordered))
	seen := make(map[string]bool, len(ordered))
	for _, p := range ordered {
		if !ValidTabKey(p.TabKey) || seen[p.TabKey] {
			continue // malformed or duplicate, dropped, never sent
		}
		seen[p.TabKey] = true
		out = append(out, TabPin{TabKey: p.TabKey, Position: len(out), Visible: p.Visible})
	}
	return out, source
}

// NormalizeTabs cleans a submitted pin set for storage: malformed keys dropped, duplicates collapsed
// to their first occurrence, positions renumbered 0..n-1 in submitted order, the whole thing capped
// at MaxStoredPins. The stored rows are therefore always a valid set even if the client sent sparse
// or colliding positions, and a later ResolveTabs has nothing left to defend against.
func NormalizeTabs(rows []TabPin) []TabPin {
	sorted := make([]TabPin, len(rows))
	copy(sorted, rows)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Position != sorted[j].Position {
			return sorted[i].Position < sorted[j].Position
		}
		return sorted[i].TabKey < sorted[j].TabKey
	})
	out := make([]TabPin, 0, len(sorted))
	seen := make(map[string]bool, len(sorted))
	for _, p := range sorted {
		if !ValidTabKey(p.TabKey) || seen[p.TabKey] {
			continue
		}
		seen[p.TabKey] = true
		out = append(out, TabPin{TabKey: p.TabKey, Position: len(out), Visible: p.Visible})
		if len(out) == MaxStoredPins {
			break
		}
	}
	return out
}
