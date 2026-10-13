//go:build demo

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// The environment each wiring test starts from: a box that says demo
// twice and a serve configuration that would load. Every test below
// changes one thing and asserts which guard fired. The database URL is
// made up and must never be dialed while the guards hold: a test that
// reaches it is a test whose guard is gone, which is what the failure
// then shows.
func serveEnv(t *testing.T) map[string]string {
	t.Helper()
	// config.Load reads the process environment, and Validate refuses an unset
	// APP_ENV (HH-198): the made-up map below is the identity environment only.
	t.Setenv("APP_ENV", "dev")
	return map[string]string{
		"DEMO_MODE":            "true",
		"APP_ENV":              "demo",
		"CORE_DATABASE_URL":    "postgres://b3:b3@127.0.0.1:5432/b3?sslmode=disable",
		"CORE_STAFF_AUDIENCE":  "hh-staff",
		"CORE_SIGNIN_MODE":     "demo",
		"CORE_TENANT_AUDIENCE": "hh-tenant",
	}
}

func envFuncs(env map[string]string) (func(string) string, func(string) (string, bool), func() []string) {
	getenv := func(k string) string { return env[k] }
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	environ := func() []string {
		out := make([]string, 0, len(env))
		for k, v := range env {
			out = append(out, k+"="+v)
		}
		return out
	}
	return getenv, lookup, environ
}

// G2 through the serve entry: a box that has not said demo twice stops
// demoServe before anything else runs, with the offending variable named.
// The cases are the guard's table, replayed through the function the
// entrypoint dispatches to, so removing the guard call from demoServe
// fails here and not only in the boot package's own suite.
func TestDemoServeRefusesABoxThatDoesNotSayDemo(t *testing.T) {
	cases := []struct {
		name  string
		drop  string
		set   map[string]string
		inErr string
	}{
		{"environment unset", "APP_ENV", nil, "APP_ENV"},
		{"environment dev", "", map[string]string{"APP_ENV": "dev"}, "APP_ENV"},
		{"mode unset", "DEMO_MODE", nil, "DEMO_MODE"},
	}
	for _, c := range cases {
		env := serveEnv(t)
		if c.drop != "" {
			delete(env, c.drop)
		}
		for k, v := range c.set {
			env[k] = v
		}
		getenv, lookup, environ := envFuncs(env)
		err := demoServe(getenv, lookup, environ)
		if err == nil {
			t.Fatalf("%s: demoServe accepted", c.name)
		}
		if !strings.Contains(err.Error(), c.inErr) {
			t.Fatalf("%s: refusal %q does not name %s", c.name, err, c.inErr)
		}
	}
}

// G4 through the serve entry: one provider variable in the environment
// picture stops demoServe before the configuration loads, naming the
// variable. Removing the RefuseProviderVars call from demoServe fails
// here, not only in the boot package's suite.
func TestDemoServeRefusesAProviderVariable(t *testing.T) {
	env := serveEnv(t)
	getenv, lookup, environ := envFuncs(env)
	picture := append(environ(), "TWILIO_AUTH_TOKEN=x")
	err := demoServe(getenv, lookup, func() []string { return picture })
	if err == nil {
		t.Fatal("a provider variable in the environment picture accepted")
	}
	if !strings.Contains(err.Error(), "TWILIO_AUTH_TOKEN") {
		t.Fatalf("refusal %q does not name TWILIO_AUTH_TOKEN", err)
	}
}

// The demo environment guard through the serve entry: a CORE_ name on no
// list refuses the boot, naming the variable and never its value, while the
// console mode's declared names pass the guard (the next guard, the made up
// database, is what stops the serve).
func TestDemoServeRefusesAnUnknownCoreName(t *testing.T) {
	env := serveEnv(t)
	getenv, lookup, environ := envFuncs(env)
	picture := append(environ(), "CORE_BRAND_NEW_WIRE=value-that-must-not-print")
	err := demoServe(getenv, lookup, func() []string { return picture })
	if err == nil {
		t.Fatal("an unknown CORE_ name was accepted")
	}
	if !strings.Contains(err.Error(), "CORE_BRAND_NEW_WIRE") || strings.Contains(err.Error(), "must-not-print") {
		t.Fatalf("refusal %q", err)
	}
}

