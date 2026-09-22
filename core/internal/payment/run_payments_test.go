package payment

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// decodeChargeBody reads the JSON charge body the client POSTed, plus the raw bytes (for absence
// assertions like "no reference field").
func decodeChargeBody(t *testing.T, r *http.Request) (map[string]any, string) {
	t.Helper()
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("charge body not JSON: %v (%s)", err, raw)
	}
	return body, string(raw)
}

// T7, the live client speaks the VERIFIED contract: POST /api/v1/charge, Bearer api_key + mid,
// body {account_token, mid, amount, currency:CAD, capture:Y, vault:N} (no reference), and on
// {result:A, trans_id} returns captured with ProcessorRef=trans_id (US-RP03; D-RUNPAY-2).
func TestRunPaymentsClient_Charge_VerifiedContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/charge" {
			t.Errorf("want POST /api/v1/charge, got %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tkn_ppc_test" {
			t.Errorf("Authorization = %q, want Bearer tkn_ppc_test", got)
		}
		if got := r.Header.Get("mid"); got != "MID-DIBBITS-1" {
			t.Errorf("mid header = %q, want MID-DIBBITS-1", got)
		}
		body, raw := decodeChargeBody(t, r)
		if body["account_token"] != "tok_runtest_4242" {
			t.Errorf("account_token = %v", body["account_token"])
		}
		if body["mid"] != "MID-DIBBITS-1" {
			t.Errorf("body mid = %v", body["mid"])
		}
		if body["amount"].(float64) != 12500 {
			t.Errorf("amount = %v, want 12500", body["amount"])
		}
		if body["currency"] != "CAD" {
			t.Errorf("currency = %v, want CAD", body["currency"])
		}
		if body["capture"] != "Y" {
			t.Errorf("capture = %v, want Y", body["capture"])
		}
		if body["vault"] != "N" {
			t.Errorf("vault = %v, want N", body["vault"])
		}
		if strings.Contains(raw, "\"reference\"") {
			t.Errorf("body must NOT carry a reference field: %s", raw)
		}
		_, _ = io.WriteString(w, `{"result":"A","trans_id":"txn_abc123","resp_text":"approved","trans_date":"2026-06-29"}`)
	}))
	defer srv.Close()

	c := NewRunPaymentsClient(srv.URL)
	res, err := c.Charge(context.Background(),
		map[string]string{"api_key": "tkn_ppc_test", "base_url": srv.URL, "currency": "CAD"},
		"MID-DIBBITS-1", 12500, "tok_runtest_4242")
	if err != nil {
		t.Fatalf("Charge: %v", err)
	}
	if res.Status != "captured" || res.ProcessorRef != "txn_abc123" {
		t.Fatalf("got %+v, want captured/txn_abc123", res)
	}
}

// T8, the request body currency is the configured "CAD", not the blueprint's USD default. (Run-side
// CAD acceptance is UNVERIFIED, this asserts what WE send, not that Run settles it; US-RP03.)
func TestRunPaymentsClient_CurrencyIsConfiguredCAD_NotUSD(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := decodeChargeBody(t, r)
		if body["currency"] != "CAD" {
			t.Errorf("currency = %v, want CAD", body["currency"])
		}
		if body["currency"] == "USD" {
			t.Error("currency must never default to USD")
		}
		_, _ = io.WriteString(w, `{"result":"A","trans_id":"t1"}`)
	}))
	defer srv.Close()

	c := NewRunPaymentsClient(srv.URL)
	if _, err := c.Charge(context.Background(),
		map[string]string{"api_key": "k", "currency": "CAD"}, "MID-1", 100, "tok"); err != nil {
		t.Fatalf("Charge: %v", err)
	}
}

