package reporting

// fence_test.go, the unit legs of the branch fence: the structural promises about how the reach binds
// that must hold with NO database, because they are about the fence itself and not about any row. The
// behavioural legs (who reads what across branches) live in fence_integration_test.go.

import (
	"context"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// TestBranchReach_ZeroValueFailsClosed pins the binding of the ZERO reach. pgx binds a nil slice as
// SQL NULL, and NULL is the global arm's whole-list sentinel in every fenced query, so a zero
// branchReach that reached a query by accident would read every row in every branch. The zero value
// must bind as an EMPTY array: = ANY('{}') is false for every row, the fence fails closed.
func TestBranchReach_ZeroValueFailsClosed(t *testing.T) {
	zero := branchReach{}
	arr, ok := zero.filter().([]string)
	if !ok {
		t.Fatalf("the zero reach filters as %T, want a []string", zero.filter())
	}
	if arr == nil {
		t.Fatal("the zero reach filters as a nil slice, which pgx binds as SQL NULL: NULL is the " +
			"GLOBAL arm, and a zero reach would read every row in every fenced list. Bind an empty " +
			"array, the fence fails closed")
	}
	if len(arr) != 0 {
		t.Errorf("the zero reach filters as %v, want an empty array", arr)
	}
	// The global arm stays open by intent: NULL is what keeps the global caller's whole list.
	global := branchReach{global: true}
	if global.filter() != nil {
		t.Error("the global reach must keep filtering as SQL NULL, it is the whole-list arm")
	}
}

// TestReachOf_EmptyGrantsBindEmpty pins the projection: the three shapes of a caller with no grants
// (a nil map, an empty map, an all-false map) all resolve to a non-global reach whose filter is a
// NON-NIL empty array, never to the global arm.
func TestReachOf_EmptyGrantsBindEmpty(t *testing.T) {
	for name, branches := range map[string]map[string]bool{
		"nil grant map":    nil,
		"empty grant map":  {},
		"all false grants": {"b1": false, "b2": false},
	} {
		svc := NewService(nil).withCaller(func(context.Context) (identity.StaffContext, bool) {
			return identity.StaffContext{
				UserID: "u", RoleCode: "BRANCH_MANAGER",
				Tier: identity.TierManager, Scope: identity.ScopeBranch, Status: "ACTIVE",
				Branches: branches,
			}, true
		})
		reach, err := svc.reachOf(context.Background())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if reach.global {
			t.Errorf("%s: the reach came back global, a caller with no grants has no branches, not all of them", name)
		}
		arr, ok := reach.filter().([]string)
		if !ok || arr == nil || len(arr) != 0 {
			t.Errorf("%s: the reach filters as %#v, want a non-nil empty array", name, reach.filter())
		}
	}
}
