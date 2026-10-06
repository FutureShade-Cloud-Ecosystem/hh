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
		{"assist key", []string{"HOS_ASSIST_ANTHROPIC_API_KEY=x"}, "HOS_ASSIST_ANTHROPIC_API_KEY"},
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
