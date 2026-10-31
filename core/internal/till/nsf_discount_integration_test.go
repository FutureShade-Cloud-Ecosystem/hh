//go:build integration

// nsf_discount_integration_test.go, TM-2 fix round, P1: a bounced cheque gives the early payment
// discount back with its money. The leg runs the real apply (discount bound), the real bounce and
// the real GL consumer, then checks the invoice, the discounts allowed account and the aging.
//
// Run: go test -tags integration ./internal/till/ -run NSFDiscount
package till

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
)

type nsfDiscountTerms struct{}

func (nsfDiscountTerms) DiscountTermsByCode(_ context.Context, _ pgx.Tx, _ string) (payments.DiscountTerms, error) {
	return payments.DiscountTerms{DiscountBps: 200}, nil
}

func TestNSFDiscount_BounceGivesTheDiscountBack(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	svc := realSvc(pool)
	cctx := globalCtx(userID)
	tag := "NSFD-" + time.Now().Format("150405.000000")

	var customerID, invoiceID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ($1, $2, 'CONTRACTOR', $3::uuid, 1000000) RETURNING id::text`,
		tag, tag, branchID).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		for _, q := range []string{
			`DELETE FROM gl_journal_lines WHERE journal_id IN (
			   SELECT j.id FROM gl_journals j JOIN payments p ON p.id = j.source_ref_id WHERE p.customer_id = $1::uuid)`,
			`DELETE FROM gl_journals WHERE source_ref_id IN (SELECT p.id FROM payments p WHERE p.customer_id = $1::uuid)`,
			`DELETE FROM events_outbox WHERE payload->>'customer_id' = $1`,
			`DELETE FROM credit_holds WHERE customer_id = $1::uuid`,
			`DELETE FROM payment_nsf WHERE payment_id IN (SELECT id FROM payments WHERE customer_id = $1::uuid)`,
			`DELETE FROM customer_transactions WHERE customer_id = $1::uuid`,
			`DELETE FROM payment_allocations WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`,
			`DELETE FROM payments WHERE customer_id = $1::uuid`,
			`DELETE FROM invoices WHERE customer_id = $1::uuid`,
			`DELETE FROM customers WHERE id = $1::uuid`,
		} {
			if _, err := pool.Exec(c, q, customerID); err != nil {
				t.Errorf("cleanup failed on %q: %v", q, err)
			}
		}
	})
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date, discount_date, terms_code,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		 VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED',
		         (now() AT TIME ZONE 'America/Toronto')::date - 2, (now() AT TIME ZONE 'America/Toronto')::date + 28,
		         (now() AT TIME ZONE 'America/Toronto')::date + 8, '2-10-NET30',
		         100000, 0, 100000, 100000, now(), 'INVOICE') RETURNING id::text`,
		tag+"-I", customerID, branchID).Scan(&invoiceID); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		 VALUES ($1::uuid, 'INVOICE', 100000, 100000, 'invoice', $2::uuid, $3::uuid, 'nsf discount leg invoice')`,
		customerID, invoiceID, branchID); err != nil {
		t.Fatalf("seed the invoice debit: %v", err)
	}

	repo := payments.NewRepository(pool).WithDiscountTerms(nsfDiscountTerms{})
	ans, err := repo.ApplyReceived(ctx, payments.ApplyRequest{
		CustomerID: customerID, Method: "CHEQUE", Reference: "CHQ NSFD", IdempotencyKey: tag,
		InvoiceIDs: []string{invoiceID}, AmountCents: 98000, ReceivedBy: userID,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if ans.Lines[0].DiscountCents != 2000 {
		t.Fatalf("the apply took %d of discount, want 2000", ans.Lines[0].DiscountCents)
	}
	if _, err := svc.MarkNSF(cctx, NSFInput{PaymentID: ans.PaymentID, Reason: "cheque returned NSF"}); err != nil {
		t.Fatalf("MarkNSF: %v", err)
	}

	glSvc := gl.NewService(gl.NewRepository(pool)).WithPaymentReader(NewRepository(pool))
	d := notify.NewDispatcher(notify.NewPgOutboxStore(pool), slog.Default())
	glSvc.RegisterPostingHandlers(d)
	for i := 0; i < 200; i++ {
		d.DrainOnce(ctx)
		var n int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM gl_journals WHERE source_ref_id = $1::uuid`, ans.PaymentID).Scan(&n); err != nil {
			t.Fatalf("watch the journals: %v", err)
		}
		if n >= 3 {
			break
		}
	}

	var bal int64
	var st string
	if err := pool.QueryRow(ctx, `SELECT balance_cents, status FROM invoices WHERE id = $1::uuid`, invoiceID).Scan(&bal, &st); err != nil {
		t.Fatalf("read invoice: %v", err)
	}
	if bal != 100000 || st != "FINALIZED" {
		t.Errorf("after the bounce the invoice is %d %s, want 100000 FINALIZED", bal, st)
	}
	// 5450 net zero over the payment's journals.
	acct := func(code string) int64 {
		var net int64
		if err := pool.QueryRow(ctx, `
			SELECT COALESCE(SUM(COALESCE(l.debit_cents, 0) - COALESCE(l.credit_cents, 0)), 0)
			  FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id JOIN gl_accounts a ON a.id = l.account_id
			 WHERE j.source_ref_id = $1::uuid AND a.code = $2`, ans.PaymentID, code).Scan(&net); err != nil {
			t.Fatalf("read %s: %v", code, err)
		}
		return net
	}
	if got := acct("5450"); got != 0 {
		t.Errorf("5450 nets %d after the bounce, want 0", got)
	}
	if got := acct("1100"); got != nsfFee(t, pool, ans.PaymentID) {
		t.Errorf("1100 nets %d over the payment journals after the bounce, want the NSF fee only", got)
	}
	// The aging ties to 1100: the customer's aged total is the invoice's 100000, the same figure
	// 1100 would hold with the invoice's own debit (100000) plus the payment journals (net 0).
	rows, err := billing.NewCreditService(billing.NewRepository(pool)).Aging(ctx, time.Now().Add(time.Hour), "")
	if err != nil {
		t.Fatalf("aging: %v", err)
	}
	aged := int64(-1)
	for _, r := range rows {
		if r.CustomerID == customerID {
			aged = r.TotalCents
		}
	}
	if want := 100000 + acct("1100"); aged != want {
		t.Errorf("the aging reads %d, 1100 reads %d", aged, want)
	}
}

// nsfFee is the NSF fee the policy charged at the bounce: the one amount that legitimately stays on
// 1100 after the cheque comes back.
func nsfFee(t *testing.T, pool *pgxpool.Pool, paymentID string) int64 {
	t.Helper()
	var fee int64
	if err := pool.QueryRow(context.Background(), `SELECT COALESCE(SUM(fee_cents), 0) FROM payment_nsf WHERE payment_id = $1::uuid`, paymentID).Scan(&fee); err != nil {
		t.Fatalf("read the nsf fee: %v", err)
	}
	return fee
}
