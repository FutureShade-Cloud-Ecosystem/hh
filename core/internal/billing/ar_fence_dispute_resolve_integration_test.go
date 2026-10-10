//go:build integration

// ar_fence_dispute_resolve_integration_test.go, the security review's P1: resolving a dispute is a per invoice act and goes through the branch door.
package billing

import (
	"context"
	"errors"
	"testing"
)

// P1: resolving a dispute is a per invoice act and goes through the same door.
func TestARFenceReview_ResolveDisputeIsNotFoundOutsideTheReach(t *testing.T) {
	w := newARFenceWorld(t)
	svc := w.registerService(arTrentonOnly())
	t.Cleanup(func() {
		_, _ = w.pool.Exec(context.Background(), `DELETE FROM ar_invoice_disputes WHERE customer_id IN ($1::uuid, $2::uuid)`, w.dual, w.kingOnly)
	})
	if _, _, err := svc.OpenDispute(arGlobalPayRec(), DisputeInput{
		InvoiceID: w.dualKingston, Reason: "OTHER", AmountCents: 1000, IdempotencyKey: "arf-d-" + randomTag()}); err != nil {
		t.Fatalf("a global caller opens the dispute: %v", err)
	}
	if resolved, err := svc.ResolveDispute(arTrentonOnly(), w.dualKingston, "WITHDRAWN"); !errors.Is(err, ErrNotFound) || resolved {
		t.Fatalf("a Trenton caller resolving a Kingston invoice's dispute must be not found, got resolved=%v err=%v", resolved, err)
	}
	// Not an oracle: an invoice without a dispute in the other branch answers the same.
	if resolved, err := svc.ResolveDispute(arTrentonOnly(), w.kingOnlyInv, "WITHDRAWN"); !errors.Is(err, ErrNotFound) || resolved {
		t.Fatalf("no dispute on a Kingston invoice must answer the same, got resolved=%v err=%v", resolved, err)
	}
	if resolved, err := svc.ResolveDispute(arGlobalPayRec(), w.dualKingston, "WITHDRAWN"); err != nil || !resolved {
		t.Fatalf("a global caller resolves it: resolved=%v err=%v", resolved, err)
	}
}
