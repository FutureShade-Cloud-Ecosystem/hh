//go:build integration

package tasks

import (
	"context"
	"testing"
)

// TestIntegrationOpenShortageSummaryCountsWhatTheYardLeftOpen is the yard manager's "open
// shortages" source (0381): unresolved shortages the yard team raised, counted by the module that
// owns them, fenced to the reach, with the age of the oldest. The database is shared, so each
// assertion is a delta against a baseline read first.
func TestIntegrationOpenShortageSummaryCountsWhatTheYardLeftOpen(t *testing.T) {
	svc, _, _, orderID, _ := newYardFixture(t)
	ctx := context.Background()
	trt := ShortageReach{BranchIDs: []string{trentonBranch}}

	base, err := svc.OpenShortageSummary(ctx, trt)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	baseAll, err := svc.OpenShortageSummary(ctx, ShortageReach{Global: true})
	if err != nil {
		t.Fatalf("baseline global: %v", err)
	}

	task := pickToInProgress(t, svc, orderID)
	first, err := svc.FlagShortage(ctx, ShortageInput{TaskID: task.ID, Description: "Charcoal paver", QtyShort: "2", UOM: "PALLET"})
	if err != nil {
		t.Fatalf("flag: %v", err)
	}
	if _, err := svc.FlagShortage(ctx, ShortageInput{TaskID: task.ID, Description: "HPB sand", QtyShort: "1", UOM: "TONNE"}); err != nil {
		t.Fatalf("flag 2: %v", err)
	}

	got, err := svc.OpenShortageSummary(ctx, trt)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if got.Count != base.Count+2 {
		t.Fatalf("open shortages %d -> %d, want two more", base.Count, got.Count)
	}
	if got.Oldest.IsZero() {
		t.Error("the summary carries no age for the oldest open shortage")
	}
	if all, _ := svc.OpenShortageSummary(ctx, ShortageReach{Global: true}); all.Count != baseAll.Count+2 {
		t.Errorf("global %d -> %d, want two more", baseAll.Count, all.Count)
	}

	// The fence fails closed: another branch and no grants both see none of these.
	other, err := svc.OpenShortageSummary(ctx, ShortageReach{BranchIDs: []string{"5d1b3f6e-0000-4000-9000-0000000000ff"}})
	if err != nil || other.Count != 0 {
		t.Fatalf("another branch's reach counted %d (%v), want 0", other.Count, err)
	}
	if none, err := svc.OpenShortageSummary(ctx, ShortageReach{}); err != nil || none.Count != 0 {
		t.Fatalf("a caller with no grants counted %d (%v), want 0", none.Count, err)
	}

	// Resolving one takes it out of the count, the resolved report stays on the task.
	if _, err := svc.ResolveShortage(ctx, ResolveShortageInput{TaskID: task.ID, ShortageID: first.ID, Resolution: "back-ordered"}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if after, _ := svc.OpenShortageSummary(ctx, trt); after.Count != base.Count+1 {
		t.Fatalf("after a resolve the count is %d, want %d", after.Count, base.Count+1)
	}
}
