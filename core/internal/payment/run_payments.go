package payment

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

// RunPaymentsClient is the live Run Payments processor (card-NOT-present / keyed-web), implementing
// Processor. Card data is tokenized client-side (Runner.js + the dealer's public key) so only an
// account_token reaches the server (PCI SAQ-A). Contract VERIFIED against the public OpenAPI
// (docs.runpayments.io, 2026-06-29): POST {base}/api/v1/charge, Bearer api_key + mid, A/B/C result enum.
//
// Per-dealer config comes from the decrypted payment_config creds map at call time:
//   - creds["api_key"]  (required), the dealer's Run Payments api_key (EXPIRING JWT; Bearer)
//   - creds["base_url"] (optional), overrides the default UAT/prod host
//
// CI exercises this via httptest only, there are NO live calls in the gate (the stub is the default).
type RunPaymentsClient struct {
	defaultBaseURL string
	httpc          *http.Client
}

// NewRunPaymentsClient builds a live client with a default base URL (overridable per dealer via creds).
func NewRunPaymentsClient(defaultBaseURL string) *RunPaymentsClient {
	return &RunPaymentsClient{defaultBaseURL: defaultBaseURL, httpc: &http.Client{Timeout: 30 * time.Second}}
}

// Name returns the processor name.
func (c *RunPaymentsClient) Name() string { return "run_payments" }

// runChargeRequest is the VERIFIED /api/v1/charge body (the required + W4-relevant optional fields).
type runChargeRequest struct {
	AccountToken string `json:"account_token"`
	MID          string `json:"mid"`
	Amount       int64  `json:"amount"` // integer cents
	Currency     string `json:"currency"`
	Capture      string `json:"capture"` // 'Y' auth+capture / 'N' auth-only
	Vault        string `json:"vault"`   // 'N', single-charge at W4 (no card-on-file)
}

// runChargeResponse is the VERIFIED /api/v1/charge response. result is an A/B/C enum.
type runChargeResponse struct {
	Result   string `json:"result"` // A=Approved, B=Retry, C=Declined
	RespCode string `json:"resp_code"`
	RespText string `json:"resp_text"`
	TransID  string `json:"trans_id"`
	CardType string `json:"card_type"`
}

// runRefreshRequest is the VERIFIED /api/v1/api_keys/refresh body. The docs show only "token"
// (the current, expiring api_key); the refresh_token's exact role is an open Q (ARCH_DELTA §10).
type runRefreshRequest struct {
	Token string `json:"token"`
}

// runRefreshResponse is the rotated credential set returned by /api/v1/api_keys/refresh.
type runRefreshResponse struct {
	APIKey       string `json:"api_key"`
	PublicKey    string `json:"public_key"`
	RefreshToken string `json:"refresh_token"`
}

