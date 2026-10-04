package quote

// order_payment_repository.go, QD-5: the reads and the per quote lock the payment door needs.
// Only the quote's own tables are read here (quotes, quote_lines, quote_accept_provenance) and the
// customer's credit limit the conversion gate already reads; the order, the invoice and the payment
// are reached through the seams in order_payment.go.

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// OrderPaymentFacts reads the quote's status, owner, branch, figures, lines sum, the customer's
// credit limit and the orders its acceptances became.
func (r *Repository) OrderPaymentFacts(ctx context.Context, quoteID string) (OrderPaymentFacts, error) {
	if r.pool == nil {
		return OrderPaymentFacts{}, errNoDB
	}
	if !looksLikeUUID(quoteID) {
		return OrderPaymentFacts{}, ErrNotFound
	}
	var f OrderPaymentFacts
	err := r.pool.QueryRow(ctx, `
		SELECT q.status, q.customer_id::text, q.branch_id::text, q.subtotal_cents, q.tax_cents,
		       q.delivery_fee_cents, q.total_cents,
		       COALESCE((SELECT SUM(line_total_cents) FROM quote_lines WHERE quote_id = q.id), 0)::bigint,
		       COALESCE(c.credit_limit_cents, 0)
		  FROM quotes q JOIN customers c ON c.id = q.customer_id
		 WHERE q.id = $1::uuid`, quoteID).
		Scan(&f.Status, &f.CustomerID, &f.BranchID, &f.SubtotalCents, &f.TaxCents, &f.FeeCents,
			&f.TotalCents, &f.LinesCents, &f.CreditLimitCents)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OrderPaymentFacts{}, ErrNotFound
		}
		return OrderPaymentFacts{}, fmt.Errorf("order payment facts for quote %q: %w", quoteID, err)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT order_id::text, order_no FROM quote_accept_provenance
		 WHERE quote_id = $1::uuid ORDER BY accept_seq`, quoteID)
	if err != nil {
		return OrderPaymentFacts{}, fmt.Errorf("order payment orders for quote %q: %w", quoteID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var o PaymentOrderRef
		if err := rows.Scan(&o.ID, &o.No); err != nil {
			return OrderPaymentFacts{}, err
		}
		f.Orders = append(f.Orders, o)
	}
	return f, rows.Err()
}

// LockOrderPayments takes one session advisory lock per quote on a dedicated connection and returns
// the release. All payment actions on a quote run under it.
func (r *Repository) LockOrderPayments(ctx context.Context, quoteID string) (func(), error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("order payment lock: %w", err)
	}
	name := "quote-order-payment:" + quoteID
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, name); err != nil {
		conn.Release()
		return nil, fmt.Errorf("order payment lock: %w", err)
	}
	return func() {
		bg := context.WithoutCancel(ctx)
		if _, err := conn.Exec(bg, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, name); err != nil {
			// A connection that could not unlock must not return to the pool holding the lock.
			_ = conn.Conn().Close(bg)
		}
		conn.Release()
	}, nil
}
