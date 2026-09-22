package dashboard

// hotkeys.go, DIB-496 (ruling D-095): the DEALER-level hotkey map.
//
// Hotkeys are configured once for the whole company (0164), not per user: "F1 always opens a cash
// sale" is only true if F1 means the same thing at every desk. This file owns what a stored row
// MEANS, the binding grammar, normalisation, and the shipped defaults, so every rule is testable
// without a database, exactly the way layout.go owns what a layout row means.
//
// WHAT THE SERVER DOES NOT KNOW, ON PURPOSE. An action id ('sale.cash.new') names an entry in the
// FRONTEND action registry (app/src/hotkey-logic.ts): what it does, where it goes, and whether the
// caller's role may follow it are the client's decisions, made with the same data the Apps grid
// already gates on (nav-access.ts, an entry-point convenience, not a security boundary; the real
// RBAC stays on the routes). The server therefore stores action ids as opaque strings and validates
// only their SHAPE, the same decision tabs.go made for pin keys, and for the same reason: a copy
// of the client's registry kept here would only ever be a stale copy.
//
// WHY THE MAP RIDES THE DASHBOARD PAYLOAD. The shell needs the bindings before the first keypress,
// and it already makes exactly one config request per session (GET /api/v1/dashboard, the same
// ride DIB-433 gave the pinned strip). A second round trip would be a window in which F1 silently
// does nothing.

import "strings"

// HotkeyBinding is one dealer-level row: a canonical chord spelling bound to a named action.
// This is both the storage shape (0164) and the wire shape (Layout.Hotkeys).
type HotkeyBinding struct {
	Binding string `json:"binding"`
	Action  string `json:"action"`
}

// DefaultHotkeys is the code-side copy of 0164's seed, SEED DEFAULTS AWAITING THE CLIENT'S
// OFFICIAL LIST (D-095 names "F1 → cash sale" and the rest are sensible guesses; see the
// migration's header for the reasoning behind each key).
//
// Code and migration agree deliberately, for the reason quickaction.go gives for its own defaults:
// when the table cannot be read (a build ahead of its migrations, a config-store blip), the counter
// gets the F1 they had yesterday rather than a keyboard that silently went dead. An EMPTY resolved
// map is different from an unreadable one and is respected as a decision, see Service.hotkeys.
//
// D-111 CHANGED WHAT THIS LIST IS UNDERNEATH. It is no longer the only thing beneath a dealer's
// rows: migration 0191 introduced a PLATFORM roster (platform_hotkeys, surface 'app.shell') that a
// platform operator edits without a release, and the repository resolves dealer-over-platform
// before this list is ever reached. This stays as the floor under BOTH of them, unchanged in
// purpose: a build running ahead of its migrations still has keys.
//
// F8 → sale.pay IS RETIRED HERE (D-111, the standing recommendation carried since train 14). Pay
// lives on the COUNTER roster, F12, by the client's own BisTrack ruling, and the shell
// advertising a second, different pay key is how the till's Complete-sale button came to display a
// stale one. F8 is left UNBOUND rather than rebound, on both maps and at every shipped default:
// an unbound key teaches nothing, and a rebound one teaches a trained hand the wrong action.
var DefaultHotkeys = []HotkeyBinding{
	{Binding: "F1", Action: "sale.cash.new"},
	{Binding: "F2", Action: "sales.tab.new"},
	{Binding: "F3", Action: "order.quick.open"},
	{Binding: "F4", Action: "search.focus"},
	{Binding: "F9", Action: "assist.open"},
}

// hotkeyModifiers is the canonical modifier order. Meta/Cmd is deliberately absent: the OS and the
// browser own that key, and a chord the shell claims there would fire for keystrokes aimed at the
// machine, the same reasoning isBackChord (_shell-history.ts) records for refusing Ctrl/Meta.
var hotkeyModifiers = []string{"Ctrl", "Alt", "Shift"}

// maxHotkeyActionLen bounds a stored action id. Registry spellings are short dotted words
// ('sale.cash.new'); sixty-four characters is generous headroom and still refuses a row that is
// clearly not an action name.
const maxHotkeyActionLen = 64

