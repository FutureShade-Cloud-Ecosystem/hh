package settings

// platform_hotkeys_effective.go, item 4c: the EFFECTIVE roster, and the one distinction the
// collision rule has been missing.
//
// - WHAT WAS WRONG, STATED AS A FACT RATHER THAN A FEELING -
//
// The collision check exists in five places, ValidatePlatformRoster here, the (surface, binding)
// unique index in 0191, _pos-hotkeys.ts collisionFor, _platform-hotkeys.ts platformCollisionFor,
// and the read-side dedupes, and every one of them is scoped to ONE SURFACE AND ONE TIER. So a
// platform operator can bind a key that is already spoken for one tier ABOVE them and nothing
// anywhere says a word. The key does not stop working; it quietly does a different thing at one
// dealer than at every other, and the operator who caused that cannot see it from the screen they
// caused it on.
//
// - AND THE OBVIOUS FIX IS WRONG, WHICH IS WHY THIS FILE ARGUES BEFORE IT VALIDATES -
//
// The tempting repair is "refuse any key claimed anywhere". It would reject the roster this product
// ships with. 0191 seeds F2, F4 and F9 on BOTH surfaces on purpose, app.shell binds F2/F4/F9 to
// sales.tab.new / search.focus / assist.open, and pos.counter binds the same three physical keys to
// search / step-next / park, and that double claim is DESIGN, not drift: the counter listens in
// the capture phase and stopPropagations, the shell listens in bubble, so while a sale is open the
// till wins and everywhere else the shell does. platform_hotkeys.go:23-36 and 0191:25-36 argue at
// length that the two substrates must stay two. A cross-substrate refusal would therefore refuse
// the defaults, which is a bug shaped exactly like a safety feature.
//
// So the rule splits in two, and the split is on WHOSE KEYBOARD THE TWO ANSWERS MEET:
//
//   REFUSE, same SURFACE, across tiers. One physical key, one surface, two different actions
//            depending on which tier answered. The tiers resolve whole-or-absent, so the two never
//            fire at once, and that is precisely what makes it worth refusing rather than warning:
//            nobody at any single till ever sees the conflict, so nobody can report it. The key
//            means one thing at a dealer who has overridden and another at every dealer who has
//            not, and the platform operator cannot tell from this screen which they are looking at.
//            The same-surface, same-tier duplicate ValidatePlatformRoster already refuses is the
//            degenerate case of this rule, not a different one.
//
//   REFUSE, a key the SHELL HARDCODES and no tier can rebind. `?` opens the shortcut overlay
//            (app-shell.ts, checked before the dealer map is consulted) and Alt+Shift+← walks back
//            a screen (_shell-history.ts). Neither is dealer data at any level. A stored row
//            claiming one is a row that can be written and never pressed, the exact failure the
//            chord-grammar discipline exists to prevent, and for `?` it is worse than that: the
//            overlay is the ONLY accessible route to the counter's key map (the legend strip is
//            aria-hidden by design), so a row that shadowed it would take the key map away from
//            precisely the people who cannot read the strip.
//
//   WARN, NEVER REFUSE, the deliberate cross-substrate double claim. It is not this file's to
//            report (a validator says yes or no), so nothing here refuses it and
//            TestPlatformRosterEffective_TheShippedCrossSubstrateDoubleClaimIsNotACollision pins
//            that. The WORDS live where a person is standing when they press the key:
//            _platform-hotkeys.ts, which has both rosters on screen and can name the other surface.
//
// - WHY THE DEALER TIER IS READ HERE AND NOT ASKED FOR -
//
// pos.counter's dealer override is a `surface_role_items` row, THE KIT'S OWN TABLE, the one
// store.go already reads for every curated surface in the product. Reading it here crosses no
// module boundary and needs no adapter.
//
// app.shell's dealer override is `dealer_hotkeys`, which belongs to internal/dashboard. This file
// does NOT read it, and deliberately declares no consumer interface for it either. Two reasons, and
// the second is the one that decided it: (1) a cross-module read is a consumer-declared interface
// plus an adapter in main.go, and (2) there is no producer to adapt, 0164 shipped that table with
// no edit endpoint at all, and 0191:120 then deleted every unattributed row out of it, so on any
// deployment that has run its migrations the shell's dealer tier is EMPTY and can only become
// non-empty by hand. A seam with no producer is dead code that reads as coverage. When a shell
// dealer editor is built, ShellDealerRoster below is the hole it drops into, and this comment is
// the note explaining why the hole was left rather than filled with a fake.
//
// EVERY READ HERE DEGRADES DOWNWARD. An unreadable dealer tier answers EMPTY, exactly as
// PlatformRosterFor does, for the reason every level of this feature gives: a screen that refused
// to save because a settings read timed out is a screen an operator learns to fight.

import (
	"context"
	"net/http"
	"sort"
	"strings"
)

// - the keys the shell hardcodes -

