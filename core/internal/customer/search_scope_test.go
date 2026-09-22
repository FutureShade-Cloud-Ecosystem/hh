package customer

import (
	"context"
	"errors"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// -- U-park E4: the customer-search branch fence --------------------------------------------------
//
// MUTATION TARGET for this whole file: resolveSearchScope in search_scope.go. Remove the
// CanAccessBranch check, or make the empty-branch arm return an unfenced scope for a scoped caller,
// and the refusals below stop refusing.

// fenceStore records the scope the service resolved, so a test can assert the fence is passed DOWN
// to the query rather than merely computed and discarded, an authorization decision that never
// reaches the SQL is not a fence.
type fenceStore struct {
	*fakeStore
	lastScope SearchScope
	calls     int
}

func (f *fenceStore) SearchAccountsScoped(ctx context.Context, q string, limit int, scope SearchScope) ([]SearchResult, error) {
	f.lastScope = scope
	f.calls++
	return f.fakeStore.SearchAccounts(ctx, q, limit)
}

func newFenceSvc() (*Service, *fenceStore) {
	fs := &fenceStore{fakeStore: seedStore()}
	return NewService(fs), fs
}

func TestSearchAccounts_BranchFence_ScopedCallerMustNameABranch(t *testing.T) {
	svc, store := newFenceSvc()
	_, err := svc.SearchAccounts(scopedCtx("branch-trt"), "north", 0, "")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("a scoped caller that names no branch must be refused, got %v", err)
	}
	if store.calls != 0 {
		t.Fatal("a refused search must not reach the store, the rows would already be on the wire")
	}
}

func TestSearchAccounts_BranchFence_ScopedCallerCannotReachAnotherBranch(t *testing.T) {
	svc, store := newFenceSvc()
	_, err := svc.SearchAccounts(scopedCtx("branch-trt"), "north", 0, "branch-kgn")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("a branch the caller has no grant for must be refused, got %v", err)
	}
	if store.calls != 0 {
		t.Fatal("a refused search must not reach the store")
	}
}

func TestSearchAccounts_BranchFence_OwnBranchPassesTheFilterDown(t *testing.T) {
	svc, store := newFenceSvc()
	if _, err := svc.SearchAccounts(scopedCtx("branch-trt"), "north", 0, "branch-trt"); err != nil {
		t.Fatalf("own branch must be searchable: %v", err)
	}
	if store.lastScope.BranchID != "branch-trt" {
		t.Fatalf("the store must receive the branch filter, got %+v", store.lastScope)
	}
	if store.lastScope.Global {
		t.Fatal("a branch-scoped caller must not be marked GLOBAL")
	}
}

func TestSearchAccounts_BranchFence_GlobalCallerMayOmitTheBranch(t *testing.T) {
	svc, store := newFenceSvc()
	if _, err := svc.SearchAccounts(globalCtx(), "north", 0, ""); err != nil {
		t.Fatalf("a GLOBAL caller may search every branch: %v", err)
	}
	if store.lastScope.BranchID != "" || !store.lastScope.Global {
		t.Fatalf("a GLOBAL unfenced search must reach the store unfiltered, got %+v", store.lastScope)
	}
}

func TestSearchAccounts_BranchFence_GlobalCallerMayNarrow(t *testing.T) {
	svc, store := newFenceSvc()
	if _, err := svc.SearchAccounts(globalCtx(), "north", 0, "branch-kgn"); err != nil {
		t.Fatalf("a GLOBAL caller may narrow to any branch: %v", err)
	}
	if store.lastScope.BranchID != "branch-kgn" {
		t.Fatalf("the narrowing must reach the store, got %+v", store.lastScope)
	}
}

// TestSearchAccounts_BranchFence_PrecedesTheEmptyTerm400 pins the ORDER of the two checks. If the
// empty-term 400 ran first, a prober could distinguish "this branch exists and is searchable" (400)
// from "you may not search it" (403) by sending a blank term, a small oracle, but a free one.
func TestSearchAccounts_BranchFence_PrecedesTheEmptyTerm400(t *testing.T) {
	svc, _ := newFenceSvc()
	_, err := svc.SearchAccounts(scopedCtx("branch-trt"), "", 0, "branch-kgn")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("the fence must be answered before the empty-term check, got %v", err)
	}
	// And with a REACHABLE branch, the empty term is still a 400 (the fence is not swallowing it).
	_, err = svc.SearchAccounts(scopedCtx("branch-trt"), "", 0, "branch-trt")
	var bad httpx.BadRequestError
	if !errors.As(err, &bad) {
		t.Fatalf("an empty term on a reachable branch must still be a 400, got %v", err)
	}
}

func TestSearchAccounts_BranchFence_UnauthenticatedIsNot500(t *testing.T) {
	svc, _ := newFenceSvc()
	_, err := svc.SearchAccounts(context.Background(), "north", 0, "branch-trt")
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("a context with no staff caller must be 401, not a 500, got %v", err)
	}
}

// TestSearchAccounts_UnfencedStore_FailsClosed covers the seam's degenerate case: a store that does
// not implement the scoped search (the portal BFF's fake is one) cannot serve a FENCED search,
// because SearchResult carries no home branch and there is nothing to filter on. Serving the
// unfiltered rows "best effort" there would leak exactly what the fence exists to stop.
func TestSearchAccounts_UnfencedStore_FailsClosed(t *testing.T) {
	svc := NewService(seedStore()) // the plain fake: accountStore only, no SearchAccountsScoped
	if _, err := svc.SearchAccounts(scopedCtx("branch-trt"), "north", 0, "branch-trt"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a store that cannot fence must refuse a fenced search, got %v", err)
	}
	// The one search such a store CAN answer honestly is the unfenced GLOBAL one.
	if _, err := svc.SearchAccounts(globalCtx(), "north", 0, ""); err != nil {
		t.Fatalf("an unfenced GLOBAL search needs no fencing support: %v", err)
	}
}

// belt-and-braces: the identity scope constants this file leans on are the ones the guards stamp.
func TestSearchScope_UsesRealIdentityScopes(t *testing.T) {
	sc, err := resolveSearchScope(globalCtx(), "")
	if err != nil || !sc.Global {
		t.Fatalf("a %s-scope caller must resolve as global, got %+v / %v", identity.ScopeGlobal, sc, err)
	}
}
