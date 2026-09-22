package pos

// gl_seam.go, SCAFFOLD-owned GL read seam on *pos.Repository. pos owns tax_decision_snapshot (0029), so the
// GL posting consumer (A-01) resolves the FN relief split for the tax.relief.assessed posting THROUGH here.
// Satisfies gl.TaxSplitReader.

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// TaxSplit returns the FN relief split recorded on an order's most-recent tax_decision_snapshot: the full HST
// (2200 liability), the on-credit portion (1165 FN relief recoverable), and the net collected. found is false
// when the order has no snapshot (a standard-rate sale), the consumer then posts no relief legs.
func (r *Repository) TaxSplit(ctx context.Context, orderID string) (full, onCredit, net int64, found bool, err error) {
	if r.pool == nil {
		return 0, 0, 0, false, errNoPool
	}
	err = r.pool.QueryRow(ctx,
		`SELECT full_hst_cents, on_credit_cents, net_collected_cents
		 FROM tax_decision_snapshot
		 WHERE order_id = $1
		 ORDER BY created_at DESC
		 LIMIT 1`, orderID,
	).Scan(&full, &onCredit, &net)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, 0, false, fmt.Errorf("pos: tax split for order %q: %w", orderID, err)
	}
	return full, onCredit, net, true, nil
}
