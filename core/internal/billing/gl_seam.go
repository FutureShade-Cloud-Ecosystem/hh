package billing

// gl_seam.go, SCAFFOLD-owned GL read seams on *billing.Repository (same-package pool access; no edit to
// billing/service.go). billing is the sole reader of invoices + customer_transactions, so the GL posting
// consumer (A-01) resolves invoice finance facts + the AR subledger balance THROUGH here. These satisfy:
//   - gl.InvoiceReader (InvoiceForGL, the ar.invoice.finalized legs)
//   - gl.ARSubledger   (ARBalanceCents, the 1100 subledger tie-out)

import (
	"context"
	"fmt"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
)

// InvoiceForGL resolves the finance projection of a finalized invoice for its journal legs (subtotal /
// tax / holdback / total + branch/customer + finalized time). FinalizedAt falls back to created_at when the
// invoice has not been stamped finalized (defensive; the consumer posts under the finalized period).
func (r *Repository) InvoiceForGL(ctx context.Context, invoiceID string) (gl.InvoiceAmounts, error) {
	if r.pool == nil {
		return gl.InvoiceAmounts{}, errNoDB
	}
	var a gl.InvoiceAmounts
	err := r.pool.QueryRow(ctx,
		`SELECT subtotal_cents, tax_cents, total_cents, holdback_cents,
		        branch_id::text, customer_id::text, invoice_no::text,
		        COALESCE(finalized_at, created_at), source_kind
		 FROM invoices WHERE id = $1`, invoiceID,
	).Scan(&a.SubtotalCents, &a.TaxCents, &a.TotalCents, &a.HoldbackCents,
		&a.BranchID, &a.CustomerID, &a.InvoiceNo, &a.FinalizedAt, &a.SourceKind)
	if err != nil {
		return gl.InvoiceAmounts{}, fmt.Errorf("billing: invoice for gl %q: %w", invoiceID, err)
	}
	return a, nil
}

// ARBalanceCents returns the AR subledger balance as-of asOf for the 1100 tie-out. It sums the SAME two
// sources the 0034 v_ar_aging view unions, open invoice balances + non-invoice AR customer_transactions
// (NSF / ADJUSTMENT / CREDIT_MEMO / WRITE_OFF), so the aging grand total ties to this figure by
// construction. branchID "" = consolidated (all branches). The issue date arm cuts on the house day of
// the as-of (an invoice joins the dealer's books on the dealer's calendar, never the server zone's
// reading of the date); the transaction arm stays on the raw instant, because a payment or an NSF is
// an instant and not a day.
//
// The invoice arm carries each document's balance AT the as-of instant ($3). At an as of on the
// CURRENT house day it reads balance_cents, the inception to date figure, so the tie-out moves with
// every balance move; only a PAST date rebuilds, the same reconstruction the aging's agingSelect
// runs, so the tie-out at a past date compares what was owed then with the GL at that date and the
// aging's grand total ties to this figure by construction: an invoice paid after the as-of date is
// still owed at it, an invoice issued after it is not on the books at it, and a document settled by
// the instant nets to zero. PAID documents are read only for that as-of balance.
func (r *Repository) ARBalanceCents(ctx context.Context, asOf time.Time, branchID string) (int64, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	day, err := r.clockAt(ctx, asOf)
	if err != nil {
		return 0, err
	}
	today, err := r.clockAt(ctx, time.Now())
	if err != nil {
		return 0, err
	}
	var sum int64
	err = r.pool.QueryRow(ctx,
		`SELECT
		   (SELECT COALESCE(SUM(b.bal), 0) FROM (
		     SELECT CASE WHEN $4::bool THEN i.balance_cents
		                 ELSE i.total_cents
		                      + COALESCE((SELECT SUM(t.amount_cents) FROM customer_transactions t
		                      WHERE t.source_type = 'invoice' AND t.source_ref_id = i.id
		                        AND t.txn_type <> i.doc_type AND t.created_at <= $3), 0)
		                      - COALESCE((SELECT SUM(CASE WHEN p.reverses_payment_id IS NULL THEN a.amount_cents ELSE -a.amount_cents END)
		                      FROM payment_allocations a JOIN payments p ON p.id = a.payment_id
		                      WHERE a.invoice_id = i.id AND a.created_at <= $3), 0)
		                      - COALESCE((SELECT SUM(ra.amount_cents) FROM payment_reapplications ra
		                      WHERE ra.invoice_id = i.id AND ra.created_at <= $3), 0)
		                   END AS bal
		       FROM invoices i
		      WHERE i.status IN ('FINALIZED','PARTIALLY_PAID','PAID')
		        AND i.issue_date <= $1
		        AND ($2 = '' OR i.branch_id = $2::uuid)) b
		      WHERE b.bal <> 0)
		 + (SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions
		     WHERE txn_type IN ('NSF','ADJUSTMENT','CREDIT_MEMO','WRITE_OFF') AND source_type <> 'invoice'
		       AND created_at <= $3
		       AND ($2 = '' OR branch_id = $2::uuid))`,
		day, branchID, asOf, sameHouseDayAs(day, today),
	).Scan(&sum)
	if err != nil {
		return 0, fmt.Errorf("billing: AR balance as-of %s: %w", asOf.Format(time.RFC3339), err)
	}
	return sum, nil
}
