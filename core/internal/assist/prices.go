package assist

// prices.go, the append only price table behind settlement.
//
// assist_model_prices(alias, model, price per million tokens in, out and cached, effective seq).
// A settlement uses the row in force when the call was made and records its id, so a past month
// is billed at the prices of that month. A price change is a new row with a higher seq.
//
// Prices are integer MICRO CENTS per million tokens, and cost is rounded UP to a whole micro cent
// per call: the ledger never under-records. There is no markup column. Markup is applied in the
// billing export.

import "context"

// ModelPrice is one row of the price table.
type ModelPrice struct {
	ID              string
	Alias           string
	Model           string
	InMicroPerM     int64
	OutMicroPerM    int64
	CachedMicroPerM int64
	Seq             int64
}

// Cost is the cost of a call in micro cents, rounded up.
func (p ModelPrice) Cost(in, out, cached int) int64 {
	total := int64(in)*p.InMicroPerM + int64(out)*p.OutMicroPerM + int64(cached)*p.CachedMicroPerM
	if total <= 0 {
		return 0
	}
	return (total + 999_999) / 1_000_000
}

// PriceStore reads the price in force for an alias and model: the row with the highest seq.
type PriceStore interface {
	PriceFor(ctx context.Context, alias, model string) (ModelPrice, bool, error)
}
