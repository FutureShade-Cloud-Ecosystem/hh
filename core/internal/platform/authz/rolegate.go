// The role gate (D48, D49, plan item RG): the dealer-configuration wall
// over the staff surface. The x-role tier gates decide whether a caller
// holds a staff role at all and of which tier; this gate decides what the
// caller's own role may reach inside that: every staff route carries, in
// the contract's x-access annotation, the surfaces it serves and the level
// it needs on at least one of them, and the gate reads the caller's role's
// configured level on those surfaces from the loaded role configuration.
// The admission rule is the plan's: admitted when the role's level is at
// least the route's level on at least one served surface, refused with the
// RBAC-403 when it is below on every one. The token carries no role (D3):
// the role resolves per request through the same StaffAuthorityResolver
// the tier guards read, never from the credential.
//
// Position and cost, stated plainly: the registrar (rolegate_registrar.go)
// attaches this gate at the registration surface, around the whole handler
// whose chain begins with the tier guard, so the gate runs AHEAD of the
// tier guard in the chain and cannot read the scope the guard stamps
// later. It therefore re-establishes the staff facts itself through the
// same Gate internals (staffScopeFor below): verification, staff class,
// the X-Org shape rule, Team membership and slug resolution, the same
// checks serveStaff runs, writing the same byte-identical refusal
// families, so a caller cannot tell which gate refused. The cost is one
// duplicated verification, membership read (carried cache) and org
// resolution per staff request, beside the duplicated authority
// resolution; folding this gate into the guards' caller chain would halve
// those reads and stays open to the comprehensive review. Refusal order
// is unobservable: the tier gate's and this gate's refusals are the same
// RBAC-403 body, so either order answers the caller identically.
//
// The configuration arrives loaded and validated or not at all: the gate
// takes a *roleconfig.Config by construction and refuses to exist without
// one, and the composition root's load is the startup contract (a refused
// configuration stops the core, never a silent default). A per dealer
// configuration is a different Config built by roleconfig.Validate on a
// modified document: the gate's code does not change.
package authz

