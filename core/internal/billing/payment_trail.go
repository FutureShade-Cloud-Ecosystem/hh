package billing

import (
	"context"
	"sync"
)

// PaymentTrailReader says whether a caller holds the payment trail reads: the two routes ruling 87
// holds to money desk WORK (a payment's history and an invoice's payments). The composition root
// builds it over the role gate's own admission to those routes, so the answer is the gate's and
// moves with the document's annotation; billing never names a role or compares a level itself.
type PaymentTrailReader interface {
	HoldsPaymentTrail(ctx context.Context, platformUserID, orgID string) (bool, error)
}

// LatePaymentTrail is a PaymentTrailReader bound after the routes are mounted: the register mounts
// with the money module, before the role gate's reader exists. Unbound it answers false, so the
// trail stays withheld until the composition binds the real one.
type LatePaymentTrail struct {
	mu sync.RWMutex
	r  PaymentTrailReader
}

// Bind sets the reader the answers come from.
func (l *LatePaymentTrail) Bind(r PaymentTrailReader) {
	l.mu.Lock()
	l.r = r
	l.mu.Unlock()
}

// HoldsPaymentTrail asks the bound reader, or says no when none is bound.
func (l *LatePaymentTrail) HoldsPaymentTrail(ctx context.Context, platformUserID, orgID string) (bool, error) {
	l.mu.RLock()
	r := l.r
	l.mu.RUnlock()
	if r == nil {
		return false, nil
	}
	return r.HoldsPaymentTrail(ctx, platformUserID, orgID)
}