// The entrypoint-only operator variable is not the core's: the guard refuses
// it, which is why the entrypoint un-exports it before the core starts
// (deploy/demo/entrypoint-port-test.sh holds that side).
func TestDemoServeRefusesTheEntrypointOnlyPortName(t *testing.T) {
	env := serveEnv(t)
	getenv, lookup, environ := envFuncs(env)
	picture := append(environ(), "CORE_INTERNAL_PORT=9090")
	err := demoServe(getenv, lookup, func() []string { return picture })
	if err == nil || !strings.Contains(err.Error(), "CORE_INTERNAL_PORT") {
		t.Fatalf("refusal %v", err)
	}
}

func TestDemoServeAcceptsTheConsoleModeNames(t *testing.T) {
	for _, name := range append([]string{"CORE_EXCHANGE_JWT_BOUND", "CORE_TRUSTED_PROXIES", "CORE_SIGNIN_EMAIL_DOMAINS", "CORE_SIGNIN_EMAIL_ADDRESSES"}, demoEnvAllowCoreNames...) {
		env := serveEnv(t)
		getenv, lookup, environ := envFuncs(env)
		picture := append(environ(), name+"=x")
		err := demoServe(getenv, lookup, func() []string { return picture })
		if err != nil && strings.Contains(err.Error(), "is on no list") {
			t.Errorf("%s refused by the guard: %v", name, err)
		}
	}
}

// G4's one exception, through the serve entry: the feedback bridge is the one
// outbound a demo box may carry (the Feature Polish ruling), so the bridge's
// own five variables pass the provider gate. The proof the gate was passed is
// the NEXT guard's refusal: the made-up database stops the serve with the
// database's own error, never with an outbound provider error naming a bridge
// variable. Written red first: on main every PLANE_ name refused the boot.
func TestDemoServeAcceptsTheFeedbackBridgeVariables(t *testing.T) {
	for _, name := range []string{
		"PLANE_API_URL", "PLANE_API_KEY", "PLANE_PROJECT_ID",
		"PLANE_WORKSPACE_SLUG", "PLANE_FEEDBACK_MODULE",
	} {
		env := serveEnv(t)
		env[name] = "bridge-value"
		getenv, lookup, environ := envFuncs(env)
		err := demoServe(getenv, lookup, environ)
		if err == nil {
			t.Fatalf("%s: the made-up database must still stop the serve", name)
		}
		if strings.Contains(err.Error(), name) || strings.Contains(err.Error(), "outbound provider") {
			t.Fatalf("%s: the bridge variable refused the boot: %v", name, err)
		}
	}
}

// The exception is exactly the bridge's five names: every other PLANE_
// variable still refuses the boot, and so do the other denied prefixes. This
// is the regression guard the ruling asked to keep.
func TestDemoServeStillRefusesEveryOtherProviderPrefix(t *testing.T) {
	for _, kv := range []string{
		"PLANE_SOMETHING_NEW=1",
		"BISTRACK_API_KEY=x",
		"TWILIO_AUTH_TOKEN=x",
		"HOS_PAYMENT_API_KEY=x",
		"HOS_ASSIST_ANTHROPIC_API_KEY=x",
	} {
		env := serveEnv(t)
		getenv, lookup, environ := envFuncs(env)
		picture := append(environ(), kv)
		err := demoServe(getenv, lookup, func() []string { return picture })
		if err == nil {
			t.Fatalf("%s: accepted", kv)
		}
		if !strings.Contains(err.Error(), "outbound provider") {
			t.Fatalf("%s: refusal %q is not the provider guard's", kv, err)
		}
	}
}

