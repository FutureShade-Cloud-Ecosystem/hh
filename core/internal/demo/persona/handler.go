//go:build demo

// Package persona is POST /api/v1/auth/persona (state note section 5, rulings
// of rows 1524 and 1530): the demo build's tap-a-role entry under the
// console sign in mode. A persona is not an FB Console user. The route
// mints the persona's core session itself, with the deployment's own
// signing key and issuer (the same SessionMinter the exchange uses), for
// one id of the fixed list in flows/personas.go. The subject is
// persona:<id>.
//
// Who may call it depends on the copy, declared by configuration:
//
//   - the testers' copy (CallerPartner): a core bearer from a real partner
//     exchange, verified by the real verifier, whose subject is on the
//     pinned partner list, and is not itself a persona subject, so a
//     persona token cannot chain;
//   - the public copy (CallerOpen): no bearer, limited per address and per
//     address and persona.
//
// Gates 3 to 7b of the exchange do not run: there is no FB Console account
// behind a persona. Its ceiling is its role's, judged at mint, and is the
// token window itself (no Console session, no refresh). A refusal uses the
// shared bodies of the authz middleware, one per status, so no refusal says
// which check failed; the failed check goes to a static log code and a
// counter, never to the wire. The audit row is written before the token.
//
// The route exists only in the demo build, console mode. The default build
// does not compile this package, and the demo mode registers the old mint
// routes instead.
package persona

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/flows"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/ratelimit"
)

// CallerMode is the copy's declared caller rule (CORE_PERSONA_CALLER).
type CallerMode string

const (
	// CallerPartner is the testers' copy: a partner's core bearer.
	CallerPartner CallerMode = "partner"
	// CallerOpen is the public copy: no bearer, rate limited.
	CallerOpen CallerMode = "open"
)

// The allowances, named so the sizing is one reviewed change. A team behind
// one edge shares an address, so the per address allowance admits a room of
// testers tapping cards; the per address and persona allowance bounds one
// card, which the person taps again only when the token window ends.
const (
	AddressAllowance        = 40
	AddressPersonaAllowance = 10
	LimitWindow             = time.Minute
)

const (
	maxBodyBytes = 1 << 10
	// auditAction and the entity vocabulary of the persona entry's row.
	auditAction     = "AUTH_PERSONA_ENTRY"
	auditEntityType = "auth.persona"
	auditPurpose    = "PERSONA_ENTRY"
)

// The static failure codes: where a refusal is recorded. They are log words
// and counter keys, never response content.
const (
	CodeRateAddress     = "p_rate_address"
	CodeRatePersona     = "p_rate_persona"
	CodeNoBearer        = "p_no_bearer"
	CodeBearerRefused   = "p_bearer_refused"
	CodeVerifierDown    = "p_verifier_unavailable"
	CodeBearerClass     = "p_bearer_class"
	CodeChained         = "p_persona_chain"
	CodeCallerNotListed = "p_caller_not_listed"
	CodeCallerNoRow     = "p_caller_no_row"
	CodeCallerRowError  = "p_caller_row_error"
	CodeBody            = "p_body"
	CodeUnknownPersona  = "p_unknown_persona"
	CodeContractor      = "p_contractor_persona"
	CodeAnchorMissing   = "p_no_anchor"
	CodeAnchorError     = "p_anchor_error"
	CodePersonaNoRow    = "p_persona_no_row"
	CodePersonaRowError = "p_persona_row_error"
	CodeNoAuthority     = "p_no_authority"
	CodeRoleDrift       = "p_role_drift"
	CodeOpenAdmin       = "p_open_admin"
	CodeCeilingError    = "p_ceiling_error"
	CodeAuditError      = "p_audit_error"
	CodeMintError       = "p_mint_error"
)

