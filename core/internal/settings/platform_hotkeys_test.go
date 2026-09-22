package settings

// platform_hotkeys_test.go, D-111: the platform roster's rules, without a database.
//
// The validators, the two refusal types, and the gate that separates the platform operator from a
// dealer's administrator. The SQL half is proved against a real database in
// platform_hotkeys_integration_test.go; the two grammars this file deliberately does NOT own are
// cross-checked by their owning packages (pos/platform_hotkeys_mirror_test.go and
// dashboard/platform_hotkeys_mirror_test.go).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// platformStore is a store that answers curation like the package fake AND carries a platform
// roster. It exists because PlatformHotkeyStore is asserted at the call site rather than folded
// into StoreIface, see the interface's own comment for why, so a test needs a store that has
// both halves to exercise the happy path, and the plain fake to exercise the "this deployment has
// no platform roster" path.
type platformStore struct {
	fakeKitStore
	rosters map[string][]PlatformHotkey
	readErr error
	wrote   map[string][]PlatformHotkey
	wroteBy string
}

func newPlatformStore() *platformStore {
	return &platformStore{
		rosters: map[string][]PlatformHotkey{},
		wrote:   map[string][]PlatformHotkey{},
	}
}

var _ StoreIface = (*platformStore)(nil)
var _ PlatformHotkeyStore = (*platformStore)(nil)