// G3 through the serve entry: the database check runs inside
// demoRegisterRoutes, before anything registers or serves, so a
// non-demo database stops demoServe with the guard's own refusal and the
// listener never opens. The listener address is deliberately
// unlistenable: if the guard were removed, serve would run to the
// listener and fail there instead, which is the failure this test
// distinguishes. Gated on the suite's database variable (the boot suite
// builds the same world by hand); it skips politely without it.
func TestDemoServeChecksTheDatabaseBeforeTheListener(t *testing.T) {
	base := os.Getenv("HH_CORE_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("HH_CORE_TEST_DATABASE_URL is not set: the serve wiring's database leg needs a scratch database to refuse")
	}
	env := serveEnv(t)
	env["CORE_DATABASE_URL"] = base
	// The gate database is migrated and holds no demo org, so the guard's
	// org check is the refusal this test expects.
	env["CORE_LISTEN_ADDR"] = "bogus-listen-address-of-the-w1-test:0"
	getenv, lookup, environ := envFuncs(env)
	err := demoServe(getenv, lookup, environ)
	if err == nil {
		t.Fatal("a non-demo database served")
	}
	if !strings.Contains(err.Error(), "demo database check") {
		t.Fatalf("refusal %q is not the database guard firing before the listener", err)
	}
}

// P3-9: the token expiry is capped at the demo ceiling; a longer value
// refuses at load with the variable named.
func TestDemoServeRefusesATokenExpiryOverTheCeiling(t *testing.T) {
	env := serveEnv(t)
	env["CORE_DEMO_TOKEN_TTL"] = "24h"
	getenv, lookup, environ := envFuncs(env)
	err := demoServe(getenv, lookup, environ)
	if err == nil {
		t.Fatal("a token expiry over the ceiling accepted")
	}
	if !strings.Contains(err.Error(), "CORE_DEMO_TOKEN_TTL") {
		t.Fatalf("refusal %q does not name CORE_DEMO_TOKEN_TTL", err)
	}
	// At the ceiling it loads: the guard fires on the excess, not the
	// knob. This configuration gets past the guards and dies at the
	// made-up database, which the message must show rather than a
	// ceiling refusal.
	env["CORE_DEMO_TOKEN_TTL"] = "1h"
	getenv, lookup, environ = envFuncs(env)
	err = demoServe(getenv, lookup, environ)
	if err == nil || strings.Contains(err.Error(), "CORE_DEMO_TOKEN_TTL") {
		t.Fatalf("an at-ceiling expiry was refused as over it: %v", err)
	}
}

// The demo dealer's one policy difference from the decided default: the
// refund approval limit is off on the demo load (the Dibbits ruling), so no
// refund in the demo, of any size, ever asks for an approver; the default
// itself stays a limit dealer, the gate every real dealer inherits. The
// mirror keeps the two loads honest in one place: if the demo ever goes
// back to a plain Default(), or the default's own switch flips, this fails.
func TestDemoRoleConfig_RefundApprovalLimitOffOnTheDemoLoadOnly(t *testing.T) {
	demo, err := demoRoleConfig()
	if err != nil {
		t.Fatalf("demoRoleConfig: %v", err)
	}
	if demo.RefundRequiresApproval(9_000_000_000) {
		t.Fatal("the demo dealer has no refund approval threshold: no cumulative, however large, may ask for an approver")
	}
	// The rest of the demo's policy is the decided default's, the one
	// difference switched: the amount and the approvers still read, so a
	// later switch back to a limit serves the default's numbers.
	if got := demo.RefundAmountWithoutApprovalCents(); got != 25000 {
		t.Fatalf("the demo keeps the default's configured amount, got %d", got)
	}

	prod, err := roleconfig.Default()
	if err != nil {
		t.Fatalf("roleconfig.Default: %v", err)
	}
	if !prod.RefundRequiresApproval(25001) {
		t.Fatal("the decided default must keep the limit on: a dealer who sets a number keeps the gate")
	}
}
