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
	"slices"
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
	// auditPurposeTenant is the tenant leg's purpose word.
	auditPurposeTenant = "TENANT_SIGN_IN"

	// classStaff and classTenant are the body's optional class field: the
	// staff exchange is the default, the tenant leg is asked for by name.
	classStaff  = "staff"
	classTenant = "tenant"

	// TenantSessionCeiling bounds a tenant token's window and the sign in
	// age of the Console session behind it: a contractor has no role knob,
	// so the standard staff session stands.
	TenantSessionCeiling = identity.StaffSessionTTL
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

	// The tenant leg's own refusals (the gates it replaces or adds).
	CodeClassBad       = "g1_class"
	CodeTenantOff      = "g1_tenant_leg_off"
	CodeTenantNoRow    = "g6t_no_portal_row"
	CodeTenantInactive = "g6t_portal_inactive"
	CodeTenantEmail    = "g6t_email_mismatch"
	CodeTenantRowError = "g6t_portal_error"
)

// AllCodes lists every static code, for the test that keeps them distinct.
func AllCodes() []string {
	return []string{
		CodeBody, CodeRateAddress, CodeConsoleNo, CodeConsoleDown, CodeAccountShape, CodeRateUser,
		CodeClaimExp, CodeClaimReplay, CodeClaimError, CodeEmailUnver, CodeAccountOff, CodeEmailList,
		CodeMethodNo, CodeMethodError, CodeNotMember, CodeMemberError, CodeNoAnchor, CodeAnchorError,
		CodeNoLinkedRow, CodeInactive, CodeNoGrant, CodeStaffError, CodeCeilingError, CodeSessionError,
		CodeSessionMiss, CodeSessionValue, CodeSignInTooOld, CodeLabelError, CodeAuditError, CodeMintError,
		CodeClassBad, CodeTenantOff, CodeTenantNoRow, CodeTenantInactive, CodeTenantEmail, CodeTenantRowError,
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
	// TenantOrgs lists the org slugs of the user's ten-<slug> Teams, the
	// tenant leg's membership read.
	TenantOrgs(ctx context.Context, platformUserID string) ([]string, error)
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

// Portal is the identity module's contractor linkage read
// (identity.PortalRoleResolver).
type Portal interface {
	LinkedPortalUser(ctx context.Context, platformUserID string) (identity.PortalLink, bool, error)
}

// TenantMinter is identity.SessionMinter.MintTenantAt.
type TenantMinter interface {
	MintTenantAt(subject, tenantID string, window time.Duration, at time.Time) (string, error)
}

// TenantLeg is what the tenant class needs beyond the staff collaborators.
// Nil in Config means the tenant class is refused.
type TenantLeg struct {
	Portal Portal
	Minter TenantMinter
}

// Auditor is audit.Service.Record.
type Auditor interface {
	Record(ctx context.Context, a audit.SensitiveAccess) error
}

// Minter is identity.SessionMinter.MintAt.
type Minter interface {
	MintAt(subject string, window time.Duration, at time.Time) (string, error)
}

// SignInMethods is the identity module's narrow reader of the dealer's
// allowed sign in methods (item 6c): the codes the org allows, the default
// set when it has none, and an error for a row it cannot trust. Gate 4b and
// the public methods route both read through it.
type SignInMethods interface {
	AllowedSignInMethods(ctx context.Context, orgID string) ([]string, error)
}

// The provider values of a FB Console session (state note section 5b),
// named so the closed table below reads as data.
const (
	// ProviderEmail is the house sign in, FB Console email and password.
	ProviderEmail = "email"
	// ProviderGoogle is a PLACEHOLDER: the Google provider value has not
	// been read from a real sign in yet. A real Google sign in confirms or
	// corrects this one constant before the Google method is relied on; an
	// unconfirmed value fails closed (a Google session whose provider reads
	// otherwise is refused at gate 4b).
	ProviderGoogle = "google"
	// ProviderMicrosoft is likewise unprobed; Microsoft is built after the
	// demo.
	ProviderMicrosoft = "microsoft"
)

// providerMethod maps a session provider to a method code. It is closed: a
// provider not listed, including the empty value, is refused at gate 4b and
// never assumed to be anything.
var providerMethod = map[string]string{
	ProviderEmail:     identity.MethodEmailPassword,
	ProviderGoogle:    identity.MethodGoogle,
	ProviderMicrosoft: identity.MethodMicrosoft,
}

// The provider values the tenant leg accepts: the email link and Google
// (the boards, row 1592). ProviderMagicURL is a PLACEHOLDER like Google's:
// the value a real email link session carries has not been read, and an
// unconfirmed value fails closed (the session is refused at the method
// gate). The table is closed and fixed in code: the dealer's staff set
// does not decide how a contractor signs in.
const ProviderMagicURL = "magic-url"

var tenantProviders = map[string]bool{
	ProviderMagicURL: true,
	ProviderGoogle:   true,
}

// portalRoleLabels names the contractor roles for the answer's user block.
var portalRoleLabels = map[string]string{
	identity.PortalRoleAccountAdmin: "Account admin",
	identity.PortalRoleBuyer:        "Buyer",
	identity.PortalRoleBuyerNoPay:   "Buyer, no pay",
	identity.PortalRoleAPFinance:    "AP finance",
	identity.PortalRoleFieldCrew:    "Field crew",
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
	// Tenant enables the contractor leg; nil refuses class tenant.
	Tenant *TenantLeg

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
		return nil, errors.New("exchange: the sign in method reader is required")
	case c.TokenTTL < identity.MinSessionWindow:
		return nil, errors.New("exchange: the token window is required")
	case c.Tenant != nil && (c.Tenant.Portal == nil || c.Tenant.Minter == nil):
		return nil, errors.New("exchange: the tenant leg needs the portal reader and the tenant minter")
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
	// Class is optional: empty or "staff" is the staff exchange, "tenant"
	// the contractor leg.
	Class string `json:"class"`
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
		h.refuse400(w, CodeBody, "the body must be one JSON object with the fields jwt, org and class only")
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		h.refuse400(w, CodeBody, "the body must be one JSON object with the fields jwt, org and class only")
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
	tenant := false
	switch in.Class {
	case "", classStaff:
	case classTenant:
		tenant = true
	default:
		h.refuse400(w, CodeClassBad, "class must be staff or tenant when given")
		return
	}
	if tenant && h.cfg.Tenant == nil {
		// The leg is not composed on this deployment. The shared 403, before
		// any address allowance or Console call is spent on it.
		h.refuse403(w, CodeTenantOff)
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
	if tenant {
		h.serveTenant(w, r, now, in, acct, jwt)
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
	orgID, ok := h.resolveAnchor(ctx, w, in.Org)
	if !ok {
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
	session, ok := h.readSession(ctx, w, now, acct.ID, jwt)
	if !ok {
		return
	}
	// Gate 4b rides the same session read: the provider maps through the
	// closed table to a method code, and the code must be in the dealer's
	// allowed set. An unmapped provider is refused before the set is read.
	method, mapped := providerMethod[session.Provider]
	if !mapped {
		h.refuse403(w, CodeMethodNo)
		return
	}
	allowedSet, err := h.cfg.Methods.AllowedSignInMethods(ctx, orgID)
	if err != nil {
		h.refuse503(w, CodeMethodError)
		return
	}
	if !slices.Contains(allowedSet, method) {
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

// resolveAnchor is gate 5b, shared by both classes: the slug against the org
// anchor. No anchor is the same 403 as no membership.
func (h *Handler) resolveAnchor(ctx context.Context, w http.ResponseWriter, org string) (string, bool) {
	orgID, ok, err := h.cfg.Orgs.ResolveSlug(ctx, org)
	if err != nil {
		h.refuse503(w, CodeAnchorError)
		return "", false
	}
	if !ok {
		h.refuse403(w, CodeNoAnchor)
		return "", false
	}
	return orgID, true
}

// readSession is gate 7b's read, shared by both classes: the Console session
// behind the JWT, read server side. No match, or a value it cannot read, is
// never a pass.
func (h *Handler) readSession(ctx context.Context, w http.ResponseWriter, now time.Time, userID string, jwt authz.ConsoleJWT) (*authz.ConsoleSession, bool) {
	sessions, err := h.cfg.Console.UserSessions(ctx, userID)
	if err != nil {
		h.refuse503(w, CodeSessionError)
		return nil, false
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
		return nil, false
	}
	if session.CreatedAt.IsZero() || session.CreatedAt.After(now.Add(sessionClockSkew)) {
		h.refuse503(w, CodeSessionValue)
		return nil, false
	}
	return session, true
}

// serveTenant is the contractor leg after gate 3b. It keeps the Console
// account half of gate 4 (verified, active) but not the staff allow lists
// (a contractor's domain is anyone's), then replaces gates 5, 6 and 7 with
// their tenant forms: membership of the org's ten-<slug> Team and no other,
// a live ACTIVE portal_users row for this platform user whose email is the
// Console account's, and the tenant ceiling. Gates 5b, 7b and 8 are the
// staff ones. The `tenant` claim is the row's own account; nothing in the
// request reaches it.
func (h *Handler) serveTenant(w http.ResponseWriter, r *http.Request, now time.Time, in request, acct authz.ConsoleAccount, jwt authz.ConsoleJWT) {
	ctx := r.Context()

	// Gate 5 (tenant): exactly one tenant Team org, and it is the requested
	// one, read from the uncached source: the per request gate's own rule.
	orgs, err := h.cfg.Members.TenantOrgs(ctx, acct.ID)
	if err != nil {
		h.refuse503(w, CodeMemberError)
		return
	}
	if len(orgs) != 1 || orgs[0] != in.Org {
		h.refuse403(w, CodeNotMember)
		return
	}

	// Gate 5b: the org anchor.
	if _, ok := h.resolveAnchor(ctx, w, in.Org); !ok {
		return
	}

	// Gate 6 (tenant): the live portal row. Never provisioned or linked
	// here: a person with no linked row has no contractor account yet.
	link, ok, err := h.cfg.Tenant.Portal.LinkedPortalUser(ctx, acct.ID)
	switch {
	case errors.Is(err, identity.ErrPortalUserInactive):
		h.refuse403(w, CodeTenantInactive)
		return
	case err != nil:
		h.refuse503(w, CodeTenantRowError)
		return
	case !ok || link.PortalUserID == "" || link.TenantID == "":
		h.refuse403(w, CodeTenantNoRow)
		return
	case !strings.EqualFold(strings.TrimSpace(link.Email), strings.TrimSpace(acct.Email)):
		// The account matches the row, or the sign in is refused: this is
		// what lets Google in beside the email link.
		h.refuse403(w, CodeTenantEmail)
		return
	}

	// Gate 7b: the sign in age, server side, and gate 4b over the closed
	// tenant provider table (the dealer's staff set is not consulted).
	session, ok := h.readSession(ctx, w, now, acct.ID, jwt)
	if !ok {
		return
	}
	if !tenantProviders[session.Provider] {
		h.refuse403(w, CodeMethodNo)
		return
	}
	window := minDuration(TenantSessionCeiling, h.cfg.TokenTTL, session.CreatedAt.Add(TenantSessionCeiling).Sub(now))
	window = window.Truncate(time.Second)
	if window < MinTokenWindow {
		h.refuse401(w, CodeSignInTooOld)
		return
	}

	// Gate 8: the audit row first, then the token.
	if err := h.cfg.Audit.Record(ctx, audit.SensitiveAccess{
		Action:     auditAction,
		EntityType: auditEntityType,
		EntityID:   link.PortalUserID,
		ActorID:    link.PortalUserID,
		Purpose:    auditPurposeTenant,
		Extra:      map[string]any{"actor_subject": acct.ID},
	}); err != nil {
		h.refuse503(w, CodeAuditError)
		return
	}
	token, err := h.cfg.Tenant.Minter.MintTenantAt(acct.ID, link.TenantID, window, now)
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
			RoleCode: link.Role, RoleName: portalRoleLabels[link.Role],
			Tier: 0, Scope: "TENANT",
		},
	})
}
