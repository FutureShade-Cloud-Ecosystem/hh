package settings

// settings_test.go, DIB-507 kit proofs: shape-only validation speaks 422 in words, the dealer
// write is admin-gated in the service, the workspace save honours the write_key replay contract
// (the 0166 posture), a stored user row wins whole-or-absent, and reset IS a delete.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// - fake store -

type fakeKitStore struct {
	profile   string
	roleItems []Item
	userItems []Item
	wsRow     WorkspaceRow
	hasWSRow  bool

	roleWrites  int
	userWrites  int
	wsWrites    int
	userDeletes int
	wsDeletes   int
	lastProfile string
	// The profile the READ was made against, and a per-profile row set so a test can tell one
	// profile's curation from another's.
	lastReadProfile    string
	roleReads          int
	roleItemsByProfile map[string][]Item
	lastRoleSet        []Item
	profileErr         error
	readWSErr          error
}

func (f *fakeKitStore) ProfileFor(context.Context, string) (string, error) {
	return f.profile, f.profileErr
}

// It CAPTURES the profile it was asked for. Ignoring the argument, which this fake did, makes a
// test asserting "?profile=driver reads driver rows" pass vacuously against a service that read the
// caller's rows and threw the request away, which is precisely the wart being closed.
func (f *fakeKitStore) ReadRoleItems(_ context.Context, _ string, profile string) ([]Item, error) {
	f.lastReadProfile = profile
	f.roleReads++
	if rows, ok := f.roleItemsByProfile[profile]; ok {
		return rows, nil
	}
	return f.roleItems, nil
}
func (f *fakeKitStore) ReplaceRoleItems(_ context.Context, _ string, profile string, items []Item, _ string) error {
	f.roleWrites++
	f.lastProfile = profile
	f.lastRoleSet = items
	return nil
}
func (f *fakeKitStore) ReadUserItems(context.Context, string, string) ([]Item, error) {
	return f.userItems, nil
}
func (f *fakeKitStore) ReplaceUserItems(_ context.Context, _, _ string, items []Item) error {
	f.userWrites++
	f.userItems = items
	return nil
}
func (f *fakeKitStore) DeleteUserItems(context.Context, string, string) error {
	f.userDeletes++
	f.userItems = nil
	return nil
}
func (f *fakeKitStore) ReadUserWorkspace(context.Context, string, string) (WorkspaceRow, bool, error) {
	return f.wsRow, f.hasWSRow, f.readWSErr
}
func (f *fakeKitStore) UpsertUserWorkspace(_ context.Context, row WorkspaceRow) error {
	f.wsWrites++
	f.wsRow = row
	f.hasWSRow = true
	return nil
}
func (f *fakeKitStore) DeleteUserWorkspace(context.Context, string, string) error {
	f.wsDeletes++
	f.hasWSRow = false
	return nil
}

var _ StoreIface = (*fakeKitStore)(nil)

func kitAdmin() identity.StaffContext {
	return identity.StaffContext{
		UserID: "u-admin", RoleCode: "GENERAL_MANAGER",
		Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	}
}

func kitFrontline() identity.StaffContext {
	return identity.StaffContext{
		UserID: "u-counter", RoleCode: "INSIDE_SALES",
		Tier: identity.TierFrontline, Scope: identity.ScopeBranch, Status: identity.StatusActive,
	}
}

func kitSvc(store *fakeKitStore, caller identity.StaffContext) *Service {
	return NewService(store).withCaller(func(context.Context) (identity.StaffContext, bool) {
		return caller, caller.UserID != ""
	})
}

func want422(t *testing.T, err error, frag string) {
	t.Helper()
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("want a *StatusError, got %v", err)
	}
	if se.Status != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d (%s)", se.Status, se.Msg)
	}
	if !strings.Contains(se.Msg, frag) {
		t.Fatalf("the refusal must carry words (%q); got %q", frag, se.Msg)
	}
}

// - validation speaks 422 in words -

