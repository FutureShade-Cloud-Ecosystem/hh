//go:build demo

package main

// demo_assist_serve_test.go, TanyAI slice 3's serve level evidence, through the REAL demoServe over
// the hand-built demo world of card_rail_g7_test.go (gated on the same database variable): a demo
// carrying the assistant's key and dealer cap serves only when the model has its price row, and
// refuses at boot, before the listener, in plain words when it has none. The key is a made up
// value and no provider is ever called.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const fakeAssistKey = "fake-key-must-not-print-0123456789"

// assistServeEnv is the serve configuration of card_rail_g7_test.go plus the assistant's two
// admitted variables, set in the process environment (the wiring reads the key there) and in the
// getenv the boot guards read.
func assistServeEnv(t *testing.T, caseURL string, port int, capValue string) func(string) string {
	t.Helper()
	t.Setenv("APP_ENV", "demo")
	t.Setenv("DEMO_MODE", "true")
	t.Setenv("PAYMENT_VAULT_KEY", hexKey(t))
	unsetForTest(t, "HOS_CARD_PROCESSOR")
	unsetForTest(t, "HOS_CARD_BASE_URL")
	t.Setenv("HOS_ASSIST_ANTHROPIC_API_KEY", fakeAssistKey)
	t.Setenv("HOS_ASSIST_DEALER_CAP_MICROCENTS", capValue)
	signKey := hexKey(t)
	env := map[string]string{
		"CORE_DATABASE_URL":                caseURL,
		"CORE_STAFF_AUDIENCE":              "hh-staff",
		"CORE_SIGNIN_MODE":                 "demo",
		"CORE_TENANT_AUDIENCE":             "hh-tenant",
		"CORE_STREAM_TICKET_KEY":           "fx-ticket-key",
		"CORE_LISTEN_ADDR":                 fmt.Sprintf("127.0.0.1:%d", port),
		"HH_ACCEPTANCE_SIGN_KEY_ID":        "fx-accept-sign-key",
		"HH_ACCEPTANCE_SIGN_KEY":           signKey,
		"HOS_ASSIST_ANTHROPIC_API_KEY":     fakeAssistKey,
		"HOS_ASSIST_DEALER_CAP_MICROCENTS": capValue,
	}
	return func(k string) string { return env[k] }
}

func TestDemoServeRefusesAKeyedAssistantWithNoPriceRow(t *testing.T) {
	caseURL := demoServeCase(t)
	getenv := assistServeEnv(t, caseURL, freeServePort(t), "5000000")
	err := demoServe(getenv, demoSaysDemoTwice(), os.Environ)
	if err == nil {
		t.Fatal("a keyed demo with no price row served")
	}
	for _, want := range []string{"assist_model_prices", "tanyai-premium", "claude-sonnet-5-5", "demo-copies.md"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not say %q", err, want)
		}
	}
	if strings.Contains(err.Error(), fakeAssistKey) {
		t.Fatal("the refusal carries the key's value")
	}
}

func TestDemoServeServesAKeyedAssistantWithItsPriceRow(t *testing.T) {
	caseURL := demoServeCase(t)
	pool, err := pgxpool.New(context.Background(), caseURL)
	if err != nil {
		t.Fatalf("case pool: %v", err)
	}
	t.Cleanup(pool.Close)
	// A test row with made up figures: the real row is an operator step (the demo runbook).
	if _, err := pool.Exec(context.Background(), `INSERT INTO assist_model_prices
		(alias, model, price_in_micro_per_m, price_out_micro_per_m, price_cached_micro_per_m, effective_seq)
		VALUES ('tanyai-premium', 'claude-sonnet-5-5', 1, 1, 1, 1)`); err != nil {
		t.Fatalf("plant the price row: %v", err)
	}
	port := freeServePort(t)
	getenv := assistServeEnv(t, caseURL, port, "5000000")

	errCh := make(chan error, 1)
	go func() { errCh <- demoServe(getenv, demoSaysDemoTwice(), os.Environ) }()
	if !waitUntilServes(t, port) {
		select {
		case err := <-errCh:
			t.Fatalf("demoServe refused or failed before serving: %v", err)
		default:
			t.Fatalf("the keyed, capped, priced demo never opened a listener on %d", port)
		}
	}
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("signal the serve role: %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("demoServe reported an error on shutdown: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("demoServe did not stop on its shutdown signal")
	}
}