// ReservedKey is one keystroke the app shell owns outright: no tier binds it, no tier can free it.
//
// Holds is a PLAIN-LANGUAGE phrase and not an action id, which is the one place this package
// departs from platform_hotkeys.go's "name the key, never the action" posture, and it departs for
// that posture's own reason. The rule there is that the human label for a DEALER'S action lives in
// the frontend registry, so a server-side refusal naming it would paint a raw enum on a screen.
// These two keys are not in any registry and never were; they are the shell's own behaviour,
// spelled in code on both sides of the wire. The server is the only place that knows the full set,
// so it is the right place to say what each one is for.
type ReservedKey struct {
	Binding string `json:"binding"`
	Holds   string `json:"holds"`
}

// The reserved set, spelled as a person reads it. `?` is app-shell.ts's isHelpChord branch, checked
// before the dealer map is consulted; Alt+Shift+← is _shell-history.ts's isBackChord.
var reservedKeys = []ReservedKey{
	{Binding: "?", Holds: "opens the shortcut list"},
	{Binding: "Alt+Shift+←", Holds: "goes back a screen"},
}

// ReservedPlatformKeys is the reserved set, copied, for the GET payload.
//
// IT IS SERVED RATHER THAN MIRRORED IN THE BROWSER because it is the one part of this rule that has
// no second owner. The chord grammars are mirrored on both sides deliberately (each side must be
// able to refuse before the round trip); this list is a fact about the shell that only ever changes
// when the shell changes, and a hand-copied array in a settings screen would be the copy that
// drifts the day someone adds a third hardcoded key.
func ReservedPlatformKeys() []ReservedKey {
	return append([]ReservedKey(nil), reservedKeys...)
}

// reservedHolderFor answers "what does the shell already do with this keystroke", or "".
//
// The comparison is on a CANONICAL FORM (lowercased, either separator, modifiers sorted, the arrow
// glyph spelled out) rather than on the literal string, because a row can arrive from four writers
// with four spellings and a rule that only catches one of them is a rule that reports itself clean.
// It is deliberately not a chord PARSER: neither grammar can produce `?` or an arrow key in the
// first place (both require F1-F12 or a letter/digit), so nothing an operator can press reaches
// this check today. That is exactly why it is written down. The protection currently rests on an
// accident of two regexes; this makes it a rule, so a hand-written row cannot take the overlay key,
// and a future widening of either grammar cannot take it by surprise.
func reservedHolderFor(binding string) string {
	want := canonicalReservedForm(binding)
	if want == "" {
		return ""
	}
	for _, r := range reservedKeys {
		if canonicalReservedForm(r.Binding) == want {
			return r.Holds
		}
	}
	return ""
}

// canonicalReservedForm folds the four spellings of one chord into one string.
func canonicalReservedForm(binding string) string {
	s := strings.ToLower(strings.TrimSpace(binding))
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "+", "-")
	s = strings.ReplaceAll(s, "←", "arrowleft")
	parts := strings.Split(s, "-")
	base := parts[len(parts)-1]
	mods := append([]string(nil), parts[:len(parts)-1]...)
	sort.Strings(mods) // spelling order is not meaning: shift-alt and alt-shift are one chord
	return strings.Join(append(mods, base), "-")
}

// - the dealer tier -

// posCounterSurfaceID is the curation surface the counter's roster is filed under. The same string
// as PlatformSurfaceCounter, and that is not a coincidence to be tidied away with one constant: one
// is a PLATFORM ROSTER's surface id and the other is a CURATION SURFACE's id, they are equal today
// because the counter is one screen, and collapsing them would hide the day they stop being.
const posCounterSurfaceID = PlatformSurfaceCounter

// counterRosterProfiles are the job shapes a dealer can curate the counter for. All five are read,
// not just the caller's: a platform operator is setting a floor for a whole deployment, so "does
// any till at this dealer already use that key for something else" is the question, and asking it
// of one job shape would answer it for one desk.
var counterRosterProfiles = []string{
	"counter", "yard", "driver", "manager", "admin",
	"manager.branch", "manager.sales", "manager.yard", "manager.logistics",
	"manager.procurement", "manager.payables", "manager.controller", "manager.general",
}

// DealerRosterFor reads the DEALER OVERRIDE tier for one surface, the level immediately above the
// platform roster, and the one whose disagreements with it are invisible from either screen.
//
// Caller-free, exactly as PlatformRosterFor is, and for the same reason: which keys a dealer has
// already claimed is a fact about the dealer, not about who is asking. A person's own legend
// preference (surface_user_items) is deliberately NOT read, that tier is one till's ergonomics and
// a platform operator has no business being blocked by it.
//
// app.shell answers empty, always. See the file header for why that is a decision and not a gap.
func (s *Service) DealerRosterFor(ctx context.Context, surface string) ([]PlatformHotkey, error) {
	if err := ValidatePlatformSurface(surface); err != nil {
		return nil, err
	}
	if surface != PlatformSurfaceCounter {
		return nil, nil
	}
	out := []PlatformHotkey{}
	seen := map[string]bool{}
	for _, profile := range counterRosterProfiles {
		items, err := s.store.ReadRoleItems(ctx, posCounterSurfaceID, profile)
		if err != nil {
			// Degrade downward, per the header. One unreadable profile must not take the other four
			// with it, so this continues rather than returning: a partial answer names more real
			// clashes than no answer does, and it can only ever under-report.
			continue
		}
		for _, it := range items {
			if it.Kind != KindAction || !it.Visible {
				continue
			}
			action, key := splitCounterRosterID(it.ID)
			if action == "" || key == "" || !counterActions[action] {
				continue // a row this build cannot render is not a clash, it is a typo
			}
			if seen[key] {
				// TWO PROFILES DISAGREEING ABOUT ONE KEY IS NOT THIS RULE'S BUSINESS. First one
				// wins the naming; the refusal it produces is identical either way, because the
				// refusal names the KEY and not the action it would break.
				continue
			}
			seen[key] = true
			out = append(out, PlatformHotkey{Action: action, Binding: key, Position: it.Position})
		}
	}
	sortPlatformKeys(out)
	return out, nil
}

