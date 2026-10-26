//go:build integration

package pos

// return_requests_reach_integration_test.go (UX-89): the staff queue's branch reach is part of the SQL, ahead of
// the row limit. A list narrowed to a branch holds only that branch's requests whatever other branches hold; an
// empty list reads nothing; nil reads every branch.

import (
	"context"
	"testing"
)

func TestIntegration_ReturnRequestQueue_ReachIsInTheQuery(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	all, err := repo.ListReturnRequestsByStatus(ctx, RequestPending, nil, 200)
	if err != nil {
		t.Fatalf("nil reach reads every branch: %v", err)
	}
	none, err := repo.ListReturnRequestsByStatus(ctx, RequestPending, []string{}, 200)
	if err != nil || len(none) != 0 {
		t.Fatalf("an empty reach reads nothing, got %d rows, %v", len(none), err)
	}
	const noSuchBranch = "00000000-0000-4000-8000-000000000000"
	narrow, err := repo.ListReturnRequestsByStatus(ctx, RequestPending, []string{noSuchBranch}, 200)
	if err != nil || len(narrow) != 0 {
		t.Fatalf("a reach holding no request's branch reads nothing, got %d rows, %v", len(narrow), err)
	}
	if len(all) > 0 {
		first := all[0].BranchID
		own, err := repo.ListReturnRequestsByStatus(ctx, RequestPending, []string{first}, 200)
		if err != nil || len(own) == 0 {
			t.Fatalf("a reach holding a request's branch reads it, got %d rows, %v", len(own), err)
		}
		for _, rr := range own {
			if rr.BranchID != first {
				t.Fatalf("a row from branch %s leaked into the reach of %s", rr.BranchID, first)
			}
		}
	}
}
