package payments

// run_gateway.go, the LIVE portal-rail processor behind HOS_PAYMENT_GATEWAY=run (DIB-416).
//
// Contract: the same VERIFIED Run Payments endpoint the POS card rail uses (POST {base}/api/v1/charge,
// Bearer api_key + mid, A/B/C result enum, docs.runpayments.io, verified 2026-06-29). This is a
// second client rather than a shared one because the two rails carry different request shapes:
// internal/payment charges a POS-tokenized card under a till session, this charges a VAULTED
// portal method under a customer. Sharing the transport would mean a seam that has to know which
// caller it is serving, which is how a payment client acquires an if-statement nobody can safely
// change later.
//
// - WHAT IS DELIBERATELY NOT IMPLEMENTED, AND WHY THAT IS THE HONEST CHOICE -
// Vault() and DetachMethod() return ErrLiveContractUnverified. Run's card-on-file tokenization is
// performed CLIENT-side by Runner.js against the dealer's public key, and the server-side
// vault/detach endpoints are not in the contract anyone here has verified. The tempting alternative
// is to guess a plausible endpoint and ship it. That would produce a rail that appears to store a
// customer's card and does not, discovered the first time someone tries to charge it, which is to
// say, at the worst possible moment, with a contractor on the phone.
//
// So the live rail can CHARGE (verified) and refuses, loudly and by name, to do the two things whose
// contract is unverified. A dealer running live today charges vaulted methods created while the rail
// was on the stub, or adds methods at checkout via nonce. When the vault contract is verified, this
// file grows two methods and nothing else changes.
//
// NO LIVE CALL IS EVER MADE IN CI. Every test drives this against httptest with throwaway strings.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ErrLiveContractUnverified, the live rail was asked for an operation whose Run contract has not
// been verified. Deliberately distinct from a transport failure: this can never succeed by retrying.
var ErrLiveContractUnverified = errors.New("payments: live gateway operation has no verified Run contract")

// RunGateway is the live processor. It holds no secrets beyond the ones handed to it at construction
// by the selector, and it never logs them.
type RunGateway struct {
	baseURL    string
	apiKey     string
	merchantID string
	httpc      *http.Client
}

// NewRunGateway builds the live client. There is no default base URL on purpose, the selector
// requires HOS_PAYMENT_BASE_URL, so a misconfigured box cannot accidentally reach a real host.
func NewRunGateway(baseURL, apiKey, merchantID string) *RunGateway {
	return &RunGateway{
		baseURL: baseURL, apiKey: apiKey, merchantID: merchantID,
		httpc: &http.Client{Timeout: 30 * time.Second},
	}
}

func (g *RunGateway) Name() string { return "run" }

// Live reports true. This is the fact the fail-closed boot guard reads, and it is the entire reason
// the guard asks the gateway rather than re-reading the mode string.
func (g *RunGateway) Live() bool { return true }

// runChargeBody is the VERIFIED /api/v1/charge request.
type runChargeBody struct {
	AccountToken string `json:"account_token"`
	MID          string `json:"mid"`
	Amount       int64  `json:"amount"` // integer cents
	Currency     string `json:"currency"`
	Capture      string `json:"capture"` // 'Y', a portal payment is auth+capture
	Vault        string `json:"vault"`   // 'N', the method is already vaulted
}

// runChargeReply is the VERIFIED /api/v1/charge response.
type runChargeReply struct {
	Result   string `json:"result"` // A=Approved, B=Retry, C=Declined
	RespCode string `json:"resp_code"`
	RespText string `json:"resp_text"`
	TransID  string `json:"trans_id"`
}

// Charge runs ONE live charge. The A/B/C result maps onto the tri-state ChargeOutcome the port
// already models, so a transient B can never collapse into a silent decline.
func (g *RunGateway) Charge(ctx context.Context, req ChargeRequest) (ChargeResult, error) {
	token := req.MethodToken
	if token == "" {
		token = req.Nonce // add-at-checkout: a one-time nonce charges directly
	}
	if token == "" {
		return ChargeResult{}, errors.New("payments: live charge needs a vaulted token or a nonce")
	}
	if req.AmountCents <= 0 {
		return ChargeResult{}, errors.New("payments: live charge amount must be positive")
	}

	body, err := json.Marshal(runChargeBody{
		AccountToken: token, MID: g.merchantID, Amount: req.AmountCents,
		Currency: req.Currency, Capture: "Y", Vault: "N",
	})
	if err != nil {
		return ChargeResult{}, fmt.Errorf("payments: marshal live charge: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+"/api/v1/charge", bytes.NewReader(body))
	if err != nil {
		return ChargeResult{}, fmt.Errorf("payments: build live charge: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+g.apiKey)
	// The server-bound key travels as a header even though Run does not honour one. It costs nothing,
	// it appears in the processor's request log, and it is the thing an operator matches a stuck
	// payment_intents row against during reconciliation (DIB-415).
	if req.IdempotencyKey != "" {
		httpReq.Header.Set("X-Idempotency-Key", req.IdempotencyKey)
	}

	resp, err := g.httpc.Do(httpReq)
	if err != nil {
		// A transport failure is an UNKNOWN outcome, not a decline. The caller leaves its intent
		// CHARGING and refuses to retry, see intent.go.
		return ChargeResult{}, fmt.Errorf("payments: live charge transport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ChargeResult{}, fmt.Errorf("payments: read live charge response: %w", err)
	}
	if resp.StatusCode >= 500 {
		// Server-side failure: also unknown. Never a decline.
		return ChargeResult{}, fmt.Errorf("payments: live charge http %d", resp.StatusCode)
	}

	var out runChargeReply
	if err := json.Unmarshal(raw, &out); err != nil {
		return ChargeResult{}, fmt.Errorf("payments: decode live charge response: %w", err)
	}
	switch out.Result {
	case "A":
		return ChargeResult{Outcome: OutcomeApproved, GatewayRef: out.TransID, AuthCode: out.RespCode, Message: out.RespText}, nil
	case "B":
		return ChargeResult{Outcome: OutcomeRetry, GatewayRef: out.TransID, AuthCode: out.RespCode, Message: out.RespText}, nil
	case "C":
		return ChargeResult{Outcome: OutcomeDeclined, GatewayRef: out.TransID, AuthCode: out.RespCode, Message: out.RespText}, nil
	default:
		// An unrecognised result is NOT an approval and NOT a decline. Saying so is the whole reason
		// ChargeOutcome is tri-state rather than a bool.
		return ChargeResult{}, fmt.Errorf("payments: unrecognised live result %q (%s): %s", out.Result, out.RespCode, out.RespText)
	}
}

// Vault is not implemented against an unverified contract, see the file header.
func (g *RunGateway) Vault(context.Context, VaultRequest) (VaultedMethod, error) {
	return VaultedMethod{}, fmt.Errorf("%w: server-side card-on-file vaulting (Run tokenizes client-side "+
		"via Runner.js; the vault endpoint is unverified)", ErrLiveContractUnverified)
}

// DetachMethod is not implemented against an unverified contract, see the file header.
func (g *RunGateway) DetachMethod(context.Context, string) error {
	return fmt.Errorf("%w: server-side method detach", ErrLiveContractUnverified)
}

// Compile-time proof the live gateway satisfies the same port the stub does.
var _ PaymentGateway = (*RunGateway)(nil)
