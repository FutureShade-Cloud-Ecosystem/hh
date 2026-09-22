package dashboard

import (
	"context"
	"errors"
	"testing"
)

// quickaction_test.go, DIB-434. The gates, named.
//
// Three of these tests are the ones the ruling asks to be able to point at: the yard shape does not
// see the button, an admin's global OFF hides it everywhere, and the counter shape sees it by
// default with nothing configured at all. They are written against the resolver rather than the
// HTTP layer because the resolver is what BOTH surfaces call, the client is handed a resolved
// list, so there is no second place the gate could be applied differently.

type fakeQuickStore struct {
	cfgs map[string]ActionConfig
	err  error
	// saved records the last SaveConfig call so a test can assert what would hit the table.
	saved *ActionConfig
	by    string
}

func (f *fakeQuickStore) Configs(context.Context) (map[string]ActionConfig, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.cfgs, nil
}

func (f *fakeQuickStore) SaveConfig(_ context.Context, cfg ActionConfig, by string) error {
	if f.err != nil {
		return f.err
	}
	c := cfg
	f.saved, f.by = &c, by
	if f.cfgs == nil {
		f.cfgs = map[string]ActionConfig{}
	}
	f.cfgs[cfg.ActionID] = cfg
	return nil
}

type fixedProfile string

func (p fixedProfile) ProfileFor(context.Context, string) string { return string(p) }

func meta(t *testing.T) ActionMeta {
	t.Helper()
	m, ok := ActionByID[ActionNewSale]
	if !ok {
		t.Fatalf("the registry lost %q, action ids are the primary key of 0151 and must not be renamed", ActionNewSale)
	}
	return m
}

// -- the three named gates ----------------------------------------------------------------------

func TestQuickAction_HiddenForYardRole(t *testing.T) {
	// Nothing configured: the shipped default alone must keep "+ Sale" off a picker's screen.
	got := ResolveVisible(ActionCatalog, nil, ProfileYard)
	if len(got) != 0 {
		t.Fatalf("the yard job shape must not see a till action; got %d action(s): %+v", len(got), got)
	}
	// And a driver, for the same reason, on the same default.
	if got := ResolveVisible(ActionCatalog, nil, ProfileDriver); len(got) != 0 {
		t.Fatalf("the driver job shape must not see a till action; got %d action(s)", len(got))
	}
}

func TestQuickAction_HiddenWhenAdminDisabled(t *testing.T) {
	// The per-profile row still says the counter sees it. The GLOBAL switch is off. Off wins -
	// "turn it off for the building" must not be quietly narrowed to "off except where a row says
	// otherwise", which is the exact bug an OR of the two gates would ship.
	cfgs := map[string]ActionConfig{
		ActionNewSale: {
			ActionID: ActionNewSale,
			Enabled:  false,
			Profiles: map[string]bool{ProfileCounter: true, ProfileManager: true},
		},
	}
	for _, p := range Profiles {
		if got := ResolveVisible(ActionCatalog, cfgs, p); len(got) != 0 {
			t.Fatalf("admin-disabled action still rendered for profile %q: %+v", p, got)
		}
	}
}

func TestQuickAction_VisibleByDefaultForCounter(t *testing.T) {
	// The default the ticket asks for: a counter clerk with an untouched configuration sees it.
	got := ResolveVisible(ActionCatalog, nil, ProfileCounter)
	if len(got) != 1 || got[0].ID != ActionNewSale {
		t.Fatalf("the counter job shape must see %q by default; got %+v", ActionNewSale, got)
	}
	if got[0].Label != "+ Sale" {
		t.Errorf("the button's words are part of the ruling: want %q, got %q", "+ Sale", got[0].Label)
	}
	// The destination is expressed in the shell's own keys so the action resolves to the frozen
	// #sales/pos deep link rather than a second router.
	if got[0].Unit != "sales" || got[0].Screen != "pos" {
		t.Errorf("destination must be the shell's own unit/screen keys: got %q/%q", got[0].Unit, got[0].Screen)
	}
	if got[0].Intent != IntentPosNewTicket {
		t.Errorf("intent must be %q, got %q", IntentPosNewTicket, got[0].Intent)
	}
	// A manager sells too.
	if got := ResolveVisible(ActionCatalog, nil, ProfileManager); len(got) != 1 {
		t.Fatalf("the manager job shape must see it by default; got %d", len(got))
	}
}

// -- the rest of the resolver's contract --------------------------------------------------------

func TestQuickAction_PerProfileOverrideNarrows(t *testing.T) {
	// The action stays globally on; the counter is switched OFF for the counter shape only.
	cfgs := map[string]ActionConfig{
		ActionNewSale: {ActionID: ActionNewSale, Enabled: true, Profiles: map[string]bool{ProfileCounter: false}},
	}
	if got := ResolveVisible(ActionCatalog, cfgs, ProfileCounter); len(got) != 0 {
		t.Fatalf("per-profile false must hide it for that shape; got %+v", got)
	}
	if got := ResolveVisible(ActionCatalog, cfgs, ProfileManager); len(got) != 1 {
		t.Fatalf("a counter-only override must not touch the manager shape; got %d", len(got))
	}
}

