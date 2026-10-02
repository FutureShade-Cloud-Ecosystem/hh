//go:build demo

package demo

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

// The visitor id (the comprehensive review's P3-8 answer): behind a host
// edge every connection arrives from the edge itself, so the address the
// mint limit keys on is one bucket shared by every tester behind it. The
// flows GET, the call every entry page load makes before any card is
// tapped, issues a cookie when the request carries none, and the mint
// keys on that cookie when it holds a well formed one: one id per
// browser, so a team tapping in together holds separate allowances
// behind the one shared address.
//
// The id carries the address discipline's own privacy posture (limit.go):
// it is a random value with no meaning, keyed on and forgotten with the
// window, never written to the database, never logged, never rendered.
// It is taken as a key only in exactly the shape the server issues (32
// lower case hex characters). A forged or farmed value that meets the
// shape does get a bucket of its own; what such a value cannot escape is
// the address ceiling beside the mint limit, which ends the farm at the
// address's own allowance however many ids a script mints.
const (
	visitorCookieName = "demo_visitor"
	// visitorCookieMaxAge is the cookie's Max-Age: the id lives for one
	// testing visit and rotates after, long enough to carry a tester's
	// whole flow (sign-in, pop-outs, manager approvals) and short enough
	// that the ids do not pile up in a browser's jar.
	visitorCookieMaxAge = 86400
)

// newVisitorID returns a fresh id: 128 random bits spelled as 32 lower
// case hex characters. The error branch below is defensive only and is
// never exercised on this toolchain: rand.Read gives up the process
// rather than hand back an error, so the fallback it guards (no cookie,
// the mints on the address key) is paper shape, not a tested behaviour.
func newVisitorID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// visitorIDFrom reads the request's key contribution: the cookie's value
// when it is a well formed id, and nothing otherwise. The value is the
// client's word, so only the exact issued shape is taken.
func visitorIDFrom(r *http.Request) string {
	c, err := r.Cookie(visitorCookieName)
	if err != nil || !wellFormedVisitorID(c.Value) {
		return ""
	}
	return c.Value
}

// wellFormedVisitorID reports whether the value is the issued shape: 32
// lower case hex characters, nothing shorter, nothing longer, nothing
// else. Upper case is refused along with everything else that is not the
// issued spelling: one id spelled in two cases would be two buckets.
func wellFormedVisitorID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// setVisitorCookie issues the id when the request carries no well formed
// one: a jar holding a value the key would refuse gets a fresh issue, so
// that browser recovers onto its own bucket instead of riding the shared
// address key. A request that already carries a well formed id keeps it:
// the id is stable for the browser, not rotated per call. No Secure
// attribute: the demo box serves plain http, and a Secure attribute
// would stop browsers setting the cookie over http, so it stays off;
// behind an https edge the cookie rides the edge's scheme fine.
func setVisitorCookie(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(visitorCookieName); err == nil && wellFormedVisitorID(c.Value) {
		return
	}
	id, err := newVisitorID()
	if err != nil {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     visitorCookieName,
		Value:    id,
		Path:     "/",
		MaxAge:   visitorCookieMaxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}
