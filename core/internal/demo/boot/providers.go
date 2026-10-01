//go:build demo

package boot

import (
	"fmt"
	"strings"
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
	// Names are refused outright.
	Names []string
	// CardVar is the card processor selection variable.
	CardVar string
	// CardAllowed is the one value CardVar may carry: the simulator stub.
	CardAllowed string
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
		for _, prefix := range deny.Prefixes {
			if strings.HasPrefix(name, prefix) {
				return fmt.Errorf("%s is set: the demo box carries no outbound provider, so a variable under %q stops the boot", name, prefix)
			}
		}
		for _, refused := range deny.Names {
			if name == refused {
				return fmt.Errorf("%s is set: the demo box carries no outbound provider, so this variable stops the boot", name)
			}
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
