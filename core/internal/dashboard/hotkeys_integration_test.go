//go:build integration

// Integration cover for DIB-496 (dealer hotkeys, 0164) and DIB-500 (payments-today default-on)
// against a real migrated Postgres. What only a database can prove, and is therefore proved here
// rather than against the fake:
//   - 0164 actually seeded the five D-095 placeholder bindings, spelled canonically, so the code
//     defaults and the migration cannot drift apart without a red test;
//   - the whole DIB-500 story in SQL: a manager-profile user whose stored override predates the
//     card sees it, hides it through the ordinary save path, and the hide PERSISTS across a
//     re-resolve, "an explicit removal wins" proved on dashboard_user_tiles rows, not on a map.
//
// FIXTURES RESTORE WHAT THEY TOUCH, per this package's integration convention: the user test uses
// a synthetic user (cascade-deleted), and nothing here writes dealer_hotkeys or profile rows.

package dashboard

import (
	"context"
	"reflect"
	"testing"
)

// The shipped seed, as read back through the repository: exactly the code-side defaults. This is
// the agreement quickaction.go documents for its own config, code and migration answer alike -
// held as an assertion instead of a comment.
//
// WHAT THIS PROVES CHANGED SHAPE UNDER D-111 WITHOUT CHANGING ITS POINT. It used to read five
// rows straight out of dealer_hotkeys, because that is where 0164 put the shipped defaults for
// want of anywhere better. 0191 moved them to where they belong, platform_hotkeys, surface
// 'app.shell', and emptied the dealer's table of rows no human ever chose, so the same call now
// exercises the tier resolution as well: no dealer curation on a fresh database, therefore the
// platform roster answers, and the platform roster is still the code defaults. The drift this
// guards against is unchanged and there is now one more seam it crosses.
func TestIntegration_DealerHotkeySeedMatchesTheCodeDefaults(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	rows, err := repo.Hotkeys(context.Background())
	if err != nil {
		t.Fatalf("Hotkeys: %v", err)
	}
	got := NormalizeHotkeys(rows)
	// ORDER BY binding: F1 F2 F3 F4 F9, the seed's own order, so DeepEqual is fair. (That comment
	// was stale from 0171 until D-111 retired F8 → sale.pay; it is true again.)
	if !reflect.DeepEqual(got, DefaultHotkeys) {
		t.Fatalf("the resolved shell map = %#v; want the code defaults %#v, the seed and hotkeys.go must not drift", got, DefaultHotkeys)
	}
}

// The DIB-500 lifecycle against real rows: default-present for a leadership override that predates
// the card, removable through the ordinary save, and the removal persists.
func TestIntegration_PaymentsTodayDefaultOnLifecycle(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo, nil)
	ctx := context.Background()
	userID := syntheticUser(t, pool)

	visible := func(why string, want bool) {
		t.Helper()
		layout, err := svc.Resolve(ctx, Scope{UserID: userID, RoleCode: "GENERAL_MANAGER"})
		if err != nil {
			t.Fatalf("%s: Resolve: %v", why, err)
		}
		for _, tile := range layout.Tiles {
			if tile.ID == TilePaymentsToday {
				if tile.Visible != want {
					t.Fatalf("%s: payments.today visible=%v, want %v", why, tile.Visible, want)
				}
				return
			}
		}
		t.Fatalf("%s: payments.today missing from the resolved layout", why)
	}

	// A personal override saved before the card shipped: names two tiles, is silent about the card.
	preCard := []LayoutRow{
		{TileID: TileSalesToday, Position: 0, Visible: true},
		{TileID: TileARAging, Position: 1, Visible: true},
	}
	if _, err := svc.SaveMyLayout(ctx, userID, preCard); err != nil {
		t.Fatalf("SaveMyLayout (pre-card): %v", err)
	}
	visible("an override that is SILENT about the card", true)

	// The person removes it, the editor submits the complete list, card named hidden.
	removed := append([]LayoutRow{}, preCard...)
	removed = append(removed, LayoutRow{TileID: TilePaymentsToday, Position: 2, Visible: false})
	if _, err := svc.SaveMyLayout(ctx, userID, removed); err != nil {
		t.Fatalf("SaveMyLayout (removed): %v", err)
	}
	visible("an explicit removal", false)
	visible("the removal on a SECOND resolve, it persists, not a one-request suppression", false)

	// Reset-to-role: the override goes, and the role layer (0157 names the card for 'manager')
	// takes over, the card is back, exactly as the owner expects "reset" to behave.
	if _, err := svc.ResetMyLayout(ctx, userID); err != nil {
		t.Fatalf("ResetMyLayout: %v", err)
	}
	visible("after reset-to-role", true)
}
