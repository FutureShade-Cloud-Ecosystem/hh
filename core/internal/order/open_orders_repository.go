package order

// open_orders_repository.go, the SQL under the open-orders seam (open_orders_seam.go). The read
// stays in this module because the 0008 tables are this module's alone; the seam hands the answer
// out as numbers, so a consumer never learns the tables' shape.

import "context"

// OpenSaleOrdersFor returns the open order lines for the named products at the named branch, ids
// and numbers only, oldest first. The predicate is the pending-fulfilment read's open-line
// predicate (not FULFILLED, CLOSED or CANCELLED, the line still owed) with the fulfilment-mode
// filter left off; see the seam file's header for both halves of that decision. A nil product list
// asks about every product with something outstanding, an empty non-nil list asks about none.
func (r *Repository) OpenSaleOrdersFor(ctx context.Context, branchID string, productIDs []string) ([]OpenSaleOrder, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	q := `
		SELECT ol.product_id::text, o.branch_id::text, o.id::text, o.order_no
		FROM orders o
		JOIN order_lines ol ON ol.order_id = o.id
		WHERE o.status NOT IN ('FULFILLED','CLOSED','CANCELLED')
		  AND ol.product_id IS NOT NULL
		  AND ol.qty_base > ol.fulfilled_base + ol.released_base
		  AND ($1 = '' OR o.branch_id = $1::uuid)`
	args := []any{branchID}
	if productIDs != nil {
		q += ` AND ol.product_id::text = ANY($2::text[])`
		args = append(args, productIDs)
	}
	// Oldest first is the worklist's display rule and the customer's: the order they placed first
	// is the one the shortfall holds up first. order_no breaks created_at ties, the module's
	// stable-ordering basis, and product_id keeps a two-line order's rows deterministic beside it.
	q += `
		ORDER BY o.created_at, o.order_no, ol.product_id`
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OpenSaleOrder
	for rows.Next() {
		var o OpenSaleOrder
		if err := rows.Scan(&o.ProductID, &o.BranchID, &o.OrderID, &o.OrderNo); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
