//go:build integration

package billing

// credit_cap_integration_test.go (SF-2e): a credit memo linked to an invoice never credits more than the invoice has
// left. S2 staged the defect: an invoice, a partial reversal, then a whole reversal of the same invoice credited the
// customer the original total again on top of the partial.
//
// The invoice here is 12,345 in total: a subtotal of 10,000 and tax of 2,345 over two lines.

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
)

type countingPoster struct{ n atomic.Int64 }

func (p *countingPoster) PostCreditMemoJournal(_ context.Context, _ CreditMemoJournal) (string, error) {
	p.n.Add(1)
	return "j", nil
}

func seedCappedInvoice(t *testing.T, pool *pgxpool.Pool, custID, no string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type, source_kind)
		 VALUES ($1, $2, $3::uuid, 'FINALIZED', current_date, current_date, 10000, 2345, 12345, 12345, now(), 'INVOICE', 'ORDER')
		 RETURNING id::text`, no, custID, trentonBranch).Scan(&id); err != nil {
		t.Fatalf("seed the invoice: %v", err)
	}
	for i, l := range []struct{ sub, tax int64 }{{6000, 1407}, {4000, 938}} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO invoice_lines (invoice_id, line_no, description, qty_entered, uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents, tax_cents)
			 VALUES ($1::uuid, $2, $3, 1, 'EA', 1, $4, 'EA', $4, $5)`,
			id, i+1, "capped line", l.sub, l.tax); err != nil {
			t.Fatalf("seed the line: %v", err)
		}
	}
	return id
}

type memoState struct{ memos, ledgerRows int }

func capState(t *testing.T, pool *pgxpool.Pool, custID string) memoState {
	t.Helper()
	var s memoState
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM invoices WHERE customer_id = $1 AND doc_type = 'CREDIT_MEMO'`, custID).Scan(&s.memos)
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM customer_transactions WHERE customer_id = $1 AND txn_type = 'CREDIT_MEMO'`, custID).Scan(&s.ledgerRows)
	return s
}

func creditedSoFar(t *testing.T, pool *pgxpool.Pool, invoiceID string) (sub, tax int64) {
	t.Helper()
	_ = pool.QueryRow(context.Background(),
		`SELECT COALESCE(-SUM(subtotal_cents),0), COALESCE(-SUM(tax_cents),0) FROM invoices WHERE reverses_invoice_id = $1::uuid AND doc_type = 'CREDIT_MEMO'`, invoiceID).Scan(&sub, &tax)
	return sub, tax
}

// (a) S2's staged case: a partial reversal, then a whole reversal. The whole one credits only what remains, with the
// tax in step, and the customer's ledger ends at the invoice's own total.
func TestIntegration_CreditCap_AWholeReversalAfterAPartialCreditsOnlyTheRemainder(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	custID := seedCustomer(t, pool, tag)
	w := NewInvoiceWriter(pool)
	inv := seedCappedInvoice(t, pool, custID, "#INV-"+tag+"-CAP")

	part, err := w.IssueCreditMemo(ctx, CreditMemoInput{CustomerID: custID, BranchID: trentonBranch, ReversesInvoiceID: inv, AmountCents: 2000, IdempotencyKey: "cap-a1-" + tag})
	if err != nil {
		t.Fatalf("the partial reversal: %v", err)
	}
	whole, err := w.IssueCreditMemo(ctx, CreditMemoInput{CustomerID: custID, BranchID: trentonBranch, ReversesInvoiceID: inv, IdempotencyKey: "cap-a2-" + tag})
	if err != nil {
		t.Fatalf("the whole reversal: %v", err)
	}
	sub, tax := creditedSoFar(t, pool, inv)
	t.Logf("partial %d, whole %d (subtotal %d, tax %d); credited so far subtotal %d tax %d", part.TotalCents, whole.TotalCents, whole.SubtotalCents, whole.TaxCents, sub, tax)
	if sub != 10000 || tax != 2345 {
		t.Errorf("the two memos credit subtotal %d and tax %d, want the invoice's 10000 and 2345", sub, tax)
	}
	if whole.SubtotalCents != -8000 || whole.TaxCents != -1876 || whole.TotalCents != -9876 {
		t.Errorf("the whole reversal after a partial is %d + %d = %d, want -8000 + -1876 = -9876", whole.SubtotalCents, whole.TaxCents, whole.TotalCents)
	}
	var net int64
	_ = pool.QueryRow(ctx, `SELECT COALESCE(SUM(amount_cents),0) FROM customer_transactions WHERE customer_id = $1 AND txn_type = 'CREDIT_MEMO'`, custID).Scan(&net)
	if net != -12345 {
		t.Errorf("the customer's credit memo ledger nets %d, want -12345 (the invoice's total)", net)
	}
}

