package order

// release_open.go, item 10 Q5: a credit only return of goods that never left the shelf reduces the
// order's open lines. The order module owns the column (order_lines.released_base, 0383) and every
// reader of the open quantity; pos reaches it only through this seam, on the return's transaction.

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ReleaseOpenQuantityTx releases qtyBase of an order line from fulfilment: the yard is no longer asked
// for it. The quantity is capped at what is still open (quantity less fulfilled less already released),
// so a stale caller cannot push the CHECK. A line with nothing open is a no-op.
func (r *Repository) ReleaseOpenQuantityTx(ctx context.Context, tx pgx.Tx, orderLineID string, qtyBase float64) error {
	if qtyBase <= 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `
UPDATE order_lines
   SET released_base = LEAST(qty_base - fulfilled_base, released_base + $2::decimal), updated_at = now()
 WHERE id = $1::uuid AND qty_base - fulfilled_base - released_base > 0`, orderLineID, fmt.Sprintf("%.4f", qtyBase)); err != nil {
		return fmt.Errorf("order: release open quantity of line %s: %w", orderLineID, err)
	}
	return nil
}
