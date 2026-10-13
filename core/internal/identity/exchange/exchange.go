// Package exchange is POST /api/v1/auth/exchange: a person who signed in to
// FB Console presents a short Console JWT and an org slug, passes the gates
// of the state note (section 3), and receives a session token the core
// itself signs. Every gate fails closed. A refusal uses the shared bodies
// of the authz middleware (one body per family, so no refusal says which
// gate failed); the failed gate goes to a static log code and a counter,
// never to the wire.
//
// Every collaborator is a narrow interface the owning module satisfies: no
// table is read here. The route belongs on the public leg in the console
// sign in mode only.
package exchange

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/ratelimit"
)

// The allowances of gate 2, named so the sizing is one reviewed change.
// A depot shares one address, so the per address allowance admits a morning
// of sign ins from one site; the per user allowance admits every device of
// one person refreshing inside one window (three shells and a retry or
// two), and the shells' contract allows at most one exchange per drain pass.
const (
	AddressAllowance = 60
	UserAllowance    = 10
	LimitWindow      = time.Minute
)

// MinTokenWindow is the floor of a minted token's life. Below it the
// remaining sign in age is spent and the person signs in again (401).
const MinTokenWindow = time.Minute

const (
	maxBodyBytes = 8 << 10
	// sessionClockSkew is how far into the future a Console session's
	// creation time may sit before it is a value the core cannot trust.
	sessionClockSkew = time.Minute
	// auditAction and the entity vocabulary of the exchange's audit row.
	auditAction     = "AUTH_EXCHANGE"
	auditEntityType = "auth.exchange"
	auditPurpose    = "STAFF_SIGN_IN"
)

// The static failure codes: where a refusal is recorded. They are log
// words and counter keys, never response content.
const (
	CodeBody         = "g1_body"
	CodeRateAddress  = "g2_rate_address"
	CodeConsoleNo    = "g3_console_refused"
	CodeConsoleDown  = "g3_console_unavailable"
	CodeAccountShape = "g3_account_mismatch"
	CodeRateUser     = "g2_rate_user"
	CodeClaimExp     = "g3b_exp_out_of_bound"
	CodeClaimReplay  = "g3b_replay"
	CodeClaimError   = "g3b_claim_error"
	CodeEmailUnver   = "g4_email_unverified"
	CodeAccountOff   = "g4_account_off"
	CodeEmailList    = "g4_email_not_listed"
	CodeMethodNo     = "g4b_method_refused"
	CodeMethodError  = "g4b_method_error"
	CodeNotMember    = "g5_not_member"
	CodeMemberError  = "g5_membership_error"
	CodeNoAnchor     = "g5b_no_anchor"
	CodeAnchorError  = "g5b_anchor_error"
	CodeNoLinkedRow  = "g6_no_linked_row"
	CodeInactive     = "g6_inactive"
	CodeNoGrant      = "g6_no_grant"
	CodeStaffError   = "g6_staff_error"
	CodeCeilingError = "g7_ceiling_error"
	CodeSessionError = "g7b_sessions_error"
	CodeSessionMiss  = "g7b_session_not_found"
	CodeSessionValue = "g7b_session_value"
	CodeSignInTooOld = "g7b_sign_in_too_old"
	CodeLabelError   = "g8_label_error"
	CodeAuditError   = "g8_audit_error"
	CodeMintError    = "g8_mint_error"
)

// AllCodes lists every static code, for the test that keeps them distinct.
func AllCodes() []string {
	return []string{
		CodeBody, CodeRateAddress, CodeConsoleNo, CodeConsoleDown, CodeAccountShape, CodeRateUser,
		CodeClaimExp, CodeClaimReplay, CodeClaimError, CodeEmailUnver, CodeAccountOff, CodeEmailList,
		CodeMethodNo, CodeMethodError, CodeNotMember, CodeMemberError, CodeNoAnchor, CodeAnchorError,
		CodeNoLinkedRow, CodeInactive, CodeNoGrant, CodeStaffError, CodeCeilingError, CodeSessionError,
		CodeSessionMiss, CodeSessionValue, CodeSignInTooOld, CodeLabelError, CodeAuditError, CodeMintError,
	}
}