// marshalForGate serialises an answer the way the handler does, so the assertions below are made
// against BYTES rather than against struct fields, what ships to a browser is bytes, and a field
// that is gated in Go and marshalled anyway is exactly the defect this class of test exists for.
func marshalForGate(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func (s *platformStore) ReadPlatformHotkeys(_ context.Context, surface string) ([]PlatformHotkey, error) {
	if s.readErr != nil {
		return nil, s.readErr
	}
	return append([]PlatformHotkey(nil), s.rosters[surface]...), nil
}

func (s *platformStore) ReplacePlatformHotkeys(_ context.Context, surface string, keys []PlatformHotkey, updatedBy string) error {
	s.rosters[surface] = append([]PlatformHotkey(nil), keys...)
	s.wrote[surface] = append([]PlatformHotkey(nil), keys...)
	s.wroteBy = updatedBy
	return nil
}

func platformSvc(store StoreIface, caller identity.StaffContext) *Service {
	return NewService(store).withCaller(func(context.Context) (identity.StaffContext, bool) {
		return caller, true
	})
}

func superAdmin() identity.StaffContext {
	return identity.StaffContext{
		UserID: "d7000000-0000-4000-9000-000000000010", RoleCode: identity.RoleSuperAdmin,
		Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: "ACTIVE",
	}
}

func dealerGM() identity.StaffContext {
	return identity.StaffContext{
		UserID: "u-gm", RoleCode: "GENERAL_MANAGER",
		Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: "ACTIVE",
	}
}

func counterClerk() identity.StaffContext {
	return identity.StaffContext{
		UserID: "u-clerk", RoleCode: "INSIDE_SALES",
		Tier: identity.TierFrontline, Scope: identity.ScopeGlobal, Status: "ACTIVE",
	}
}

// THE GATE THAT IS THE WHOLE POINT OF THE TIER. A dealer's General Manager is unambiguously an
// administrator, IsAdmin() is true, they may curate every screen in the product, and is
// unambiguously NOT the platform operator. If IsAdmin() gated this write, the level whose entire
// purpose is to sit ABOVE the dealer would be editable by the dealer, and the tier would be
// decoration.
func TestPlatformHotkeys_ADealersAdministratorMayNotChangeThePlatformRoster(t *testing.T) {
	store := newPlatformStore()
	gm := dealerGM()
	if !gm.IsAdmin() {
		t.Fatal("the premise of this test is that a GM IS an administrator; fix the fixture")
	}
	err := platformSvc(store, gm).PutPlatformHotkeys(context.Background(), PlatformSurfaceCounter,
		[]PlatformHotkey{{Action: "pay", Binding: "f12"}})
	var refusal *NotPlatformAdminError
	if !errors.As(err, &refusal) {
		t.Fatalf("a dealer's GM must be refused with NotPlatformAdminError; got %v", err)
	}
	if len(store.wrote) != 0 {
		t.Fatalf("the refusal must precede the write; store recorded %#v", store.wrote)
	}
	// The refusal names the caller's role and the way forward, so it is answerable without a
	// support call, and it does NOT say "you are not an administrator", which would be false.
	msg := refusal.Error()
	if !strings.Contains(msg, "GENERAL_MANAGER") {
		t.Fatalf("the refusal must name the role it refused: %q", msg)
	}
	if !strings.Contains(msg, "Counter settings") {
		t.Fatalf("the refusal must name the way forward, a dealer's own keys: %q", msg)
	}
	if strings.Contains(msg, "limited to administrators") {
		t.Fatalf("this is not the not-an-administrator refusal; a GM IS one: %q", msg)
	}
}

func TestPlatformHotkeys_TheRefusalIsADistinctTypeFromTheAdminRefusal(t *testing.T) {
	// Two gates, two sentences. Collapsing them into one arm would have to pick one of the two
	// sentences and tell somebody the wrong thing about why they were refused.
	var na *NotAdminError
	if errors.As(error(&NotPlatformAdminError{RoleCode: "GENERAL_MANAGER"}), &na) {
		t.Fatal("the platform refusal must not satisfy errors.As for NotAdminError")
	}
	var np *NotPlatformAdminError
	if errors.As(error(&NotAdminError{RoleCode: "INSIDE_SALES"}), &np) {
		t.Fatal("the admin refusal must not satisfy errors.As for NotPlatformAdminError")
	}
}

func TestPlatformHotkeys_ThePlatformOperatorWritesAndTheWriteIsAttributed(t *testing.T) {
	store := newPlatformStore()
	caller := superAdmin()
	err := platformSvc(store, caller).PutPlatformHotkeys(context.Background(), PlatformSurfaceCounter,
		[]PlatformHotkey{{Action: "pay", Binding: "f12"}, {Action: "step-next", Binding: "f4"}})
	if err != nil {
		t.Fatalf("the platform operator must be allowed: %v", err)
	}
	got := store.wrote[PlatformSurfaceCounter]
	if len(got) != 2 {
		t.Fatalf("want 2 rows written; got %#v", got)
	}
	// Positions come from the SUBMITTED ORDER, not from the body's own position field: the screen
	// sends a list, and a list's order is what a person arranged. Trusting the field would let two
	// rows share a position and make the legend's order arbitrary.
	if got[0].Action != "pay" || got[0].Position != 0 || got[1].Position != 1 {
		t.Fatalf("positions must be renumbered from the submitted order; got %#v", got)
	}
	if store.wroteBy != caller.UserID {
		t.Fatalf("the write must be attributed to its author; got %q", store.wroteBy)
	}
}

// A roster where two actions claim one key resolves by row order and drops the loser SILENTLY, so
// an administrator who found out afterwards would find out from a counter person telling them a key
// stopped working. It is refused here, in words, before anything is stored.
func TestPlatformHotkeys_TwoActionsMayNotClaimOneKey(t *testing.T) {
	store := newPlatformStore()
	err := platformSvc(store, superAdmin()).PutPlatformHotkeys(context.Background(), PlatformSurfaceCounter,
		[]PlatformHotkey{{Action: "pay", Binding: "f4"}, {Action: "step-next", Binding: "f4"}})
	var se *StatusError
	if !errors.As(err, &se) || se.Status != http.StatusUnprocessableEntity {
		t.Fatalf("a duplicate binding must be a 422 in words; got %v", err)
	}
	if !strings.Contains(se.Msg, "f4") {
		t.Fatalf("the refusal must name the key: %q", se.Msg)
	}
	if !strings.Contains(se.Msg, "pick another key") {
		t.Fatalf("the refusal must name the way forward: %q", se.Msg)
	}
	// It names the KEY and not the action, deliberately: the human label lives in the frontend
	// registry, and a server refusal that said "already runs step-next" would paint a raw enum on
	// a screen. The configurator refuses the same clash at the keypress, where it has the label.
	if strings.Contains(se.Msg, "step-next") {
		t.Fatalf("the server refusal must not name a stored action id: %q", se.Msg)
	}
	if len(store.wrote) != 0 {
		t.Fatal("nothing may be stored when the roster is refused")
	}
}

func TestPlatformHotkeys_OneActionTakesOneKey(t *testing.T) {
	err := platformSvc(newPlatformStore(), superAdmin()).PutPlatformHotkeys(context.Background(), PlatformSurfaceCounter,
		[]PlatformHotkey{{Action: "pay", Binding: "f12"}, {Action: "pay", Binding: "f11"}})
	var se *StatusError
	if !errors.As(err, &se) || se.Status != http.StatusUnprocessableEntity {
		t.Fatalf("one action bound twice must be a 422 in words; got %v", err)
	}
}

// THE TWO SURFACES KEEP THEIR OWNERS' POSTURES, and this is where that is pinned. pos keeps a
// closed action set because a counter action it cannot render draws a dead keycap on a legend
// pinned across the foot of every till; the shell keeps SHAPE only because 0164 ruled that the
// client owns what an action IS and the server may not keep a copy of that list to go stale.
func TestPlatformHotkeys_TheCounterHasAClosedActionSetAndTheShellHasShapeOnly(t *testing.T) {
	svc := platformSvc(newPlatformStore(), superAdmin())
	ctx := context.Background()

	err := svc.PutPlatformHotkeys(ctx, PlatformSurfaceCounter, []PlatformHotkey{{Action: "not-an-action", Binding: "f7"}})
	if err == nil {
		t.Fatal("the counter must refuse an action it cannot carry out, a dead keycap on every till")
	}
	// The same id on the shell is accepted, because the shell's registry is the client's and this
	// server does not hold a copy of it. A row naming an action this build does not know does
	// nothing, silently, which is already what a shell hotkey into a forbidden screen does.
	if err := svc.PutPlatformHotkeys(ctx, PlatformSurfaceShell, []PlatformHotkey{{Action: "not.an.action", Binding: "F7"}}); err != nil {
		t.Fatalf("the shell keeps SHAPE only (the 0164 ruling); got %v", err)
	}
	// Shape is still a real gate on the shell: markup and shouting are not action ids.
	if err := svc.PutPlatformHotkeys(ctx, PlatformSurfaceShell, []PlatformHotkey{{Action: "<script>", Binding: "F7"}}); err == nil {
		t.Fatal("the shell must still refuse an id that is not a lowercase dotted word")
	}
	if err := svc.PutPlatformHotkeys(ctx, PlatformSurfaceShell, []PlatformHotkey{{Action: "Sale.Cash.New", Binding: "F7"}}); err == nil {
		t.Fatal("the shell must still refuse an id that is not lowercase")
	}
}

func TestPlatformHotkeys_AnUnknownSurfaceIsRefusedInWords(t *testing.T) {
	err := platformSvc(newPlatformStore(), superAdmin()).PutPlatformHotkeys(context.Background(), "quotes.builder",
		[]PlatformHotkey{{Action: "pay", Binding: "f12"}})
	var se *StatusError
	if !errors.As(err, &se) || se.Status != http.StatusUnprocessableEntity {
		t.Fatalf("an unknown surface must be a 422 in words; got %v", err)
	}
	if strings.Contains(se.Msg, "quotes.builder") {
		t.Fatalf("the refusal must not echo an arbitrary caller string back: %q", se.Msg)
	}
}

// An empty roster is refused rather than stored, because storing it would leave every terminal on
// the deployment inheriting a keyboard that does nothing, and the level below (the shipped floor)
// would never be reached, since a whole-or-absent tier that is PRESENT and empty is still present.
func TestPlatformHotkeys_AnEmptyRosterIsRefusedRatherThanShipped(t *testing.T) {
	err := platformSvc(newPlatformStore(), superAdmin()).PutPlatformHotkeys(context.Background(), PlatformSurfaceCounter, nil)
	var se *StatusError
	if !errors.As(err, &se) || se.Status != http.StatusUnprocessableEntity {
		t.Fatalf("an empty roster must be a 422 in words; got %v", err)
	}
	if !strings.Contains(se.Msg, "at least one key") {
		t.Fatalf("the refusal must say what to do: %q", se.Msg)
	}
}

func TestPlatformHotkeys_TheLegendIsBoundedSoItCannotBecomeASecondNavBar(t *testing.T) {
	keys := make([]PlatformHotkey, 0, maxPlatformKeys+1)
	for i := 0; i <= maxPlatformKeys; i++ {
		keys = append(keys, PlatformHotkey{Action: "a" + string(rune('a'+i)), Binding: "F" + string(rune('1'+i))})
	}
	if err := ValidatePlatformRoster(PlatformSurfaceShell, keys); err == nil {
		t.Fatalf("a roster of %d must be refused, past that the legend is a second navigation bar", len(keys))
	}
}

// - the read -

// CAN_EDIT IS THE SERVER'S ANSWER TO "MAY I", which is why the browser needs no authority logic of
// its own. The READ is staff-wide on purpose: a dealer's administrator seeing the platform floor is
// how they understand what their own override is overriding, and hiding it would make Counter
// settings' "Reset to the platform roster" a button whose destination is invisible.
func TestPlatformHotkeys_EveryoneMayReadAndOnlyThePlatformOperatorMayEdit(t *testing.T) {
	store := newPlatformStore()
	store.rosters[PlatformSurfaceCounter] = []PlatformHotkey{{Action: "pay", Binding: "f12", Position: 0}}
	ctx := context.Background()

	for _, tc := range []struct {
		name    string
		caller  identity.StaffContext
		canEdit bool
	}{
		{"the platform operator", superAdmin(), true},
		{"a dealer's general manager", dealerGM(), false},
		{"a counter clerk", counterClerk(), false},
	} {
		view, err := platformSvc(store, tc.caller).PlatformHotkeys(ctx)
		if err != nil {
			t.Fatalf("%s must be able to READ the platform roster: %v", tc.name, err)
		}
		if view.CanEdit != tc.canEdit {
			t.Fatalf("%s: CanEdit = %v, want %v", tc.name, view.CanEdit, tc.canEdit)
		}
		if len(view.Rosters) != len(PlatformSurfaces) {
			t.Fatalf("%s: every surface must be present so the screen never half-renders; got %d", tc.name, len(view.Rosters))
		}
		if len(view.Rosters[1].Keys) != 1 || view.Rosters[1].Keys[0].Binding != "f12" {
			t.Fatalf("%s: the counter roster must read back whole; got %#v", tc.name, view.Rosters[1])
		}
		// An unset surface reads as an EMPTY LIST, never null: the client is spared distinguishing
		// [] from null, the same choice the quick-action handler records.
		if view.Rosters[0].Keys == nil {
			t.Fatalf("%s: an unset roster must be an empty array on the wire, not null", tc.name)
		}
	}
}

// A settings read that failed says nothing about whether the keys are right, so it answers EMPTY
// and lets the caller fall to the level below. A till whose keys stopped working because a read
// timed out is a till nobody trusts again.
func TestPlatformHotkeys_AnUnreadableRosterIsEmptyAndNeverAFault(t *testing.T) {
	store := newPlatformStore()
	store.readErr = errors.New("relation platform_hotkeys does not exist")
	view, err := platformSvc(store, superAdmin()).PlatformHotkeys(context.Background())
	if err != nil {
		t.Fatalf("an unreadable roster must not fail the screen: %v", err)
	}
	for _, r := range view.Rosters {
		if len(r.Keys) != 0 {
			t.Fatalf("%s must read empty when the store cannot answer; got %#v", r.Surface, r.Keys)
		}
	}
}

// A store that predates this tier keeps working and simply has no platform roster, which resolves
// to the shipped floor, exactly as an unreadable one does. This is why PlatformHotkeyStore is
// asserted at the call site instead of being folded into StoreIface.
func TestPlatformHotkeys_AStoreWithoutTheTierDegradesRatherThanPanics(t *testing.T) {
	svc := platformSvc(&fakeKitStore{}, superAdmin())
	view, err := svc.PlatformHotkeys(context.Background())
	if err != nil {
		t.Fatalf("a store without the platform tier must degrade, not fail: %v", err)
	}
	for _, r := range view.Rosters {
		if len(r.Keys) != 0 {
			t.Fatalf("%s must be empty on a store with no platform tier", r.Surface)
		}
	}
	// And a WRITE against such a store is refused in words rather than panicking on a nil seam.
	if err := svc.PutPlatformHotkeys(context.Background(), PlatformSurfaceCounter,
		[]PlatformHotkey{{Action: "pay", Binding: "f12"}}); err == nil {
		t.Fatal("a write with no platform store must be refused, not silently dropped")
	}
}

// - the roster carries no authority and no money -

// The same gate the counter's own roster carries. A bound key grants nothing: pressing it does
// exactly what pressing the button does, and a caller who may not ring a sale still may not ring
// one with a key. So the payload must carry no cost, no price and no authority field, if one ever
// appeared here it would be a permission travelling on a presentation payload.
func TestPlatformHotkeys_TheRosterCarriesNoPermissionAndNoMoney(t *testing.T) {
	store := newPlatformStore()
	store.rosters[PlatformSurfaceCounter] = []PlatformHotkey{{Action: "pay", Binding: "f12"}}
	view, err := platformSvc(store, superAdmin()).PlatformHotkeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	blob := marshalForGate(t, view)
	for _, banned := range []string{"cost", "margin", "price", "cents", "role_code", "tier", "scope", "permission"} {
		if strings.Contains(strings.ToLower(blob), banned) {
			t.Fatalf("the platform roster payload must not carry %q, the gate is at the marshal layer: %s", banned, blob)
		}
	}
	// The control, so the absences above are not vacuous: the answer really does carry the roster
	// and the caller's can_edit.
	if !strings.Contains(blob, `"binding":"f12"`) || !strings.Contains(blob, `"can_edit":true`) {
		t.Fatalf("the payload must still carry the roster and the edit answer: %s", blob)
	}
}
