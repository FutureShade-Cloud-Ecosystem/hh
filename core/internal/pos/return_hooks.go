package pos

// return_hooks.go, pos-DECLARED consumer seam for the W7 return restock (the INV-CORE mirror of
// sale_hooks.go's StockDecrementer). Ships with a BEHAVIOR-PRESERVING NoopRestock default so a bare
// service commits returns without stock moves until main.go wires the real *inventory.Service adapter
// (posRestockAdapter, the posStockAdapter pattern). Inventory stays the sole stock writer.

import (
	"context"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/jackc/pgx/v5"
)

// StockRestocker books a committed return back into stock inside the return's tx. Implemented by
// inventory.RestockForReturnTx: one RETURN move per line, to_location = branch, valued at the CURRENT
// weighted average (the average is unchanged), balance +qty.
type StockRestocker interface {
	RestockForReturnTx(ctx context.Context, tx pgx.Tx, in RestockInput) error
	// ReturnMoveValuesTx answers the return's RETURN moves' per line values (round 2, S3's P1): the
	// order.returned event carries them, so the COGS reversal posts the value that moved.
	ReturnMoveValuesTx(ctx context.Context, tx pgx.Tx, returnID string) ([]inventory.ShipLineValue, error)
	// ShippedValueForOrderLineTx answers the value the ORIGINAL SALE moved for one order line
	// (round 2, S3's P1): the write off path posts it, because goods that were written off
	// have no move of their own and their cost is the cost they left the shelf with.
	ShippedValueForOrderLineTx(ctx context.Context, tx pgx.Tx, orderID, productID string, qtyBase float64) (inventory.ShipLineValue, bool, error)
}

// RestockInput is the restock request for one committed return.
type RestockInput struct {
	ReturnID string
	OrderID  string
	BranchID string
	ReturnNo string
	Lines    []StockLine
}

// NoopRestock is the behavior-preserving default: it moves no stock (identical in spirit to NoopStock).
type NoopRestock struct{}

// RestockForReturnTx does nothing. FROZEN.
func (NoopRestock) RestockForReturnTx(context.Context, pgx.Tx, RestockInput) error { return nil }

// ReturnMoveValuesTx answers no values (nothing moved).
func (NoopRestock) ReturnMoveValuesTx(context.Context, pgx.Tx, string) ([]inventory.ShipLineValue, error) {
	return nil, nil
}

// ShippedValueForOrderLineTx answers no value (nothing moved, so nothing to carry).
func (NoopRestock) ShippedValueForOrderLineTx(context.Context, pgx.Tx, string, string, float64) (inventory.ShipLineValue, bool, error) {
	return inventory.ShipLineValue{}, false, nil
}
