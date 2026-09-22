package dashboard

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// -- DIB-433 · the pinned strip's rules, proved without a database ------------------------------

func pinKeys(pins []TabPin) []string {
	out := make([]string, 0, len(pins))
	for _, p := range pins {
		out = append(out, p.TabKey)
	}
	return out
}

func TestValidTabKeyAcceptsDeepLinkBodiesAndRefusesEverythingElse(t *testing.T) {
	ok := []string{"sales", "sales/pos", "finance/statements", "admin", "yard/yard", "messaging/messaging"}
	for _, k := range ok {
		if !ValidTabKey(k) {
			t.Errorf("ValidTabKey(%q) = false, want true, that is a real deep-link body", k)
		}
	}
	bad := []string{
		"", "/", "/pos", "sales/", "Sales", "sales/POS", "sales//pos", "sales/pos/extra",
		"__proto__", "sales/__proto__", "sales pos", "sales?pos", "#sales/pos",
		"9sales", "sales/9pos", strings.Repeat("a", 65),
	}
	for _, k := range bad {
		if ValidTabKey(k) {
			t.Errorf("ValidTabKey(%q) = true, want false", k)
		}
	}
	// SHAPE IS NOT EXISTENCE, AND THIS TEST SAYS SO OUT LOUD. 'constructor' is a perfectly
	// well-formed key and this function accepts it, because the registry of surfaces that exist
	// lives in app/src/dashboard-nav.ts and nowhere else. What stops it becoming a tab is the
	// shell's OWN-PROPERTY registry check (isNavUnitKey, a plain `in` would walk the prototype
	// chain and let it through), proved on the Node runner in dashboard-nav.test.mjs. Asserting
	// false here would only mean Go had grown a second, rotting copy of that registry.
	if !ValidTabKey("constructor") {
		t.Error("ValidTabKey(\"constructor\") = false, shape validation is not supposed to know the registry")
	}
}

func TestResolveTabsOverrideIsWholeOrAbsent(t *testing.T) {
	role := []TabPin{
		{TabKey: "sales/pos", Position: 0, Visible: true},
		{TabKey: "sales/catalogue", Position: 1, Visible: true},
		{TabKey: "sales/quotes", Position: 2, Visible: true},
	}
	t.Run("no user rows → the role default, source role", func(t *testing.T) {
		pins, src := ResolveTabs(role, nil)
		if src != SourceRole {
			t.Fatalf("source = %q, want %q", src, SourceRole)
		}
		if got, want := strings.Join(pinKeys(pins), ","), "sales/pos,sales/catalogue,sales/quotes"; got != want {
			t.Fatalf("pins = %s, want %s", got, want)
		}
	})
	t.Run("any user row wins outright, the role layer is not consulted", func(t *testing.T) {
		// One row. If the layers merged, the role's other two would still be here; the whole
		// point of whole-or-absent is that they are not.
		pins, src := ResolveTabs(role, []TabPin{{TabKey: "warranty/warranty", Position: 0, Visible: true}})
		if src != SourceUser {
			t.Fatalf("source = %q, want %q", src, SourceUser)
		}
		if got := pinKeys(pins); len(got) != 1 || got[0] != "warranty/warranty" {
			t.Fatalf("pins = %v, want exactly [warranty/warranty]", got)
		}
	})
	t.Run("an all-unpinned override is a real, empty strip, not a fallback to the role", func(t *testing.T) {
		// THE REASON `visible` EXISTS. A person who unpins everything stores their role's keys at
		// visible=false. If that resolved back to the role default they could never HAVE an empty
		// strip, and the unpin would silently undo itself on next login.
		off := []TabPin{
			{TabKey: "sales/pos", Position: 0, Visible: false},
			{TabKey: "sales/catalogue", Position: 1, Visible: false},
			{TabKey: "sales/quotes", Position: 2, Visible: false},
		}
		pins, src := ResolveTabs(role, off)
		if src != SourceUser {
			t.Fatalf("source = %q, want %q", src, SourceUser)
		}
		for _, p := range pins {
			if p.Visible {
				t.Fatalf("%s came back visible; the override said otherwise", p.TabKey)
			}
		}
	})
	t.Run("neither layer → an empty strip, source catalog", func(t *testing.T) {
		pins, src := ResolveTabs(nil, nil)
		if src != SourceCatalog {
			t.Fatalf("source = %q, want %q", src, SourceCatalog)
		}
		if len(pins) != 0 {
			t.Fatalf("pins = %v, want none, there is no surface catalog in Go to fall back to", pins)
		}
	})
}

func TestResolveTabsIsDeterministicAndRefusesJunk(t *testing.T) {
	rows := []TabPin{
		{TabKey: "sales/quotes", Position: 5, Visible: true},
		{TabKey: "sales/pos", Position: 5, Visible: true}, // colliding position, key breaks the tie
		{TabKey: "sales/pos", Position: 9, Visible: true}, // duplicate, first occurrence wins
		{TabKey: "__proto__", Position: 0, Visible: true}, // malformed, dropped
		{TabKey: "finance", Position: 1, Visible: true},
	}
	pins, _ := ResolveTabs(rows, nil)
	if got, want := strings.Join(pinKeys(pins), ","), "finance,sales/pos,sales/quotes"; got != want {
		t.Fatalf("pins = %s, want %s", got, want)
	}
	for i, p := range pins {
		if p.Position != i {
			t.Fatalf("position %d on pin %d, resolved positions must be dense and ordered", p.Position, i)
		}
	}
}

