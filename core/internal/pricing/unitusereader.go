package pricing

// unitusereader.go is the pricing module's read the product module's
// unit set door checks a removal through (HH-LOAD-1 LD-3): whether any
// pricing row of one product still names one unit. The table gate keeps
// the pricing tables this module's, so the reader lives here and the
// unit set writer declares the narrow interface it needs, taken by
// constructor at the wiring. It runs on the caller's transaction (the
// load frame's), opens no connection, and commits nothing.

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// LoadedUnitUseReader answers the product unit set door's removal
// question: the list price, the pricing rules, the quantity break
// headers and their items, the promotions' fixed prices, the clearance
// prices, the customer category overrides, and the sales conversion
// edges, any row of those naming the unit for the product is a removal
// refused.
//
// The category legs mirror the resolver's match exactly, and the
// resolver matches on the category alone:
//
//   - MatchingRules (core/internal/pricing/repository.go:85-92) matches
//     CUSTOMER_CATEGORY on customer_id AND category_id (repository.go:88)
//     and TIER_CATEGORY on customer_type AND category_id
//     (repository.go:89); neither arm reads product_id, and the service's
//     rule filters (service.go:465-474) do not either, so a rule of
//     either scope that ALSO carries a product beside the category still
//     applies to every product of that category. The reader therefore
//     reads those two scopes on category_id with no product predicate.
//   - QuantityBreaksFor (repository.go:110-121) matches
//     `h.product_id = $1 OR h.category_id = $2` (repository.go:119), so a
//     header carrying a product beside the category applies too, for the
//     threshold and the items' price units alike.
//   - OverrideFor (repository.go:145) serves a customer category
//     override by customer and category; its price_uom becomes the
//     declared unit (service.go:462) and the result conversion needs it
//     (service.go:497), so an override priced in a unit the set drops
//     fails every later resolve for that customer. The reader carries no
//     customer, so it reads every override of the product's category.
//
// The exactness has its other face: a rule scoped to the category's
// parent is not this product's row and the read answers clear. The
// TIER_DEFAULT rules (repository.go:90) and the category scoped and
// store wide promotions apply to every product and are beyond a per
// product check; that reading is recorded with the contract census.
type LoadedUnitUseReader struct{}

// ProductUnitInUseTx reports whether any pricing row of productID, or
// of the product's category matched the way the resolver matches it,
// names uom, inside the caller's transaction. A nil categoryID reads
// the product scoped families only.
func (LoadedUnitUseReader) ProductUnitInUseTx(ctx context.Context, tx pgx.Tx, productID string, categoryID *string, uom string) (bool, error) {
	var named bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pricing_list_prices
			 WHERE product_id = $1::uuid AND price_uom = $2
		) OR EXISTS (
			SELECT 1 FROM pricing_rules
			 WHERE product_id = $1::uuid AND price_uom = $2
		) OR EXISTS (
			SELECT 1 FROM pricing_rules
			 WHERE scope IN ('CUSTOMER_CATEGORY','TIER_CATEGORY')
			   AND category_id = $3::uuid AND price_uom = $2
		) OR EXISTS (
			SELECT 1 FROM quantity_break_headers
			 WHERE product_id = $1::uuid AND threshold_uom = $2
		) OR EXISTS (
			SELECT 1 FROM quantity_break_headers
			 WHERE category_id = $3::uuid AND threshold_uom = $2
		) OR EXISTS (
			SELECT 1
			  FROM quantity_break_items qi
			  JOIN quantity_break_headers qh ON qh.id = qi.header_id
			 WHERE qh.product_id = $1::uuid AND qi.price_uom = $2
		) OR EXISTS (
			SELECT 1
			  FROM quantity_break_items qi
			  JOIN quantity_break_headers qh ON qh.id = qi.header_id
			 WHERE qh.category_id = $3::uuid AND qi.price_uom = $2
		) OR EXISTS (
			SELECT 1 FROM pricing_promotions
			 WHERE product_id = $1::uuid AND price_uom = $2
		) OR EXISTS (
			SELECT 1 FROM pricing_clearance_items
			 WHERE product_id = $1::uuid AND price_uom = $2
		) OR EXISTS (
			SELECT 1 FROM pricing_uom_conversions
			 WHERE product_id = $1::uuid AND (from_uom = $2 OR to_uom = $2)
		) OR EXISTS (
			SELECT 1 FROM customer_category_overrides
			 WHERE category_id = $3::uuid AND price_uom = $2
		)`, productID, uom, categoryID).Scan(&named)
	if err != nil {
		return false, fmt.Errorf("pricing: loaded unit use read: %w", err)
	}
	return named, nil
}