// AllCodes lists every static code, for the test that keeps them distinct.
func AllCodes() []string {
	return []string{
		CodeRateAddress, CodeRatePersona, CodeNoBearer, CodeBearerRefused, CodeVerifierDown, CodeBearerClass,
		CodeChained, CodeCallerNotListed, CodeCallerNoRow, CodeCallerRowError, CodeBody, CodeUnknownPersona,
		CodeContractor, CodeAnchorMissing, CodeAnchorError, CodePersonaNoRow, CodePersonaRowError,
		CodeNoAuthority, CodeRoleDrift, CodeOpenAdmin, CodeCeilingError, CodeAuditError, CodeMintError,
	}
}

// The collaborators, each the narrow shape the owning module satisfies.

// Verifier is identity.Verifier.
type Verifier interface {
	Verify(ctx context.Context, token string) (identity.Token, error)
}

// Staff is the identity module's staff reads.
type Staff interface {
	LinkedActiveUser(ctx context.Context, platformUserID string) (userID string, ok bool, err error)
	ResolveStaff(ctx context.Context, platformUserID, orgID string) (identity.StaffAuthority, bool, error)
}

// Orgs resolves the slug against the org anchor.
type Orgs interface {
	ResolveSlug(ctx context.Context, orgSlug string) (orgID string, ok bool, err error)
}

// Auditor is audit.Service.Record.
type Auditor interface {
	Record(ctx context.Context, a audit.SensitiveAccess) error
}

// Minter is identity.SessionMinter.MintAt.
type Minter interface {
	MintAt(subject string, window time.Duration, at time.Time) (string, error)
}

// Config is the handler's constructor input.
type Config struct {
	Mode CallerMode
	// Verifier verifies the caller's bearer; required in the partner mode
	// and refused in the open mode, which never reads a bearer.
	Verifier Verifier
	// Partners are the Console user ids that may enter (CORE_PERSONA_PARTNERS),
	// already split. Empty refuses every caller. Refused in the open mode.
	Partners []string
	Staff    Staff
	Orgs     Orgs
	Knob     identity.DriverSessionHours
	Audit    Auditor
	Minter   Minter

	// Trusted names the proxies allowed to speak for a client address.
	Trusted ratelimit.TrustedProxies
	// AddressLimit and PersonaLimit select the allowances; nil picks the
	// named constants.
	AddressLimit *ratelimit.AddressLimit
	PersonaLimit *ratelimit.AddressLimit

	Now func() time.Time
	Log *slog.Logger
}

// Handler serves the persona route.
type Handler struct {
	cfg      Config
	partners map[string]bool
	addrLim  *ratelimit.AddressLimit
	persLim  *ratelimit.AddressLimit
	salt     [16]byte
	now      func() time.Time
	log      *slog.Logger
	mu       sync.Mutex
	failures map[string]int64
}

