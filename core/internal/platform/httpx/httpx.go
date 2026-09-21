// Package httpx holds the shared HTTP response helpers every module's handler
// uses, so JSON encoding + error-envelope shape live in exactly one place.
package httpx

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
)

// BadRequestError marks a client-input error that every handler maps to HTTP 400.
// It is the single, exported, errors.As-able bad-request type shared across all
// domain services and the portal BFF, so a client-validation error raised in any
// composed module is recognized the same way everywhere (review #13).
type BadRequestError struct{ Msg string }

func (e BadRequestError) Error() string { return e.Msg }

// BadRequestf builds a BadRequestError from a printf-style message.
func BadRequestf(format string, a ...any) error {
	return BadRequestError{Msg: fmt.Sprintf(format, a...)}
}

// WriteJSON writes v as JSON with the given status. HTML escaping is disabled so
// payloads with <, >, & round-trip byte-for-byte.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// modulePrefix is the `<module>: ` that every domain service prefixes its messages with ,
// "dispatch: ", "billing: ", "invcount: ". A single all-lowercase token, a colon, one space.
//
// It is deliberately anchored and deliberately narrow. `[a-z][a-z0-9]*` admits `gl`, `ar`, `pos`,
// `bankrec`, `landedcost` and every other module name in this monolith, and admits nothing with a
// space, a capital, a hyphen or an underscore in it, so it cannot reach into a sentence and take
// a clause. Sweeping the tree for `"<lowercase-token>: "` at the head of a message string finds
// module names and nothing else.
var modulePrefix = regexp.MustCompile(`^[a-z][a-z0-9]*: `)

// Error writes a `{"error": msg}` envelope with the given status.
//
// THE MODULE PREFIX IS STRIPPED HERE, AND ONLY HERE. Client-facing copy carries zero internal
// engineering vocabulary, and "dispatch:" is a fact about our package layout, it tells a counter
// clerk which of our directories was unhappy. The prefix is genuinely useful on the Go side: it
// makes an error greppable to its module, it survives wrapping, and handlers log the raw error
// separately. So it stays in the source and comes off at the one door it was never wanted behind.
//
// ONE PLACE, NOT FIVE HUNDRED. This package exists so "JSON encoding + error-envelope shape live in
// exactly one place" (the package doc above), and that is precisely what makes the rule enforceable:
// the alternative is editing 500-odd message literals and trusting every future lane to remember.
// A rule that depends on remembering is a rule that is already broken somewhere.
//
// IT STRIPS AND DOES NOT CAPITALIZE. Many messages begin with the identifier the caller got wrong
// ("branch_id is required", "commit_prompt_hours must be between 0 and 336"), and upper-casing the
// first letter of an identifier turns a name the reader can act on into one they cannot find.
func Error(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": CounterMessage(msg)})
}

// CounterMessage is the strip on its own, for the handful of envelopes that carry EXTRA FIELDS
// beside the message and therefore cannot go through Error. Exported so those sites share this
// treatment instead of quietly opting out of it, an envelope with a payload is still an envelope a
// person reads.
func CounterMessage(msg string) string { return modulePrefix.ReplaceAllString(msg, "") }

// ErrorWith writes an error envelope carrying additional fields alongside the message, the shape a
// few refusals need when the client must be handed the offending draft, block or policy back.
//
// It exists so "an envelope with extra fields" is not a reason to hand-roll a map literal and lose
// the counter-vocabulary treatment with it. `error` is written LAST and cannot be shadowed by a key
// in extra.
func ErrorWith(w http.ResponseWriter, status int, msg string, extra map[string]any) {
	body := make(map[string]any, len(extra)+1)
	for k, v := range extra {
		body[k] = v
	}
	body["error"] = CounterMessage(msg)
	WriteJSON(w, status, body)
}

// NotImplemented writes a 501 with the standard error envelope. A scaffolded route
// that has no behavior yet answers honestly (501) rather than fabricating data or
// 404-ing, so a parallel implementer can see exactly which seams are still stubs.
func NotImplemented(w http.ResponseWriter, msg string) {
	Error(w, http.StatusNotImplemented, msg)
}
