package dashboard

import (
	"context"
	"testing"
)

// manager_home_profile_test.go, the per-manager-role profiles (decision 5, one Manager Home whose
// landing is a per-role profile row in the 0140 substrate). The unit half pins the VOCABULARY and
// the RESOLUTION; the integration half (manager_home_profile_integration_test.go) pins the seeded
// rows, because a Go map only proves that I can type.
//
// THE SHAPE OF THE RULE. The five job shapes stay the five job shapes: counter, yard, driver,
// manager, admin. What this adds is one profile row per MANAGER ROLE, named manager.<role>, so a
// Manager Home is curated per role (its widgets, its Needs-you sources, its tabs) instead of six
// roles sharing the controller's "manager" shape. A manager.* profile is a REFINEMENT of the
// vocabulary, not a sixth job shape: the curation copies its base shape's rows on the day it is
// born, and diverges from there by editing data, never code.
//
// The eight, exactly as the role matrix files them under manager-home (default.json: work or
// manage): branch, sales, yard, logistics, procurement, payables and receivables, financial
// controller, general manager.

// managerProfiles is the eight per-role profile names, in the matrix's role order. A closed list in
// code because the CHECK constraint carries the same eight in SQL and a ninth is a migration by
// design (a profile row set a dealer can curate, born copied from its base shape).
var managerProfiles = []string{
	"manager.branch",
	"manager.sales",
	"manager.yard",
	"manager.logistics",
	"manager.procurement",
	"manager.payables",
	"manager.controller",
	"manager.general",
}

// TestManagerProfilesAreProfiles pins the closed set: every manager role's profile value is a
// known profile to IsProfile and the Profiles list, because IsProfile is the gate behind every
// admin write (SaveProfileLayout, SaveProfileTabs, SaveProfileWidgets, SaveProfileGallery all
// refuse an unknown name with ErrUnknownProfile). A manager profile the gate refuses is a profile
// an admin cannot curate, which is a profile that cannot diverge from its birth copy, which is the
// feature not existing.
func TestManagerProfilesAreProfiles(t *testing.T) {
	for _, p := range managerProfiles {
		if !IsProfile(p) {
			t.Errorf("%q is not a known profile; the admin gates would refuse to curate it", p)
		}
		found := false
		for _, v := range Profiles {
			if v == p {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%q is missing from the Profiles list; the admin editor would not offer it", p)
		}
	}
	// The five job shapes are still the vocabulary's first-class members; widening must not
	// replace them, or every non-manager role in the building loses its Home.
	for _, p := range []string{ProfileCounter, ProfileYard, ProfileDriver, ProfileManager, ProfileAdmin} {
		if !IsProfile(p) {
			t.Errorf("%q stopped being a profile", p)
		}
	}
}

// TestProfileForAnswersTheManagersOwnProfile is the resolution half of the red test: the yard
// manager's home answers manager.yard, NOT the shared "manager" shape, and another manager role
// still answers its own. ProfileFor reads the store, so this runs against the fake; the seeded
// rows behind the fake's map are the integration half.
func TestProfileForAnswersTheManagersOwnProfile(t *testing.T) {
	fs := newFakeStore()
	fs.roleProfile = map[string]string{
		"YARD_MANAGER":         "manager.yard",
		"BRANCH_MANAGER":       "manager.branch",
		"FINANCIAL_CONTROLLER": "manager.controller",
	}
	s := NewService(fs, nil)
	cases := map[string]string{
		"YARD_MANAGER":         "manager.yard",
		"BRANCH_MANAGER":       "manager.branch",
		"FINANCIAL_CONTROLLER": "manager.controller",
	}
	for role, want := range cases {
		if got := s.ProfileFor(context.Background(), role); got != want {
			t.Errorf("%s answers %q, want their own profile %q (the shared shapes are not the answer)",
				role, got, want)
		}
	}
	// An unknown profile from the store still falls back to the counter shape, 0140's least
	// privileged useful home; widening the vocabulary must not widen the fallback.
	fs.roleProfile["SOMEONE_NEW"] = "manager.nobody"
	if got := s.ProfileFor(context.Background(), "SOMEONE_NEW"); got != ProfileCounter {
		t.Fatalf("an unmapped manager profile resolves to %q, want the counter fallback", got)
	}
}

// TestManagerProfilesFallBackToTheirBaseShapesDefaults pins the unreadable-table posture for the
// refinements: a build that cannot read dashboard_needs_sources serves each manager the policy its
// profile was born with, the yard manager's own three sources included, and never an empty queue.
func TestManagerProfilesFallBackToTheirBaseShapesDefaults(t *testing.T) {
	for _, p := range managerProfiles {
		if len(defaultNeedsFor(p)) == 0 {
			t.Errorf("%s has no fallback needs policy; an unreadable table would blank its queue", p)
		}
	}
	want := []string{NeedsOpenShortages, NeedsStalePicks, NeedsReceivingDifferences}
	got := defaultNeedsFor(ProfileManagerYard)
	if len(got) != len(want) {
		t.Fatalf("the yard manager's fallback holds %d sources, want %v", len(got), want)
	}
	for i, k := range want {
		if got[i].SourceKey != k {
			t.Errorf("yard manager source %d is %q, want %q", i, got[i].SourceKey, k)
		}
	}
	if a, b := defaultNeedsFor(ProfileManagerBranch), DefaultNeedsSources[ProfileManager]; len(a) != len(b) {
		t.Errorf("the branch manager's fallback is %d sources, its base shape ships %d", len(a), len(b))
	}
	if a, b := defaultNeedsFor(ProfileManagerGeneral), DefaultNeedsSources[ProfileAdmin]; len(a) != len(b) {
		t.Errorf("the general manager's fallback is %d sources, its base shape ships %d", len(a), len(b))
	}
	// A job shape still answers itself, exactly as before.
	if len(defaultNeedsFor(ProfileCounter)) != len(DefaultNeedsSources[ProfileCounter]) {
		t.Error("a plain job shape's fallback moved")
	}
}

// TestNewSaleIsOfferedToTheSellingManagers pins the quick action defaults: + Sale followed the
// selling shapes, so the managers born from manager and admin keep it, and the two born from the
// dock do not, exactly as their base shape.
func TestNewSaleIsOfferedToTheSellingManagers(t *testing.T) {
	meta := ActionByID[ActionNewSale]
	for _, p := range managerProfiles {
		base := ManagerBaseShape[p]
		if got, want := VisibleFor(meta, nil, p), meta.DefaultProfiles[base]; got != want {
			t.Errorf("%s sees + Sale = %v, its base shape %s sees %v", p, got, base, want)
		}
	}
}

// TestEveryManagerProfileHasABaseShape keeps the closed map whole: a refinement with no base shape
// would have nothing to be born from.
func TestEveryManagerProfileHasABaseShape(t *testing.T) {
	for _, p := range managerProfiles {
		base, ok := ManagerBaseShape[p]
		if !ok || !IsProfile(base) || base == p {
			t.Errorf("%s has base shape %q (%v)", p, base, ok)
		}
	}
	if len(ManagerBaseShape) != len(managerProfiles) {
		t.Errorf("ManagerBaseShape names %d profiles, the closed list holds %d",
			len(ManagerBaseShape), len(managerProfiles))
	}
}
