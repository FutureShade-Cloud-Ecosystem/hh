//go:build demo

package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo"
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

// Slice 3: the assistant is admitted on a demo box by two exact names and
// nothing else. Every other HOS_ASSIST_ name (the gateway pair, the dealer
// tag, a name a later slice adds, a near miss of an admitted name) still
// refuses the boot with the provider guard's own message.
func TestDemoServeStillRefusesEveryOtherAssistantName(t *testing.T) {
	for _, kv := range []string{
		"HOS_ASSIST_GATEWAY_URL=http://gateway.example/v1",
		"HOS_ASSIST_GATEWAY_KEY=x",
		"HOS_ASSIST_DEALER_TAG=x",
		"HOS_ASSIST_SOMETHING_NEW=1",
		"HOS_ASSIST_ANTHROPIC_API_KEY_2=x",
		"HOS_ASSIST_DEALER_CAP_MICROCENTS_OLD=1",
	} {
		env := serveEnv(t)
		env["HOS_ASSIST_ANTHROPIC_API_KEY"] = "fake-key-for-the-guard-test"
		env["HOS_ASSIST_DEALER_CAP_MICROCENTS"] = "1000000"
		getenv, lookup, environ := envFuncs(env)
		picture := append(environ(), kv)
		err := demoServe(getenv, lookup, func() []string { return picture })
		if err == nil {
			t.Fatalf("%s: accepted", kv)
		}
		name, _, _ := strings.Cut(kv, "=")
		if !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), "outbound provider") {
			t.Fatalf("%s: refusal %q is not the provider guard naming it", kv, err)
		}
	}
}

// Slice 3: the assistant's key and the dealer cap pass the provider gate.
// The proof the gate was passed is the next guard's refusal (the made-up
// database), never an outbound provider error naming either variable, and
// no refusal ever carries the key's value. Written red first: on main the
// key refused the boot.
func TestDemoServeAdmitsTheAssistantKeyAndCap(t *testing.T) {
	env := serveEnv(t)
	env["HOS_ASSIST_ANTHROPIC_API_KEY"] = "fake-key-must-not-print"
	env["HOS_ASSIST_DEALER_CAP_MICROCENTS"] = "1000000"
	getenv, lookup, environ := envFuncs(env)
	err := demoServe(getenv, lookup, environ)
	if err == nil {
		t.Fatal("the made-up database must still stop the serve")
	}
	for _, bad := range []string{"HOS_ASSIST_", "outbound provider", "fake-key-must-not-print"} {
		if strings.Contains(err.Error(), bad) {
			t.Fatalf("the assistant's variables refused the boot (%q in %v)", bad, err)
		}
	}
}

// Slice 3, through the serve entry: the key without its cap refuses the boot
// before the database is reached, naming the cap and never the key.
func TestDemoServeRefusesTheAssistantKeyWithoutItsCap(t *testing.T) {
	for _, cap := range []string{"", "0", "-1", "plenty"} {
		env := serveEnv(t)
		env["HOS_ASSIST_ANTHROPIC_API_KEY"] = "fake-key-must-not-print"
		if cap != "" {
			env["HOS_ASSIST_DEALER_CAP_MICROCENTS"] = cap
		}
		getenv, lookup, environ := envFuncs(env)
		err := demoServe(getenv, lookup, environ)
		if err == nil {
			t.Fatalf("cap %q: accepted", cap)
		}
		if !strings.Contains(err.Error(), "HOS_ASSIST_DEALER_CAP_MICROCENTS") || strings.Contains(err.Error(), "must-not-print") {
			t.Fatalf("cap %q: refusal %q", cap, err)
		}
	}
}

