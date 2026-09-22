//go:build integration

// runpay_selector_integration_test.go, DIB T3: the D-RUNPAY-2 flag flip as executable evidence,
// on the POS card rail.
//
// internal/payment already proves the two processors in isolation: store_test.go drives the stub over
// a fake config store, run_payments_test.go drives the live client over httptest. Neither proves the
// thing D-RUNPAY-2 actually asserts, that the SELECTION is a constructor argument, that the whole
// path downstream of it follows, and that the live branch can be reached with no Run account.
//
// Both tests run against the REAL Store and a REAL payment_config row (credentials sealed by the real
// AES-256-GCM vault), so the config load, decrypt and dispatch are the production ones. No live call
// is made under any tag: the stub is in-process, and the "live" leg points RunPaymentsClient at an
// httptest server via the per-dealer creds["base_url"] override with throwaway test credentials.
//
// NOTE (escalated, see REPORT-W2-T.md): cmd/server/main.go:266 hardcodes payment.RunPaymentsStub{}.
// There is no env/config flag to flip, so these tests prove the seam is flip-READY and pin the exact
// one-line change; they cannot prove a switch that does not exist yet.
package payment

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// configureRail seals a real payment_config row for the given creds and returns the live Store. It
// takes the DIB-396 payment_config lease (the table is a singleton), exactly as cleanConfig does.
func configureRail(t *testing.T, creds map[string]string) *Store {
	t.Helper()
	pool := intPool(t)
	cleanConfig(t, pool) // also takes the singleton lease for the rest of the test
	st := intStore(t, pool)
	if err := st.Configure(context.Background(), "run_payments", "MID-SELECTOR", "CAD", "", 0, false, creds, ""); err != nil {
		t.Fatalf("configure rail: %v", err)
	}
	return st
}

// TestAcceptance_CardRail_StubSelected_CapturesWithoutLeavingTheProcess is the STUB-mode acceptance:
// the default rail captures, reports itself as simulated, and reaches no network.
func TestAcceptance_CardRail_StubSelected_CapturesWithoutLeavingTheProcess(t *testing.T) {
	ctx := context.Background()
	// A base_url that would fail loudly if anything tried to dial it, the stub must never look.
	st := configureRail(t, map[string]string{"api_key": "stub_key_not_a_secret", "base_url": "http://127.0.0.1:1/must-not-be-dialled"})

	svc := NewService(st, RunPaymentsStub{})
	if !svc.Simulated() {
		t.Fatal("the default rail must report Simulated(), the POS labels the card reader off this flag")
	}

	rcpt, err := svc.Charge(ctx, ChargeInput{
		AmountCents: 55000, PaymentToken: "tok_stub_acceptance", IdempotencyKey: "idem-stub-1", CardType: "CREDIT",
	})
	if err != nil {
		t.Fatalf("stub capture: %v", err)
	}
	if rcpt.Status != "captured" {
		t.Fatalf("stub capture status = %q, want \"captured\"", rcpt.Status)
	}
	if rcpt.ProcessorRef == "" {
		t.Fatal("a capture must carry a reconcilable processor ref")
	}
	if rcpt.Processor != "run_payments" {
		t.Fatalf("receipt processor = %q, want the configured \"run_payments\"", rcpt.Processor)
	}

	// D-RUNPAY-1 still holds on this path: the gateway call writes nothing to payments.
	var leaked int
	if err := intPool(t).QueryRow(ctx, `SELECT count(*) FROM payments WHERE gateway_ref = $1`, rcpt.ProcessorRef).Scan(&leaked); err != nil {
		t.Fatalf("count payments: %v", err)
	}
	if leaked != 0 {
		t.Fatalf("the stub capture wrote %d payments row(s), the till owns that write", leaked)
	}
}

