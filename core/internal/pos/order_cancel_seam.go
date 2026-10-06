// order_cancel_seam.go, B2/R2: the sale half of the order module's cancel predicate. A manual order
// CANCELLED is refused when money is already attached; this answers "does this order have a committed
// counter sale?" without the order module importing pos or reading pos_sale (module boundary).
// Satisfies order.CommittedSaleReader and order.CommittedSaleTxReader structurally, posRepo is passed
// to orderSvc.WithCancelGuards in the composition root, the same way it already satisfies
// gl.TaxSplitReader (gl_seam.go).
package pos

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// OrderHasCommittedPOSSale reports whether the order has a COMMITTED pos_sale.
//
// pos_sale.status ∈ (COMMITTED, VOIDED). Only COMMITTED counts: a VOIDED sale has already been unwound,
// so it is not money still attached to the order. The read is covered by idx_pos_sale_order.
//
// A read failure is returned, never swallowed, an unreadable predicate must not be mistaken for
// permission to cancel (fail closed).
func (r *Repository) OrderHasCommittedPOSSale(ctx context.Context, orderID string) (bool, error) {
	return r.OrderHasCommittedPOSSaleTx(ctx, nil, orderID)
}

// OrderHasCommittedPOSSaleTx is OrderHasCommittedPOSSale joined to the caller's transaction (nil tx
// means the pool, the shape of the tree's other store seams). The order module runs the cancel
// predicate INSIDE its own order transaction; reading through the pool there takes a second connection
// while the first is still held, and pool-size callers in flight together starve each other with no
// lock waited on anywhere (the sessions sit idle in transaction, so the database cannot see it). The
// transaction-joining read answers through the caller's transaction instead; every caller without a
// transaction (an HTTP read) keeps the pool method.
func (r *Repository) OrderHasCommittedPOSSaleTx(ctx context.Context, tx pgx.Tx, orderID string) (bool, error) {
	if r.pool == nil {
		// No pool means we cannot prove absence. Report the error so the order service fails closed
		// rather than reading a bare false as "no money attached".
		return false, fmt.Errorf("pos: no database configured for the order cancel predicate")
	}
	const q = `
		SELECT EXISTS (
			SELECT 1 FROM pos_sale
			WHERE order_id = $1::uuid AND status = 'COMMITTED'
		)`
	var exists bool
	var err error
	if tx != nil {
		err = tx.QueryRow(ctx, q, orderID).Scan(&exists)
	} else {
		err = r.pool.QueryRow(ctx, q, orderID).Scan(&exists)
	}
	if err != nil {
		return false, fmt.Errorf("check committed pos_sale for order %q: %w", orderID, err)
	}
	return exists, nil
}
