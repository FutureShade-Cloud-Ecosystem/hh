package adapters

// stock.go, assist.StockReader over inventory's caller-scoped stock lookup.

import (
	"context"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
)

// StockLookup is what this adapter needs from the inventory module, declared here (the consumer
// declares the interface) so the adapter can be tested against a double that records the reach it
// was handed, which is the only way to assert from outside that the caller's scope actually
// travelled into the read.
type StockLookup interface {
	LookupStock(ctx context.Context, reach inventory.BranchReach, ref string) (inventory.ProductLookup, []inventory.BranchStock, bool, error)
}

// stockAdapter satisfies assist.StockReader.
type stockAdapter struct{ svc StockLookup }

var _ assist.StockReader = stockAdapter{}

// NewStockReader builds the seam. A nil service returns a NIL READER, not a reader that panics:
// assist.Deps treats a nil member as an unwired seam and answers that command with an honest "this
// lookup isn't switched on in this system yet" (503) while every other command keeps working. A
// partial wiring is a normal state during a rollout; a nil-pointer panic inside an assistant is an
// outage in a feature nobody depends on yet.
func NewStockReader(svc StockLookup) assist.StockReader {
	if svc == nil {
		return nil
	}
	return stockAdapter{svc: svc}
}

// StockFor answers "how many have we got" as the asking user.
//
// THE THREE-VALUED ANSWER IS CARRIED THROUGH UNCHANGED, because the two empties are different
// sentences and the command above says them differently (DIB-443):
//
//	found=false           → "I can't find a product matching …"
//	found=true, no rows   → "… has no stock on record yet."
//
// Collapsing them here, returning found=false for a product with no balances, or synthesising a row
// of zeros so the slice is never empty, would erase a distinction a tester noticed and a ticket was
// filed about. So neither happens.
func (a stockAdapter) StockFor(ctx context.Context, c assist.Caller, productRef string) (assist.ProductRef, []assist.StockRecord, bool, error) {
	p, rows, found, err := a.svc.LookupStock(ctx, stockReach(c), productRef)
	if err != nil {
		return assist.ProductRef{}, nil, false, err
	}
	if !found {
		return assist.ProductRef{}, nil, false, nil
	}
	ref := assist.ProductRef{ProductID: p.ProductID, SKU: p.SKU, Name: p.Name}
	out := make([]assist.StockRecord, 0, len(rows))
	for _, b := range rows {
		out = append(out, assist.StockRecord{
			BranchName: b.BranchName,
			OnHand:     b.OnHandBase,
			Reserved:   b.ReservedBase,
			// Available is the SERVER'S number, carried through untouched. This adapter must never
			// compute on_hand − reserved: inventory.AvailableBase is the one definition of that rule
			// and a second copy here would be a second thing to forget when it changes.
			Available: b.AvailableBase,
			// The UOM belongs to the PRODUCT, not to the branch row, every balance in
			// stock_balances is in the product's base unit. Taking it from the product is what stops
			// a quantity ever being printed against a unit it is not measured in.
			UOM: p.UOM,
		})
	}
	// A resolved product with no balance rows returns an EMPTY (non-nil) slice: the caller reads
	// len(rows) == 0 as "no stock on record", which is a true answer and not an absence.
	return ref, out, true, nil
}
