package pos

// hotkeys.go, the counter's KEY ROSTER: which F-key does what at this dealer's tills.
//
// THE ASK (Ryan Rood, Aug 26): the BisTrack muscle memory. "a function key advances the step, an
// F12-style key takes payment", plus a persistent legend pinned at the bottom of the screen so
// nobody has to memorise anything. And, the part that decides where this lives, the keys must be
// the DEALER's, because BisTrack refugees will want their old ones.
//
// SO THE ROSTER IS DATA, NOT CODE (D-105). A key binding is presentation ergonomics: it grants no
// power, moves no money and gates nothing, pressing F8 does exactly what pressing the gold button
// does, and a caller who may not ring a sale still may not ring one with a key. That puts it in the
// generic curation substrate (surface_role_items / surface_user_items under surface `pos.counter`,
// the same rows the hot list is curated through) rather than in a typed policy column, and it is
// why POS adds no settings endpoint: a dealer re-keys the counter through the settings kit's own
// PUT, like every other curated surface in the product.
//
// THE ENCODING. A kit item id is an opaque lowercase string the FRONTEND registry owns (the
// 0149/0164 ruling, this server validates shape and never keeps a list). One roster row is
// therefore `<action>.<key>`: `pay.f8`, `step-next.f2`, `search.shift-f2`. The action is everything
// before the LAST dot; the key is what follows. Position orders the legend. Visible=false is how a
// dealer removes a key without forgetting which one it was.
//
// AND IT WORKS BEFORE ANYONE CONFIGURES ANYTHING. With no curation on file the roster is the
// DEFAULT below, the same "useful on day one" posture the hot list takes. The default deliberately
// avoids every key a browser owns (see browserOwnedKeys): a shipped default that fights the browser
// is a shipped default that gets turned off.

