package pos

// return_hooks.go, pos-DECLARED consumer seam for the W7 return restock (the INV-CORE mirror of
// sale_hooks.go's StockDecrementer). Ships with a BEHAVIOR-PRESERVING NoopRestock default so a bare
// service commits returns without stock moves until main.go wires the real *inventory.Service adapter
// (posRestockAdapter, the posStockAdapter pattern). Inventory stays the sole stock writer.

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// StockRestocker books a committed return back into stock inside the return's tx. Implemented by
// inventory.RestockForReturnTx: one RETURN move per line, to_location = branch, valued at the CURRENT
// weighted average (the average is unchanged), balance +qty.
type StockRestocker interface {
	RestockForReturnTx(ctx context.Context, tx pgx.Tx, in RestockInput) error
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
