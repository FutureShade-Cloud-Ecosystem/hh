//go:build integration

package billing

// credit_memo_supplier_deposit_integration_test.go, item 9 delta review P2-C,
// the database half: the refusal reads the document's own source_kind, so it
// is proved against the real schema (a memo against an ordinary invoice
// beside it still mirrors it, the refusal does not overreach).

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seedSupplierDepositInvoice plants the deposit document the ring writes: a
// FINALIZED INVOICE under the SUPPLIER_DEPOSIT source kind, one line at the
// deposit amount.
func seedSupplierDepositInvoice(t *testing.T, pool *pgxpool.Pool, customerID, no string, subtotal int64) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at,
		                       doc_type, source_kind)
		 VALUES ($1, $2, $3::uuid, 'FINALIZED', (now() AT TIME ZONE 'America/Toronto')::date, (now() AT TIME ZONE 'America/Toronto')::date, $4, 0, $4, $4, now(),
		         'INVOICE', 'SUPPLIER_DEPOSIT')
		 RETURNING id::text`,
		no, customerID, trentonBranch, subtotal).Scan(&id); err != nil {
		t.Fatalf("seed supplier deposit invoice %s: %v", no, err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO invoice_lines (invoice_id, line_no, description, qty_entered, uom_entered,
		                            qty_base, unit_price_cents, price_uom, line_total_cents, tax_cents)
		 VALUES ($1::uuid, 1, 'Supplier deposit on a special order', 1, 'EA', 1, $2, 'EA', $2, 0)`,
		id, subtotal); err != nil {
		t.Fatalf("seed the deposit line: %v", err)
	}
	return id
}

// A credit memo against a deposit document is refused in words, whole or
// partial, and nothing is written: no memo row, no subledger row.
func TestIntegration_CreditMemo_SupplierDepositRefused(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	custID := seedCustomer(t, pool, tag)
	w := NewInvoiceWriter(pool)

	depositID := seedSupplierDepositInvoice(t, pool, custID, "#INV-"+tag+"-DEP", 5_000)
	var memosBefore int
	pool.QueryRow(ctx,
		`SELECT count(*) FROM invoices WHERE customer_id = $1 AND doc_type = 'CREDIT_MEMO'`, custID).Scan(&memosBefore)

	for _, tc := range []struct {
		name   string
		amount int64
	}{{"the whole deposit", 0}, {"a partial credit", 2_000}} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := w.IssueCreditMemo(ctx, CreditMemoInput{
				CustomerID: custID, BranchID: trentonBranch,
				ReversesInvoiceID: depositID, AmountCents: tc.amount,
				IdempotencyKey: "cm-dep-" + tag + "-" + tc.name,
			})
			var refusal *SupplierDepositNotCreditable
			if !errors.As(err, &refusal) {
				t.Fatalf("the %s against a deposit document must be refused in words, got %v", tc.name, err)
			}
			if refusal.Error() == "" || refusal.InvoiceNo == "" {
				t.Fatalf("the refusal names neither its words nor its document: %+v", refusal)
			}
		})
	}

	var memosAfter int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM invoices WHERE customer_id = $1 AND doc_type = 'CREDIT_MEMO'`, custID).
		Scan(&memosAfter); err != nil || memosAfter != memosBefore {
		t.Fatalf("a refused memo wrote rows: %d before, %d after (%v)", memosBefore, memosAfter, err)
	}

	// The refusal does not overreach: an ORDINARY invoice beside the deposit
	// document still mirrors faithfully.
	ordinaryID := seedSupplierDepositInvoice(t, pool, custID, "#INV-"+tag+"-ORD", 3_000)
	if _, err := pool.Exec(ctx,
		`UPDATE invoices SET source_kind = 'ORDER' WHERE id = $1::uuid`, ordinaryID); err != nil {
		t.Fatalf("re-mark the ordinary invoice: %v", err)
	}
	doc, err := w.IssueCreditMemo(ctx, CreditMemoInput{
		CustomerID: custID, BranchID: trentonBranch,
		ReversesInvoiceID: ordinaryID,
		IdempotencyKey:    "cm-ord-" + tag,
	})
	if err != nil {
		t.Fatalf("an ordinary invoice must still mirror: %v", err)
	}
	if doc.DocType != DocCreditMemo || doc.TotalCents != -3_000 {
		t.Fatalf("the ordinary memo: %+v", doc)
	}
}
