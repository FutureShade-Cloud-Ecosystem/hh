//go:build integration

// discount_tie_integration_test.go, TM-2 item 3: after a discounted application the aging, the
// invoices and GL 1100 tie. External package on purpose, like the journal leg: it wires the real
// GL consumer and the real aging, which the internal files cannot import without a cycle.
//
// Run: go test -tags integration ./internal/payments/ -run DiscountTies
package payments_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

type tieTerms struct{ svc *customer.Service }

func (d tieTerms) DiscountTermsByCode(ctx context.Context, tx pgx.Tx, code string) (payments.DiscountTerms, error) {
	t, err := d.svc.TermsByCode(customer.WithCallerTx(ctx, tx), code)
	if err != nil {
		return payments.DiscountTerms{}, err
	}
	return payments.DiscountTerms{DiscountBps: t.DiscountBps}, nil
}

func TestApplyReceived_DiscountTies_AgingInvoicesAndGL1100(t *testing.T) {
	pool := journalPool(t)
	ctx := context.Background()
	tag := "APT-" + randomHex()
	const branch = "5d1b3f6e-0000-4000-9000-000000000001"
	var customerID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ($1, $2, 'CONTRACTOR', $3::uuid, 0) RETURNING id::text`,
		tag, "Discount tie "+tag, branch).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM gl_journal_lines WHERE journal_id IN (
		   SELECT j.id FROM gl_journals j JOIN payments p ON p.id = j.source_ref_id WHERE p.customer_id = $1::uuid)`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM gl_journals WHERE source_ref_id IN (SELECT p.id FROM payments p WHERE p.customer_id = $1::uuid)`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE payload->>'customer_id' = $1`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM payment_allocations WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM payments WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, customerID)
	})
	today := houseday.DefaultZone().Today(time.Now)
	var invID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date, discount_date, terms_code,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		 VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $4::date, $4::date, '2-10-NET30', 100000, 0, 100000, 100000, $4, 'INVOICE')
		 RETURNING id::text`, tag+"-I", customerID, branch, today).Scan(&invID); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	// The subledger row the writer would have raised with the invoice, so the aging has its debit.
	if _, err := pool.Exec(ctx,
		`INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		 VALUES ($1::uuid, 'INVOICE', 100000, 100000, 'invoice', $2::uuid, $3::uuid, 'tie leg invoice')`,
		customerID, invID, branch); err != nil {
		t.Fatalf("seed the invoice debit: %v", err)
	}

	repo := payments.NewRepository(pool).WithDiscountTerms(tieTerms{svc: customer.NewService(customer.NewRepository(pool))})
	ans, err := repo.ApplyReceived(ctx, payments.ApplyRequest{
		CustomerID: customerID, Method: "CHEQUE", Reference: "CHQ TIE",
		IdempotencyKey: tag, InvoiceIDs: []string{invID}, AmountCents: 40000,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	glSvc := gl.NewService(gl.NewRepository(pool)).WithPaymentReader(till.NewRepository(pool))
	d := notify.NewDispatcher(notify.NewPgOutboxStore(pool), slog.Default())
	glSvc.RegisterPostingHandlers(d)
	for i := 0; i < 200; i++ {
		d.DrainOnce(ctx)
		var n int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM gl_journals WHERE source_ref_id = $1::uuid`, ans.PaymentID).Scan(&n); err != nil {
			t.Fatalf("watch the journals: %v", err)
		}
		if n >= 2 {
			break
		}
	}

	// 40000 of money and 800 of discount: the invoice owes 59200.
	var balance int64
	if err := pool.QueryRow(ctx, `SELECT balance_cents FROM invoices WHERE id = $1::uuid`, invID).Scan(&balance); err != nil {
		t.Fatalf("read the invoice: %v", err)
	}
	if balance != 59200 {
		t.Fatalf("the invoice owes 59200, got %d", balance)
	}
	// GL 1100 for this payment: the receipt's credit plus the discount's credit equal what the
	// invoice fell by.
	var credit1100 int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(COALESCE(l.credit_cents, 0) - COALESCE(l.debit_cents, 0)), 0)
		  FROM gl_journal_lines l
		  JOIN gl_journals j ON j.id = l.journal_id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.source_ref_id = $1::uuid AND a.code = '1100'`, ans.PaymentID).Scan(&credit1100); err != nil {
		t.Fatalf("read 1100: %v", err)
	}
	if credit1100 != 40800 {
		t.Fatalf("GL 1100 is credited 40000 + 800 = 40800 for the payment, got %d", credit1100)
	}
	// The subledger: the customer's net transactions are what the invoice still owes.
	var sub int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(SUM(amount_cents),0) FROM customer_transactions WHERE customer_id = $1::uuid`, customerID).Scan(&sub); err != nil {
		t.Fatalf("read the subledger: %v", err)
	}
	if sub != 59200 {
		t.Fatalf("the subledger nets to 59200, got %d", sub)
	}
	// The aging: this customer's row totals the same 59200.
	rows, err := billing.NewCreditService(billing.NewRepository(pool)).Aging(ctx, time.Now().Add(time.Hour), "")
	if err != nil {
		t.Fatalf("aging: %v", err)
	}
	var aged int64 = -1
	for _, r := range rows {
		if r.CustomerID == customerID {
			aged = r.TotalCents
		}
	}
	if aged != 59200 {
		t.Fatalf("the aging row ties at 59200, got %d", aged)
	}
}
