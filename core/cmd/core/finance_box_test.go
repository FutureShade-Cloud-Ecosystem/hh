package main

// The finance box boot guard, wiring side: serve and worker both build their
// graph through registerRoutes, so the guard runs there and a refusal stops
// the boot before any listener. The environment legs refuse before any
// database connection is attempted, which lets these legs run without a
// database at all.

import (
	"strings"
	"testing"
)

// financeBoxGetenv is the box statement a finance box carries: HH_BOX_KIND
// names finance, every other name the guard reads is absent (the rails unset
// read as stub).
func financeBoxGetenv(values map[string]string) func(string) string {
	return func(k string) string { return values[k] }
}

func TestRegisterRoutes_FinanceBoxRefusesNonStagingEnvBeforeAnyConnection(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("PAYMENT_VAULT_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	t.Setenv("PORTAL_BASE_URL", "https://finance-box.example.test")
	t.Setenv("ERP_BASE_URL", "https://finance-box.example.test")
	unsetForTest(t, "DEMO_MODE")

	_, err := registerRoutes(discardRegistrar{}, financeBoxGetenv(map[string]string{
		"HH_BOX_KIND": "finance",
	}))
	if err == nil || !strings.Contains(err.Error(), "APP_ENV") || !strings.Contains(err.Error(), "staging") {
		t.Fatalf("a finance box on a non staging env must stop the boot naming APP_ENV and staging, got %v", err)
	}
}

func TestRegisterRoutes_NonFinanceBoxSkipsTheGuard(t *testing.T) {
	// A copy that never said it is the finance box must keep booting exactly
	// as before: the guard is inert without HH_BOX_KIND=finance, and the
	// refusal below is the ordinary missing configuration one, not the guard.
	t.Setenv("APP_ENV", "production")
	t.Setenv("PAYMENT_VAULT_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	unsetForTest(t, "PORTAL_BASE_URL")
	unsetForTest(t, "ERP_BASE_URL")
	unsetForTest(t, "DEMO_MODE")

	_, err := registerRoutes(discardRegistrar{}, func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "PORTAL_BASE_URL") {
		t.Fatalf("a non finance copy must die on the ordinary configuration check, got %v", err)
	}
}
