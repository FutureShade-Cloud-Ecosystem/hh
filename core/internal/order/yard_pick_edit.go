package order

// yard_pick_edit.go, what an order edit does to a short line (the principal's ruling).
//
// A manager's edit of a short line's quantity, the yard's weigh door (which edits through the same
// door), a removal of the line, or an added line each change what the order IS, so the numbers a short
// pick froze (the line's ordered quantity and total, and the header's ordered subtotal) are no longer
// the order's. The rule:
//
//	an edit of a short line's quantity is a NEW ORDER QUANTITY. It clears that line's backorder and
//	its frozen ordered values in the same transaction, records that it did in the order's history, and
//	tells the order's rep; the money then follows the edit's own pricing. Removing a short line clears
//	it the same way. Any edit rebases the header's frozen ordered subtotal onto the order as it now
//	stands, so a later pick pro-rates from today's numbers and never from a stale total.
//
// Without this, a short to 3 of 5 followed by an edit to 10 and a pick of 4 billed 4/12 of the old
// frozen total instead of 4 at the agreed price.

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ClearedBackorder is a short line's backorder cleared by an edit.
type ClearedBackorder struct {
	LineNo      int
	Description string
	UOM         string
	ClearedQty  string
	NewQty      string
}

// BackorderClearedNotice is what the rep is told when an edit clears a backorder.
type BackorderClearedNotice struct {
	Order       YardOrderFacts
	LineNo      int
	Description string
	UOM         string
	ClearedQty  string
	NewQty      string
	ActorID     string
	// Key is unique to this clearing, so two edits are two messages.
	Key string
}

// BackorderNotifier is the consumer-declared seam over the messaging module's rep routing.
type BackorderNotifier interface {
	BackorderCleared(ctx context.Context, n BackorderClearedNotice) error
}

// WithBackorderNotifier injects the rep notice for a cleared backorder. Nil = no notice (the history
// row is still written).
func (s *Service) WithBackorderNotifier(n BackorderNotifier) *Service {
	s.backorderNotifier = n
	return s
}

// orderFactsStore reads the order's notice facts.
type orderFactsStore interface {
	YardOrderFactsOf(ctx context.Context, orderID string) (YardOrderFacts, error)
}

// YardOrderFactsOf reads who an order is for and what it totals.
func (r *Repository) YardOrderFactsOf(ctx context.Context, orderID string) (YardOrderFacts, error) {
	if r.pool == nil {
		return YardOrderFacts{}, errNoDB
	}
	var f YardOrderFacts
	if err := r.pool.QueryRow(ctx, `
SELECT order_no::text, customer_id::text, branch_id::text, COALESCE(salesperson_id::text, ''), total_cents
  FROM orders WHERE id = $1`, orderID).Scan(&f.OrderNo, &f.CustomerID, &f.BranchID, &f.SalespersonID, &f.TotalAfterCents); err != nil {
		return YardOrderFacts{}, fmt.Errorf("order facts for %s: %w", orderID, err)
	}
	f.TotalBeforeCents = f.TotalAfterCents
	return f, nil
}

// clearedHook records, on the edit's own transaction, that the edit cleared a backorder, and keeps
// what was cleared for the notice sent after commit.
func (s *Service) clearedHook(actor, orderID string, sink *[]ClearedBackorder) func(context.Context, pgx.Tx, ClearedBackorder) error {
	return func(ctx context.Context, tx pgx.Tx, c ClearedBackorder) error {
		*sink = append(*sink, c)
		return s.recordFieldChange(ctx, tx, actor, orderID, c.LineNo, "backorder_cleared", c.ClearedQty, "0.0000")
	}
}

// announceCleared tells the rep, best effort and after the edit committed.
func (s *Service) announceCleared(ctx context.Context, actor, orderID string, cleared []ClearedBackorder) {
	if s.backorderNotifier == nil || len(cleared) == 0 {
		return
	}
	of, ok := s.store.(orderFactsStore)
	if !ok {
		return
	}
	facts, err := of.YardOrderFactsOf(ctx, orderID)
	if err != nil {
		return
	}
	facts.OrderID = orderID
	for _, c := range cleared {
		_ = s.backorderNotifier.BackorderCleared(ctx, BackorderClearedNotice{
			Order: facts, LineNo: c.LineNo, Description: c.Description, UOM: c.UOM,
			ClearedQty: c.ClearedQty, NewQty: c.NewQty, ActorID: actor,
			Key: fmt.Sprintf("order.backorder_cleared:%s:%d:%d", orderID, c.LineNo, time.Now().UnixNano()),
		})
	}
}

