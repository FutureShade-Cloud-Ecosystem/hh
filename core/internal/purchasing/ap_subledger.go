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

// APPaymentsReader supplies the settlement side of the AP subledger: the supplier payments the
// ledger carries against trade payables, as of an instant. Purchasing owns no ledger tables, so the
// composition satisfies this over the GL (see the glPaidAPReader adapter in cmd/core).
type APPaymentsReader interface {
	PaidAPCentsAsOf(ctx context.Context, asOf time.Time) (int64, error)
}

// WithAPPaymentsReader injects the settlement side of the AP subledger read. Unwired, OpenAPCentsAsOf
// refuses closed rather than reporting a balance that silently ignores settlements.
func (s *Service) WithAPPaymentsReader(r APPaymentsReader) *Service { s.apPayments = r; return s }

// OpenAPCentsAsOf is the AP subledger balance as of an instant: posted vendor bills dated on or
// before asOf, less the supplier payments settled against trade payables as of the same instant.
// This is the one read the month walk's AP tie consumes; it is a company-level total for the
// tie-out, not a staff surface, so it carries no caller gate and no branch scope.
func (s *Service) OpenAPCentsAsOf(ctx context.Context, asOf time.Time) (int64, error) {
	if s.apPayments == nil {
		return 0, ErrAPPaymentsReaderUnwired
	}
	bills, err := s.repo.PostedBillTotalCentsAsOf(ctx, asOf)
	if err != nil {
		return 0, err
	}
	paid, err := s.apPayments.PaidAPCentsAsOf(ctx, asOf)
	if err != nil {
		return 0, err
	}
	return bills - paid, nil
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
