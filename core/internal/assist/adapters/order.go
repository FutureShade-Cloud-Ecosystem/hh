package adapters

// order.go, assist.OrderReader over the order module's caller-scoped summary read.

import (
	"context"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
)

// OrderSummaryReader is what this adapter needs from the order module.
type OrderSummaryReader interface {
	SummaryByNumber(ctx context.Context, scope order.ReadScope, orderNo string) (order.Summary, bool, error)
}

type orderAdapter struct{ svc OrderSummaryReader }

var _ assist.OrderReader = orderAdapter{}

// NewOrderReader builds the seam. A nil service returns a nil reader, see NewStockReader.
func NewOrderReader(svc OrderSummaryReader) assist.OrderReader {
	if svc == nil {
		return nil
	}
	return orderAdapter{svc: svc}
}

// OrderByNumber resolves the VISIBLE order number as the asking user.
//
// found=false covers three situations that are one sentence to the person asking, no such number, a
// number at a branch they cannot reach, and no branch grants at all, and it is returned rather than
// an error for all three. The order module collapses them deliberately (see order/summary.go): a
// distinct "that exists but is not yours" would hand back exactly the fact the branch-scoped resolve
// exists to withhold, and the command's own note already says "or it may belong to a branch you
// don't have access to", which is honest without being an oracle.
func (a orderAdapter) OrderByNumber(ctx context.Context, c assist.Caller, orderNo string) (assist.OrderRecord, bool, error) {
	s, found, err := a.svc.SummaryByNumber(ctx, orderReach(c), orderNo)
	if err != nil {
		return assist.OrderRecord{}, false, err
	}
	if !found {
		return assist.OrderRecord{}, false, nil
	}
	return assist.OrderRecord{
		// DIB-465: the uuid travels beside the visible number so the answer can carry a deep link.
		// The summary read already returns it; nothing new is queried for this.
		OrderID:      s.OrderID,
		OrderNo:      s.OrderNo,
		Status:       s.Status,
		CustomerName: s.CustomerName,
		BranchName:   s.BranchName,
		TotalCents:   s.TotalCents,
		PlacedAt:     s.PlacedAt,
		LineCount:    s.LineCount,
	}, true, nil
}
