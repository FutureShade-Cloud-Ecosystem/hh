package dashboard

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// -- DIB-496: the dealer hotkey map, grammar, normalisation, and the wire ride ------------------

func TestNormalizeHotkeyBinding_CanonicalisesSpellings(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"F1", "F1"},
		{"f1", "F1"},
		{" f10 ", "F10"},
		{"F12", "F12"},
		{"ctrl+shift+k", "Ctrl+Shift+K"},
		// Input modifier ORDER must not matter; the canonical order is Ctrl, Alt, Shift.
		{"shift+ctrl+k", "Ctrl+Shift+K"},
		{"ALT+9", "Alt+9"},
		{"option+p", "Alt+P"}, // the macOS spelling of the same key
		{"control+alt+shift+f2", "Ctrl+Alt+Shift+F2"},
		{"Ctrl + K", "Ctrl+K"}, // whitespace around the joiner is a human spelling, not a refusal
	}
	for _, c := range cases {
		got, ok := NormalizeHotkeyBinding(c.in)
		if !ok || got != c.want {
			t.Errorf("NormalizeHotkeyBinding(%q) = %q, %v; want %q, true", c.in, got, ok, c.want)
		}
	}
}

func TestNormalizeHotkeyBinding_RefusesWhatIsNotAChord(t *testing.T) {
	bad := []string{
		"",              // nothing
		"K",             // a bare letter is a keystroke someone will type, not a chord
		"7",             // a bare digit, same
		"Shift+K",       // Shift+K is how a person types a capital K, Shift alone rescues nothing
		"F0",            // no such key
		"F13",           // beyond the keyboard this system supports
		"F123",          // not a key at all
		"Ctrl+Ctrl+K",   // a duplicated modifier is a typo, not a stricter chord
		"Meta+K",        // the OS owns that key, same refusal isBackChord makes
		"Cmd+K",         // ditto, macOS spelling
		"Ctrl+",         // a chord with no key
		"+F1",           // an empty modifier slot
		"Ctrl+Enter",    // named keys outside the grammar wait for the client's official list
		"Ctrl+Shift+ab", // two letters is not a key token
	}
	for _, in := range bad {
		if got, ok := NormalizeHotkeyBinding(in); ok {
			t.Errorf("NormalizeHotkeyBinding(%q) = %q, true; want refusal", in, got)
		}
	}
}

