//go:build demo

package boot

import (
	"strings"
	"testing"
)

var deny = ProviderDeny{
	Prefixes: []string{"PLANE_", "BISTRACK_", "TWILIO_", "HOS_PAYMENT_", "HOS_ASSIST_"},
	// The feedback bridge's own five (the Feature Polish ruling): the one
	// outbound a demo box may carry, by exact name.
	PrefixExceptions: []string{
		"PLANE_API_URL", "PLANE_API_KEY", "PLANE_PROJECT_ID",
		"PLANE_WORKSPACE_SLUG", "PLANE_FEEDBACK_MODULE",
		// The assistant's two (slice 3), by exact name.
		"HOS_ASSIST_ANTHROPIC_API_KEY", "HOS_ASSIST_DEALER_CAP_MICROCENTS",
	},
	Names:       []string{"SMS_PROVIDER", "HOS_CARD_BASE_URL", "NATS_URL", "HOS_STATEMENT_NOTICES"},
	CardVar:     "HOS_CARD_PROCESSOR",
	CardAllowed: "stub",
}

// G4: every denied shape refuses, by name: any variable under a denied
// prefix, any denied single name, and the card processor variable at any
// value but the simulator stub.
func TestRefuseProviderVarsRefusals(t *testing.T) {
	cases := []struct {
		name    string
		environ []string
		want    string
	}{
		{"plane prefix on any suffix", []string{"PLANE_SOMETHING_NEW=1"}, "PLANE_SOMETHING_NEW"},
		{"bistrack pair", []string{"BISTRACK_API_KEY=x"}, "BISTRACK_API_KEY"},
		{"twilio credential", []string{"TWILIO_AUTH_TOKEN=x"}, "TWILIO_AUTH_TOKEN"},
		{"live gateway key", []string{"HOS_PAYMENT_API_KEY=x"}, "HOS_PAYMENT_API_KEY"},
		{"assist key lookalike", []string{"HOS_ASSIST_ANTHROPIC_API_KEY_2=x"}, "HOS_ASSIST_ANTHROPIC_API_KEY_2"},
		{"assist future name", []string{"HOS_ASSIST_SOMETHING_NEW=x"}, "HOS_ASSIST_SOMETHING_NEW"},
		{"assist gateway url", []string{"HOS_ASSIST_GATEWAY_URL=http://gateway.example/v1"}, "HOS_ASSIST_GATEWAY_URL"},
		{"assist gateway key", []string{"HOS_ASSIST_GATEWAY_KEY=x"}, "HOS_ASSIST_GATEWAY_KEY"},
		{"assist dealer tag", []string{"HOS_ASSIST_DEALER_TAG=x"}, "HOS_ASSIST_DEALER_TAG"},
		{"sms provider selection", []string{"SMS_PROVIDER=twilio"}, "SMS_PROVIDER"},
		{"card base url", []string{"HOS_CARD_BASE_URL=https://cards.example"}, "HOS_CARD_BASE_URL"},
		{"message bus", []string{"NATS_URL=nats://bustr.example"}, "NATS_URL"},
		{"statement notices", []string{"HOS_STATEMENT_NOTICES=1"}, "HOS_STATEMENT_NOTICES"},
		{"card processor live", []string{"HOS_CARD_PROCESSOR=run"}, "HOS_CARD_PROCESSOR"},
		{"card processor empty is not stub", []string{"HOS_CARD_PROCESSOR="}, "HOS_CARD_PROCESSOR"},
	}
	for _, c := range cases {
		err := RefuseProviderVars(c.environ, deny)
		if err == nil {
			t.Fatalf("%s: accepted", c.name)
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: refusal %q does not name %s", c.name, err, c.want)
		}
	}
}

// The feedback bridge's five exact names pass the gate (the Feature Polish
// ruling): the one outbound a demo box may carry, and only these five - any
// other PLANE_ name still refuses, which the refusal table above holds.
func TestRefuseProviderVarsAcceptsTheFeedbackBridge(t *testing.T) {
	environ := []string{
		"PLANE_API_URL=https://pm.example",
		"PLANE_API_KEY=bridge-key",
		"PLANE_PROJECT_ID=bridge-project",
		"PLANE_WORKSPACE_SLUG=ws",
		"PLANE_FEEDBACK_MODULE=Feature Polish",
	}
	if err := RefuseProviderVars(environ, deny); err != nil {
		t.Fatalf("the feedback bridge's variables refused: %v", err)
	}
}