// T9, the live client sends NO Run idempotency header (dedup is OURS server-side). The call-scoped
// __idem key threads Service.Charge for the stub's determinism but must NOT leak to the wire (US-RP06).
func TestRunPaymentsClient_NoRunIdempotencyHeader_DedupServerSide(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, h := range []string{"X-Idempotency-Key", "Idempotency-Key"} {
			if r.Header.Get(h) != "" {
				t.Errorf("client must send no %s header", h)
			}
		}
		_, raw := decodeChargeBody(t, r)
		if strings.Contains(raw, "__idem") {
			t.Errorf("the call-scoped __idem key must not appear in the body: %s", raw)
		}
		_, _ = io.WriteString(w, `{"result":"A","trans_id":"t1"}`)
	}))
	defer srv.Close()

	c := NewRunPaymentsClient(srv.URL)
	if _, err := c.Charge(context.Background(),
		map[string]string{"api_key": "k", "__idem": "client-uuid"}, "MID-1", 100, "tok"); err != nil {
		t.Fatalf("Charge: %v", err)
	}
}

// T10, a declined result (C) is surfaced as an error + Status="failed" carrying resp_text, never
// swallowed and never captured (US-RP03).
func TestRunPaymentsClient_Result_C_Declined(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"result":"C","resp_code":"05","resp_text":"insufficient funds","trans_id":"t_decl"}`)
	}))
	defer srv.Close()

	c := NewRunPaymentsClient(srv.URL)
	res, err := c.Charge(context.Background(), map[string]string{"api_key": "k"}, "MID-1", 100, "tok")
	if err == nil {
		t.Fatal("a declined (C) charge must return an error")
	}
	if res.Status != "failed" {
		t.Fatalf("status = %q, want failed", res.Status)
	}
	if !strings.Contains(err.Error(), "insufficient funds") {
		t.Fatalf("error must surface resp_text, got %v", err)
	}
}

// T29, a retry result (B) maps EXPLICITLY to failed, surfacing resp_text, NEVER captured (no retry
// tender state at W4; US-RP03; ARCH_DELTA §2.4).
func TestRunPaymentsClient_Result_B_RetryMapsToFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"result":"B","resp_text":"please retry","trans_id":"t_retry"}`)
	}))
	defer srv.Close()

	c := NewRunPaymentsClient(srv.URL)
	res, err := c.Charge(context.Background(), map[string]string{"api_key": "k"}, "MID-1", 100, "tok")
	if err == nil {
		t.Fatal("a retry (B) result must return an error")
	}
	if res.Status == "captured" {
		t.Fatal("a retry (B) result must NEVER be captured")
	}
	if res.Status != "failed" {
		t.Fatalf("status = %q, want failed", res.Status)
	}
	if !strings.Contains(err.Error(), "please retry") {
		t.Fatalf("error must surface the retry hint, got %v", err)
	}
}

// T11, a missing api_key (or mid) fails closed: an error BEFORE any network call (US-RP03).
func TestRunPaymentsClient_MissingApiKeyOrMid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("no network call must be made when api_key/mid is missing")
	}))
	defer srv.Close()

	c := NewRunPaymentsClient(srv.URL)
	if _, err := c.Charge(context.Background(),
		map[string]string{"base_url": srv.URL}, "MID-1", 100, "tok"); err == nil {
		t.Fatal("missing api_key must fail closed")
	}
	if _, err := c.Charge(context.Background(),
		map[string]string{"api_key": "k", "base_url": srv.URL}, "", 100, "tok"); err == nil {
		t.Fatal("missing mid must fail closed")
	}
}

