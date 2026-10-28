//go:build integration

package till

// nsf_at_now_integration_test.go, the bounce's own leg for S6's R2-P1-1 (ruled, FROM-PRINCIPAL
// 174): after the real bounce, the as of figures at now read the invoice's own balance_cents, the
// inception to date figure. The main leg, beside every other writer, is billing's
// ar_aging_at_now_integration_test.go; the bounce is here because the service's caller wiring is
// package private, and its at now figure carries the fee's own ADJUSTMENT row beside the balance.
//
// W2-6A's own leg sits here too, because it reads the same at now figures and this file already
// reads the one real clock: a test file that pins an instant and meets a real clock is the house's
// dated time bomb (internal/testaudit), so the pinned as-of legs live in
// alloc_reversal_rows_integration_test.go and the at now legs live here.

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
	var feeCents int64
	if err := pool.QueryRow(ctx, `SELECT fee_cents FROM payment_nsf WHERE payment_id = $1::uuid`, pid).Scan(&feeCents); err != nil {
		t.Fatalf("read the fee: %v", err)
	}
	if got != want+feeCents {
		t.Fatalf("the as of aging at now reads %d, want the balance sum %d plus the fee %d (at now the figure is the balance)", got, want, feeCents)
	}
	after, err := brepo.ARBalanceCents(ctx, now, "")
	if err != nil {
		t.Fatalf("read the tie figure: %v", err)
	}
	if after-before != want+feeCents {
		t.Fatalf("the 1100 tie figure at now moved %d, want the balance sum %d plus the fee %d", after-before, want, feeCents)
	}
}

// W2-6A: today's figures do not move. The bounce's money figures are the figures it moved before the
// allocation rows gained a kind (0450): the invoice is open again at its full balance, the account ages
// that balance at now, the register's applied figure is zero (the cheque pays nothing now) and the
// payment's own allocations net to zero.
func TestIntegration_W2_6A_TodaysFiguresDoNotMoveAcrossTheBounce(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	svc := realSvc(pool)
	cctx := globalCtx(userID)

	pid, invID, custID, cleanup := seedNSFCase(t, pool, branchID, 50000)
	t.Cleanup(cleanup)

	if _, err := svc.MarkNSF(cctx, NSFInput{PaymentID: pid, Reason: "cheque returned NSF"}); err != nil {
		t.Fatalf("bounce: %v", err)
	}

	// The invoice is open again at its full balance.
	var bal int64
	var invStatus string
	if err := pool.QueryRow(ctx, `SELECT balance_cents, status FROM invoices WHERE id = $1::uuid`, invID).Scan(&bal, &invStatus); err != nil {
		t.Fatalf("read the invoice: %v", err)
	}
	if bal != 50000 || invStatus != "FINALIZED" {
		t.Fatalf("the invoice reads %s/%d after the bounce, want FINALIZED/50000", invStatus, bal)
	}

	// The aging at now reads the balance the bounce restored, plus the fee the policy charged.
	brepo := billing.NewRepository(pool)
	now := time.Now()
	rows, err := brepo.AgingRowsIn(ctx, now, 60, "", billing.AllBranches)
	if err != nil {
		t.Fatalf("read the aging at now: %v", err)
	}
	var aged int64
	for _, r := range rows {
		if r.CustomerID == custID {
			aged = r.TotalCents
		}
	}
	var feeCents int64
	if err := pool.QueryRow(ctx, `SELECT fee_cents FROM payment_nsf WHERE payment_id = $1::uuid`, pid).Scan(&feeCents); err != nil {
		t.Fatalf("read the fee: %v", err)
	}
	if aged != 50000+feeCents {
		t.Fatalf("the account ages %d at now, want %d (the invoice's restored balance plus the fee)", aged, 50000+feeCents)
	}

	// The register's applied figure: nothing is applied to the invoice now, exactly as before, when
	// the bounce's rows were gone.
	applied, err := brepo.AppliedCents(ctx, invID)
	if err != nil {
		t.Fatalf("read the applied cents: %v", err)
	}
	if applied != 0 {
		t.Fatalf("the register reads %d applied on the invoice, want 0: the cheque pays nothing now", applied)
	}

	// The payment's own allocations net to zero.
	var allocNet int64
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount_cents), 0) FROM payment_allocations WHERE payment_id = $1::uuid`, pid).Scan(&allocNet); err != nil {
		t.Fatalf("read the allocation net: %v", err)
	}
	if allocNet != 0 {
		t.Fatalf("the bounced payment's allocations net %d, want 0", allocNet)
	}

	// The same line in one place, so the branch and the base it replaces can be quoted side by side:
	// this leg was run unchanged on the branch base (92ff33c, a separate database migrated without
	// 0450) and printed the same figures, so nothing about today's money moved.
	t.Logf("FIGURES invoice_status=%s invoice_balance=%d aging_at_now=%d applied=%d allocation_net=%d nsf_fee=%d",
		invStatus, bal, aged, applied, allocNet, feeCents)
}