// Slice 3: the demo wiring must carry the assistant's per visitor, per
// address and box wide limits, the tanyai-premium pin and the stub fallback
// whenever the key is carried. The guard reads the service the wiring built
// (like G7 reads the card rail), so a service built without any one of them
// refuses, and one built the way the demo composition builds it passes.
func TestDemoRefusesAnAssistantThatIsNotDemoWired(t *testing.T) {
	bare := func() *assist.Service { return assist.NewService(nil, nil, nil, nil, nil, nil) }
	limits := func(s *assist.Service) *assist.Service {
		return s.WithRateLimits(assist.NewRateLimits(assistDemoLimits(true), time.Now))
	}
	if err := demoRefuseAssistNotDemoWired(nil, true); err == nil {
		t.Fatal("a keyed demo with no assistant was accepted")
	}
	if err := demoRefuseAssistNotDemoWired(bare(), false); err != nil {
		t.Fatalf("a keyless demo needs nothing to be safe, got %v", err)
	}
	for name, svc := range map[string]*assist.Service{
		"no limits":        bare().WithPinnedRoute(assist.AliasPremium).WithStubFallback(),
		"limits only":      limits(bare()),
		"no pin":           limits(bare()).WithStubFallback(),
		"pinned elsewhere": limits(bare()).WithPinnedRoute(assist.AliasMain).WithStubFallback(),
		"no stub fallback": limits(bare()).WithPinnedRoute(assist.AliasPremium),
	} {
		if err := demoRefuseAssistNotDemoWired(svc, true); err == nil {
			t.Fatalf("%s: a keyed demo was accepted", name)
		}
	}
	whole := limits(bare()).WithPinnedRoute(assist.AliasPremium).WithStubFallback()
	if err := demoRefuseAssistNotDemoWired(whole, true); err != nil {
		t.Fatalf("the demo composition refused: %v", err)
	}
	// The composition root asks for the limits by the configuration's own demo flag.
	if assistDemoLimits(true) == nil || assistDemoLimits(false) != nil {
		t.Fatal("assistDemoLimits must be non-nil for a demo copy and nil elsewhere")
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

// The testers' copy per-visitor allowance is read once at boot, capped at
// the maximum so it cannot be unbounded, and falls back to the default when
// unset or invalid.
func TestDemoServeVisitorAllowance(t *testing.T) {
	env := serveEnv(t)
	env["CORE_SIGNIN_MODE"] = "demo"
	getenv, _, _ := envFuncs(env)

	cfg, err := loadDemoServeConfig(getenv)
	if err != nil {
		t.Fatalf("loadDemoServeConfig: %v", err)
	}
	if cfg.visitorAllowance != demo.DefaultBurstAllowance {
		t.Errorf("unset allowance = %d, want default %d", cfg.visitorAllowance, demo.DefaultBurstAllowance)
	}

	env["HH_DEMO_MINT_VISITOR_ALLOWANCE"] = "14"
	cfg, err = loadDemoServeConfig(getenv)
	if err != nil {
		t.Fatalf("loadDemoServeConfig with 14: %v", err)
	}
	if cfg.visitorAllowance != 14 {
		t.Errorf("allowance 14 = %d, want 14", cfg.visitorAllowance)
	}

	env["HH_DEMO_MINT_VISITOR_ALLOWANCE"] = "100"
	cfg, err = loadDemoServeConfig(getenv)
	if err != nil {
		t.Fatalf("loadDemoServeConfig with 100: %v", err)
	}
	if cfg.visitorAllowance != 20 {
		t.Errorf("allowance 100 = %d, want capped 20", cfg.visitorAllowance)
	}
}

// The operator's address ceiling is read once at boot beside the visitor
// allowance: unset keeps the default, a value is capped, and it never falls
// below the visitor allowance in effect.
func TestDemoServeAddressCeiling(t *testing.T) {
	env := serveEnv(t)
	env["CORE_SIGNIN_MODE"] = "demo"
	getenv, _, _ := envFuncs(env)

	cases := []struct {
		name, ceiling, visitor string
		want                   int
	}{
		{"unset", "", "", demo.DefaultAddressCeiling},
		{"value", "120", "", 120},
		{"capped", "100000", "", demo.MaxAddressCeiling},
		{"junk", "lots", "", demo.DefaultAddressCeiling},
		{"under the visitor allowance", "8", "14", 14},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env["HH_DEMO_MINT_ADDRESS_CEILING"] = c.ceiling
			env["HH_DEMO_MINT_VISITOR_ALLOWANCE"] = c.visitor
			cfg, err := loadDemoServeConfig(getenv)
			if err != nil {
				t.Fatalf("loadDemoServeConfig: %v", err)
			}
			if cfg.addressCeiling != c.want {
				t.Errorf("addressCeiling = %d, want %d", cfg.addressCeiling, c.want)
			}
		})
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
