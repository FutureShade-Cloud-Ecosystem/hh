//go:build integration

// discount_undo_integration_test.go, TM-2 fix round, P3-1: a void and a reversal of a discounted
// application give the discount back, on the invoice side and in the ledger. External package, like
// the tie leg: it drains the real GL consumer.
//
// Run: go test -tags integration ./internal/payments/ -run DiscountUndo
package payments_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

func TestDiscountUndo_VoidAndReversalGiveTheDiscountBack(t *testing.T) {
	for _, undo := range []string{"void", "reversal"} {
		t.Run(undo, func(t *testing.T) { discountUndoLeg(t, undo) })
	}
}

func discountUndoLeg(t *testing.T, undo string) {
	pool := journalPool(t)
	ctx := context.Background()
	tag := "UND-" + randomHex()
	const branch = "5d1b3f6e-0000-4000-9000-000000000001"
	var customerID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ($1, $2, 'CONTRACTOR', $3::uuid, 0) RETURNING id::text`,
		tag, "Discount undo "+tag, branch).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	t.Cleanup(func() { undoCleanup(t, pool, customerID) })

	today := houseday.DefaultZone().Today(time.Now)
	var invID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date, discount_date, terms_code,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		 VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $4::date, $4::date, '2-10-NET30', 100000, 0, 100000, 100000, $4, 'INVOICE')
		 RETURNING id::text`, tag+"-I", customerID, branch, today).Scan(&invID); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		 VALUES ($1::uuid, 'INVOICE', 100000, 100000, 'invoice', $2::uuid, $3::uuid, 'undo leg invoice')`,
		customerID, invID, branch); err != nil {
		t.Fatalf("seed the invoice debit: %v", err)
	}

	repo := payments.NewRepository(pool).WithDiscountTerms(tieTerms{svc: customer.NewService(customer.NewRepository(pool))})
	ans, err := repo.ApplyReceived(ctx, payments.ApplyRequest{
		CustomerID: customerID, Method: "CHEQUE", Reference: "CHQ UND", IdempotencyKey: tag,
		InvoiceIDs: []string{invID}, AmountCents: 98000,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if ans.Lines[0].DiscountCents != 2000 {
		t.Fatalf("the apply took %d of discount, want 2000", ans.Lines[0].DiscountCents)
	}

	switch undo {
	case "void":
		if _, err := repo.VoidPayment(ctx, payments.VoidInput{PaymentID: ans.PaymentID, Reason: "undo leg", IsManager: true}, time.Now(), houseday.DefaultZone()); err != nil {
			t.Fatalf("void: %v", err)
		}
	default:
		if _, err := repo.ReversePayment(ctx, payments.ReverseInput{PaymentID: ans.PaymentID, Reason: "undo leg", IsManager: true}, time.Now()); err != nil {
			t.Fatalf("reverse: %v", err)
		}
	}

	glSvc := gl.NewService(gl.NewRepository(pool)).WithPaymentReader(till.NewRepository(pool))
	d := notify.NewDispatcher(notify.NewPgOutboxStore(pool), slog.Default())
	glSvc.RegisterPostingHandlers(d)
	journals := func() int {
		var n int
		if err := pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM gl_journals j WHERE j.source_ref_id IN (SELECT id FROM payments WHERE customer_id = $1::uuid)`,
			customerID).Scan(&n); err != nil {
			t.Fatalf("count journals: %v", err)
		}
		return n
	}
	for i := 0; i < 200 && journals() < 3; i++ {
		d.DrainOnce(ctx)
	}
	if n := journals(); n != 3 {
		t.Fatalf("the receipt, the discount and the %s journals are posted, found %d", undo, n)
	}

	// The invoice side: back at face.
	var bal int64
	var st string
	if err := pool.QueryRow(ctx, `SELECT balance_cents, status FROM invoices WHERE id = $1::uuid`, invID).Scan(&bal, &st); err != nil {
		t.Fatalf("read invoice: %v", err)
	}
	if bal != 100000 || st != "FINALIZED" {
		t.Errorf("after the %s the invoice is %d %s, want 100000 FINALIZED", undo, bal, st)
	}
	// The subledger: the discount's own rows (against the invoice) net to zero, taking then giving back.
	var discountRows int64
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions
		  WHERE customer_id = $1::uuid AND txn_type = 'ADJUSTMENT' AND source_type = 'invoice' AND source_ref_id = $2::uuid`,
		customerID, invID).Scan(&discountRows); err != nil {
		t.Fatalf("read the discount rows: %v", err)
	}
	if discountRows != 0 {
		t.Errorf("the discount's subledger rows net %d, want 0 (taken then given back)", discountRows)
	}
	// The GL lines: DR 1100 / CR 5450 for the give back, beside the cash reversal. 5450 and 1100
	// both net to zero across the payment's journals; and the give back journal itself carries
	// the DR AR 2000 / CR 5450 2000 pair.
	net := func(code string) (debit, credit int64) {
		if err := pool.QueryRow(ctx, `
			SELECT COALESCE(SUM(COALESCE(l.debit_cents, 0)), 0), COALESCE(SUM(COALESCE(l.credit_cents, 0)), 0)
			  FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id JOIN gl_accounts a ON a.id = l.account_id
			 WHERE j.source_ref_id IN (SELECT id FROM payments WHERE customer_id = $1::uuid) AND a.code = $2`,
			customerID, code).Scan(&debit, &credit); err != nil {
			t.Fatalf("read %s: %v", code, err)
		}
		return
	}
	if dr, cr := net("5450"); dr != 2000 || cr != 2000 {
		t.Errorf("5450 is debited %d and credited %d, want 2000 each (taken, then given back)", dr, cr)
	}
	if dr, cr := net("1100"); dr != cr || dr != 100000 {
		t.Errorf("1100 is debited %d and credited %d, want 100000 each", dr, cr)
	}
}

func undoCleanup(t *testing.T, pool *pgxpool.Pool, customerID string) {
	c := context.Background()
	for _, q := range []string{
		`DELETE FROM gl_journal_lines WHERE journal_id IN (
		   SELECT j.id FROM gl_journals j WHERE j.source_ref_id IN (SELECT p.id FROM payments p WHERE p.customer_id = $1::uuid))`,
		`DELETE FROM gl_journals WHERE source_ref_id IN (SELECT p.id FROM payments p WHERE p.customer_id = $1::uuid)`,
		`DELETE FROM customer_transactions WHERE customer_id = $1::uuid`,
		`DELETE FROM events_outbox WHERE payload->>'customer_id' = $1`,
		`DELETE FROM payment_allocations WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`,
		`UPDATE payments SET reversed_by_payment_id = NULL WHERE customer_id = $1::uuid AND reversed_by_payment_id IS NOT NULL`,
		`DELETE FROM payments WHERE customer_id = $1::uuid AND reverses_payment_id IS NOT NULL`,
		`DELETE FROM payments WHERE customer_id = $1::uuid`,
		`DELETE FROM invoices WHERE customer_id = $1::uuid`,
		`DELETE FROM customers WHERE id = $1::uuid`,
	} {
		if _, err := pool.Exec(c, q, customerID); err != nil {
			t.Errorf("cleanup failed on %q: %v", q, err)
		}
	}
}
