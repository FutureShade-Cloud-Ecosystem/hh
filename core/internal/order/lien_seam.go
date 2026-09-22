package order

// lien_seam.go, SCAFFOLD-owned holdback read seam on *order.Repository (same-package pool access; no edit to
// order/service.go). order owns order_lines, so the W6 M6 Lien Cockpit resolves an order's holdback status
// THROUGH here. Satisfies the lien OrderHoldbackReader seam. FROZEN signature, the M6 worker consumes it.

import (
	"context"
	"fmt"
)

// HoldbackStatusForOrder returns the aggregate holdback status of an order, read from
// order_lines.lien_holdback_status (0038): "RELEASED" only when every line is RELEASED, "HELD" if any line is
// still held, else "NONE". An order with no lines is "NONE".
func (r *Repository) HoldbackStatusForOrder(ctx context.Context, orderID string) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var held, releasedOrNone, total int
	err := r.pool.QueryRow(ctx,
		`SELECT
		   COUNT(*) FILTER (WHERE lien_holdback_status = 'HELD'),
		   COUNT(*) FILTER (WHERE lien_holdback_status IN ('RELEASED','NONE')),
		   COUNT(*)
		 FROM order_lines WHERE order_id = $1`, orderID,
	).Scan(&held, &releasedOrNone, &total)
	if err != nil {
		return "", fmt.Errorf("order: holdback status for order %q: %w", orderID, err)
	}
	switch {
	case total == 0:
		return "NONE", nil
	case held > 0:
		return "HELD", nil
	default:
		// Every line is RELEASED or NONE; report RELEASED if any retention ever applied, else NONE.
		var anyReleased int
		if err := r.pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM order_lines WHERE order_id = $1 AND lien_holdback_status = 'RELEASED'`, orderID,
		).Scan(&anyReleased); err != nil {
			return "", fmt.Errorf("order: holdback status for order %q: %w", orderID, err)
		}
		if anyReleased > 0 {
			return "RELEASED", nil
		}
		return "NONE", nil
	}
}
