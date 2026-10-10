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
		wl, err := inventorySvc.ShortfallWorklist(ctx, inventory.BranchReach{Global: true}, "", 500)
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

	_, poID, poNo, err := inventorySvc.CreatePOFromShortfall(ctx, inventory.BranchReach{Global: true}, row.BranchID, []string{k08})
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

// The Opus review's P1-1, on the composition root's own adapters: a draft started at Trenton must
// not reach a buyer whose only grant is Kingston, neither as a row on the worklist nor as the draft
// pointer that row would carry, and the create route's read answers 403 for the branch out of reach
// whatever the stock there says.
func TestIntegration_ADraftAtTrentonIsNotReadByAKingstonOnlyBuyer(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "draftfence")
	seedOnce(t, ctx, pool)

	purchasingSvc := purchasing.NewService(purchasing.NewRepository(pool)).
		WithVendors(w4cVendorDirectory{pool: pool}).
		WithBranches(item5BranchDirectory{pool: pool}).
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	inventorySvc := inventory.NewService(inventory.NewRepository(pool)).
		WithPOCreator(unionPOCreator{svc: purchasingSvc}).
		WithOnOrderReader(inv470OnOrder{svc: purchasingSvc}).
		WithDraftPOReader(invDraftPOs{svc: purchasingSvc})
	var userID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users ORDER BY created_at, id LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("no planted user: %v", err)
	}
	leader := staffcontext.ContextWithStaff(ctx, staffcontext.StaffContext{
		UserID: userID, RoleCode: "PROCUREMENT_MANAGER",
		Tier: staffcontext.TierLeadership, Scope: staffcontext.ScopeGlobal, Status: "ACTIVE",
	})
	var trt, kgn string
	if err := pool.QueryRow(ctx, `SELECT (SELECT id::text FROM locations WHERE code = 'TRT'), (SELECT id::text FROM locations WHERE code = 'KGN')`).Scan(&trt, &kgn); err != nil || trt == "" || kgn == "" {
		t.Fatalf("branch ids: %q %q %v", trt, kgn, err)
	}
	k08 := demoID("product:P07")
	if _, _, _, err := inventorySvc.CreatePOFromShortfall(leader, inventory.BranchReach{Global: true}, trt, []string{k08}); err != nil {
		t.Fatalf("draft K08 at Trenton: %v", err)
	}

	kingstonOnly := staffcontext.ContextWithStaff(ctx, staffcontext.StaffContext{
		UserID: userID, RoleCode: "PROCUREMENT_MANAGER", Tier: staffcontext.TierManager,
		Scope: staffcontext.ScopeBranch, Status: "ACTIVE", Branches: map[string]bool{kgn: true},
	})
	reach := inventory.BranchReach{BranchIDs: []string{kgn}}
	wl, err := inventorySvc.ShortfallWorklist(kingstonOnly, reach, "", 500)
	if err != nil {
		t.Fatalf("the Kingston buyer's own worklist: %v", err)
	}
	for _, r := range wl.Rows {
		if r.BranchID == trt || r.DraftPOID != "" && r.BranchCode == "TRT" {
			t.Errorf("a Kingston only buyer read Trenton's row %s with draft %q (%q)", r.SKU, r.DraftPOID, r.DraftPONo)
		}
	}
	if _, err := inventorySvc.ShortfallWorklist(kingstonOnly, reach, trt, 500); err == nil {
		t.Errorf("a named branch out of reach was read, want the 403")
	}
	// The create route fences its read first: the answer for Trenton does not depend on Trenton's stock.
	_, _, _, err = inventorySvc.CreatePOFromShortfall(kingstonOnly, reach, trt, []string{k08})
	if err == nil || err.Error() != "inventory: you do not have access to that branch" {
		t.Errorf("create from shortfall at Trenton by a Kingston only buyer = %v, want the 403 in words", err)
	}
}
