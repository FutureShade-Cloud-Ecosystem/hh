package purchasing

// unitusereader.go is the purchasing module's read the product module's
// unit set door checks a removal through (HH-LOAD-1 LD-3 round 2):
// whether any supplier row of one product still sells in one unit. The
// vendor skus are this module's table (0197), so the reader lives here
// and the unit set writer declares the narrow interface it needs, taken
// by constructor at the wiring. It runs on the caller's transaction
// (the load frame's), opens no connection, and commits nothing.

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// LoadedUnitUseReader answers the product unit set door's removal
// question for the supplier leg: a vendor sku that sells the product in
// the unit is a live conversion the supplier's own catalogue carries
// (one skid holds sixty pieces), and LD-4's supplier sku loading feeds
// it.
type LoadedUnitUseReader struct{}

// ProductUnitInUseTx reports whether any vendor sku of productID sells
// in uom, inside the caller's transaction.
func (LoadedUnitUseReader) ProductUnitInUseTx(ctx context.Context, tx pgx.Tx, productID, uom string) (bool, error) {
	var named bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM purchasing_vendor_skus
			 WHERE product_id = $1::uuid AND vendor_uom = $2
		)`, productID, uom).Scan(&named)
	if err != nil {
		return false, fmt.Errorf("purchasing: loaded unit use read: %w", err)
	}
	return named, nil
}
