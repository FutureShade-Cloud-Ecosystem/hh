package order

// yard_pick_plan.go, a short pick on an order that has a shipment plan (migration 0126).
//
// The plan's groups must PARTITION each order line: the group quantities sum to the line's qty_base,
// held by a deferred constraint at commit. A short pick lowers qty_base, so the plan has to give back
// the same quantity or the commit is refused. The rule, decided here:
//
//	the shortfall is taken from the line's UNSHIPPED groups (PLANNED, PICKING, READY), the newest
//	group first, and a group left with nothing on it stays as an empty truck; a group that has been
//	DISPATCHED or COMPLETED is never touched. If the unshipped groups cannot give back the whole
//	shortfall, the quantity already out the door cannot be taken back and the pick is refused in
//	words (ErrLineInUse, 409), never by the constraint. A correction (the yard finds the rest) puts
//	the quantity back on the newest unshipped group, preferring one that already carries the line.
//
// THE GROUP IS LOCKED TOO (FOR UPDATE OF sgl, g), and its status is read under that lock: a dispatch
// that has not committed holds the group's row, so the pick waits for it and then no longer sees a
// READY group, where reading only the group lines let a truck that was leaving be shrunk. The lock
// order is the order row, the order lines, the group lines, then the groups, the order giveToPlan and
// ReplaceShipmentPlan already take; a dispatch takes only the one group row, and nothing here touches
// payments or invoices, so no cycle forms with the lock order of row 821.
//
// The group invoice path pro-rates a line's money by the group quantities, so with the plan shrunk
// along with the line the progress invoices still sum to the (reduced) line exactly.

import (
	"context"
	"fmt"
	"math/big"

	"github.com/jackc/pgx/v5"
)

// unshippedGroupStatuses are the group statuses whose quantity has not gone out.
var unshippedGroupStatuses = []string{"PLANNED", "PICKING", "READY"}

// adjustPlanTx moves the line's shipment-group quantity by delta (negative for a shortfall) under the
// caller's transaction. A no-op for an order with no live shipment plan.
func adjustPlanTx(ctx context.Context, tx pgx.Tx, orderID, lineID string, lineNo int, delta *big.Rat) error {
	if delta.Sign() == 0 {
		return nil
	}
	var live bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM shipment_groups WHERE order_id = $1 AND status <> 'CANCELLED')`, orderID).Scan(&live); err != nil {
		return fmt.Errorf("yard pick: plan probe: %w", err)
	}
	if !live {
		return nil
	}
	if delta.Sign() < 0 {
		return takeFromPlan(ctx, tx, lineID, lineNo, new(big.Rat).Neg(delta))
	}
	return giveToPlan(ctx, tx, orderID, lineID, lineNo, delta)
}

func takeFromPlan(ctx context.Context, tx pgx.Tx, lineID string, lineNo int, need *big.Rat) error {
	type held struct {
		id  string
		qty *big.Rat
	}
	rows, err := tx.Query(ctx, `
SELECT sgl.id::text, sgl.qty_base::text
  FROM shipment_group_lines sgl JOIN shipment_groups g ON g.id = sgl.shipment_group_id
 WHERE sgl.order_line_id = $1 AND g.status = ANY($2)
 ORDER BY g.group_no DESC FOR UPDATE OF sgl, g`, lineID, unshippedGroupStatuses)
	if err != nil {
		return fmt.Errorf("yard pick: plan read: %w", err)
	}
	var groups []held
	for rows.Next() {
		var id, q string
		if err := rows.Scan(&id, &q); err != nil {
			rows.Close()
			return fmt.Errorf("yard pick: plan scan: %w", err)
		}
		r, _ := ratOf(q)
		groups = append(groups, held{id, r})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("yard pick: plan rows: %w", err)
	}
	for _, g := range groups {
		if need.Sign() == 0 {
			break
		}
		take := g.qty
		if need.Cmp(take) < 0 {
			take = new(big.Rat).Set(need)
		}
		rest := new(big.Rat).Sub(g.qty, take)
		if rest.Sign() == 0 {
			_, err = tx.Exec(ctx, `DELETE FROM shipment_group_lines WHERE id = $1`, g.id)
		} else {
			_, err = tx.Exec(ctx, `UPDATE shipment_group_lines SET qty_base = $2::numeric WHERE id = $1`, g.id, rat4(rest))
		}
		if err != nil {
			return fmt.Errorf("yard pick: plan shrink: %w", err)
		}
		need = new(big.Rat).Sub(need, take)
	}
	if need.Sign() > 0 {
		return fmt.Errorf("%w: line %d has %s on shipment groups that have already gone out, which a short pick cannot take back; change the shipment plan first",
			ErrLineInUse, lineNo, rat4(need))
	}
	return nil
}

func giveToPlan(ctx context.Context, tx pgx.Tx, orderID, lineID string, lineNo int, add *big.Rat) error {
	var groupID string
	err := tx.QueryRow(ctx, `
SELECT g.id::text FROM shipment_groups g
 WHERE g.order_id = $1 AND g.status = ANY($2)
 ORDER BY (EXISTS (SELECT 1 FROM shipment_group_lines x WHERE x.shipment_group_id = g.id AND x.order_line_id = $3)) DESC,
          g.group_no DESC
 LIMIT 1 FOR UPDATE OF g`, orderID, unshippedGroupStatuses, lineID).Scan(&groupID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return fmt.Errorf("%w: line %d has no unshipped shipment group to take the restored %s; change the shipment plan first",
				ErrLineInUse, lineNo, rat4(add))
		}
		return fmt.Errorf("yard pick: plan target: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO shipment_group_lines (shipment_group_id, order_line_id, qty_base) VALUES ($1, $2, $3::numeric)
ON CONFLICT (shipment_group_id, order_line_id) DO UPDATE SET qty_base = shipment_group_lines.qty_base + EXCLUDED.qty_base`,
		groupID, lineID, rat4(add)); err != nil {
		return fmt.Errorf("yard pick: plan restore: %w", err)
	}
	return nil
}
