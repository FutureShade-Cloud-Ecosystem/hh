package inventory

// replenish_repository.go, the pgx half of DIB-72.
//
// The worklist is ONE query. The naive shape is a loop over managed lines calling the four-bucket
// read per row, which is 200 round trips for a 200-line worklist and gets slower exactly as a branch
// gets busier. Here the three quantity sources hang off the managed line as separate lateral
// aggregates, separate because joining stock_balances to stock_transfer_lines to po_lines directly
// would fan out (one balance row × two open transfers × three open PO lines = six copies of the
// on-hand) and produce a number nobody can reproduce by hand.
//
// The shortfall predicate and the suggested quantity are computed in SQL so the ORDER BY and the
// LIMIT operate on the real ranking rather than on an arbitrary page that Go then filters. The Go
// side RE-COMPUTES both from the returned components (ProjectedPosition / SuggestQty) and the
// integration test asserts the two agree, so the SQL is a fast path, not a second source of truth
// that can drift from the one in replenish.go.

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// seamOnOrderQty is the "already on order" leg, DIB-470.
//
// It reads the numbers the OnOrderReader seam supplied, unnested from three parallel parameter
// arrays, and NOT a purchase-order table: purchase orders are purchasing's rows and this module must
// not join them. What it replaced (`openPOQty`, deleted with this ticket) counted the 0011
// purchase_orders rows, which have been RECEIVING DOCUMENTS since 0120, created and received in one
// transaction, therefore never open, therefore a constant zero.
//
// A lateral rather than a join so the shape matches its two neighbours exactly, and so a product the
// seam said nothing about produces NULL → COALESCE 0 rather than dropping the managed line.
const seamOnOrderQty = `
	SELECT SUM(oo.qty) AS qty
	  FROM UNNEST($3::uuid[], $4::uuid[], $5::float8[]) AS oo(product_id, branch_id, qty)
	 WHERE oo.product_id = rp.product_id
	   AND oo.branch_id  = rp.branch_id`

// inboundTransferQty is the "already on a truck" leg.
const inboundTransferQty = `
	SELECT SUM(tl.qty_shipped_base - tl.qty_received_base) AS qty
	  FROM stock_transfer_lines tl
	  JOIN stock_transfers t ON t.id = tl.transfer_id
	 WHERE tl.product_id = rp.product_id
	   AND t.to_branch_id = rp.branch_id
	   AND t.status = 'IN_TRANSIT'
	   AND tl.qty_shipped_base > tl.qty_received_base`