// Charge auth+captures a tokenized payment via the dealer's Run Payments account (capture:'Y').
// The api_key is an EXPIRING JWT: on a 401 the client refreshes via /api/v1/api_keys/refresh,
// rotates the in-memory creds map (so Service can re-encrypt the active config), and retries once.
func (c *RunPaymentsClient) Charge(ctx context.Context, creds map[string]string, merchantID string, amountCents int64, paymentToken string) (ChargeResult, error) {
	apiKey := creds["api_key"]
	if apiKey == "" {
		return ChargeResult{}, fmt.Errorf("run_payments: missing api_key in payment config")
	}
	if merchantID == "" {
		return ChargeResult{}, fmt.Errorf("run_payments: missing mid")
	}
	if paymentToken == "" {
		return ChargeResult{}, fmt.Errorf("run_payments: missing payment token (card must be tokenized client-side)")
	}
	base := creds["base_url"]
	if base == "" {
		base = c.defaultBaseURL
	}
	if base == "" {
		return ChargeResult{}, fmt.Errorf("run_payments: no base_url configured")
	}
	currency := creds["currency"]
	if currency == "" {
		currency = "CAD" // config-driven; CAD-at-Run is UNVERIFIED (open Q) but we send CAD
	}

	body, _ := json.Marshal(runChargeRequest{
		AccountToken: paymentToken,
		MID:          merchantID,
		Amount:       amountCents,
		Currency:     currency,
		Capture:      "Y",
		Vault:        "N",
	})

	raw, status, err := c.postCharge(ctx, base, body, apiKey, merchantID)
	if err != nil {
		return ChargeResult{}, fmt.Errorf("run_payments: request failed: %w", err)
	}
	// Expiring JWT: a 401 means the api_key lapsed. Refresh once, rotate creds, retry the charge.
	if status == http.StatusUnauthorized {
		newKey, newRefresh, rerr := c.refreshAPIKey(ctx, base, apiKey, creds["refresh_token"])
		if rerr != nil {
			return ChargeResult{}, fmt.Errorf("run_payments: api_key refresh failed: %w", rerr)
		}
		creds["api_key"] = newKey // rotate in-memory; Service re-encrypts the active payment_config row
		if newRefresh != "" {
			creds["refresh_token"] = newRefresh
		}
		raw, status, err = c.postCharge(ctx, base, body, newKey, merchantID)
		if err != nil {
			return ChargeResult{}, fmt.Errorf("run_payments: request failed after refresh: %w", err)
		}
	}
	if status >= 400 {
		return ChargeResult{}, fmt.Errorf("run_payments: status %d", status)
	}

	var out runChargeResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return ChargeResult{}, fmt.Errorf("run_payments: bad response: %w", err)
	}
	switch out.Result {
	case "A": // Approved → captured
		return ChargeResult{ProcessorRef: out.TransID, Status: "captured"}, nil
	case "C": // Declined
		return ChargeResult{ProcessorRef: out.TransID, Status: "failed"}, fmt.Errorf("run_payments: declined (%s): %s", out.RespCode, out.RespText)
	default: // B (Retry) or unknown → no tender state in our model; map explicitly to failed
		return ChargeResult{ProcessorRef: out.TransID, Status: "failed"}, fmt.Errorf("run_payments: result %q (%s): %s", out.Result, out.RespCode, out.RespText)
	}
}

// postCharge sends one POST /api/v1/charge and returns the body + HTTP status. The Bearer api_key
// is set on the wire but NEVER logged (no logging in this path).
func (c *RunPaymentsClient) postCharge(ctx context.Context, base string, body []byte, apiKey, merchantID string) (raw []byte, status int, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/charge", bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey) // never logged
	req.Header.Set("mid", merchantID)

	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, _ = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return raw, resp.StatusCode, nil
}

// refreshAPIKey exchanges the current (expired) api_key for a fresh rotating credential set via
// POST {base}/api/v1/api_keys/refresh. Returns the new api_key (and refresh_token, if present).
func (c *RunPaymentsClient) refreshAPIKey(ctx context.Context, base, currentKey, _ string) (apiKey, refreshToken string, err error) {
	body, _ := json.Marshal(runRefreshRequest{Token: currentKey})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/api_keys/refresh", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpc.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return "", "", fmt.Errorf("status %d", resp.StatusCode)
	}
	var out runRefreshResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", "", fmt.Errorf("bad refresh response: %w", err)
	}
	if out.APIKey == "" {
		return "", "", errors.New("refresh returned no api_key")
	}
	return out.APIKey, out.RefreshToken, nil
}

// runRefundRequest is the VERIFIED /api/v1/void-or-refund body: the original capture's trans_id, the mid,
// the amount to reverse (integer cents), and the action ('refund' = money back to the card; a full-amount
// same-day reversal would be 'void', we always send 'refund' for the NSF/A-05 path).
type runRefundRequest struct {
	TransID string `json:"trans_id"`
	MID     string `json:"mid"`
	Amount  int64  `json:"amount"`
	Action  string `json:"action"`
}

