package billing

// pay_balance_seam.go, the invoice reads and locks the POS Pay balance door
// (item 9.4) composes with the order, customer and payments reads. The
// invoices table is this module's, so the door asks here for what the order's
// documents bill, hold and owe, and never reads the table itself. Every method
// takes the caller's transaction (the door opens a read only one for its
// advisory reads).
//
// The owed predicate is the ONE the pick hold shares: the open balances of
// live INVOICE documents, and what those documents bill. A void document bills
// nothing, a draft has not billed yet, and a credit memo is its own document,
// never a skew on either sum.

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// OrderInvoiceMoneyRow is what one order's live INVOICE documents hold:
// OpenCents the balances still open (FINALIZED or PARTIALLY_PAID), InvoicedCents
// what they bill, PaidCents what they have taken (billed less open).
type OrderInvoiceMoneyRow struct {
	OpenCents     int64
	InvoicedCents int64
	PaidCents     int64
}

const orderInvoiceMoneySQL = `
	SELECT order_id::text,
	       COALESCE(SUM(balance_cents) FILTER (WHERE doc_type = 'INVOICE' AND status IN ('FINALIZED','PARTIALLY_PAID') AND balance_cents > 0), 0),
	       COALESCE(SUM(total_cents) FILTER (WHERE doc_type = 'INVOICE' AND status NOT IN ('DRAFT','VOID')), 0),
	       COALESCE(SUM(total_cents - balance_cents) FILTER (WHERE doc_type = 'INVOICE' AND status NOT IN ('DRAFT','VOID')), 0)
	  FROM invoices
	 WHERE order_id = ANY($1::uuid[])
	 GROUP BY order_id`

// OrderInvoiceMoney answers the invoice money of each order id, keyed by order
// id; an order with no invoice is absent (all three figures zero).
func (r *Repository) OrderInvoiceMoney(ctx context.Context, tx pgx.Tx, orderIDs []string) (map[string]OrderInvoiceMoneyRow, error) {
	rows, err := tx.Query(ctx, orderInvoiceMoneySQL, orderIDs)
	if err != nil {
		return nil, fmt.Errorf("billing: order invoice money: %w", err)
	}
	defer rows.Close()
	out := make(map[string]OrderInvoiceMoneyRow, len(orderIDs))
	for rows.Next() {
		var id string
		var m OrderInvoiceMoneyRow
		if err := rows.Scan(&id, &m.OpenCents, &m.InvoicedCents, &m.PaidCents); err != nil {
			return nil, fmt.Errorf("billing: scan order invoice money: %w", err)
		}
		out[id] = m
	}
	return out, rows.Err()
}

// orderInvoiceMoney is the one order's figures, zero when it has no invoice.
func orderInvoiceMoney(ctx context.Context, tx pgx.Tx, orderID string) (OrderInvoiceMoneyRow, error) {
	var m OrderInvoiceMoneyRow
	rows, err := tx.Query(ctx, orderInvoiceMoneySQL, []string{orderID})
	if err != nil {
		return m, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id, &m.OpenCents, &m.InvoicedCents, &m.PaidCents); err != nil {
			return OrderInvoiceMoneyRow{}, err
		}
	}
	return m, rows.Err()
}

// OrderInvoiceIDs lists every invoice id of each order (any document type and
// status: the money trail walks the allocations to all of them), keyed by order
// id.
func (r *Repository) OrderInvoiceIDs(ctx context.Context, tx pgx.Tx, orderIDs []string) (map[string][]string, error) {
	rows, err := tx.Query(ctx,
		`SELECT order_id::text, id::text FROM invoices WHERE order_id = ANY($1::uuid[]) ORDER BY created_at, id`, orderIDs)
	if err != nil {
		return nil, fmt.Errorf("billing: order invoice ids: %w", err)
	}
	defer rows.Close()
	out := make(map[string][]string, len(orderIDs))
	for rows.Next() {
		var orderID, invoiceID string
		if err := rows.Scan(&orderID, &invoiceID); err != nil {
			return nil, fmt.Errorf("billing: scan order invoice id: %w", err)
		}
		out[orderID] = append(out[orderID], invoiceID)
	}
	return out, rows.Err()
}

// OrderIDForInvoiceTx resolves an invoice to its order on the caller's
// transaction ("" when the invoice is missing or not bound to an order).
func (r *Repository) OrderIDForInvoiceTx(ctx context.Context, tx pgx.Tx, invoiceID string) (string, error) {
	var orderID string
	rows, err := tx.Query(ctx, `SELECT COALESCE(order_id::text, '') FROM invoices WHERE id = $1::uuid`, invoiceID)
	if err != nil {
		return "", fmt.Errorf("billing: order for invoice %q: %w", invoiceID, err)
	}
	defer rows.Close()
	for rows.Next() {
		if err := rows.Scan(&orderID); err != nil {
			return "", err
		}
	}
	return orderID, rows.Err()
}

// OpenOrderInvoice is one open invoice of an order, locked FOR NO KEY UPDATE.
type OpenOrderInvoice struct {
	InvoiceID    string
	InvoiceNo    string
	BalanceCents int64
}

// LockOpenOrderInvoicesTx locks the order's open invoice rows FOR NO KEY UPDATE
// in ascending id order, the money lock order's third step
// (payments/LOCK-ORDER.md), and answers them. The lock predicate is the one
// the advisory sums use (live INVOICE documents with a positive open balance),
// so the balance the locks re-derive is the balance the pre read refused or
// captured against.
func (r *Repository) LockOpenOrderInvoicesTx(ctx context.Context, tx pgx.Tx, orderID string) ([]OpenOrderInvoice, error) {
	rows, err := tx.Query(ctx, `
		SELECT id::text, invoice_no::text, balance_cents
		  FROM invoices
		 WHERE order_id = $1::uuid AND doc_type = 'INVOICE' AND status IN ('FINALIZED','PARTIALLY_PAID') AND balance_cents > 0
		 ORDER BY id ASC
		 FOR NO KEY UPDATE`, orderID)
	if err != nil {
		return nil, fmt.Errorf("billing: lock open order invoices: %w", err)
	}
	defer rows.Close()
	var out []OpenOrderInvoice
	for rows.Next() {
		var inv OpenOrderInvoice
		if err := rows.Scan(&inv.InvoiceID, &inv.InvoiceNo, &inv.BalanceCents); err != nil {
			return nil, fmt.Errorf("billing: scan open order invoice: %w", err)
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}