func TestQuickAction_PerProfileOverrideCanReveal(t *testing.T) {
	// The other direction: a branch that wants its yard leads ringing sales can switch the yard
	// shape ON without a release. That is the whole point of config-as-data.
	cfgs := map[string]ActionConfig{
		ActionNewSale: {ActionID: ActionNewSale, Enabled: true, Profiles: map[string]bool{ProfileYard: true}},
	}
	if got := ResolveVisible(ActionCatalog, cfgs, ProfileYard); len(got) != 1 {
		t.Fatalf("an explicit per-profile true must reveal it; got %d", len(got))
	}
}

func TestQuickAction_UnconfiguredProfileFallsBackToCatalogDefault(t *testing.T) {
	// A stored config that names only ONE profile must leave the others on the catalog default,
	// not on false. Absent and false are different facts.
	cfgs := map[string]ActionConfig{
		ActionNewSale: {ActionID: ActionNewSale, Enabled: true, Profiles: map[string]bool{ProfileYard: true}},
	}
	if got := ResolveVisible(ActionCatalog, cfgs, ProfileCounter); len(got) != 1 {
		t.Fatalf("an unmentioned profile must keep its catalog default (counter=true); got %d", len(got))
	}
	if got := ResolveVisible(ActionCatalog, cfgs, ProfileDriver); len(got) != 0 {
		t.Fatalf("an unmentioned profile must keep its catalog default (driver=false); got %d", len(got))
	}
}

func TestQuickAction_EffectiveForFillsEveryProfile(t *testing.T) {
	eff := EffectiveFor(meta(t), nil)
	if eff.Stored {
		t.Error("nothing stored → Stored must be false so the admin surface can say \"default\"")
	}
	for _, p := range Profiles {
		if _, ok := eff.Profiles[p]; !ok {
			t.Errorf("profile %q missing from the admin view, every shape needs a resolved answer, not a blank cell", p)
		}
	}
	if !eff.Profiles[ProfileCounter] || eff.Profiles[ProfileYard] {
		t.Errorf("resolved defaults wrong: counter=%v yard=%v", eff.Profiles[ProfileCounter], eff.Profiles[ProfileYard])
	}
}

func TestQuickAction_NormalizeProfilesDropsUnknownShapes(t *testing.T) {
	got := NormalizeProfiles(map[string]bool{ProfileCounter: true, "warehouse": true, "": true})
	if len(got) != 1 || !got[ProfileCounter] {
		t.Fatalf("a submitted matrix must be narrowed to the closed job-shape set; got %+v", got)
	}
}

// -- the service + its degradation --------------------------------------------------------------

func TestQuickActionService_DegradesToDefaultsWhenStoreFails(t *testing.T) {
	// A config table that cannot be read must give the counter clerk the button they had
	// yesterday, not a silently missing one. Hiding on error would look exactly like an admin
	// having turned it off, and nobody would know which had happened.
	svc := NewQuickActionService(&fakeQuickStore{err: errors.New("boom")}, fixedProfile(ProfileCounter), nil)
	profile, actions := svc.VisibleFor(context.Background(), "INSIDE_SALES")
	if profile != ProfileCounter {
		t.Fatalf("profile: want counter, got %q", profile)
	}
	if len(actions) != 1 {
		t.Fatalf("a store failure must degrade to the shipped default, not to hidden; got %d", len(actions))
	}
}

func TestQuickActionService_SaveRejectsUnknownAction(t *testing.T) {
	store := &fakeQuickStore{}
	svc := NewQuickActionService(store, fixedProfile(ProfileAdmin), nil)
	if _, err := svc.SaveConfig(context.Background(), "sale.nope", true, nil, "u1"); !errors.Is(err, ErrUnknownAction) {
		t.Fatalf("want ErrUnknownAction, got %v", err)
	}
	if store.saved != nil {
		t.Error("an unknown action id must not reach the store, that row could never be resolved again")
	}
}

func TestQuickActionService_SaveRoundTrips(t *testing.T) {
	store := &fakeQuickStore{}
	svc := NewQuickActionService(store, fixedProfile(ProfileCounter), nil)
	eff, err := svc.SaveConfig(context.Background(), ActionNewSale, true,
		map[string]bool{ProfileCounter: false, "warehouse": true}, "admin-1")
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if store.by != "admin-1" {
		t.Errorf("the actor must reach the store for the audit columns; got %q", store.by)
	}
	if _, bad := store.saved.Profiles["warehouse"]; bad {
		t.Error("an unknown profile must be normalised away before it is stored")
	}
	if eff.Profiles[ProfileCounter] {
		t.Error("the answer must reflect the edit just made (counter=false)")
	}
	// And the same edit is what the next reader resolves.
	_, actions := svc.VisibleFor(context.Background(), "INSIDE_SALES")
	if len(actions) != 0 {
		t.Fatalf("after switching the counter shape off, a counter caller must see nothing; got %d", len(actions))
	}
}

func TestQuickActionService_AdminViewCoversTheWholeRegistry(t *testing.T) {
	svc := NewQuickActionService(&fakeQuickStore{}, fixedProfile(ProfileAdmin), nil)
	view := svc.AdminView(context.Background())
	if len(view) != len(ActionCatalog) {
		t.Fatalf("the admin surface must list every registered action; want %d, got %d", len(ActionCatalog), len(view))
	}
	if view[0].Lead == "" || view[0].Title == "" {
		t.Error("an admin toggling a button off needs the sentence that says what it does")
	}
}