// TestAcceptance_CardRail_FlagFlipSelectsLiveClient_NoRunCredentials is the flip. Constructing the
// Service with the live RunPaymentsClient instead of the stub must:
//   - flip Simulated() to false (so the POS stops labelling the rail as test mode),
//   - route the capture through the LIVE client's verified HTTP contract,
//   - carry the per-dealer config (mid, currency) from the same real payment_config row,
//
// all without a Run account: the dealer creds are throwaway strings and base_url points at httptest.
func TestAcceptance_CardRail_FlagFlipSelectsLiveClient_NoRunCredentials(t *testing.T) {
	ctx := context.Background()

	var seen struct {
		hits     int
		path     string
		auth     string
		mid      string
		amount   int64
		currency string
		capture  string
	}
	run := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.hits++
		seen.path = r.URL.Path
		seen.auth = r.Header.Get("Authorization")
		var body struct {
			AccountToken string `json:"account_token"`
			MID          string `json:"mid"`
			Amount       int64  `json:"amount"`
			Currency     string `json:"currency"`
			Capture      string `json:"capture"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		seen.mid, seen.amount, seen.currency, seen.capture = body.MID, body.Amount, body.Currency, body.Capture
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"result": "A", "resp_code": "00", "resp_text": "APPROVED",
			"trans_id": "run_live_ref_0001", "card_type": "CREDIT",
		})
	}))
	defer run.Close()

	// Throwaway per-dealer credentials, sealed by the real vault into the real config row. Nothing
	// here is a live secret and nothing reaches the internet, base_url is the httptest listener.
	st := configureRail(t, map[string]string{
		"api_key":  "test-only-not-a-secret",
		"base_url": run.URL,
	})

	// -- THE FLIP -----------------------------------------------------------------------------
	// One argument. Everything below follows from it.
	svc := NewService(st, NewRunPaymentsClient(run.URL))

	if svc.Simulated() {
		t.Fatal("after the flip the rail must NOT report Simulated(), the POS would keep labelling live charges as test mode")
	}

	rcpt, err := svc.Charge(ctx, ChargeInput{
		AmountCents: 55000, PaymentToken: "tok_live_selector", IdempotencyKey: "idem-live-1", CardType: "CREDIT",
	})
	if err != nil {
		t.Fatalf("capture through the selected live client: %v", err)
	}

	if seen.hits != 1 {
		t.Fatalf("the live client made %d request(s), want exactly 1, the flip did not reach the dispatch", seen.hits)
	}
	if seen.path != "/api/v1/charge" {
		t.Fatalf("live path = %q, want the verified /api/v1/charge", seen.path)
	}
	if !strings.HasPrefix(seen.auth, "Bearer ") {
		t.Fatalf("live call must present a Bearer api_key, got %q", seen.auth)
	}
	// The per-dealer facts came out of the SAME real payment_config row the stub leg read, decrypted
	// by the real vault, the flip changes the processor, never the configuration source.
	if seen.mid != "MID-SELECTOR" {
		t.Fatalf("live charge mid = %q, want the configured MID-SELECTOR", seen.mid)
	}
	if seen.currency != "CAD" {
		t.Fatalf("live charge currency = %q, want CAD (never a defaulted USD)", seen.currency)
	}
	if seen.amount != 55000 {
		t.Fatalf("live charge amount = %d, want 55000", seen.amount)
	}
	if seen.capture != "Y" {
		t.Fatalf("counter sales are auth+capture: capture = %q, want \"Y\"", seen.capture)
	}

	// The receipt is the LIVE processor's ref, not a stub-shaped one.
	if rcpt.ProcessorRef != "run_live_ref_0001" {
		t.Fatalf("processor ref = %q, want the live trans_id run_live_ref_0001", rcpt.ProcessorRef)
	}
	if rcpt.Status != "captured" {
		t.Fatalf("live capture status = %q, want \"captured\"", rcpt.Status)
	}

	// D-RUNPAY-1 survives the flip: even the live rail writes nothing to payments.
	var leaked int
	if err := intPool(t).QueryRow(ctx, `SELECT count(*) FROM payments WHERE gateway_ref = $1`, rcpt.ProcessorRef).Scan(&leaked); err != nil {
		t.Fatalf("count payments: %v", err)
	}
	if leaked != 0 {
		t.Fatalf("the live rail wrote %d payments row(s), the till owns that write on BOTH sides of the flip", leaked)
	}

	// And the stub is still reachable from the same store: the flip is a construction-time choice,
	// not a global mutation, so a staged rollout (one branch live, the rest simulated) is possible.
	stubSvc := NewService(st, RunPaymentsStub{})
	if !stubSvc.Simulated() {
		t.Fatal("constructing a live rail must not retro-flip a stub rail built from the same store")
	}
	if seen.hits != 1 {
		t.Fatalf("constructing a second service must not issue a request; hits = %d", seen.hits)
	}
}
