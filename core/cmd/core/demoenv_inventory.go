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
