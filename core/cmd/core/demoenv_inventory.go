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
// one of them refuses the boot, except the five exact Plane bridge names
// listed in demoEnvPlaneBridgeAllowNames below: the demo talks to nothing
// else.
var demoEnvDenyPrefixes = []string{
	"PLANE_",
	"BISTRACK_",
	"TWILIO_",
	"HOS_PAYMENT_",
	"HOS_ASSIST_",
}

// demoEnvPlaneBridgeAllowNames is the one exception to the deny prefixes
// (the Feature Polish ruling): the feedback bridge is the one outbound a
// demo box may carry, and only these five exact names pass the gate. Every
// other PLANE_ name still refuses the boot, and the values come only from
// FB Deploy's environment sourced from FB Vault: never this repository,
// never the image, never a fixture.
var demoEnvPlaneBridgeAllowNames = []string{
	"PLANE_API_URL",
	"PLANE_API_KEY",
	"PLANE_PROJECT_ID",
	"PLANE_WORKSPACE_SLUG",
	"PLANE_FEEDBACK_MODULE",
}

// demoEnvDenyNames are the single environment names refused outright on
// a demo box: the SMS provider selection, the live card processor's
// endpoint, the message bus, the statement notices switch, and the load
// doors switch (the doors are a dealer deployment's decision, never a
// demo box's; the machine credential names beside the switch are classed
// allowed, the doors off relaxation ignores them with one warning).
var demoEnvDenyNames = []string{
	"SMS_PROVIDER",
	"HOS_CARD_BASE_URL",
	"NATS_URL",
	"HOS_STATEMENT_NOTICES",
	"CORE_LOAD_DOORS",
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
	"ALLOW_DEV_DEFAULT_DB",
	"ALLOW_SEED",
	"APP_ENV",
	"DATABASE_URL",
	"DEALER_NAME",
	"DEMO_MODE",
	"ERP_BASE_URL",
	"HH_CORE_TEST_DATABASE_URL",
	"HH_DEMO_MINT_ADDRESS_CEILING",
	"HH_DEMO_MINT_VISITOR_ALLOWANCE",
	// The HH_WEB_ names are the web runtime config's public facts, read by
	// the image's entrypoint (deploy/demo/write-web-config.sh) and never by
	// the core, so they are allowed here and are not in the inventory below.
	"HH_WEB_CONSOLE_ENDPOINT",
	"HH_WEB_CONSOLE_PROJECT_ID",
	"HH_WEB_CORE_BASE_URL",
	"HH_WEB_DEALER_MARK",
	"HH_WEB_DEALER_NAME",
	"HH_WEB_MODE",
	"HH_WEB_ORG",
	"HH_WEB_PERSONAS",
	"HH_WEB_PERSONAS_OPEN",
	"HH_WEB_PERSONA_CALLER",
	"JWKS_URL",
	"PAYMENT_VAULT_KEY",
	"PLANE_FEEDBACK_MODULE",
	"PORT",
	"PORTAL_BASE_URL",
	"SMS_RATE_PER_MIN",
}

var demoEnvAllowPrefixes = []string{
	"HH_ACCEPTANCE_SIGN_",
	"SMS_WINDOW_",
}

// demoEnvExplicitPrefix is the prefix the demo serve no longer allows
// wholesale: a CORE_ name is allowed on a demo box only when it is on
// demoEnvAllowCoreNames, so a new name under it fails the boot until it is
// stated by name here (the demo environment guard, G4's blind spot closed).
const demoEnvExplicitPrefix = "CORE_"

