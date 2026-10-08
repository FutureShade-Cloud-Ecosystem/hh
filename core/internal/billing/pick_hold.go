package billing

// pick_hold.go, item 9.3 (the row 1047 deposits work): the pay before release
// facts, read FROM THE MONEY for the order module's picking hold.
//
// The hold is computed, never stamped: owed is the unpaid balance of the
// order's invoices PLUS any part of the order's total nothing has invoiced yet
// (a converted cash on demand order owes its whole total before its first
// invoice exists, which is exactly the order the hold was missing on), and
// held is zero day terms with money outstanding. A credit customer's order
// (terms above zero) is never held by this rule, whatever it owes: it owes on
// terms, which is what terms are. The dispatch gate (CanDispatch) keeps its
// own reading, untouched.
//
// Satisfies order.PickHoldReader through the composition root's adapter
// (billing is never imported by order; the module boundary holds).

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// PickHoldWords is the mandated refusal sentence (the order module owns the
// copy its errors carry; this one is the words the composition root's adapter
// maps onto the order's facts).
const PickHoldWords = "This order cannot reach picking until the balance is paid."

// PickInProcessWords is the deposit chip: a deposit puts the order in process,
// and the hold stays until the balance is paid.
const PickInProcessWords = "In process: deposit taken"

// PickHoldForOrder answers the pay before release facts in one round trip:
// held, the money still owed, the money paid so far, and the order total. A
// missing order answers a plain error (the caller surfaces it).
func (r *Repository) PickHoldForOrder(ctx context.Context, orderID string) (held bool, owedCents, paidCents, totalCents int64, err error) {
	if r.pool == nil {
		return false, 0, 0, 0, errNoDB
	}
	var terms int
	var invoiced int64
	// invOwed: the open balances (FINALIZED or PARTIALLY_PAID, the same
	// statuses the dispatch gate and the aging read count). invoiced: what the
	// order's non draft documents bill. paid: what those documents have taken,
	// their totals less their balances.
	err = r.pool.QueryRow(ctx, `
		SELECT o.total_cents, c.payment_terms_days,
		       COALESCE(inv.owed, 0), COALESCE(inv.invoiced, 0), COALESCE(inv.paid, 0)
		  FROM orders o
		  JOIN customers c ON c.id = o.customer_id
		  LEFT JOIN (
		    SELECT order_id,
		           COALESCE(SUM(balance_cents) FILTER (WHERE status IN ('FINALIZED','PARTIALLY_PAID')), 0) AS owed,
		           COALESCE(SUM(total_cents) FILTER (WHERE status <> 'DRAFT'), 0) AS invoiced,
		           COALESCE(SUM(total_cents - balance_cents) FILTER (WHERE status <> 'DRAFT'), 0) AS paid
		      FROM invoices WHERE order_id = $1::uuid GROUP BY order_id
		  ) inv ON inv.order_id = o.id
		 WHERE o.id = $1::uuid`, orderID).
		Scan(&totalCents, &terms, &owedCents, &invoiced, &paidCents)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, 0, 0, 0, fmt.Errorf("billing: pick hold: order %s not found", orderID)
		}
		return false, 0, 0, 0, fmt.Errorf("billing: pick hold for %s: %w", orderID, err)
	}
	// The uninvoiced remainder is owed too under pay before release: nothing
	// has billed it and nothing has collected it.
	if rest := totalCents - invoiced; rest > 0 {
		owedCents += rest
	}
	held = terms <= 0 && owedCents > 0
	return held, owedCents, paidCents, totalCents, nil
}
