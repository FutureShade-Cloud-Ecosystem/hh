package pricing

import "context"

// CandidateProducts reads the active products a pricing change may reach, with the cost
// the floor is graded against. It is a second deliberately cross-module read in this
// repository (the first is LowMarginSalesLines): the products and their weighted cost
// live outside the pricing tables, and a floor warning with no products is no warning.
// It writes nothing. With a branch the cost is that branch's; with none it is the
// highest branch cost, so the warning errs toward naming a product, not hiding it. A
// product with no cost row comes back with cost 0 and is counted as not graded.
func (r *Repository) CandidateProducts(ctx context.Context, categoryID, productID, branchID string, limit int) ([]CandidateProduct, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if limit <= 0 {
		limit = PreviewProductCap + 1
	}
	const q = `
	SELECT p.id::text, COALESCE(p.category_id::text, ''), p.sku::text, p.name, p.base_uom::text,
	       COALESCE(MAX(pbc.avg_unit_cost_cents), 0)
	  FROM products p
	  LEFT JOIN product_branch_costs pbc
	         ON pbc.product_id = p.id AND ($3::uuid IS NULL OR pbc.branch_id = $3::uuid)
	 WHERE p.is_active
	   AND ($1::uuid IS NULL OR p.category_id = $1::uuid)
	   AND ($2::uuid IS NULL OR p.id = $2::uuid)
	 GROUP BY p.id
	 ORDER BY p.sku, p.id
	 LIMIT $4`
	rows, err := r.pool.Query(ctx, q, nullStr(categoryID), nullStr(productID), nullStr(branchID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CandidateProduct
	for rows.Next() {
		var c CandidateProduct
		if err := rows.Scan(&c.ProductID, &c.CategoryID, &c.SKU, &c.Name, &c.BaseUOM, &c.CostCents); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
