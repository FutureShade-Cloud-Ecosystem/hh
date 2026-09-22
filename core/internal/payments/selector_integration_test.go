//go:build integration

// selector_integration_test.go, DIB-416: the flag flip driven through the REAL selector, end to end.
//
// Wave 2 shipped TestAcceptance_GatewaySelector_FlagFlipSelectsLiveWithoutCredentials, which proved
// the SEAM was flip-ready by constructing the live-shaped gateway by hand. It carried an explicit
// note that it could not prove a switch that did not exist, because main.go hardcoded
// payments.NewStubGateway(). This is that test's missing half: the same flip, made by
// SelectGateway(LoadGatewayConfig()) reading the actual documented environment variables, settling an
// actual invoice through the actual live client.
//
// No Run account, no network egress: HOS_PAYMENT_BASE_URL points at an httptest listener and every
// credential is a throwaway string. Nothing in this file is a secret and nothing leaves the process.
package payments

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAcceptance_GatewaySelector_RealFlagSelectsLiveAndSettles is the flip against the real flag.
func TestAcceptance_GatewaySelector_RealFlagSelectsLiveAndSettles(t *testing.T) {
	ctx := context.Background()

	var seen struct {
		hits   int
		path   string
		auth   string
		idem   string
		mid    string
		amount int64
		body   runChargeBody
	}
	run := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.hits++
		seen.path = r.URL.Path
		seen.auth = r.Header.Get("Authorization")
		seen.idem = r.Header.Get("X-Idempotency-Key")
		_ = json.NewDecoder(r.Body).Decode(&seen.body)
		seen.mid, seen.amount = seen.body.MID, seen.body.Amount
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"result": "A", "resp_code": "00", "resp_text": "APPROVED", "trans_id": "run_portal_ref_0001",
		})
	}))
	defer run.Close()

	// - THE FLAG -
	// Set exactly as an operator would, then read back through the production loader. t.Setenv
	// restores the environment automatically, which keeps this test order-independent under
	// -shuffle=on (the suite gate).
	t.Setenv(EnvGatewayMode, "run")
	t.Setenv(EnvGatewayAPIKey, "test-only-not-a-secret")
	t.Setenv(EnvGatewayMID, "MID-PORTAL-SELECTOR")
	t.Setenv(EnvGatewayBaseURL, run.URL)
	t.Setenv(EnvGatewaySecret, "test-only-not-a-secret")
	t.Setenv(EnvAppEnv, "staging")

	gw, err := SelectGateway(LoadGatewayConfig())
	if err != nil {
		t.Fatalf("the documented flag must select the live rail: %v", err)
	}
	if !gw.Live() || gw.Name() != "run" {
		t.Fatalf("selected %q live=%v, want the live \"run\" rail", gw.Name(), gw.Live())
	}

	// The rig vaults its method through the gateway it is handed, and the live rail deliberately
	// refuses to vault against an unverified contract, so vault on the stub, then charge on the live
	// rail. That split is exactly the migration path a dealer flipping the flag actually takes: their
	// existing stored methods keep working.
	rig := newPayRig(t, NewStubGateway())
	cleanupIntents(t, rig)
	liveSvc := NewService(NewRepository(rig.pool), gw)

	pay, err := liveSvc.Pay(ctx, rig.customerID, PayRequest{
		InvoiceIDs: []string{rig.invoiceA}, PaymentMethodID: rig.methodID,
	})
	if err != nil {
		t.Fatalf("settle through the flag-selected live rail: %v", err)
	}

	if seen.hits != 1 {
		t.Fatalf("the live client made %d request(s), want exactly 1, the flag did not reach the dispatch", seen.hits)
	}
	if seen.path != "/api/v1/charge" {
		t.Fatalf("live path = %q, want the verified /api/v1/charge", seen.path)
	}
	if !strings.HasPrefix(seen.auth, "Bearer ") {
		t.Fatalf("live call must present a Bearer api_key, got %q", seen.auth)
	}
	if seen.mid != "MID-PORTAL-SELECTOR" {
		t.Fatalf("live mid = %q, want the configured MID-PORTAL-SELECTOR", seen.mid)
	}
	if seen.amount != 40000 {
		t.Fatalf("live amount = %d, want the SERVER-derived 40000 (never a client-supplied figure)", seen.amount)
	}
	if seen.body.Capture != "Y" {
		t.Fatalf("a portal payment is auth+capture: capture = %q, want \"Y\"", seen.body.Capture)
	}
	if seen.idem == "" {
		t.Fatal("the server-bound idempotency key must ride on the live request, it is what an operator " +
			"matches a stuck payment_intents row against during reconciliation (DIB-415)")
	}

	// The persisted ref is the LIVE trans_id, and the settlement is the ordinary one.
	if pay.GatewayRef != "run_portal_ref_0001" {
		t.Fatalf("persisted gateway ref = %q, want the live trans_id", pay.GatewayRef)
	}
	var status string
	var balance int64
	if err := rig.pool.QueryRow(ctx, `SELECT status, balance_cents FROM invoices WHERE id = $1::uuid`, rig.invoiceA).
		Scan(&status, &balance); err != nil {
		t.Fatalf("read invoice: %v", err)
	}
	if status != "PAID" || balance != 0 {
		t.Fatalf("the live settle left invoice %s/%d, want PAID/0", status, balance)
	}

	// DIB-415 holds on the live rail too: the intent settled naming this payment.
	var istatus, ipayment string
	if err := rig.pool.QueryRow(ctx,
		`SELECT status, COALESCE(payment_id::text,'') FROM payment_intents WHERE customer_id = $1::uuid`,
		rig.customerID).Scan(&istatus, &ipayment); err != nil {
		t.Fatalf("read intent: %v", err)
	}
	if istatus != IntentSettled || ipayment != pay.ID {
		t.Fatalf("intent = %s/%s, want SETTLED naming %s", istatus, ipayment, pay.ID)
	}

	// And a replay through the LIVE rail returns the original rather than charging again, the whole
	// point of DIB-415 being that Run returns a fresh trans_id for a repeated request.
	replay, err := liveSvc.Pay(ctx, rig.customerID, PayRequest{
		InvoiceIDs: []string{rig.invoiceA}, PaymentMethodID: rig.methodID,
	})
	if err != nil || replay.ID != pay.ID {
		t.Fatalf("live replay = %s / %v, want the original payment %s", replay.ID, err, pay.ID)
	}
	if seen.hits != 1 {
		t.Fatalf("the live replay reached the processor (%d hits), that is a real double charge", seen.hits)
	}
}

// TestAcceptance_GatewaySelector_RealFlagDefaultsToStub is the same path with the flag ABSENT, which
// is how every box that is not a live dealer boots. It is here because "the default is safe" is a
// claim that has to be executable, not a comment.
func TestAcceptance_GatewaySelector_RealFlagDefaultsToStub(t *testing.T) {
	t.Setenv(EnvGatewayMode, "")
	t.Setenv(EnvAppEnv, "dev")

	gw, err := SelectGateway(LoadGatewayConfig())
	if err != nil {
		t.Fatalf("an unset flag must select the stub: %v", err)
	}
	if gw.Live() {
		t.Fatal("the unset flag produced a LIVE gateway")
	}

	rig := newPayRig(t, gw)
	cleanupIntents(t, rig)
	pay, err := rig.svc.Pay(context.Background(), rig.customerID, PayRequest{
		InvoiceIDs: []string{rig.invoiceB}, PaymentMethodID: rig.methodID,
	})
	if err != nil {
		t.Fatalf("stub settle through the selector: %v", err)
	}
	if pay.AmountCents != 15000 {
		t.Fatalf("settled %d, want 15000", pay.AmountCents)
	}
}
