//go:build integration

package till

// nsf_at_now_integration_test.go, the bounce's own leg for S6's R2-P1-1 (ruled, FROM-PRINCIPAL
// 174): after the real bounce, the as of figures at now read the invoice's own balance_cents, the
// inception to date figure. The main leg, beside every other writer, is billing's
// ar_aging_at_now_integration_test.go; the bounce is here because the service's caller wiring is
// package private, and its at now figure carries the fee's own ADJUSTMENT row beside the balance.

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
)

func TestIntegration_NSF_TheAgingAtNowReadsTheBalanceTheBounceMoved(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	svc := realSvc(pool)
	cctx := globalCtx(userID)

	pid, _, custID, cleanup := seedNSFCase(t, pool, branchID, 50000)
	t.Cleanup(cleanup)

	brepo := billing.NewRepository(pool)
	before, err := brepo.ARBalanceCents(ctx, time.Now(), "")
	if err != nil {
		t.Fatalf("read the tie figure: %v", err)
	}

	if _, err := svc.MarkNSF(cctx, NSFInput{PaymentID: pid, Reason: "the bounce writer's own at now leg"}); err != nil {
		t.Fatalf("bounce: %v", err)
	}

	now := time.Now()
	var want int64
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(balance_cents), 0) FROM invoices
		  WHERE customer_id = $1::uuid AND status IN ('FINALIZED','PARTIALLY_PAID','PAID')`, custID).Scan(&want); err != nil {
		t.Fatalf("read the balance sum: %v", err)
	}
	rows, err := brepo.AgingRowsIn(ctx, now, 60, "", billing.AllBranches)
	if err != nil {
		t.Fatalf("read the aging: %v", err)
	}
	var got int64
	for _, r := range rows {
		if r.CustomerID == custID {
			got = r.TotalCents
		}
	}
	if got != want {
		t.Fatalf("the as of aging at now reads %d, want the balance sum %d (at now the figure is the balance)", got, want)
	}
	// The tie figure carries the balance the bounce moved and the fee's own ADJUSTMENT row beside it.
	var feeCents int64
	if err := pool.QueryRow(ctx, `SELECT fee_cents FROM payment_nsf WHERE payment_id = $1::uuid`, pid).Scan(&feeCents); err != nil {
		t.Fatalf("read the fee: %v", err)
	}
	after, err := brepo.ARBalanceCents(ctx, now, "")
	if err != nil {
		t.Fatalf("read the tie figure: %v", err)
	}
	if after-before != want+feeCents {
		t.Fatalf("the 1100 tie figure at now moved %d, want the balance sum %d plus the fee %d", after-before, want, feeCents)
	}
}
