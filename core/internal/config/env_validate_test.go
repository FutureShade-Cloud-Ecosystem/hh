package config

import (
	"strings"
	"testing"
)

// validNonDev is a production Config that satisfies every clause; each case below
// breaks exactly one thing in it.
func validNonDev() Config {
	return Config{
		Env:             "production",
		PaymentVaultKey: strongVaultKey,
		PortalBaseURL:   "https://pro.example.com/portal.html",
		StaffBaseURL:    "https://erp.example.com",
	}
}

// TestLoad_UnsetAppEnvIsRecorded: an unset APP_ENV still reads as dev for the one-shot
// tools (migrate, seed, demoseed), but Load records that nobody said so, which is the
// fact the roles that serve traffic refuse on.
func TestLoad_UnsetAppEnvIsRecorded(t *testing.T) {
	t.Setenv("APP_ENV", "")
	c := Load()
	if c.Env != "dev" || !c.EnvUnset {
		t.Fatalf("unset APP_ENV: Env=%q EnvUnset=%v, want dev and true", c.Env, c.EnvUnset)
	}
	t.Setenv("APP_ENV", "dev")
	if c = Load(); c.Env != "dev" || c.EnvUnset {
		t.Fatalf("explicit dev: Env=%q EnvUnset=%v, want dev and false", c.Env, c.EnvUnset)
	}
}

// TestLoad_BaseURLDefaultsOnlyInDevAndDemo: a production box must not inherit a
// localhost link base from a default.
func TestLoad_BaseURLDefaultsOnlyInDevAndDemo(t *testing.T) {
	t.Setenv("PORTAL_BASE_URL", "")
	t.Setenv("ERP_BASE_URL", "")
	for env, wantLocal := range map[string]bool{"dev": true, "demo": true, "staging": false, "production": false} {
		t.Setenv("APP_ENV", env)
		c := Load()
		gotLocal := strings.Contains(c.PortalBaseURL, "localhost") && strings.Contains(c.StaffBaseURL, "localhost")
		if gotLocal != wantLocal {
			t.Errorf("env %q: localhost defaults present = %v, want %v (portal=%q erp=%q)", env, gotLocal, wantLocal, c.PortalBaseURL, c.StaffBaseURL)
		}
	}
}

func TestValidate_AppEnvAndBaseURLs(t *testing.T) {
	cases := []struct {
		name    string
		mut     func(*Config)
		wantErr string // empty: must pass; otherwise the variable the error names
	}{
		{"unset APP_ENV is refused", func(c *Config) { c.Env = "dev"; c.EnvUnset = true }, "APP_ENV"},
		{"unset APP_ENV is refused even with a good config", func(c *Config) { c.EnvUnset = true }, "APP_ENV"},
		{"explicit dev stays permissive", func(c *Config) { *c = Config{Env: "dev"} }, ""},
		{"production, everything valid", func(c *Config) {}, ""},
		{"staging, everything valid", func(c *Config) { c.Env = "staging" }, ""},
		{"portal url unset", func(c *Config) { c.PortalBaseURL = "" }, "PORTAL_BASE_URL"},
		{"erp url unset", func(c *Config) { c.StaffBaseURL = "" }, "ERP_BASE_URL"},
		{"portal url localhost", func(c *Config) { c.PortalBaseURL = "https://localhost/portal.html" }, "PORTAL_BASE_URL"},
		{"portal url the old default", func(c *Config) { c.PortalBaseURL = "http://localhost:5173/portal.html" }, "PORTAL_BASE_URL"},
		{"erp url 127.0.0.1", func(c *Config) { c.StaffBaseURL = "https://127.0.0.1:5173" }, "ERP_BASE_URL"},
		{"erp url ipv6 loopback", func(c *Config) { c.StaffBaseURL = "https://[::1]:5173" }, "ERP_BASE_URL"},
		{"erp url plain http", func(c *Config) { c.StaffBaseURL = "http://erp.example.com" }, "ERP_BASE_URL"},
		{"portal url relative", func(c *Config) { c.PortalBaseURL = "/portal.html" }, "PORTAL_BASE_URL"},
		{"portal url empty host", func(c *Config) { c.PortalBaseURL = "https:///portal.html" }, "PORTAL_BASE_URL"},
		{"portal url not a url", func(c *Config) { c.PortalBaseURL = "pro.example.com" }, "PORTAL_BASE_URL"},
		{"vault key absent", func(c *Config) { c.PaymentVaultKey = "" }, "PAYMENT_VAULT_KEY"},
		{"vault key short", func(c *Config) { c.PaymentVaultKey = "deadbeef" }, "PAYMENT_VAULT_KEY"},
		{"demo box is not held to the url clauses", func(c *Config) { c.Env = EnvDemo; c.PortalBaseURL = ""; c.StaffBaseURL = "" }, ""},
		{"demo box is still held to the vault key", func(c *Config) { c.Env = EnvDemo; c.PaymentVaultKey = "" }, "PAYMENT_VAULT_KEY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validNonDev()
			tc.mut(&c)
			err := c.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want pass, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want a refusal naming %s, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("refusal must name %s: %q", tc.wantErr, err)
			}
			if strings.Contains(err.Error(), strongVaultKey) {
				t.Fatalf("the refusal printed the vault key")
			}
		})
	}
}

// TestValidate_UnsetAppEnvMessageNamesAcceptedValues: the operator reads this at 3am.
func TestValidate_UnsetAppEnvMessageNamesAcceptedValues(t *testing.T) {
	c := validNonDev()
	c.EnvUnset = true
	err := c.Validate()
	if err == nil {
		t.Fatal("unset APP_ENV must be refused")
	}
	for _, v := range []string{"APP_ENV", "dev", "staging", "production", "demo"} {
		if !strings.Contains(err.Error(), v) {
			t.Errorf("message must mention %q: %q", v, err)
		}
	}
}
