// Package redact holds the one credential type of the assistant: a Secret prints as
// whether it is set and nothing else, under every fmt verb, as JSON or text and as a
// slog value. Every key, database URL, password and signed claim in package assist and
// package readtx lives in one.
package redact

import (
	"fmt"
	"log/slog"
)

// Secret holds a credential (a database URL with its password, the claims
// signing key, a role password, a signed claim) so that no formatting path can
// print it: the fmt verbs, JSON, text marshalling and a slog attribute all print
// only whether it is set. The value comes out only through Reveal, which the few
// call sites that need the raw bytes use and nothing logs.
//
// The value sits behind a pointer, not in a string field: fmt cannot call
// methods on an unexported struct field, so a struct that holds a Secret (or a
// pointer to one) unexported would print a string field raw under %v, %+v, %#v
// and even a bad verb. A pointer field prints as an address at any depth.
type Secret struct{ v *string }

const (
	secretSet   = "[secret set]"
	secretUnset = "[secret unset]"
)

func NewSecret(v string) Secret {
	if v == "" {
		return Secret{}
	}
	return Secret{v: &v}
}

// Reveal returns the raw value. Callers must not log, format or store it.
func (s Secret) Reveal() string {
	if s.v == nil {
		return ""
	}
	return *s.v
}

func (s Secret) IsEmpty() bool { return s.v == nil }

// Set reports whether a value is present.
func (s Secret) Set() bool { return s.v != nil }

func (s Secret) String() string {
	if s.v == nil {
		return secretUnset
	}
	return secretSet
}
func (s Secret) GoString() string { return s.String() }

// Format covers every verb, including %x and %d, which fmt would otherwise apply
// to the underlying value.
func (s Secret) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(s.String())) }

func (s Secret) MarshalText() ([]byte, error) { return []byte(s.String()), nil }
func (s Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + s.String() + `"`), nil }
func (s Secret) LogValue() slog.Value         { return slog.StringValue(s.String()) }
