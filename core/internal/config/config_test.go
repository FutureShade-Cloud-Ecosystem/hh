package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	c := Load()
	if c.Port != 8080 {
		t.Fatalf("default port = %d, want 8080", c.Port)
	}
	if c.Env != "dev" {
		t.Fatalf("default env = %q, want dev", c.Env)
	}
	if c.DatabaseURL == "" {
		t.Fatal("database url default missing")
	}
}

func TestIntEnvOverride(t *testing.T) {
	t.Setenv("PORT", "9090")
	if c := Load(); c.Port != 9090 {
		t.Fatalf("port override = %d, want 9090", c.Port)
	}
}

// strongVaultKey is a valid 64-hex (32-byte) PAYMENT_VAULT_KEY for non-dev validation.
const strongVaultKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// In dev, defaults are accepted (local ergonomics; dev short-circuits Validate).
// The two signing-secret clauses this test used to exercise left with the
// HS256 world they served: the carried package no longer configures token
// signing, so what remains to prove about Validate is the dev short-circuit
// itself, the vault-key fail-closed, and the demo-flag refusals in
// demo_mode_test.go.
func TestValidate_DevAcceptsDefaults(t *testing.T) {
	c := Config{Env: "dev"}
	if err := c.Validate(); err != nil {
		t.Fatalf("dev with defaults should pass: %v", err)
	}
}

func TestValidate_ProductionWithVaultKey(t *testing.T) {
	c := Config{Env: "production", PaymentVaultKey: strongVaultKey}
	if err := c.Validate(); err != nil {
		t.Fatalf("production with a valid vault key should pass: %v", err)
	}
}

// The RUN-PAY vault key must be a 32-byte (64 hex char) key in non-dev, fail closed otherwise (W4).
func TestValidate_NonDevRequiresVaultKey(t *testing.T) {
	// missing
	c := Config{Env: "production"}
	if err := c.Validate(); err == nil {
		t.Fatal("a missing PAYMENT_VAULT_KEY must be rejected in production")
	}
	// wrong length / non-hex
	c = Config{Env: "production", PaymentVaultKey: "deadbeef"}
	if err := c.Validate(); err == nil {
		t.Fatal("a short PAYMENT_VAULT_KEY must be rejected in production")
	}
}
