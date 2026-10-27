//go:build integration

package billing

// ar_aging_at_now_integration_test.go, S6's R2-P1-1 (ruled, FROM-PRINCIPAL 174): at an as of on the
// CURRENT house day the as of figures read the invoice's own balance_cents, the inception to date
// figure; the history rebuild is for a PAST date only. A writer that moves balance_cents without
// leaving every row the rebuild reads, a future writer or a hand correction, must not split today's
// aging and the 1100 tie figure from the invoice's own balance. One leg per writer that moves the
// balance (the settle, the reversal, the void, the credit applied, the deposit close), plus one
// document moved by hand, the shape the assistant's adapters fixture plants. The bounce, the one
// writer this package cannot construct (the till service's caller wiring is package private), has
// its own leg beside the real bounce in till/nsf_at_now_integration_test.go.

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
)

// atNowBalanceSum is what the invoice rows say the customer owes: the figure the as of reads at now.
func atNowBalanceSum(t *testing.T, pool *pgxpool.Pool, cust string) int64 {
	t.Helper()
	var want int64
	if err := pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(balance_cents), 0) FROM invoices
		  WHERE customer_id = $1::uuid AND status IN ('FINALIZED','PARTIALLY_PAID','PAID')`, cust).
		Scan(&want); err != nil {
		t.Fatalf("read the balance sum: %v", err)
	}
	return want
}

// cleanupPaymentsOf removes the receipt rows a leg's writers left, so the shared gate database
// carries nothing of this test's payments beside its own cleaned invoices.
func cleanupPaymentsOf(t *testing.T, pool *pgxpool.Pool, cust string) {
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM payment_allocations a USING payments p WHERE a.payment_id = p.id AND p.customer_id = $1::uuid`, cust)
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, cust)
		_, _ = pool.Exec(c, `DELETE FROM payments WHERE customer_id = $1::uuid`, cust)
	})
}

// assertAtNowIsTheBalance pins one planted shape: the aging's own read at now and the 1100 tie
// figure's move both equal the balance sum, whatever the history rebuild would say.
func assertAtNowIsTheBalance(t *testing.T, pool *pgxpool.Pool, before int64, cust string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	repo := NewRepository(pool)
	want := atNowBalanceSum(t, pool, cust)
	if got := agingTotalForCustomer(t, pool, now, cust); got != want {
		t.Fatalf("the as of aging at now reads %d, want the balance sum %d (at now the figure is the balance)", got, want)
	}
	after, err := repo.ARBalanceCents(ctx, now, "")
	if err != nil {
		t.Fatalf("read the tie figure at now: %v", err)
	}
	if after-before != want {
		t.Fatalf("the 1100 tie figure at now moved %d, want the balance sum %d (at now the figure is the balance)", after-before, want)
	}
}

