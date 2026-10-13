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
func (r *Repository) ARBalanceCents(ctx context.Context, asOf time.Time, branchID string) (int64, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	day, err := r.clockAt(ctx, asOf)
	if err != nil {
		return 0, err
	}
	var sum int64
	err = r.pool.QueryRow(ctx,
		`SELECT
		   (SELECT COALESCE(SUM(balance_cents), 0) FROM invoices
		     WHERE status IN ('FINALIZED','PARTIALLY_PAID') AND balance_cents <> 0
		       AND issue_date <= $1
		       AND ($2 = '' OR branch_id = $2::uuid))
		 + (SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions
		     WHERE txn_type IN ('NSF','ADJUSTMENT','CREDIT_MEMO','WRITE_OFF') AND source_type <> 'invoice'
		       AND created_at <= $3
		       AND ($2 = '' OR branch_id = $2::uuid))`,
		day, branchID, asOf,
	).Scan(&sum)
	if err != nil {
		return 0, fmt.Errorf("billing: AR balance as-of %s: %w", asOf.Format(time.RFC3339), err)
	}
	return sum, nil
}
