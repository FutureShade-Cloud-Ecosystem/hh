package messaging

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

// - Approvals rail (0075) / SSE stream auth (D-P1-1) -
//
// An EventSource cannot send an Authorization: Bearer header, so the browser can't open the SSE stream
// the way the REST calls authenticate. The fix (D-P1-1 fix #2): the client mints a short-lived,
// single-use, HMAC-signed ticket via POST /stream-ticket (which IS a normal Bearer'd fetch), then opens
// GET /stream?ticket=... . The ticket payload is USER-BOUND ({user_id, exp, nonce}): the stream then
// serves exactly that user's membership view, so a leaked ticket cannot widen scope, it is read-only and
// membership-filtered, bounded by a 60s TTL + single-use.
//
// Single-use is an in-memory nonce cache. Documented + accepted residual (B4 auth lens): a process restart
// clears the cache, permitting a replay bounded by the 60s TTL on a read-only, membership-filtered stream
//, acceptable on the single-process deploy. Nothing money/mutating rides this path.

// streamTicketTTL bounds a stream ticket. Short by design (D-P1-1): a ticket is minted immediately before
// the EventSource opens, so 60s is ample and caps any leak/replay window.
const streamTicketTTL = 60 * time.Second

var (
	errTicketInvalid = errors.New("messaging: invalid stream ticket")
	errTicketExpired = errors.New("messaging: expired stream ticket")
	errTicketReused  = errors.New("messaging: stream ticket already used")
)

// ticketClaims is the signed, user-bound payload. Nonce makes each ticket single-use.
type ticketClaims struct {
	UserID string `json:"user_id"`
	Exp    int64  `json:"exp"`
	Nonce  string `json:"nonce"`
}

// ticketAuth mints + verifies stream tickets against an HMAC secret, with an in-memory single-use nonce
// cache. now is injectable for deterministic expiry/replay tests.
type ticketAuth struct {
	secret []byte
	now    func() time.Time
	mu     sync.Mutex
	used   map[string]int64 // nonce -> exp unix (single-use cache; GC'd lazily on access)
}

// newTicketAuth builds the authenticator over an HMAC secret.
func newTicketAuth(secret string) *ticketAuth {
	return &ticketAuth{secret: []byte(secret), now: time.Now, used: map[string]int64{}}
}

// Mint issues a 60s single-use ticket bound to userID: base64url(payload).base64url(HMAC-SHA256(payload)).
func (t *ticketAuth) Mint(userID string) (string, error) {
	nonce, err := randNonce()
	if err != nil {
		return "", err
	}
	c := ticketClaims{UserID: userID, Exp: t.now().Add(streamTicketTTL).Unix(), Nonce: nonce}
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	p := base64.RawURLEncoding.EncodeToString(payload)
	return p + "." + t.sign(p), nil
}

// Verify checks the HMAC (constant-time), the expiry, and single-use (replay), returning the bound user
// id. A forged/mutated payload fails the HMAC (so the user-binding cannot be tampered); an expired or
// already-used ticket is rejected.
func (t *ticketAuth) Verify(ticket string) (string, error) {
	p, sig, ok := strings.Cut(ticket, ".")
	if !ok || p == "" || sig == "" {
		return "", errTicketInvalid
	}
	if subtle.ConstantTimeCompare([]byte(t.sign(p)), []byte(sig)) != 1 {
		return "", errTicketInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return "", errTicketInvalid
	}
	var c ticketClaims
	if err := json.Unmarshal(payload, &c); err != nil || c.UserID == "" || c.Nonce == "" {
		return "", errTicketInvalid
	}
	nowUnix := t.now().Unix()
	if nowUnix >= c.Exp {
		return "", errTicketExpired
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gcLocked(nowUnix)
	if _, seen := t.used[c.Nonce]; seen {
		return "", errTicketReused
	}
	t.used[c.Nonce] = c.Exp
	return c.UserID, nil
}

func (t *ticketAuth) sign(p string) string {
	mac := hmac.New(sha256.New, t.secret)
	mac.Write([]byte(p))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// gcLocked drops expired nonces so the single-use cache stays bounded (caller holds t.mu).
func (t *ticketAuth) gcLocked(nowUnix int64) {
	for n, exp := range t.used {
		if nowUnix >= exp {
			delete(t.used, n)
		}
	}
}

func randNonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
