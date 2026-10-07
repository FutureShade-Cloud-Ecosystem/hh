package order

// yard_pick_tax.go, the per line tax of a shorted order (migration 0369).
//
// Each order line carries its own tax (order_lines.tax_cents, the tax engine's split of the header
// tax) and a return reads it, so when the header tax is pro-rated for a short the lines' tax must
// follow, and the lines must still sum to the header. The split is the same largest remainder rule:
// each line's weight is its ORIGINAL tax scaled by how much of its ORIGINAL total it now carries (a
// whole line is its own original), so a correction returns every line to exactly the tax it started
// with. The originals are the frozen ones on order_line_shortfalls; a line never short is its own.
//
// It runs only when the lines' original tax adds up to the header's original tax, as an order the
// tax engine split does. An order whose lines carry no tax (tax held on the header alone) or a split
// that disagrees with its header is left exactly as it is: re-splitting a figure nothing can vouch
// for would invent tax on lines.

import (
	"context"
	"fmt"
	"math/big"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/moneyutil"
)

type taxedLine struct {
	id                                      string
	total, tax, origTotal, origTax, current int64
}

// snapshotLineTaxTx takes every line's ordered tax: replacing the old ones when the order is whole
// (refresh), only adding the ones a line lacks when it is not (an added line).
func snapshotLineTaxTx(ctx context.Context, tx pgx.Tx, orderID string, refresh bool) error {
	var conflict string
	if refresh {
		conflict = "(order_line_id) DO UPDATE SET ordered_tax_cents = EXCLUDED.ordered_tax_cents"
	} else {
		conflict = "(order_line_id) DO NOTHING"
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO order_line_tax_basis (order_line_id, order_id, ordered_tax_cents)
SELECT id, order_id, tax_cents FROM order_lines WHERE order_id = $1
ON CONFLICT `+conflict, orderID); err != nil {
		return fmt.Errorf("yard pick: line tax basis: %w", err)
	}
	return nil
}

// reallocLineTaxTx re-splits the header's new tax across the order's lines under the caller's
// transaction.
func reallocLineTaxTx(ctx context.Context, tx pgx.Tx, orderID string, basisTax, newTax int64) error {
	rows, err := tx.Query(ctx, `
SELECT ol.id::text, ol.line_total_cents, ol.tax_cents,
       CASE WHEN ol.backorder_base > 0 THEN COALESCE(sf.ordered_total_cents, ol.line_total_cents) ELSE ol.line_total_cents END,
       COALESCE(tb.ordered_tax_cents, ol.tax_cents)
  FROM order_lines ol LEFT JOIN order_line_shortfalls sf ON sf.order_line_id = ol.id
  LEFT JOIN order_line_tax_basis tb ON tb.order_line_id = ol.id
 WHERE ol.order_id = $1 ORDER BY ol.line_no`, orderID)
	if err != nil {
		return fmt.Errorf("yard pick: line tax read: %w", err)
	}
	var lines []taxedLine
	var origSum int64
	for rows.Next() {
		var l taxedLine
		if err := rows.Scan(&l.id, &l.total, &l.current, &l.origTotal, &l.origTax); err != nil {
			rows.Close()
			return fmt.Errorf("yard pick: line tax scan: %w", err)
		}
		origSum += l.origTax
		lines = append(lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if origSum == 0 || origSum != basisTax {
		return nil
	}
	weights := make([]int64, len(lines))
	for i, l := range lines {
		if l.origTotal <= 0 {
			continue
		}
		// weight = origTax * (total / origTotal), kept to a hundredth of a cent
		w := new(big.Rat).SetFrac64(l.origTax*l.total*100, l.origTotal)
		weights[i] = new(big.Int).Quo(w.Num(), w.Denom()).Int64()
	}
	for i, share := range moneyutil.AllocateLargestRemainder(newTax, weights) {
		if share == lines[i].current {
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE order_lines SET tax_cents = $2 WHERE id = $1`, lines[i].id, share); err != nil {
			return fmt.Errorf("yard pick: line tax write: %w", err)
		}
	}
	return nil
}
