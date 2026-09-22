package payments

// selector.go, DIB-416 / D-RUNPAY-2: the flag that decides which processor the portal rail talks to,
// and the fail-closed boot guard that refuses to start a production box on the wrong one.
//
// WHAT WAS MISSING. D-RUNPAY-2 was recorded as a decision, "ship on the deterministic stub, flip to
// the live gateway by config", and Wave 2 proved the SEAM was flip-ready (one constructor argument;
// everything downstream follows). What it could not prove was the flip itself, because there was no
// flag: cmd/server/main.go hardcoded payments.NewStubGateway(). Separately, the PaymentGateway port
// doc has always promised "the boot path must fail closed in prod, refusing to register payment
// routes unless a LIVE gateway + webhook secret resolve", and nothing implemented it.
//
// - THE CONFIGURATION SURFACE (this is the documented contract) -
//
//	HOS_PAYMENT_GATEWAY   stub | run     default: stub
//	                      Which processor the CONTRACTOR PORTAL rail charges through.
//	HOS_PAYMENT_API_KEY                  the Run api_key (required when mode=run)
//	HOS_PAYMENT_MERCHANT_ID              the Run mid       (required when mode=run)
//	HOS_PAYMENT_BASE_URL                 Run host override (required when mode=run, there is no
//	                                     default production host baked in, deliberately: a typo in a
//	                                     mode flag must not be able to reach a real processor)
//	HOS_PAYMENT_WEBHOOK_SECRET           required when mode=run (the port doc's second condition)
//	APP_ENV               dev | staging | production   (read, not owned, by this package)
//
// The POS card rail has its own flag with the same shape, see internal/payment/selector.go. They are
// separate on purpose: a dealer piloting live cards at one counter while the portal stays on the stub
// is a real rollout plan, and one flag for both would forbid it.
//
// - THE FOUR RULES, AND WHY EACH IS THE WAY IT IS -
//
//  1. UNSET → STUB. The default must be the thing that cannot move money. Everything about this file
//     is downstream of that.
//
//  2. mode=run WITHOUT credentials → REFUSE TO BOOT. Not "warn and fall back to stub". A box told to
//     be live and unable to be live is misconfigured, and the failure mode of falling back is that
//     payments silently stop being real while every screen says they worked.
//
//  3. APP_ENV=production ON A NON-LIVE GATEWAY → REFUSE TO BOOT. This is the port doc's promise, and
//     it is the rule most likely to be argued with, so: the danger is not that a stub in production
//     loses money, it is that it CANNOT. Invoices settle, receipts print, the AR ages down, and no
//     money has moved. That is discovered at a bank reconciliation weeks later, against a month of
//     transactions. Refusing to boot converts a silent month into a loud minute. There is no
//     environment-variable escape hatch, because an escape hatch is the thing that gets set during an
//     incident and never unset.
//
//  4. AN UNRECOGNISED MODE → REFUSE. A typo'd flag falling back to the stub would be safe money-wise
//     and wrong operationally: it hides a config error behind correct-looking behaviour.
//
// Nothing here reads a credential from source. All values come from the environment, and the guard
// checks only whether they are PRESENT, never what they contain, and it never logs them.

import (
	"fmt"
	"os"
	"strings"
)

// Gateway modes for HOS_PAYMENT_GATEWAY.
const (
	ModeStub = "stub"
	ModeRun  = "run"
)

// Environment variable names, in one place so the doc above and the code cannot drift apart.
const (
	EnvGatewayMode    = "HOS_PAYMENT_GATEWAY"
	EnvGatewayAPIKey  = "HOS_PAYMENT_API_KEY"
	EnvGatewayMID     = "HOS_PAYMENT_MERCHANT_ID"
	EnvGatewayBaseURL = "HOS_PAYMENT_BASE_URL"
	EnvGatewaySecret  = "HOS_PAYMENT_WEBHOOK_SECRET"
	EnvAppEnv         = "APP_ENV"
)

// GatewayConfig is the resolved selection input. It is a plain struct so the boot guard is testable
// at the CONSTRUCTOR, with no environment and no process to start.
type GatewayConfig struct {
	Mode          string
	APIKey        string
	MerchantID    string
	BaseURL       string
	WebhookSecret string
	Env           string
}

// LoadGatewayConfig reads the selection from the environment. Absent mode → stub.
func LoadGatewayConfig() GatewayConfig {
	return GatewayConfig{
		Mode:          strings.ToLower(strings.TrimSpace(os.Getenv(EnvGatewayMode))),
		APIKey:        os.Getenv(EnvGatewayAPIKey),
		MerchantID:    os.Getenv(EnvGatewayMID),
		BaseURL:       os.Getenv(EnvGatewayBaseURL),
		WebhookSecret: os.Getenv(EnvGatewaySecret),
		Env:           strings.ToLower(strings.TrimSpace(os.Getenv(EnvAppEnv))),
	}
}

// SelectGateway is the flag, the guard and the only construction site for the portal rail's
// processor. It returns an error rather than panicking so main can decide how to die; every caller
// is expected to treat the error as fatal.
func SelectGateway(cfg GatewayConfig) (PaymentGateway, error) {
	mode := cfg.Mode
	if mode == "" {
		mode = ModeStub
	}

	var gw PaymentGateway
	switch mode {
	case ModeStub:
		gw = NewStubGateway()
	case ModeRun:
		// Rule 2: every credential the live rail needs must be present BEFORE we hand back something
		// that claims to be live. Missing ones are named together so an operator fixes the config in
		// one pass instead of discovering them one restart at a time.
		var missing []string
		if cfg.APIKey == "" {
			missing = append(missing, EnvGatewayAPIKey)
		}
		if cfg.MerchantID == "" {
			missing = append(missing, EnvGatewayMID)
		}
		if cfg.BaseURL == "" {
			missing = append(missing, EnvGatewayBaseURL)
		}
		if cfg.WebhookSecret == "" {
			missing = append(missing, EnvGatewaySecret)
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("payments: %s=run but %s not set, refusing to boot a rail that "+
				"claims to be live and cannot be (DIB-416)", EnvGatewayMode, strings.Join(missing, ", "))
		}
		gw = NewRunGateway(cfg.BaseURL, cfg.APIKey, cfg.MerchantID)
	default:
		// Rule 4.
		return nil, fmt.Errorf("payments: unknown %s=%q (want %q or %q), refusing to guess which "+
			"processor a payment should go to", EnvGatewayMode, cfg.Mode, ModeStub, ModeRun)
	}

	// Rule 3, applied LAST so it holds no matter which branch produced the gateway. Asking the
	// gateway itself (Live()) rather than re-reading the mode string means a future processor that
	// forgets to report itself live cannot slip past this.
	if cfg.Env == "production" && !gw.Live() {
		return nil, fmt.Errorf("payments: %s=production with a non-live gateway (%q), refusing to boot. "+
			"A stub in production does not lose money, it fails to MOVE it, and that is discovered at a "+
			"bank reconciliation weeks later (DIB-416)", EnvAppEnv, gw.Name())
	}
	return gw, nil
}
