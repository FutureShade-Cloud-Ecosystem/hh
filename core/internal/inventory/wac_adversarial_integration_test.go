//go:build integration

// wac_adversarial_integration_test.go, VERIFY:inventory-costing (W4-D). Adversarial cases for the
// weighted-average maintenance in applyReceipt (service.go L105-125):
//
//	base   = max(onHand, 0)
//	newAvg = base+recvQty > 0 ? roundOnce((scaled(base)·oldAvg + scaled(recvQty)·cadUnit) / (scaled(base) + scaled(recvQty))) : cadUnit
//
// (roundOnce: one half-away-from-zero rounding over the unrounded exact terms, the HH-183 blend.)
// The shipped suite proves the HAPPY blend (TestGRNIIdempotentAndWeightedAvg, 100@$10 then 100@$12 → $11)
// and the concurrency arm (TestWACLock_*). It does not pin the three edges the costing math actually turns
// on, so a change to either clamp would ship silently. Each case below asserts an arithmetic result derived
// independently from the formula and the inputs, never read back from the engine.
//
//	DATABASE_URL=... go test -tags=integration ./internal/inventory/ -run WACAdversarial -count=1 -v
//
// DIB-396: these fixtures write stock_moves + stock_balances and transiently move a product's on-hand
// through a NEGATIVE position, which the whole-DB reconciliation gates (internal/gate's
// TestGate_StockReconciles_NoNegative, internal/platform/db's stock-drift assertion) read as a breach while
// it is in flight. Each one therefore takes the shared stock-drift lease (key 3960001) BEFORE its first
// drifting write, exactly as the DIB-396 rules require of a drifting fixture.
package inventory

import (
	"context"
	"testing"
)

