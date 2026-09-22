//go:build integration

// branch_fence_integration_test.go, DIB-390. A count sheet is a stock-and-GL instrument: freezing it
// snapshots a branch's on-hand, and POSTING it writes signed COUNT moves at weighted-average cost plus a
// reason-coded journal, all stamped with the SHEET's branch. Until now the only branch check was
// "non-empty", so a Kingston staffer could open a Trenton count, and, worse, post one.
//
// These tests fence both ends: creation (the request's branch) and every transition (the LOADED sheet's
// branch, which is the one the money follows).
package invcount

import (
	"errors"
	"testing"
)

const branchKGN = "5d1b3f6e-0000-4000-9000-000000000002"

func forbidden(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: want a refusal, got nil", what)
	}
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 403 {
		t.Fatalf("%s: err = %v, want a 403 branch refusal", what, err)
	}
}

// TestIntegration_InvCount_CreateCrossBranchForbidden: a Kingston caller cannot open a Trenton sheet.
func TestIntegration_InvCount_CreateCrossBranchForbidden(t *testing.T) {
	pool := intPool(t)
	svc := realSvc(pool, &fakeStockSeam{})

	_, err := svc.CreateSheet(ctxAsAt(userMaker, branchKGN), CreateSheetRequest{BranchID: branchTRT, Scope: "FULL"})
	forbidden(t, err, "create a Trenton sheet as a Kingston caller")

	// …and the same caller CAN open one in their own branch (the fence is a fence, not a wall).
	own, err := svc.CreateSheet(ctxAsAt(userMaker, branchKGN), CreateSheetRequest{BranchID: branchKGN, Scope: "FULL"})
	if err != nil {
		t.Fatalf("create in own branch: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(t.Context(), `DELETE FROM count_lines WHERE sheet_id=$1`, own.ID)
		_, _ = pool.Exec(t.Context(), `DELETE FROM count_sheets WHERE id=$1`, own.ID)
	})
}

// TestIntegration_InvCount_PostCrossBranchForbidden is the leg that matters most: the post moves WAC stock
// and posts GL. A Kingston caller must not be able to post a Trenton sheet, even a fully approved one.
func TestIntegration_InvCount_PostCrossBranchForbidden(t *testing.T) {
	pool := intPool(t)
	stock := &fakeStockSeam{avg: 350, snaps: []Snapshot{
		{ProductID: prodP1, LocationID: branchTRT, OnHandBase: 100, AvgCostCents: 350},
	}}
	stock.cleanupMoves(t, pool)
	svc := realSvc(pool, stock)

	// Drive a Trenton sheet all the way to APPROVED with Trenton callers.
	sheet, err := svc.CreateSheet(ctxAs(userMaker), CreateSheetRequest{BranchID: branchTRT})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(t.Context(), `DELETE FROM count_lines WHERE sheet_id=$1`, sheet.ID)
		_, _ = pool.Exec(t.Context(), `DELETE FROM count_sheets WHERE id=$1`, sheet.ID)
	})
	if _, err := svc.FreezeSheet(ctxAs(userMaker), sheet.ID); err != nil {
		t.Fatalf("freeze: %v", err)
	}
	if _, err := svc.SubmitCounts(ctxAs(userMaker), sheet.ID, SubmitCountsRequest{
		Lines: []CountLineInput{{ProductID: prodP1, LocationID: branchTRT, CountedQtyBase: 98, ReasonCode: "DAMAGE"}},
	}); err != nil {
		t.Fatalf("submit counts: %v", err)
	}
	if _, err := svc.ApproveSheet(ctxAs(userCheck), sheet.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// The cross-branch post is refused, and nothing moves.
	kgn := ctxAsAt(userCheck, branchKGN)
	forbidden(t, svc.PostSheet(kgn, sheet.ID), "post a Trenton sheet as a Kingston caller")
	if n := len(stock.createdMoves); n != 0 {
		t.Errorf("a refused post wrote %d stock move(s); want 0", n)
	}
	var status string
	if err := pool.QueryRow(t.Context(), `SELECT status FROM count_sheets WHERE id=$1`, sheet.ID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status == StatusPosted {
		t.Error("a refused post still flipped the sheet to POSTED")
	}

	// The branch's OWN manager may post it (ruled: a branch-scoped manager of that branch, not GLOBAL).
	if err := svc.PostSheet(ctxAs(userCheck), sheet.ID); err != nil {
		t.Fatalf("own-branch manager post: %v", err)
	}
}

// TestIntegration_InvCount_TransitionsCrossBranchForbidden covers the rest of the lifecycle: every
// transition re-checks against the LOADED sheet's branch, not against whoever created it.
func TestIntegration_InvCount_TransitionsCrossBranchForbidden(t *testing.T) {
	pool := intPool(t)
	stock := &fakeStockSeam{avg: 350, snaps: []Snapshot{
		{ProductID: prodP1, LocationID: branchTRT, OnHandBase: 100, AvgCostCents: 350},
	}}
	stock.cleanupMoves(t, pool)
	svc := realSvc(pool, stock)

	sheet, err := svc.CreateSheet(ctxAs(userMaker), CreateSheetRequest{BranchID: branchTRT})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(t.Context(), `DELETE FROM count_lines WHERE sheet_id=$1`, sheet.ID)
		_, _ = pool.Exec(t.Context(), `DELETE FROM count_sheets WHERE id=$1`, sheet.ID)
	})
	kgn := ctxAsAt(userCheck, branchKGN)

	t.Run("freeze", func(t *testing.T) {
		_, err := svc.FreezeSheet(kgn, sheet.ID)
		forbidden(t, err, "freeze across branches")
	})
	t.Run("cancel", func(t *testing.T) {
		forbidden(t, svc.CancelSheet(kgn, sheet.ID), "cancel across branches")
	})

	if _, err := svc.FreezeSheet(ctxAs(userMaker), sheet.ID); err != nil {
		t.Fatalf("freeze (own branch): %v", err)
	}
	t.Run("submit-counts", func(t *testing.T) {
		_, err := svc.SubmitCounts(kgn, sheet.ID, SubmitCountsRequest{
			Lines: []CountLineInput{{ProductID: prodP1, LocationID: branchTRT, CountedQtyBase: 1, ReasonCode: "DAMAGE"}},
		})
		forbidden(t, err, "submit counts across branches")
	})

	if _, err := svc.SubmitCounts(ctxAs(userMaker), sheet.ID, SubmitCountsRequest{
		Lines: []CountLineInput{{ProductID: prodP1, LocationID: branchTRT, CountedQtyBase: 99, ReasonCode: "DAMAGE"}},
	}); err != nil {
		t.Fatalf("submit counts (own branch): %v", err)
	}
	t.Run("approve", func(t *testing.T) {
		_, err := svc.ApproveSheet(kgn, sheet.ID)
		forbidden(t, err, "approve across branches")
	})
}
