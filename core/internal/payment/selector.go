package payment

// selector.go, DIB-416 / D-RUNPAY-2 on the POS CARD rail (the other half; the contractor-portal rail
// is internal/payments/selector.go).
//
// TWO FLAGS, NOT ONE, AND THAT IS DELIBERATE. The portal rail and the counter card rail are different
// processors serving different humans, and a dealer piloting live cards at one counter while the
// portal stays on the deterministic stub is a real, sensible rollout. One shared flag would forbid
// that, so each rail owns its own.
//
// -- THE CONFIGURATION SURFACE --------------------------------------------------------------------
//
//	HOS_CARD_PROCESSOR   stub | run     default: stub
//	                     Which processor the POS card rail captures through.
//	HOS_CARD_BASE_URL                   Run host (required when mode=run). No default host is baked
//	                                    in: a typo in the mode flag must not be able to reach a real
//	                                    processor.
//	APP_ENV              dev | staging | production
//
// CREDENTIALS ARE NOT IN THIS FLAG SET, and that difference from the portal rail matters. The card
// rail's api_key and mid are PER-DEALER and already live sealed in payment_config (0027, AES-256-GCM
// via the vault), resolved per call. Re-declaring them as environment variables would create a second
// source of truth for a credential, the failure mode being a box that boots happily on an env key
// while the sealed row says something else. So the boot guard here checks that the rail's
// CONFIGURATION SOURCE is usable, and the per-dealer secret stays exactly where it already is.
//
// The four rules are identical to the portal rail's, for identical reasons: unset → stub; run without
// its host → refuse; production on a simulated rail → refuse; unknown mode → refuse. The one worth
// re-reading is production-on-simulated: a simulated card rail at a live counter does not lose money,
// it fails to TAKE it, while printing receipts that say it did.

import (
	"fmt"
	"os"
	"strings"
)

// Card rail modes for HOS_CARD_PROCESSOR.
const (
	CardModeStub = "stub"
	CardModeRun  = "run"
)

// Environment variable names, in one place so the doc and the code cannot drift.
const (
	EnvCardMode    = "HOS_CARD_PROCESSOR"
	EnvCardBaseURL = "HOS_CARD_BASE_URL"
	EnvAppEnv      = "APP_ENV"
)

// CardRailConfig is the resolved selection input, a plain struct, so the boot guard is testable at
// the CONSTRUCTOR with no environment and no process.
type CardRailConfig struct {
	Mode    string
	BaseURL string
	Env     string
}

// LoadCardRailConfig reads the selection from the environment. Absent mode → stub.
func LoadCardRailConfig() CardRailConfig {
	return CardRailConfig{
		Mode:    strings.ToLower(strings.TrimSpace(os.Getenv(EnvCardMode))),
		BaseURL: os.Getenv(EnvCardBaseURL),
		Env:     strings.ToLower(strings.TrimSpace(os.Getenv(EnvAppEnv))),
	}
}

// SelectCardProcessor is the flag and the guard for the POS card rail. The returned Processor is the
// single constructor argument NewService takes, so everything downstream of the flip, Simulated(),
// the receipt's processor ref, the dispatch, follows from this one value.
func SelectCardProcessor(cfg CardRailConfig) (Processor, error) {
	mode := cfg.Mode
	if mode == "" {
		mode = CardModeStub
	}

	var p Processor
	simulated := false
	switch mode {
	case CardModeStub:
		p, simulated = RunPaymentsStub{}, true
	case CardModeRun:
		if cfg.BaseURL == "" {
			return nil, fmt.Errorf("payment: %s=run but %s is not set, refusing to boot a card rail that "+
				"claims to be live and has nowhere to send a charge (DIB-416)", EnvCardMode, EnvCardBaseURL)
		}
		p = NewRunPaymentsClient(cfg.BaseURL)
	default:
		return nil, fmt.Errorf("payment: unknown %s=%q (want %q or %q), refusing to guess which processor "+
			"a card should be charged through", EnvCardMode, cfg.Mode, CardModeStub, CardModeRun)
	}

	if cfg.Env == "production" && simulated {
		return nil, fmt.Errorf("payment: %s=production with the simulated card rail, refusing to boot. "+
			"A simulated rail at a live counter prints receipts for money it never took (DIB-416)", EnvAppEnv)
	}
	return p, nil
}
