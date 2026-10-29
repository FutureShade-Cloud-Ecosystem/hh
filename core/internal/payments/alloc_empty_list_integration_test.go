//go:build integration

// alloc_empty_list_integration_test.go: the payments sync feed answers an empty JSON
// array for a payment's allocations, never null. An unapplied receipt carries no
// allocation rows, and the sync scope's projection marshals the payments model
// through its json tags, so a nil allocations slice reached the feed as
// "allocations": null: the read that owns the empty answer must seed the empty
// slice for every payment it was asked about.
package payments

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// TestIntegration_UnappliedPaymentSyncFeedAnswersAllocationsArray plants one RECEIVED
// payment with no allocation rows (an unapplied receipt, the shape a deposit batch
// leaves) and pulls the payments scope: the entity's allocations must marshal as [],
// never null, because a nil Go slice marshals as JSON null.
func TestIntegration_UnappliedPaymentSyncFeedAnswersAllocationsArray(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)

	var branchID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type='BRANCH' LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no seeded BRANCH: %v", err)
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var customerID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ('ALLOC-EMPTY-'||$1, 'Alloc Empty '||$1, 'CONTRACTOR', $2::uuid, 0) RETURNING id::text`,
		suffix, branchID).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	var paymentID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, branch_id)
		 VALUES ('ALLOC-P-'||$1, $2::uuid, 'CHEQUE', 25000, 'RECEIVED', $3::uuid) RETURNING id::text`,
		suffix, customerID, branchID).Scan(&paymentID); err != nil {
		t.Fatalf("seed unapplied payment: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM payment_allocations WHERE payment_id = $1::uuid`, paymentID)
		_, _ = pool.Exec(c, `DELETE FROM payments WHERE id = $1::uuid`, paymentID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, customerID)
	})

	svc := NewService(NewRepository(pool), NewStubGateway())
	store := NewPaymentsSyncStore(pool, svc)
	items, _, err := store.Pull(ctx, sync.ScopeFilter{TenantID: customerID, Tenant: true, Actor: "alloc-empty"}, 0, 200)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	for _, it := range items {
		if it.ID != paymentID {
			continue
		}
		raw, err := json.Marshal(it.Data["allocations"])
		if err != nil {
			t.Fatalf("marshal allocations: %v", err)
		}
		if got := string(raw); got != "[]" {
			t.Fatalf("the sync feed's allocations on an unapplied payment: allocations is %s, want the empty array [] not null", got)
		}
		return
	}
	t.Fatalf("payment %s not in the pull's page", paymentID)
}
