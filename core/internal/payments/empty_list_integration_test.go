//go:build integration

// empty_list_integration_test.go: the portal-facing list reads answer an empty JSON
// array, never null. ListMethods and ListPayments start their slices nil and append
// only when rows exist, so a tenant with no methods and no payments marshaled null;
// the portal handler normalises both today, but a second caller would inherit the
// null. The read that owns the empty answer must be born the empty slice.
package payments

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// TestIntegration_EmptyAnswersMarshalEmptyArrays reads a fresh tenant's methods and
// payments: both lists must marshal as [], never null, because a nil Go slice
// marshals as JSON null.
func TestIntegration_EmptyAnswersMarshalEmptyArrays(t *testing.T) {
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
		 VALUES ('EMPTY-LIST-'||$1, 'Empty List '||$1, 'CONTRACTOR', $2::uuid, 0) RETURNING id::text`,
		suffix, branchID).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1::uuid`, customerID)
	})

	repo := NewRepository(pool)
	methods, err := repo.ListMethods(ctx, customerID)
	if err != nil {
		t.Fatalf("list methods: %v", err)
	}
	raw, err := json.Marshal(methods)
	if err != nil {
		t.Fatalf("marshal methods: %v", err)
	}
	if got := string(raw); got != "[]" {
		t.Fatalf("methods on a tenant with none: methods is %s, want the empty array [] not null", got)
	}

	payments, err := repo.ListPayments(ctx, customerID)
	if err != nil {
		t.Fatalf("list payments: %v", err)
	}
	raw, err = json.Marshal(payments)
	if err != nil {
		t.Fatalf("marshal payments: %v", err)
	}
	if got := string(raw); got != "[]" {
		t.Fatalf("payments on a tenant with none: payments is %s, want the empty array [] not null", got)
	}
}