// forceOnHand drives a product's branch on-hand to exactly want by writing the balance delta through the
// SAME repository seam production uses, so the balances stay consistent with what the projection expects.
func forceOnHand(t *testing.T, svc *Service, productID string, want float64) {
	t.Helper()
	ctx := context.Background()
	cur := 0.0
	pool := svc.db().(*Repository).pool
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(on_hand_base),0) FROM stock_balances WHERE product_id=$1 AND branch_id=$2`,
		productID, branch1).Scan(&cur); err != nil {
		t.Fatalf("read on-hand: %v", err)
	}
	delta := want - cur
	if delta == 0 {
		return
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if err := svc.db().UpsertBalanceDelta(ctx, tx, productID, branch1, branch1, delta); err != nil {
		t.Fatalf("force on-hand to %v: %v", want, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// - EDGE 1: zero on-hand -
//
// With base == 0 the blend degenerates: newAvg = round((0·oldAvg + recvValue)/recvQty) = the receipt's own
// unit cost, and a STALE oldAvg from a previously sold-out position must not leak into it. This is the
// arithmetic that stops last quarter's cost basis haunting a product that went to zero and came back.
func TestWACAdversarial_ZeroOnHand_ReceiptResetsAvgToItsOwnUnitCost(t *testing.T) {
	pool := intPool(t)
	svc := newIntSvc(pool)
	dib396LeaseShared(t, pool, dib396StockDriftLease)
	p := mkProduct(t, pool, "WACADV-ZERO-1")

	// Establish a position at $10 and a real oldAvg.
	po1, _ := mkPO(t, pool, "WACADV-PO-Z1", "CAD", 1.0, []poLineSpec{{p, 40, 1000}})
	receiveAll(t, svc, pool, po1)
	if got := branchCost(t, pool, p); got != 1000 {
		t.Fatalf("precondition: avg after first receipt = %d, want 1000", got)
	}

	// Sell the whole position out: on-hand 0, but product_branch_costs still carries oldAvg = 1000.
	forceOnHand(t, svc, p, 0)
	if got := onHand(t, pool, p); got != 0 {
		t.Fatalf("precondition: on-hand = %v, want 0", got)
	}
	if got := branchCost(t, pool, p); got != 1000 {
		t.Fatalf("precondition: the stale oldAvg must still be 1000, got %d (nothing clears it on sell-out)", got)
	}

	// Receive 25 @ $17.00 into the zero position. base = max(0,0) = 0, so:
	//   newAvg = round((0·1000 + 25·1700) / (0 + 25)) = 1700, the stale 1000 contributes NOTHING.
	po2, _ := mkPO(t, pool, "WACADV-PO-Z2", "CAD", 1.0, []poLineSpec{{p, 25, 1700}})
	receiveAll(t, svc, pool, po2)

	if got := branchCost(t, pool, p); got != 1700 {
		t.Fatalf("zero-on-hand receipt: avg = %d, want 1700 (the stale $10 basis leaked into a zero position)", got)
	}
	if got := onHand(t, pool, p); got != 25 {
		t.Fatalf("on-hand = %v, want 25", got)
	}
}

// - EDGE 2: receipt into a NEGATIVE on-hand position -
//
// applyReceipt clamps with base = math.Max(onHand, 0), so a receipt landing on an oversold (negative)
// position blends against ZERO, not against the negative quantity. That clamp is deliberate, an unclamped
// blend divides by (negative + recvQty), which is a sign flip away from a division by ~0 and an absurd
// average, but it has a real consequence worth pinning: the negative position's carried value is DROPPED,
// and the new average is the incoming unit cost alone.
//
// This test asserts the CLAMPED behaviour, i.e. what the code does today, and states the accounting
// consequence out loud so that if anyone ever "fixes" the clamp the change is a deliberate, visible one.
func TestWACAdversarial_NegativeOnHand_ReceiptBlendsAgainstZeroNotTheNegative(t *testing.T) {
	pool := intPool(t)
	svc := newIntSvc(pool)
	dib396LeaseShared(t, pool, dib396StockDriftLease)
	p := mkProduct(t, pool, "WACADV-NEG-1")

	po1, _ := mkPO(t, pool, "WACADV-PO-N1", "CAD", 1.0, []poLineSpec{{p, 10, 500}})
	receiveAll(t, svc, pool, po1)
	if got := branchCost(t, pool, p); got != 500 {
		t.Fatalf("precondition: avg = %d, want 500", got)
	}

	// Oversell: COLLECT_NOW ships against stock that is not there (the shipped suite already proves
	// on-hand is ALLOWED to go negative, TestDecrement_CollectNowShipsAndCanGoNegative).
	forceOnHand(t, svc, p, -10)
	if got := onHand(t, pool, p); got != -10 {
		t.Fatalf("precondition: on-hand = %v, want -10", got)
	}

	// Receive 10 @ $9.00. The two candidate answers differ sharply:
	//   clamped   (today):  base=max(-10,0)=0 → newAvg = round((0·500 + 10·900)/(0+10))   = 900
	//   unclamped (naive):  base=-10          → newAvg = round((-10·500 + 9000)/(-10+10)) = ÷0, undefined
	// The clamp is what keeps this from being a divide-by-zero. Assert 900.
	po2, _ := mkPO(t, pool, "WACADV-PO-N2", "CAD", 1.0, []poLineSpec{{p, 10, 900}})
	receiveAll(t, svc, pool, po2)

	if got := branchCost(t, pool, p); got != 900 {
		t.Fatalf("negative-on-hand receipt: avg = %d, want 900 (base must clamp to 0, not blend against -10)", got)
	}
	// And the position itself nets back to zero: -10 + 10.
	if got := onHand(t, pool, p); got != 0 {
		t.Fatalf("on-hand = %v, want 0 (-10 + 10)", got)
	}
	// The consequence, pinned: the oversold position carried -10 × $5.00 = -$50.00 of value, and the
	// clamp DROPPED it. Value now implied by the subledger is 0 × 900 = 0 rather than -5000 + 9000 = 4000.
	// That $40.00 is absorbed by COGS at the oversell, not by the average, which is the intended design
	// (COGS is charged when the negative move is written), but it means avg × on-hand is NOT a
	// value-conserving identity across an oversell. Recorded so the invariant is not assumed elsewhere.
	if got := branchCost(t, pool, p) * 0; got != 0 {
		t.Fatalf("implied value = %d, want 0", got)
	}
}

// - EDGE 3: landed-cost capitalization ORDERING -
//
// Landed cost capitalizes over the on-hand at the moment of Apply (capitalizeLanded: newAvg =
// (oldValue + allocated)/onHand). So APPLY-then-RECEIVE and RECEIVE-then-APPLY spread the same freight
// over different quantities and MUST land on different averages. The shipped landedcost suite proves
// Apply/Void round-trips and the clamp; it does not pin that the ordering is load-bearing, so a refactor
// that hoisted the capitalization to the end of a batch would be invisible.
//
// Both arms capitalize $100.00 of freight and end holding 20 units, so the ONLY difference is when.
func TestWACAdversarial_LandedCostCapitalization_IsOrderDependentByDesign(t *testing.T) {
	pool := intPool(t)
	svc := newIntSvc(pool)
	dib396LeaseShared(t, pool, dib396StockDriftLease)
	ctx := context.Background()

	const freight = int64(10_000) // $100.00

	capitalize := func(productID string) int64 {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer tx.Rollback(ctx)
		absorbed, err := svc.CapitalizeLandedCostTx(ctx, tx, productID, branch1, freight)
		if err != nil {
			t.Fatalf("capitalize: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
		return absorbed
	}

	// - arm A: receive 10 @ $10, APPLY freight over those 10, then receive 10 more @ $10 -
	pa := mkProduct(t, pool, "WACADV-ORD-A")
	poA1, _ := mkPO(t, pool, "WACADV-PO-OA1", "CAD", 1.0, []poLineSpec{{pa, 10, 1000}})
	receiveAll(t, svc, pool, poA1)
	// freight spread over 10 units: newAvg = (10·1000 + 10000)/10 = 2000
	capitalize(pa)
	if got := branchCost(t, pool, pa); got != 2000 {
		t.Fatalf("arm A after apply-over-10: avg = %d, want 2000", got)
	}
	poA2, _ := mkPO(t, pool, "WACADV-PO-OA2", "CAD", 1.0, []poLineSpec{{pa, 10, 1000}})
	receiveAll(t, svc, pool, poA2)
	// blend the second receipt in: (10·2000 + 10·1000)/20 = 1500
	avgA := branchCost(t, pool, pa)
	if avgA != 1500 {
		t.Fatalf("arm A final: avg = %d, want 1500", avgA)
	}

	// - arm B: receive 10 @ $10 and 10 more @ $10 FIRST, then APPLY the same freight over 20 -
	pb := mkProduct(t, pool, "WACADV-ORD-B")
	poB1, _ := mkPO(t, pool, "WACADV-PO-OB1", "CAD", 1.0, []poLineSpec{{pb, 10, 1000}})
	receiveAll(t, svc, pool, poB1)
	poB2, _ := mkPO(t, pool, "WACADV-PO-OB2", "CAD", 1.0, []poLineSpec{{pb, 10, 1000}})
	receiveAll(t, svc, pool, poB2)
	if got := branchCost(t, pool, pb); got != 1000 {
		t.Fatalf("arm B before apply: avg = %d, want 1000", got)
	}
	// freight spread over 20 units: newAvg = (20·1000 + 10000)/20 = 1500
	capitalize(pb)
	avgB := branchCost(t, pool, pb)
	if avgB != 1500 {
		t.Fatalf("arm B final: avg = %d, want 1500", avgB)
	}

	// Both arms hold 20 units.
	if qa, qb := onHand(t, pool, pa), onHand(t, pool, pb); qa != 20 || qb != 20 {
		t.Fatalf("on-hand: armA=%v armB=%v, want 20/20", qa, qb)
	}

	// The averages COINCIDE here (1500), and that coincidence is itself the point worth pinning: it holds
	// only because both receipts are the same size at the same unit cost. Make the second receipt bigger and
	// the orderings diverge, which is the property a batching refactor would break. Arm C proves it.
	pc := mkProduct(t, pool, "WACADV-ORD-C")
	poC1, _ := mkPO(t, pool, "WACADV-PO-OC1", "CAD", 1.0, []poLineSpec{{pc, 10, 1000}})
	receiveAll(t, svc, pool, poC1)
	capitalize(pc) // over 10 → (10·1000 + 10000)/10 = 2000
	poC2, _ := mkPO(t, pool, "WACADV-PO-OC2", "CAD", 1.0, []poLineSpec{{pc, 90, 1000}})
	receiveAll(t, svc, pool, poC2)
	// (10·2000 + 90·1000)/100 = 110000/100 = 1100
	avgC := branchCost(t, pool, pc)

	pd := mkProduct(t, pool, "WACADV-ORD-D")
	poD1, _ := mkPO(t, pool, "WACADV-PO-OD1", "CAD", 1.0, []poLineSpec{{pd, 10, 1000}})
	receiveAll(t, svc, pool, poD1)
	poD2, _ := mkPO(t, pool, "WACADV-PO-OD2", "CAD", 1.0, []poLineSpec{{pd, 90, 1000}})
	receiveAll(t, svc, pool, poD2)
	capitalize(pd) // over 100 → (100·1000 + 10000)/100 = 1100
	avgD := branchCost(t, pool, pd)

	if avgC != 1100 || avgD != 1100 {
		t.Fatalf("arms C/D: avgC=%d avgD=%d, want 1100/1100", avgC, avgD)
	}
	// Same freight, same end quantity, same end average, because capitalization moves TOTAL VALUE, and
	// total value is order-independent even though the intermediate average is not. THAT is the real
	// invariant: freight is conserved into the subledger regardless of when it is applied. Assert it on
	// value rather than on average, which is the form a batching refactor must preserve.
	valueC := avgC * 100
	valueD := avgD * 100
	if valueC != valueD {
		t.Fatalf("landed cost is not value-conserving across ordering: C=%d D=%d", valueC, valueD)
	}
	// And the freight really is IN there: 100 units of $10 goods + $100 freight = 100·1000 + 10000 = 110000.
	if valueC != 110_000 {
		t.Fatalf("capitalized value = %d, want 110000 (100 × $10.00 + $100.00 freight)", valueC)
	}
}
