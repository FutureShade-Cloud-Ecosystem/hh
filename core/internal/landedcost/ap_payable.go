package landedcost

// ap_payable.go, the module's own read of the landed-cost payable for the month walk's AP tie-out.
// The landed-cost documents post their freight/duty/handling payable to GL 2100, the same account
// the vendor bills ride, so the tie reads this balance through the module's own seam rather than
// naming the module's table from the outside.

import (
	"context"
	"fmt"
	"time"
)

// OutstandingPayableCentsAsOf returns the landed-cost payable still owed at asOf: the CAD total of
// every document applied on or before asOf and not yet voided at asOf.
//
// The landed_costs header carries no applied_at/voided_at columns, so the read rides the status
// stamps SetStatusTx writes: a POSTED (or retro-adjusted) header's updated_at is its apply, a
// VOIDED header's updated_at is its void, and created_at always precedes the apply. A document
// created after asOf is never counted, so a document that does not exist at asOf cannot enter the
// balance from either side. The one shape the read cannot separate is a document created before
// asOf but applied after it and voided later still: created_at admits it while the GL at asOf has
// nothing of it. Apply follows Create within one posting in this product's every path, so the
// window is the apply call's own length.
func (s *Service) OutstandingPayableCentsAsOf(ctx context.Context, asOf time.Time) (int64, error) {
	return s.repo.OutstandingPayableCentsAsOf(ctx, asOf)
}

// OutstandingPayableCentsAsOf is the repository read behind the service seam.
func (r *Repository) OutstandingPayableCentsAsOf(ctx context.Context, asOf time.Time) (int64, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	var total int64
	if err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(freight_cad_cents + duty_cad_cents + handling_cad_cents), 0)
		  FROM landed_costs
		 WHERE created_at <= $1
		   AND (
		         (status IN ('POSTED', 'RETRO_ADJUSTED') AND updated_at <= $1)
		      OR (status = 'VOIDED' AND updated_at > $1)
		       )`, asOf).Scan(&total); err != nil {
		return 0, fmt.Errorf("landedcost: outstanding payable as of %s: %w", asOf.Format("2006-01-02 15:04:05"), err)
	}
	return total, nil
}
