package portal

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// tokenTTL bounds how long an invite link is valid (US-CP07-3).
const tokenTTL = 7 * 24 * time.Hour

// jwtTTL bounds a contractor session.
const jwtTTL = 12 * time.Hour

// sessionMaxAge is the ABSOLUTE ceiling on one login, measured from the original login instant
// and never from the newest token (DIB-479 / OR-3). Sliding refresh without this is not a session
// policy at all, a token that can always mint its successor is a permanent credential with a
// 12-hour costume, and the only way to end it is to deactivate the person.
//
// Seven days is the number the spec landed on, and it is chosen against the actual failure it
// prevents: a contractor's tab that has been open since last week, on a phone in a truck, is not
// a session anybody is still watching. Re-authenticating weekly is a Monday-morning password
// prompt; the alternative is a bearer token with no end.
const sessionMaxAge = 7 * 24 * time.Hour

// clockSkew tolerates minor clock drift when rejecting future-dated tokens (L-4).
const clockSkew = 60 * time.Second

var errInvalidToken = errors.New("portal: invalid or expired token")

// dummyHash is a valid bcrypt hash compared against on the login miss path so a
// non-existent / inactive account costs the same wall-clock as a real one (no
// timing enumeration). Its plaintext is irrelevant, the result is discarded.
const dummyHash = "$2a$10$ja7jD99TS1QsOtU.7dVDIu.HwgOK2pBERc60C4IyjMEi49IvR/dGO"

// burnPassword spends constant bcrypt time without revealing anything (miss path).
func burnPassword(plain string) { _ = bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(plain)) }

// hashPassword returns a bcrypt hash (salt + cost embedded).
func hashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// checkPassword reports whether plain matches the stored bcrypt hash.
func checkPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// newInviteToken returns a random raw token (shown once to the admin) and its
// sha256 hex hash (stored). Only the hash ever touches the DB.
func newInviteToken() (raw, hashed string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	return raw, hashToken(raw), nil
}

// hashToken is the deterministic sha256 hex of a raw invite token.
func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// -- JWT (HS256, stdlib) ------------------------------------------------------
// The contractor portal (the Lit app in this monorepo, D-028) stores the opaque
// token and sends it as a Bearer header; the ERP is the only party that signs and
// verifies it, so a minimal HS256 implementation avoids a third-party JWT dependency.

// Claims is the contractor JWT payload.
type Claims struct {
	Sub        string `json:"sub"`
	CustomerID string `json:"customer_id"`
	Role       string `json:"role"`
	Iat        int64  `json:"iat"`
	Exp        int64  `json:"exp"`
	// Ost, "original session time", is the instant the LOGIN happened, carried forward
	// unchanged through every refresh (DIB-479 / OR-3). It is what makes the 7-day absolute cap
	// STATELESS: the token itself remembers how old the session is, so there is no refresh-token
	// table to write, rotate, reap, or leak, and no revocation store that a horizontally-scaled
	// API would have to agree about.
	//
	// It is safe to trust for this purpose ONLY because it is inside the HMAC, a caller who
	// edits it invalidates the signature, and a caller who mints one has the signing key and has
	// already won. It is NOT an authorization input: role and status are still re-read from the
	// DB on every single request (Service.Authenticate, H-4).
	//
	// omitempty + the zero fallback in sessionStart(): tokens issued before this claim existed
	// are still valid, and their session age is measured from their own iat, which can only ever
	// make the cap arrive SOONER, never later.
	Ost int64 `json:"ost,omitempty"`
}

// sessionStart is the instant the session began: the original-login claim when the token carries
// one, and the token's own issue time when it does not (a pre-DIB-479 token). Fail-SHORT by
// construction, the fallback can only shorten a session, never extend one.
func (c Claims) sessionStart() int64 {
	if c.Ost > 0 {
		return c.Ost
	}
	return c.Iat
}

// sessionExpiry is the wall-clock instant this session dies no matter how many times it is
// refreshed. One expression, read by both the refusal in verifyJWT and the session_expires_at the
// refresh response hands the client, so the server and the client can never disagree about when
// the session ends.
func (c Claims) sessionExpiry() time.Time {
	return time.Unix(c.sessionStart(), 0).Add(sessionMaxAge)
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func sign(secret, signingInput string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	return b64(mac.Sum(nil))
}

// issueJWT signs a token for a user. now is injected so tests are deterministic.
//
// The caller decides the session anchor: a LOGIN passes a zero Ost and gets `now` stamped in, a
// REFRESH passes the anchor it read off the presented token and it survives untouched. That is the
// whole sliding-refresh mechanism, iat/exp roll forward, ost does not.
func issueJWT(secret string, c Claims, now time.Time) (string, error) {
	if secret == "" {
		return "", errors.New("portal: JWT secret not configured")
	}
	c.Iat = now.Unix()
	c.Exp = now.Add(jwtTTL).Unix()
	if c.Ost <= 0 {
		c.Ost = c.Iat // a fresh login IS the start of the session
	}
	h, _ := json.Marshal(jwtHeader{Alg: "HS256", Typ: "JWT"})
	p, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	signingInput := b64(h) + "." + b64(p)
	return signingInput + "." + sign(secret, signingInput), nil
}

// verifyJWT checks the header (alg+typ pinned), signature, issued-at, and expiry.
func verifyJWT(secret, token string, now time.Time) (Claims, error) {
	if secret == "" {
		return Claims{}, errInvalidToken // never verify against an empty key (L-1)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, errInvalidToken
	}
	// Pin alg=HS256 AND typ=JWT, reject alg=none / alg-confusion / odd typ before trusting bytes (L-4).
	hdrBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Claims{}, errInvalidToken
	}
	var h jwtHeader
	if err := json.Unmarshal(hdrBytes, &h); err != nil || h.Alg != "HS256" || h.Typ != "JWT" {
		return Claims{}, errInvalidToken
	}
	signingInput := parts[0] + "." + parts[1]
	expected := sign(secret, signingInput)
	if subtle.ConstantTimeCompare([]byte(expected), []byte(parts[2])) != 1 {
		return Claims{}, errInvalidToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, errInvalidToken
	}
	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return Claims{}, errInvalidToken
	}
	nowUnix := now.Unix()
	if c.Iat > nowUnix+int64(clockSkew.Seconds()) {
		return Claims{}, fmt.Errorf("%w: issued in the future", errInvalidToken) // L-4
	}
	if nowUnix >= c.Exp {
		return Claims{}, fmt.Errorf("%w: expired", errInvalidToken)
	}
	// Re-bound the lifetime: reject a token whose self-declared window exceeds policy (L-4).
	if c.Exp-c.Iat > int64(jwtTTL.Seconds())+int64(clockSkew.Seconds()) {
		return Claims{}, fmt.Errorf("%w: lifetime exceeds policy", errInvalidToken)
	}
	// The ABSOLUTE session cap, enforced on EVERY request and not merely at refresh time
	// (DIB-479 / OR-3). A cap checked only where new tokens are minted is not a cap: the last
	// token issued a minute before the deadline would outlive it by a full 12-hour TTL, and the
	// "seven day" promise would quietly mean seven days and twelve hours. Checked here, the
	// session ends when it says it ends.
	if now.After(c.sessionExpiry()) {
		return Claims{}, fmt.Errorf("%w: session exceeded maximum age", errInvalidToken)
	}
	return c, nil
}

// PortalContext is the authenticated caller, derived from a verified JWT.
type PortalContext struct {
	UserID     string
	CustomerID string
	Role       string
}

// Caps returns the caller's capabilities.
func (c PortalContext) Caps() Capabilities { return CapabilitiesFor(c.Role) }
