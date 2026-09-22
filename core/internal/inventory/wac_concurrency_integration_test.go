//go:build integration

// wac_concurrency_integration_test.go, L8 MONEY-CRITICAL concurrency regression (live DB half). Proves that a PO
// RECEIPT and a landed-cost APPLY on the SAME (product, branch), run in two CONCURRENT transactions, no longer
// lost-update the perpetual weighted-average cost: the LockBranchCost advisory lock (repository.go) serializes the
// read-modify-write so the final WAC equals a valid SEQUENTIAL result and the perpetual subledger value ties out to
// initialValue + receiptValue + absorbedFreight (no value leaked → GL 1200 stays tied to the subledger).
//
// Run:
//
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable' \
//	  go test -tags=integration ./internal/inventory/... -run TestWACLock -count=1 -race
//
// Against the PRE-FIX code (no advisory lock, READ COMMITTED) both transactions read the same old WAC and the
// second commit clobbers the first → the perpetual value identity below breaks and the test FAILS. With the lock,
// the second tx blocks until the first commits, reads the already-committed WAC, and the identity holds every run.
package inventory

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

func TestWACLock_ReceiptVsLandedCost_NoLostUpdate(t *testing.T) {
	pool := intPool(t)
	// DIB-396: 30 iterations each seed stock_balances directly (no matching stock_moves), so the
	// projection drifts for the whole run, declare the window (see dib396_lease_test.go).
	dib396LeaseShared(t, pool, dib396StockDriftLease)
	svc := newIntSvc(pool)
	ctx := context.Background()

	// Fixed, order-independent fixture: seed 100 units @ 1000¢ ($1000000 subledger). Receipt adds 100 units @ 2000¢
	// (+$200000). Landed-cost applies 5000¢ of freight over the on-hand, small enough to be fully absorbed under
	// EITHER interleaving, so the final perpetual value MUST be exactly initialValue + receiptValue + freight
	// regardless of which tx serializes first. A lost update drops one of the two contributions.
	const (
		startOnHand = 100.0
		startAvg    = int64(1000)
		recvQty     = 100.0
		recvUnit    = int64(2000)
		freight     = int64(5000)
		iterations  = 30
	)

	for i := 0; i < iterations; i++ {
		p := mkProduct(t, pool, "WACLOCK")
		if _, err := pool.Exec(ctx,
			`INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base) VALUES ($1,$2,$2,$3)`,
			p, branch1, startOnHand); err != nil {
			t.Fatalf("iter %d: seed on-hand: %v", i, err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents) VALUES ($1,$2,$3)`,
			p, branch1, startAvg); err != nil {
			t.Fatalf("iter %d: seed cost: %v", i, err)
		}
		po, _ := mkPO(t, pool, "WACLOCK-PO", "CAD", 1.0, []poLineSpec{{p, recvQty, recvUnit}})

		// Two transactions, released together, each mutating the same (p, branch1) WAC. The barrier makes both open
		// + reach the lock as close as possible so the pre-fix lost-update window is maximally exercised.
		var start sync.WaitGroup
		start.Add(1)
		var wg sync.WaitGroup
		wg.Add(2)
		errCh := make(chan error, 2)

		go func() { // PO receipt
			defer wg.Done()
			tx, err := pool.Begin(ctx)
			if err != nil {
				errCh <- fmt.Errorf("receipt begin: %w", err)
				return
			}
			defer tx.Rollback(ctx)
			start.Wait()
			if err := svc.ReceivePOTx(ctx, tx, po); err != nil {
				errCh <- fmt.Errorf("receipt: %w", err)
				return
			}
			errCh <- tx.Commit(ctx)
		}()

		go func() { // landed-cost apply (capitalize freight)
			defer wg.Done()
			tx, err := pool.Begin(ctx)
			if err != nil {
				errCh <- fmt.Errorf("apply begin: %w", err)
				return
			}
			defer tx.Rollback(ctx)
			start.Wait()
			if _, _, _, err := svc.capitalizeLandedSnap(ctx, tx, p, branch1, freight); err != nil {
				errCh <- fmt.Errorf("apply: %w", err)
				return
			}
			errCh <- tx.Commit(ctx)
		}()

		start.Done() // release both
		wg.Wait()
		close(errCh)
		for err := range errCh {
			// A serialization retry is acceptable in principle, but the advisory lock uses plain blocking (no
			// serializable retry), so both must succeed. A commit error here is a real failure.
			if err != nil {
				t.Fatalf("iter %d: concurrent tx failed: %v", i, err)
			}
		}

		// Perpetual subledger value MUST equal every contribution, no lost update leaked value.
		finalAvg := branchCost(t, pool, p)
		finalOH := onHand(t, pool, p)
		gotValue := round(float64(finalAvg) * finalOH)
		initValue := round(float64(startAvg) * startOnHand)
		receiptValue := round(recvQty * float64(recvUnit))
		wantValue := initValue + receiptValue + freight
		if gotValue != wantValue {
			t.Fatalf("iter %d: perpetual value = %d (avg %d × oh %.0f), want %d "+
				"(init %d + receipt %d + freight %d), LOST UPDATE: subledger diverged from GL 1200",
				i, gotValue, finalAvg, finalOH,
				wantValue, initValue, receiptValue, freight)
		}
		// On-hand must reflect the receipt exactly (independent of interleaving).
		if wantOH := startOnHand + recvQty; finalOH != wantOH {
			t.Fatalf("iter %d: on-hand = %.4f, want %.4f", i, finalOH, wantOH)
		}
		// And the final WAC must be a valid SEQUENTIAL result: receipt-then-capitalize OR capitalize-then-receipt.
		wantR2C := seqReceiptThenCapitalize(startOnHand, startAvg, recvQty, recvUnit, freight)
		wantC2R := seqCapitalizeThenReceipt(startOnHand, startAvg, recvQty, recvUnit, freight)
		if finalAvg != wantR2C && finalAvg != wantC2R {
			t.Fatalf("iter %d: final WAC %d is neither serial order (R→C=%d, C→R=%d), not serialized",
				i, finalAvg, wantR2C, wantC2R)
		}
	}
}

// seqReceiptThenCapitalize is the WAC when the receipt commits first, then the freight capitalizes over the grown on-hand.
func seqReceiptThenCapitalize(oh float64, avg int64, recvQty float64, recvUnit, freight int64) int64 {
	afterReceipt := round((oh*float64(avg) + recvQty*float64(recvUnit)) / (oh + recvQty))
	newOH := oh + recvQty
	oldVal := round(float64(afterReceipt) * newOH)
	return round(float64(oldVal+freight) / newOH)
}

// seqCapitalizeThenReceipt is the WAC when the freight capitalizes first (over the original on-hand), then the receipt blends in.
func seqCapitalizeThenReceipt(oh float64, avg int64, recvQty float64, recvUnit, freight int64) int64 {
	oldVal := round(float64(avg) * oh)
	afterCap := round(float64(oldVal+freight) / oh)
	return round((oh*float64(afterCap) + recvQty*float64(recvUnit)) / (oh + recvQty))
}
