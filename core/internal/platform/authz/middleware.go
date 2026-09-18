// Package authz is the org and tenant middleware core (ADR-0020,
// docs/adr/0020-org-resolution.md). The order is fixed: JWKS verification
// and bearer class first; public operations resolve no org and X-Org on a
// public request is ignored with zero effect; the staff leg requires
// X-Org, validates Team membership per request (D28) and only then
// resolves the slug against the org anchor and stamps org_id; the tenant
// leg rejects X-Org and stamps org from the Team behind the token and
// tenant from the token. Scoping is stamped typed request context, never
// left to handlers. Refusal statuses are pinned by the ADR: 400 for the
// two X-Org shape legs, 403 for every org refusal with one byte-identical
// body, 401 for identity failures with one body, and the OrgResolver read
// is the only core-database touch, on the success path only, so every
// refused request executes no core-database statement.
package authz

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

// HeaderXOrg is the staff caller's org selection (ADR-0020 decision).
const HeaderXOrg = "X-Org"

// orgSlugPattern is the server-side slug shape pinned by the ADR-0020
// addendum (docs/adr/0020-org-resolution.md, the enforcement vocabulary
// section): the contract's XOrg parameter is a bare string, and this
// pattern pins the vocabulary the platform's org:<slug> team names carry.
// A slug that fails it is a shape failure and answers 400 before any
// platform or database call.
var orgSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// Refusal bodies. One body per family and byte-identical across every leg
// of that family, which is what kills the oracle: no response may reveal
// whether a slug exists, whether a membership exists, or which check
// failed. The strings are unpinned by any decision and are constants in
// one place for the Tech Lead to rule on; none carries request content.
var (
	bodyUnauthorized = []byte(`{"error":"Missing or invalid credentials"}`)
	bodyForbidden    = []byte(`{"error":"Forbidden"}`)
	bodyUnavailable  = []byte(`{"error":"authorization dependencies unavailable"}`)

	bodyMissingOrgHeader    = []byte(`{"error":"missing required header X-Org"}`)
	bodyRepeatedOrgHeader   = []byte(`{"error":"header X-Org must appear exactly once"}`)
	bodyMalformedOrgHeader  = []byte(`{"error":"header X-Org is not a valid org slug"}`)
	bodyOrgHeaderNotAllowed = []byte(`{"error":"header X-Org is not accepted on this route"}`)
)

// Gate is the middleware core. It holds the verifier, the membership
// source and the org resolver, all injected: the gate itself never
// constructs a platform client or a database pool, which is what keeps
// pool acquisition structurally later than org resolution.
type Gate struct {
	verify  *identity.Verifier
	members MembershipSource
	orgs    OrgResolver
}

// NewGate validates the collaborators and returns the gate. nil is a
// construction error, never a runtime surprise.
func NewGate(verifier *identity.Verifier, members MembershipSource, orgs OrgResolver) (*Gate, error) {
	if verifier == nil {
		return nil, errors.New("authz: verifier is required")
	}
	if members == nil {
		return nil, errors.New("authz: membership source is required")
	}
	if orgs == nil {
		return nil, errors.New("authz: org resolver is required")
	}
	return &Gate{verify: verifier, members: members, orgs: orgs}, nil
}

// Public serves a public operation. No verification, no org resolution,
// no scope: X-Org on a public request is an undeclared header with zero
// effect, a named non-rule rather than a silent one (ADR-0020 leg 2). The
// handler is returned unchanged to make the zero effect literal.
func (g *Gate) Public(next http.Handler) http.Handler {
	return next
}

// Staff serves a staff operation (ADR-0020 leg 3): verify, require the
// staff class, require X-Org, validate Team membership per D28, then
// resolve the slug against the org anchor and stamp the staff scope.
func (g *Gate) Staff(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := g.verifyBearer(w, r)
		if !ok {
			return
		}
		if token.Class != identity.ClassStaff {
			refuse(w, http.StatusForbidden, bodyForbidden)
			return
		}
		g.serveStaff(w, r, token, next)
	})
}

