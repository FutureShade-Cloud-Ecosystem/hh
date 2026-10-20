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

import (
	"context"
	"fmt"
	"strings"
)

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

// RequirePriceRow reports, in plain words, whether the alias can be priced: it is a configured,
// enabled vendor alias and the price table holds a row for its alias and model. The demo boot
// calls it before it will carry a key, so a missing row stops the boot instead of refusing every
// paid call at run time (the meter refuses a paid call with no row). The row is an operator step,
// documented in docs/runbooks/demo-copies.md, and is never seeded.
func RequirePriceRow(ctx context.Context, aliases AliasStore, prices PriceStore, alias string) error {
	rows, err := aliases.Aliases(ctx)
	if err != nil {
		return fmt.Errorf("the alias table cannot be read, so %s cannot be priced: %w", alias, err)
	}
	for _, row := range rows {
		if row.Alias != alias {
			continue
		}
		if !row.Enabled || row.AdapterKind == AdapterStub || strings.TrimSpace(row.Model) == "" || row.Model == stubModelName {
			break
		}
		_, ok, err := prices.PriceFor(ctx, alias, row.Model)
		if err != nil {
			return fmt.Errorf("the price table cannot be read, so %s with model %s cannot be priced: %w", alias, row.Model, err)
		}
		if !ok {
			return fmt.Errorf("no price row in assist_model_prices for alias %s and model %s: a paid call with no price is refused, so insert the vendor's published prices before the key is placed (docs/runbooks/demo-copies.md)", alias, row.Model)
		}
		return nil
	}
	return fmt.Errorf("alias %s is not an enabled vendor alias with a model in the alias table, so it cannot be priced (docs/runbooks/demo-copies.md)", alias)
}
