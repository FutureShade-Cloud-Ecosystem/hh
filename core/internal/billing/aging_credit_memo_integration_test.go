//go:build integration

package billing

// aging_credit_memo_integration_test.go, FW1-06b round 2, S6's P1-1.
//
// The as of rebuild added a credit memo document's own originating subledger row on top of the
// document's negative total, so every memo counted twice in the aging and in the 1100 tie figure,
// today's figures included. The legs here plant one document per writer (the finance memo, the
// reversal memo, the imported credit, the counter return credit) and hold both figures at now to the
// customer's own balance_cents sum. Two further shapes are the controls that must KEEP counting: a
// counter sale with a credit applied, and a deposit cancel's closing adjustment, whose move rows are
// not the document's own row.

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// agingTotalForCustomer reads one customer's aging grand total at the as of instant, 0 when the
// account carries nothing open.
func agingTotalForCustomer(t *testing.T, pool *pgxpool.Pool, asOf time.Time, cust string) int64 {
	t.Helper()
	rows, err := NewRepository(pool).AgingRowsIn(context.Background(), asOf, 60, "", AllBranches)
	if err != nil {
		t.Fatalf("read the aging: %v", err)
	}
	for _, r := range rows {
		if r.CustomerID == cust {
			return r.TotalCents
		}
	}
	return 0
}

// seedStandaloneCredit plants the rows the loader (load_receivables.go's negative record) and the
// counter return write for a credit memo document: a CREDIT_MEMO document carrying its negative
// balance, and the document's own originating CREDIT_MEMO subledger row tagged to it.
func seedStandaloneCredit(t *testing.T, pool *pgxpool.Pool, cust, no string, total int64) {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', '1990-01-15', '1990-01-15',
		        $4, 0, $4, $4, now(), 'CREDIT_MEMO')
		RETURNING id::text`, no, cust, trentonBranch, total).Scan(&id); err != nil {
		t.Fatalf("seed the credit memo document: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO customer_transactions
			(customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo, occurred_at)
		VALUES ($1::uuid, 'CREDIT_MEMO', $2, 0, 'invoice', $3::uuid, $4::uuid, $5, now())`,
		cust, total, id, trentonBranch, no); err != nil {
		t.Fatalf("seed the memo's own subledger row: %v", err)
	}
}

