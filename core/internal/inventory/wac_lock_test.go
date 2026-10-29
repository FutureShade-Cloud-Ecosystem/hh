package inventory

// wac_lock_test.go, L8 MONEY-CRITICAL concurrency regression (unit half). Every WAC read-modify-write path on
// product_branch_costs (PO receipt applyReceipt, landed-cost capitalize, landed-cost restore) MUST serialize on
// (product_id, branch_id) by taking the advisory lock BEFORE it reads on-hand + the current WAC, otherwise a
// concurrent receipt and a landed-cost Apply/Void each read the old WAC, compute independently, and the second
// commit clobbers the first (lost update → subledger diverges from GL 1200). This unit test pins that the lock is
// taken (via the fakeStore's LockBranchCost counter); it FAILS without the LockBranchCost calls added to the three
// mutation paths. The end-to-end no-lost-update property under real concurrent transactions is proven by the live
// integration test TestWACLock_ReceiptVsLandedCost_NoLostUpdate (build tag: integration).

import (
	"context"
	"testing"
	"time"
)

func TestWACLock_ReceiptTakesLockBeforeRMW(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	f.hdr = poHeader{ID: "po-1", PONo: "PO-1", BranchID: "br-1", Status: "SUBMITTED", Currency: "CAD", FxRate: 1}
	f.lines = []poLine{{ID: "l1", LineNo: 1, ProductID: "p", UOM: "EA", QtyBase: 100, ReceivedBase: 0, UnitCostCents: 1000}}
	s := NewService(nil).withStore(f)

	if _, err := s.applyReceipt(ctx, nil, "po-1", nil, ""); err != nil {
		t.Fatalf("applyReceipt: %v", err)
	}
	if got := f.lockedBefore[key("p", "br-1")]; got != 1 {
		t.Fatalf("applyReceipt: LockBranchCost(p,br-1) called %d times, want 1, WAC RMW is NOT serialized (lost-update risk)", got)
	}
}

func TestWACLock_CapitalizeTakesLock(t *testing.T) {
	ctx := context.Background()
	const product, branch = "p1", "b1"
	f := newFakeStore()
	f.onhand[key(product, branch)] = 100
	f.cost[key(product, branch)] = 500
	s := NewService(nil).withStore(f)

	if _, _, _, err := s.capitalizeLandedSnap(ctx, nil, product, branch, 2700, time.Now(), ""); err != nil {
		t.Fatalf("capitalizeLandedSnap: %v", err)
	}
	if got := f.lockedBefore[key(product, branch)]; got != 1 {
		t.Fatalf("capitalizeLandedSnap: LockBranchCost(%s,%s) called %d times, want 1, WAC RMW is NOT serialized", product, branch, got)
	}
}

func TestWACLock_RestoreTakesLock(t *testing.T) {
	ctx := context.Background()
	const product, branch = "p1", "b1"
	f := newFakeStore()
	f.onhand[key(product, branch)] = 100
	f.cost[key(product, branch)] = 527
	s := NewService(nil).withStore(f)

	// Apply then Void round-trip; the Void path (restoreLandedCost) must also take the lock.
	absorbed, snapAvg, _, err := s.capitalizeLandedSnap(ctx, nil, product, branch, 2700, time.Now(), "")
	if err != nil {
		t.Fatalf("capitalizeLandedSnap: %v", err)
	}
	before := f.lockedBefore[key(product, branch)]
	if _, err := s.restoreLandedCost(ctx, nil, product, branch, snapAvg, absorbed, time.Now(), ""); err != nil {
		t.Fatalf("restoreLandedCost: %v", err)
	}
	if got := f.lockedBefore[key(product, branch)] - before; got != 1 {
		t.Fatalf("restoreLandedCost: LockBranchCost(%s,%s) called %d times, want 1, WAC RMW is NOT serialized", product, branch, got)
	}
}