// The collaborators, each the narrow shape the owning module satisfies.

// Console is the FB Console client (authz.ConsoleClient).
type Console interface {
	AccountUnderJWT(ctx context.Context, jwt string) (authz.ConsoleAccount, error)
	UserSessions(ctx context.Context, userID string) ([]authz.ConsoleSession, error)
}

// Members is the org Team membership read; the composition hands the
// UNCACHED source, never the per request cache.
type Members interface {
	OrgMember(ctx context.Context, platformUserID, orgSlug string) (bool, error)
}

// Orgs resolves the slug against the org anchor.
type Orgs interface {
	ResolveSlug(ctx context.Context, orgSlug string) (orgID string, ok bool, err error)
}

// Claims is the identity module's single use JWT claim.
type Claims interface {
	ClaimJWT(ctx context.Context, sigHash [32]byte, exp time.Time) (bool, error)
}

// Staff is the identity module's staff reads.
type Staff interface {
	LinkedActiveUser(ctx context.Context, platformUserID string) (userID string, ok bool, err error)
	ResolveStaff(ctx context.Context, platformUserID, orgID string) (identity.StaffAuthority, bool, error)
	StaffRoleLabel(ctx context.Context, roleID string) (label string, ok bool, err error)
}

// Auditor is audit.Service.Record.
type Auditor interface {
	Record(ctx context.Context, a audit.SensitiveAccess) error
}

// Minter is identity.SessionMinter.MintAt.
type Minter interface {
	MintAt(subject string, window time.Duration, at time.Time) (string, error)
}

// SignInMethods is the seam of gate 4b (the dealer's allowed sign in
// methods, item 6c). It receives the provider of the Console session that
// gate 7b matched, and runs once that session is known, which is after
// gate 4. Not allowed is the 403, an error the 503.
type SignInMethods interface {
	SignInMethodAllowed(ctx context.Context, orgID, provider string) (bool, error)
}

// Config is the handler's constructor input; every collaborator is
// required.
type Config struct {
	Console Console
	Members Members
	Orgs    Orgs
	Claims  Claims
	Staff   Staff
	Knob    identity.DriverSessionHours
	Audit   Auditor
	Minter  Minter
	Methods SignInMethods

	// AllowedDomains and AllowedAddresses are gate 4's lists, already
	// normalised (ParseAllowList). Both empty refuses every sign in.
	AllowedDomains   []string
	AllowedAddresses []string
	// TokenTTL is CORE_EXCHANGE_TOKEN_TTL.
	TokenTTL time.Duration

	// Trusted names the proxies allowed to speak for a client address.
	Trusted ratelimit.TrustedProxies
	// AddressLimit and UserLimit select the gate 2 allowances; nil picks
	// the named constants.
	AddressLimit *ratelimit.AddressLimit
	UserLimit    *ratelimit.AddressLimit

	Now func() time.Time
	Log *slog.Logger
}

// Handler serves the exchange.
type Handler struct {
	cfg      Config
	domains  map[string]bool
	addrs    map[string]bool
	addrLim  *ratelimit.AddressLimit
	userLim  *ratelimit.AddressLimit
	now      func() time.Time
	log      *slog.Logger
	mu       sync.Mutex
	failures map[string]int64
}

