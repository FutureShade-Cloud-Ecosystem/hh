package assist

// secret.go, the one type every credential in this package lives in (TanyAI slice 1 round 2,
// review P2-1). A key held in a Secret prints as whether it is set and nothing else, under every
// fmt verb and as JSON or text, so a log line, an error, a struct dump or a debug endpoint cannot
// carry it. The only way out is Reveal, called at the point a header is written.

import "fmt"

// Secret is a credential value.
type Secret string

const (
	secretSet   = "[secret set]"
	secretUnset = "[secret unset]"
)

// String reports presence only.
func (s Secret) String() string {
	if s == "" {
		return secretUnset
	}
	return secretSet
}

// GoString covers %#v.
func (s Secret) GoString() string { return s.String() }

// Format covers every verb (%v, %+v, %#v, %s, %q, %x, %d ...).
func (s Secret) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(s.String())) }

// MarshalJSON and MarshalText keep the value out of any encoder.
func (s Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + s.String() + `"`), nil }
func (s Secret) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// Set reports whether a value is present.
func (s Secret) Set() bool { return s != "" }

// Reveal returns the value. Call it only where a request header is written.
func (s Secret) Reveal() string { return string(s) }