// New validates the configuration.
func New(c Config) (*Handler, error) {
	switch c.Mode {
	case CallerPartner:
		if c.Verifier == nil {
			return nil, errors.New("persona: the partner caller rule requires the bearer verifier")
		}
	case CallerOpen:
		if c.Verifier != nil || len(c.Partners) > 0 {
			return nil, errors.New("persona: the open caller rule reads no bearer, so it takes neither a verifier nor a partner list")
		}
	default:
		return nil, errors.New("persona: the caller rule must be partner or open")
	}
	switch {
	case c.Staff == nil:
		return nil, errors.New("persona: the staff reader is required")
	case c.Orgs == nil:
		return nil, errors.New("persona: the org resolver is required")
	case c.Knob == nil:
		return nil, errors.New("persona: the driver session knob source is required")
	case c.Audit == nil:
		return nil, errors.New("persona: the audit recorder is required")
	case c.Minter == nil:
		return nil, errors.New("persona: the session minter is required")
	}
	h := &Handler{cfg: c, partners: map[string]bool{}, failures: map[string]int64{}}
	for _, p := range c.Partners {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.Contains(p, ":") {
			return nil, errors.New("persona: a partner id holds a colon, so it is not an FB Console id")
		}
		h.partners[p] = true
	}
	h.addrLim = c.AddressLimit
	if h.addrLim == nil {
		h.addrLim = ratelimit.NewAddressLimit(AddressAllowance, LimitWindow, c.Now)
	}
	h.persLim = c.PersonaLimit
	if h.persLim == nil {
		h.persLim = ratelimit.NewAddressLimit(AddressPersonaAllowance, LimitWindow, c.Now)
	}
	if _, err := rand.Read(h.salt[:]); err != nil {
		return nil, errors.New("persona: no randomness for the address salt")
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

// ParsePartners reads a comma list into trimmed, non empty entries.
func ParsePartners(list string) []string {
	var out []string
	for _, e := range strings.Split(list, ",") {
		if e = strings.TrimSpace(e); e != "" {
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
// request is logged: no token, no id, no address.
func (h *Handler) fail(code string) {
	h.mu.Lock()
	h.failures[code]++
	h.mu.Unlock()
	h.log.Warn("persona entry refused", "code", code)
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
func (h *Handler) refuse400(w http.ResponseWriter) {
	h.fail(CodeBody)
	httpx.Error(w, http.StatusBadRequest, "the body must be one JSON object with the field persona only")
}

type request struct {
	Persona *string `json:"persona"`
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

// bearerToken extracts the bearer credential; the scheme is case
// insensitive and any other shape is a 401 class failure.
func bearerToken(header string) (string, bool) {
	scheme, rest, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "bearer") || rest == "" || strings.ContainsAny(rest, " \t") {
		return "", false
	}
	return rest, true
}

// addressBucket is the salted hash of the client address, spelled with the
// letters a to p only: a digit run in the stored value could be read as an
// identifier by the audit service's cleartext detector and replaced. The
// salt is born with the process, so a bucket names an address within one
// run and the address cannot be recovered from the row.
func (h *Handler) addressBucket(address string) string {
	sum := sha256.Sum256(append(h.salt[:], address...))
	out := make([]byte, 16)
	for i := 0; i < 8; i++ {
		out[2*i] = 'a' + sum[i]>>4
		out[2*i+1] = 'a' + sum[i]&0xf
	}
	return string(out)
}

// ServeHTTP runs the checks in order: the address limit, the caller, the
// body, the persona, the limits that need the persona, the persona's
// authority, then the audit row and the token.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := h.now()
	address := ratelimit.ClientAddress(r, h.cfg.Trusted)

	if !h.addrLim.Allow(address) {
		h.refuse429(w, CodeRateAddress)
		return
	}

	// The caller, before the body, so a caller who is not allowed learns
	// nothing about the body's shape.
	var partnerSubject, partnerRow string
	if h.cfg.Mode == CallerPartner {
		subject, row, ok := h.partner(ctx, w, r)
		if !ok {
			return
		}
		partnerSubject, partnerRow = subject, row
	}

	var in request
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || in.Persona == nil {
		h.refuse400(w)
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		h.refuse400(w)
		return
	}

	p, ok := flows.PersonaByID(*in.Persona)
	if !ok {
		h.refuse403(w, CodeUnknownPersona)
		return
	}
	if p.Contractor {
		h.refuse403(w, CodeContractor)
		return
	}
	if h.cfg.Mode == CallerOpen && !h.persLim.Allow(address+"|"+p.ID) {
		h.refuse429(w, CodeRatePersona)
		return
	}

	orgID, ok, err := h.cfg.Orgs.ResolveSlug(ctx, flows.OrgSlug)
	if err != nil {
		h.refuse503(w, CodeAnchorError)
		return
	}
	if !ok {
		h.refuse503(w, CodeAnchorMissing)
		return
	}

	// The persona's rows were written by the populate. A fixed persona
	// without them is a defect of the box, never an admission.
	usersID, linked, err := h.cfg.Staff.LinkedActiveUser(ctx, p.Subject())
	if err != nil {
		h.refuse503(w, CodePersonaRowError)
		return
	}
	if !linked {
		h.refuse503(w, CodePersonaNoRow)
		return
	}
	authority, ok, err := h.cfg.Staff.ResolveStaff(ctx, p.Subject(), orgID)
	if err != nil {
		h.refuse503(w, CodeNoAuthority)
		return
	}
	if !ok {
		h.refuse503(w, CodeNoAuthority)
		return
	}
	if authority.RoleID != p.Role {
		h.refuse503(w, CodeRoleDrift)
		return
	}
	// The lead's rule, pending the principal's A6 ruling on admin cards on
	// the public copy: a stranger with no bearer never holds a tier 1 global
	// token, whose guards admit every admin route. Judged on the resolved
	// authority, so there is no list of roles to keep in step. The testers'
	// copy keeps the admin cards.
	if h.cfg.Mode == CallerOpen && authority.IsAdmin() {
		h.refuse403(w, CodeOpenAdmin)
		return
	}

	// The ceiling of the winning role, judged now: a driver whose dealer
	// knob is unreadable is refused on purpose, since the knob is the
	// contract. The window is the ceiling itself, in whole seconds so
	// exp - iat is exactly what the guard judges.
	ceiling, err := identity.SessionCeiling(ctx, authority.RoleID, orgID, h.cfg.Knob)
	if err != nil {
		h.refuse503(w, CodeCeilingError)
		return
	}
	window := ceiling.Truncate(time.Second)

	row := audit.SensitiveAccess{
		Action:     auditAction,
		EntityType: auditEntityType,
		EntityID:   usersID,
		Purpose:    auditPurpose,
		Extra:      map[string]any{"subject_ref": p.Subject()},
	}
	if h.cfg.Mode == CallerPartner {
		row.ActorID = partnerRow
		row.Extra["actor_subject"] = partnerSubject
	} else {
		row.ActorID = usersID
		row.MaskedRef = h.addressBucket(address)
	}
	if err := h.cfg.Audit.Record(ctx, row); err != nil {
		h.refuse503(w, CodeAuditError)
		return
	}
	token, err := h.cfg.Minter.MintAt(p.Subject(), window, now)
	if err != nil {
		h.refuse503(w, CodeMintError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, response{
		Token:       token,
		ExpiresAtMs: (now.Unix() + int64(window/time.Second)) * 1000,
		User: userBlock{
			UserID: p.Subject(), Email: p.Email, DisplayName: p.Label,
			RoleCode: authority.RoleID, RoleName: p.Label,
			Tier: authority.Tier, Scope: strings.ToUpper(authority.Scope),
		},
	})
}

// partner judges the testers' copy's caller: a bearer the real verifier
// accepts, of the staff class, whose subject is not a persona subject and is
// on the pinned list, with a linked ACTIVE users row for the audit row. It
// writes the refusal itself and reports false.
func (h *Handler) partner(ctx context.Context, w http.ResponseWriter, r *http.Request) (subject, usersRow string, ok bool) {
	raw := r.Header.Get("Authorization")
	if raw == "" {
		h.refuse401(w, CodeNoBearer)
		return "", "", false
	}
	token, good := bearerToken(raw)
	if !good {
		h.refuse401(w, CodeBearerRefused)
		return "", "", false
	}
	verified, err := h.cfg.Verifier.Verify(ctx, token)
	if err != nil {
		var verr *identity.VerifyError
		if errors.As(err, &verr) && verr.Class() == identity.ClassOperational {
			h.refuse503(w, CodeVerifierDown)
		} else {
			h.refuse401(w, CodeBearerRefused)
		}
		return "", "", false
	}
	if verified.Class != identity.ClassStaff || verified.Subject == "" {
		h.refuse403(w, CodeBearerClass)
		return "", "", false
	}
	if flows.IsPersonaSubject(verified.Subject) || strings.Contains(verified.Subject, ":") {
		h.refuse403(w, CodeChained)
		return "", "", false
	}
	if !h.partners[verified.Subject] {
		h.refuse403(w, CodeCallerNotListed)
		return "", "", false
	}
	row, linked, err := h.cfg.Staff.LinkedActiveUser(ctx, verified.Subject)
	if err != nil {
		h.refuse503(w, CodeCallerRowError)
		return "", "", false
	}
	if !linked {
		h.refuse403(w, CodeCallerNoRow)
		return "", "", false
	}
	return verified.Subject, row, true
}