// Tenant serves a tenant operation (ADR-0020 leg 4): verify, require the
// tenant class, reject X-Org, resolve the org from the Team behind the
// token, stamp the tenant scope with the tenant from the token.
func (g *Gate) Tenant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := g.verifyBearer(w, r)
		if !ok {
			return
		}
		if token.Class != identity.ClassTenant {
			refuse(w, http.StatusForbidden, bodyForbidden)
			return
		}
		g.serveTenant(w, r, token, next)
	})
}

// Sync serves a dual-bearer operation: the G4 rule runs bearer-conditioned,
// X-Org required under the staff bearer and rejected under the tenant
// bearer, exactly as on the single-class surfaces (ADR-0020, the sync
// paragraph). The route half of that surface is HH-3.5's acceptance; this
// middleware half is what this lane proves (QA finding F7).
func (g *Gate) Sync(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := g.verifyBearer(w, r)
		if !ok {
			return
		}
		switch token.Class {
		case identity.ClassStaff:
			g.serveStaff(w, r, token, next)
		case identity.ClassTenant:
			g.serveTenant(w, r, token, next)
		default:
			// Unreachable: verification refuses unclassifiable tokens.
			refuse(w, http.StatusForbidden, bodyForbidden)
		}
	})
}

// StaffScoped is Staff with the scope handed to the handler by parameter:
// a handler written this way cannot run without a stamped scope.
func (g *Gate) StaffScoped(next func(http.ResponseWriter, *http.Request, StaffScope)) http.Handler {
	return g.Staff(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scope, ok := StaffScopeFrom(r.Context())
		if !ok {
			// Unreachable through Staff, which stamps before calling;
			// fail closed regardless, never proceed scopeless.
			refuse(w, http.StatusForbidden, bodyForbidden)
			return
		}
		next(w, r, scope)
	}))
}

// TenantScoped is Tenant with the scope handed to the handler by
// parameter.
func (g *Gate) TenantScoped(next func(http.ResponseWriter, *http.Request, TenantScope)) http.Handler {
	return g.Tenant(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scope, ok := TenantScopeFrom(r.Context())
		if !ok {
			refuse(w, http.StatusForbidden, bodyForbidden)
			return
		}
		next(w, r, scope)
	}))
}

// serveStaff runs the staff leg after class verification.
func (g *Gate) serveStaff(w http.ResponseWriter, r *http.Request, token identity.Token, next http.Handler) {
	slug, ok := g.staffOrgSelection(w, r)
	if !ok {
		return
	}
	member, err := g.members.OrgMember(r.Context(), token.Subject, slug)
	if err != nil {
		refuse(w, http.StatusServiceUnavailable, bodyUnavailable)
		return
	}
	if !member {
		refuse(w, http.StatusForbidden, bodyForbidden)
		return
	}
	orgID, ok, err := g.orgs.ResolveSlug(r.Context(), slug)
	if err != nil {
		refuse(w, http.StatusServiceUnavailable, bodyUnavailable)
		return
	}
	if !ok {
		// Same body as the no-membership leg: distinguishing a known slug
		// without an anchor row from an absent membership would reopen the
		// directory oracle the 403 body rule closes.
		refuse(w, http.StatusForbidden, bodyForbidden)
		return
	}
	scope := StaffScope{
		PlatformUserID: token.Subject,
		OrgSlug:        slug,
		OrgID:          orgID,
		IssuedAt:       token.IssuedAt,
		ExpiresAt:      token.ExpiresAt,
	}
	next.ServeHTTP(w, r.WithContext(WithStaffScope(r.Context(), scope)))
}

