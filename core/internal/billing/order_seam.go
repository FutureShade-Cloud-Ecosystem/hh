// order_seam.go, the narrow read the order module's DIB-191 INVOICED driver composes to resolve a
// finalized invoice to its owning order. The ar.invoice.finalized event carries only invoice_id; this
// lets the order module advance orders.status WITHOUT importing billing or JOINing the invoices table
// (module boundary: order does not read invoices). Satisfies order.InvoiceOrderResolver structurally
// (billingRepo is passed to order.NewLifecycleConsumer in the composition root).
package billing

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// OrderIDForInvoice returns the owning order_id of an invoice, or "" when the invoice is missing or not
// bound to an order (the caller treats "" as "nothing to advance"). Not tenant-scoped, the consumer is
// a system/event driver, and orders.id is opaque to the caller.
func (r *Repository) OrderIDForInvoice(ctx context.Context, invoiceID string) (string, error) {
	if r.pool == nil {
		return "", nil
	}
	var orderID string
	err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(order_id::text, '') FROM invoices WHERE id = $1`, invoiceID).Scan(&orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("resolve order for invoice %q: %w", invoiceID, err)
	}
	return orderID, nil
}

// InvoiceStandingForOrder is the order's live invoice and how much of it is paid, for the order
// module's yard notice (it declares the port and never reads the invoice tables itself). The newest
// non-void INVOICE document wins; found is false when the order has none. Paid is what the document
// has taken, its total less what is still outstanding.
func (r *Repository) InvoiceStandingForOrder(ctx context.Context, orderID string) (invoiceNo string, totalCents, paidCents int64, found bool, err error) {
	if r.pool == nil {
		return "", 0, 0, false, nil
	}
	err = r.pool.QueryRow(ctx, `
SELECT invoice_no::text, total_cents, total_cents - balance_cents
  FROM invoices
 WHERE order_id = $1 AND doc_type = 'INVOICE' AND status <> 'VOID'
 ORDER BY created_at DESC LIMIT 1`, orderID).Scan(&invoiceNo, &totalCents, &paidCents)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, 0, false, nil
		}
		return "", 0, 0, false, fmt.Errorf("invoice standing for order %q: %w", orderID, err)
	}
	return invoiceNo, totalCents, paidCents, true, nil
}
