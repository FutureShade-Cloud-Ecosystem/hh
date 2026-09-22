package pos

// repository_quickpicks.go, the two catalogue reads behind the quick-pick grid.
//
// Both stay inside the POS module's read boundary the same way the rev-8 add-on reads do: they
// SELECT products / order_lines / pos_sale, never another module's write path, and they compute
// nothing about price (the pricing engine owns every cent the tiles show).

import (
	"context"
	"strings"
)

// quickPickRow is one catalogue row behind a tile. It deliberately carries NO price: the tile's
// numbers come from the pricing engine, so a row read here can never become a second price source.
type quickPickRow struct {
	ProductID     string
	SKU           string
	Name          string
	ProductSize   string
	DimensionsLWH string
	BaseUOM       string
	SoldBy        string
	HasImage      bool
	// Application / Collection are the two catalogue facets the drillable hot list groups by -
	// Ryan Rood's own example is bulk -> mulch -> black, which is exactly application -> collection
	// -> the product itself. Both are indexed columns that have existed since 0002; the hot list
	// needed no schema, only these two strings carried through to the tile.
	Application string
	Collection  string
}

// SizeLine is the tile's one supporting line: the pack/size wording if the catalogue has it, else
// the dimensions, else nothing at all (an empty line is not rendered, a tile with a blank second
// row reads as missing data).
func (r quickPickRow) SizeLine() string {
	if s := strings.TrimSpace(r.ProductSize); s != "" {
		return s
	}
	return strings.TrimSpace(r.DimensionsLWH)
}

// ReadProductsBySKU resolves curated SKUs (lowercased ids from the settings kit) to catalogue rows.
// Case-insensitive because the kit's id alphabet is lowercase and real SKUs are not; inactive and
// unknown SKUs simply do not come back, which is how a retired product disappears from a curated
// grid without anyone having to notice.
func (r *Repository) ReadProductsBySKU(ctx context.Context, skus []string) ([]quickPickRow, error) {
	if r.pool == nil {
		return nil, errNoPool
	}
	if len(skus) == 0 {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, `
SELECT p.id::text, p.sku::text, p.name,
       COALESCE(p.product_size,''), COALESCE(p.dimensions_lwh,''),
       p.base_uom::text, COALESCE(p.sold_by,''),
       EXISTS (SELECT 1 FROM product_images pi WHERE pi.product_id = p.id),
       COALESCE(p.application,''), COALESCE(p.collection,'')
  FROM products p
 WHERE p.is_active AND lower(p.sku::text) = ANY($1)`, skus)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanQuickPickRows(rows)
}

// TopSellingProducts is the DERIVED hot list, what a counter sees before anybody curates
// anything, and since the 27-Aug ruling (R2) it is ranked BULK FIRST.
//
// THE RULING, AND THE WORDS BEHIND IT. "Bulk aggregates - but should be configurable." Ryan
// Bostock: "the volume of sales we do in bulk material is higher than contracts/projects", and
// "if we had a hot list sort of touchscreen looking all the different color mulches and the
// different aggregates. That would be neat." So a cold deployment must open on a yard's BULK
// BOARD, mulch broken out by colour, aggregates by size, not on a general catalogue root.
//
// HOW BULK IS RECOGNISED WITHOUT INVENTING A SCHEMA. A product is bulk when its category is the
// one 0067 created for exactly this (BULK, and the AGGREGATE it was split from), or when the yard
// SELLS it by a bulk unit, tonnes or cubic yards. Both facts already exist on every dealer's own
// catalogue, so this ships as an ordering rule rather than as seed rows a real dealer would have to
// delete. On a yard with no bulk at all the partition is simply empty and the ranking is unchanged.
//
// WITHIN each partition the order is still what this store actually sells: sold line count across
// COMMITTED POS sales, busiest first, then the catalogue's own featured/SKU order for the lines
// that have not been rung yet. One query rather than two, because "no committed sales yet" is not
// a different question, it is the same ranking with every count at zero.
func (r *Repository) TopSellingProducts(ctx context.Context, limit int) ([]quickPickRow, error) {
	if r.pool == nil {
		return nil, errNoPool
	}
	if limit <= 0 {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, `
WITH sold AS (
    SELECT ol.product_id, count(*)::int AS n
      FROM order_lines ol
     WHERE EXISTS (SELECT 1 FROM pos_sale ps WHERE ps.order_id = ol.order_id AND ps.status = 'COMMITTED')
     GROUP BY ol.product_id
)
SELECT p.id::text, p.sku::text, p.name,
       COALESCE(p.product_size,''), COALESCE(p.dimensions_lwh,''),
       p.base_uom::text, COALESCE(p.sold_by,''),
       EXISTS (SELECT 1 FROM product_images pi WHERE pi.product_id = p.id),
       COALESCE(p.application,''), COALESCE(p.collection,'')
  FROM products p
  LEFT JOIN sold s ON s.product_id = p.id
  LEFT JOIN product_categories pc ON pc.id = p.category_id
 WHERE p.is_active
 ORDER BY CASE
            WHEN pc.code IN ('BULK', 'AGGREGATE') THEN 0
            WHEN upper(COALESCE(NULLIF(p.sold_by, ''), p.base_uom::text)) IN ('T', 'CYD') THEN 0
            ELSE 1
          END,
          COALESCE(s.n, 0) DESC,
          p.is_featured DESC,
          p.sku
 LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanQuickPickRows(rows)
}

// scanQuickPickRows is the one scan both reads share, the column list is identical by design, so
// a change to what a tile needs is a change in ONE place.
func scanQuickPickRows(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
},
) ([]quickPickRow, error) {
	var out []quickPickRow
	for rows.Next() {
		var q quickPickRow
		if err := rows.Scan(&q.ProductID, &q.SKU, &q.Name, &q.ProductSize, &q.DimensionsLWH,
			&q.BaseUOM, &q.SoldBy, &q.HasImage, &q.Application, &q.Collection); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}
