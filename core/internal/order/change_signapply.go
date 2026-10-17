package order

// change_signapply.go, SCOPE:F10 stage 3: the manager signs and applies a change in one tap.
//
// SKELETON. This file holds only the names stage 3 will build on, so the red tests compile and fail
// on their assertions. Every behaviour below is a stub that refuses; the build replaces each one.

import (
	"context"
	"errors"
)

// TaxRateReader answers the dealer's standard tax rate in basis points on a date ("" is today), the
// seam the quote builder already reads the same rate through. Declared here, in the consumer, and
// satisfied in main.go by the tax engine.
type TaxRateReader interface {
	StandardRateBps(ctx context.Context, asOf string) (int, error)
}

// WithStandardRate injects the tax rate seam.
func (s *Service) WithStandardRate(r TaxRateReader) *Service { s.taxRate = r; return s }

// ErrChangeKeyReused: the Idempotency-Key was already used for a different change order.
var ErrChangeKeyReused = errors.New("order: this idempotency key was used for a different change order; use a new key for a new action")

// errSignApplyUnbuilt marks the skeleton. It is never mapped to a status and never reaches a caller
// once stage 3 is built.
var errSignApplyUnbuilt = errors.New("order: sign and apply is not built yet")

// signApplyWrite is what the service hands the store for one sign and apply.
type signApplyWrite struct {
	ChangeID string
	Key      string
	Signer   string
	Note     string
}

// SignApplyChange signs (when a finance signature is still owed) and applies a change, once.
func (s *Service) SignApplyChange(ctx context.Context, changeID, idempotencyKey, note string) (ChangeOrder, error) {
	return ChangeOrder{}, errSignApplyUnbuilt
}
