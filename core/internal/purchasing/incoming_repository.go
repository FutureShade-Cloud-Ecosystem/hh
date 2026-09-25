package purchasing

// incoming_repository.go, the pgx half of DIB-460's on-order read, and the expected-date write.
//
// TWO PROPERTIES WORTH READING THE SQL FOR:
//
//  1. THE BRANCH PREDICATE IS ALWAYS PRESENT. `($3::uuid[] IS NULL OR p.branch_id = ANY($3::uuid[]))`
//     with NULL meaning "global". A caller with no grants renders an EMPTY array, and
//     `= ANY('{}')` is false for every row, so the fence fails CLOSED. The alternative shape
//     (omitting the clause when there is no filter) fails OPEN, and it looks identical in review.
//
//  2. NO JOIN THIS MODULE DOES NOT OWN. Not `vendors` (the name is enriched through the
//     VendorDirectory seam in the service, exactly as ListPOs does it), not `locations` (the branch
//     is returned as an id and named by the surface), not `products`. purchasing's repository has
//     never contained a foreign JOIN and this read does not start one.

import (
	"context"
	"fmt"
)

// - the ONE definition of an outstanding purchase-order line -
//
// Three fragments, shared by BOTH reads over these rows: DIB-460's per-product detail list and
// DIB-470's per-(product, branch) aggregate. They are fragments rather than two similar queries
// because the aggregate is what the replenishment worklist GRADES with and the list is what the
// product page SHOWS, and DIB-470 was filed because those two numbers disagreed. Sharing the text
// is what makes them unable to disagree again; two hand-written copies of the same predicate is
// exactly the shape that drifted.
//
// The parameter positions are part of the contract: $2 is the status set in both queries.
const openPOLineSource = `
	  FROM purchasing_po_lines pl
	  JOIN purchasing_pos p ON p.id = pl.po_id`

// openPOLineOutstanding is what "still coming" means as a QUANTITY: ordered minus received, in the
// line's BASE unit. Base, not sell units, the caller grading a stock position counts in base.
const openPOLineOutstanding = `(pl.qty_ordered_base - pl.qty_received_base)`

// openPOLineIsOpen is what "still coming" means as a PREDICATE. `status = ANY($2)` takes the status
// set as a parameter rather than inlining a literal IN list, so openPOStatuses in incoming.go is the
// single definition and the SQL cannot quietly disagree with the Go that documents it.
const openPOLineIsOpen = `
	       p.status = ANY($2::text[])
	   AND pl.qty_ordered_base > pl.qty_received_base`

// openPOLinesSQL is DIB-460's read: every outstanding line for one product, soonest first.
const openPOLinesSQL = `
	SELECT p.id::text, p.po_no::text, pl.id::text, p.status,
	       p.vendor_id::text, p.branch_id::text,
	       COALESCE(to_char(p.expected_at,'YYYY-MM-DD'),''),
	       pl.qty_ordered_base::float8, pl.qty_received_base::float8,
	       ` + openPOLineOutstanding + `::float8,
	       pl.uom::text, pl.unit_cost_cents` + openPOLineSource + `
	 WHERE pl.product_id = $1::uuid
	   AND ` + openPOLineIsOpen + `
	   AND ($3::uuid[] IS NULL OR p.branch_id = ANY($3::uuid[]))
	 ORDER BY p.expected_at NULLS LAST, p.po_no, pl.line_no`

// outstandingOnOrderSQL is DIB-470's aggregate: the same lines, totalled per product per branch.
//
// $1 is the branch (NULL = every branch), $2 the status set, $3 an optional product filter (NULL =
// every product). GROUPed in SQL rather than summed in Go so a branch with a thousand open lines
// crosses the wire as the few dozen numbers the worklist actually grades with.
const outstandingOnOrderSQL = `
	SELECT pl.product_id::text, p.branch_id::text,
	       SUM(` + openPOLineOutstanding + `)::float8` + openPOLineSource + `
	 WHERE ` + openPOLineIsOpen + `
	   AND ($1::uuid IS NULL OR p.branch_id = $1::uuid)
	   AND ($3::uuid[] IS NULL OR pl.product_id = ANY($3::uuid[]))
	 GROUP BY pl.product_id, p.branch_id
	HAVING SUM(` + openPOLineOutstanding + `) > 0`

