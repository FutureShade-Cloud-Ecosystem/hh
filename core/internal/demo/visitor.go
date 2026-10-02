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
// It is taken as a key only in exactly the shape the server issues, so a
// client chosen value buys no bucket of its own, and the address ceiling
// beside the mint limit bounds a script that farms ids anyway.
const (
	visitorCookieName = "demo_visitor"
	// visitorCookieMaxAge is the cookie's Max-Age: the id lives for one
	// testing visit and rotates after, long enough to carry a tester's
	// whole flow (sign-in, pop-outs, manager approvals) and short enough
	// that the ids do not pile up in a browser's jar.
	visitorCookieMaxAge = 86400
)

// newVisitorID returns a fresh id: 128 random bits spelled as 32 hex
// characters. A box whose randomness is broken issues no cookie, and the
// mints fall back to the address key: the demo keeps its bound, which is
// the safe direction.
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
// hex characters, nothing shorter, nothing longer, nothing else.
func wellFormedVisitorID(value string) bool {
	if len(value) != 32 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// setVisitorCookie issues the id when the request carries none. A request
// that already holds one keeps it: the id is stable for the browser, not
// rotated per call. No Secure attribute: the demo box serves plain http
// behind its edge, and a Secure cookie would be dropped there.
func setVisitorCookie(w http.ResponseWriter, r *http.Request) {
	if _, err := r.Cookie(visitorCookieName); err == nil {
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
