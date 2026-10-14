// order_cancel_seam.go, B2/R2: the invoice half of the order module's cancel predicate. A manual order
// CANCELLED is refused when money the cancel has no exit for is already attached; this answers "does
// this order have a finalized invoice other than its supplier deposit documents?" without the order
// module importing billing or reading the invoices table (module boundary). The deposit documents are
// the cancel's own money to settle (deposit_cancel.go, row 1193). Satisfies order.FinalizedInvoiceReader
// structurally, billingRepo is passed to orderSvc.WithCancelGuards in the composition root, the same way
// it already satisfies order.InvoiceOrderResolver (order_seam.go).
package billing

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// OrderHasFinalizedInvoice reports whether the order has at least one invoice
// whose finalize transaction has run, OTHER THAN a supplier deposit document.
//
// invoices.status ∈ (DRAFT, FINALIZED, PARTIALLY_PAID, PAID, VOID). We count FINALIZED / PARTIALLY_PAID
// / PAID:
//   - DRAFT is excluded, nothing has posted yet, so cancelling orphans nothing.
//   - VOID is excluded, the invoice has already been unwound, so it is not money still attached.
//
// A SUPPLIER_DEPOSIT document is excluded (decision log row 1193): an order
// whose ONLY money is its deposit document may be cancelled, and the cancel
// itself settles the deposit (billing.CancelOrderDeposits, credited back or
// kept). The document is money the cancel has an exit for, not money it
// orphans; any OTHER finalized invoice still blocks, exactly as before.
//
// A read failure is returned, never swallowed: the caller must not treat an unreadable predicate as
// permission to cancel (fail closed).
func (r *Repository) OrderHasFinalizedInvoice(ctx context.Context, orderID string) (bool, error) {
	return r.OrderHasFinalizedInvoiceTx(ctx, nil, orderID)
}

// OrderHasFinalizedInvoiceTx is OrderHasFinalizedInvoice joined to the caller's transaction (nil tx means
// the pool). The order module runs the predicate inside its own order transaction; reading through the
// pool there would take a second connection while the first is still held, and pool-size callers in
// flight together would starve each other (no lock is waited on, so the database cannot see it). Satisfies
// order.FinalizedInvoiceTxReader structurally.
func (r *Repository) OrderHasFinalizedInvoiceTx(ctx context.Context, tx pgx.Tx, orderID string) (bool, error) {
	if r.pool == nil {
		// No pool means we cannot prove absence. Report the error so the order service fails closed
		// rather than reading a bare false as "no money attached".
		return false, fmt.Errorf("billing: no database configured for the order cancel predicate")
	}
	const q = `
		SELECT EXISTS (
			SELECT 1 FROM invoices
			WHERE order_id = $1::uuid
			  AND status IN ('FINALIZED','PARTIALLY_PAID','PAID')
			  AND source_kind <> 'SUPPLIER_DEPOSIT'
		)`
	var exists bool
	var err error
	if tx != nil {
		err = tx.QueryRow(ctx, q, orderID).Scan(&exists)
	} else {
		err = r.pool.QueryRow(ctx, q, orderID).Scan(&exists)
	}
	if err != nil {
		return false, fmt.Errorf("check finalized invoice for order %q: %w", orderID, err)
	}
	return exists, nil
}
