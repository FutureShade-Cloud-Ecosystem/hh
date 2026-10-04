package quote

// order_payment_repository.go, QD-5: the one transaction, the reads and the per quote lock the
// payment door needs.
// Only the quote's own tables are read here (quotes, quote_lines, quote_accept_provenance) and the
// customer's credit limit the conversion gate already reads; the order, the invoice and the payment
// are reached through the seams in order_payment.go.

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// OrderPaymentFactsTx reads the quote's status, owner, branch, figures, lines sum, the customer's
// credit limit and the orders its acceptances became, on the door's transaction.
func (r *Repository) OrderPaymentFactsTx(ctx context.Context, tx pgx.Tx, quoteID string) (OrderPaymentFacts, error) {
	if !looksLikeUUID(quoteID) {
		return OrderPaymentFacts{}, ErrNotFound
	}
	var f OrderPaymentFacts
	err := tx.QueryRow(ctx, `
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
	rows, err := tx.Query(ctx, `
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

// InOrderPaymentTx runs one payment action in ONE transaction on ONE pool connection (QD-5).
//
// The first statement is pg_advisory_xact_lock on the quote's name, taken INSIDE the transaction, so
// the lock is not a second connection and not a session lock that can outlive a failure: it is held
// until commit or rollback and then gone. Calls on one quote (same key or different keys) therefore
// run one after another, each seeing the previous one's committed rows, while calls on other quotes
// proceed beside them, every call holding exactly the one connection its transaction is. A pool of
// any size serves any number of parallel callers; they queue for the connection, never for each
// other's second connection.
//
// fn's ctx carries the transaction for the conversion (withTx); tx is the same transaction for the
// seams. A nil return commits; any error rolls back whole.
func (r *Repository) InOrderPaymentTx(ctx context.Context, quoteID string, fn func(ctx context.Context, tx pgx.Tx) error) error {
	if r.pool == nil {
		return errNoDB
	}
	// Admission: at most pool size minus one actions hold a connection at once, so one connection is
	// always free for the nested pool read the stock seam makes while a conversion holds this
	// transaction (inventory's allocation policy lookup is on the pool, not the transaction). Without
	// it a burst of conversions as large as the pool would each wait for a connection no one can free.
	gate := paymentGate(r.pool)
	select {
	case gate <- struct{}{}:
		defer func() { <-gate }()
	case <-ctx.Done():
		return fmt.Errorf("order payment: waiting for a connection: %w", ctx.Err())
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("order payment: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		"quote-order-payment:"+quoteID); err != nil {
		return fmt.Errorf("order payment lock: %w", err)
	}
	if err := fn(withTx(ctx, tx), tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("order payment: commit: %w", err)
	}
	return nil
}

// paymentGates holds one admission gate per pool, sized pool max minus one (at least one).
var paymentGates sync.Map // *pgxpool.Pool -> chan struct{}

func paymentGate(pool *pgxpool.Pool) chan struct{} {
	if g, ok := paymentGates.Load(pool); ok {
		return g.(chan struct{})
	}
	n := int(pool.Config().MaxConns) - 1
	if n < 1 {
		n = 1
	}
	g, _ := paymentGates.LoadOrStore(pool, make(chan struct{}, n))
	return g.(chan struct{})
}

// txCtxKey carries the door's transaction to the conversion, which the quote package owns and which is
// called through the one conversion path (runConvert) with its established signature.
type txCtxKey struct{}

// withTx returns ctx carrying tx for the conversion to run on.
func withTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, txCtxKey{}, tx)
}

// txFrom returns the transaction ctx carries, if any.
func txFrom(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txCtxKey{}).(pgx.Tx)
	return tx, ok && tx != nil
}
