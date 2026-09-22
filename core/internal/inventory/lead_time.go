package inventory

// lead_time.go, DIB-479: "if you haven't got it, when can I have it?", per branch.
//
// WHERE LEAD TIME ACTUALLY LIVES. product_reorder_points.lead_time_days (0117), keyed by
// (product, branch). It is branch-grained for the same reason the reorder point is: Trenton and
// Kingston order from different places on different days, and one global number would be wrong at
// one of them. A product with no reorder-point row at a branch is not replenishment-managed there
// and has NO lead time on file, an absent key, never a zero, because zero means "same day" and
// telling a contractor "same day" about a product nobody has ever set a lead time for is the kind
// of promise that ends in a phone call.
//
// READ-ONLY, and structurally so: there is no tx parameter in this file and nothing here writes.

import "context"

// LeadTimeDaysByBranch returns branch id → lead-time days for one product, for every branch that
// carries an ACTIVE replenishment policy for it. Branches without one are absent from the map.
//
// The signature is primitive-only on purpose, matching OnHandByBranch's rationale: a consumer can
// declare this seam without importing inventory, so *inventory.Service satisfies it directly with
// no adapter type and no new import edge in either direction.
func (s *Service) LeadTimeDaysByBranch(ctx context.Context, productID string) (map[string]int, error) {
	if productID == "" {
		return nil, badReq("inventory: product_id is required")
	}
	lr, ok := s.db().(leadTimeStore)
	if !ok {
		// A fake store in a unit test. "No lead times on file" is the same honest answer as a
		// product nobody has set one for, and it is the answer that degrades a detail page rather
		// than failing it.
		return map[string]int{}, nil
	}
	return lr.LeadTimeDaysByBranch(ctx, productID)
}

// leadTimeStore is the optional persistence seam for this read, separate from `store` so the
// package's existing fakes keep satisfying what they already satisfy (the stockLookupStore
// convention in stock_lookup.go).
type leadTimeStore interface {
	LeadTimeDaysByBranch(ctx context.Context, productID string) (map[string]int, error)
}

// LeadTimeDaysByBranch is the pgx read. Only ACTIVE policies count: a deactivated reorder point is
// a policy somebody switched off, and quoting its lead time back to a customer would be quoting a
// number the yard no longer stands behind.
func (r *Repository) LeadTimeDaysByBranch(ctx context.Context, productID string) (map[string]int, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT rp.branch_id::text, rp.lead_time_days
		  FROM product_reorder_points rp
		 WHERE rp.product_id = $1::uuid
		   AND rp.is_active`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var branchID string
		var days int
		if err := rows.Scan(&branchID, &days); err != nil {
			return nil, err
		}
		out[branchID] = days
	}
	return out, rows.Err()
}