// New validates the collaborators.
func New(c Config) (*Handler, error) {
	switch {
	case c.Console == nil:
		return nil, errors.New("exchange: the console client is required")
	case c.Members == nil:
		return nil, errors.New("exchange: the membership source is required")
	case c.Orgs == nil:
		return nil, errors.New("exchange: the org resolver is required")
	case c.Claims == nil:
		return nil, errors.New("exchange: the claim store is required")
	case c.Staff == nil:
		return nil, errors.New("exchange: the staff reader is required")
	case c.Knob == nil:
		return nil, errors.New("exchange: the driver session knob source is required")
	case c.Audit == nil:
		return nil, errors.New("exchange: the audit recorder is required")
	case c.Minter == nil:
		return nil, errors.New("exchange: the session minter is required")
	case c.Methods == nil:
		return nil, errors.New("exchange: the sign in method seam is required")
	case c.TokenTTL < identity.MinSessionWindow:
		return nil, errors.New("exchange: the token window is required")
	}
	h := &Handler{cfg: c, domains: map[string]bool{}, addrs: map[string]bool{}, failures: map[string]int64{}}
	for _, d := range c.AllowedDomains {
		h.domains[strings.ToLower(strings.TrimSpace(d))] = true
	}
	for _, a := range c.AllowedAddresses {
		h.addrs[strings.ToLower(strings.TrimSpace(a))] = true
	}
	h.addrLim = c.AddressLimit
	if h.addrLim == nil {
		h.addrLim = ratelimit.NewAddressLimit(AddressAllowance, LimitWindow, c.Now)
	}
	h.userLim = c.UserLimit
	if h.userLim == nil {
		h.userLim = ratelimit.NewAddressLimit(UserAllowance, LimitWindow, c.Now)
	}
	h.now = c.Now
	if h.now == nil {
		h.now = time.Now
	}
	h.log = c.Log
	if h.log == nil {
		h.log = slog.Default()
	}
	return h, nil
}

// ParseAllowList reads a comma list into lower case, trimmed, non empty
// entries.
func ParseAllowList(list string) []string {
	var out []string
	for _, e := range strings.Split(list, ",") {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			out = append(out, e)
		}
	}
	return out
}

// GateFailures is a snapshot of the per code counters.
func (h *Handler) GateFailures() map[string]int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[string]int64, len(h.failures))
	for k, v := range h.failures {
		out[k] = v
	}
	return out
}

// fail records the static code and counts it. Nothing else about the
// request is logged: no JWT, no id, no address, no email.
func (h *Handler) fail(code string) {
	h.mu.Lock()
	h.failures[code]++
	h.mu.Unlock()
	h.log.Warn("exchange refused", "code", code)
}

func (h *Handler) refuse401(w http.ResponseWriter, code string) {
	h.fail(code)
	authz.RefuseUnauthorized(w)
}
func (h *Handler) refuse403(w http.ResponseWriter, code string) {
	h.fail(code)
	authz.RefuseForbidden(w)
}
func (h *Handler) refuse503(w http.ResponseWriter, code string) {
	h.fail(code)
	authz.RefuseUnavailable(w)
}
func (h *Handler) refuse429(w http.ResponseWriter, code string) {
	h.fail(code)
	httpx.Error(w, http.StatusTooManyRequests, "too many sign in attempts, wait a moment and try again")
}
func (h *Handler) refuse400(w http.ResponseWriter, code, msg string) {
	h.fail(code)
	httpx.Error(w, http.StatusBadRequest, msg)
}

type request struct {
	JWT string `json:"jwt"`
	Org string `json:"org"`
}

var jwtSegment = regexp.MustCompile(`^[A-Za-z0-9_-]+={0,2}$`)

// wellFormedJWT is gate 1's structural check: three non empty base64url
// segments inside the length bound. Whether Console accepts it is gate 3's.
func wellFormedJWT(s string) bool {
	if s == "" || len(s) > authz.MaxConsoleJWTLen {
		return false
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if !jwtSegment.MatchString(p) {
			return false
		}
	}
	return true
}

