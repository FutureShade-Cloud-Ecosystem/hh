package main

// The demo box's environment classification: the outbound-provider deny
// set (G4), the demo box's environment allow list, and the inventory of
// every environment name the core reads outside tests. The data lives in
// one untagged file so both builds see one copy: the demo serve refuses
// on the deny set, and the default-build inventory test
// (demoenv_inventory_test.go) holds this file to the source tree, so a
// new environment read fails the build until it is inventoried and
// classed here.

// demoEnvDenyPrefixes are the environment prefixes a demo box must not
// carry: the Plane feedback bridge, the BisTrack sync, the SMS provider,
// the assist provider and the live payment gateway. Any variable under
// one of them refuses the boot: the demo talks to nothing.
var demoEnvDenyPrefixes = []string{
	"PLANE_",
	"BISTRACK_",
	"TWILIO_",
	"HOS_PAYMENT_",
	"HOS_ASSIST_",
}

// demoEnvDenyNames are the single environment names refused outright on
// a demo box: the SMS provider selection, the live card processor's
// endpoint, the message bus and the statement notices switch.
var demoEnvDenyNames = []string{
	"SMS_PROVIDER",
	"HOS_CARD_BASE_URL",
	"NATS_URL",
	"HOS_STATEMENT_NOTICES",
}

// demoEnvCardProcessorVar and demoEnvCardProcessorValue: the card
// processor variable, whose every value except the simulator stub refuses
// the boot, so the demo's card rail can never be pointed at a live
// processor through the environment.
var (
	demoEnvCardProcessorVar   = "HOS_CARD_PROCESSOR"
	demoEnvCardProcessorValue = "stub"
)

// demoEnvAllowNames and demoEnvAllowPrefixes are the demo box's allow
// list, the classification vocabulary for the inventory below: a name the
// core reads is either refused on a demo box (the deny set above) or
// explicitly allowed to be present there. A name on no list fails the
// inventory test until someone classes it, which is how a new provider
// fails the build instead of shipping a wire out of the demo box.
var demoEnvAllowNames = []string{
	"ALLOW_SEED",
	"APP_ENV",
	"DATABASE_URL",
	"DEMO_MODE",
	"ERP_BASE_URL",
	"HH_CORE_TEST_DATABASE_URL",
	"JWKS_URL",
	"PAYMENT_VAULT_KEY",
	"PORT",
	"PORTAL_BASE_URL",
	"SMS_RATE_PER_MIN",
}

var demoEnvAllowPrefixes = []string{
	"CORE_",
	"HH_ACCEPTANCE_SIGN_",
	"SMS_WINDOW_",
}

// envInventoryFixture is the maintained inventory of every environment
// name the core reads outside tests, core code only, one entry per name.
// It is held to the source tree by demoenv_inventory_test.go, which walks
// every non-test Go file under the core module, resolves the environment
// readers' literal and constant arguments, and fails when a read is
// missing from this list or this list names a read that is gone. When a
// change adds or removes an environment read, this list changes in the
// same commit and every name on it must be classed: denied on the demo
// box, allowed on it, or the card processor's own rule.
var envInventoryFixture = []string{
	"ALLOW_SEED",
	"APP_ENV",
	"BISTRACK_API_KEY",
	"BISTRACK_ENDPOINT",
	"CORE_APPWRITE_API_KEY",
	"CORE_APPWRITE_ENDPOINT",
	"CORE_APPWRITE_PROJECT",
	"CORE_CLOCK_SKEW",
	"CORE_DATABASE_URL",
	"CORE_DEMO_TOKEN_TTL",
	"CORE_JWKS_CACHE_TTL",
	"CORE_JWKS_REFRESH_FLOOR",
	"CORE_JWKS_URL",
	"CORE_LISTEN_ADDR",
	"CORE_STAFF_AUDIENCE",
	"CORE_STREAM_TICKET_KEY",
	"CORE_TENANT_AUDIENCE",
	"CORE_TOKEN_ISSUER",
	"DATABASE_URL",
	"DEMO_MODE",
	"ERP_BASE_URL",
	"HH_ACCEPTANCE_SIGN_KEY",
	"HH_ACCEPTANCE_SIGN_KEY_ID",
	"HH_ACCEPTANCE_SIGN_KEY_PREVIOUS",
	"HH_ACCEPTANCE_SIGN_KEY_PREVIOUS_ID",
	"HOS_ASSIST_ANTHROPIC_API_KEY",
	"HOS_CARD_BASE_URL",
	"HOS_CARD_PROCESSOR",
	"HOS_PAYMENT_API_KEY",
	"HOS_PAYMENT_BASE_URL",
	"HOS_PAYMENT_GATEWAY",
	"HOS_PAYMENT_MERCHANT_ID",
	"HOS_PAYMENT_WEBHOOK_SECRET",
	"HOS_STATEMENT_NOTICES",
	"JWKS_URL",
	"NATS_URL",
	"PAYMENT_VAULT_KEY",
	"PLANE_API_KEY",
	"PLANE_API_URL",
	"PLANE_PROJECT_ID",
	"PLANE_WORKSPACE_SLUG",
	"PORT",
	"PORTAL_BASE_URL",
	"SMS_PROVIDER",
	"SMS_RATE_PER_MIN",
	"SMS_WINDOW_END_MIN",
	"SMS_WINDOW_START_MIN",
	"SMS_WINDOW_ZONE",
	"TWILIO_ACCOUNT_SID",
	"TWILIO_API_BASE",
	"TWILIO_AUTH_TOKEN",
	"TWILIO_FROM_NUMBER",
}