import (
	"context"
	"errors"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// StaffRoleSource resolves the caller's staff role per request. The
// concrete reader is identity.StaffAuthorityResolver over the identity
// tables (the same instance the tier guards hold); the narrow seam here
// keeps the gate honest about the one fact it reads, the role id, and
// lets tests inject doubles.
type StaffRoleSource interface {
	ResolveStaff(ctx context.Context, platformUserID, orgID string) (identity.StaffAuthority, bool, error)
}

// RouteAccess is one staff route's x-access annotation: the surfaces the
// route serves and the level it needs on at least one of them. The
// registrar builds it from the census and this package validates it at
// attach time, so a malformed annotation refuses the wiring and never
// reaches a request.
type RouteAccess struct {
	Surfaces []string
	Level    roleconfig.Level
}

// valid checks the annotation against roleconfig's vocabulary. An empty
// surface list, an unknown surface, or an unknown level is a wiring
// defect: the gate cannot know what it would be guarding.
func (a RouteAccess) valid() error {
	if len(a.Surfaces) == 0 {
		return errors.New("authz: route access names no surface")
	}
	for _, s := range a.Surfaces {
		if !roleconfig.IsSurface(s) {
			return errors.New("authz: route access names unknown surface " + s)
		}
	}
	switch a.Level {
	case roleconfig.LevelNone, roleconfig.LevelRead, roleconfig.LevelWork, roleconfig.LevelManage, roleconfig.LevelAdmin:
	default:
		return errors.New("authz: route access names unknown level " + string(a.Level))
	}
	return nil
}

// RoleGate is the dealer-configuration gate over the staff surface. It
// holds the middleware core, the caller's role source and the loaded
// configuration, all injected; it never loads, never caches, and never
// falls back.
type RoleGate struct {
	gate  *Gate
	roles StaffRoleSource
	cfg   *roleconfig.Config
}

// NewRoleGate validates the collaborators and returns the gate. A nil
// middleware core, role source or configuration is a construction error,
// never a runtime surprise: a gate that cannot ask its question must
// refuse to exist rather than admit for lack of an answer.
func NewRoleGate(gate *Gate, roles StaffRoleSource, cfg *roleconfig.Config) (*RoleGate, error) {
	if gate == nil {
		return nil, errors.New("authz: role gate requires the middleware core")
	}
	if roles == nil {
		return nil, errors.New("authz: role gate requires a staff role source")
	}
	if cfg == nil {
		return nil, errors.New("authz: role gate requires a loaded role configuration")
	}
	return &RoleGate{gate: gate, roles: roles, cfg: cfg}, nil
}

// Middleware attaches the gate to one route. The annotation is validated
// here, at attach time, so a wiring defect fails the boot loudly instead
// of failing open at request time.
func (g *RoleGate) Middleware(access RouteAccess) (func(http.Handler) http.Handler, error) {
	if err := access.valid(); err != nil {
		return nil, err
	}
	surfaces := append([]string(nil), access.Surfaces...)
	level := access.Level
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			scope, ok := g.gate.staffScopeFor(w, r)
			if !ok {
				// The staff leg already wrote its refusal.
				return
			}
			authority, has, err := g.roles.ResolveStaff(r.Context(), scope.PlatformUserID, scope.OrgID)
			if err != nil {
				if errors.Is(err, identity.ErrStaffUserInactive) {
					// An account-lifetime fact, the 401 family, exactly as
					// the tier guards answer it.
					RefuseUnauthorized(w)
					return
				}
				// Resolution failure and grant-shape data defects answer
				// the 503 family: an inability to ask, never an admission.
				RefuseUnavailable(w)
				return
			}
			if !has {
				RefuseForbidden(w)
				return
			}
			// The admission rule: at least one served surface where the
			// role's configured level reaches the route's level. An unknown
			// role name is a grant row outside the configuration's
			// vocabulary: a data defect answered by the 503 family, never
			// read as level none.
			for _, surface := range surfaces {
				cell, err := g.cfg.Level(authority.RoleID, surface)
				if err != nil {
					RefuseUnavailable(w)
					return
				}
				if cell.AtLeast(level) {
					next.ServeHTTP(w, r)
					return
				}
			}
			RefuseForbidden(w)
		})
	}, nil
}

// staffScopeFor re-runs the staff leg's resolution half for a second gate
// on the same request: verification, staff class, the X-Org shape rule,
// Team membership and slug resolution, the same checks serveStaff runs in
// the same order, writing the same refusal families with the same bodies.
// It returns the scope a staff handler would receive; it stamps nothing,
// because the tier guard downstream stamps its own.
func (g *Gate) staffScopeFor(w http.ResponseWriter, r *http.Request) (StaffScope, bool) {
	token, ok := g.verifyBearer(w, r)
	if !ok {
		return StaffScope{}, false
	}
	if token.Class != identity.ClassStaff {
		refuse(w, http.StatusForbidden, BodyForbidden)
		return StaffScope{}, false
	}
	slug, ok := g.staffOrgSelection(w, r)
	if !ok {
		return StaffScope{}, false
	}
	member, err := g.members.OrgMember(r.Context(), token.Subject, slug)
	if err != nil {
		refuse(w, http.StatusServiceUnavailable, BodyUnavailable)
		return StaffScope{}, false
	}
	if !member {
		refuse(w, http.StatusForbidden, BodyForbidden)
		return StaffScope{}, false
	}
	orgID, ok, err := g.orgs.ResolveSlug(r.Context(), slug)
	if err != nil {
		refuse(w, http.StatusServiceUnavailable, BodyUnavailable)
		return StaffScope{}, false
	}
	if !ok {
		// The same body as the no-membership leg, for the same reason
		// serveStaff gives: no directory oracle.
		refuse(w, http.StatusForbidden, BodyForbidden)
		return StaffScope{}, false
	}
	return StaffScope{
		PlatformUserID: token.Subject,
		OrgSlug:        slug,
		OrgID:          orgID,
		IssuedAt:       token.IssuedAt,
		ExpiresAt:      token.ExpiresAt,
	}, true
}