// serveTenant runs the tenant leg after class verification.
func (g *Gate) serveTenant(w http.ResponseWriter, r *http.Request, token identity.Token, next http.Handler) {
	if len(r.Header.Values(HeaderXOrg)) > 0 {
		// The mirror case (ADR-0020): sending a parameter the operation
		// prohibits is the same shape class as omitting one it requires.
		// Checked before any platform call: the rule costs nothing to
		// enforce and the caller learns nothing but its own defect.
		refuse(w, http.StatusBadRequest, bodyOrgHeaderNotAllowed)
		return
	}
	orgs, err := g.members.TenantOrgs(r.Context(), token.Subject)
	if err != nil {
		refuse(w, http.StatusServiceUnavailable, bodyUnavailable)
		return
	}
	if len(orgs) != 1 {
		// No tenant team, or tenant teams across more than one org: both
		// fail closed, one org per tenant account (ADR-0016 rejected
		// tenant above org), same body, no enumeration.
		refuse(w, http.StatusForbidden, bodyForbidden)
		return
	}
	orgID, ok, err := g.orgs.ResolveSlug(r.Context(), orgs[0])
	if err != nil {
		refuse(w, http.StatusServiceUnavailable, bodyUnavailable)
		return
	}
	if !ok {
		refuse(w, http.StatusForbidden, bodyForbidden)
		return
	}
	scope := TenantScope{
		PlatformUserID: token.Subject,
		OrgSlug:        orgs[0],
		OrgID:          orgID,
		TenantID:       token.TenantID,
		IssuedAt:       token.IssuedAt,
		ExpiresAt:      token.ExpiresAt,
	}
	next.ServeHTTP(w, r.WithContext(WithTenantScope(r.Context(), scope)))
}

// staffOrgSelection reads the caller's org selection. The header is
// required by the contract's parameter shape (required: true on every
// staff operation), so its absence is a shape failure and answers 400,
// never 401 (the credential verified) and never 403 (no gate ran;
// spending the RBAC-403 signal on a shape error would blind the
// under-tier sweep). The header value itself never enters any response.
func (g *Gate) staffOrgSelection(w http.ResponseWriter, r *http.Request) (string, bool) {
	values := r.Header.Values(HeaderXOrg)
	if len(values) == 0 {
		refuse(w, http.StatusBadRequest, bodyMissingOrgHeader)
		return "", false
	}
	if len(values) > 1 {
		refuse(w, http.StatusBadRequest, bodyRepeatedOrgHeader)
		return "", false
	}
	slug := values[0]
	if !orgSlugPattern.MatchString(slug) {
		refuse(w, http.StatusBadRequest, bodyMalformedOrgHeader)
		return "", false
	}
	return slug, true
}

// verifyBearer verifies the credential and writes the refusal itself on
// failure: 401 for every who-you-are and malformed case with one body, so
// no client can tell which check failed, and 503 when the verifier could
// not run, which is an operational failure and not a verdict on the
// token. Nothing logs the header or the token.
func (g *Gate) verifyBearer(w http.ResponseWriter, r *http.Request) (identity.Token, bool) {
	raw := r.Header.Get("Authorization")
	if raw == "" {
		refuse(w, http.StatusUnauthorized, bodyUnauthorized)
		return identity.Token{}, false
	}
	token, ok := bearerToken(raw)
	if !ok {
		refuse(w, http.StatusUnauthorized, bodyUnauthorized)
		return identity.Token{}, false
	}
	verified, err := g.verify.Verify(r.Context(), token)
	if err != nil {
		var verr *identity.VerifyError
		if errors.As(err, &verr) && verr.Class() == identity.ClassOperational {
			refuse(w, http.StatusServiceUnavailable, bodyUnavailable)
			return identity.Token{}, false
		}
		refuse(w, http.StatusUnauthorized, bodyUnauthorized)
		return identity.Token{}, false
	}
	return verified, true
}

// bearerToken extracts the bearer credential. The scheme is
// case-insensitive per RFC 6750; any other shape is a 401-class failure.
func bearerToken(header string) (string, bool) {
	scheme, rest, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "bearer") || rest == "" || strings.ContainsAny(rest, " \t") {
		return "", false
	}
	return rest, true
}

// refuse writes one refusal family body. Every 403 in this package passes
// through here with the same bytes, and every 401 likewise, which is the
// invariant the sweeps assert byte for byte.
func refuse(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// After the status is sent there is no recovery path and nothing may
	// be derived from a body-write error, so it is not branched on.
	_, _ = w.Write(body)
}
