package portal

// refresh.go, POST /api/portal/v1/token/refresh (DIB-479, spec §1.2 / OR-3).
//
// THE PROBLEM. The portal JWT is a 12-hour token with no way to renew it. A contractor who opens
// the board at 6am is signed out at 6pm, mid-delivery, possibly mid-payment, and HH Pro's whole
// posture is a tab that stays open across a working day. Raising the TTL to 24h (the rejected half
// of OR-3) buys one day and pays for it with a token that stays useful for a day after it leaks.
//
// THE SHAPE. A SLIDING window with an ABSOLUTE ceiling:
//
//	login    → token{iat: T,  exp: T+12h,  ost: T}
//	refresh  → token{iat: T', exp: T'+12h, ost: T}     ← ost is carried, never re-stamped
//	refusal  → 401, plain, once now > ost + 7d
//
// STATELESS, ON PURPOSE. There is no refresh-token table. The absolute cap needs exactly one fact -
// when the login happened, and the token can carry that fact inside its own signature (Claims.Ost).
// A table would buy server-side revocation, which sounds like the reason to build one until you
// notice the ERP ALREADY has the property a revocation list is usually built to get: every request
// re-loads the user and re-derives capabilities from the CURRENT role and status (H-4), so
// deactivating someone ends their access on their next request whether or not their token is still
// signed. A revocation table would be a second, weaker copy of a guarantee that already holds.
// (The one thing it would add, "sign this ONE device out", is parked in NOTES-FIX-479.md with
// its design, because it is a feature, not a gap in this one.)
//
// WHAT REFRESH DOES NOT DO. It does not trust the presented token's role: the new token is minted
// from the FRESHLY LOADED user, so a person demoted five minutes ago refreshes into their new role
// rather than renewing their old one. It does not resurrect an expired token (verifyJWT refuses
// first, refresh is for a still-valid session, not a lapsed one). And it does not extend a session
// past its ceiling, which is the only thing standing between "sliding refresh" and "a permanent
// credential that renews itself".

import (
	"context"
	"net/http"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// RefreshResponse is the refresh payload. It is the LOGIN response plus the two instants a client
// needs in order to schedule itself: when this token dies, and when the session it belongs to dies
// no matter what. Handing both over means the adapter never has to parse a JWT to know when to stop
// trying, and a client that knows the session ends at a fixed instant can warn a contractor BEFORE
// it drops them, instead of discovering it on a failed request.
type RefreshResponse struct {
	Token  string `json:"token"`
	User   User   `json:"user"`
	Config Config `json:"config"`
	// ExpiresAt is this token's own expiry (issued-at + 12h).
	ExpiresAt time.Time `json:"expires_at"`
	// SessionExpiresAt is the ABSOLUTE ceiling, original login + 7 days. It does not move when a
	// token is refreshed; that is the entire point of it.
	SessionExpiresAt time.Time `json:"session_expires_at"`
}

// Refresh exchanges a still-valid portal token for a fresh one on the same session.
//
// Every refusal is the SAME error (errInvalidToken → a plain 401): expired, tampered, unknown user,
// deactivated user, and session-too-old are all "your session is gone, sign in again". Telling a
// caller WHICH of those it was tells an attacker whether a stolen token belongs to a real account.
//
// The re-check is not a formality, it runs the full authenticate path, so a user deactivated or
// demoted since their last request cannot refresh their way past it (spec §1.2).
func (s *Service) Refresh(ctx context.Context, token string) (RefreshResponse, error) {
	caller, claims, err := s.authenticate(ctx, token)
	if err != nil {
		return RefreshResponse{}, errInvalidToken
	}
	now := s.now()
	// The absolute cap. verifyJWT already refuses a token whose session has aged out, so this is
	// belt AND braces, but it is the arm that this endpoint exists to enforce, and a guard that
	// only holds because some other guard also holds is one refactor from not holding at all.
	if now.After(claims.sessionExpiry()) {
		return RefreshResponse{}, errInvalidToken
	}
	u, err := s.store.GetUser(ctx, caller.UserID)
	if err != nil {
		return RefreshResponse{}, errInvalidToken
	}
	// Claims are rebuilt from the RELOADED user (sub/customer_id/role), with the session anchor
	// carried across from the presented token. issueJWT stamps the new iat/exp.
	fresh, err := issueJWT(s.secret, Claims{
		Sub:        caller.UserID,
		CustomerID: caller.CustomerID,
		Role:       caller.Role,
		Ost:        claims.sessionStart(),
	}, now)
	if err != nil {
		return RefreshResponse{}, err
	}
	return RefreshResponse{
		Token:            fresh,
		User:             u,
		Config:           defaultConfig(),
		ExpiresAt:        now.Add(jwtTTL).UTC(),
		SessionExpiresAt: claims.sessionExpiry().UTC(),
	}, nil
}

// registerRefreshRoutes wires the sliding-refresh endpoint. h.authn is the explicit wrapper: a
// valid token is required (no capability, every role may keep its own session alive), and the
// guard's own 401 is the same answer the service gives, so a caller cannot tell "your token is
// bad" from "your session is too old". Both mean: log in again.
func (h *Handler) registerRefreshRoutes(r httpx.Registrar) {
	// Unregistered mint surface (the bearer split, HH-3.2 fold): the helper
	// stays compilable against the Registrar seam; no served mux calls it.
	r.Handle("POST /api/portal/v1/token/refresh", http.HandlerFunc(h.authn(h.refreshToken)))
}

// refreshToken re-reads the Bearer token off the request. The guard already verified it, this
// handler needs the token STRING itself (the guard hands on a PortalContext, which deliberately
// does not carry the raw credential), and re-reading a header is cheaper and clearer than widening
// the caller context to carry a token every other route would ignore.
func (h *Handler) refreshToken(w http.ResponseWriter, r *http.Request) {
	token, ok := bearerToken(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	resp, err := h.svc.Refresh(r.Context(), token)
	if err != nil {
		// Deliberately NOT h.writeErr: that maps errInvalidToken to 400 "invalid or expired
		// invite", which is the invite-accept flow's sentence and would be a lie here. A refused
		// refresh is a 401 and nothing else.
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}
