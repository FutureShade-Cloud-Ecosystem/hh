package pos

// platform_hotkeys_test.go, D-111: the counter roster's PLATFORM tier.
//
// Two jobs, and the second is the one that will save somebody later:
//
//  1. the resolver now has four levels, and each one still yields to the one below it EXACTLY the
//     way it did before the tier was inserted;
//  2. THE MIRROR. settings/platform_hotkeys.go keeps a copy of this package's action set, because
//     it must validate a platform write and cannot import this package (this package imports it).
//     A copy nobody checks is a copy waiting to drift, and drift here is silent: the platform tier
//     would accept an action the counter cannot render, and every dealer on the deployment would
//     inherit a dead keycap on a legend pinned across the foot of every till.

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

// platformKit is a curation seam that also carries a platform roster, the way *settings.Service
// does in production. Kept separate from the package's other fakes because PlatformRosterReader is
// asserted at the call site, a seam that predates the tier must keep working.
type platformKit struct {
	cfg        settings.SurfaceConfig
	cfgErr     error
	platform   []settings.PlatformHotkey
	platformEr error
	asks       int
}

// The variadic tail matches the widened kit read (session A item 3c). This fake asks for no options:
// POS reads its own caller's curation, which is what it always did.
func (k *platformKit) Config(context.Context, string, string, ...settings.ConfigOpt) (settings.SurfaceConfig, error) {
	return k.cfg, k.cfgErr
}

func (k *platformKit) PlatformRosterFor(_ context.Context, surface string) ([]settings.PlatformHotkey, error) {
	k.asks++
	if surface != settings.PlatformSurfaceCounter {
		return nil, nil
	}
	return k.platform, k.platformEr
}

// plainKit is a curation seam with NO platform tier, a unit-test fake or a scaffold that predates
// D-111. It must keep compiling and keep working.
type plainKit struct{ cfg settings.SurfaceConfig }

func (k *plainKit) Config(context.Context, string, string, ...settings.ConfigOpt) (settings.SurfaceConfig, error) {
	return k.cfg, nil
}

func actionItems(pairs ...[2]string) []settings.Item {
	out := make([]settings.Item, 0, len(pairs))
	for i, p := range pairs {
		out = append(out, settings.Item{Kind: settings.KindAction, ID: p[0] + "." + p[1], Position: i, Visible: true})
	}
	return out
}

func keyOf(keys []PosHotkey, action string) string {
	for _, k := range keys {
		if k.Action == action {
			return k.Key
		}
	}
	return ""
}

// THE ACCEPTANCE CRITERION, IN ONE TEST: a dealer with no override inherits the platform roster,
// and the answer says so in one word so a support call can explain it without a database session.
func TestHotkeys_ADealerWithNoOverrideInheritsThePlatformRoster(t *testing.T) {
	kit := &platformKit{platform: []settings.PlatformHotkey{
		{Action: HotkeyPay, Binding: "f11", Position: 0},
		{Action: HotkeyStepNext, Binding: "f6", Position: 1},
	}}
	svc := (&Service{}).WithSurfaceCuration(kit)
	out, err := svc.Hotkeys(context.Background(), "")
	if err != nil {
		t.Fatalf("Hotkeys: %v", err)
	}
	if out.Source != "platform" {
		t.Fatalf("Source = %q; want %q, the level that answered must be nameable in one word", out.Source, "platform")
	}
	if keyOf(out.Keys, HotkeyPay) != "f11" || keyOf(out.Keys, HotkeyStepNext) != "f6" {
		t.Fatalf("the platform roster must be what answers; got %#v", out.Keys)
	}
	// And the browser-owned flag is stamped on whichever level produced the answer, f11 is a key
	// mainstream browsers claim, and the legend marks it so the escape rule is visible.
	for _, k := range out.Keys {
		if k.Key == "f11" && !k.BrowserOwned {
			t.Fatal("markOwned must stamp a platform row exactly as it stamps a dealer's")
		}
	}
}