func TestValidateItems_RefusesInWords(t *testing.T) {
	if err := ValidateItems(nil); err == nil {
		t.Fatal("an empty set must be refused (reset is a DELETE, not an empty PUT)")
	} else {
		want422(t, err, "DELETE")
	}
	want422(t, ValidateItems([]Item{{Kind: "tile", ID: "x", Position: 0}}), "panel, column, chip or action")
	want422(t, ValidateItems([]Item{{Kind: KindColumn, ID: "Bad Id!", Position: 0}}), "lowercase")
	want422(t, ValidateItems([]Item{{Kind: KindColumn, ID: "line.qty", Position: -1}}), "negative")
	want422(t, ValidateItems([]Item{
		{Kind: KindColumn, ID: "line.qty", Position: 0},
		{Kind: KindColumn, ID: "line.qty", Position: 1},
	}), "twice")
	if err := ValidateItems([]Item{{Kind: KindColumn, ID: "line.qty", Position: 0, Visible: true}}); err != nil {
		t.Fatalf("a well-shaped set must pass: %v", err)
	}
}

func TestValidateGeometry_RefusesInWords(t *testing.T) {
	if _, _, err := ValidateGeometry("quote", 19, "", SplitDefault); err == nil {
		t.Fatal("want a 422 for split 19")
	} else {
		want422(t, err, "between 20 and 80")
	}
	if _, _, err := ValidateGeometry("quote", 50, "SIDEWAYS", SplitDefault); err == nil {
		t.Fatal("want a 422 for a garbage arrangement")
	} else {
		want422(t, err, "PRIMARY_LEFT or PRIMARY_RIGHT")
	}
	// Omitted values normalize to the defaults, the column-default posture.
	s, a, err := ValidateGeometry("quote", 0, "", 62)
	if err != nil || s != 62 || a != ArrangementPrimaryLeft {
		t.Fatalf("omitted geometry must normalize to defaults; got %d/%s/%v", s, a, err)
	}
}

func TestValidateVisibility_RefusesInWords(t *testing.T) {
	if _, err := ValidateVisibility("quote", "EVERYONE"); err == nil {
		t.Fatal("want a 422 for an unknown visibility class")
	} else {
		want422(t, err, "MANAGER_UP, ALL_STAFF or HIDDEN")
	}
	if v, err := ValidateVisibility("quote", ""); err != nil || v != VisibilityManagerUp {
		t.Fatalf("omitted visibility must normalize to MANAGER_UP; got %q/%v", v, err)
	}
}

// - the dealer-curation write is admin-gated in the service -

func TestPutRoleItems_AdminGate(t *testing.T) {
	store := &fakeKitStore{}
	items := []Item{{Kind: KindColumn, ID: "line.qty", Position: 0, Visible: true}}

	err := kitSvc(store, kitFrontline()).PutRoleItems(context.Background(), "quotes.builder", "counter", items)
	var na *NotAdminError
	if !errors.As(err, &na) {
		t.Fatalf("a frontline caller must be refused with NotAdminError; got %v", err)
	}
	if store.roleWrites != 0 {
		t.Fatal("the refused write must not reach the store")
	}

	if err := kitSvc(store, kitAdmin()).PutRoleItems(context.Background(), "quotes.builder", "counter", items); err != nil {
		t.Fatalf("the admin write must land: %v", err)
	}
	if store.roleWrites != 1 || store.lastProfile != "counter" {
		t.Fatalf("want one captured write for counter; got %d/%q", store.roleWrites, store.lastProfile)
	}
}

// - the workspace write_key replay contract (0166) -

