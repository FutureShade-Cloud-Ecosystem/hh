package payment

import (
	"context"
	"errors"
)

// -- the Processor seam (contract-exact per SHARED CROSS-MODULE CONTRACTS) --
//
// SCAFFOLD-OWNED SHARED SEAM. The Processor interface + the result/input shapes below are
// the stable contract A-03 (till) consumes via Service.Charge and that future processor
// variants (live client, terminal) implement. Module agents fill in bodies; do not change
// these signatures without a co-sign.

// ChargeResult is a Processor's low-level response. Contract-exact: {ProcessorRef, Status}.
type ChargeResult struct {
	ProcessorRef string // the processor's transaction id → becomes payments.gateway_ref
	Status       string // captured | authorized | failed
}

// RefundResult is a Processor's low-level refund/void response, same shape as ChargeResult so the
// refund seam mirrors the charge seam. (W4: the SEAM exists; no Processor implements it yet.)
type RefundResult struct {
	ProcessorRef string // the processor's refund/void transaction id → A-05/till persists it
	Status       string // refunded | voided | failed
}

// ErrNotImplemented is the sentinel a Processor returns for a seam method that is wired but not yet
// built (W4 Refund). Callers can errors.Is() it to distinguish "not built yet" from a real failure.
var ErrNotImplemented = errors.New("payment: processor operation not implemented")

// ErrNoConfig means no active payment_config has been set up yet.
var ErrNoConfig = errors.New("payment: no active payment configuration")

// ErrSurchargeNotEnforced means surcharge_enabled=true was refused. No tender path on this server
// computes a surcharge (QuoteTender has no production caller), and D18 requires a recorded
// jurisdiction and its cap before enabling, none of which is recorded. Accepting the flag would
// store a setting nothing enforces while every charge stays surcharge-free, so the setting cannot
// be enabled. Turning it off is always accepted.
var ErrSurchargeNotEnforced = errors.New("payment: surcharge_enabled cannot be enabled: no tender path on this server computes a surcharge and no surcharge jurisdiction and cap is recorded (D18)")

// errBadCharge is returned before any processor dispatch when the charge inputs are invalid.
var errBadCharge = errors.New("payment: invalid charge (token and positive amount required)")

// Processor is a card processor the dealer transacts with directly (Run Payments). It receives the
// dealer's own creds + merchant id + a TOKENIZED instrument, never a PAN. Stateless: per-dealer
// secrets are injected at call time (so the same Processor value serves any dealer/config).
type Processor interface {
	Name() string
	Charge(ctx context.Context, creds map[string]string, merchantID string, amountCents int64, paymentToken string) (ChargeResult, error)
	// Refund reverses (full or partial) a prior capture identified by its processorRef. W4 completes
	// the SEAM only: RunPaymentsStub + RunPaymentsClient return (RefundResult{}, ErrNotImplemented).
	// The real impl is W5/A-05 (NSF/reversals), additive, no interface or migration change needed.
	Refund(ctx context.Context, creds map[string]string, merchantID string, processorRef string, amountCents int64) (RefundResult, error)
}

// -- the till-facing service shapes (NOT the raw Processor, the vault stays inside) --

// ChargeInput is what A-03's TakeTender passes for a CARD tender. The till computes amountCents
// (it already includes whatever tax the POS displayed, RUN-PAY is tax-blind).
type ChargeInput struct {
	AmountCents    int64  // > 0, cents, CAD
	PaymentToken   string // client-tokenized (Runner.js); the ONLY instrument arg, never a PAN
	Reference      string // payment_no / order ref for processor-side correlation
	IdempotencyKey string // client UUID, replay-safe; offline-first
	CardType       string // CREDIT | DEBIT | PREPAID | UNKNOWN, surcharge branch input (advisory at launch)
}

// ChargeReceipt is the till-facing outcome the till persists onto the payments row. Distinct from
// the low-level ChargeResult so the contract's ChargeResult stays pristine.
type ChargeReceipt struct {
	Processor    string // → payments.processor
	ProcessorRef string // → payments.gateway_ref
	Status       string // captured | authorized | failed
}

// errBadRefund is returned before any processor dispatch when the refund inputs are invalid.
var errBadRefund = errors.New("payment: invalid refund (original processor ref and positive amount required)")

// RefundInput is what a refund caller (the POS return flow, via its main.go adapter) passes to reverse
// a prior capture. ProcessorRef is the ORIGINAL capture's gateway_ref, never a PAN, never a token.
type RefundInput struct {
	AmountCents    int64  // > 0, cents, CAD (full or partial)
	ProcessorRef   string // the original capture's payments.gateway_ref
	Reference      string // return_no / correlation ref
	IdempotencyKey string // replay-safe refund key
}

// RefundReceipt is the caller-facing outcome; ProcessorRef is the REVERSAL's own gateway ref (persisted
// by the till onto the REFUNDED payments row, the till stays the sole payments writer).
type RefundReceipt struct {
	Processor    string
	ProcessorRef string
	Status       string // refunded | voided | failed
}
