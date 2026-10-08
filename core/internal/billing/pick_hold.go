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
// (billing is never imported by order; the module boundary holds). The order's
// total and the customer's terms are not billing's tables: they arrive through
// the two narrow seams below, bound at the composition root to the order and
// customer repositories, and only the invoice sums are read here.

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

// PickHoldOrders is the order half of the hold's facts, declared here and
// bound at the composition root to the order repository: the customer and the
// total of one order, on the caller's transaction. found is false for a
// missing order.
type PickHoldOrders interface {
	PickHoldOrderTx(ctx context.Context, tx pgx.Tx, orderID string) (customerID string, totalCents int64, found bool, err error)
}

// PickHoldCustomers is the customer half, bound to the customer repository:
// the account's payment terms in days, on the caller's transaction.
type PickHoldCustomers interface {
	PickHoldTermsDaysTx(ctx context.Context, tx pgx.Tx, customerID string) (days int, found bool, err error)
}

// WithPickHoldSources binds the two seams the hold reads the order's total and
// the customer's terms through. Unwired, the hold answers an error (never a
// clear), so a composition that forgot the binding fails loudly.
func (r *Repository) WithPickHoldSources(orders PickHoldOrders, customers PickHoldCustomers) *Repository {
	r.holdOrders, r.holdCustomers = orders, customers
	return r
}

// PickHoldForOrder answers the pay before release facts: held, the money still
// owed, the money paid so far, and the order total. A missing order answers a
// plain error (the caller surfaces it). The pool read runs in one read only
// repeatable read transaction so the three facts share a snapshot.
func (r *Repository) PickHoldForOrder(ctx context.Context, orderID string) (held bool, owedCents, paidCents, totalCents int64, err error) {
	if r.pool == nil {
		return false, 0, 0, 0, errNoDB
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return false, 0, 0, 0, fmt.Errorf("billing: pick hold begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return r.pickHoldForOrder(ctx, tx, orderID)
}

// PickHoldForOrderTx is the same read on the caller's transaction: the
// guarded writers that already hold one (the pickups sync confirm inside the
// transaction that seals the pickup, the order module's status writers under
// their row locks) read the hold there, so a payment route that locks the
// order row is ordered against the move (an NSF does not take the order row,
// so against it the window is narrowed, not closed), and no second pool
// connection is taken while the transaction holds its own.
func (r *Repository) PickHoldForOrderTx(ctx context.Context, tx pgx.Tx, orderID string) (held bool, owedCents, paidCents, totalCents int64, err error) {
	return r.pickHoldForOrder(ctx, tx, orderID)
}

func (r *Repository) pickHoldForOrder(ctx context.Context, tx pgx.Tx, orderID string) (held bool, owedCents, paidCents, totalCents int64, err error) {
	if r.holdOrders == nil || r.holdCustomers == nil {
		return false, 0, 0, 0, errors.New("billing: pick hold sources are not wired")
	}
	customerID, totalCents, found, err := r.holdOrders.PickHoldOrderTx(ctx, tx, orderID)
	if err != nil {
		return false, 0, 0, 0, err
	}
	if !found {
		return false, 0, 0, 0, fmt.Errorf("billing: pick hold: order %s not found", orderID)
	}
	terms, found, err := r.holdCustomers.PickHoldTermsDaysTx(ctx, tx, customerID)
	if err != nil {
		return false, 0, 0, 0, err
	}
	if !found {
		return false, 0, 0, 0, fmt.Errorf("billing: pick hold: customer %s not found", customerID)
	}
	m, err := orderInvoiceMoney(ctx, tx, orderID)
	if err != nil {
		return false, 0, 0, 0, fmt.Errorf("billing: pick hold for %s: %w", orderID, err)
	}
	owedCents, paidCents = m.OpenCents, m.PaidCents
	// The uninvoiced remainder is owed too under pay before release: nothing
	// has billed it and nothing has collected it.
	if rest := totalCents - m.InvoicedCents; rest > 0 {
		owedCents += rest
	}
	held = terms <= 0 && owedCents > 0
	return held, owedCents, paidCents, totalCents, nil
}