// runRefundResponse is the VERIFIED /api/v1/void-or-refund response (same A/B/C result enum as charge).
type runRefundResponse struct {
	Result   string `json:"result"` // A=Approved, B=Retry, C=Declined
	RespCode string `json:"resp_code"`
	RespText string `json:"resp_text"`
	TransID  string `json:"trans_id"`
}

// Refund reverses (full or partial) a prior capture identified by processorRef via POST
// {base}/api/v1/void-or-refund (the VERIFIED endpoint). The base_url is resolved from the dealer creds
// exactly like Charge, so the gate stays OFF for live: with no configured base_url the client returns
// ErrNotImplemented (no host to call, CI drives it through httptest only, never a live network call).
func (c *RunPaymentsClient) Refund(ctx context.Context, creds map[string]string, merchantID, processorRef string, amountCents int64) (RefundResult, error) {
	apiKey := creds["api_key"]
	if apiKey == "" {
		return RefundResult{}, fmt.Errorf("run_payments: missing api_key in payment config")
	}
	if merchantID == "" {
		return RefundResult{}, fmt.Errorf("run_payments: missing mid")
	}
	if processorRef == "" {
		return RefundResult{}, fmt.Errorf("run_payments: missing processor ref (the original capture trans_id)")
	}
	if amountCents <= 0 {
		return RefundResult{}, fmt.Errorf("run_payments: refund amount must be positive")
	}
	base := creds["base_url"]
	if base == "" {
		base = c.defaultBaseURL
	}
	// Env-gated OFF for live: no configured host → no call. CI supplies a base_url via httptest.
	if base == "" {
		return RefundResult{}, ErrNotImplemented
	}

	body, _ := json.Marshal(runRefundRequest{TransID: processorRef, MID: merchantID, Amount: amountCents, Action: "refund"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/void-or-refund", bytes.NewReader(body))
	if err != nil {
		return RefundResult{}, fmt.Errorf("run_payments: refund request build: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey) // never logged
	req.Header.Set("mid", merchantID)

	resp, err := c.httpc.Do(req)
	if err != nil {
		return RefundResult{}, fmt.Errorf("run_payments: refund request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return RefundResult{}, fmt.Errorf("run_payments: refund status %d", resp.StatusCode)
	}
	var out runRefundResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return RefundResult{}, fmt.Errorf("run_payments: bad refund response: %w", err)
	}
	switch out.Result {
	case "A": // Approved → refunded
		return RefundResult{ProcessorRef: out.TransID, Status: "refunded"}, nil
	case "C": // Declined
		return RefundResult{ProcessorRef: out.TransID, Status: "failed"}, fmt.Errorf("run_payments: refund declined (%s): %s", out.RespCode, out.RespText)
	default: // B (Retry) or unknown
		return RefundResult{ProcessorRef: out.TransID, Status: "failed"}, fmt.Errorf("run_payments: refund result %q (%s): %s", out.Result, out.RespCode, out.RespText)
	}
}

// Void is not built yet.
func (c *RunPaymentsClient) Void(context.Context, map[string]string, string, string, int64) (RefundResult, error) {
	return RefundResult{}, ErrNotImplemented
}

// runOutcome is what one Run answer means for the money.
type runOutcome int

const (
	outcomeApproved runOutcome = iota
	outcomeDeclined
	outcomeUnknown
	outcomeReauth
)

// runAnswer is the decoded body every Run call answers with (the same A/B/C enum on every endpoint).
type runAnswer struct {
	Result   string `json:"result"`
	RespCode string `json:"resp_code"`
	RespText string `json:"resp_text"`
	TransID  string `json:"trans_id"`
}

// classifyRun is not built yet.
func classifyRun(status int, transportErr error, raw []byte, afterRefresh bool) (runOutcome, runAnswer, error) {
	return outcomeDeclined, runAnswer{}, nil
}
