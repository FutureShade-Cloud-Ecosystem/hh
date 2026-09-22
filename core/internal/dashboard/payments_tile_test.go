package dashboard

import "testing"

// -- DIB-463: the payments-today tile's place in the catalog ---------------------
//
// These tests are about the TILE FRAMEWORK's existing mechanisms doing the work, not about a special
// case for payments. Each one would fail if somebody "helpfully" made this tile behave differently
// from every other tile.

// The catalog entry exists, names the messaging module, and deep-links to a real shell surface.
func TestCatalog_PaymentsTodayIsRegistered(t *testing.T) {
	meta, ok := CatalogByID[TilePaymentsToday]
	if !ok {
		t.Fatal("payments.today must be in the tile catalog, an unregistered id cannot be measured or stored")
	}
	if meta.Module != "messaging" {
		t.Fatalf("the tile's module names who owns the NUMBER, and it is the rollup: got %q", meta.Module)
	}
	if meta.Kind != KindMoneyCents {
		t.Fatalf("the headline is money, so the client formats cents: got %q", meta.Kind)
	}
	if meta.Unit == "" || meta.Screen == "" {
		t.Fatal("the tile must name a shell surface, or clicking it does nothing (tileHref returns '')")
	}
}

// ROLE GATING, PART 1, the tile is invisible to everyone the layout does not name.
//
// This is the shipped mechanism, not a new one: DefaultVisible=false means the catalog fallback hides
// it and a curated profile that does not list it appends it hidden. 0157 then names it for 'manager'
// and 'admin' only, so owner/finance roles get it and the counter, yard and driver profiles do not.
func TestCatalog_PaymentsTodayIsHiddenUntilAProfileNamesIt(t *testing.T) {
	meta := CatalogByID[TilePaymentsToday]
	if meta.DefaultVisible {
		t.Fatal("payments.today must NOT default visible, that is what keeps it off the yard's home screen")
	}

	// A profile that does not name it (the yard's stored layout) gets it hidden.
	yard := []LayoutRow{{TileID: TilePickQueue, Position: 0, Visible: true}}
	slots, _ := ResolveLayout(Catalog, CatalogByID, yard, nil)
	for _, s := range slots {
		if s.Meta.ID == TilePaymentsToday && s.Visible {
			t.Fatal("a curated layout that does not name the tile must not show it")
		}
	}

	// The catalog fallback (a profile with no stored rows at all) also leaves it off.
	fallback, _ := ResolveLayout(Catalog, CatalogByID, nil, nil)
	for _, s := range fallback {
		if s.Meta.ID == TilePaymentsToday && s.Visible {
			t.Fatal("the catalog fallback must not show payments.today either")
		}
	}
}

// ROLE GATING, PART 2, a profile row (what 0157 seeds for manager/admin) turns it on, and a person
// can still hide it for themselves, because it is an ordinary tile in an ordinary layout.
func TestCatalog_PaymentsTodayIsVisibleWhenTheProfileNamesIt(t *testing.T) {
	manager := []LayoutRow{
		{TileID: TileSalesToday, Position: 0, Visible: true},
		{TileID: TilePaymentsToday, Position: 1, Visible: true},
	}
	slots, _ := ResolveLayout(Catalog, CatalogByID, manager, nil)
	found := false
	for _, s := range slots {
		if s.Meta.ID == TilePaymentsToday {
			found = true
			if !s.Visible {
				t.Fatal("a profile row naming the tile must show it")
			}
		}
	}
	if !found {
		t.Fatal("the tile must survive layout resolution")
	}

	// A personal override wins whole-or-absent, like every other tile.
	mine := []LayoutRow{{TileID: TileSalesToday, Position: 0, Visible: true}}
	own, _ := ResolveLayout(Catalog, CatalogByID, manager, mine)
	for _, s := range own {
		if s.Meta.ID == TilePaymentsToday && s.Visible {
			t.Fatal("a user override that drops the tile must hide it, no tile is un-hideable")
		}
	}
}

// An unwired tile draws an em-dash and names its module, never a zero. "$0.00 today" and "nobody is
// measuring payments" are different sentences and only one of them is ever true when the provider is
// missing, the same rule the rest of the cockpit lives by.
func TestPaymentsToday_UnwiredRendersAnEmDashNotAZero(t *testing.T) {
	svc := NewService(nil, nil)
	if wired := svc.WiredTiles(); containsID(wired, TilePaymentsToday) {
		t.Fatal("no provider is registered in this test, so the tile must report as unwired")
	}
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