// TestIntegration_FW106bRound2_TheAgingAndTheTieCountAMemoOnce is S6's P1-1 leg: for a document from
// each writer, the aging at now and the 1100 tie figure read the customer's balance_cents sum, never
// the memo counted twice.
func TestIntegration_FW106bRound2_TheAgingAndTheTieCountAMemoOnce(t *testing.T) {
	pool := intPool(t)

	// Each shape plants its own customer and documents, and answers the customer the figures are read
	// for. The four memo shapes are the red; the last two are the keep counting controls.
	type shape struct {
		name  string
		plant func(t *testing.T) (cust string)
	}
	shapes := []shape{
		{"the finance memo", func(t *testing.T) string {
			cust := seedCustomer(t, pool, ns(t))
			if _, err := NewInvoiceWriter(pool).IssueCreditMemo(context.Background(), CreditMemoInput{
				CustomerID: cust, BranchID: trentonBranch, AmountCents: 25_00,
				Memo: "round 2 finance memo", IdempotencyKey: "r2-memo-" + ns(t),
			}); err != nil {
				t.Fatalf("issue the finance memo: %v", err)
			}
			return cust
		}},
		{"the reversal memo", func(t *testing.T) string {
			cust := seedCustomer(t, pool, ns(t))
			inv := seedInvoice(t, pool, cust, ns(t), invSpec{
				no: "INV", status: "FINALIZED", subtotal: 50_000, tax: 6_500,
				total: 56_500, balance: 56_500, finalized: true,
			})
			if _, err := NewInvoiceWriter(pool).IssueCreditMemo(context.Background(), CreditMemoInput{
				CustomerID: cust, BranchID: trentonBranch, ReversesInvoiceID: inv,
				AmountCents: 0, Memo: "round 2 reversal memo", IdempotencyKey: "r2-rev-" + ns(t),
			}); err != nil {
				t.Fatalf("issue the reversal memo: %v", err)
			}
			return cust
		}},
		{"the imported credit", func(t *testing.T) string {
			cust := seedCustomer(t, pool, ns(t))
			seedStandaloneCredit(t, pool, cust, "LOAD-"+ns(t), -12_345)
			return cust
		}},
		{"the counter return credit", func(t *testing.T) string {
			cust := seedCustomer(t, pool, ns(t))
			seedStandaloneCredit(t, pool, cust, "RET-"+ns(t), -9_000)
			return cust
		}},
		{"the counter sale with a credit applied", func(t *testing.T) string {
			// A move row, not the document's own: the credit is tagged to the sale's invoice, so the
			// rebuild must keep counting it. The sale's balance carries it already.
			cust := seedCustomer(t, pool, ns(t))
			inv := seedInvoice(t, pool, cust, ns(t), invSpec{
				no: "SALE", status: "FINALIZED", subtotal: 50_000, tax: 0,
				total: 50_000, balance: 30_000, finalized: true,
			})
			if _, err := pool.Exec(context.Background(), `
				INSERT INTO customer_transactions
					(customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo, occurred_at)
				VALUES ($1::uuid, 'CREDIT_MEMO', -20_000, 30_000, 'invoice', $2::uuid, $3::uuid, 'a counter credit applied', now())`,
				cust, inv, trentonBranch); err != nil {
				t.Fatalf("seed the applied credit: %v", err)
			}
			return cust
		}},
		{"the deposit cancel", func(t *testing.T) string {
			// A part paid deposit cancelled under KEPT: the document reads PAID at the paid part, and
			// the closing ADJUSTMENT is a move row tagged to it that the rebuild must keep counting.
			cust, orderID, _ := seedDepositOrderAt(t, pool, ns(t), 3_000)
			if _, err := NewInvoiceWriter(pool).CancelOrderDeposits(context.Background(), nil, orderID, "KEPT", "the order was cancelled"); err != nil {
				t.Fatalf("cancel the deposit: %v", err)
			}
			return cust
		}},
	}

	// The shapes share one run and their cleanups wait for its end (they are registered on the
	// test, not the step), so nothing a shape planted disappears under a later figure.
	ctx := context.Background()
	repo := NewRepository(pool)

	for _, sh := range shapes {
		before, err := repo.ARBalanceCents(ctx, time.Now(), "")
		if err != nil {
			t.Fatalf("%s: read the tie figure before: %v", sh.name, err)
		}

		cust := sh.plant(t)

		// The as of instant is taken after the plant, so the documents and their rows are on the
		// books at it: the figures at now are the ones today's screens read.
		now := time.Now()

		var want int64
		if err := pool.QueryRow(ctx, `
			SELECT COALESCE(SUM(balance_cents), 0) FROM invoices
			 WHERE customer_id = $1::uuid AND status IN ('FINALIZED','PARTIALLY_PAID','PAID')`, cust).
			Scan(&want); err != nil {
			t.Fatalf("%s: read the customer's balance sum: %v", sh.name, err)
		}

		got := agingTotalForCustomer(t, pool, now, cust)
		if got != want {
			t.Fatalf("fw1-06b round 2 red: the as of aging for %s reads %d, want the balance sum %d (the memo counted once)", sh.name, got, want)
		}
		after, err := repo.ARBalanceCents(ctx, now, "")
		if err != nil {
			t.Fatalf("%s: read the tie figure after: %v", sh.name, err)
		}
		if after-before != want {
			t.Fatalf("fw1-06b round 2 red: the 1100 tie figure moved %d for %s, want the balance sum %d (the memo counted once)", after-before, sh.name, want)
		}
	}
}
