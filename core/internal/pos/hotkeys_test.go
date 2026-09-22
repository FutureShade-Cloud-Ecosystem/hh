package pos

// hotkeys_test.go, the counter's key roster (hotkeys.go).
//
// The client asked for BisTrack muscle memory and said, in the same breath, that his people will
// want their old keys. That is the whole reason these tests exist at the DATA layer rather than
// over a component: what is being pinned is that the roster is configuration a dealer owns, that it
// is useful before anyone configures it, and that a bad configuration degrades to something a
// counter can still work with, never to a till with no keys.

import (
	"context"
	"errors"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

func action(id string, pos int, visible bool) settings.Item {
	return settings.Item{Kind: settings.KindAction, ID: id, Position: pos, Visible: visible}
}

func hotkeyHarness(t *testing.T, cfg *settings.SurfaceConfig, err error) *harness {
	t.Helper()
	h := newHarness()
	if cfg != nil || err != nil {
		c := settings.SurfaceConfig{}
		if cfg != nil {
			c = *cfg
		}
		h.svc.WithSurfaceCuration(&fakeCuration{cfg: c, err: err})
	}
	return h
}

func keysOf(in PosHotkeys) map[string]string {
	out := map[string]string{}
	for _, k := range in.Keys {
		out[k.Action] = k.Key
	}
	return out
}

// -- useful on day one ---------------------------------------------------------------------------

func TestHotkeys_WithNoCurationTheDefaultRosterAnswers(t *testing.T) {
	h := hotkeyHarness(t, &settings.SurfaceConfig{}, nil)
	out, err := h.svc.Hotkeys(context.Background(), "branch-trt")
	if err != nil {
		t.Fatal(err)
	}
	if out.Source != "default" {
		t.Fatalf("source = %q, want default", out.Source)
	}
	// The 27-Aug ruling (R1) IS this map: BisTrack's F4-advances / F12-pays, with the other three
	// picked around them. Changing a key here is changing what a counter person's hands already do.
	got := keysOf(out)
	for action, key := range map[string]string{
		HotkeyStepNext: "f4", HotkeyStepBack: "shift-f4", HotkeyPay: "f12", HotkeyPark: "f9", HotkeySearch: "f2",
	} {
		if got[action] != key {
			t.Fatalf("%s = %q, want %q", action, got[action], key)
		}
	}
	// F8 used to ring the sale. It is now UNBOUND rather than rebound: an unbound key teaches
	// nothing, a rebound one teaches a trained hand the wrong action.
	for _, k := range out.Keys {
		if k.Key == "f8" {
			t.Fatalf("f8 is bound to %s, the retired ring key must be left unbound, not reused", k.Action)
		}
	}
}

func TestHotkeys_TheShippedDefaultTakesNoKeyABrowserOwns(t *testing.T) {
	// SUPERSEDED IN PART BY THE 27-AUG RULING (R1), and rewritten the way D-107 rewrote the
	// DIB-425 floor: keep the name, pin what the gate was actually protecting.
	//
	// What it protected: a default that silently fights the host browser is a default that gets
	// turned off, and then the feature does not exist. What the ruling changed: the client asked
	// for F12 BY NAME ("bistrack does ... [F]12 to take a payment"), so exactly one browser-owned
	// key is now shipped on purpose. What still must hold, and is what this test now asserts:
	//   * the ONLY browser-owned key in the shipped default is the pay key the client named, and
	//   * it is FLAGGED, so the legend can mark it and the frontend's escape rule is visible.
	// Every other shipped key must still be one the browser does not claim.
	h := hotkeyHarness(t, &settings.SurfaceConfig{}, nil)
	out, _ := h.svc.Hotkeys(context.Background(), "")
	for _, k := range out.Keys {
		if !k.BrowserOwned {
			continue
		}
		if k.Action != HotkeyPay {
			t.Fatalf("the default roster binds %s to %s, which the browser owns", k.Key, k.Action)
		}
		if k.Key != "f12" {
			t.Fatalf("the pay key is %q; only the client's own F12 may take a browser key", k.Key)
		}
	}
	if !keysOwned(out)[HotkeyPay] {
		t.Fatal("the default pay key is F12 and must be FLAGGED browser-owned, so the legend can mark it")
	}
}

func TestHotkeys_TheRatifiedPayKeyIsHonoured(t *testing.T) {
	// The ratified pay key is now F12. The commander's 27-Aug ruling ("Use bistrack patterns")
	// supersedes the earlier "F8 completes a sale" default, on the client's own words: "bistrack
	// does [F]4 to move to the next step and then [F]12 to take a payment."
	h := hotkeyHarness(t, &settings.SurfaceConfig{}, nil)
	if keysOf(mustHotkeys(t, h))[HotkeyPay] != "f12" {
		t.Fatal("the default pay key must be the ratified F12 (BisTrack's payment key)")
	}
	_ = h
}

// keysOwned maps action -> whether the answer flagged that key as one a browser claims.
func keysOwned(in PosHotkeys) map[string]bool {
	out := map[string]bool{}
	for _, k := range in.Keys {
		out[k.Action] = k.BrowserOwned
	}
	return out
}

func mustHotkeys(t *testing.T, h *harness) PosHotkeys {
	t.Helper()
	out, err := h.svc.Hotkeys(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// -- the dealer owns the keys --------------------------------------------------------------------

func TestHotkeys_DealerCurationReplacesTheDefaultRoster(t *testing.T) {
	cfg := settings.SurfaceConfig{RoleItems: []settings.Item{
		action("pay.f12", 0, true),
		action("step-next.f1", 1, true),
	}}
	h := hotkeyHarness(t, &cfg, nil)
	out := mustHotkeys(t, h)
	if out.Source != "dealer" {
		t.Fatalf("source = %q, want dealer", out.Source)
	}
	if keysOf(out)[HotkeyPay] != "f12" {
		t.Fatalf("the dealer's F12 must win: %+v", out.Keys)
	}
	if len(out.Keys) != 2 {
		t.Fatalf("a curated roster is WHOLE, the default must not be merged in: %+v", out.Keys)
	}
}

func TestHotkeys_ABrowserOwnedChoiceIsFlaggedNotRefused(t *testing.T) {
	// The client asked for "an F12-style key takes payment" in as many words. Refusing it would be
	// this server overruling a dealer's ergonomics; the answer is to carry the fact so the legend
	// can mark it and the frontend can hold its escape rule.
	cfg := settings.SurfaceConfig{RoleItems: []settings.Item{action("pay.f12", 0, true)}}
	out := mustHotkeys(t, hotkeyHarness(t, &cfg, nil))
	if len(out.Keys) != 1 || !out.Keys[0].BrowserOwned {
		t.Fatalf("F12 must arrive bound AND flagged: %+v", out.Keys)
	}
}

func TestHotkeys_AUsersOwnRosterWinsWholeOverTheDealers(t *testing.T) {
	cfg := settings.SurfaceConfig{
		RoleItems: []settings.Item{action("pay.f8", 0, true), action("park.f9", 1, true)},
		UserItems: []settings.Item{action("pay.f4", 0, true)},
	}
	out := mustHotkeys(t, hotkeyHarness(t, &cfg, nil))
	if out.Source != "user" || len(out.Keys) != 1 || out.Keys[0].Key != "f4" {
		t.Fatalf("the person's own set wins whole: %+v", out)
	}
}

func TestHotkeys_PositionOrdersTheLegend(t *testing.T) {
	cfg := settings.SurfaceConfig{RoleItems: []settings.Item{
		action("park.f9", 5, true),
		action("pay.f8", 1, true),
		action("step-next.f2", 3, true),
	}}
	out := mustHotkeys(t, hotkeyHarness(t, &cfg, nil))
	want := []string{HotkeyPay, HotkeyStepNext, HotkeyPark}
	for i, a := range want {
		if out.Keys[i].Action != a {
			t.Fatalf("legend order = %+v, want %v", out.Keys, want)
		}
	}
}

func TestHotkeys_HiddenIsHowADealerRemovesAKeyWithoutForgettingIt(t *testing.T) {
	cfg := settings.SurfaceConfig{RoleItems: []settings.Item{
		action("pay.f8", 0, true),
		action("park.f9", 1, false),
	}}
	out := mustHotkeys(t, hotkeyHarness(t, &cfg, nil))
	if len(out.Keys) != 1 || out.Keys[0].Action != HotkeyPay {
		t.Fatalf("a hidden row must not bind a key: %+v", out.Keys)
	}
}

// -- a bad configuration degrades; it never breaks the till --------------------------------------

func TestHotkeys_AnUnreadableCurationShipsTheDefaultRoster(t *testing.T) {
	// A till whose keys stopped working because a settings read timed out is a till nobody trusts
	// again. Empty-never-error, applied to ergonomics.
	out := mustHotkeys(t, hotkeyHarness(t, nil, errors.New("settings unreachable")))
	if out.Source != "default" || len(out.Keys) != len(defaultHotkeys) {
		t.Fatalf("an outage must ship the default roster: %+v", out)
	}
}

func TestHotkeys_ARosterOfOnlyGarbageFallsBackRatherThanGoingSilent(t *testing.T) {
	cfg := settings.SurfaceConfig{RoleItems: []settings.Item{
		action("nosuchaction.f8", 0, true),
		action("pay.f13", 1, true),
		action("pay.q", 2, true),
		action("payf8", 3, true),
	}}
	out := mustHotkeys(t, hotkeyHarness(t, &cfg, nil))
	if out.Source != "default" {
		t.Fatalf("a counter with no keys is worse than a counter with the wrong keys: %+v", out)
	}
}

func TestHotkeys_UnrenderableRowsAreDroppedSilentlyBesideGoodOnes(t *testing.T) {
	cfg := settings.SurfaceConfig{RoleItems: []settings.Item{
		action("pay.f8", 0, true),
		action("nosuchaction.f2", 1, true), // no label, no behaviour, a dead keycap
		action("park.f99", 2, true),        // not a function key
	}}
	out := mustHotkeys(t, hotkeyHarness(t, &cfg, nil))
	if len(out.Keys) != 1 || out.Keys[0].Action != HotkeyPay {
		t.Fatalf("the good row stands, the rest draw nothing: %+v", out.Keys)
	}
}

func TestHotkeys_TwoRowsMayNotClaimTheSameKeyOrTheSameAction(t *testing.T) {
	// Which one wins would depend on row order, and a counter person pressing a key must never have
	// to know the row order of a settings table.
	cfg := settings.SurfaceConfig{RoleItems: []settings.Item{
		action("pay.f8", 0, true),
		action("park.f8", 1, true),
		action("pay.f9", 2, true),
	}}
	out := mustHotkeys(t, hotkeyHarness(t, &cfg, nil))
	if len(out.Keys) != 1 || out.Keys[0].Key != "f8" || out.Keys[0].Action != HotkeyPay {
		t.Fatalf("first row wins, the collisions draw nothing: %+v", out.Keys)
	}
}

func TestHotkeys_TheLegendIsBoundedSoItCannotBecomeASecondNavBar(t *testing.T) {
	var items []settings.Item
	for i, a := range []string{"pay", "park", "step-next", "step-back", "search"} {
		items = append(items, action(a+".f"+string(rune('1'+i)), i, true))
	}
	// Duplicates beyond the roster's actions cannot exist (one action, one key), so the cap is
	// tested for what it is: a ceiling the resolver holds regardless of what is on file.
	out := mustHotkeys(t, hotkeyHarness(t, &settings.SurfaceConfig{RoleItems: items}, nil))
	if len(out.Keys) > maxHotkeys {
		t.Fatalf("a legend past %d keycaps is a second navigation bar: %d", maxHotkeys, len(out.Keys))
	}
}

// -- the key grammar -----------------------------------------------------------------------------

func TestHotkeys_OnlyFunctionKeysAndTheirChordsAreBindable(t *testing.T) {
	// A letter bound at the counter would fire while somebody types a customer name, this screen
	// expects every ordinary keystroke to land in the scan field.
	for _, good := range []string{"f1", "f8", "f12", "shift-f2", "ctrl-f4", "shift-ctrl-f8", "alt-f9"} {
		if !validHotkeyKey(good) {
			t.Fatalf("%q should be bindable", good)
		}
	}
	for _, bad := range []string{"q", "f0", "f13", "f", "", "meta-f8", "shift-shift-f2", "f8-shift", "shift-"} {
		if validHotkeyKey(bad) {
			t.Fatalf("%q must not be bindable", bad)
		}
	}
}

func TestHotkeys_TheActionIsEverythingBeforeTheLASTDot(t *testing.T) {
	a, k, ok := splitHotkeyID("STEP-NEXT.F2")
	if !ok || a != HotkeyStepNext || k != "f2" {
		t.Fatalf("got %q/%q/%v", a, k, ok)
	}
	if _, _, ok := splitHotkeyID(".f8"); ok {
		t.Fatal("an empty action is not an action")
	}
	if _, _, ok := splitHotkeyID("pay."); ok {
		t.Fatal("an empty key is not a key")
	}
}

// -- it is ergonomics, not permission ------------------------------------------------------------

func TestHotkeys_TheRosterCarriesNoPermissionAndNoMoney(t *testing.T) {
	// The reason this lives in the presentation substrate at all. A bound key does exactly what its
	// visible control does; it grants nothing. If a field here ever implied otherwise, the roster
	// would belong in a typed, CHECK-constrained policy column instead.
	out := mustHotkeys(t, hotkeyHarness(t, &settings.SurfaceConfig{}, nil))
	for _, k := range out.Keys {
		if !validHotkeyActions[k.Action] {
			t.Fatalf("an action nobody can render reached the wire: %q", k.Action)
		}
	}
}