func TestSaveWorkspace_ReplaySameKey_NoSecondWrite(t *testing.T) {
	store := &fakeKitStore{}
	svc := kitSvc(store, kitFrontline())
	in := WorkspaceSaveInput{SplitPct: 40, Arrangement: ArrangementPrimaryRight, IdempotencyKey: "k-1"}

	first, err := svc.SaveWorkspace(context.Background(), "quotes.builder", in)
	if err != nil || first.Replayed {
		t.Fatalf("first save must write plainly: %+v %v", first, err)
	}
	// The retry: same key, DIFFERENT payload, the STORED row must answer, and nothing writes.
	replay, err := svc.SaveWorkspace(context.Background(), "quotes.builder",
		WorkspaceSaveInput{SplitPct: 70, Arrangement: ArrangementPrimaryLeft, IdempotencyKey: "k-1"})
	if err != nil {
		t.Fatalf("replay errored: %v", err)
	}
	if !replay.Replayed || replay.SplitPct != 40 || replay.Arrangement != ArrangementPrimaryRight {
		t.Fatalf("the replay must answer the STORED row: %+v", replay)
	}
	if store.wsWrites != 1 {
		t.Fatalf("a replayed key must not write again; writes=%d", store.wsWrites)
	}
}

func TestSaveWorkspace_SplitRejected_422(t *testing.T) {
	svc := kitSvc(&fakeKitStore{}, kitFrontline())
	_, err := svc.SaveWorkspace(context.Background(), "quotes.builder",
		WorkspaceSaveInput{SplitPct: 5, IdempotencyKey: "k"})
	want422(t, err, "between 20 and 80")
	_, err = svc.SaveWorkspace(context.Background(), "quotes.builder", WorkspaceSaveInput{SplitPct: 50})
	want422(t, err, "idempotency_key")
}

// - resolution: user row wins whole-or-absent; dealer defaults via the module hook -

func TestConfig_UserRowWinsAndDealerHookFeedsDefaults(t *testing.T) {
	store := &fakeKitStore{
		profile:   "manager",
		roleItems: []Item{{Kind: KindColumn, ID: "line.qty", Position: 0, Visible: true}},
	}
	svc := kitSvc(store, kitAdmin()).WithSurface("quotes.builder", SurfaceHooks{
		WorkspaceDefaults: func(context.Context, string) (int, string, error) {
			return 66, ArrangementPrimaryRight, nil
		},
	})

	// No user row: the module hook's dealer defaults answer, source=dealer.
	cfg, err := svc.Config(context.Background(), "quotes.builder", "")
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if cfg.Workspace.Source != "dealer" || cfg.Workspace.SplitPct != 66 || cfg.Workspace.Arrangement != ArrangementPrimaryRight {
		t.Fatalf("dealer defaults must come from the module hook: %+v", cfg.Workspace)
	}
	if cfg.Profile != "manager" || len(cfg.RoleItems) != 1 {
		t.Fatalf("profile + role items must resolve: %+v", cfg)
	}

	// A stored row wins WHOLE-OR-ABSENT, the dealer defaults are not consulted.
	store.hasWSRow = true
	store.wsRow = WorkspaceRow{SplitPct: 25, Arrangement: ArrangementPrimaryLeft, CostHidden: true}
	cfg, err = svc.Config(context.Background(), "quotes.builder", "")
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if cfg.Workspace.Source != "user" || cfg.Workspace.SplitPct != 25 || !cfg.Workspace.CostHidden {
		t.Fatalf("the stored row IS the workspace: %+v", cfg.Workspace)
	}
}

func TestConfig_ProfileFallsBackLeastPrivileged(t *testing.T) {
	store := &fakeKitStore{profile: ""} // no dashboard_role_profiles row for this role
	cfg, err := kitSvc(store, kitFrontline()).Config(context.Background(), "quotes.builder", "")
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if cfg.Profile != ProfileFallback {
		t.Fatalf("a missing mapping must resolve to %q, never a manager's view by accident; got %q",
			ProfileFallback, cfg.Profile)
	}
}

// - reset IS a delete -

