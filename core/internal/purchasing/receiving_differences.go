package purchasing

// receiving_differences.go, the short-receipt count behind the yard manager's Needs-you row (0380).
//
// A receiving difference is a purchase order that landed part of what was ordered and was neither
// finished nor closed: status PARTIAL with at least one line still short. It leaves the count when
// the rest lands (RECEIVED) or when the order is deliberately closed short (CLOSED), which is the
// settle action the row links to. The count lives here because the order and receipt tables are this
// module's; the dashboard's adapter asks through this seam.

import (
	"context"
	"fmt"
	"time"
)

// ReceivingDifferenceSummary is how many orders sit short and when the oldest last landed anything.
type ReceivingDifferenceSummary struct {
	Count  int
	Oldest time.Time
}

type receivingDifferenceStore interface {
	ReceivingDifferenceSummary(ctx context.Context, reach BranchReach) (ReceivingDifferenceSummary, error)
}

// ReceivingDifferences counts the short-received orders inside the reach.
func (s *Service) ReceivingDifferences(ctx context.Context, reach BranchReach) (ReceivingDifferenceSummary, error) {
	if s.repo == nil {
		return ReceivingDifferenceSummary{}, errNoDB
	}
	var st receivingDifferenceStore = s.repo
	return st.ReceivingDifferenceSummary(ctx, reach)
}

// receivingDifferenceSQL: one row per PARTIAL order with a short line, aged by its latest receipt
// (an order that landed 40 of 100 yesterday is a day old, whatever day it was placed). The branch
// predicate is always present, the fence the incoming read documents.
const receivingDifferenceSQL = `
	SELECT count(*)::int, min(last_landed)
	  FROM (
	    SELECT p.id, (SELECT max(r.received_at) FROM purchasing_receipts r
	                   WHERE r.po_id = p.id AND r.deleted_at IS NULL) AS last_landed
	      FROM purchasing_pos p
	     WHERE p.status = $1
	       AND ($2::uuid[] IS NULL OR p.branch_id = ANY($2::uuid[]))
	       AND EXISTS (SELECT 1 FROM purchasing_po_lines pl
	                    WHERE pl.po_id = p.id AND pl.qty_ordered_base > pl.qty_received_base)
	  ) short`

// ReceivingDifferenceSummary is the pgx half.
func (r *Repository) ReceivingDifferenceSummary(ctx context.Context, reach BranchReach) (ReceivingDifferenceSummary, error) {
	if r.pool == nil {
		return ReceivingDifferenceSummary{}, errNoDB
	}
	if reach.reachesNothing() {
		return ReceivingDifferenceSummary{}, nil
	}
	var out ReceivingDifferenceSummary
	var oldest *time.Time
	if err := r.pool.QueryRow(ctx, receivingDifferenceSQL, StatusPartial, reach.branchFilter()).
		Scan(&out.Count, &oldest); err != nil {
		return ReceivingDifferenceSummary{}, fmt.Errorf("purchasing: receiving differences: %w", err)
	}
	if oldest != nil {
		out.Oldest = *oldest
	}
	return out, nil
}
