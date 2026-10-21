//go:build integration

// seqtrigger_counterfirst_integration_test.go: the loader table leg for vendors (PR 556, round 3e). See the
// products leg (product/seqtrigger_counterfirst_integration_test.go) for the cycle and the method: the REST
// writer (the real Repository.UpdateVendorMaster) is parked through testkit.DrawPark, the batch is a transaction
// that takes the vendors counter with the load door's statement and then wants the vendor row. Red at 55593fff
// with 40P01; green once the writer takes the counter first (LockVendorsSeqTx).
package purchasing

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestIntegration_CounterFirst_VendorEditBesideALoadBatch(t *testing.T) {
	h := newHarness(t)
	vendorMasterCleanup(t, h)
	org := h.orgID(t)
	ctx := context.Background()
	v, err := h.svc.CreateVendor(h.ctx, org, CreateVendorRequest{Name: "Counter First Supply (demo)"})
	if err != nil {
		t.Fatalf("create vendor: %v", err)
	}

	park := testkit.NewDrawPark(t, h.pool, "vendors", 55010103)
	name := "Counter First Supply Ltd (demo)"
	edit := make(chan error, 1)
	go func() {
		_, err := NewRepository(park.Pool).UpdateVendorMaster(ctx, org, v.ID, &name, nil, nil, nil)
		edit <- err
	}()
	park.WaitParked("the edit stands at its first touch of the vendors counter")

	var settled atomic.Bool
	batch := make(chan error, 1)
	go func() {
		defer settled.Store(true)
		tx, err := h.pool.Begin(ctx)
		if err != nil {
			batch <- err
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, `UPDATE scope_sequences SET next_value = next_value WHERE scope = 'vendors'`); err != nil {
			batch <- err
			return
		}
		if _, err := tx.Exec(ctx, `SELECT 1 FROM vendors WHERE id = $1::uuid FOR NO KEY UPDATE`, v.ID); err != nil {
			batch <- err
			return
		}
		batch <- tx.Commit(ctx)
	}()
	park.WaitBlockedOr("the batch waits for the vendor row", settled.Load)

	park.Answers(map[string]chan error{"edit": edit, "batch": batch})
}
