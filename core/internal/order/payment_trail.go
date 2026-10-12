package order

import "context"

// PaymentTrailReader says whether a caller holds the payment trail reads (ruling 87: a payment's
// history and an invoice's payments, held to money desk or counter till WORK). The activity stream
// carries payments only for a caller it admits. The composition root supplies it over the role gate,
// so this module names no role and no level.
type PaymentTrailReader interface {
	HoldsPaymentTrail(ctx context.Context, platformUserID, orgID string) (bool, error)
}

// WithPaymentTrail binds the reader. Unbound, no caller holds the trail: the activity stream then
// leaves the payments out for everyone (least privilege for an unwired composition).
func (h *Handler) WithPaymentTrail(t PaymentTrailReader) *Handler {
	h.trail = t
	return h
}
