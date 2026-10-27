package pos

// tax_seam.go, the POS returns side of the tax module's HST reconcile (FW1-11). pos owns the
// pos_returns table; tax reads the period's returned tax through this seam and never the table.

import (
	"context"
	"fmt"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// ReturnHSTCents sums the tax refunded on committed POS returns (pos_returns.tax_cents) in the
// period, bounded by the dealer's own business day zone. A POS return debits 2200 through the
// pos.return.committed outbox consumer without writing an invoices credit memo row, so the tax
// reconcile reads it here as its third document line (returns) and subtracts it alongside the
// vendor bills' input tax credits. month 0 is the whole year; branchID "" is consolidated.
func (r *Repository) ReturnHSTCents(ctx context.Context, year, month int, branchID string) (int64, error) {
	if r.pool == nil {
		return 0, errNoPool
	}
	zone, err := houseday.Resolve(ctx, r.zone)
	if err != nil {
		return 0, fmt.Errorf("pos: resolve business zone for return hst: %w", err)
	}
	startMonth := time.Month(month)
	months := 1
	if month <= 0 {
		startMonth = time.January
		months = 12
	}
	since := time.Date(year, startMonth, 1, 0, 0, 0, 0, zone.Location())
	until := since.AddDate(0, months, 0)
	var total int64
	err = r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(tax_cents), 0)
		FROM pos_returns
		WHERE created_at >= $1
		  AND created_at < $2
		  AND ($3 = '' OR branch_id = $3::uuid)`, since, until, branchID).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("pos: return hst: %w", err)
	}
	return total, nil
}
