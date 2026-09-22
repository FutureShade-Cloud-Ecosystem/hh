package settings

// platform_hotkeys.go, D-111: the PLATFORM tier of the hotkey model, and the one place it is
// edited from.
//
// THE RULING, verbatim: "i think we need a platform admin configurator for hotkeys". The thing that
// was missing was never an editor, train 14 shipped the dealer's, but the level ABOVE it. Below
// the dealer's override there was only a `var` in Go source, so the only way for Hardscape House to
// change what a key does across the deployment was to cut a release. This file is the level a
// platform operator can actually edit:
//
//	user legend preference  (exists; dismiss/restore, the keys stay live either way)
//	dealer override         (exists; train 14's counter editor)
//	PLATFORM default roster (HERE, new)
//	shipped BisTrack floor  (pos.defaultHotkeys / dashboard.DefaultHotkeys, still underneath)
//
// WHY IT LIVES IN THE KIT AND NOT IN pos OR dashboard. It serves BOTH hotkey substrates, and
// neither of those packages may import the other. The kit is already the shared home for "the
// substrate a surface's settings live in", it already owns the 422-in-words validators, and it
// already mounts the HTTP surface every settings screen talks to. So the platform roster rides the
// same mount, with the same voice.
//
// - THE TWO SUBSTRATES STAY TWO -
//
// This product has two hotkey maps and they are not the same map:
//
//	app.shell    the app-wide NAVIGATION map (dealer_hotkeys, 0164/0171). Chord spelling "F1",
//	             "Ctrl+Shift+K". Action ids are the frontend nav registry's ('sale.cash.new').
//	pos.counter  the in-sale ACTION roster (surface_role_items kind `action`, 0172). Chord spelling
//	             "f4", "shift-f4". Action ids are the counter's five ('step-next', 'pay').
//
// Different namespaces, different grammars, and until this session two different answers about F8.
// A previous conflation of them is on record as a real defect, the till's Complete-sale button
// advertised F8 while the counter actually paid on F12, so this file goes out of its way NOT to
// merge them. One table, keyed by surface; one read per surface; one write per surface; and every
// rule below is asked of a SURFACE, never of "hotkeys" in general.
//
// - WHAT IS VALIDATED HERE, AND WHAT DELIBERATELY IS NOT -
//
// NOT THE CHORD GRAMMAR. There are already four spellings of the chord grammar in this codebase
// (dashboard.NormalizeHotkeyBinding, pos.canonicalHotkeyKey, and their two TypeScript mirrors), and
// dashboard/hotkeys.go says the quiet part out loud: "a row the two disagree on is a key that can
// be stored but never pressed, so keep them identical." A FIFTH copy here, in a package that can
// import neither owner, would be the copy that drifts. So this file does what 0164 itself chose
// for exactly this table shape: it stores the spelling and lets the OWNING MODULE canonicalise on
// read. pos.splitHotkeyID and dashboard.NormalizeHotkeys already repair or drop a mis-spelled row,
// and they now do it for a platform row on precisely the same terms as a dealer row.
//
// THE ACTION SET, PER SURFACE, IN EACH OWNER'S OWN POSTURE. The two owners disagree about whether
// the server may keep a list of actions, and they disagree for good reasons, so this file keeps
// both postures rather than inventing a third:
//
//   - pos.counter has a closed set (pos.validHotkeyActions), because an action the counter cannot
//     render draws a dead keycap on a legend pinned across the foot of every till. The platform
//     tier is the FLOOR the whole deployment inherits, so that argument is only stronger here.
//   - app.shell keeps SHAPE only (0164's ruling: "the client owns what a surface/action IS, so the
//     server does not keep a copy of that list to drift out of date"). A shell row naming an action
//     this build does not know does nothing, silently, which is already what a shell hotkey aimed
//     at a screen the caller's role cannot open does.
//
// The counter list here is a mirror of pos's, and platform_hotkeys_mirror_test.go in each owning
// package is what holds them equal, the same "two copies, one test" arrangement the BisTrack
// floor already uses across the Go/TypeScript line.
//
// - THE COLLISION, AND WHO NAMES THE ACTION -
//
// A roster where two actions claim one key resolves by row order and drops the loser silently, a
// key that simply stopped working, with nothing on any screen to say why. So a duplicate binding is
// refused here, before the write. The refusal names the KEY and not the action, because the human
// LABEL for an action lives in the frontend registry and this server does not have it; a refusal
// that said "already runs step-next" would paint a raw enum on a screen. The configurator refuses
// the same clash at the moment of the keypress, where it does have the labels and can say "F4
// already runs Next", which is the sentence a person actually reads. This is the floor under that,
// for a caller that is not the configurator.

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// The two surfaces the platform roster covers. These strings are STORED (the 0191 CHECK
// enumerates them) and they match the surface ids the two modules already use, so a support call
// reading a row and a support call reading a curation row are reading the same word.
const (
	// PlatformSurfaceShell is the app-wide navigation map (dashboard's dealer_hotkeys).
	PlatformSurfaceShell = "app.shell"
	// PlatformSurfaceCounter is the counter's in-sale roster (pos's pos.counter curation).
	PlatformSurfaceCounter = "pos.counter"
)