func TestIntegration_TheAgingAtNowReadsTheBalanceForEachWriter(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	repo := NewRepository(pool)
	payRepo := payments.NewRepository(pool)

	// The settle: a receipt applied to an open invoice moves the balance and writes its allocation.
	before, err := repo.ARBalanceCents(ctx, time.Now(), "")
	if err != nil {
		t.Fatalf("read the tie figure: %v", err)
	}
	cust := seedCustomer(t, pool, ns(t))
	inv := seedInvoice(t, pool, cust, ns(t), invSpec{no: "SETTLE", status: "FINALIZED", subtotal: 50_000, tax: 0, total: 50_000, balance: 50_000, finalized: true})
	cleanupPaymentsOf(t, pool, cust)
	if _, err := payRepo.ApplyReceived(ctx, payments.ApplyRequest{
		CustomerID: cust, Method: "CHEQUE", Reference: "ATNOW-1", IdempotencyKey: "atnow-settle-" + cust,
		InvoiceIDs: []string{inv}, AmountCents: 20_000,
	}); err != nil {
		t.Fatalf("settle: %v", err)
	}
	assertAtNowIsTheBalance(t, pool, before, cust)

	// The reversal: the settled receipt reversed, the whole balance back on the books.
	before, err = repo.ARBalanceCents(ctx, time.Now(), "")
	if err != nil {
		t.Fatalf("read the tie figure: %v", err)
	}
	cust = seedCustomer(t, pool, ns(t))
	inv = seedInvoice(t, pool, cust, ns(t), invSpec{no: "REVERSAL", status: "FINALIZED", subtotal: 50_000, tax: 0, total: 50_000, balance: 50_000, finalized: true})
	cleanupPaymentsOf(t, pool, cust)
	if _, err := payRepo.ApplyReceived(ctx, payments.ApplyRequest{
		CustomerID: cust, Method: "CHEQUE", Reference: "ATNOW-2", IdempotencyKey: "atnow-reverse-" + cust,
		InvoiceIDs: []string{inv}, AmountCents: 50_000,
	}); err != nil {
		t.Fatalf("settle for the reversal: %v", err)
	}
	var payID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM payments WHERE customer_id = $1::uuid AND status = 'RECEIVED' LIMIT 1`, cust).Scan(&payID); err != nil {
		t.Fatalf("read the receipt: %v", err)
	}
	if _, err := payRepo.ReversePayment(ctx, payments.ReverseInput{
		PaymentID: payID, Reason: "the reversal writer's own leg", ActorID: "atnow", IsManager: true,
	}, time.Now()); err != nil {
		t.Fatalf("reverse: %v", err)
	}
	assertAtNowIsTheBalance(t, pool, before, cust)

	// The void: this PR's own writer, a same day receipt undone as never taken.
	before, err = repo.ARBalanceCents(ctx, time.Now(), "")
	if err != nil {
		t.Fatalf("read the tie figure: %v", err)
	}
	cust = seedCustomer(t, pool, ns(t))
	inv = seedInvoice(t, pool, cust, ns(t), invSpec{no: "VOID", status: "FINALIZED", subtotal: 50_000, tax: 0, total: 50_000, balance: 50_000, finalized: true})
	cleanupPaymentsOf(t, pool, cust)
	if _, err := payRepo.ApplyReceived(ctx, payments.ApplyRequest{
		CustomerID: cust, Method: "CHEQUE", Reference: "ATNOW-3", IdempotencyKey: "atnow-void-" + cust,
		InvoiceIDs: []string{inv}, AmountCents: 20_000,
	}); err != nil {
		t.Fatalf("settle for the void: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM payments WHERE customer_id = $1::uuid AND status = 'RECEIVED' LIMIT 1`, cust).Scan(&payID); err != nil {
		t.Fatalf("read the receipt: %v", err)
	}
	if _, err := payRepo.VoidPayment(ctx, payments.VoidInput{
		PaymentID: payID, Reason: "the void writer's own leg", ActorID: "atnow", IsManager: true,
	}, time.Now(), houseday.DefaultZone()); err != nil {
		t.Fatalf("void: %v", err)
	}
	assertAtNowIsTheBalance(t, pool, before, cust)

	// The credit applied: the counter return's writer moves the balance against a credit document.
	before, err = repo.ARBalanceCents(ctx, time.Now(), "")
	if err != nil {
		t.Fatalf("read the tie figure: %v", err)
	}
	cust = seedCustomer(t, pool, ns(t))
	inv = seedInvoice(t, pool, cust, ns(t), invSpec{no: "CREDIT", status: "FINALIZED", subtotal: 40_000, tax: 0, total: 40_000, balance: 40_000, finalized: true})
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := pos.NewRepository(pool).ApplyCreditToInvoice(ctx, tx, inv, 15_000); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("apply the credit: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		 VALUES ($1::uuid, 'CREDIT_MEMO', -15_000, 25_000, 'invoice', $2::uuid, $3::uuid, 'the applied credit')`,
		cust, inv, trentonBranch); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("write the credit's row: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	assertAtNowIsTheBalance(t, pool, before, cust)

	// The deposit close: a part paid supplier deposit cancelled under KEPT reads PAID at the paid part.
	before, err = repo.ARBalanceCents(ctx, time.Now(), "")
	if err != nil {
		t.Fatalf("read the tie figure: %v", err)
	}
	cust, orderID, _ := seedDepositOrderAt(t, pool, ns(t), 1_500)
	if _, err := NewInvoiceWriter(pool).CancelOrderDeposits(ctx, nil, orderID, "KEPT", "the deposit close writer's own leg"); err != nil {
		t.Fatalf("cancel the deposit: %v", err)
	}
	assertAtNowIsTheBalance(t, pool, before, cust)

	// Moved by hand: the adapters fixture's own shape, a document whose balance says one thing and
	// whose history says another. On or after the current house day (R3-P1-1, now and tomorrow
	// alike) the balance is the figure; the rebuild is for a past date only.
	tomorrow := time.Now().AddDate(0, 0, 1)
	before, err = repo.ARBalanceCents(ctx, time.Now(), "")
	if err != nil {
		t.Fatalf("read the tie figure: %v", err)
	}
	beforeTomorrow, err := repo.ARBalanceCents(ctx, tomorrow, "")
	if err != nil {
		t.Fatalf("read the tie figure at tomorrow: %v", err)
	}
	cust = seedCustomer(t, pool, ns(t))
	seedInvoice(t, pool, cust, ns(t), invSpec{no: "HAND", status: "FINALIZED", subtotal: 2_500, tax: 0, total: 2_500, balance: 2_500, finalized: true})
	if _, err := pool.Exec(ctx, `UPDATE invoices SET balance_cents = 0, status = 'PAID' WHERE customer_id = $1::uuid`, cust); err != nil {
		t.Fatalf("move the balance by hand: %v", err)
	}
	assertAtNowIsTheBalance(t, pool, before, cust)
	if got, want := agingTotalForCustomer(t, pool, tomorrow, cust), atNowBalanceSum(t, pool, cust); got != want {
		t.Fatalf("the as of aging at tomorrow reads %d, want the balance sum %d (on or after today the figure is the balance)", got, want)
	}
	afterTomorrow, err := repo.ARBalanceCents(ctx, tomorrow, "")
	if err != nil {
		t.Fatalf("read the tie figure at tomorrow: %v", err)
	}
	if want := atNowBalanceSum(t, pool, cust); afterTomorrow-beforeTomorrow != want {
		t.Fatalf("the 1100 tie figure at tomorrow moved %d, want the balance sum %d (on or after today the figure is the balance)", afterTomorrow-beforeTomorrow, want)
	}

	// Issued ahead of the house day (the seed's own UTC shape, the assistant's parity fixture): a
	// document issued tomorrow by the dealer's calendar is on neither figure at now, the balance arm
	// included. The issue date arm cuts on the house day of the as of (the evening guard's red), so
	// the document joins the books on its issue day, not at the instant a writer filed it. The plant
	// names the dealer's tomorrow through houseday, not the session's CURRENT_DATE (the UTC day):
	// a UTC shaped date is the dealer's today in the evening window and its tomorrow in the day,
	// so a leg planted from it would assert different rules as the wall clock turns.
	before, err = repo.ARBalanceCents(ctx, time.Now(), "")
	if err != nil {
		t.Fatalf("read the tie figure: %v", err)
	}
	cust = seedCustomer(t, pool, ns(t))
	seedInvoice(t, pool, cust, ns(t), invSpec{no: "AHEAD", status: "FINALIZED", subtotal: 4_000, tax: 0, total: 4_000, balance: 4_000, finalized: true})
	aheadDay := houseday.AddDays(func() time.Time { return time.Now() }, 1)
	if _, err := pool.Exec(ctx, `UPDATE invoices SET issue_date = $2::date, due_date = $2::date + 31 WHERE customer_id = $1::uuid`, cust, aheadDay); err != nil {
		t.Fatalf("issue ahead of the house day: %v", err)
	}
	if got := agingTotalForCustomer(t, pool, time.Now(), cust); got != 0 {
		t.Fatalf("the as of aging at now reads %d for a document issued ahead of the house day, want 0 (it joins the books on its issue day)", got)
	}
	after, err := repo.ARBalanceCents(ctx, time.Now(), "")
	if err != nil {
		t.Fatalf("read the tie figure at now: %v", err)
	}
	if moved := after - before; moved != 0 {
		t.Fatalf("the 1100 tie figure at now moved %d for a document issued ahead of the house day, want 0 (it joins the books on its issue day)", moved)
	}
}