// readShortTx reads a line's backorder under the caller's transaction; nil when it has none.
func readShortTx(ctx context.Context, tx pgx.Tx, orderID string, lineNo int, newQty string) (*ClearedBackorder, error) {
	var c ClearedBackorder
	var back string
	err := tx.QueryRow(ctx, `
SELECT description, uom_entered::text, backorder_entered::text, qty_entered::text
  FROM order_lines WHERE order_id = $1 AND line_no = $2`, orderID, lineNo).Scan(&c.Description, &c.UOM, &back, &c.NewQty)
	if err != nil {
		return nil, fmt.Errorf("read backorder of line %d: %w", lineNo, err)
	}
	if r, ok := ratOf(back); !ok || r.Sign() <= 0 {
		return nil, nil
	}
	c.LineNo, c.ClearedQty = lineNo, back
	if newQty != "" {
		c.NewQty = newQty
	}
	return &c, nil
}

// clearShortfallTx clears a line's backorder and frozen ordered values after an edit set its quantity.
func clearShortfallTx(ctx context.Context, tx pgx.Tx, orderID string, lineNo int) (*ClearedBackorder, error) {
	c, err := readShortTx(ctx, tx, orderID, lineNo, "")
	if err != nil || c == nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE order_lines SET backorder_entered = 0, backorder_base = 0 WHERE order_id = $1 AND line_no = $2`, orderID, lineNo); err != nil {
		return nil, fmt.Errorf("clear backorder of line %d: %w", lineNo, err)
	}
	// The row is KEPT and only reset to the line as the edit left it, never deleted: its revision keys
	// the rep's notices, and a deleted row would restart the count so a later short repeated an
	// earlier notice's key and went unannounced. While the line has no backorder the row is ignored.
	tag, err := tx.Exec(ctx, `
UPDATE order_line_shortfalls sf SET ordered_entered = ol.qty_entered, ordered_base = ol.qty_base,
       ordered_total_cents = ol.line_total_cents, reason = '', note = '', updated_at = now()
  FROM order_lines ol WHERE ol.id = sf.order_line_id AND ol.order_id = $1 AND ol.line_no = $2
   AND ol.qty_entered > 0 AND ol.qty_base > 0`, orderID, lineNo)
	if err != nil {
		return nil, fmt.Errorf("reset shortfall of line %d: %w", lineNo, err)
	}
	if tag.RowsAffected() == 0 {
		// A line with no quantity left has no ordered values to hold; its row goes.
		if _, err := tx.Exec(ctx, `DELETE FROM order_line_shortfalls WHERE order_id = $1 AND line_no = $2`, orderID, lineNo); err != nil {
			return nil, fmt.Errorf("clear shortfall of line %d: %w", lineNo, err)
		}
	}
	return c, nil
}

// rebaseShortfallBasisTx moves the header's frozen ordered subtotal onto the order as it now stands
// (each short line at its frozen ordered total, every other line at its own), and, when no line is
// short any more, the ordered tax onto the tax the order carries. A no-op for an order never short.
func rebaseShortfallBasisTx(ctx context.Context, tx pgx.Tx, orderID string) error {
	if _, err := tx.Exec(ctx, `
UPDATE order_shortfall_basis b SET
  ordered_subtotal_cents = COALESCE((
    SELECT SUM(CASE WHEN ol.backorder_base > 0 THEN COALESCE(sf.ordered_total_cents, ol.line_total_cents) ELSE ol.line_total_cents END)
      FROM order_lines ol LEFT JOIN order_line_shortfalls sf ON sf.order_line_id = ol.id
     WHERE ol.order_id = b.order_id), 0),
  ordered_tax_cents = CASE
    WHEN EXISTS (SELECT 1 FROM order_lines WHERE order_id = b.order_id AND backorder_base > 0) THEN b.ordered_tax_cents
    ELSE (SELECT tax_cents FROM orders WHERE id = b.order_id) END
WHERE b.order_id = $1`, orderID); err != nil {
		return fmt.Errorf("rebase shortfall basis: %w", err)
	}
	// The lines' ordered tax follows the same rule: retaken while the order is whole, only topped up
	// for an added line while it is not. Nothing to do for an order never short.
	var hasBasis, short bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM order_shortfall_basis WHERE order_id = $1),
	       EXISTS (SELECT 1 FROM order_lines WHERE order_id = $1 AND backorder_base > 0)`, orderID).Scan(&hasBasis, &short); err != nil {
		return fmt.Errorf("rebase shortfall basis: %w", err)
	}
	if !hasBasis {
		return nil
	}
	return snapshotLineTaxTx(ctx, tx, orderID, !short)
}
