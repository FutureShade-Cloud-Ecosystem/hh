// order_cancel_seam.go, B2/R2: the sale half of the order module's cancel predicate. A manual order
// CANCELLED is refused when money is already attached; this answers "does this order have a committed
// counter sale?" without the order module importing pos or reading pos_sale (module boundary).
// Satisfies order.CommittedSaleReader structurally, posRepo is passed to orderSvc.WithCancelGuards in
// the composition root, the same way it already satisfies gl.TaxSplitReader (gl_seam.go).
package pos

import (
	"context"
	"fmt"
)

// OrderHasCommittedPOSSale reports whether the order has a COMMITTED pos_sale.
//
// pos_sale.status ∈ (COMMITTED, VOIDED). Only COMMITTED counts: a VOIDED sale has already been unwound,
// so it is not money still attached to the order. The read is covered by idx_pos_sale_order.
//
// A read failure is returned, never swallowed, an unreadable predicate must not be mistaken for
// permission to cancel (fail closed).
func (r *Repository) OrderHasCommittedPOSSale(ctx context.Context, orderID string) (bool, error) {
	if r.pool == nil {
		return false, fmt.Errorf("pos: no database configured for the order cancel predicate")
	}
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pos_sale
			WHERE order_id = $1::uuid AND status = 'COMMITTED'
		)`, orderID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check committed pos_sale for order %q: %w", orderID, err)
	}
	return exists, nil
}