// OpenPOLinesForProduct returns every outstanding PO line for one product inside the reach.
//
// SOONEST FIRST, with the undated orders LAST (NULLS LAST). A person asking "when is it showing up"
// is asking about the next truck, so the row that answers them has to be the first one they read;
// an undated order answers nothing and belongs at the bottom.
func (r *Repository) OpenPOLinesForProduct(ctx context.Context, productID string, reach BranchReach) ([]IncomingPOLine, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	// A caller with no grants: answered without a round trip. The empty-array predicate above would
	// return the same nothing, so this is an optimisation and NOT the fence, the fence is in the SQL,
	// where it survives a caller that skips this method.
	if reach.reachesNothing() {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, openPOLinesSQL, productID, openPOStatuses, reach.branchFilter())
	if err != nil {
		return nil, fmt.Errorf("purchasing: open po lines for product: %w", err)
	}
	defer rows.Close()
	var out []IncomingPOLine
	for rows.Next() {
		var l IncomingPOLine
		if err := rows.Scan(&l.POID, &l.PONo, &l.POLineID, &l.Status, &l.VendorID, &l.BranchID,
			&l.ExpectedAt, &l.QtyOrderedBase, &l.QtyReceivedBase, &l.QtyOutstanding,
			&l.UOM, &l.UnitCostCents); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// OutstandingOnOrderBase totals the outstanding base quantity per (product, branch), DIB-470.
//
// branchID "" = every branch; productIDs empty = every product. Both are widened to SQL NULL rather
// than to an empty array, because an empty array here means "nothing matches" (the DIB-460 fence
// arm) and "no filter" must not borrow that spelling.
func (r *Repository) OutstandingOnOrderBase(ctx context.Context, branchID string, productIDs []string) ([]OutstandingOnOrder, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, outstandingOnOrderSQL,
		nullStr(branchID), openPOStatuses, nullStrings(productIDs))
	if err != nil {
		return nil, fmt.Errorf("purchasing: outstanding on order: %w", err)
	}
	defer rows.Close()
	var out []OutstandingOnOrder
	for rows.Next() {
		var o OutstandingOnOrder
		if err := rows.Scan(&o.ProductID, &o.BranchID, &o.QtyBase); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// nullStrings renders an optional id filter: empty → SQL NULL (no filter), else the array.
func nullStrings(ids []string) any {
	if len(ids) == 0 {
		return nil
	}
	return ids
}

// UpdateExpectedAt writes (or clears) purchasing_pos.expected_at. An empty string stores NULL, one
// representation of "no promised date" in the database rather than two. The purchase_orders scope's
// seq rides the correction (0213), so a desk correction is visible to a device's pull.
func (r *Repository) UpdateExpectedAt(ctx context.Context, poID, expectedAt string) error {
	if r.pool == nil {
		return errNoDB
	}
	tag, err := r.pool.Exec(ctx, poSeqCTE+`
		UPDATE purchasing_pos SET expected_at = $2::date, updated_at = now(), seq = (SELECT next_value FROM poseq)
		 WHERE id = $1`,
		poID, nullStr(expectedAt))
	if err != nil {
		return fmt.Errorf("purchasing: set expected_at: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errNotFound
	}
	return nil
}

// UpdateDeliveryMethod writes purchasing_pos.delivery_method, DIB-572's correction path (ported
// from the ERP).
//
// The value is ALWAYS one the service normalised (normalizeDeliveryMethod); the CHECK the 0219
// port carries is the floor under that, not the first line of defence. Unlike expected_at there
// is no NULL to write: "nobody recorded it" is the UNKNOWN value, so the column has one
// representation of not knowing rather than two.
func (r *Repository) UpdateDeliveryMethod(ctx context.Context, poID, method string) error {
	if r.pool == nil {
		return errNoDB
	}
	tag, err := r.pool.Exec(ctx, poSeqCTE+`
		UPDATE purchasing_pos SET delivery_method = $2, updated_at = now(), seq = (SELECT next_value FROM poseq)
		 WHERE id = $1`,
		poID, method)
	if err != nil {
		return fmt.Errorf("purchasing: set delivery_method: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errNotFound
	}
	return nil
}