// The demo box's own environment passes: the card processor unset or at
// the stub, and none of the denied names.
func TestRefuseProviderVarsAcceptsTheDemoBox(t *testing.T) {
	environ := []string{
		"DEMO_MODE=true",
		"APP_ENV=demo",
		"CORE_DATABASE_URL=postgres://demo@127.0.0.1/db?sslmode=disable",
		"DATABASE_URL=postgres://demo@127.0.0.1/db?sslmode=disable",
		"PORT=8080",
		"HOS_CARD_PROCESSOR=stub",
		"HH_ACCEPTANCE_SIGN_KEY_ID=demo-1",
	}
	if err := RefuseProviderVars(environ, deny); err != nil {
		t.Fatalf("the demo box's environment refused: %v", err)
	}
	// An empty environment and a valueless entry are not refusals either.
	if err := RefuseProviderVars(nil, deny); err != nil {
		t.Fatalf("empty environment refused: %v", err)
	}
	if err := RefuseProviderVars([]string{"=novalue"}, deny); err != nil {
		t.Fatalf("valueless entry refused: %v", err)
	}
}

// A name under the explicit prefix that is on no list refuses the boot: a
// new CORE_ name is never classed allowed by default, so a new wire out of
// the box (the console mode's Appwrite names are the one declared shape)
// has to be stated by name before a demo box boots with it.
func TestRefuseProviderVarsRefusesAnUnknownNameUnderTheExplicitPrefix(t *testing.T) {
	d := deny
	d.ExplicitPrefix = "CORE_"
	d.ExplicitAllowed = []string{"CORE_SIGNIN_MODE", "CORE_APPWRITE_ENDPOINT"}
	if err := RefuseProviderVars([]string{"CORE_SIGNIN_MODE=console", "CORE_APPWRITE_ENDPOINT=https://x", "PATH=/bin"}, d); err != nil {
		t.Fatalf("listed names refused: %v", err)
	}
	err := RefuseProviderVars([]string{"CORE_SIGNIN_MODE=console", "CORE_BRAND_NEW_WIRE=secret-value"}, d)
	if err == nil {
		t.Fatal("an unknown CORE_ name was accepted")
	}
	if !strings.Contains(err.Error(), "CORE_BRAND_NEW_WIRE") {
		t.Fatalf("refusal does not name the variable: %v", err)
	}
	if strings.Contains(err.Error(), "secret-value") {
		t.Fatal("refusal carries the value")
	}
	// The default zero value keeps the old behaviour (no explicit prefix).
	if err := RefuseProviderVars([]string{"CORE_BRAND_NEW_WIRE=1"}, deny); err != nil {
		t.Fatalf("zero explicit prefix refused: %v", err)
	}
}

// Slice 3: a demo box that carries the assistant's key must carry a
// positive dealer cap, or the boot refuses with a message naming the cap and
// never the key's value. No key means no requirement (the stub serves).
func TestRefuseAssistKeyWithoutCap(t *testing.T) {
	const keyValue = "fake-key-must-not-print"
	cases := []struct {
		name    string
		env     map[string]string
		wantErr bool
	}{
		{"no key, no cap", map[string]string{}, false},
		{"empty key, no cap", map[string]string{"HOS_ASSIST_ANTHROPIC_API_KEY": "  "}, false},
		{"cap without key", map[string]string{"HOS_ASSIST_DEALER_CAP_MICROCENTS": "5"}, false},
		{"key with a cap", map[string]string{"HOS_ASSIST_ANTHROPIC_API_KEY": keyValue, "HOS_ASSIST_DEALER_CAP_MICROCENTS": "5000000"}, false},
		{"key, cap unset", map[string]string{"HOS_ASSIST_ANTHROPIC_API_KEY": keyValue}, true},
		{"key, cap empty", map[string]string{"HOS_ASSIST_ANTHROPIC_API_KEY": keyValue, "HOS_ASSIST_DEALER_CAP_MICROCENTS": ""}, true},
		{"key, cap zero", map[string]string{"HOS_ASSIST_ANTHROPIC_API_KEY": keyValue, "HOS_ASSIST_DEALER_CAP_MICROCENTS": "0"}, true},
		{"key, cap negative", map[string]string{"HOS_ASSIST_ANTHROPIC_API_KEY": keyValue, "HOS_ASSIST_DEALER_CAP_MICROCENTS": "-4"}, true},
		{"key, cap not a number", map[string]string{"HOS_ASSIST_ANTHROPIC_API_KEY": keyValue, "HOS_ASSIST_DEALER_CAP_MICROCENTS": "lots"}, true},
	}
	for _, c := range cases {
		err := RefuseAssistKeyWithoutCap(func(k string) string { return c.env[k] })
		if (err != nil) != c.wantErr {
			t.Fatalf("%s: err = %v, wantErr %v", c.name, err, c.wantErr)
		}
		if err == nil {
			continue
		}
		if !strings.Contains(err.Error(), "HOS_ASSIST_DEALER_CAP_MICROCENTS") {
			t.Fatalf("%s: refusal %q does not name the missing cap", c.name, err)
		}
		if strings.Contains(err.Error(), keyValue) {
			t.Fatalf("%s: refusal carries the key's value", c.name)
		}
	}
}
