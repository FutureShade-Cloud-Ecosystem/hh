package payment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// StubCaptureRefPrefix prefixes every capture ref the stub mints. The printed receipt reads it to
// mark simulated card charges on paper: a CARD payments row whose gateway_ref carries this prefix
// is a charge that never reached a processor, and the receipt says so beside the tender. A live
// gateway's adapter writes its own refs, only the stub mints this shape.
const StubCaptureRefPrefix = "rp_sim_"

// RunPaymentsStub is the DETERMINISTIC stand-in for the Run Payments /api/v1/charge keyed / card-NOT-
// present call (the gate default; no live network call). It mirrors the VERIFIED A/B/C outcome: a
// successful charge is the analogue of Run's result=='A' (Approved) → Status="captured", with a
// deterministic ProcessorRef standing in for trans_id so replays are stable in CI. The idempotency key
// reaches the stub via creds["__idem"] (Service.Charge injects it call-scoped) so a replay yields the
// same ref. A real terminal/keyed client slots in behind Processor unchanged.
type RunPaymentsStub struct{}

// Name returns the processor name.
func (RunPaymentsStub) Name() string { return "run_payments" }

// Charge "captures" a tokenized payment deterministically. Guards mirror the verified required keys.
func (RunPaymentsStub) Charge(_ context.Context, creds map[string]string, merchantID string, amountCents int64, paymentToken string) (ChargeResult, error) {
	if creds["api_key"] == "" {
		return ChargeResult{}, errors.New("missing processor api_key")
	}
	if merchantID == "" {
		return ChargeResult{}, errors.New("missing merchant id (mid)")
	}
	if paymentToken == "" {
		return ChargeResult{}, errors.New("missing payment token (card must be tokenized client-side)")
	}
	if amountCents <= 0 {
		return ChargeResult{}, errors.New("amount must be positive")
	}
	// Deterministic ref: prefer the idempotency key, else hash(token|amount|mid).
	seed := creds["__idem"]
	if seed == "" {
		seed = fmt.Sprintf("%s|%d|%s", paymentToken, amountCents, merchantID)
	}
	h := sha256.Sum256([]byte(seed))
	return ChargeResult{ProcessorRef: "rp_sim_" + hex.EncodeToString(h[:8]), Status: "captured"}, nil
}

// Refund reverses a prior capture DETERMINISTICALLY (the gate default; no live network call). It mirrors
// the verified void-or-refund outcome: a successful reversal is Run's result=='A' (Approved) → Status
// "refunded", with a deterministic ProcessorRef standing in for the reversal trans_id so replays are stable
// in CI. Guards mirror the required keys: the same creds + a non-empty processorRef + a positive amount.
// (W5/A-05: this replaces the W4 ErrNotImplemented seam, purely additive, no signature change.)
func (RunPaymentsStub) Refund(_ context.Context, creds map[string]string, merchantID, processorRef string, amountCents int64) (RefundResult, error) {
	if creds["api_key"] == "" {
		return RefundResult{}, errors.New("missing processor api_key")
	}
	if merchantID == "" {
		return RefundResult{}, errors.New("missing merchant id (mid)")
	}
	if processorRef == "" {
		return RefundResult{}, errors.New("missing processor ref (the original capture trans_id)")
	}
	if amountCents <= 0 {
		return RefundResult{}, errors.New("refund amount must be positive")
	}
	// Deterministic ref: prefer the idempotency key, else hash(processorRef|amount|mid).
	seed := creds["__idem"]
	if seed == "" {
		seed = fmt.Sprintf("%s|%d|%s", processorRef, amountCents, merchantID)
	}
	h := sha256.Sum256([]byte("refund|" + seed))
	return RefundResult{ProcessorRef: "rp_ref_" + hex.EncodeToString(h[:8]), Status: "refunded"}, nil
}
