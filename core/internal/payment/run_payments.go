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

// ProcessorClientTimeout bounds every HTTP call the live processor client
// makes (the one bound a caller outside payment may reason about: nothing the
// client started can still be running past it). Exported because the pos
// card refund resolution fences its settle against this window plus a crash
// margin; defined here, next the client it bounds, so the two never drift.
const ProcessorClientTimeout = 30 * time.Second

// NewRunPaymentsClient builds a live client with a default base URL (overridable per dealer via creds).
func NewRunPaymentsClient(defaultBaseURL string) *RunPaymentsClient {
	return &RunPaymentsClient{defaultBaseURL: defaultBaseURL, httpc: &http.Client{Timeout: ProcessorClientTimeout}}
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
// Every answer goes through classifyRun, the one outcome table: an approval returns captured, a decline
// wraps ErrProcessorDeclined, and anything whose outcome cannot be told wraps ErrProcessorOutcomeUnknown.
func (c *RunPaymentsClient) Charge(ctx context.Context, creds map[string]string, merchantID string, amountCents int64, paymentToken string) (ChargeResult, error) {
	apiKey := creds["api_key"]
	if apiKey == "" {
		return ChargeResult{}, notDispatched("run_payments: missing api_key in payment config")
	}
	if merchantID == "" {
		return ChargeResult{}, notDispatched("run_payments: missing mid")
	}
	if paymentToken == "" {
		return ChargeResult{}, notDispatched("run_payments: missing payment token (card must be tokenized client-side)")
	}
	base := creds["base_url"]
	if base == "" {
		base = c.defaultBaseURL
	}
	if base == "" {
		return ChargeResult{}, notDispatched("run_payments: no base_url configured")
	}
	currency := creds["currency"]
	if currency == "" {
		// Unreachable through the store: activeConfig threads the row's currency in, the column is NOT
		// NULL and the migration's check holds three letters, and Service.charge has already refused a
		// row that differs from the dealer's currency. Kept as the Ontario default for a client driven
		// directly with bare creds (the unit legs).
		currency = "CAD"
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
	outcome, ans, cerr := classifyRun(status, err, raw, false)
	// Expiring JWT: a 401 means the api_key lapsed and the charge was not processed. Refresh once,
	// rotate creds, re-post the charge (the same dispatch, never a second one).
	if outcome == outcomeReauth {
		newKey, newRefresh, rerr := c.refreshAPIKey(ctx, base, apiKey, creds["refresh_token"])
		if rerr != nil {
			return ChargeResult{}, notDispatched("run_payments: api_key refresh failed: %v", rerr)
		}
		creds["api_key"] = newKey // rotate in-memory; Service re-encrypts the active payment_config row
		if newRefresh != "" {
			creds["refresh_token"] = newRefresh
		}
		raw, status, err = c.postCharge(ctx, base, body, newKey, merchantID)
		outcome, ans, cerr = classifyRun(status, err, raw, true)
	}
	if outcome == outcomeApproved { // A: Approved -> captured
		return ChargeResult{ProcessorRef: ans.TransID, Status: "captured"}, nil
	}
	return ChargeResult{ProcessorRef: ans.TransID, Status: "failed"}, fmt.Errorf("run_payments: %w", cerr)
}

// runOutcome is what one Run answer means for the money.
type runOutcome int

const (
	outcomeApproved runOutcome = iota // A: the money moved
	outcomeDeclined                   // C: no money moved
	outcomeUnknown                    // the money may or may not have moved
	outcomeReauth                     // 401: the key lapsed, nothing was processed; refresh and re-post once
)

// runAnswer is the decoded body every Run call answers with (the same A/B/C enum on every endpoint).
type runAnswer struct {
	Result   string `json:"result"`
	RespCode string `json:"resp_code"`
	RespText string `json:"resp_text"`
	TransID  string `json:"trans_id"`
}

// classifyRun is THE outcome table, one function for every Run call (charge, void, refund); Run's answers
// change its rows and nothing else. status is the HTTP status, transportErr a failure to send or to read
// the answer (the context deadline included), raw the body and refreshSpent whether the 401 refresh and
// re-post is already used (or does not exist for the call).
//
//	transport error, deadline, unreadable answer  unknown
//	401, refresh still available                  reauth (the caller refreshes and re-posts once)
//	401, refresh spent                            unknown
//	5xx, any non-2xx other than 401               unknown (the body is never read)
//	2xx, body not JSON                            unknown
//	2xx, result A                                 approved
//	2xx, result C                                 declined (a *DeclineError)
//	2xx, result B or anything else                unknown (B stays unknown until Run says what it means)
//
// The error is nil for approved and reauth; otherwise it wraps ErrProcessorDeclined or
// ErrProcessorOutcomeUnknown.
func classifyRun(status int, transportErr error, raw []byte, refreshSpent bool) (runOutcome, runAnswer, error) {
	if transportErr != nil {
		return outcomeUnknown, runAnswer{}, unknownOutcome("request failed: %w", transportErr)
	}
	if status == http.StatusUnauthorized {
		if refreshSpent {
			return outcomeUnknown, runAnswer{}, unknownOutcome("status 401 after the key refresh")
		}
		return outcomeReauth, runAnswer{}, nil
	}
	if status < 200 || status > 299 {
		return outcomeUnknown, runAnswer{}, unknownOutcome("status %d", status)
	}
	var ans runAnswer
	if err := json.Unmarshal(raw, &ans); err != nil {
		return outcomeUnknown, runAnswer{}, unknownOutcome("bad response: %w", err)
	}
	switch ans.Result {
	case "A":
		return outcomeApproved, ans, nil
	case "C":
		return outcomeDeclined, ans, &DeclineError{RespCode: ans.RespCode, RespText: ans.RespText}
	default: // B (Retry) or unrecognised
		return outcomeUnknown, ans, unknownOutcome("result %q (%s): %s", ans.Result, ans.RespCode, ans.RespText)
	}
}

// unknownOutcome builds an error that wraps ErrProcessorOutcomeUnknown.
func unknownOutcome(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrProcessorOutcomeUnknown}, args...)...)
}

