package main

import (
	"os"
	"strings"
	"testing"
)

// The serve and worker roles both build their graph through registerRoutes, whose first
// act is config.Validate. These drive that seam with no database: the refusal has to
// land before anything dials, so the error is the configuration's own.
func TestRegisterRoutes_RefusesUnsetAppEnv(t *testing.T) {
	t.Setenv("APP_ENV", "")
	os.Unsetenv("APP_ENV")
	_, err := registerRoutes(discardRegistrar{}, func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "invalid configuration") || !strings.Contains(err.Error(), "APP_ENV") {
		t.Fatalf("an unset APP_ENV must stop the core with a configuration error naming APP_ENV, got %v", err)
	}
}

func TestRegisterRoutes_ProductionRefusesMissingBaseURLs(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("PAYMENT_VAULT_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	t.Setenv("PORTAL_BASE_URL", "")
	t.Setenv("ERP_BASE_URL", "")
	_, err := registerRoutes(discardRegistrar{}, func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "invalid configuration") || !strings.Contains(err.Error(), "PORTAL_BASE_URL") {
		t.Fatalf("production with no PORTAL_BASE_URL must stop the core naming it, got %v", err)
	}
}
