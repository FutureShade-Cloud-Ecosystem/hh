//go:build integration

// draft_cover_integration_test.go, the principal's ruling that a draft never covers a shortfall,
// proved end to end on the demo world with the composition root's own adapters: the K08 gap
// (Granular A at the procurement branch) is drafted from the worklist, STAYS on the worklist
// carrying the draft's id and number, and leaves it only when the draft is placed.
package main

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
)

func TestIntegration_ADraftFromK08LeavesK08OnTheWorklistAndPlacingItTakesK08Off(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "draftcover")
	seedOnce(t, ctx, pool)

	purchasingSvc := purchasing.NewService(purchasing.NewRepository(pool)).
		WithVendors(w4cVendorDirectory{pool: pool}).
		WithBranches(item5BranchDirectory{pool: pool}).
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	inventorySvc := inventory.NewService(inventory.NewRepository(pool)).
		WithPOCreator(unionPOCreator{svc: purchasingSvc}).
		WithOnOrderReader(inv470OnOrder{svc: purchasingSvc}).
		WithDraftPOReader(invDraftPOs{svc: purchasingSvc})
	// A global leader, on a user row the migrations planted: the gap is the point here, the wall is
	// proved at the gate (authz).
	var userID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users ORDER BY created_at, id LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("no planted user: %v", err)
	}
	ctx = staffcontext.ContextWithStaff(ctx, staffcontext.StaffContext{
		UserID: userID, RoleCode: "PROCUREMENT_MANAGER",
		Tier: staffcontext.TierLeadership, Scope: staffcontext.ScopeGlobal, Status: "ACTIVE",
	})

	k08 := demoID("product:P07")
	find := func() (inventory.ShortfallRow, bool) {
		t.Helper()
		wl, err := inventorySvc.ShortfallWorklist(ctx, "", 500)
		if err != nil {
			t.Fatalf("worklist: %v", err)
		}
		for _, r := range wl.Rows {
			if r.ProductID == k08 && r.BranchCode == "TRT" {
				return r, true
			}
		}
		return inventory.ShortfallRow{}, false
	}

	row, ok := find()
	if !ok {
		t.Fatal("K08 (Granular A at Trenton) is not on the demo worklist, the gap the buyer is meant to find")
	}
	if row.DraftPOID != "" || row.OnOrderBase != 0 {
		t.Fatalf("K08 starts with on order %v and a draft %q, want neither", row.OnOrderBase, row.DraftPOID)
	}

	_, poID, poNo, err := inventorySvc.CreatePOFromShortfall(ctx, row.BranchID, []string{k08})
	if err != nil {
		t.Fatalf("draft a PO from the worklist: %v", err)
	}

	row, ok = find()
	if !ok {
		t.Fatal("the draft took K08 off the worklist: a draft must never cover a shortfall")
	}
	if row.OnOrderBase != 0 {
		t.Errorf("K08 reads %v on order with only a draft, want 0", row.OnOrderBase)
	}
	if row.DraftPOID != poID || row.DraftPONo != poNo {
		t.Errorf("K08 points at draft %q (%q), want the one just made %q (%q)", row.DraftPOID, row.DraftPONo, poID, poNo)
	}

	if _, err := purchasingSvc.Transition(ctx, poID, purchasing.StatusOrdered); err != nil {
		t.Fatalf("place the draft: %v", err)
	}
	if row, ok = find(); ok {
		t.Errorf("K08 is still on the worklist after its order was placed: on order %v, short %v, draft %q", row.OnOrderBase, row.ShortBase, row.DraftPOID)
	}
}