// T30, on an expired-api_key (401) response the client refreshes via POST /api/v1/api_keys/refresh,
// re-charges with the new api_key, and rotates the in-memory creds (so Service re-encrypts the active
// config). No static secret is assumed (US-RP03; ARCH_DELTA §2.11).
func TestRunPaymentsClient_RefreshesExpiredApiKey(t *testing.T) {
	var refreshHit bool
	var chargeAttempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/charge":
			chargeAttempts++
			if r.Header.Get("Authorization") == "Bearer expired_key" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, `{"error":"api_key expired"}`)
				return
			}
			if r.Header.Get("Authorization") != "Bearer fresh_key" {
				t.Errorf("re-charge must use the refreshed key, got %q", r.Header.Get("Authorization"))
			}
			_, _ = io.WriteString(w, `{"result":"A","trans_id":"t_after_refresh"}`)
		case "/api/v1/api_keys/refresh":
			refreshHit = true
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "expired_key") {
				t.Errorf("refresh body must carry the current (expired) token, got %s", body)
			}
			_, _ = io.WriteString(w, `{"api_key":"fresh_key","public_key":"pub_new","refresh_token":"rt_new"}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	creds := map[string]string{"api_key": "expired_key", "refresh_token": "rt_old", "base_url": srv.URL}
	c := NewRunPaymentsClient(srv.URL)
	res, err := c.Charge(context.Background(), creds, "MID-1", 100, "tok")
	if err != nil {
		t.Fatalf("Charge after refresh: %v", err)
	}
	if !refreshHit {
		t.Fatal("the client must call /api/v1/api_keys/refresh on an expired key")
	}
	if chargeAttempts != 2 {
		t.Fatalf("charge attempts = %d, want 2 (initial 401 + retry)", chargeAttempts)
	}
	if res.Status != "captured" || res.ProcessorRef != "t_after_refresh" {
		t.Fatalf("got %+v, want captured/t_after_refresh", res)
	}
	// The rotating creds were updated in-memory so Service can re-encrypt them.
	if creds["api_key"] != "fresh_key" {
		t.Fatalf("creds api_key = %q, want fresh_key (rotated)", creds["api_key"])
	}
	if creds["refresh_token"] != "rt_new" {
		t.Fatalf("creds refresh_token = %q, want rt_new (rotated)", creds["refresh_token"])
	}
}

// T27 (live half), W5/A-05: the live client speaks the VERIFIED refund contract: POST
// /api/v1/void-or-refund, Bearer api_key + mid, body {trans_id, mid, amount, action:refund}, and on
// {result:A, trans_id} returns Status "refunded" with ProcessorRef=trans_id. Driven via httptest only.
func TestRunPaymentsClient_Refund_VerifiedContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/void-or-refund" {
			t.Errorf("want POST /api/v1/void-or-refund, got %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tkn_ppc_test" {
			t.Errorf("Authorization = %q, want Bearer tkn_ppc_test", got)
		}
		if got := r.Header.Get("mid"); got != "MID-DIBBITS-1" {
			t.Errorf("mid header = %q, want MID-DIBBITS-1", got)
		}
		body, _ := decodeChargeBody(t, r)
		if body["trans_id"] != "txn_abc123" {
			t.Errorf("trans_id = %v, want txn_abc123", body["trans_id"])
		}
		if body["amount"].(float64) != 12500 {
			t.Errorf("amount = %v, want 12500", body["amount"])
		}
		if body["action"] != "refund" {
			t.Errorf("action = %v, want refund", body["action"])
		}
		_, _ = io.WriteString(w, `{"result":"A","trans_id":"rfnd_xyz789","resp_text":"approved"}`)
	}))
	defer srv.Close()

	c := NewRunPaymentsClient(srv.URL)
	res, err := c.Refund(context.Background(),
		map[string]string{"api_key": "tkn_ppc_test", "base_url": srv.URL}, "MID-DIBBITS-1", "txn_abc123", 12500)
	if err != nil {
		t.Fatalf("live Refund: %v", err)
	}
	if res.Status != "refunded" || res.ProcessorRef != "rfnd_xyz789" {
		t.Fatalf("live Refund = %+v, want {rfnd_xyz789, refunded}", res)
	}
}

// T27b (live half, gate default), with NO configured base_url the live client is OFF: it returns
// ErrNotImplemented and makes no network call (there is no host to reach).
func TestRunPaymentsClient_Refund_NoBaseURL_Off(t *testing.T) {
	c := NewRunPaymentsClient("") // no default host
	res, err := c.Refund(context.Background(), map[string]string{"api_key": "k"}, "MID-1", "txn_abc123", 100)
	if !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("live Refund with no base_url err = %v, want ErrNotImplemented", err)
	}
	if (res != RefundResult{}) {
		t.Fatalf("live Refund off must return a zero RefundResult, got %+v", res)
	}
}