// (b) A reversal larger than the remainder is refused in plain words naming what remains, and writes nothing.
func TestIntegration_CreditCap_AReversalAboveTheRemainderIsRefusedWithNothingWritten(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	custID := seedCustomer(t, pool, tag)
	poster := &countingPoster{}
	w := NewInvoiceWriter(pool).WithCreditMemoPoster(poster)
	inv := seedCappedInvoice(t, pool, custID, "#INV-"+tag+"-CAPB")
	if _, err := w.IssueCreditMemo(ctx, CreditMemoInput{CustomerID: custID, BranchID: trentonBranch, ReversesInvoiceID: inv, AmountCents: 2000, IdempotencyKey: "cap-b1-" + tag}); err != nil {
		t.Fatalf("the partial reversal: %v", err)
	}
	before, posted := capState(t, pool, custID), poster.n.Load()
	_, err := w.IssueCreditMemo(ctx, CreditMemoInput{CustomerID: custID, BranchID: trentonBranch, ReversesInvoiceID: inv, AmountCents: 9000, IdempotencyKey: "cap-b2-" + tag})
	t.Logf("a 9000 reversal with 8000 left: %v", err)
	if err == nil {
		t.Fatalf("a reversal above the remainder was accepted")
	}
	if !strings.Contains(err.Error(), "$80.00") {
		t.Errorf("the refusal reads %q, want it to say $80.00 is left to credit", err.Error())
	}
	if after := capState(t, pool, custID); after != before || poster.n.Load() != posted {
		t.Errorf("the refused reversal wrote: %+v to %+v, journals %d to %d", before, after, posted, poster.n.Load())
	}
	// Exactly the remainder is accepted, and finishes the invoice's tax to the cent.
	last, err := w.IssueCreditMemo(ctx, CreditMemoInput{CustomerID: custID, BranchID: trentonBranch, ReversesInvoiceID: inv, AmountCents: 8000, IdempotencyKey: "cap-b3-" + tag})
	if err != nil {
		t.Fatalf("a reversal of exactly the remainder: %v", err)
	}
	if last.TaxCents != -1876 {
		t.Errorf("the last partial carries tax %d, want -1876 (the tax that is left)", last.TaxCents)
	}
}

// (c) An invoice credited in full has nothing left, whole or partial.
func TestIntegration_CreditCap_AnInvoiceCreditedInFullHasNothingLeft(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	custID := seedCustomer(t, pool, tag)
	w := NewInvoiceWriter(pool)
	inv := seedCappedInvoice(t, pool, custID, "#INV-"+tag+"-CAPC")
	if _, err := w.IssueCreditMemo(ctx, CreditMemoInput{CustomerID: custID, BranchID: trentonBranch, ReversesInvoiceID: inv, IdempotencyKey: "cap-c1-" + tag}); err != nil {
		t.Fatalf("the whole reversal: %v", err)
	}
	before := capState(t, pool, custID)
	for name, amount := range map[string]int64{"whole": 0, "partial": 500} {
		_, err := w.IssueCreditMemo(ctx, CreditMemoInput{CustomerID: custID, BranchID: trentonBranch, ReversesInvoiceID: inv, AmountCents: amount, IdempotencyKey: "cap-c2-" + name + tag})
		if err == nil || !strings.Contains(err.Error(), "nothing left") {
			t.Errorf("a %s reversal of a fully credited invoice answered %v, want the nothing left words", name, err)
		}
	}
	if after := capState(t, pool, custID); after != before {
		t.Errorf("the refused reversals wrote: %+v to %+v", before, after)
	}
}

// Two partial reversals of one invoice whose sum exceeds it: the reversed invoice is taken FOR NO KEY UPDATE and the
// remainder read under it, so exactly one commits and the other is refused, with no deadlock. The first is parked at
// its invoices counter draw (testkit.DrawPark, a BEFORE INSERT park) holding the invoice row.
func TestIntegration_CreditCap_TwoPartialReversalsCannotBothPassTheCap(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	custID := seedCustomer(t, pool, tag)
	inv := seedCappedInvoice(t, pool, custID, "#INV-"+tag+"-CAPD")
	park := testkit.NewDrawPark(t, pool, "invoices", 55010801)
	first, second := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := NewInvoiceWriter(park.Pool).IssueCreditMemo(ctx, CreditMemoInput{CustomerID: custID, BranchID: trentonBranch, ReversesInvoiceID: inv, AmountCents: 6000, IdempotencyKey: "cap-d1-" + tag})
		first <- err
	}()
	park.WaitParked("the first reversal holds the invoice at its counter draw")
	go func() {
		_, err := NewInvoiceWriter(pool).IssueCreditMemo(ctx, CreditMemoInput{CustomerID: custID, BranchID: trentonBranch, ReversesInvoiceID: inv, AmountCents: 6000, IdempotencyKey: "cap-d2-" + tag})
		second <- err
	}()
	park.WaitBlocked("the second reversal queues on the invoice")
	park.AnswersAllowing(map[string]chan error{"first": first, "second": second}, map[string]string{"second": "left to credit"})
	sub, _ := creditedSoFar(t, pool, inv)
	if sub != 6000 {
		t.Errorf("the invoice is credited %d in subtotal, want exactly one 6000 memo", sub)
	}
}
