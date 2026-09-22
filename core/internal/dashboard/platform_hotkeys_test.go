package dashboard

// platform_hotkeys_test.go, D-111, the shell half: the retirement of sale.pay, and the MIRROR that
// holds the kit's shell-side validation equal to this package's own.
//
// settings/platform_hotkeys.go must refuse a platform write naming a malformed shell action id, and
// it cannot import this package to ask. So it carries a copy of validHotkeyAction's rule, and this
// is where the copy is held honest. A copy nobody checks is a copy waiting to drift, and drift here
// is silent in the worst direction: the platform tier would accept an id this build drops on read,
// so a platform operator would set a key, see it saved, and find it does nothing.

import (
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

// The probe set covers each rule the shape gate has: the shapes it must accept, and one example of
// every way an id can be malformed. Both sides are asked about the SAME strings, so a disagreement
// names itself.
var shellActionProbes = []string{
	"sale.cash.new",         // the ordinary case
	"assist.open",           // two segments
	"a",                     // one character is a legal id
	"under_score",           // underscores and dashes are in the alphabet
	"dash-ed",               //
	"digit9",                //
	"",                      // empty
	"Sale.Cash.New",         // uppercase
	"sale cash new",         // spaces
	"<script>",              // markup
	"sale/cash",             // a slash
	"sale.cash.néw",         // non-ASCII
	strings.Repeat("x", 65), // past the length bound
}

// THE MIRROR. If this fails, do not edit this test: make the two rules agree.
func TestPlatformHotkeys_TheKitsShellShapeGateMatchesThisPackages(t *testing.T) {
	for _, probe := range shellActionProbes {
		mine := validHotkeyAction(probe)
		// The kit's answer, asked through the only door it exposes: a one-row roster on the shell
		// surface, bound to a key this build definitely accepts, so the ONLY thing that can refuse
		// it is the action id.
		theirs := settings.ValidatePlatformRoster(settings.PlatformSurfaceShell,
			[]settings.PlatformHotkey{{Action: probe, Binding: "F7"}}) == nil
		if mine != theirs {
			t.Fatalf("the shell shape gate has drifted on %q: dashboard says %v, the settings kit says %v",
				probe, mine, theirs)
		}
	}
	// Vacuity guard: the probe set must actually exercise both answers, or the loop above proves
	// only that both sides agree about nothing.
	var accepted, refused int
	for _, probe := range shellActionProbes {
		if validHotkeyAction(probe) {
			accepted++
		} else {
			refused++
		}
	}
	if accepted < 4 || refused < 4 {
		t.Fatalf("the probe set must exercise both answers; accepted %d, refused %d", accepted, refused)
	}
}

// The retirement, asserted against the shipped default rather than against the migration, the
// migration's half is pinned in the frontend suite, which is where the seed-versus-registry drift
// gate already lives. Here the fact is simply that this build's floor no longer knows a pay key.
func TestPlatformHotkeys_TheShellFloorNoLongerKnowsAPayKey(t *testing.T) {
	for _, hb := range DefaultHotkeys {
		if hb.Action == "sale.pay" {
			t.Fatalf("sale.pay is still on the shell floor (as %s), pay belongs to the counter roster alone", hb.Binding)
		}
		if hb.Binding == "F8" {
			t.Fatalf("F8 is bound to %s, the retired ring key is left UNBOUND, not reused", hb.Action)
		}
	}
	// The floor is not simply empty: the five actions that are not pay survive the retirement.
	if len(DefaultHotkeys) != 5 {
		t.Fatalf("retiring one action must not take the rest of the map with it; got %d", len(DefaultHotkeys))
	}
	// And a stored row naming the retired action is still HANDLED, normalisation keeps it (the
	// server holds no registry, by the 0164 ruling) and the client's own registry drops it. That
	// division is deliberate and is what makes an orphaned dealer row a key that does nothing
	// rather than a broken payload.
	got := NormalizeHotkeys([]HotkeyBinding{{Binding: "F8", Action: "sale.pay"}})
	if len(got) != 1 || got[0].Binding != "F8" {
		t.Fatalf("the server keeps no action registry; an unknown id is the client's to drop: %#v", got)
	}
}