func TestResets_AreDeletes(t *testing.T) {
	store := &fakeKitStore{hasWSRow: true, userItems: []Item{{Kind: KindColumn, ID: "line.qty"}}}
	svc := kitSvc(store, kitFrontline())
	if err := svc.ResetUserItems(context.Background(), "quotes.builder"); err != nil {
		t.Fatalf("reset items: %v", err)
	}
	if err := svc.ResetWorkspace(context.Background(), "quotes.builder"); err != nil {
		t.Fatalf("reset workspace: %v", err)
	}
	if store.userDeletes != 1 || store.wsDeletes != 1 {
		t.Fatalf("reset must be a DELETE (items=%d ws=%d)", store.userDeletes, store.wsDeletes)
	}
}

// - surface id shape -

func TestSurfaceID_ShapeOnly(t *testing.T) {
	want422(t, ValidateSurfaceID("Quotes Builder"), "lowercase")
	if err := ValidateSurfaceID("quotes.builder"); err != nil {
		t.Fatalf("a well-shaped surface id must pass: %v", err)
	}
}

// - item 3c (session A), the kit's config read can name the profile it is reading -

// TestConfig_AnAdminReadsTheProfileItIsEditing closes the wart the counter lane named.
//
// THE DEFECT. PutRoleItems takes a profile, an admin curates the YARD shape's columns by writing
// {profile: "yard", items: [...]}. Config took none: it resolved the CALLER's profile and read that.
// So an editor wrote one thing and displayed another, and the only way to see what you had just saved
// was to be the role you had saved it for.
func TestConfig_AnAdminReadsTheProfileItIsEditing(t *testing.T) {
	store := &fakeKitStore{
		profile: "admin",
		roleItemsByProfile: map[string][]Item{
			"admin": {{Kind: KindColumn, ID: "admin.only", Position: 0, Visible: true}},
			"yard":  {{Kind: KindColumn, ID: "yard.only", Position: 0, Visible: true}},
		},
	}
	svc := NewService(store).withCaller(func(context.Context) (identity.StaffContext, bool) {
		return identity.StaffContext{UserID: "u-1", RoleCode: "TECH_ADMIN", Tier: identity.TierLeadership,
			Scope: identity.ScopeGlobal, Status: identity.StatusActive}, true
	})

	// Without an option, nothing changes: the caller's own profile, exactly as before the widening.
	own, err := svc.Config(context.Background(), "quotes.builder", "")
	if err != nil {
		t.Fatalf("plain config: %v", err)
	}
	if store.lastReadProfile != "admin" {
		t.Fatalf("a config read with no option must read the caller's own profile, read %q", store.lastReadProfile)
	}
	if len(own.RoleItems) != 1 || own.RoleItems[0].ID != "admin.only" {
		t.Fatalf("wrong rows for the caller's own profile: %+v", own.RoleItems)
	}

	// WITH the option, the store is asked for the profile being edited.
	asked, err := svc.Config(context.Background(), "quotes.builder", "", ForProfile("yard"))
	if err != nil {
		t.Fatalf("config for another profile: %v", err)
	}
	if store.lastReadProfile != "yard" {
		t.Fatalf("the requested profile must reach the store, read %q", store.lastReadProfile)
	}
	if len(asked.RoleItems) != 1 || asked.RoleItems[0].ID != "yard.only" {
		t.Fatalf("an editor must be handed the rows it is editing: %+v", asked.RoleItems)
	}
	if asked.ItemsProfile != "yard" {
		t.Fatalf("the answer must say whose curation it is carrying, got %q", asked.ItemsProfile)
	}
}

