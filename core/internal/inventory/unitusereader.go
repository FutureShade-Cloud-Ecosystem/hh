package inventory

// unitusereader.go is the inventory module's read the product module's
// unit set door checks a removal through (HH-LOAD-1 LD-3): whether any
// stock row of one product still names one unit. The stock ledger is
// this module's (0005), so the reader lives here and the unit set
// writer declares the narrow interface it needs, taken by constructor
// at the wiring. It runs on the caller's transaction (the load frame's),
// opens no connection, and commits nothing.

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// LoadedUnitUseReader answers the product unit set door's removal
// question for the stock leg: a move entered in the unit is a stock row
// that still names it.
type LoadedUnitUseReader struct{}

// ProductUnitInUseTx reports whether any stock move of productID was
// entered in uom, inside the caller's transaction.
func (LoadedUnitUseReader) ProductUnitInUseTx(ctx context.Context, tx pgx.Tx, productID, uom string) (bool, error) {
	var named bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM stock_moves
			 WHERE product_id = $1::uuid AND uom_entered = $2
		)`, productID, uom).Scan(&named)
	if err != nil {
		return false, fmt.Errorf("inventory: loaded unit use read: %w", err)
	}
	return named, nil
}
