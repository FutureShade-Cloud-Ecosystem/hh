package config

// demo_mode_test.go, DEMO_MODE, the flag behind internal/demo's credential bypass.
//
// A separate file from config_test.go because this flag is a different KIND of setting from
// everything else in this package. The rest of Validate() guards keys: a weak one weakens a
// signature. This one guards a door: set on the wrong box, it hands out staff sessions to anyone who
// can reach the port. So it is checked first in Validate, it is off by default, it fails closed on a
// value it cannot parse, and it takes two independent facts to open.
//
//	go test ./internal/config/ -count=1

import (
	"strings"
	"testing"
)

// -- the default -------------------------------------------------------------

// TestDemoMode_IsOffByDefault. The single most important property in this file: a box that says
// nothing about the demo does not have the demo.
func TestDemoMode_IsOffByDefault(t *testing.T) {
	if c := Load(); c.DemoMode {
		t.Fatal("DEMO_MODE defaults to ON, a box must ask for the credential bypass, never inherit it")
	}
	if c := Load(); c.DemoEnabled() {
		t.Fatal("DemoEnabled() is true with no DEMO_MODE set")
	}
}

// TestDemoMode_ReadsTheEnvVarAndFailsClosed. Only values strconv.ParseBool recognises turn it on;
// everything else, including the plausible-looking "yes" and "on", reads as off, because this is
// the one flag where a typo must land on the closed side of the door.
func TestDemoMode_ReadsTheEnvVarAndFailsClosed(t *testing.T) {
	for _, on := range []string{"1", "t", "T", "true", "TRUE", "True"} {
		t.Setenv("DEMO_MODE", on)
		if !Load().DemoMode {
			t.Errorf("DEMO_MODE=%q did not turn the flag on", on)
		}
	}
	for _, off := range []string{"0", "f", "false", "FALSE", "False", "yes", "on", "YES", "enabled", "please", " true", "true ", ""} {
		t.Setenv("DEMO_MODE", off)
		if Load().DemoMode {
			t.Errorf("DEMO_MODE=%q turned the credential bypass ON, anything strconv.ParseBool does not recognise must fail closed", off)
		}
	}
}

// -- the second lock: DemoEnabled --------------------------------------------

// TestDemoEnabled_NeedsTheFlagAndTheEnvironment. The composition root gates on this, and it is
// deliberately stricter than the flag alone: a box must both opt in and BE a demo box.
func TestDemoEnabled_NeedsTheFlagAndTheEnvironment(t *testing.T) {
	for _, c := range []struct {
		env  string
		flag bool
		want bool
	}{
		{"dev", true, true},      // the local preview stack
		{EnvDemo, true, true},    // the droplet's demo site
		{"dev", false, false},    // no flag, no route, even in dev
		{EnvDemo, false, false},  // a demo-env box that never opted in
		{"staging", true, false}, // THE ONE THAT MATTERS: staging carries real homework
		{"production", true, false},
		{"prod", true, false},
		{"qa", true, false},
		{"", true, false},      // an unset APP_ENV is not a demo box
		{"Demo", true, false},  // case-sensitive: "demo" or nothing
		{" demo", true, false}, // and not merely demo-shaped
	} {
		got := Config{Env: c.env, DemoMode: c.flag}.DemoEnabled()
		if got != c.want {
			t.Errorf("DemoEnabled(env=%q, flag=%v) = %v, want %v", c.env, c.flag, got, c.want)
		}
	}
}

// -- the loud refusal --------------------------------------------------------

// TestValidate_RefusesToBootANonDemoBoxCarryingTheFlag. A warning would scroll past in a deploy log
// and the box would serve password-free sessions for a week. This stops the process.
func TestValidate_RefusesToBootANonDemoBoxCarryingTheFlag(t *testing.T) {
	for _, env := range []string{"staging", "production"} {
		c := Config{Env: env, PaymentVaultKey: strongVaultKey, DemoMode: true}
		err := c.Validate()
		if err == nil {
			t.Errorf("env %q with DEMO_MODE set must refuse to boot", env)
			continue
		}
		// The message has to tell an operator which lever to move; "invalid configuration" costs
		// somebody an afternoon.
		if msg := err.Error(); !strings.Contains(msg, "DEMO_MODE") || !strings.Contains(msg, env) {
			t.Errorf("env %q: the refusal must name the flag and the environment it found: %q", env, msg)
		}
	}
}

// TestValidate_AdmitsTheFlagOnADemoBox, with everything else a non-dev box must still supply. A
// demo box is not exempt from the remaining clauses: it is on the public internet like any other.
// (The strong-secrets sub-case this test used to carry left with the signing
// secrets themselves; the vault-key clause is the fail-closed check a demo box
// is still held to.)
func TestValidate_AdmitsTheFlagOnADemoBox(t *testing.T) {
	c := Config{Env: EnvDemo, PaymentVaultKey: strongVaultKey, DemoMode: true}
	if err := c.Validate(); err != nil {
		t.Fatalf("a demo box with a valid vault key must boot: %v", err)
	}
	// And it is still held to every other clause, the demo environment is not a bypass for those.
	novault := Config{Env: EnvDemo, DemoMode: true}
	if err := novault.Validate(); err == nil {
		t.Fatal("a demo box with no PAYMENT_VAULT_KEY must still be refused")
	}
}

// TestValidate_DevIsUnaffected. The local preview stack runs APP_ENV=dev with the flag on, and dev
// has always short-circuited before every check in Validate. Pinned so a future edit that moves the
// demo check above that early return does not break every developer's boot.
func TestValidate_DevIsUnaffected(t *testing.T) {
	c := Config{Env: "dev", DemoMode: true}
	if err := c.Validate(); err != nil {
		t.Fatalf("dev with DEMO_MODE on must still boot on the dev defaults: %v", err)
	}
	if !c.DemoEnabled() {
		t.Fatal("dev with DEMO_MODE on must be DemoEnabled, it is the local preview stack")
	}
}

// TestValidate_ADemoBoxWithoutTheFlagIsAnOrdinaryBox. APP_ENV=demo on its own opens nothing; the
// two facts are independent by design.
func TestValidate_ADemoBoxWithoutTheFlagIsAnOrdinaryBox(t *testing.T) {
	c := Config{Env: EnvDemo, PaymentVaultKey: strongVaultKey}
	if err := c.Validate(); err != nil {
		t.Fatalf("APP_ENV=demo without the flag is an ordinary non-dev box: %v", err)
	}
	if c.DemoEnabled() {
		t.Fatal("APP_ENV=demo alone must not enable the credential bypass")
	}
}