func TestNormalizeTabsCapsStorage(t *testing.T) {
	rows := make([]TabPin, 0, MaxStoredPins+10)
	for i := 0; i < MaxStoredPins+10; i++ {
		rows = append(rows, TabPin{TabKey: "u" + string(rune('a'+i%26)) + "/m" + string(rune('a'+i/26)), Position: i, Visible: true})
	}
	if got := len(NormalizeTabs(rows)); got != MaxStoredPins {
		t.Fatalf("stored %d pins, want the cap of %d", got, MaxStoredPins)
	}
}

func TestSaveMyTabsRefusesAnOverrideThatWouldReadAsNoOverride(t *testing.T) {
	f := newFakeStore()
	svc := NewService(f, nil)
	// Every key malformed → nothing survives normalisation. Stored, that would be zero rows, which
	// is exactly what "reset to role" leaves behind, so the person's next login would silently
	// hand them the role default back.
	_, err := svc.SaveMyTabs(context.Background(), "u1", []TabPin{{TabKey: "__proto__"}, {TabKey: "#sales"}})
	if !errors.Is(err, ErrEmptyTabs) {
		t.Fatalf("err = %v, want ErrEmptyTabs", err)
	}
	if len(f.userTabs["u1"]) != 0 {
		t.Fatalf("a refused save still wrote %d rows", len(f.userTabs["u1"]))
	}
}

func TestResetMyTabsRestoresTheRoleDefault(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	f.roleProfile["INSIDE_SALES"] = ProfileCounter
	f.profileTabs[ProfileCounter] = []TabPin{
		{TabKey: "sales/pos", Position: 0, Visible: true},
		{TabKey: "sales/catalogue", Position: 1, Visible: true},
	}
	svc := NewService(f, nil)

	if _, err := svc.SaveMyTabs(ctx, "u1", []TabPin{{TabKey: "warranty/warranty", Visible: true}}); err != nil {
		t.Fatalf("SaveMyTabs: %v", err)
	}
	strip, err := svc.TabsFor(ctx, ProfileCounter, "u1")
	if err != nil {
		t.Fatalf("TabsFor: %v", err)
	}
	if strip.Source != SourceUser || len(strip.Pins) != 1 {
		t.Fatalf("after save: source=%s pins=%v", strip.Source, pinKeys(strip.Pins))
	}

	n, err := svc.ResetMyTabs(ctx, "u1")
	if err != nil || n != 1 {
		t.Fatalf("ResetMyTabs = %d, %v", n, err)
	}
	strip, err = svc.TabsFor(ctx, ProfileCounter, "u1")
	if err != nil {
		t.Fatalf("TabsFor after reset: %v", err)
	}
	if strip.Source != SourceRole {
		t.Fatalf("source = %q after reset, want %q", strip.Source, SourceRole)
	}
	if got, want := strings.Join(pinKeys(strip.Pins), ","), "sales/pos,sales/catalogue"; got != want {
		t.Fatalf("pins = %s, want the role default %s", got, want)
	}
}

// An unmapped role gets the counter strip, not a blank one and not an admin's, the same
// least-privileged-useful fallback 0140 established for tiles.
func TestUnmappedRoleFallsBackToTheCounterStrip(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	f.profileTabs[ProfileCounter] = []TabPin{{TabKey: "sales/pos", Position: 0, Visible: true}}
	f.profileTabs[ProfileAdmin] = []TabPin{{TabKey: "admin", Position: 0, Visible: true}}
	svc := NewService(f, nil)

	profile := svc.ProfileFor(ctx, "SOME_ROLE_SHIPPED_LAST_TUESDAY")
	if profile != ProfileCounter {
		t.Fatalf("profile = %q, want %q", profile, ProfileCounter)
	}
	strip, err := svc.TabsFor(ctx, profile, "")
	if err != nil {
		t.Fatalf("TabsFor: %v", err)
	}
	if got := pinKeys(strip.Pins); len(got) != 1 || got[0] != "sales/pos" {
		t.Fatalf("pins = %v, want the counter strip", got)
	}
}

// The strip rides the SAME response as the tiles: one request, one paint, no rearranging strip.
func TestResolveCarriesThePinnedStrip(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	allVisible(ProfileCounter, f)
	f.profileTabs[ProfileCounter] = []TabPin{{TabKey: "sales/pos", Position: 0, Visible: true}}
	svc := NewService(f, nil)
	l, err := svc.Resolve(ctx, Scope{Profile: ProfileCounter, UserID: "u1"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if l.Tabs.Source != SourceRole || len(l.Tabs.Pins) != 1 || l.Tabs.Pins[0].TabKey != "sales/pos" {
		t.Fatalf("Layout.Tabs = %+v, want the counter role strip", l.Tabs)
	}
}

// A role default MAY be emptied by an admin, unlike a user override there is no lower layer for
// "empty" to be mistaken for.
func TestSaveProfileTabsAllowsAnEmptyRoleDefault(t *testing.T) {
	f := newFakeStore()
	svc := NewService(f, nil)
	pins, err := svc.SaveProfileTabs(context.Background(), ProfileDriver, nil, "admin-1")
	if err != nil {
		t.Fatalf("SaveProfileTabs: %v", err)
	}
	if len(pins) != 0 {
		t.Fatalf("pins = %v, want none", pinKeys(pins))
	}
	if _, err := svc.SaveProfileTabs(context.Background(), "not-a-profile", nil, "admin-1"); !errors.Is(err, ErrUnknownProfile) {
		t.Fatalf("err = %v, want ErrUnknownProfile", err)
	}
}
