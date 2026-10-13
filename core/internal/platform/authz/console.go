package authz

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ConsoleClient is the sign in exchange's client for FB Console (the
// Appwrite server API): the account read under the person's own JWT, and
// the session listing under the core's server key. Like the membership
// source it makes exactly one attempt per call, follows no redirect (a 3xx
// is an operational failure, so a custom header such as the JWT is never
// re-sent to wherever a redirect points), bounds the time and the body it
// will read, and returns static errors that never carry a response body, the
// JWT or the key. The two sentinels are the only classification the caller
// needs: ErrConsoleRefused is Console's own no (a 401, or an account id
// outside the Console id shape), ErrConsoleUnavailable is everything else.
type ConsoleClient struct {
	endpoint string
	project  string
	apiKey   string
	hc       *http.Client
}

var (
	// ErrConsoleRefused: Console refused the credential.
	ErrConsoleRefused = errors.New("authz: the console refused the credential")
	// ErrConsoleUnavailable: Console could not give a usable answer
	// (unreachable, timeout, any status but the expected one, a redirect,
	// a body that is oversized or malformed). Never a verdict on the caller.
	ErrConsoleUnavailable = errors.New("authz: the console could not answer")
)

const (
	// consoleAccountBodyLimit and consoleSessionsBodyLimit bound what is
	// read; a larger answer is unavailable, never truncated and parsed.
	consoleAccountBodyLimit  = 64 << 10
	consoleSessionsBodyLimit = 1 << 20
	// MaxConsoleJWTLen bounds the JWT the exchange accepts. A Console JWT
	// is a few hundred bytes.
	MaxConsoleJWTLen = 4096
	// maxConsoleIDLen is the Console id length cap.
	maxConsoleIDLen = 36
)

// NewConsoleClient validates the configuration like the membership source
// does. A nil client selects one with the package timeout; an injected
// client is copied, and the copy refuses redirects whatever the original
// did.
func NewConsoleClient(endpoint, project, apiKey string, hc *http.Client) (*ConsoleClient, error) {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("authz: console endpoint must be an absolute http or https URL")
	}
	if project == "" {
		return nil, errors.New("authz: console project is required")
	}
	if apiKey == "" {
		return nil, errors.New("authz: console api key is required")
	}
	return &ConsoleClient{endpoint: u.String(), project: project, apiKey: apiKey, hc: refusingRedirects(hc)}, nil
}

// refusingRedirects returns a copy of the client (the package default when
// nil) that hands a 3xx back as the response instead of following it.
func refusingRedirects(hc *http.Client) *http.Client {
	if hc == nil {
		hc = &http.Client{Timeout: appwriteDefaultTimeout}
	}
	c := *hc
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &c
}

// ValidConsoleID reports whether the id has the Console id shape: 1 to 36
// characters from a-z A-Z 0-9 period hyphen underscore, not starting with a
// special character. A colon is outside it, so no Console id can equal a
// namespaced persona subject.
func ValidConsoleID(id string) bool {
	if id == "" || len(id) > maxConsoleIDLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		alnum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if alnum {
			continue
		}
		if i > 0 && (c == '.' || c == '-' || c == '_') {
			continue
		}
		return false
	}
	return true
}

// ConsoleAccount is the part of GET /account the exchange reads.
type ConsoleAccount struct {
	ID            string
	Email         string
	Name          string
	EmailVerified bool
	Active        bool
}

type consoleAccountWire struct {
	ID                *string `json:"$id"`
	Email             *string `json:"email"`
	Name              string  `json:"name"`
	EmailVerification *bool   `json:"emailVerification"`
	Status            *bool   `json:"status"`
}

// AccountUnderJWT reads GET /account with the person's JWT and the project,
// and no server key. The JWT is the credential: Console answers 200 only
// for a live JWT of a live session.
func (c *ConsoleClient) AccountUnderJWT(ctx context.Context, jwt string) (ConsoleAccount, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"/account", nil)
	if err != nil {
		return ConsoleAccount{}, ErrConsoleUnavailable
	}
	req.Header.Set("X-Appwrite-Project", c.project)
	req.Header.Set("X-Appwrite-JWT", jwt)
	body, status, ok := c.do(req, consoleAccountBodyLimit)
	if !ok {
		return ConsoleAccount{}, ErrConsoleUnavailable
	}
	if status == http.StatusUnauthorized {
		return ConsoleAccount{}, ErrConsoleRefused
	}
	if status != http.StatusOK {
		return ConsoleAccount{}, ErrConsoleUnavailable
	}
	var w consoleAccountWire
	if err := json.Unmarshal(body, &w); err != nil {
		return ConsoleAccount{}, ErrConsoleUnavailable
	}
	if w.ID == nil || !ValidConsoleID(*w.ID) {
		return ConsoleAccount{}, ErrConsoleRefused
	}
	if w.Email == nil || w.EmailVerification == nil || w.Status == nil {
		return ConsoleAccount{}, ErrConsoleUnavailable
	}
	return ConsoleAccount{
		ID: *w.ID, Email: *w.Email, Name: w.Name,
		EmailVerified: *w.EmailVerification, Active: *w.Status,
	}, nil
}

