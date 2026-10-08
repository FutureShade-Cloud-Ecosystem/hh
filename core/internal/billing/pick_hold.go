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

// pickHoldQuerier is the read surface the pool and a caller's transaction
// share, so the one hold read serves the pool readers (the order read, the
// will call doors) and the guarded writers that ask inside their own
// transaction (a sync confirm sealing its pickup, an order status move).
type pickHoldQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// PickHoldForOrder answers the pay before release facts in one round trip:
// held, the money still owed, the money paid so far, and the order total. A
// missing order answers a plain error (the caller surfaces it).
func (r *Repository) PickHoldForOrder(ctx context.Context, orderID string) (held bool, owedCents, paidCents, totalCents int64, err error) {
	if r.pool == nil {
		return false, 0, 0, 0, errNoDB
	}
	return pickHoldForOrder(ctx, r.pool, orderID)
}

// PickHoldForOrderTx is the same read on the caller's transaction: the
// guarded writers that already hold one (the pickups sync confirm inside the
// transaction that seals the pickup, the order module's status writers under
// their row locks) read the hold there, so a payment landing beside them
// cannot slip between the read and the move, and no second pool connection
// is taken while the transaction holds its own.
func (r *Repository) PickHoldForOrderTx(ctx context.Context, tx pgx.Tx, orderID string) (held bool, owedCents, paidCents, totalCents int64, err error) {
	return pickHoldForOrder(ctx, tx, orderID)
}

func pickHoldForOrder(ctx context.Context, db pickHoldQuerier, orderID string) (held bool, owedCents, paidCents, totalCents int64, err error) {
	var terms int
	var invoiced int64
	// invOwed: the open balances (FINALIZED or PARTIALLY_PAID, the same
	// statuses the dispatch gate and the aging read count). invoiced: what the
	// order's live INVOICE documents bill. paid: what those documents have
	// taken, their totals less their balances. All three count only INVOICE
	// rows that are neither DRAFT nor VOID, the predicate order_seam.go and
	// invoice_writer.go hold: a voided invoice billed nothing (the order still
	// owes its whole total), and a credit memo is its own document, never a
	// skew on the order's invoiced or paid figures.
	err = db.QueryRow(ctx, `
		SELECT o.total_cents, c.payment_terms_days,
		       COALESCE(inv.owed, 0), COALESCE(inv.invoiced, 0), COALESCE(inv.paid, 0)
		  FROM orders o
		  JOIN customers c ON c.id = o.customer_id
		  LEFT JOIN (
		    SELECT order_id,
		           COALESCE(SUM(balance_cents) FILTER (WHERE doc_type = 'INVOICE' AND status IN ('FINALIZED','PARTIALLY_PAID')), 0) AS owed,
		           COALESCE(SUM(total_cents) FILTER (WHERE doc_type = 'INVOICE' AND status NOT IN ('DRAFT','VOID')), 0) AS invoiced,
		           COALESCE(SUM(total_cents - balance_cents) FILTER (WHERE doc_type = 'INVOICE' AND status NOT IN ('DRAFT','VOID')), 0) AS paid
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
