//go:build integration

package till

// alloc_reversal_rows_integration_test.go, W2-6A: the bounce keeps the allocations it undoes.
//
// A bounce used to DELETE its payment's allocation rows. The payment's split across invoices died
// with them, and an as-of aging rebuilt from the dated acts found no allocation at all for a cheque
// that was good at the as-of date, so the invoice aged at a date it had been paid.
//
// Migration 0450 gives the allocation row a kind. The application stays where it is; the undo is
// written beside it as a REVERSAL row: the same payment, the same invoice, the exact negative of the
// application, dated at the bounce's own instant, naming the application it reverses.
//
// This file is the bounce's own leg: the rows survive the bounce, and the as-of aging reads the
// history the bounce left behind.
//
// Run: make reset && go test -tags integration ./internal/till/...
import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
)

func TestIntegration_W2_6A_TheBounceKeepsTheAllocationsItUndoes(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	svc := realSvc(pool)
	cctx := globalCtx(userID)

	pid, invID, _, cleanup := seedNSFCase(t, pool, branchID, 50000)
	t.Cleanup(cleanup)

	if _, err := svc.MarkNSF(cctx, NSFInput{PaymentID: pid, Reason: "cheque returned NSF"}); err != nil {
		t.Fatalf("bounce: %v", err)
	}

	// The application is still there, and the undo is written beside it as a reversal row: the same
	// payment and the same invoice, the exact negative, dated at the bounce's own instant, naming
	// the application it reverses.
	var appID, revID string
	var appAmt, revAmt int64
	var revKind string
	var revAt time.Time
	var revRef *string
	if err := pool.QueryRow(ctx,
		`SELECT a.id::text, a.amount_cents, r.id::text, r.kind, r.amount_cents, r.created_at,
		        r.reverses_allocation_id::text
		   FROM payment_allocations a
		   JOIN payment_allocations r
		     ON r.payment_id = a.payment_id AND r.invoice_id = a.invoice_id AND r.kind = 'REVERSAL'
		  WHERE a.payment_id = $1::uuid AND a.kind = 'APPLICATION'`, pid).
		Scan(&appID, &appAmt, &revID, &revKind, &revAmt, &revAt, &revRef); err != nil {
		t.Fatalf("read the allocation history: %v", err)
	}
	if appAmt != 50000 {
		t.Fatalf("the application row reads %d cents, want 50000", appAmt)
	}
	if revKind != "REVERSAL" {
		t.Fatalf("the undo row reads kind %q, want REVERSAL", revKind)
	}
	if revAmt != -50000 {
		t.Fatalf("the reversal row reads %d cents, want -50000 (the exact negative of the application)", revAmt)
	}
	if revRef == nil || *revRef != appID {
		t.Fatalf("the reversal row names %v, want the application row %s", revRef, appID)
	}

	// The bounce's own instant dates the undo: the row is the bounce, not the payment.
	var nsfAt time.Time
	if err := pool.QueryRow(ctx, `SELECT created_at FROM payment_nsf WHERE payment_id = $1::uuid`, pid).Scan(&nsfAt); err != nil {
		t.Fatalf("read the nsf record: %v", err)
	}
	if d := revAt.Sub(nsfAt); d > time.Second || d < -time.Second {
		t.Fatalf("the reversal row is dated %s and the bounce %s: the undo must carry the bounce's own instant", revAt, nsfAt)
	}

	// The invoice is open again at its full balance: the bounce's figures did not move.
	var bal int64
	var invStatus string
	if err := pool.QueryRow(ctx, `SELECT balance_cents, status FROM invoices WHERE id = $1::uuid`, invID).Scan(&bal, &invStatus); err != nil {
		t.Fatalf("read the invoice: %v", err)
	}
	if bal != 50000 || invStatus != "FINALIZED" {
		t.Fatalf("the invoice reads %s/%d after the bounce, want FINALIZED/50000", invStatus, bal)
	}
}