// ConsoleSession is the part of a Console session the exchange reads.
// CreatedAt is the zero time when Console's value does not parse; the
// caller treats that as a value it cannot tell, never as a pass.
type ConsoleSession struct {
	ID        string
	CreatedAt time.Time
	Provider  string
}

type consoleSessionWire struct {
	ID        string `json:"$id"`
	CreatedAt string `json:"$createdAt"`
	Provider  string `json:"provider"`
}

type consoleSessionPage struct {
	Total    *int                 `json:"total"`
	Sessions []consoleSessionWire `json:"sessions"`
}

// UserSessions reads GET /users/{id}/sessions with the server key. A
// listing that Console says holds more than it sent is refused, like the
// membership listing: a missing session could hide the one the caller
// claims.
func (c *ConsoleClient) UserSessions(ctx context.Context, userID string) ([]ConsoleSession, error) {
	if !ValidConsoleID(userID) {
		return nil, errors.New("authz: console user id is not a console id")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"/users/"+url.PathEscape(userID)+"/sessions", nil)
	if err != nil {
		return nil, ErrConsoleUnavailable
	}
	req.Header.Set("X-Appwrite-Project", c.project)
	req.Header.Set("X-Appwrite-Key", c.apiKey)
	body, status, ok := c.do(req, consoleSessionsBodyLimit)
	if !ok || status != http.StatusOK {
		return nil, ErrConsoleUnavailable
	}
	var page consoleSessionPage
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, ErrConsoleUnavailable
	}
	if page.Total != nil && *page.Total > len(page.Sessions) {
		return nil, ErrConsoleUnavailable
	}
	out := make([]ConsoleSession, 0, len(page.Sessions))
	for _, s := range page.Sessions {
		created, err := time.Parse(time.RFC3339Nano, s.CreatedAt)
		if err != nil {
			created = time.Time{}
		}
		out = append(out, ConsoleSession{ID: s.ID, CreatedAt: created, Provider: s.Provider})
	}
	return out, nil
}

// do performs the one attempt and reads at most limit bytes. ok is false
// for a transport failure or a body past the limit.
func (c *ConsoleClient) do(req *http.Request, limit int64) (body []byte, status int, ok bool) {
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, 0, false
	}
	defer func() { _ = resp.Body.Close() }()
	body, err = io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(body)) > limit {
		return nil, resp.StatusCode, false
	}
	return body, resp.StatusCode, true
}

// ConsoleJWT is what the exchange reads from a Console JWT after Console
// has accepted it. The payload is read UNVERIFIED: it carries no authority
// of its own, it names the session and user Console already vouched for by
// answering the account read, and the signature bytes are the replay key.
type ConsoleJWT struct {
	SessionID string
	UserID    string
	// ExpiresAt is the payload's exp in Unix seconds.
	ExpiresAt int64
	// Signature is the decoded signature, the canonical form the claim key
	// hashes: two spellings of one signature (padded or not) decode to the
	// same bytes.
	Signature []byte
}

type consoleJWTPayload struct {
	Exp       json.RawMessage `json:"exp"`
	SessionID string          `json:"sessionId"`
	UserID    string          `json:"userId"`
}

// ParseConsoleJWT splits the three segments and reads the payload and the
// signature. A JWT with no exp, a non numeric exp (a string, a fraction,
// null) or an undecodable part is refused. The error is static.
func ParseConsoleJWT(jwt string) (ConsoleJWT, error) {
	errMalformed := errors.New("authz: the console JWT is malformed")
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return ConsoleJWT{}, errMalformed
	}
	raw, err := decodeSegment(parts[1])
	if err != nil {
		return ConsoleJWT{}, errMalformed
	}
	var p consoleJWTPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return ConsoleJWT{}, errMalformed
	}
	exp, err := strconv.ParseInt(strings.TrimSpace(string(p.Exp)), 10, 64)
	if err != nil || exp <= 0 {
		return ConsoleJWT{}, errMalformed
	}
	sig, err := decodeSegment(parts[2])
	if err != nil || len(sig) == 0 {
		return ConsoleJWT{}, errMalformed
	}
	return ConsoleJWT{SessionID: p.SessionID, UserID: p.UserID, ExpiresAt: exp, Signature: sig}, nil
}

// decodeSegment decodes one base64url segment, with or without padding.
func decodeSegment(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}