// demoEnvAllowCoreNames are the CORE_ names a demo box may carry. The
// console sign in mode is a declared shape of a demo image: the key file
// names (secret files, paths only), the FB Console connection (the one
// outbound a console mode box makes, to FB Console and nothing else), the
// issuer, the exchange windows, the allow lists, the trusted proxy list and
// the JWT bound. CORE_LOAD_DOORS is not here: it is denied by name.
var demoEnvAllowCoreNames = []string{
	"CORE_APPWRITE_API_KEY",
	"CORE_APPWRITE_ENDPOINT",
	"CORE_APPWRITE_PROJECT",
	"CORE_CLOCK_SKEW",
	"CORE_DATABASE_URL",
	"CORE_DEMO_TOKEN_TTL",
	"CORE_EXCHANGE_JWT_BOUND",
	"CORE_EXCHANGE_TOKEN_TTL",
	"CORE_JWKS_CACHE_TTL",
	"CORE_JWKS_REFRESH_FLOOR",
	"CORE_JWKS_URL",
	"CORE_LISTEN_ADDR",
	"CORE_MACHINE_AUDIENCE",
	"CORE_MACHINE_JWKS_URL",
	"CORE_PERSONA_CALLER",
	"CORE_PERSONA_PARTNERS",
	"CORE_SESSION_KEY_NEXT_FILE",
	"CORE_SESSION_KEY_PREVIOUS_FILE",
	"CORE_SESSION_SIGNING_KEY_FILE",
	"CORE_SIGNIN_EMAIL_ADDRESSES",
	"CORE_SIGNIN_EMAIL_DOMAINS",
	"CORE_SIGNIN_MODE",
	"CORE_STAFF_AUDIENCE",
	"CORE_STREAM_TICKET_KEY",
	"CORE_TENANT_AUDIENCE",
	"CORE_TOKEN_ISSUER",
	"CORE_TRUSTED_PROXIES",
}

// envInventoryFixture is the maintained inventory of every environment
// name the core reads outside tests, core code only, one entry per name.
// It is held to the source tree by demoenv_inventory_test.go, which walks
// every non-test Go file under the core module, resolves the environment
// readers' literal and constant arguments, and fails when a read is
// missing from this list or the list names a read that is gone. The
// scanner sees a reader shaped as Getenv or LookupEnv under any receiver
// or import alias, a helper or field or bare name in its reader set, with
// a literal or package-constant argument; it does not follow a reader
// passed under a name outside that set, a computed name, or an interface
// of another shape, and the names read only through pinned dynamic sites
// (main.go's literal table of the verifier timing knobs) are kept in the
// test's envInventoryDynamicNames beside it. When a change adds or
// removes an environment read, this list changes in the same commit and
// every name on it must be classed: denied on the demo box, allowed on
// it, or the card processor's own rule.
var envInventoryFixture = []string{
	"ALLOW_DEV_DEFAULT_DB",
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
	"CORE_EXCHANGE_JWT_BOUND",
	"CORE_EXCHANGE_TOKEN_TTL",
	"CORE_JWKS_CACHE_TTL",
	"CORE_JWKS_REFRESH_FLOOR",
	"CORE_JWKS_URL",
	"CORE_LISTEN_ADDR",
	"CORE_LOAD_DOORS",
	"CORE_MACHINE_AUDIENCE",
	"CORE_MACHINE_JWKS_URL",
	"CORE_PERSONA_CALLER",
	"CORE_PERSONA_PARTNERS",
	"CORE_SESSION_KEY_NEXT_FILE",
	"CORE_SESSION_KEY_PREVIOUS_FILE",
	"CORE_SESSION_SIGNING_KEY_FILE",
	"CORE_SIGNIN_EMAIL_ADDRESSES",
	"CORE_SIGNIN_EMAIL_DOMAINS",
	"CORE_SIGNIN_MODE",
	"CORE_STAFF_AUDIENCE",
	"CORE_STREAM_TICKET_KEY",
	"CORE_TENANT_AUDIENCE",
	"CORE_TOKEN_ISSUER",
	"CORE_TRUSTED_PROXIES",
	"DATABASE_URL",
	"DEALER_NAME",
	"DEMO_MODE",
	"ERP_BASE_URL",
	"HH_DEMO_MINT_ADDRESS_CEILING",
	"HH_DEMO_MINT_VISITOR_ALLOWANCE",
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
	"PLANE_FEEDBACK_MODULE",
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
