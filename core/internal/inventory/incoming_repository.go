package inventory

// incoming_repository.go, the pgx half of DIB-460's in-transit detail read.
//
// The predicate is the SAME one the four-bucket read's in_transit lateral uses (visibility.go) and
// the same one the DIB-72 worklist's inboundTransferQty uses: IN_TRANSIT status, inbound to the
// branch, with units still on the truck. Three readers, one definition of "on a truck", if this
// query and that lateral ever disagreed, a card would show a line of 15 next to a bucket of 0.

import (
	"context"
	"fmt"
)

// InTransitLinesForProduct lists the open transfer legs bringing a product into the reach.
//
// The branch predicate is on to_branch_id: the reach is about which YARDS a caller may look into,
// and an inbound leg belongs to its destination. `($2::uuid[] IS NULL OR …)` with NULL meaning
// global, and an EMPTY array matching nothing, the fence fails closed.
func (r *Repository) InTransitLinesForProduct(ctx context.Context, productID string, reach BranchReach) ([]InTransitLine, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT t.id::text, t.transfer_no::text,
		       t.from_branch_id::text, fb.code::text, fb.name,
		       t.to_branch_id::text, tb.code::text,
		       (tl.qty_shipped_base - tl.qty_received_base)::float8,
		       tl.uom::text,
		       COALESCE(to_char(t.shipped_at, 'YYYY-MM-DD'), ''),
		       -- DIB-472: the stored promise, '' when there is none. Same spelling as the shipped date
		       -- and as purchasing's expected_at, so ONE browser formatter renders all three.
		       COALESCE(to_char(t.eta, 'YYYY-MM-DD'), '')
		  FROM stock_transfer_lines tl
		  JOIN stock_transfers t ON t.id = tl.transfer_id
		  JOIN locations fb ON fb.id = t.from_branch_id
		  JOIN locations tb ON tb.id = t.to_branch_id
		 WHERE tl.product_id = $1::uuid
		   AND t.status = 'IN_TRANSIT'
		   AND tl.qty_shipped_base > tl.qty_received_base
		   AND ($2::uuid[] IS NULL OR t.to_branch_id = ANY($2::uuid[]))
		 ORDER BY t.shipped_at, t.transfer_no, tl.line_no`, productID, reach.branchFilter())
	if err != nil {
		return nil, fmt.Errorf("inventory: in-transit lines for product: %w", err)
	}
	defer rows.Close()
	var out []InTransitLine
	for rows.Next() {
		var l InTransitLine
		if err := rows.Scan(&l.TransferID, &l.TransferNo, &l.FromBranchID, &l.FromCode, &l.FromName,
			&l.ToBranchID, &l.ToCode, &l.QtyBase, &l.UOM, &l.ShippedAt, &l.ETA); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ReplenishLineFor reads ONE managed replenishment line with its live position, the shortfall
// worklist's row for a single (product, branch), WITHOUT the "is it short" filter.
//
// It is the same query fragment (inboundTransferQty), the same DIB-470 on-order seam, and the same
// Go arithmetic (ShortfallRow.Derive) as ShortfallWorklist, deliberately: DIB-461's "Order now" must
// suggest exactly what the worklist would have suggested for that product, or the two screens
// disagree about how much to buy and a buyer has to decide which one is lying.
//
// onOrderBase arrives from the caller for exactly that reason. Reading it here would be a second
// definition of "on order" one refactor away from drifting from the worklist's, the drift DIB-470
// was filed about, reintroduced one file over.
//
// found=false means the product has no managed line at that branch. That is an ANSWER (nobody has
// set a replenishment policy for it), not an error, and the caller turns it into the sentence that
// says what to do about it.
func (r *Repository) ReplenishLineFor(ctx context.Context, productID, branchID string, onOrderBase float64) (ShortfallRow, bool, error) {
	if r.pool == nil {
		return ShortfallRow{}, false, errNoDB
	}
	var s ShortfallRow
	err := r.pool.QueryRow(ctx, `
		WITH lines AS (
		  SELECT rp.product_id, rp.branch_id, rp.reorder_point_base, rp.reorder_qty_base,
		         COALESCE(rp.order_multiple_base, 0) AS order_multiple_base,
		         COALESCE(rp.preferred_vendor_id::text, '') AS preferred_vendor_id,
		         rp.lead_time_days,
		         COALESCE(bal.on_hand, 0)  AS on_hand,
		         COALESCE(bal.reserved, 0) AS reserved,
		         COALESCE(tr.qty, 0)       AS in_transit,
		         $3::float8                AS on_order
		    FROM product_reorder_points rp
		    LEFT JOIN LATERAL (
		        SELECT SUM(sb.on_hand_base) AS on_hand, SUM(sb.qty_reserved) AS reserved
		          FROM stock_balances sb
		         WHERE sb.product_id = rp.product_id AND sb.branch_id = rp.branch_id
		    ) bal ON TRUE
		    LEFT JOIN LATERAL (`+inboundTransferQty+`) tr ON TRUE
		   WHERE rp.is_active AND rp.product_id = $1::uuid AND rp.branch_id = $2::uuid
		)
		SELECT g.product_id::text, p.sku::text, p.name, g.branch_id::text, l.code::text,
		       g.on_hand, g.reserved, g.in_transit, g.on_order,
		       g.reorder_point_base, g.reorder_qty_base, g.order_multiple_base,
		       g.preferred_vendor_id, COALESCE(v.name, ''), g.lead_time_days,
		       COALESCE(pbc.avg_unit_cost_cents, 0), COALESCE(p.base_uom::text, '')
		  FROM lines g
		  JOIN products  p ON p.id = g.product_id
		  JOIN locations l ON l.id = g.branch_id
		  LEFT JOIN vendors v ON v.id = g.preferred_vendor_id::uuid
		  LEFT JOIN product_branch_costs pbc
		         ON pbc.product_id = g.product_id AND pbc.branch_id = g.branch_id`,
		productID, branchID, onOrderBase).
		Scan(&s.ProductID, &s.SKU, &s.ProductName, &s.BranchID, &s.BranchCode,
			&s.OnHandBase, &s.ReservedBase, &s.InTransitBase, &s.OnOrderBase,
			&s.ReorderPointBase, &s.ReorderQtyBase, &s.OrderMultipleBase,
			&s.PreferredVendorID, &s.PreferredVendorName, &s.LeadTimeDays,
			&s.AvgCostCents, &s.UOM)
	if err != nil {
		if isNoRows(err) {
			return ShortfallRow{}, false, nil
		}
		return ShortfallRow{}, false, err
	}
	// Derived in Go from the ONE definition, exactly as ShortfallWorklist does after its scan.
	if err := s.Derive(); err != nil {
		return ShortfallRow{}, false, err
	}
	return s, true, nil
}