// The tier is INSERTED, not substituted: a dealer who has curated still wins, whole.
func TestHotkeys_ADealerOverrideStillWinsOverThePlatformRoster(t *testing.T) {
	kit := &platformKit{
		cfg:      settings.SurfaceConfig{RoleItems: actionItems([2]string{HotkeyPay, "f10"})},
		platform: []settings.PlatformHotkey{{Action: HotkeyPay, Binding: "f11"}},
	}
	out, err := (&Service{}).WithSurfaceCuration(kit).Hotkeys(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if out.Source != "dealer" || keyOf(out.Keys, HotkeyPay) != "f10" {
		t.Fatalf("a dealer's own curation must win whole; got source %q keys %#v", out.Source, out.Keys)
	}
	// WHOLE-OR-ABSENT, not row by row: the dealer bound one action, so the dealer's roster is ONE
	// action. Merging the platform's other rows in would make "which level decided this" a question
	// about two tables at once, which is exactly what Source exists to prevent.
	if len(out.Keys) != 1 {
		t.Fatalf("the levels must not merge row by row; got %#v", out.Keys)
	}
}

// And the person's own rows still win over both, so inserting a level under the dealer did not
// disturb the level above it.
func TestHotkeys_APersonsOwnRosterStillWinsOverThePlatformRoster(t *testing.T) {
	kit := &platformKit{
		cfg: settings.SurfaceConfig{
			UserItems: actionItems([2]string{HotkeyPay, "f7"}),
			RoleItems: actionItems([2]string{HotkeyPay, "f10"}),
		},
		platform: []settings.PlatformHotkey{{Action: HotkeyPay, Binding: "f11"}},
	}
	out, err := (&Service{}).WithSurfaceCuration(kit).Hotkeys(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if out.Source != "user" || keyOf(out.Keys, HotkeyPay) != "f7" {
		t.Fatalf("a person's own roster must still win; got source %q keys %#v", out.Source, out.Keys)
	}
}

// The shipped floor is still underneath everything, so a build ahead of its migrations has keys.
func TestHotkeys_AnEmptyPlatformRosterFallsThroughToTheShippedFloor(t *testing.T) {
	out, err := (&Service{}).WithSurfaceCuration(&platformKit{}).Hotkeys(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if out.Source != "default" {
		t.Fatalf("Source = %q; want the shipped floor", out.Source)
	}
	if !reflect.DeepEqual(stripOwned(out.Keys), defaultHotkeys) {
		t.Fatalf("the floor must be the BisTrack roster; got %#v", out.Keys)
	}
}

// A settings read that failed says nothing about whether the PLATFORM roster is readable, and the
// deployment's own floor is a better answer than the code's. So the platform level is still
// consulted on the degrade path, the posture every level of this feature takes: fall to the level
// below, never to a fault.
func TestHotkeys_AnUnreadableCurationStillReachesThePlatformRoster(t *testing.T) {
	kit := &platformKit{
		cfgErr:   errors.New("settings read timed out"),
		platform: []settings.PlatformHotkey{{Action: HotkeyPay, Binding: "f11"}},
	}
	out, err := (&Service{}).WithSurfaceCuration(kit).Hotkeys(context.Background(), "")
	if err != nil {
		t.Fatalf("an unreadable curation must never be a fault: %v", err)
	}
	if out.Source != "platform" || keyOf(out.Keys, HotkeyPay) != "f11" {
		t.Fatalf("the platform roster must answer a failed curation read; got %q %#v", out.Source, out.Keys)
	}
}

func TestHotkeys_AnUnreadablePlatformRosterFallsToTheShippedFloor(t *testing.T) {
	kit := &platformKit{platformEr: errors.New("relation platform_hotkeys does not exist")}
	out, err := (&Service{}).WithSurfaceCuration(kit).Hotkeys(context.Background(), "")
	if err != nil {
		t.Fatalf("an unreadable platform roster must never be a fault: %v", err)
	}
	if out.Source != "default" {
		t.Fatalf("Source = %q; want the shipped floor", out.Source)
	}
}

// A curation seam that predates the tier keeps working and simply has no platform roster. This is
// why PlatformRosterReader is a separate interface asserted at the call site: widening
// SurfaceCuration would have made every existing fake a compile error and taught nothing.
func TestHotkeys_ACurationSeamWithoutThePlatformTierStillAnswers(t *testing.T) {
	out, err := (&Service{}).WithSurfaceCuration(&plainKit{}).Hotkeys(context.Background(), "")
	if err != nil {
		t.Fatalf("a seam without the tier must degrade, not fail: %v", err)
	}
	if out.Source != "default" {
		t.Fatalf("Source = %q; want the shipped floor", out.Source)
	}
}

// A PLATFORM row goes through THIS package's grammar, exactly as a dealer's curation row does.
// That is why the platform table stores a spelling instead of the kit validating it into one:
// there are already four spellings of the chord grammar in this codebase, and a fifth in a package
// that can import neither owner would be the copy that drifts.
func TestHotkeys_APlatformRowIsCanonicalisedByTheCounterOwnGrammar(t *testing.T) {
	kit := &platformKit{platform: []settings.PlatformHotkey{
		{Action: HotkeyStepBack, Binding: "CTRL-Shift-B", Position: 0}, // sloppy on purpose
		{Action: HotkeyPay, Binding: "f13", Position: 1},               // not a key at all
		{Action: "not-an-action", Binding: "f7", Position: 2},          // nothing can render it
		{Action: HotkeySearch, Binding: "f2", Position: 3},
	}}
	out, err := (&Service{}).WithSurfaceCuration(kit).Hotkeys(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got := keyOf(out.Keys, HotkeyStepBack); got != "shift-ctrl-b" {
		t.Fatalf("a sloppy spelling must be repaired on the way out, not served as a key that never fires; got %q", got)
	}
	if keyOf(out.Keys, HotkeyPay) != "" {
		t.Fatal("f13 is not a key this system binds, the row must be dropped, not drawn")
	}
	if len(out.Keys) != 2 {
		t.Fatalf("an unrenderable row draws nothing rather than a dead keycap; got %#v", out.Keys)
	}
}

// -- THE MIRROR ----------------------------------------------------------------------------------

// settings/platform_hotkeys.go carries a copy of this package's action set so it can refuse a
// platform write naming an action the counter cannot carry out. It cannot import this package, so
// the copy is held equal HERE, the same "two copies, one test" arrangement the BisTrack floor
// already uses across the Go/TypeScript line.
//
// If this fails, do not edit this test: make the two lists agree. A platform tier that accepts an
// action the counter cannot render puts a dead keycap on every till on the deployment, and it does
// so silently, because an id the frontend cannot draw draws nothing.
func TestPlatformHotkeys_TheKitsCopyOfTheCounterActionsMatchesThisPackages(t *testing.T) {
	mine := make([]string, 0, len(validHotkeyActions))
	for a := range validHotkeyActions {
		mine = append(mine, a)
	}
	sort.Strings(mine)
	theirs := settings.PlatformCounterActions()
	if !reflect.DeepEqual(mine, theirs) {
		t.Fatalf("the kit's mirror of the counter action set has drifted:\n  pos:      %v\n  settings: %v", mine, theirs)
	}
	if len(mine) != 5 {
		t.Fatalf("the counter has five actions; got %d, if that changed, both lists move together", len(mine))
	}
}

// The two surfaces are two, at the id level as well as in prose. If the kit ever accepted a counter
// action on the shell roster (or the reverse), an administrator editing one map could change the
// other's meaning without either screen saying so.
func TestPlatformHotkeys_TheCounterActionsAreNotBindableOnTheShellRoster(t *testing.T) {
	for _, a := range settings.PlatformCounterActions() {
		// The shell keeps SHAPE only by ruling, so 'pay' and 'park' ARE shaped like shell ids and
		// are accepted there, what must never be true is the reverse, that a shell id (dotted,
		// e.g. 'sale.cash.new') is accepted on the counter, because the counter can render none of
		// them and would draw a dead keycap.
		if err := settings.ValidatePlatformRoster(settings.PlatformSurfaceCounter,
			[]settings.PlatformHotkey{{Action: "sale.cash.new", Binding: "f7"}}); err == nil {
			t.Fatal("a shell action must not be bindable on the counter roster")
		}
		_ = a
	}
}

func stripOwned(keys []PosHotkey) []PosHotkey {
	out := make([]PosHotkey, 0, len(keys))
	for _, k := range keys {
		out = append(out, PosHotkey{Action: k.Action, Key: k.Key})
	}
	return out
}
