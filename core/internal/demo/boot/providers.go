//go:build demo

package boot

import (
	"fmt"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
)

// ProviderDeny is the demo box's outbound-provider deny set (G4): the
// prefixes and single names a demo box must not carry, and the card
// processor variable whose every value except the simulator stub refuses
// the boot. The deny set arrives by value from the serve wiring, where it
// is the one copy the default-build inventory test also holds the source
// tree to, so the runtime refusal and the build-time inventory cannot
// drift apart unnoticed.
type ProviderDeny struct {
	// Prefixes are refused on any variable whose name starts with one.
	Prefixes []string
	// PrefixExceptions are the exact names a prefix refusal lifts, one
	// outbound at a time: the feedback bridge's own variables (the Feature
	// Polish ruling) pass the gate while every other name under the same
	// prefix still refuses. An exception lifts only the prefix clause: a
	// name also carried by Names stays refused, and no exception ever
	// touches the card processor's own rule.
	PrefixExceptions []string
	// Names are refused outright.
	Names []string
	// CardVar is the card processor selection variable.
	CardVar string
	// CardAllowed is the one value CardVar may carry: the simulator stub.
	CardAllowed string
	// ExplicitPrefix, when set, makes every variable under it refused
	// unless its exact name is in ExplicitAllowed: the prefix is no longer
	// allowed wholesale, so a future name under it fails the boot until
	// someone states it. Empty means no such rule.
	ExplicitPrefix  string
	ExplicitAllowed []string
}

// RefuseProviderVars refuses an environment picture that carries any
// outbound provider: the demo talks to nothing, and a provider value set
// on the box is a wire out of the demo that must not exist. The environ
// is the os.Environ shape, one NAME=VALUE entry per element; only names
// are inspected, no value is ever copied into an error.
func RefuseProviderVars(environ []string, deny ProviderDeny) error {
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if name == "" {
			continue
		}
		excepted := false
		for _, allowed := range deny.PrefixExceptions {
			if name == allowed {
				excepted = true
				break
			}
		}
		for _, prefix := range deny.Prefixes {
			if !excepted && strings.HasPrefix(name, prefix) {
				return fmt.Errorf("%s is set: the demo box carries no outbound provider, so a variable under %q stops the boot", name, prefix)
			}
		}
		for _, refused := range deny.Names {
			if name == refused {
				return fmt.Errorf("%s is set: the demo box carries no outbound provider, so this variable stops the boot", name)
			}
		}
		if deny.ExplicitPrefix != "" && strings.HasPrefix(name, deny.ExplicitPrefix) && !contains(deny.ExplicitAllowed, name) {
			return fmt.Errorf("%s is set but is on no list: a name under %q must be stated by name before a demo box may carry it", name, deny.ExplicitPrefix)
		}
		if name == deny.CardVar {
			_, value, _ := strings.Cut(kv, "=")
			if value != deny.CardAllowed {
				return fmt.Errorf("%s is %q, want %q or unset: the demo's card rail is the simulator and can never be pointed at a live processor", name, value, deny.CardAllowed)
			}
		}
	}
	return nil
}

func contains(list []string, name string) bool {
	for _, e := range list {
		if e == name {
			return true
		}
	}
	return false
}

// RefuseAssistKeyWithoutCap is the assistant's cap condition (TanyAI slice
// 3): a demo box that carries the assistant's key must also carry a positive
// dealer cap, or the boot stops. The cap is FutureBuild's hard bound on the
// demo's spend (row 2496); a key with no cap, a zero, a negative number or a
// value that is not a whole number would spend with nothing above it. The
// message names the cap's variable and never the key's value. With no key
// there is nothing to bound: the demo serves the stub.
func RefuseAssistKeyWithoutCap(getenv func(string) string) error {
	if strings.TrimSpace(getenv(assist.EnvAPIKey)) == "" {
		return nil
	}
	if assist.ParseDealerCap(getenv(assist.EnvDealerCap)) <= 0 {
		return fmt.Errorf("%s is missing or not a positive whole number of micro cents: the demo box carries the assistant's key, and a key is never carried without the dealer cap that bounds its spend", assist.EnvDealerCap)
	}
	return nil
}
