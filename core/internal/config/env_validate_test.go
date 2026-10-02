package config

import (
	"fmt"
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

// TestValidate_UnsetAppEnvMessageNamesAcceptedValues: the operator reads this message to learn what to set.
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

// TestValidate_AppEnvIsExactlyOneOfTheFour: a typo must not boot as a "strict" box that then
// slips past the checks keyed on the literal word production. Case-sensitive, no trimming.
func TestValidate_AppEnvIsExactlyOneOfTheFour(t *testing.T) {
	for _, env := range []string{"prod", "Production", "PRODUCTION", "stagin", " ", "dev ", " dev", "Dev", "Demo", "qa"} {
		c := validNonDev()
		c.Env = env
		err := c.Validate()
		if err == nil {
			t.Errorf("APP_ENV %q must be refused", env)
			continue
		}
		msg := err.Error()
		for _, want := range []string{"APP_ENV", "dev", "staging", "production", "demo"} {
			if !strings.Contains(msg, want) {
				t.Errorf("APP_ENV %q: refusal must mention %q: %q", env, want, msg)
			}
		}
		if strings.Contains(msg, fmt.Sprintf("%q", env)) {
			t.Errorf("APP_ENV %q: refusal echoed the bad value: %q", env, msg)
		}
	}
	for _, env := range []string{"dev", "staging", "production", "demo"} {
		c := validNonDev()
		c.Env = env
		if err := c.Validate(); err != nil {
			t.Errorf("APP_ENV %q must pass: %v", env, err)
		}
	}
}

// badBaseURLs are values a base URL must be refused for. Each is run through both variables.
var badBaseURLs = []string{
	"",
	"https://localhost", "https://localhost.", "https://LOCALHOST", "https://foo.localhost", "https://foo.localhost.",
	"https://127.0.0.1", "https://127.0.0.1.", "https://127.1", "https://2130706433", "https://0x7f.0.0.1",
	"https://0177.0.0.1", "https://127.0.0.2", "https://0.0.0.0", "https://[::]", "https://[::1]",
	"https://[::1%25eth0]", "https://[fe80::1%25eth0]", "https://[::ffff:127.0.0.1]",
	"http://erp.example.com", "ftp://erp.example.com", "//erp.example.com", "/portal.html", "erp.example.com",
	"https:///portal.html", "https://:8443",
	"https://user:pw-sentinel@erp.example.com", "https://user@erp.example.com",
	"https://erp.example.com/?q=sentinel", "https://erp.example.com/#frag-sentinel", "https://erp.example.com/?",
	// round 3: bare 0x labels, non-ASCII spellings of localhost and 127.0.0.1, junk hosts and ports
	"https://127.0.0x.1", "https://0x7f.0x.0x.1", "https://127.0x.1",
	"https://localhost\u3002", "https://127\u30020\u30020\u30021", "https://\uff4c\uff4f\uff43\uff41\uff4c\uff48\uff4f\uff53\uff54",
	"https://local\u00adhost", "https://local\u200bhost", "https://erp.exam\u00e9ple.com",
	"https://localhost..", "https://..", "https://-", "https://h:", "https://erp.example.com:0", "https://erp.example.com:65536",
	"https://erp.example.com/a b", "https://erp.example.com/a\u00a0b", "https://erp.example.com/a\u0085b", "https://erp.example.com/a\u2028b",
	"https://erp.example.com ", " https://erp.example.com", "https://erp.example.com/\x00", "https://erp.example.com/a\nb",
}

func TestValidate_BaseURLShapeIsRefusedForBothVariables(t *testing.T) {
	for _, raw := range badBaseURLs {
		for name, set := range map[string]func(*Config, string){
			"PORTAL_BASE_URL": func(c *Config, v string) { c.PortalBaseURL = v },
			"ERP_BASE_URL":    func(c *Config, v string) { c.StaffBaseURL = v },
		} {
			c := validNonDev()
			set(&c, raw)
			err := c.Validate()
			if err == nil {
				t.Errorf("%s=%q must be refused", name, raw)
				continue
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("%s=%q: refusal must name the variable: %q", name, raw, err)
			}
			// Never the value: credentials, tokens and hosts stay out of logs.
			for _, leak := range []string{"pw-sentinel", "frag-sentinel", "q=sentinel", "user@", "erp.example.com"} {
				if strings.Contains(raw, leak) && strings.Contains(err.Error(), leak) {
					t.Errorf("%s=%q: refusal printed part of the value (%q): %q", name, raw, leak, err)
				}
			}
			if raw != "" && len(raw) > 4 && strings.Contains(err.Error(), raw) {
				t.Errorf("%s=%q: refusal printed the whole value: %q", name, raw, err)
			}
		}
	}
}

// Policy: private and internal hosts are allowed (a staging box may sit on a tailnet).
func TestValidate_BaseURLAcceptsOrdinaryHosts(t *testing.T) {
	for _, raw := range []string{
		"https://erp.example.com", "https://erp.example.com/", "https://pro.example.com/portal.html",
		"https://erp.example.com:8443", "https://203.0.113.7", "https://10.0.0.5", "https://100.64.48.69",
		"https://[2001:db8::1]", "https://staging", "https://erp.example.com:65535", "https://erp.example.com/base/path", "https://erp.internal", "https://box.local", "https://xn--e1afmkfd.example", "https://name.local/portal.html", "https://0x.example.com",
	} {
		c := validNonDev()
		c.PortalBaseURL, c.StaffBaseURL = raw, raw
		if err := c.Validate(); err != nil {
			t.Errorf("%q must pass: %v", raw, err)
		}
	}
}

// A mistyped APP_ENV is refused by the allowlist even with DEMO_MODE on, and is not echoed.
func TestValidate_AppEnvAllowlistWinsOverDemoModeAndDoesNotEcho(t *testing.T) {
	for _, env := range []string{"prod", "stagin", "Production", "qa", "demo-box"} {
		c := validNonDev()
		c.Env, c.DemoMode = env, true
		err := c.Validate()
		if err == nil {
			t.Errorf("APP_ENV %q with DEMO_MODE must be refused", env)
			continue
		}
		if !strings.Contains(err.Error(), "accepted values") || strings.Contains(err.Error(), fmt.Sprintf("%q", env)) {
			t.Errorf("APP_ENV %q with DEMO_MODE: want the allowlist refusal without an echo, got %q", env, err)
		}
	}
}