// notDispatched builds an error that wraps ErrProcessorNotDispatched: nothing reached the processor.
func notDispatched(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrProcessorNotDispatched}, args...)...)
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
	raw, err = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, 0, err // an answer cut short is no answer
	}
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
// the amount to reverse (integer cents), and the action: 'refund' = money back to the card (the NSF/A-05
// path and the compensation's fallback), 'void' = a full-amount same-day reversal (the compensation's first
// move). Refund never sends 'void' and Void never sends 'refund'.
type runRefundRequest struct {
	TransID string `json:"trans_id"`
	MID     string `json:"mid"`
	Amount  int64  `json:"amount"`
	Action  string `json:"action"`
}

// Refund reverses (full or partial) a prior capture identified by processorRef via POST
// {base}/api/v1/void-or-refund (the VERIFIED endpoint). The base_url is resolved from the dealer creds
// exactly like Charge, so the gate stays OFF for live: with no configured base_url the client returns
// ErrNotImplemented (no host to call, CI drives it through httptest only, never a live network call).
func (c *RunPaymentsClient) Refund(ctx context.Context, creds map[string]string, merchantID, processorRef string, amountCents int64) (RefundResult, error) {
	return c.reverse(ctx, creds, merchantID, processorRef, amountCents, "refund", "refunded")
}

// Void reverses a prior full-amount capture with action "void" on the same endpoint and the same
// request, result handling and outcome classifier as Refund. It never sends "refund". Like Refund it
// sends no idempotency header and no key in the body (the per dispatch key in creds["__idem"] is ours).
func (c *RunPaymentsClient) Void(ctx context.Context, creds map[string]string, merchantID, processorRef string, amountCents int64) (RefundResult, error) {
	return c.reverse(ctx, creds, merchantID, processorRef, amountCents, "void", "voided")
}

// reverse is the shared body of Refund and Void. A 401 is not refreshed here (the reversal follows its
// capture by moments), so classifyRun reads it as unknown.
func (c *RunPaymentsClient) reverse(ctx context.Context, creds map[string]string, merchantID, processorRef string, amountCents int64, action, doneStatus string) (RefundResult, error) {
	apiKey := creds["api_key"]
	if apiKey == "" {
		return RefundResult{}, notDispatched("run_payments: missing api_key in payment config")
	}
	if merchantID == "" {
		return RefundResult{}, notDispatched("run_payments: missing mid")
	}
	if processorRef == "" {
		return RefundResult{}, notDispatched("run_payments: missing processor ref (the original capture trans_id)")
	}
	if amountCents <= 0 {
		return RefundResult{}, notDispatched("run_payments: %s amount must be positive", action)
	}
	base := creds["base_url"]
	if base == "" {
		base = c.defaultBaseURL
	}
	// Env-gated OFF for live: no configured host → no call. CI supplies a base_url via httptest.
	if base == "" {
		return RefundResult{}, fmt.Errorf("%w: %w", ErrProcessorNotDispatched, ErrNotImplemented)
	}

	body, _ := json.Marshal(runRefundRequest{TransID: processorRef, MID: merchantID, Amount: amountCents, Action: action})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/void-or-refund", bytes.NewReader(body))
	if err != nil {
		return RefundResult{}, notDispatched("run_payments: %s request build: %v", action, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey) // never logged
	req.Header.Set("mid", merchantID)

	var raw []byte
	status := 0
	resp, err := c.httpc.Do(req)
	if err == nil {
		defer resp.Body.Close()
		status = resp.StatusCode
		raw, err = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	}
	outcome, ans, cerr := classifyRun(status, err, raw, true)
	if outcome == outcomeApproved { // A: Approved
		return RefundResult{ProcessorRef: ans.TransID, Status: doneStatus}, nil
	}
	return RefundResult{ProcessorRef: ans.TransID, Status: "failed"}, fmt.Errorf("run_payments: %s: %w", action, cerr)
}