// TestConfig_ProfileIsTheCALLERSAndNeverTheRequested is the security half, and it is the whole reason
// the requested profile got a field of its own.
//
// quote-workarea.ts gates its settings button on `config.profile === 'admin'`. If Config echoed the
// REQUESTED profile back in that field, any staff member could ask for `?profile=admin` and be handed
// the admin control by a screen that had no way to know better. The field keeps meaning "who am I".
func TestConfig_ProfileIsTheCALLERSAndNeverTheRequested(t *testing.T) {
	store := &fakeKitStore{profile: "admin"}
	svc := NewService(store).withCaller(func(context.Context) (identity.StaffContext, bool) {
		return identity.StaffContext{UserID: "u-1", RoleCode: "TECH_ADMIN", Tier: identity.TierLeadership,
			Scope: identity.ScopeGlobal, Status: identity.StatusActive}, true
	})
	cfg, err := svc.Config(context.Background(), "quotes.builder", "", ForProfile("driver"))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if cfg.Profile != "admin" {
		t.Fatalf(`Profile must stay the CALLER's resolved shape, got %q.
A screen gates its admin control on this field; echoing the requested profile back would let any
caller ask their way into it.`, cfg.Profile)
	}
	if cfg.ItemsProfile != "driver" {
		t.Fatalf("and the requested profile belongs in ItemsProfile, got %q", cfg.ItemsProfile)
	}
}

// TestConfig_ANonAdminCannotReadAnotherProfilesCuration. Reading another job shape's curation is the
// read half of the same admin act as writing it, so it is gated identically, and refused BEFORE the
// store is touched, which is what the read counter proves.
func TestConfig_ANonAdminCannotReadAnotherProfilesCuration(t *testing.T) {
	store := &fakeKitStore{profile: "counter"}
	svc := NewService(store).withCaller(func(context.Context) (identity.StaffContext, bool) {
		return identity.StaffContext{UserID: "u-2", RoleCode: "INSIDE_SALES", Tier: identity.TierFrontline,
			Scope: identity.ScopeGlobal, Status: identity.StatusActive}, true
	})
	_, err := svc.Config(context.Background(), "quotes.builder", "", ForProfile("manager"))
	if err == nil {
		t.Fatal("a frontline caller must not read the manager profile's curation")
	}
	var na *NotAdminError
	if !errors.As(err, &na) {
		t.Fatalf("the refusal must be the kit's 403 type: %#v", err)
	}
	if store.roleReads != 0 {
		t.Fatalf("the refusal must land BEFORE the store is read (reads=%d)", store.roleReads)
	}

	// Asking for YOUR OWN profile explicitly is not an admin act and is not refused, otherwise a
	// screen that always sends the profile it is showing would break for every non-admin.
	if _, err := svc.Config(context.Background(), "quotes.builder", "", ForProfile("counter")); err != nil {
		t.Fatalf("asking for one's own profile by name must be allowed: %v", err)
	}
}

// TestConfig_AnUnknownProfileIsRefusedInWords, the kit's 422-in-words posture, on the new parameter.
func TestConfig_AnUnknownProfileIsRefusedInWords(t *testing.T) {
	store := &fakeKitStore{profile: "admin"}
	svc := NewService(store).withCaller(func(context.Context) (identity.StaffContext, bool) {
		return identity.StaffContext{UserID: "u-1", RoleCode: "TECH_ADMIN", Tier: identity.TierLeadership,
			Scope: identity.ScopeGlobal, Status: identity.StatusActive}, true
	})
	_, err := svc.Config(context.Background(), "quotes.builder", "", ForProfile("warehouse"))
	if err == nil {
		t.Fatal("an unknown profile must be refused")
	}
	var se *StatusError
	if !errors.As(err, &se) || se.Status != http.StatusUnprocessableEntity {
		t.Fatalf("the refusal must be the kit's 422: %#v", err)
	}
	if !strings.Contains(err.Error(), "counter, yard, driver, manager or admin") {
		t.Fatalf("and it must name the profiles that exist: %v", err)
	}
}

// Every profile the curation gate accepts is a profile the counter roster reads: a manager.* shape
// left out would hide a dealer's key claim from the platform operator's collision check.
func TestCounterRosterReadsEveryValidProfile(t *testing.T) {
	in := map[string]bool{}
	for _, p := range counterRosterProfiles {
		in[p] = true
	}
	for p := range validProfiles {
		if !in[p] {
			t.Errorf("profile %q is curatable but not read by the counter roster", p)
		}
	}
}
