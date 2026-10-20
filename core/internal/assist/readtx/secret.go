package readtx

import (
	"fmt"
	"log/slog"
)

// Secret holds a credential (a database URL with its password, the claims
// signing key, a role password) so that no formatting path can print it: the
// fmt verbs, JSON, text marshalling and a slog attribute all print a fixed
// mask. The value comes out only through Reveal, which the few call sites that
// need the raw bytes use and nothing logs.
//
// The value is held behind a closure, not a string field: fmt cannot call
// methods on an unexported struct field, so a struct that holds a Secret (or a
// pointer to one) in an unexported field would print a string field raw under
// %v, %+v, %#v and even a bad verb. A function value prints as an address.
type Secret struct{ get func() string }

const secretMask = "[redacted]"

func NewSecret(v string) Secret {
	if v == "" {
		return Secret{}
	}
	return Secret{get: func() string { return v }}
}

// Reveal returns the raw value. Callers must not log, format or store it.
func (s Secret) Reveal() string {
	if s.get == nil {
		return ""
	}
	return s.get()
}

func (s Secret) IsEmpty() bool { return s.get == nil }

func (Secret) String() string   { return secretMask }
func (Secret) GoString() string { return secretMask }

// Format covers every verb, including %x and %d, which fmt would otherwise apply
// to the underlying string.
func (Secret) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(secretMask)) }

func (Secret) MarshalText() ([]byte, error) { return []byte(secretMask), nil }
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + secretMask + `"`), nil }
func (Secret) LogValue() slog.Value         { return slog.StringValue(secretMask) }