// PlatformSurfaces is the ordered list of surfaces the configurator edits. Order is the order the
// screen renders them in: the app-wide map first, because it is the one every screen in the
// product obeys, then the counter's, which only the till obeys.
var PlatformSurfaces = []string{PlatformSurfaceShell, PlatformSurfaceCounter}

// counterActions mirrors pos.validHotkeyActions. See the file header for why the mirror exists and
// which test holds it equal to the original.
var counterActions = map[string]bool{
	"step-next": true, "step-back": true, "pay": true, "park": true, "search": true,
}

// PlatformCounterActions is the mirrored counter action set, exported ONLY so the owning package
// can hold it equal to its own, see pos/platform_hotkeys_mirror_test.go. Nothing in production
// reads it; the mirror is the whole reason it is exported, and a mirror nobody checks is a copy
// waiting to drift.
func PlatformCounterActions() []string {
	out := make([]string, 0, len(counterActions))
	for a := range counterActions {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// maxPlatformKeys bounds one platform roster. It matches pos's own maxHotkeys for the same stated
// reason, a legend past that many keycaps is a second navigation bar, and nobody reads a second
// navigation bar, and applies to the shell for the symmetric reason: the `?` overlay is a list a
// person is meant to be able to hold in their head.
const maxPlatformKeys = 8

// maxPlatformBinding bounds a stored chord spelling. "Ctrl+Alt+Shift+F12" is 18 characters; 32 is
// headroom that still refuses a row that is clearly not a chord. It matches 0191's CHECK.
const maxPlatformBinding = 32

// maxPlatformAction bounds a stored action id, matching dashboard's maxHotkeyActionLen and 0191.
const maxPlatformAction = 64

// PlatformHotkey is one row of a platform roster: which action, which key, where in the list.
type PlatformHotkey struct {
	Action   string `json:"action"`
	Binding  string `json:"binding"`
	Position int    `json:"position"`
}

// PlatformRoster is one surface's whole platform roster, plus the tier immediately above it.
//
// DEALER RIDES ALONG BECAUSE THE COLLISION IS ONLY VISIBLE WITH BOTH. Item 4c: a platform binding
// that lands on a key the dealer has already bound to something else on the same surface is a key
// with two meanings across one deployment, and nothing on either screen said so. The configurator
// refuses that at the press and names which action holds it, which it can only do if the dealer's
// rows arrive with the platform's. It is EMPTY for app.shell, always; platform_hotkeys_effective.go
// argues why that is a decision rather than a gap.
type PlatformRoster struct {
	Surface string           `json:"surface"`
	Keys    []PlatformHotkey `json:"keys"`
	Dealer  []PlatformHotkey `json:"dealer"`
}

// PlatformHotkeyView is the GET /api/v1/platform/hotkeys answer.
//
// CanEdit IS THE SERVER'S ANSWER TO "MAY I", AND IT IS WHY THE CLIENT NEEDS NO AUTHORITY LOGIC.
// The read is staff-wide on purpose, a dealer's administrator seeing what the platform floor
// says is how they understand what their own override is overriding, and hiding it would make the
// dealer editor's "reset to the platform roster" a button whose destination is invisible. The
// WRITE is the platform operator's alone, and rather than have the browser evaluate a role code,
// the server says whether this caller may write and the screen renders accordingly. Same posture
// the AR landing's capability flags take, and the same reason: authority is the server's fact.
type PlatformHotkeyView struct {
	Rosters []PlatformRoster `json:"rosters"`
	CanEdit bool             `json:"can_edit"`
	// Reserved is the shell's own hardcoded keys, served, not mirrored in the browser, because it
	// is the one half of this rule with no second owner. See ReservedPlatformKeys.
	Reserved []ReservedKey `json:"reserved"`
}

// NotPlatformAdminError is the refusal a dealer's administrator gets at a platform write. It is a
// SEPARATE type from NotAdminError because it is a separate gate: NotAdminError means "you are not
// an administrator", which four roles are; this means "you are not the platform operator", which
// exactly one is. Collapsing them would make the sentence lie to a General Manager, who is
// unambiguously an administrator and still may not do this.
type NotPlatformAdminError struct{ RoleCode string }

func (e *NotPlatformAdminError) Error() string {
	if e.RoleCode == "" {
		return "settings: the platform key roster is changed by the platform administrator"
	}
	return fmt.Sprintf(
		"settings: the platform key roster is changed by the platform administrator, %s cannot change it; a dealer's own keys are set in Counter settings",
		e.RoleCode)
}

// ValidatePlatformSurface refuses an unknown surface in words.
func ValidatePlatformSurface(surface string) error {
	for _, s := range PlatformSurfaces {
		if s == surface {
			return nil
		}
	}
	return Errf(http.StatusUnprocessableEntity,
		"settings: the platform key roster covers the app shortcuts and the counter keys, and nothing else")
}

// ValidatePlatformRoster refuses a malformed or self-contradicting roster in words, BEFORE any
// CHECK or unique index could answer with a 500. Every rule is stated in the file header; the one
// that matters most to a person is the last, because it is the one a real edit trips.
func ValidatePlatformRoster(surface string, keys []PlatformHotkey) error {
	if err := ValidatePlatformSurface(surface); err != nil {
		return err
	}
	if len(keys) == 0 {
		return Errf(http.StatusUnprocessableEntity,
			"settings: bind at least one key, or every terminal inherits a keyboard that does nothing")
	}
	if len(keys) > maxPlatformKeys {
		return Errf(http.StatusUnprocessableEntity,
			"settings: at most %d keys on one roster, past that the legend is a second navigation bar", maxPlatformKeys)
	}
	seenAction := map[string]bool{}
	seenBinding := map[string]bool{}
	for _, k := range keys {
		action := strings.TrimSpace(k.Action)
		binding := strings.TrimSpace(k.Binding)
		if !validPlatformAction(surface, action) {
			return Errf(http.StatusUnprocessableEntity,
				"settings: %q is not an action this build can carry out on that screen", action)
		}
		if binding == "" || len([]rune(binding)) > maxPlatformBinding {
			return Errf(http.StatusUnprocessableEntity,
				"settings: every bound action needs a key, spelled in at most %d characters", maxPlatformBinding)
		}
		if k.Position < 0 {
			return Errf(http.StatusUnprocessableEntity, "settings: key positions cannot be negative")
		}
		if seenAction[action] {
			// One action, one key. Two rows for one action would make which one shows on the
			// legend depend on row order, and a person pressing a key must never have to know that.
			return Errf(http.StatusUnprocessableEntity,
				"settings: %q is bound twice, one action takes one key", action)
		}
		// THE COLLISION. Named by the key rather than by the action it would break: see the file
		// header, the human label lives in the frontend registry, and a refusal that named the
		// stored id would paint a raw enum on a screen. The configurator refuses the same clash at
		// the keypress, where it has the label.
		if seenBinding[binding] {
			return Errf(http.StatusUnprocessableEntity,
				"settings: %s is claimed twice on this roster, free it on the other action first, or pick another key", binding)
		}
		seenAction[action] = true
		seenBinding[binding] = true
	}
	return nil
}

// validPlatformAction applies each surface's OWNER's posture, see the file header.
func validPlatformAction(surface, action string) bool {
	if action == "" || len(action) > maxPlatformAction {
		return false
	}
	if surface == PlatformSurfaceCounter {
		return counterActions[action]
	}
	// app.shell: SHAPE only, mirroring dashboard.validHotkeyAction, lowercase dotted words.
	for i := 0; i < len(action); i++ {
		c := action[i]
		ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '-'
		if !ok {
			return false
		}
	}
	return true
}

// PlatformRosterFor reads ONE surface's platform roster. Caller-free on purpose: this is the seam
// a module's own resolver reads through (pos.Service.Hotkeys), and resolving a till's keys must not
// depend on who is asking, a bound key grants no power and gates nothing.
//
// An unreadable roster answers EMPTY, never an error, for the reason every level of this feature
// gives: a till whose keys stopped working because a settings read timed out is a till nobody
// trusts again. The caller's next fallback (the shipped floor) then answers.
func (s *Service) PlatformRosterFor(ctx context.Context, surface string) ([]PlatformHotkey, error) {
	if err := ValidatePlatformSurface(surface); err != nil {
		return nil, err
	}
	reader, ok := s.store.(PlatformHotkeyStore)
	if !ok {
		return nil, nil
	}
	keys, err := reader.ReadPlatformHotkeys(ctx, surface)
	if err != nil {
		return nil, nil
	}
	return keys, nil
}

// PlatformHotkeys reads every surface's roster plus this caller's answer to "may I change it".
// Staff-wide read, see PlatformHotkeyView's comment for why.
func (s *Service) PlatformHotkeys(ctx context.Context) (PlatformHotkeyView, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return PlatformHotkeyView{}, err
	}
	out := PlatformHotkeyView{
		Rosters:  make([]PlatformRoster, 0, len(PlatformSurfaces)),
		CanEdit:  caller.IsSuperAdmin(),
		Reserved: ReservedPlatformKeys(),
	}
	for _, surface := range PlatformSurfaces {
		keys, kerr := s.PlatformRosterFor(ctx, surface)
		if kerr != nil {
			return PlatformHotkeyView{}, kerr
		}
		if keys == nil {
			keys = []PlatformHotkey{}
		}
		// The dealer tier degrades downward on its own account: it is context for a refusal, not
		// the roster, and a screen that would not open because the tier ABOVE the one it edits was
		// unreadable would be a screen down for a reason nobody can act on.
		dealer, derr := s.DealerRosterFor(ctx, surface)
		if derr != nil || dealer == nil {
			dealer = []PlatformHotkey{}
		}
		out.Rosters = append(out.Rosters, PlatformRoster{Surface: surface, Keys: keys, Dealer: dealer})
	}
	return out, nil
}

// PutPlatformHotkeys replaces ONE surface's platform roster, whole.
//
// WHOLE-OR-ABSENT, LIKE EVERY OTHER LEVEL OF THIS SUBSTRATE. A partial write would let a roster
// carry a key nobody chose, half-inherited from a previous edit, and "which level answered" would
// stop being a single word anyone could put in a support answer.
//
// GATED ON THE PLATFORM OPERATOR, NOT ON "AN ADMINISTRATOR". IsAdmin() admits four roles including
// the dealer's own General Manager, so it cannot gate a tier whose entire purpose is to sit ABOVE
// the dealer. IsSuperAdmin() is the only predicate in the identity model that separates the two,
// and it is used here exactly as identity's own escalation guards use it.
func (s *Service) PutPlatformHotkeys(ctx context.Context, surface string, keys []PlatformHotkey) error {
	caller, err := s.caller(ctx)
	if err != nil {
		return err
	}
	if !caller.IsSuperAdmin() {
		return &NotPlatformAdminError{RoleCode: caller.RoleCode}
	}
	// The dealer tier is READ FOR THE REFUSAL and never blocks the write on its own failure. This
	// is a write path, so "degrade downward" is not automatic here and the choice has to be argued:
	// the press-time refusal in the configurator is the gate a person actually meets, this is the
	// floor under it for a caller that is not the configurator, and a floor that turns a settings
	// timeout into "you may not save your work" is a floor that costs more than it protects. It can
	// only ever under-report, never wrongly refuse.
	dealer, derr := s.DealerRosterFor(ctx, surface)
	if derr != nil {
		dealer = nil
	}
	if err := ValidatePlatformRosterAgainst(surface, keys, dealer); err != nil {
		return err
	}
	writer, ok := s.store.(PlatformHotkeyStore)
	if !ok {
		return Errf(http.StatusServiceUnavailable, "settings: the platform key roster is not available on this deployment")
	}
	// Positions are renumbered from the submitted ORDER rather than trusted from the body: the
	// screen sends a list, and a list's order is what a person arranged. Trusting the field would
	// let two rows share a position and make the legend's order arbitrary.
	rows := make([]PlatformHotkey, 0, len(keys))
	for i, k := range keys {
		rows = append(rows, PlatformHotkey{
			Action:   strings.TrimSpace(k.Action),
			Binding:  strings.TrimSpace(k.Binding),
			Position: i,
		})
	}
	return writer.ReplacePlatformHotkeys(ctx, surface, rows, caller.UserID)
}

// PlatformHotkeyStore is the platform roster's persistence seam. It is a SEPARATE interface from
// StoreIface, asserted at the call site, so that a store which predates this tier (a scaffold, a
// test fake that only ever needed curation) keeps compiling and simply has no platform roster -
// which resolves to the shipped floor, exactly as an unreadable one does.
type PlatformHotkeyStore interface {
	ReadPlatformHotkeys(ctx context.Context, surface string) ([]PlatformHotkey, error)
	ReplacePlatformHotkeys(ctx context.Context, surface string, keys []PlatformHotkey, updatedBy string) error
}

// sortPlatformKeys puts a roster in legend order: position, then action id so the order is total
// and a read is byte-stable between calls.
func sortPlatformKeys(keys []PlatformHotkey) {
	sort.SliceStable(keys, func(i, j int) bool {
		if keys[i].Position != keys[j].Position {
			return keys[i].Position < keys[j].Position
		}
		return keys[i].Action < keys[j].Action
	})
}
