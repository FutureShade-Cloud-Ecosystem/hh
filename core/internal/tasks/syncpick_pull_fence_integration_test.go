//go:build integration

// syncpick_pull_fence_integration_test.go: the pick_tasks sync pull held to the caller's branch reach (UX-81,
// FROM-PRINCIPAL item 69). A branch scoped yard device pulls the picks of the branches it reaches and the picks
// that belong to no branch, never another branch's rows (which carry the claimer's id); a GLOBAL caller pulls
// all; a caller with no active authority pulls nothing; and the fence leaves the cursor exact: the pull's
// `seq > since` neither skips nor reorders the rows the caller does reach.
//
// Run with DATABASE_URL set to a throwaway migrated database; the legs skip politely without one.
package tasks

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
)

// pullAs makes the pick store's staff authority answer a caller scoped as given, then pulls one page.
func (d *pickDoor) pullAs(sc identity.StaffContext, has bool, since int64, limit int) []string {
	d.t.Helper()
	d.authority = func() (identity.StaffContext, bool, error) { return sc, has, nil }
	items, _, err := d.store.Pull(context.Background(), d.f, since, limit)
	if err != nil {
		d.t.Fatalf("pull: %v", err)
	}
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return ids
}

func (d *pickDoor) pullRaw(sc identity.StaffContext) string {
	d.t.Helper()
	d.authority = func() (identity.StaffContext, bool, error) { return sc, true, nil }
	items, _, err := d.store.Pull(context.Background(), d.f, 0, 100)
	if err != nil {
		d.t.Fatalf("pull: %v", err)
	}
	raw, _ := json.Marshal(items)
	return string(raw)
}

func scopedTo(user string, branches ...string) identity.StaffContext {
	b := map[string]bool{}
	for _, id := range branches {
		b[id] = true
	}
	return identity.StaffContext{UserID: user, RoleCode: "YARD_TEAM", Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
		Status: identity.StatusActive, Branches: b}
}

func contains(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func TestPickPullFence_ABranchScopedDeviceDoesNotPullAnotherBranchsPicks(t *testing.T) {
	d := newPickDoor(t)
	pickA, pickB, pickNone := d.task(d.branchA), d.task(d.branchB), d.task("")
	// A claims branch A's pick, so its row carries the claimer's id.
	d.authority = func() (identity.StaffContext, bool, error) { return scopedTo(d.user, d.branchA), true, nil }
	if res := d.push(apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "update", Payload: map[string]any{"id": pickA, "status": "ACCEPTED"}}); res[0].Status != "applied" {
		t.Fatalf("A claims: %+v %v", res[0], res[0].Error)
	}
	claimer := d.user

	b := scopedTo(uuid.NewString(), d.branchB)
	ids := d.pullAs(b, true, 0, 100)
	t.Logf("branch B device pulls %d rows: A's pick present %v, B's pick present %v, branchless pick present %v", len(ids),
		contains(ids, pickA), contains(ids, pickB), contains(ids, pickNone))
	if contains(ids, pickA) {
		t.Errorf("a branch B device must not pull branch A's pick")
	}
	if !contains(ids, pickB) || !contains(ids, pickNone) {
		t.Errorf("a branch B device pulls its own branch's pick and a pick with no branch: %v", ids)
	}
	if raw := d.pullRaw(b); strings.Contains(raw, claimer) || strings.Contains(raw, pickA) {
		t.Errorf("another branch's claimer id and pick id must not ride the pull")
	}

	a := scopedTo(claimer, d.branchA)
	ids = d.pullAs(a, true, 0, 100)
	if !contains(ids, pickA) || contains(ids, pickB) || !contains(ids, pickNone) {
		t.Errorf("a branch A device pulls A's pick and the branchless pick, not B's: %v", ids)
	}

	global := scopedTo(uuid.NewString())
	global.Scope, global.Tier = identity.ScopeGlobal, identity.TierLeadership
	ids = d.pullAs(global, true, 0, 100)
	if !contains(ids, pickA) || !contains(ids, pickB) || !contains(ids, pickNone) {
		t.Errorf("a GLOBAL caller still pulls every branch: %v", ids)
	}

	// A scoped caller who holds no branch grant pulls only the branchless pick.
	none := scopedTo(uuid.NewString())
	ids = d.pullAs(none, true, 0, 100)
	if contains(ids, pickA) || contains(ids, pickB) || !contains(ids, pickNone) {
		t.Errorf("a scoped caller with no grant pulls only the branchless pick: %v", ids)
	}
}

func TestPickPullFence_NoActiveAuthorityPullsNothing(t *testing.T) {
	d := newPickDoor(t)
	d.task(d.branchA)
	d.task("")
	if ids := d.pullAs(identity.StaffContext{}, false, 0, 100); len(ids) != 0 {
		t.Errorf("an absent authority pulls nothing: %v", ids)
	}
	inactive := scopedTo(uuid.NewString(), d.branchA)
	inactive.Status = "SUSPENDED"
	if ids := d.pullAs(inactive, true, 0, 100); len(ids) != 0 {
		t.Errorf("an inactive authority pulls nothing: %v", ids)
	}
}

// TestPickPullFence_TheCursorStaysExact: picks of two branches interleave in seq order; a branch B device paging
// two rows at a time sees exactly its own rows, in seq order, none skipped, none repeated.
func TestPickPullFence_TheCursorStaysExact(t *testing.T) {
	d := newPickDoor(t)
	var wantB []string
	for i := 0; i < 4; i++ {
		d.task(d.branchA)
		wantB = append(wantB, d.task(d.branchB))
	}
	d.task(d.branchA)
	b := scopedTo(uuid.NewString(), d.branchB)
	d.authority = func() (identity.StaffContext, bool, error) { return b, true, nil }

	var got []string
	var since int64
	pages := 0
	for {
		items, _, err := d.store.Pull(context.Background(), d.f, since, 2)
		if err != nil {
			t.Fatalf("pull: %v", err)
		}
		pages++
		if len(items) == 0 {
			break
		}
		for _, it := range items {
			if it.Seq <= since {
				t.Fatalf("the cursor repeated or reordered a row: seq %d after %d", it.Seq, since)
			}
			since = it.Seq
			got = append(got, it.ID)
		}
		if pages > 20 {
			t.Fatal("the cursor did not advance")
		}
	}
	if strings.Join(got, ",") != strings.Join(wantB, ",") {
		t.Errorf("a branch B device pages exactly its own rows in seq order:\n got  %v\n want %v", got, wantB)
	}
	if pages != 3 {
		t.Errorf("four rows at two a page is two full pages and an empty one, got %d pages", pages)
	}
}
