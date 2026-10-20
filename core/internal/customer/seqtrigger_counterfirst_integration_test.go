//go:build integration

// seqtrigger_counterfirst_integration_test.go: the loader table leg for customers (PR 556, round 3e). See the
// products leg (product/seqtrigger_counterfirst_integration_test.go) for the cycle and the method: the REST
// writer (the real Repository.UpdateBillingAddress) is parked through testkit.DrawPark, the batch is a
// transaction that takes the customers counter with the load door's statement and then wants the customer row.
// Red at 55593fff with 40P01; green once the writer takes the counter first (LockCustomersSeqTx).
package customer

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestIntegration_CounterFirst_CustomerEditBesideALoadBatch(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	acct := fmt.Sprintf("CF-%d", time.Now().UnixNano())
	var id string
	if err := pool.QueryRow(ctx, `INSERT INTO customers (account_no, name, customer_type) VALUES ($1, $2, 'CONTRACTOR') RETURNING id::text`, acct, acct).Scan(&id); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1::uuid`, id) })

	park := testkit.NewDrawPark(t, pool, "customers", 55010102)
	edit := make(chan error, 1)
	go func() {
		_, err := NewRepository(park.Pool).UpdateBillingAddress(ctx, id, map[string]any{"city": "Counter First"})
		edit <- err
	}()
	park.WaitParked("the edit stands at its first touch of the customers counter")

	var settled atomic.Bool
	batch := make(chan error, 1)
	go func() {
		defer settled.Store(true)
		tx, err := pool.Begin(ctx)
		if err != nil {
			batch <- err
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, `UPDATE scope_sequences SET next_value = next_value WHERE scope = 'customers'`); err != nil {
			batch <- err
			return
		}
		if _, err := tx.Exec(ctx, `SELECT 1 FROM customers WHERE id = $1::uuid FOR NO KEY UPDATE`, id); err != nil {
			batch <- err
			return
		}
		batch <- tx.Commit(ctx)
	}()
	park.WaitBlockedOr("the batch waits for the customer row", settled.Load)

	park.Answers(map[string]chan error{"edit": edit, "batch": batch})
}
