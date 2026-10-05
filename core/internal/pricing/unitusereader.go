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
// headers and their items, and the sales conversion edges, any row of
// those naming the unit for the product is a removal refused.
type LoadedUnitUseReader struct{}

// ProductUnitInUseTx reports whether any pricing row of productID names
// uom, inside the caller's transaction.
func (LoadedUnitUseReader) ProductUnitInUseTx(ctx context.Context, tx pgx.Tx, productID, uom string) (bool, error) {
	var named bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pricing_list_prices
			 WHERE product_id = $1::uuid AND price_uom = $2
		) OR EXISTS (
			SELECT 1 FROM pricing_rules
			 WHERE product_id = $1::uuid AND price_uom = $2
		) OR EXISTS (
			SELECT 1 FROM quantity_break_headers
			 WHERE product_id = $1::uuid AND threshold_uom = $2
		) OR EXISTS (
			SELECT 1
			  FROM quantity_break_items qi
			  JOIN quantity_break_headers qh ON qh.id = qi.header_id
			 WHERE qh.product_id = $1::uuid AND qi.price_uom = $2
		) OR EXISTS (
			SELECT 1 FROM pricing_uom_conversions
			 WHERE product_id = $1::uuid AND (from_uom = $2 OR to_uom = $2)
		)`, productID, uom).Scan(&named)
	if err != nil {
		return false, fmt.Errorf("pricing: loaded unit use read: %w", err)
	}
	return named, nil
}
