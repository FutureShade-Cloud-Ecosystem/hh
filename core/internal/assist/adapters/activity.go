package adapters

// activity.go, DIB-494. assist.OrderActivityReader over the order module's caller-scoped merged
// audit trail (order.ActivityByNumber).
//
// The adapter's whole job is vocabulary: order.Summary → assist.OrderRecord, order event kind →
// assist event kind. The AUTHORITY work, the branch-scoped resolve, the post-load reaches() check,
// the collapse of "not yours" into found=false, happens inside the order module, behind the same
// SummaryByNumber fence the order.lookup adapter crosses (order.go), so this file cannot widen
// anything: it hands over orderReach(c) and carries the answer through untouched.

import (
	"context"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
)

// OrderTrailReader is what this adapter needs from the order module.
type OrderTrailReader interface {
	ActivityByNumber(ctx context.Context, scope order.ReadScope, ref string) (order.Summary, []order.OrderActivityEvent, bool, error)
}

// The shipped service satisfies the seam, a build error HERE, not in someone else's wiring paste.
var _ OrderTrailReader = (*order.Service)(nil)

type orderActivityAdapter struct{ svc OrderTrailReader }

var _ assist.OrderActivityReader = orderActivityAdapter{}

// NewOrderActivityReader builds the seam. A nil service returns a nil reader, the "unwired seam"
// contract every constructor in this package keeps (see NewStockReader).
func NewOrderActivityReader(svc OrderTrailReader) assist.OrderActivityReader {
	if svc == nil {
		return nil
	}
	return orderActivityAdapter{svc: svc}
}

// ActivityFor reads one order's merged change trail as the asking user.
//
// found=false covers the same three situations OrderByNumber's does (no such number, another
// branch's number, no grants at all) because it is literally the same resolve, the order module
// collapses them once and this carries the collapse through.
func (a orderActivityAdapter) ActivityFor(ctx context.Context, c assist.Caller, orderRef string) (assist.OrderRecord, []assist.OrderAuditEvent, bool, error) {
	sum, rows, found, err := a.svc.ActivityByNumber(ctx, orderReach(c), orderRef)
	if err != nil || !found {
		return assist.OrderRecord{}, nil, false, err
	}
	o := assist.OrderRecord{
		OrderID:      sum.OrderID,
		OrderNo:      sum.OrderNo,
		Status:       sum.Status,
		CustomerName: sum.CustomerName,
		BranchName:   sum.BranchName,
		TotalCents:   sum.TotalCents,
		PlacedAt:     sum.PlacedAt,
		LineCount:    sum.LineCount,
	}
	events := make([]assist.OrderAuditEvent, 0, len(rows))
	for _, r := range rows {
		events = append(events, assist.OrderAuditEvent{
			Kind:       trailKind(r.Kind),
			FromStatus: r.FromStatus,
			ToStatus:   r.ToStatus,
			Reason:     r.Reason,
			Field:      r.Field,
			LineNo:     r.LineNo,
			Before:     r.Before,
			After:      r.After,
			ActorID:    r.ActorID,
			ActorName:  r.ActorName,
			At:         r.OccurredAt,
		})
	}
	return o, events, true, nil
}

// trailKind maps the order module's event vocabulary onto the seam's. The two are spelled the
// same today; the mapping exists so a renaming on either side is a compile-time conversation here
// rather than a silently unrecognised kind in a command.
func trailKind(k string) string {
	switch k {
	case order.ActivityStatus:
		return assist.AuditKindStatus
	case order.ActivityEdit:
		return assist.AuditKindEdit
	default:
		return k
	}
}
