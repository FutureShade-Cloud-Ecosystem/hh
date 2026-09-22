package pos

// repository_rev8.go, DIB-488 rev 8 store reads. All four families are the SAME isolated M2-debt
// cross-read posture as ReadStockOnHand / ReadStockUnitMeta (display-only reads of catalog/costing
// data; pos never writes those tables):
//
//   ReadProductWeightKg      products.weight_per_unit_kg, the DISCOVERED weight source (item 9;
//                            no migration: the column has carried seeded kg-per-base-unit since
//                            the original catalogue schema).
//   ReadBranchCosts          product_branch_costs.avg_unit_cost_cents, THE pricing engine's cost
//                            source (the same per-branch WAC row pricing.LowMarginSalesLines and
//                            inventory.BranchCostCents read), per product BASE uom (item 10).
//   ReadAddonPairings /      the 0169 co-purchase pairing map + its candidate products (DIB-502,
//   ListAddonCandidates      item 8), category-level, data-driven, replaceable from real sales.
//   TopSellingApplications   committed-sale category ranking, the honest default for the curated
//                            quick-chip set until the dealer curates one (item 4).

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
)

// splitChipSet decodes the pos_policy.quick_chip_set TEXT column (comma-separated) into the wire
// list. Empty column → nil (absent on the wire, never a one-empty-string list).
func splitChipSet(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// joinChipSet is splitChipSet's write-side inverse (validated in the service before it gets here).
func joinChipSet(set []string) string { return strings.Join(set, ",") }

// ReadProductWeightKg reads the product's unit weight in kg per BASE unit. found=false when the
// catalogue has no weight on file, the caller surfaces nothing (an em-dash line, an excluded
// footer term), never a guessed number.
func (r *Repository) ReadProductWeightKg(ctx context.Context, productID string) (float64, bool, error) {
	if r.pool == nil {
		return 0, false, errNoPool
	}
	var kg *float64
	err := r.pool.QueryRow(ctx,
		`SELECT weight_per_unit_kg::float8 FROM products WHERE id::text = $1`, productID).Scan(&kg)
	if err != nil {
		if err == pgx.ErrNoRows {
			return 0, false, nil
		}
		return 0, false, err
	}
	if kg == nil || *kg <= 0 {
		return 0, false, nil
	}
	return *kg, true, nil
}

// ReadBranchCosts reads the per-branch WAC (avg_unit_cost_cents, per product BASE uom) for a set
// of products at one branch. Products with no cost row are simply ABSENT from the map, cost
// missing is a state of the costing data, never coalesced to 0 (a 0 cost would fake a 100% margin).
func (r *Repository) ReadBranchCosts(ctx context.Context, productIDs []string, branchID string) (map[string]int64, error) {
	out := map[string]int64{}
	if r.pool == nil {
		return out, errNoPool
	}
	if len(productIDs) == 0 || branchID == "" {
		return out, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT product_id::text, avg_unit_cost_cents
		   FROM product_branch_costs
		  WHERE branch_id::text = $1 AND product_id::text = ANY($2) AND avg_unit_cost_cents > 0`,
		branchID, productIDs)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var c int64
		if err := rows.Scan(&id, &c); err != nil {
			return out, err
		}
		out[id] = c
	}
	return out, rows.Err()
}

// ReadAddonPairings returns the 0169 pairing map: source application → suggested applications in
// rank order. An empty table is an empty map, the strip hides (empty-never-error).
func (r *Repository) ReadAddonPairings(ctx context.Context) (map[string][]string, error) {
	out := map[string][]string{}
	if r.pool == nil {
		return out, errNoPool
	}
	rows, err := r.pool.Query(ctx,
		`SELECT source_application::text, suggested_application::text
		   FROM pos_addon_pairings ORDER BY source_application, rank, suggested_application`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var src, sug string
		if err := rows.Scan(&src, &sug); err != nil {
			return out, err
		}
		out[src] = append(out[src], sug)
	}
	return out, rows.Err()
}

// ListAddonCandidates lists ACTIVE products in the suggested applications, excluding the ticket's
// own products, best-anchored first (a suggestion tile with a price reads better than one
// without), capped by limit. The list-price join is the SAME pricing_list_prices anchor the
// counter search shows; has_image mirrors the counter-search EXISTS so the tile's thumb hint is
// honest.
func (r *Repository) ListAddonCandidates(ctx context.Context, applications, excludeProductIDs []string, limit int) ([]addonCandidate, error) {
	if r.pool == nil {
		return nil, errNoPool
	}
	if len(applications) == 0 || limit <= 0 {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, `
SELECT p.id::text, p.sku::text, p.name,
       COALESCE(p.product_size,''), COALESCE(p.dimensions_lwh,''),
       p.base_uom::text, COALESCE(p.sold_by,''),
       COALESCE(p.minimum_order_qty::float8, 0),
       plp.list_price_cents, COALESCE(plp.price_uom::text,''),
       EXISTS (SELECT 1 FROM product_images pi WHERE pi.product_id = p.id),
       COALESCE(p.application,'')
  FROM products p
  LEFT JOIN pricing_list_prices plp ON plp.product_id = p.id
 WHERE p.is_active
   AND p.application = ANY($1)
   AND NOT (p.id::text = ANY($2))
 ORDER BY array_position($1::citext[], p.application), plp.list_price_cents IS NULL, p.sku
 LIMIT $3`, applications, excludeProductIDs, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []addonCandidate
	for rows.Next() {
		var c addonCandidate
		if err := rows.Scan(&c.ProductID, &c.SKU, &c.Name, &c.ProductSize, &c.DimensionsLWH,
			&c.BaseUOM, &c.SoldBy, &c.MinimumOrder, &c.ListPriceCents, &c.PriceUOM,
			&c.HasImage, &c.Application); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ReadProductApplications maps product ids → their application category (the same category
// vocabulary the pairing map speaks). Products without a category are simply absent.
func (r *Repository) ReadProductApplications(ctx context.Context, productIDs []string) (map[string]string, error) {
	out := map[string]string{}
	if r.pool == nil {
		return out, errNoPool
	}
	if len(productIDs) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id::text, COALESCE(application,'') FROM products WHERE id::text = ANY($1)`, productIDs)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, app string
		if err := rows.Scan(&id, &app); err != nil {
			return out, err
		}
		if app != "" {
			out[id] = app
		}
	}
	return out, rows.Err()
}

// TopSellingApplications ranks the categories of COMMITTED POS sales' lines by sold line count -
// the data-driven default for the curated quick-chip set (item 4) until the dealer curates one.
// No sales yet → the catalogue's categories by product count, so a fresh install still opens on a
// short honest row.
func (r *Repository) TopSellingApplications(ctx context.Context, limit int) ([]string, error) {
	if r.pool == nil {
		return nil, errNoPool
	}
	if limit <= 0 {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, `
SELECT p.application::text
  FROM order_lines ol
  JOIN products p ON p.id = ol.product_id
 WHERE COALESCE(p.application,'') <> ''
   AND EXISTS (SELECT 1 FROM pos_sale ps WHERE ps.order_id = ol.order_id AND ps.status = 'COMMITTED')
 GROUP BY p.application
 ORDER BY count(*) DESC, p.application
 LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) > 0 {
		return out, nil
	}
	// No committed sales yet: fall back to the catalogue's busiest categories.
	rows2, err := r.pool.Query(ctx, `
SELECT application::text FROM products
 WHERE is_active AND COALESCE(application,'') <> ''
 GROUP BY application ORDER BY count(*) DESC, application LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows2.Close()
	for rows2.Next() {
		var a string
		if err := rows2.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows2.Err()
}
