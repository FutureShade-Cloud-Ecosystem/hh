package authz

// rolegate_shortage_test.go, the shortage resolve route and the shortage_resolvers setting (X11, the
// roles roadmap's Yard Pick wiring): the route's census annotation names the configuration's list,
// so the gate admits a caller one of whose roles is on it and refuses one whose roles reach the
// yard surface but sit on no list, whatever their cells say. Under the decided default the list
// names exactly the two roles that reach the yard surface at work (yard_team, yard_manager), so the
// default's answer is today's answer; a dealer that widens the surface or narrows the list gets the
// list's wall.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// The resolve route's annotation names the shortage resolvers list, read through the settings
// module's own reader, so a mistyped list name is an attach-time refusal rather than a silent no-list.
func TestShortageResolve_NamesTheShortageResolversList(t *testing.T) {
	access := censusAccess(t, "yardResolveShortage")
	if access.List != roleconfig.SettingShortageResolvers {
		t.Fatalf("yardResolveShortage names list %q, want %q", access.List, roleconfig.SettingShortageResolvers)
	}
	if len(access.Surfaces) != 1 || access.Surfaces[0] != "yard-pick" || access.Level != roleconfig.LevelWork {
		t.Fatalf("yardResolveShortage access is %+v, want the yard-pick surface at work", access)
	}
}

// Under the decided default the two listed roles resolve (the list matches the surface's work cells
// exactly, which is what keeps the default's behaviour today's), and the refusal is the RBAC-403
// family the whole gate answers.
func TestShortageResolve_TheDefaultAdmitsTheListedRoles(t *testing.T) {
	for _, role := range []string{"yard_team", "yard_manager"} {
		f := newFixture(t)
		code, reached := runGatedAs(t, f, censusAccess(t, "yardResolveShortage"), role)
		if code != http.StatusOK || !reached {
			t.Errorf("%s must keep the resolve route under the default, got %d reached=%v", role, code, reached)
		}
	}
}

// The dealer seam: a configuration that widens the yard surface to a role the list does not name
// (branch_manager given yard-pick work) still refuses that role the resolve route, while the listed
// yard roles pass; and a configuration that narrows the list refuses a role whose cell would admit
// it. The list is the rule's own wall beside the level.
func TestShortageResolve_TheListRefusesARoleTheCellsWouldAdmit(t *testing.T) {
	doc := mustDefaultDocument(t)
	for i := range doc.Cells {
		if doc.Cells[i].Role == "branch_manager" && doc.Cells[i].Surface == "yard-pick" {
			doc.Cells[i].Level = roleconfig.LevelWork
		}
	}
	cfg, err := roleconfig.Validate(doc)
	if err != nil {
		t.Fatalf("Validate on the widened document: %v", err)
	}
	f := newFixture(t)
	headers := gatedStaffHeaders(t, f)
	code, body, _ := runGated(t, f, gateOverRoles(t, f, []string{"branch_manager"}, cfg), censusAccess(t, "yardResolveShortage"), headers)
	if code != http.StatusForbidden || body != string(BodyForbidden) {
		t.Fatalf("the widened cell must not carry the resolve route: status %d body %s, want the RBAC-403", code, body)
	}
	code, _, _ = runGated(t, f, gateOverRoles(t, f, []string{"yard_team"}, cfg), censusAccess(t, "yardResolveShortage"), headers)
	if code != http.StatusOK {
		t.Fatalf("the listed yard team must keep the resolve route, got %d", code)
	}

	// The narrowed list: only the yard manager, so the yard team (whose cell still reaches work)
	// is refused in the same plain words.
	var resolvers []string
	if err := json.Unmarshal(doc.Settings[roleconfig.SettingShortageResolvers], &resolvers); err != nil {
		t.Fatalf("decode the default resolvers list: %v", err)
	}
	narrowed := make([]string, 0, len(resolvers))
	for _, r := range resolvers {
		if r == "yard_manager" {
			narrowed = append(narrowed, r)
		}
	}
	raw, err := json.Marshal(narrowed)
	if err != nil {
		t.Fatalf("encode the narrowed resolvers list: %v", err)
	}
	doc.Settings[roleconfig.SettingShortageResolvers] = raw
	narrowCfg, err := roleconfig.Validate(doc)
	if err != nil {
		t.Fatalf("Validate on the narrowed document: %v", err)
	}
	code, body, _ = runGated(t, f, gateOverRoles(t, f, []string{"yard_team"}, narrowCfg), censusAccess(t, "yardResolveShortage"), headers)
	if code != http.StatusForbidden || body != string(BodyForbidden) {
		t.Fatalf("the delisted yard team must be refused the resolve route: status %d body %s, want the RBAC-403", code, body)
	}
	code, _, _ = runGated(t, f, gateOverRoles(t, f, []string{"yard_manager"}, narrowCfg), censusAccess(t, "yardResolveShortage"), headers)
	if code != http.StatusOK {
		t.Fatalf("the listed yard manager must keep the resolve route, got %d", code)
	}
}

// Holds answers the same wall ahead of the call, so a client can decline to offer the resolve
// action to a caller the gate would refuse.
func TestShortageResolve_HoldsAnswersTheList(t *testing.T) {
	g := gateOverRoles(t, newFixture(t), []string{"yard_team"}, mustConfig(t))
	held, err := g.Holds(context.Background(), "sub", "org", censusAccess(t, "yardResolveShortage"))
	if err != nil {
		t.Fatalf("Holds: %v", err)
	}
	if !held {
		t.Fatal("Holds says the yard team does not hold the resolve route's access")
	}
}