// NormalizeHotkeyBinding canonicalises one chord spelling, or returns ok=false for a string that
// does not name a chord this system supports.
//
// The grammar: optional modifiers (Ctrl, Alt, Shift, canonical order, any input order, any case,
// no duplicates) joined by '+', then ONE key token: F1-F12, a letter A-Z, or a digit 0-9. A bare
// letter or digit is refused, typing 'k' into open space must never fire an action, and the
// input-focus guard on the client cannot protect a screen with nothing focusable on it. Shift
// alone does not rescue one either: Shift+K is how a person types a capital K. So letters and
// digits require Ctrl or Alt; bare F-keys are the point of the feature. The client grammar
// (app/src/hotkey-logic.ts bindingFromEvent) is this rule's mirror, a row the two disagree on is
// a key that can be stored but never pressed, so keep them identical.
func NormalizeHotkeyBinding(raw string) (string, bool) {
	parts := strings.Split(strings.TrimSpace(raw), "+")
	if len(parts) == 0 {
		return "", false
	}
	seen := map[string]bool{}
	for _, p := range parts[:len(parts)-1] {
		mod := canonicalModifier(p)
		if mod == "" || seen[mod] {
			return "", false
		}
		seen[mod] = true
	}
	key, ok := canonicalKeyToken(parts[len(parts)-1])
	if !ok {
		return "", false
	}
	if !strings.HasPrefix(key, "F") && !seen["Ctrl"] && !seen["Alt"] {
		return "", false // a bare (or merely shifted) letter/digit is a keystroke, not a chord
	}
	var b strings.Builder
	for _, mod := range hotkeyModifiers {
		if seen[mod] {
			b.WriteString(mod)
			b.WriteString("+")
		}
	}
	b.WriteString(key)
	return b.String(), true
}

func canonicalModifier(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "ctrl", "control":
		return "Ctrl"
	case "alt", "option":
		return "Alt"
	case "shift":
		return "Shift"
	}
	return ""
}

// canonicalKeyToken accepts F1-F12, A-Z and 0-9 (any case), spelled canonically on the way out.
func canonicalKeyToken(p string) (string, bool) {
	t := strings.ToUpper(strings.TrimSpace(p))
	if len(t) >= 2 && len(t) <= 3 && t[0] == 'F' {
		n := t[1:]
		if n != "0" && isDigits(n) && (len(n) == 1 || (n[0] == '1' && n[1] <= '2')) {
			return t, true
		}
		return "", false
	}
	if len(t) == 1 && ((t[0] >= 'A' && t[0] <= 'Z') || (t[0] >= '0' && t[0] <= '9')) {
		return t, true
	}
	return "", false
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// validHotkeyAction is the SHAPE gate for a stored action id: lowercase dotted words, the spelling
// every registered id already uses ('sale.new', 'sale.cash.new'). Anything else is dropped on
// read, a hand-edited row cannot smuggle markup or a kilobyte of junk onto every session's
// payload. Which actions EXIST is the client registry's question, not this one's.
func validHotkeyAction(action string) bool {
	if action == "" || len(action) > maxHotkeyActionLen {
		return false
	}
	for i := 0; i < len(action); i++ {
		c := action[i]
		ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '-'
		if !ok {
			return false
		}
	}
	return true
}

// NormalizeHotkeys cleans a stored map for the wire: bindings canonicalised, malformed rows and
// malformed action ids dropped, duplicate bindings collapsed to their first occurrence. Every rule
// is a read-side defence, there is no write API yet (D-095 gates the admin editor), so the rows
// arrive from SQL someone typed, and this is where a typo degrades to "that key does nothing"
// instead of to a broken payload.
//
// Never returns nil: an empty ARRAY is the honest wire answer for "this dealer bound nothing", and
// it saves the client from distinguishing [] from null, the same choice the quick-action handler
// records.
func NormalizeHotkeys(rows []HotkeyBinding) []HotkeyBinding {
	out := make([]HotkeyBinding, 0, len(rows))
	seen := map[string]bool{}
	for _, r := range rows {
		binding, ok := NormalizeHotkeyBinding(r.Binding)
		if !ok || seen[binding] || !validHotkeyAction(r.Action) {
			continue
		}
		seen[binding] = true
		out = append(out, HotkeyBinding{Binding: binding, Action: r.Action})
	}
	return out
}
