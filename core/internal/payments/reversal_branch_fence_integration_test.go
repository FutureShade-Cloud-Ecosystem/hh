//go:build integration

// reversal_branch_fence_integration_test.go: a payment reverse is a money act on one payment, and a
// caller whose branches do not include the payment's branch finds no such payment (the same answer as a
// missing id, no existence oracle), and nothing is posted.
package payments

import (
	"context"
	"errors"
	"testing"
)

func TestIntegration_Reversal_ANotReachedBranchFindsNoSuchPayment(t *testing.T) {
	ctx := context.Background()
	rig := newPayRig(t, NewStubGateway())
	cleanupIntents(t, rig)
	pay, err := rig.svc.Pay(ctx, rig.customerID, PayRequest{
		InvoiceIDs: []string{rig.invoiceA, rig.invoiceB}, PaymentMethodID: rig.methodID, ExpectedCents: 55000,
	})
	if err != nil {
		t.Fatalf("seed payment: %v", err)
	}
	svc := NewReverseService(NewRepository(rig.pool))

	_, err = svc.Reverse(ctx, ReverseInput{PaymentID: pay.ID, Reason: "wrong branch", IsManager: true,
		CanAccessBranch: func(string) bool { return false }})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("a caller who does not reach the payment's branch must find no such payment, got %v", err)
	}
	var reversed bool
	_ = rig.pool.QueryRow(ctx, `SELECT reversed_at IS NOT NULL FROM payments WHERE id = $1::uuid`, pay.ID).Scan(&reversed)
	if reversed {
		t.Fatalf("the refused reverse still reversed the payment")
	}

	if _, err := svc.Reverse(ctx, ReverseInput{PaymentID: pay.ID, Reason: "right branch", IsManager: true,
		CanAccessBranch: func(string) bool { return true }}); err != nil {
		t.Fatalf("a caller who reaches the branch reverses it: %v", err)
	}
}
