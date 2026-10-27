//go:build integration

// nsf_void_exclusion_integration_test.go, FW1-06b: the bounce and the void exclude each other, both
// ways. The void refuses a bounced receipt (the bounce owns that undo); this leg is the other
// direction: a voided desk receipt, stamped VOID by the payments module's void act, is refused the
// bounce. A bounce reinstates the payment's invoices and books a returned item, and a receipt that
// never happened has nothing to give back. The guard lives in the store's locked read, mirrored by
// the service's fast check, the same shape as the reversal exclusion.

package till

import (
	"context"
	"strings"
	"testing"
)

func TestIntegration_NSF_AVoidedReceiptCannotBeBounced(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	svc := realSvc(pool)
	cctx := globalCtx(userID)

	pid, invID, _, cleanup := seedNSFCase(t, pool, branchID, 50000)
	t.Cleanup(cleanup)

	if _, err := pool.Exec(ctx,
		`UPDATE payments SET status = 'VOID', metadata = '{"void_reason":"keyed twice"}'::jsonb WHERE id = $1`, pid); err != nil {
		t.Fatalf("stamp the receipt VOID: %v", err)
	}

	_, err := svc.MarkNSF(cctx, NSFInput{PaymentID: pid, Reason: "bounce a voided receipt"})
	if err == nil || !strings.Contains(err.Error(), "only a received or deposited payment") {
		t.Fatalf("bouncing a voided receipt answers %v, want the not bounceable refusal", err)
	}

	// Nothing was written: the receipt is still VOID, the invoice is still settled, no NSF row exists.
	var status, invStatus string
	var nsfRows int
	if err := pool.QueryRow(ctx, `SELECT status FROM payments WHERE id = $1`, pid).Scan(&status); err != nil {
		t.Fatalf("read the payment: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM invoices WHERE id = $1`, invID).Scan(&invStatus); err != nil {
		t.Fatalf("read the invoice: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_nsf WHERE payment_id = $1`, pid).Scan(&nsfRows); err != nil {
		t.Fatalf("read the nsf rows: %v", err)
	}
	if status != "VOID" || invStatus != "PAID" || nsfRows != 0 {
		t.Fatalf("the refused bounce wrote: payment %s, invoice %s, nsf rows %d", status, invStatus, nsfRows)
	}
}