// The reason for the item: a cheque applied on day 1 and bounced on day 10 reads as PAID on day 5's
// as-of aging and OWED on day 10's. It read the other way round before the allocation rows gained a
// kind: the bounce deleted them, so the rebuild found no allocation for a cheque that was good at
// the as-of date and the invoice aged at a date it had been paid.
func TestIntegration_W2_6A_TheAsOfAgingReadsTheAllocationHistory(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)

	// One clock rules the bounce (FW1-13b), so the test dates the bounce itself.
	appliedOn := time.Date(1990, 1, 20, 12, 0, 0, 0, time.UTC)
	finalizedOn := time.Date(1990, 1, 15, 12, 0, 0, 0, time.UTC)
	bouncedOn := time.Date(1990, 7, 1, 12, 0, 0, 0, time.UTC)
	day5 := time.Date(1990, 6, 30, 12, 0, 0, 0, time.UTC)
	day10 := time.Date(1990, 7, 15, 12, 0, 0, 0, time.UTC)

	pid, invID, custID, cleanup := seedNSFCase(t, pool, branchID, 50000)
	t.Cleanup(cleanup)
	// The cheque was taken on day 1 and the invoice was on the books well before the as-of days.
	if _, err := pool.Exec(ctx,
		`UPDATE payment_allocations SET created_at = $2::timestamptz WHERE payment_id = $1::uuid`, pid, appliedOn); err != nil {
		t.Fatalf("date the application: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE invoices SET created_at = $2::timestamptz, finalized_at = $2::timestamptz,
		        issue_date = $3::date, due_date = $4::date
		  WHERE id = $1::uuid`, invID, finalizedOn, finalizedOn.Format("2006-01-02"), "1990-02-14"); err != nil {
		t.Fatalf("date the invoice: %v", err)
	}

	brepo := billing.NewRepository(pool)
	base5, err := brepo.ARBalanceCents(ctx, day5, "")
	if err != nil {
		t.Fatalf("the tie figure at day 5: %v", err)
	}
	base10, err := brepo.ARBalanceCents(ctx, day10, "")
	if err != nil {
		t.Fatalf("the tie figure at day 10: %v", err)
	}
	base5Row := agingRowFor(t, brepo, custID, day5)
	base10Row := agingRowFor(t, brepo, custID, day10)

	svc := realSvc(pool).WithClock(func() time.Time { return bouncedOn })
	if _, err := svc.MarkNSF(globalCtx(userID), NSFInput{PaymentID: pid, Reason: "cheque returned NSF"}); err != nil {
		t.Fatalf("bounce: %v", err)
	}

	// Day 5: the cheque was good on that day, so the invoice does not age. The bounce happened after
	// it and must not rewrite that day.
	after5, err := brepo.ARBalanceCents(ctx, day5, "")
	if err != nil {
		t.Fatalf("the tie figure at day 5: %v", err)
	}
	if after5-base5 != 0 {
		t.Fatalf("the tie figure at day 5 moved %d across the bounce, want 0: the cheque was good on day 5 and the bounce happened on day 10", after5-base5)
	}
	if got := agingRowFor(t, brepo, custID, day5); got != base5Row {
		t.Fatalf("the account's aging at day 5 reads %d across the bounce, want %d: the cheque was good on day 5", got, base5Row)
	}
	if got := agingRowFor(t, brepo, custID, day5); got != 0 {
		t.Fatalf("the account ages %d at day 5, want 0: the cheque was good on day 5", got)
	}

	// Day 10: the bounce is inside the as-of instant, so the invoice is owed again.
	after10, err := brepo.ARBalanceCents(ctx, day10, "")
	if err != nil {
		t.Fatalf("the tie figure at day 10: %v", err)
	}
	if after10-base10 != 50000 {
		t.Fatalf("the tie figure at day 10 moved %d across the bounce, want 50000: the cheque was taken back on day 10 and the invoice is owed again", after10-base10)
	}
	if got := agingRowFor(t, brepo, custID, day10); got != 50000 {
		t.Fatalf("the account ages %d at day 10, want 50000: the cheque bounced on day 10 and the invoice is owed again", got)
	}
	if base10Row != 0 {
		t.Fatalf("the account aged %d at day 10 BEFORE the bounce, want 0: the cheque was good on day 10 too, until the bounce", base10Row)
	}
}

// agingRowFor is the account's aging total at an instant, or zero when the account does not age at
// all (a settled document drops out of the aging entirely).
func agingRowFor(t *testing.T, repo *billing.Repository, customerID string, asOf time.Time) int64 {
	t.Helper()
	rows, err := repo.AgingRows(context.Background(), asOf, 60, "")
	if err != nil {
		t.Fatalf("read the aging at %s: %v", asOf, err)
	}
	for _, r := range rows {
		if r.CustomerID == customerID {
			return r.TotalCents
		}
	}
	return 0
}
