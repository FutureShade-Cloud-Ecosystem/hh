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
// in demoEnvPlaneBridgeAllowNames and the two exact assistant names in
// demoEnvAssistAllowNames below: the demo talks to nothing else.
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

// demoEnvAssistAllowNames is the second exception to the deny prefixes
// (TanyAI slice 3, row 2496: the demo runs a real Claude model). The
// assistant was refused on a demo box while it could only be stubbed; it is
// admitted now because slices 1 and 2 are on main and make it safe to carry,
// and only on these conditions, each held by a guard of its own: the two
// exact names below and no other HOS_ASSIST_ name (the gateway pair and the
// dealer tag stay refused, so the demo reaches the vendor directly through
// the one native alias and no gateway); a key is never carried without a
// positive dealer cap (boot.RefuseAssistKeyWithoutCap); the wired service
// carries the demo's per visitor, per address and box wide limits; a paid
// call with no price row is refused by the meter and the boot refuses a key
// whose model has no row; with no key or the cap reached the demo answers
// on the stub and says so. The values come only from FB Deploy's
// environment sourced from FB Vault (prod /hh-demo): never this
// repository, never the image, never a fixture.
var demoEnvAssistAllowNames = []string{
	"HOS_ASSIST_ANTHROPIC_API_KEY",
	"HOS_ASSIST_DEALER_CAP_MICROCENTS",
}

// demoEnvPrefixExceptions is every exact name a deny prefix lifts: the
// feedback bridge's five and the assistant's two.
func demoEnvPrefixExceptions() []string {
	return append(append([]string(nil), demoEnvPlaneBridgeAllowNames...), demoEnvAssistAllowNames...)
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
	// The finance box mark (W2-2): it names a deployment kind a demo box
	// never is, so a demo box carrying it refuses the boot.
	"HH_BOX_KIND",
	// The evening guard's simulated clock shift (W2-1b): a demo box tells
	// the time, it does not move it, so a demo box carrying a shift refuses
	// the boot.
	"HH_CLOCK_SHIFT_SECONDS",
	// The finance box's people (W2-2): they are the finance box's, a demo
	// box has none, and a demo box carrying the list refuses the boot.
	"HH_FINANCE_USERS",
	// The finance world's dump names (W2-1b): the file, the address, the
	// digest and the fetch credential of the world the finance box restores.
	// A demo box never restores a world, so a demo box carrying any of them
	// refuses the boot, and the credential never rides an ordinary
	// deployment.
	"HH_FINANCE_WORLD_FILE",
	"HH_FINANCE_WORLD_URL",
	"HH_FINANCE_WORLD_SHA256",
	"HH_FINANCE_WORLD_TOKEN",
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
	// the image's entrypoint (deploy/demo/write-web-config.sh); HH_WEB_ORG is
	// also read by the core's finance one-shot (the org anchor's slug), so
	// it carries the inventory entry above beside this allow entry.
	"HH_WEB_CONSOLE_ENDPOINT",
	"HH_WEB_CONSOLE_PROJECT_ID",
	"HH_WEB_CORE_BASE_URL",
	"HH_WEB_DEALER_MARK",
	// The world's dealer name is the finance one-shot's plan dealer, the
	// same public fact the web runtime carries.
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
	"HH_BOX_KIND",
	// The evening guard's simulated clock shift (W2-1b): whole seconds added to every time.Now in
	// the world's day processes. A demo box carries no shifted clock.
	"HH_CLOCK_SHIFT_SECONDS",
	"HH_DEMO_MINT_ADDRESS_CEILING",
	"HH_DEMO_MINT_VISITOR_ALLOWANCE",
	"HH_FINANCE_USERS",
	// The world's dealer name (W2-1b): the finworld driver's plan dealer, read beside HH_WEB_ORG's
	// finance one-shot use.
	"HH_WEB_DEALER_NAME",
	"HH_WEB_ORG",
	"HH_ACCEPTANCE_SIGN_KEY",
	"HH_ACCEPTANCE_SIGN_KEY_ID",
	"HH_ACCEPTANCE_SIGN_KEY_PREVIOUS",
	"HH_ACCEPTANCE_SIGN_KEY_PREVIOUS_ID",
	"HH_FINANCE_WORLD_FILE",
	"HH_FINANCE_WORLD_SHA256",
	"HH_FINANCE_WORLD_TOKEN",
	"HH_FINANCE_WORLD_URL",
	"HOS_ASSIST_ANTHROPIC_API_KEY",
	"HOS_ASSIST_CLAIMS_KEY",
	"HOS_ASSIST_PRO_READER_DATABASE_URL",
	"HOS_ASSIST_PRO_READER_PASSWORD",
	"HOS_ASSIST_READER_DATABASE_URL",
	"HOS_ASSIST_READER_PASSWORD",
	"HOS_ASSIST_DEALER_CAP_MICROCENTS",
	"HOS_ASSIST_DEALER_TAG",
	"HOS_ASSIST_GATEWAY_KEY",
	"HOS_ASSIST_GATEWAY_URL",
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