// ShortfallWorklist returns the managed lines whose projected position is under the reorder point.
//
// onOrder is the DIB-470 seam's answer, outstanding purchase-order base quantity per (product,
// branch). A nil slice is a legitimate call and means "nothing on order, or nobody could say"; the
// service is what knows which of those two it was, and what tells the surface.
func (r *Repository) ShortfallWorklist(ctx context.Context, branchID string, limit int, onOrder []OnOrderQty) ([]ShortfallRow, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	ooProduct, ooBranch, ooQty := unnestOnOrder(onOrder)
	rows, err := r.pool.Query(ctx, `
		WITH lines AS (
		  SELECT rp.product_id, rp.branch_id, rp.reorder_point_base, rp.reorder_qty_base,
		         COALESCE(rp.order_multiple_base, 0) AS order_multiple_base,
		         COALESCE(rp.preferred_vendor_id::text, '') AS preferred_vendor_id,
		         rp.lead_time_days,
		         COALESCE(bal.on_hand, 0)  AS on_hand,
		         COALESCE(bal.reserved, 0) AS reserved,
		         COALESCE(tr.qty, 0)       AS in_transit,
		         COALESCE(po.qty, 0)       AS on_order
		    FROM product_reorder_points rp
		    LEFT JOIN LATERAL (
		        SELECT SUM(sb.on_hand_base) AS on_hand, SUM(sb.qty_reserved) AS reserved
		          FROM stock_balances sb
		         WHERE sb.product_id = rp.product_id AND sb.branch_id = rp.branch_id
		    ) bal ON TRUE
		    LEFT JOIN LATERAL (`+inboundTransferQty+`) tr ON TRUE
		    LEFT JOIN LATERAL (`+seamOnOrderQty+`) po ON TRUE
		   WHERE rp.is_active AND ($1 = '' OR rp.branch_id = $1::uuid)
		), graded AS (
		  SELECT lines.*,
		         (on_hand - reserved) AS available,
		         (on_hand - reserved) + in_transit + on_order AS projected
		    FROM lines
		)
		SELECT g.product_id::text, p.sku::text, p.name, g.branch_id::text, l.code::text,
		       g.on_hand, g.reserved, g.available, g.in_transit, g.on_order, g.projected,
		       g.reorder_point_base, g.reorder_qty_base, g.order_multiple_base,
		       g.reorder_point_base - g.projected AS short_base,
		       g.preferred_vendor_id, COALESCE(v.name, ''), g.lead_time_days,
		       COALESCE(pbc.avg_unit_cost_cents, 0),
		       COALESCE(p.base_uom::text, '')
		  FROM graded g
		  JOIN products  p ON p.id = g.product_id
		  JOIN locations l ON l.id = g.branch_id
		  LEFT JOIN vendors v ON v.id = NULLIF(g.preferred_vendor_id, '')::uuid
		  LEFT JOIN product_branch_costs pbc
		         ON pbc.product_id = g.product_id AND pbc.branch_id = g.branch_id
		 WHERE g.projected < g.reorder_point_base
		 ORDER BY (g.reorder_point_base - g.projected) DESC, p.sku
		 LIMIT $2`, branchID, limit, ooProduct, ooBranch, ooQty)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ShortfallRow
	for rows.Next() {
		var s ShortfallRow
		if err := rows.Scan(&s.ProductID, &s.SKU, &s.ProductName, &s.BranchID, &s.BranchCode,
			&s.OnHandBase, &s.ReservedBase, &s.AvailableBase, &s.InTransitBase, &s.OnOrderBase,
			&s.ProjectedBase, &s.ReorderPointBase, &s.ReorderQtyBase, &s.OrderMultipleBase,
			&s.ShortBase, &s.PreferredVendorID, &s.PreferredVendorName, &s.LeadTimeDays,
			&s.AvgCostCents, &s.UOM); err != nil {
			return nil, err
		}
		// Re-derive from the ONE Go definition. The SQL above computes the same numbers to make the
		// ORDER BY and LIMIT correct; this call makes replenish.go the source of truth, and
		// TestIntegration_Shortfall_SQLAndGoAgree pins the two together.
		if err := s.Derive(); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// unnestOnOrder splits the seam's answer into the three parallel arrays seamOnOrderQty unnests.
//
// Three arrays rather than one composite parameter because pgx sends a Go slice of a plain type as a
// Postgres array without a registered composite type, and a read that needed a DDL type to be
// installed before it worked would be a migration in disguise.
//
// The lengths are equal by construction (one append per row, in the same loop), UNNEST over ragged
// arrays pads with NULL, which would silently pair a product with somebody else's quantity.
func unnestOnOrder(rows []OnOrderQty) (productIDs, branchIDs []string, qty []float64) {
	productIDs = make([]string, 0, len(rows))
	branchIDs = make([]string, 0, len(rows))
	qty = make([]float64, 0, len(rows))
	for _, r := range rows {
		if r.ProductID == "" || r.BranchID == "" {
			// A row that names no product or no branch cannot be matched to a managed line, and
			// UNNEST would turn its empty string into a uuid cast error for the whole query.
			continue
		}
		productIDs = append(productIDs, r.ProductID)
		branchIDs = append(branchIDs, r.BranchID)
		qty = append(qty, r.QtyBase)
	}
	return productIDs, branchIDs, qty
}

const reorderPointSelect = `
	SELECT rp.product_id::text, p.sku::text, p.name, rp.branch_id::text,
	       rp.reorder_point_base, rp.reorder_qty_base, COALESCE(rp.order_multiple_base, 0),
	       COALESCE(rp.preferred_vendor_id::text, ''), rp.lead_time_days, rp.is_active, rp.notes
	  FROM product_reorder_points rp
	  JOIN products p ON p.id = rp.product_id`

func scanReorderPoints(rows pgx.Rows) ([]ReorderPoint, error) {
	defer rows.Close()
	var out []ReorderPoint
	for rows.Next() {
		var rp ReorderPoint
		if err := rows.Scan(&rp.ProductID, &rp.SKU, &rp.ProductName, &rp.BranchID,
			&rp.ReorderPointBase, &rp.ReorderQtyBase, &rp.OrderMultipleBase,
			&rp.PreferredVendorID, &rp.LeadTimeDays, &rp.IsActive, &rp.Notes); err != nil {
			return nil, err
		}
		out = append(out, rp)
	}
	return out, rows.Err()
}

// ListReorderPoints returns the managed lines at a branch (all branches when branchID == "").
func (r *Repository) ListReorderPoints(ctx context.Context, branchID string, limit int) ([]ReorderPoint, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, reorderPointSelect+`
		 WHERE ($1 = '' OR rp.branch_id = $1::uuid)
		 ORDER BY p.sku LIMIT $2`, branchID, limit)
	if err != nil {
		return nil, err
	}
	return scanReorderPoints(rows)
}

// UpsertReorderPoint writes one managed line. order_multiple_base 0 stores NULL (= no multiple),
// so "no rounding" is one representation in the database rather than two.
func (r *Repository) UpsertReorderPoint(ctx context.Context, in ReorderPoint, actorID string) (ReorderPoint, error) {
	if r.pool == nil {
		return ReorderPoint{}, errNoDB
	}
	var multiple any
	if in.OrderMultipleBase > 0 {
		multiple = in.OrderMultipleBase
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO product_reorder_points
			(product_id, branch_id, reorder_point_base, reorder_qty_base, order_multiple_base,
			 preferred_vendor_id, lead_time_days, is_active, notes, updated_by, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now())
		ON CONFLICT (product_id, branch_id) DO UPDATE SET
			reorder_point_base  = EXCLUDED.reorder_point_base,
			reorder_qty_base    = EXCLUDED.reorder_qty_base,
			order_multiple_base = EXCLUDED.order_multiple_base,
			preferred_vendor_id = EXCLUDED.preferred_vendor_id,
			lead_time_days      = EXCLUDED.lead_time_days,
			is_active           = EXCLUDED.is_active,
			notes               = EXCLUDED.notes,
			updated_by          = EXCLUDED.updated_by,
			updated_at          = now()`,
		in.ProductID, in.BranchID, in.ReorderPointBase, in.ReorderQtyBase, multiple,
		nullStr(in.PreferredVendorID), in.LeadTimeDays, in.IsActive, in.Notes, nullStr(actorID))
	if err != nil {
		return ReorderPoint{}, err
	}
	rows, err := r.pool.Query(ctx, reorderPointSelect+`
		 WHERE rp.product_id = $1 AND rp.branch_id = $2`, in.ProductID, in.BranchID)
	if err != nil {
		return ReorderPoint{}, err
	}
	list, err := scanReorderPoints(rows)
	if err != nil {
		return ReorderPoint{}, err
	}
	if len(list) == 0 {
		return ReorderPoint{}, errNotFound
	}
	return list[0], nil
}

// BootstrapReorderPoints opens a managed line for every stocked product at the branch that does not
// already have one. ON CONFLICT DO NOTHING, so a human-tuned point is never overwritten by a re-run.
func (r *Repository) BootstrapReorderPoints(ctx context.Context, branchID, actorID string) (int, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO product_reorder_points (
			product_id, branch_id, reorder_point_base, reorder_qty_base, order_multiple_base,
			preferred_vendor_id, lead_time_days, notes, updated_by)
		SELECT sb.product_id,
		       sb.branch_id,
		       FLOOR(SUM(sb.on_hand_base) / 2)::DECIMAL(19,4),
		       GREATEST(FLOOR(SUM(sb.on_hand_base) / 2), 1)::DECIMAL(19,4),
		       NULLIF(p.full_skid_quantity, 0)::DECIMAL(19,4),
		       (SELECT v.id FROM vendors v WHERE v.is_active ORDER BY v.vendor_no LIMIT 1),
		       7,
		       'bootstrapped from on-hand',
		       $2
		  FROM stock_balances sb
		  JOIN products p ON p.id = sb.product_id
		 WHERE p.is_active AND sb.branch_id = $1
		 GROUP BY sb.product_id, sb.branch_id, p.full_skid_quantity
		HAVING SUM(sb.on_hand_base) >= 2
		ON CONFLICT (product_id, branch_id) DO NOTHING`, branchID, nullStr(actorID))
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}