import (
	"context"
	"sort"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

// hotkeyKind is the kit item kind a roster row is filed under. An "action" in the kit's vocabulary
// is a small curated affordance that DOES something, which is exactly what a bound key is.
const hotkeyKind = settings.KindAction

// maxHotkeys bounds one roster. A legend is chrome pinned across the foot of the screen; past this
// many keycaps it is a second navigation bar, and nobody reads a second navigation bar.
const maxHotkeys = 8

// PosHotkey is one bound key: what it does, which key does it, and where it sits in the legend.
type PosHotkey struct {
	Action string `json:"action"`
	Key    string `json:"key"`
	// BrowserOwned marks a key mainstream browsers claim (F1 help, F3 find, F5 reload, F12 dev
	// tools…). It is NOT a refusal, a dealer who wants F12 for payment gets F12, because that is
	// literally what the client asked for. It is a fact the counter carries so the legend can mark
	// the key and the frontend can hold its escape rule (a modifier always returns the key to the
	// browser). Refusing the binding outright would be this server deciding a dealer's ergonomics.
	BrowserOwned bool `json:"browser_owned,omitempty"`
}

// PosHotkeys is the GET /api/v1/pos/hotkeys answer. Source names which level answered, "user",
// "dealer", "platform" or "default", for the same reason QuickPicks carries one: so a support call
// can explain why Dave's keys differ from everyone else's without a database session. D-111 added
// "platform"; the other three mean exactly what they meant before it.
type PosHotkeys struct {
	Source string      `json:"source"`
	Keys   []PosHotkey `json:"keys"`
}

// The counter's five actions. Names are STORED ids, so the registry rule applies: ADD, NEVER
// RENAME, a renamed action orphans every dealer's binding and silently kills their keys.
const (
	HotkeyStepNext = "step-next" // advance the accordion one step
	HotkeyStepBack = "step-back" // and back, because a one-way key is a trap
	HotkeyPay      = "pay"       // complete the sale (the ratified F8)
	HotkeyPark     = "park"      // park the ticket, the cheap abandon (N4)
	HotkeySearch   = "search"    // put the caret back in the scan/search field
)

// validHotkeyActions is the server's shape check on the action half of an id. It is deliberately a
// small closed set rather than "any string": an unknown ACTION cannot be rendered as anything (the
// frontend has no label and no behaviour for it), so admitting one would put a dead keycap on the
// legend. This is the one place the 0149 "never keep a list" posture yields, and it yields to the
// same reason it exists, an id nobody can render is not curation, it is a typo.
var validHotkeyActions = map[string]bool{
	HotkeyStepNext: true, HotkeyStepBack: true, HotkeyPay: true, HotkeyPark: true, HotkeySearch: true,
}

// defaultHotkeys is the shipped roster, BISTRACK'S KEYS, by ruling (27 Aug, R1).
//
// Ryan Rood, on the call: "for hot keys bistrack does [F]4 to move to the next step and then [F]12
// to take a payment. So, it'd be nice if we either hardcoded something like that where, you can hit
// F4 to move to the next step automatically." That is the whole argument. Every BisTrack refugee
// at this dealer arrives with F4-advances and F12-pays already in their hands, and a default that
// contradicts the muscle memory it is meant to serve is worse than having no default.
//
// SO F4 ADVANCES AND F12 PAYS, and the other three are picked AROUND those two:
//
//	step-back  shift-f4  the advance key's pair, one chord, one direction, learned once.
//	park       f9        a free bare F-key no mainstream browser claims.
//	search     f2        likewise, and the closest bare key to the scan box's own reflex.
//
// F12 IS A BROWSER KEY AND WE BIND IT ANYWAY. markOwned flags it, the legend marks it, and the
// frontend's escape rule keeps it reachable: `f12` matches F12 and nothing else, so Shift+F12 still
// opens dev tools. Refusing the client's own key to protect him from his own ergonomics would be
// this server overruling the person who has to use it.
//
// F8 IS DELIBERATELY LEFT UNBOUND. It used to ring the sale (the earlier "F8 completes a sale"
// default); rebinding it to some OTHER action would turn a trained reflex into the wrong action.
// An unbound key teaches nothing. A rebound one teaches a lie.
var defaultHotkeys = []PosHotkey{
	{Action: HotkeyStepNext, Key: "f4"},
	{Action: HotkeyStepBack, Key: "shift-f4"},
	{Action: HotkeyPay, Key: "f12"},
	{Action: HotkeyPark, Key: "f9"},
	{Action: HotkeySearch, Key: "f2"},
}

// DefaultHotkeys is the shipped roster, copied, the admin editor's "reset to the BisTrack
// defaults" is a write of exactly this list, so the reset and the cold-start roster can never
// drift into two different answers.
func DefaultHotkeys() []PosHotkey { return append([]PosHotkey(nil), defaultHotkeys...) }

// browserOwnedKeys are the function keys mainstream browsers bind. Binding one is allowed and
// flagged; the frontend's escape rule (any unrequested modifier yields the key back) is what keeps
// a dealer's choice from being a hijack.
var browserOwnedKeys = map[string]bool{
	"f1": true, "f3": true, "f5": true, "f6": true, "f7": true, "f10": true, "f11": true, "f12": true,
}

// PlatformRosterReader is the D-111 platform tier's read, as POS needs it. Satisfied by
// *settings.Service.
//
// A SEPARATE interface from SurfaceCuration, asserted at the call site rather than folded into it,
// for the reason every seam in this file is narrow: a curation reader that predates the platform
// tier (a unit-test fake, a scaffold) keeps satisfying SurfaceCuration and simply has no platform
// roster, which falls through to the shipped floor, exactly as an unreadable one does. Widening
// SurfaceCuration instead would have made every existing fake a compile error and taught nothing.
type PlatformRosterReader interface {
	PlatformRosterFor(ctx context.Context, surface string) ([]settings.PlatformHotkey, error)
}

// Hotkeys resolves the roster, four levels deep (D-111):
//
//	user     this person's own rows        (the tier is live code with no producer yet, see below)
//	dealer   their role profile's rows     (train 14's counter editor writes these)
//	platform the deployment's roster       (0191; what Hardscape House ships and can change)
//	default  the shipped BisTrack floor    (defaultHotkeys, below everything, always available)
//
// EACH LEVEL IS WHOLE-OR-ABSENT, and that is why Source can be one word. A dealer who has bound
// anything has bound everything they are going to; the levels do not merge row by row. Merging
// would make "why is Dave's F9 different" a question about four tables at once, which is exactly
// what Source exists to prevent anyone from having to ask.
//
// THE PLATFORM LEVEL IS WHY THE DEALER'S RESET IS NOW A DELETE. Before D-111 the level under the
// dealer was a constant in this file, so a dealer curating the defaults into their own level was
// untidy rather than wrong. Now a platform operator can change the level underneath, and a dealer
// who had once pressed "reset" and saved would be permanently deaf to it. The kit's role-items
// DELETE (settings.Service.ResetRoleItems) is what puts a dealer back to inheriting.
//
// THE DEGRADE POSTURE IS UNCHANGED AT EVERY LEVEL: an unreadable read ships the level below it,
// never a fault. A till whose keys stopped working because a settings read timed out is a till
// nobody trusts again.
func (s *Service) Hotkeys(ctx context.Context, branchID string) (PosHotkeys, error) {
	out := PosHotkeys{Source: "default", Keys: append([]PosHotkey(nil), defaultHotkeys...)}
	kit := s.surfaceCuration()
	if kit == nil {
		return markOwned(out), nil
	}
	cfg, err := kit.Config(ctx, QuickPickSurface, branchID)
	if err != nil {
		// An unreadable curation ships the roster below it, never a fault (see the header). The
		// platform level is still consulted: a settings-substrate read failing says nothing about
		// whether the platform roster is readable, and the deployment's own floor is a better
		// answer than the code's.
		if keys := s.platformRoster(ctx, kit); len(keys) > 0 {
			return markOwned(PosHotkeys{Source: "platform", Keys: keys}), nil
		}
		return markOwned(out), nil
	}
	if keys := hotkeyRows(cfg.UserItems); len(keys) > 0 {
		return markOwned(PosHotkeys{Source: "user", Keys: keys}), nil
	}
	if keys := hotkeyRows(cfg.RoleItems); len(keys) > 0 {
		return markOwned(PosHotkeys{Source: "dealer", Keys: keys}), nil
	}
	if keys := s.platformRoster(ctx, kit); len(keys) > 0 {
		return markOwned(PosHotkeys{Source: "platform", Keys: keys}), nil
	}
	return markOwned(out), nil
}

// platformRoster reads the deployment's counter roster, canonicalised through THIS package's own
// grammar.
//
// The canonicalisation is the point, and it is why the platform table stores a spelling rather than
// being validated into one by the kit. There are already four spellings of the chord grammar in
// this codebase and dashboard/hotkeys.go says the quiet part out loud, "a row the two disagree on
// is a key that can be stored but never pressed, so keep them identical." A fifth copy in a package
// that can import neither owner would be the copy that drifts. So a platform row arrives here as
// text and goes through splitHotkeyID exactly as a dealer's curation row does: a stored
// `ctrl-shift-b` and a stored `shift-ctrl-b` are one binding, repaired on the way out, and a row
// naming an action this build cannot carry out is dropped rather than drawn as a dead keycap.
//
// Anything unreadable answers an empty roster, so the caller falls to the shipped floor.
func (s *Service) platformRoster(ctx context.Context, kit SurfaceCuration) []PosHotkey {
	reader, ok := kit.(PlatformRosterReader)
	if !ok {
		return nil
	}
	rows, err := reader.PlatformRosterFor(ctx, settings.PlatformSurfaceCounter)
	if err != nil {
		return nil
	}
	items := make([]settings.Item, 0, len(rows))
	for _, r := range rows {
		items = append(items, settings.Item{
			Kind: hotkeyKind, ID: r.Action + "." + r.Binding, Position: r.Position, Visible: true,
		})
	}
	return hotkeyRows(items)
}

// hotkeyRows turns kit items into roster rows, in position order.
//
// Every rejection below is silent and local, which is the substrate's own contract: an id the
// frontend cannot render draws nothing rather than breaking the screen around it. A roster that
// resolved to nothing at all falls back to the default one level up, a counter with no keys is
// worse than a counter with the wrong keys.
func hotkeyRows(items []settings.Item) []PosHotkey {
	rows := make([]settings.Item, 0, len(items))
	for _, it := range items {
		if it.Kind == hotkeyKind && it.Visible {
			rows = append(rows, it)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Position < rows[j].Position })
	out := make([]PosHotkey, 0, len(rows))
	seenAction := map[string]bool{}
	seenKey := map[string]bool{}
	for _, it := range rows {
		action, key, ok := splitHotkeyID(it.ID)
		if !ok {
			continue
		}
		// One action, one key, and one key, one action. Two rows claiming F8 would make which one
		// wins depend on row order, a counter person pressing a key must never have to know that.
		if seenAction[action] || seenKey[key] {
			continue
		}
		seenAction[action] = true
		seenKey[key] = true
		out = append(out, PosHotkey{Action: action, Key: key})
		if len(out) >= maxHotkeys {
			break
		}
	}
	return out
}

// splitHotkeyID parses `<action>.<key>`, action is everything before the LAST dot, so an action
// name may itself contain dots without the parse becoming ambiguous. The key comes back CANONICAL
// (modifiers in shift-ctrl-alt order): a stored `ctrl-shift-b` is the same binding as
// `shift-ctrl-b`, and only one of those two spellings can ever match a pressed key, so the row is
// repaired on the way out rather than being served as a binding that silently never fires.
func splitHotkeyID(id string) (action, key string, ok bool) {
	s := strings.ToLower(strings.TrimSpace(id))
	i := strings.LastIndex(s, ".")
	if i <= 0 || i == len(s)-1 {
		return "", "", false
	}
	action, key = s[:i], s[i+1:]
	if !validHotkeyActions[action] {
		return "", "", false
	}
	canon, ok := canonicalHotkeyKey(key)
	if !ok {
		return "", "", false
	}
	return action, canon, true
}

// validHotkeyKey reports whether a key is bindable at all. Order-tolerant on purpose, see
// canonicalHotkeyKey, which is where the spelling is settled.
func validHotkeyKey(key string) bool {
	_, ok := canonicalHotkeyKey(key)
	return ok
}

// canonicalHotkeyKey validates a binding and returns its ONE canonical spelling.
//
// TWO KINDS OF KEY ARE BINDABLE, and the difference is whether the key TYPES anything:
//
//   - A FUNCTION KEY stands alone, f1..f12, optionally with shift-/ctrl-/alt-. It inserts no
//     character, so claiming it costs the counter's scan field nothing.
//   - A LETTER OR DIGIT requires ctrl or alt, `ctrl-b`, `shift-ctrl-b`. Colton asked for chords
//     like "control shift b" by name (27 Aug), so refusing them outright would refuse the request.
//     Shift ALONE is not enough: shift+b is how a person types a capital B, and a counter screen
//     whose premise is that everything typed lands in the scan box must never claim that.
//
// Modifiers may arrive in any order (an older row, a hand-written curation) and come back sorted;
// a repeated modifier is still a refusal, because that is a typo rather than a spelling.
func canonicalHotkeyKey(key string) (string, bool) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(key)), "-")
	base := parts[len(parts)-1]
	seen := map[string]bool{}
	for _, m := range parts[:len(parts)-1] {
		switch m {
		case "shift", "ctrl", "alt":
			if seen[m] {
				return "", false
			}
			seen[m] = true
		default:
			return "", false
		}
	}
	if !isFunctionKeyName(base) {
		if !isTypedKeyName(base) {
			return "", false
		}
		if !seen["ctrl"] && !seen["alt"] {
			return "", false
		}
	}
	out := make([]string, 0, 4)
	for _, m := range []string{"shift", "ctrl", "alt"} { // the canonical order, stated once
		if seen[m] {
			out = append(out, m)
		}
	}
	return strings.Join(append(out, base), "-"), true
}

// isTypedKeyName is one letter or digit, a key that TYPES, and therefore is only bindable with a
// ctrl or alt modifier in front of it.
func isTypedKeyName(k string) bool {
	if len(k) != 1 {
		return false
	}
	c := k[0]
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}

func isFunctionKeyName(k string) bool {
	if len(k) < 2 || k[0] != 'f' {
		return false
	}
	n := 0
	for _, c := range k[1:] {
		if c < '0' || c > '9' {
			return false
		}
		n = n*10 + int(c-'0')
	}
	return n >= 1 && n <= 12
}

// markOwned stamps the browser-owned flag on the way out, so the answer carries the fact whichever
// level produced it.
func markOwned(in PosHotkeys) PosHotkeys {
	for i := range in.Keys {
		parts := strings.Split(in.Keys[i].Key, "-")
		in.Keys[i].BrowserOwned = browserOwnedKeys[parts[len(parts)-1]]
	}
	return in
}
