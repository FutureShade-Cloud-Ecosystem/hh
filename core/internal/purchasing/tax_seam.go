package purchasing

// tax_seam.go, the input tax credits side of the tax module's HST reconcile (FW1-11). purchasing owns
// the vendor bills; tax reads the period's bill tax through this seam and never the table.

import (
	"context"
	"fmt"
)

// VendorBillITCCents sums the input tax credits on the period's POSTED vendor bills, by the bills' own
// bill_date: the 2200 debits side of the tie-out, shown apart from the collected side and netted
// against it. month 0 is the whole year; branchID "" is consolidated.
func (r *Repository) VendorBillITCCents(ctx context.Context, year, month int, branchID string) (int64, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	var total int64
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(tax_cents), 0)
		FROM purchasing_bills
		WHERE status = 'POSTED'
		  AND EXTRACT(YEAR FROM bill_date) = $1
		  AND ($2 = 0 OR EXTRACT(MONTH FROM bill_date) = $2)
		  AND ($3 = '' OR branch_id = $3::uuid)`, year, month, branchID).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("purchasing: vendor bill itc: %w", err)
	}
	return total, nil
}