// splitCounterRosterID decodes a counter curation row id into its two halves.
//
// THE ENCODING BELONGS TO internal/pos (hotkeys.go: "one roster row is `<action>.<key>`… the action
// is everything before the LAST dot; the key is what follows"), and this is a second reader of it,
// which is a cost worth naming. The alternative was a cross-module read of pos's resolver, and it
// was rejected twice over: pos.Service.Hotkeys resolves FOUR tiers into one answer, so it cannot
// tell this caller whether the dealer level is the one that spoke, and it reads the CALLER'S own
// user items, so the same platform edit would be refused for one operator and allowed for another.
// A four-line split that only ever produces a candidate for a refusal, never a rendered word, never
// an authority decision, is the smaller of the two mistakes. TestDealerRosterFor_DecodesTheRosterID
// pins the shape against the ids the counter editor actually writes.
func splitCounterRosterID(id string) (action, key string) {
	i := strings.LastIndex(id, ".")
	if i <= 0 || i == len(id)-1 {
		return "", ""
	}
	return id[:i], id[i+1:]
}

// - the rule -

// ValidatePlatformRosterAgainst is ValidatePlatformRoster plus the two cross-tier refusals argued in
// the file header.
//
// IT IS A SEPARATE FUNCTION AND NOT A WIDENING, because ValidatePlatformRoster has two callers
// outside this package that assert on its exact meaning, dashboard's shell-shape gate and pos's
// action-set gate both ask it "is this ONE ROSTER well formed" with no dealer tier in sight, and a
// third argument would have made both of them ask a question they do not mean. Same reason
// SurfaceCuration was not widened for the platform tier: a seam every existing caller must change is
// a seam that gets worked around.
//
// `dealer` may be nil, which means "no tier above this one has spoken" and is the state of every
// fresh deployment.
func ValidatePlatformRosterAgainst(surface string, keys, dealer []PlatformHotkey) error {
	if err := ValidatePlatformRoster(surface, keys); err != nil {
		return err
	}
	for _, k := range keys {
		binding := strings.TrimSpace(k.Binding)
		action := strings.TrimSpace(k.Action)
		if holds := reservedHolderFor(binding); holds != "" {
			return Errf(http.StatusUnprocessableEntity,
				"settings: %s %s on every screen and no roster can take it, pick another key", binding, holds)
		}
		for _, d := range dealer {
			if !strings.EqualFold(strings.TrimSpace(d.Binding), binding) || d.Action == action {
				continue
			}
			// NAMED BY THE KEY, NOT BY THE ACTION IT WOULD BREAK, platform_hotkeys.go's posture,
			// unchanged: the human label for a dealer's action lives in the frontend registry, and a
			// refusal that said "already runs step-next" would paint a raw enum on a screen. The
			// configurator refuses the same clash at the press, where it has the label and can say
			// which one. The way forward names the screen the other key is freed on, because that
			// is the part an operator cannot guess.
			return Errf(http.StatusUnprocessableEntity,
				"settings: %s already does something else at this dealer's counter, free it in Counter settings first, or pick another key", binding)
		}
	}
	return nil
}

// ClaimedElsewhere reports the keys this roster shares with the OTHER surface's roster.
//
// THIS IS THE WARN HALF, AND IT RETURNS DATA RATHER THAN AN ERROR ON PURPOSE. F2, F4 and F9 are
// double-claimed across app.shell and pos.counter on the shipped default and that is deliberate
// (see the file header). A validator's only vocabulary is yes and no, so it must say yes here; what
// a person needs is the third answer, "this works, and here is why it works", which is a sentence
// on a screen, not a status code. The screen composes it; this only supplies the fact, so that the
// fact and the roster it is computed from can never be one read out of date.
func ClaimedElsewhere(keys, other []PlatformHotkey) []string {
	held := map[string]bool{}
	for _, o := range other {
		held[strings.ToLower(strings.TrimSpace(o.Binding))] = true
	}
	out := []string{}
	for _, k := range keys {
		b := strings.TrimSpace(k.Binding)
		if held[strings.ToLower(b)] {
			out = append(out, b)
		}
	}
	return out
}
