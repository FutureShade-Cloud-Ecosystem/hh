package adapters

// payment.go, assist.PaymentReader over the payments module's three-shape lookup.

import (
	"context"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
)

// PaymentFinder is what this adapter needs from internal/payments.
type PaymentFinder interface {
	FindPayments(ctx context.Context, reach payments.BranchReach, q payments.LookupQuery) ([]payments.PaymentMatch, error)
}

type paymentAdapter struct{ svc PaymentFinder }

var _ assist.PaymentReader = paymentAdapter{}

// NewPaymentReader builds the seam. A nil service returns a nil reader, see NewStockReader.
func NewPaymentReader(svc PaymentFinder) assist.PaymentReader {
	if svc == nil {
		return nil
	}
	return paymentAdapter{svc: svc}
}

// FindPayments answers the lookup as the asking user.
//
// THE AMOUNT IS PASSED THROUGH IN CENTS AND UNTOUCHED. The command layer has already parsed
// "$1,240.00" into 124000 and refused anything it could not read exactly (it rejects three decimal
// places rather than rounding them, because a lookup that silently searches for a different amount
// than the one on the screen is worse than one that says it did not understand). This adapter must
// not round, widen, or tolerate: an exact match is the contract on both sides of the seam, and the
// payments module states the same rule from its end.
//
// Limit is forwarded rather than re-decided. The command bounds a chat answer to ten records
// because a long list pasted into a chat window is unreadable; payments caps it again at its own
// ceiling. Two independent bounds is correct, neither module should have to trust the other's.
func (a paymentAdapter) FindPayments(ctx context.Context, c assist.Caller, q assist.PaymentQuery) ([]assist.PaymentRecord, error) {
	rows, err := a.svc.FindPayments(ctx, paymentReach(c), payments.LookupQuery{
		Reference:   q.Reference,
		AmountCents: q.AmountCents,
		CustomerRef: q.CustomerRef,
		Limit:       q.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]assist.PaymentRecord, 0, len(rows))
	for _, p := range rows {
		out = append(out, assist.PaymentRecord{
			PaymentNo: p.PaymentNo,
			// The gateway reference is the OTHER number a person might be holding, and the command
			// only prints it when it differs from the payment number, so a stub-gateway deployment
			// where the two are the same does not show the same string twice.
			Reference:    p.GatewayRef,
			CustomerName: p.CustomerName,
			BranchName:   p.BranchName,
			Method:       p.Method,
			Status:       p.Status,
			AmountCents:  p.AmountCents,
			ReceivedAt:   p.ReceivedAt,
		})
	}
	// No matches is an EMPTY slice and a nil error. The command turns that into "No payment matches
	// reference PMT-9, it may be under a different reference, or at a branch you don't have access
	// to", which is a complete answer.
	return out, nil
}
