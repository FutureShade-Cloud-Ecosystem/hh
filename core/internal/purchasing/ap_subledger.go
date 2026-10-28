package purchasing

// ap_subledger.go, the module's own read of the vendor-bill side of the AP subledger, for the month
// walk's tie-out. GL 2100 carries this balance PLUS the landed-cost payable (landedcost posts its
// freight/duty/handling payable to the same account), so the tie reads each side through its own
// module's seam and no module's SQL names another module's table.

import (
	"context"
	"fmt"
	"time"
)

// PostedBillTotalCentsAsOf returns the CAD total of every POSTED vendor bill dated on or before
// asOf: the vendor-bills component of the AP subledger balance. This is a company-level total for
// the ledger tie-out, not a staff surface, so it carries no caller gate and no branch scope.
func (s *Service) PostedBillTotalCentsAsOf(ctx context.Context, asOf time.Time) (int64, error) {
	return s.repo.PostedBillTotalCentsAsOf(ctx, asOf)
}

// PostedBillTotalCentsAsOf is the repository read behind the service seam.
func (r *Repository) PostedBillTotalCentsAsOf(ctx context.Context, asOf time.Time) (int64, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	var total int64
	if err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(total_cents), 0)
		  FROM purchasing_bills
		 WHERE status = 'POSTED'
		   AND bill_date <= $1::date`, asOf).Scan(&total); err != nil {
		return 0, fmt.Errorf("purchasing: posted bill total as of %s: %w", asOf.Format("2006-01-02"), err)
	}
	return total, nil
}
