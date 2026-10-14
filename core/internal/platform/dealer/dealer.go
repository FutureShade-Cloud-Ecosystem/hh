// Package dealer holds the one dealer name a running box shows a person.
//
// Every string a person reads (an email, a text, an export workbook, a filter label, the assistant's
// brief) reads the dealer's own configured name through here, and falls back to a neutral role word
// when none is set. No string may carry a dealer's name in code: a box must never show one dealer's
// name on another's (decision log rows 1592, 1605).
//
// The name is set once at boot from the DEALER_NAME configuration (cmd/core, after config.Load) and
// read everywhere else. It is process wide because the core serves one dealer per box.
package dealer

import (
	"strings"
	"sync/atomic"
)

var name atomic.Value // string

// Set records the dealer's configured name. Call it once at boot, before serving. Surrounding space
// is trimmed; an empty or blank name clears it, so every reader falls back to its neutral word.
func Set(n string) {
	name.Store(strings.TrimSpace(n))
}

// Name is the configured dealer name, or "" when none is set.
func Name() string {
	n, _ := name.Load().(string)
	return n
}

// Or is the configured dealer name, or fallback (a neutral role word such as "your dealer", never
// another dealer's name) when none is set.
func Or(fallback string) string {
	if n := Name(); n != "" {
		return n
	}
	return fallback
}
