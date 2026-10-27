package inventory

// stock_value_seam.go, FW1-09d: the CONSOLIDATED stock value the GL's close pre flight reads. The pre
// flight ties the company's 1200 balance against the company's stock, so it asks for one figure: every
// branch's on hand at that branch's weighted average cost, valued in ONE statement in exact NUMERIC and
// answered as integer cents. The count sheet's own snapshot (count_seam.go) stays branch scoped; this
// one answers for the company as a whole (and for one branch, when the caller names it).

import (
	"context"
)

// StockValueCents values everything on hand at a branch (an empty branch id is the COMPANY, the way
// the GL's consolidated reads take it), half away from zero per row - the rounding this module's own
// valuation writes - and never brings a quantity into Go as a float on the way: the database multiplies
// the DECIMAL quantity by the BIGINT cost and the caller receives integer cents (FW1-09d round 2; the
// float64 product this read replaced refused a month whose books tied, by -$0.01). The month close
// holds no transaction, and the year end's pre flight reads stock on its own connection by design.
func (s *Service) StockValueCents(ctx context.Context, branchID string) (int64, error) {
	return s.db().StockValueCents(ctx, nil, branchID)
}