func TestNormalizeHotkeys_CleansTheStoredMap(t *testing.T) {
	got := NormalizeHotkeys([]HotkeyBinding{
		{Binding: "f1", Action: "sale.cash.new"},              // canonicalised
		{Binding: "F1", Action: "something.else"},             // duplicate binding, first one wins
		{Binding: "K", Action: "sale.cash.new"},               // bare letter, dropped
		{Binding: "F2", Action: "Not An Action!"},             // malformed action id, dropped
		{Binding: "F3", Action: ""},                           // empty action, dropped
		{Binding: "shift+ctrl+k", Action: "order.quick.open"}, // canonicalised, kept
	})
	want := []HotkeyBinding{
		{Binding: "F1", Action: "sale.cash.new"},
		{Binding: "Ctrl+Shift+K", Action: "order.quick.open"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeHotkeys = %#v; want %#v", got, want)
	}
}

// The wire answer for "no hotkeys" is an empty ARRAY, never nil, the same contract the
// quick-action handler records, so no client ever has to distinguish [] from null.
func TestNormalizeHotkeys_NeverReturnsNil(t *testing.T) {
	if NormalizeHotkeys(nil) == nil {
		t.Fatal("NormalizeHotkeys(nil) must be an empty slice, not nil")
	}
	if NormalizeHotkeys([]HotkeyBinding{{Binding: "junk"}}) == nil {
		t.Fatal("NormalizeHotkeys of only-junk must be an empty slice, not nil")
	}
}

// The shipped defaults must already BE canonical: a seed the normaliser rewrites would mean the
// stored rows and the served rows disagree about the same keys.
//
// THE COUNT PIN HAS MOVED TWICE, BOTH TIMES DELIBERATELY, AND ITS JOB HAS NEVER CHANGED: the
// roster may not drift SILENTLY. UNION-TRAIN11 moved it 5 → 6 when 0171 added F8 → sale.pay.
// D-111 moves it 6 → 5 by retiring that same row, which is the standing recommendation carried
// since train 14 and restated in train 15: pay lives on the COUNTER roster (F12, the client's own
// BisTrack ruling), and a shell map that also advertises a pay key is how the till's Complete-sale
// button came to display a stale one. Migration 0191 removes the seeded row in the same commit -
// scoped to `binding='F8' AND action_id='sale.pay'`, so a dealer who re-pointed F8 at something
// else keeps the row 0171's ON CONFLICT DO NOTHING promised them.
//
// F8 IS NOW UNBOUND AT EVERY SHIPPED DEFAULT, on both maps, and the assertion below says so
// outright rather than leaving it to the count: an unbound key teaches nothing, and a rebound one
// teaches a trained hand the wrong action.
func TestDefaultHotkeys_AreCanonicalAndSurviveNormalisation(t *testing.T) {
	if len(DefaultHotkeys) != 5 {
		t.Fatalf("the D-095 seed is five actions (placeholders awaiting the client's list; F8 → sale.pay retired by D-111 / 0191); got %d", len(DefaultHotkeys))
	}
	for _, hb := range DefaultHotkeys {
		if hb.Binding == "F8" {
			t.Fatalf("F8 is bound to %s, the retired pay key must be left unbound on the shell map, not reused", hb.Action)
		}
		if hb.Action == "sale.pay" {
			t.Fatalf("sale.pay is still on the shell map (as %s), pay belongs to the counter roster alone", hb.Binding)
		}
	}
	if !reflect.DeepEqual(NormalizeHotkeys(DefaultHotkeys), DefaultHotkeys) {
		t.Fatalf("DefaultHotkeys must normalise to themselves; got %#v", NormalizeHotkeys(DefaultHotkeys))
	}
}

// The map rides the dashboard payload, the one request the shell already makes, and the stored
// rows are what it carries.
func TestResolve_ServesTheDealerHotkeyMap(t *testing.T) {
	f := newFakeStore()
	f.hotkeys = []HotkeyBinding{{Binding: "f6", Action: "assist.open"}} // sloppily spelled on purpose
	svc := NewService(f, nil)
	layout, err := svc.Resolve(context.Background(), Scope{UserID: "u1", RoleCode: "COUNTER"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := []HotkeyBinding{{Binding: "F6", Action: "assist.open"}}
	if !reflect.DeepEqual(layout.Hotkeys, want) {
		t.Fatalf("Layout.Hotkeys = %#v; want the normalised stored map %#v", layout.Hotkeys, want)
	}
}

// DEGRADE TO THE DEFAULT, NOT TO DEAD KEYS: an unreadable hotkey table serves the shipped seed
// (the F1 the counter had yesterday), logs, and does NOT fail the home screen.
func TestResolve_HotkeyStoreFailureFallsBackToShippedDefaults(t *testing.T) {
	f := newFakeStore()
	f.hotkeysErr = errors.New("relation dealer_hotkeys does not exist")
	svc := NewService(f, nil)
	layout, err := svc.Resolve(context.Background(), Scope{UserID: "u1", RoleCode: "COUNTER"})
	if err != nil {
		t.Fatalf("a hotkey-store failure must not fail the dashboard: %v", err)
	}
	if !reflect.DeepEqual(layout.Hotkeys, DefaultHotkeys) {
		t.Fatalf("Layout.Hotkeys on store failure = %#v; want the shipped defaults", layout.Hotkeys)
	}
}

// An EMPTY table is a dealer's decision (every binding deleted = no hotkeys) and is respected -
// unlike an unreadable one. The two must not collapse into each other.
func TestResolve_EmptyHotkeyTableMeansNoHotkeys(t *testing.T) {
	f := newFakeStore() // f.hotkeys stays nil: the store answers "no rows" with no error
	svc := NewService(f, nil)
	layout, err := svc.Resolve(context.Background(), Scope{UserID: "u1", RoleCode: "COUNTER"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if layout.Hotkeys == nil {
		t.Fatal("Layout.Hotkeys must be an empty array on the wire, not null")
	}
	if len(layout.Hotkeys) != 0 {
		t.Fatalf("an empty stored map must serve no hotkeys, not the defaults; got %#v", layout.Hotkeys)
	}
}