type userBlock struct {
	UserID      string `json:"user_id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	RoleCode    string `json:"role_code"`
	RoleName    string `json:"role_name"`
	Tier        int    `json:"tier"`
	Scope       string `json:"scope"`
}

type response struct {
	Token       string    `json:"token"`
	ExpiresAtMs int64     `json:"expiresAtMs"`
	User        userBlock `json:"user"`
}

// ServeHTTP runs the gates in the note's order.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := h.now()

	// Gate 1: the body, in its own words.
	var in request
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		h.refuse400(w, CodeBody, "the body must be one JSON object with the fields jwt and org only")
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		h.refuse400(w, CodeBody, "the body must be one JSON object with the fields jwt and org only")
		return
	}
	if !wellFormedJWT(in.JWT) {
		h.refuse400(w, CodeBody, "jwt must be a console JWT")
		return
	}
	if !authz.ValidOrgSlug(in.Org) {
		h.refuse400(w, CodeBody, "org is not a valid org slug")
		return
	}

	// Gate 2, the address half.
	if !h.addrLim.Allow(ratelimit.ClientAddress(r, h.cfg.Trusted)) {
		h.refuse429(w, CodeRateAddress)
		return
	}

	// Gate 3: Console answers for the JWT.
	acct, err := h.cfg.Console.AccountUnderJWT(ctx, in.JWT)
	if err != nil {
		if errors.Is(err, authz.ErrConsoleRefused) {
			h.refuse401(w, CodeConsoleNo)
		} else {
			h.refuse503(w, CodeConsoleDown)
		}
		return
	}
	jwt, err := authz.ParseConsoleJWT(in.JWT)
	if err != nil {
		h.refuse401(w, CodeClaimExp)
		return
	}
	if jwt.UserID != acct.ID {
		h.refuse401(w, CodeAccountShape)
		return
	}

	// Gate 2, the user half.
	if !h.userLim.Allow(acct.ID) {
		h.refuse429(w, CodeRateUser)
		return
	}

	// Gate 3b: the JWT is single use.
	claimed, err := h.cfg.Claims.ClaimJWT(ctx, identity.HashJWTSignature(jwt.Signature), time.Unix(jwt.ExpiresAt, 0))
	switch {
	case errors.Is(err, identity.ErrJWTExpOutOfBound):
		h.refuse401(w, CodeClaimExp)
		return
	case err != nil:
		h.refuse503(w, CodeClaimError)
		return
	case !claimed:
		h.refuse401(w, CodeClaimReplay)
		return
	}

	// Gate 4: the account and the allow lists.
	if !acct.EmailVerified {
		h.refuse403(w, CodeEmailUnver)
		return
	}
	if !acct.Active {
		h.refuse403(w, CodeAccountOff)
		return
	}
	if !h.emailListed(acct.Email) {
		h.refuse403(w, CodeEmailList)
		return
	}

	// Gate 5: Team membership from the uncached source.
	member, err := h.cfg.Members.OrgMember(ctx, acct.ID, in.Org)
	if err != nil {
		h.refuse503(w, CodeMemberError)
		return
	}
	if !member {
		h.refuse403(w, CodeNotMember)
		return
	}

	// Gate 5b: the org anchor. No anchor is the same 403 as no membership.
	orgID, ok, err := h.cfg.Orgs.ResolveSlug(ctx, in.Org)
	if err != nil {
		h.refuse503(w, CodeAnchorError)
		return
	}
	if !ok {
		h.refuse403(w, CodeNoAnchor)
		return
	}

	// Gate 6: a linked, ACTIVE users row and at least one grant.
	usersID, linked, err := h.cfg.Staff.LinkedActiveUser(ctx, acct.ID)
	if err != nil {
		h.refuse503(w, CodeStaffError)
		return
	}
	if !linked {
		h.refuse403(w, CodeNoLinkedRow)
		return
	}
	authority, ok, err := h.cfg.Staff.ResolveStaff(ctx, acct.ID, orgID)
	if err != nil {
		if errors.Is(err, identity.ErrStaffUserInactive) {
			h.refuse403(w, CodeInactive)
		} else {
			h.refuse503(w, CodeStaffError)
		}
		return
	}
	if !ok {
		h.refuse403(w, CodeNoGrant)
		return
	}

	// Gate 7: the ceiling of the winning role. A driver whose knob is
	// unreadable is refused on purpose, since the knob is the contract.
	ceiling, err := identity.SessionCeiling(ctx, authority.RoleID, orgID, h.cfg.Knob)
	if err != nil {
		h.refuse503(w, CodeCeilingError)
		return
	}

	// Gate 7b (with gate 4b): the Console session behind the JWT, read
	// server side. No match, or a value it cannot read, is never a pass.
	sessions, err := h.cfg.Console.UserSessions(ctx, acct.ID)
	if err != nil {
		h.refuse503(w, CodeSessionError)
		return
	}
	var session *authz.ConsoleSession
	if jwt.SessionID != "" {
		for i := range sessions {
			if sessions[i].ID == jwt.SessionID {
				session = &sessions[i]
				break
			}
		}
	}
	if session == nil {
		h.refuse503(w, CodeSessionMiss)
		return
	}
	if session.CreatedAt.IsZero() || session.CreatedAt.After(now.Add(sessionClockSkew)) {
		h.refuse503(w, CodeSessionValue)
		return
	}
	allowed, err := h.cfg.Methods.SignInMethodAllowed(ctx, orgID, session.Provider)
	if err != nil {
		h.refuse503(w, CodeMethodError)
		return
	}
	if !allowed {
		h.refuse403(w, CodeMethodNo)
		return
	}
	window := minDuration(ceiling, h.cfg.TokenTTL, session.CreatedAt.Add(ceiling).Sub(now))
	// Whole seconds, so exp - iat is exactly the window the ceiling judges.
	window = window.Truncate(time.Second)
	if window < MinTokenWindow {
		h.refuse401(w, CodeSignInTooOld)
		return
	}

	label, ok, err := h.cfg.Staff.StaffRoleLabel(ctx, authority.RoleID)
	if err != nil || !ok {
		h.refuse503(w, CodeLabelError)
		return
	}

	// Gate 8: the audit row first, then the token.
	if err := h.cfg.Audit.Record(ctx, audit.SensitiveAccess{
		Action:     auditAction,
		EntityType: auditEntityType,
		EntityID:   usersID,
		ActorID:    usersID,
		Purpose:    auditPurpose,
		Extra:      map[string]any{"actor_subject": acct.ID},
	}); err != nil {
		h.refuse503(w, CodeAuditError)
		return
	}
	token, err := h.cfg.Minter.MintAt(acct.ID, window, now)
	if err != nil {
		h.refuse503(w, CodeMintError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, response{
		Token:       token,
		ExpiresAtMs: (now.Unix() + int64(window/time.Second)) * 1000,
		User: userBlock{
			UserID: acct.ID, Email: acct.Email, DisplayName: acct.Name,
			RoleCode: authority.RoleID, RoleName: label,
			Tier: authority.Tier, Scope: strings.ToUpper(authority.Scope),
		},
	})
}

// emailListed is gate 4's allow list: the whole address, or the domain
// after the @, case insensitive and exact. An address that does not hold
// exactly one @ with a local part is on no list, so a crafted local part
// can never read as a listed domain. Both lists empty lists nobody.
func (h *Handler) emailListed(email string) bool {
	e := strings.ToLower(strings.TrimSpace(email))
	at := strings.LastIndexByte(e, '@')
	if at <= 0 || at == len(e)-1 || strings.Count(e, "@") != 1 {
		return false
	}
	return h.addrs[e] || h.domains[e[at+1:]]
}

func minDuration(first time.Duration, rest ...time.Duration) time.Duration {
	m := first
	for _, d := range rest {
		if d < m {
			m = d
		}
	}
	return m
}
