// The role gate (D48, D49, plan item RG, PR 44 review rulings): the
// dealer-configuration wall over the staff surface. The x-role tier gates
// decide whether a caller holds a staff role at all and of which tier;
// this gate decides what the caller's own roles may reach inside that:
// every staff route carries, in the contract's x-access annotation, the
// surfaces it serves and the level it needs on at least one of them, and
// the gate reads the caller's configured levels on those surfaces from the
// loaded role configuration. The token carries no role (D3): the roles
// resolve per request through the identity tables, never from the
// credential.
//
// The whole row, not the winner (X10, the PR 44 review's P2-1 ruling): the
// gate reads every role the caller holds and admits when ANY held role's
// configured level reaches the route's level on at least one served
// surface. A person granted general_manager and inside_sales rings a sale
// on the second grant's work cell though the first grant's read cell would
// refuse it. The platform roles ride the same walk: a caller whose roles
// include one of the three platform roles passes the matrix check.
//
// The platform roles (the PR 44 review's P1-2 ruling, recorded):
// super_admin, dealer_admin and fb_operator stand outside the matrix
// (ROLE-ACCESS-r3 keeps them there; the lead's catalog ruling 10 keeps
// their seats), and the lead ruled the gate must not turn "outside the
// matrix" into "cannot use the product": a caller holding one of the three
// passes this gate's level check on every staff route, and the tier guard
// downstream still applies its own x-role checks to them exactly as before
// this gate existed. A route that additionally names a configuration list
// is not covered by the pass: the list is a named rule's own wall (X6's
// pricing editors for example) and the caller must still hold a listed
// role beside the level.
//
// The named list (the PR 44 review's P2-2 ruling): an x-access block may
// name one of the configuration's role lists; the gate admits only callers
// one of whose roles is on the named list, beside the level check. The
// pricing writes name pricing_editors, so the decided default edits prices
// by the sales manager and the general manager alone, and hr, whose
// dealer-admin cell is admin, is refused the price writes; the loader
// refuses hr on that list, so the default can never name it there.
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
// resolution per staff request, beside the role-list read; folding this
// gate into the guards' caller chain would halve those reads and stays
// open to the comprehensive review. Refusal order is unobservable: the
// tier gate's and this gate's refusals are the same RBAC-403 body, so
// either order answers the caller identically.
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

// The three platform staff roles (0215 seeds them grantable at tier 1;
// ROLE-ACCESS-r3 leaves them outside the matrix). The ruling above binds
// the treatment: the level check passes, the tier guard and any named list
// still apply.
const (
	PlatformRoleSuperAdmin  = "super_admin"
	PlatformRoleDealerAdmin = "dealer_admin"
	PlatformRoleFBOperator  = "fb_operator"
)

// platformStaffRoles is the named list of the three platform role keys.
var platformStaffRoles = []string{PlatformRoleSuperAdmin, PlatformRoleDealerAdmin, PlatformRoleFBOperator}

// isPlatformStaffRole reports whether the role is one of the three.
func isPlatformStaffRole(role string) bool {
	for _, r := range platformStaffRoles {
		if r == role {
			return true
		}
	}
	return false
}

// StaffRolesSource resolves every role the caller holds in the org, the
// whole row the gate walks. The concrete reader is
// identity.StaffAuthorityResolver.ResolveRoles over the identity tables
// (the same instance the tier guards hold); the narrow seam keeps the gate
// honest about the one fact it reads and lets tests inject doubles. An
// empty list with a nil error is no active grant; identity's
// ErrStaffUserInactive rides the error.
type StaffRolesSource interface {
	ResolveRoles(ctx context.Context, platformUserID, orgID string) ([]string, error)
}

// RouteAccess is one staff route's x-access annotation: the surfaces the
// route serves, the level it needs on at least one of them, and the
// optional name of a configuration list whose members alone the route
// serves (pricing_editors on the price writes). The registrar builds it
// from the census and this package validates it at attach time, so a
// malformed annotation refuses the wiring and never reaches a request.
type RouteAccess struct {
	Surfaces []string
	Level    roleconfig.Level
	List     string
}

// listReaders names the lists an annotation may name and reads each from
// the configuration. A list key outside this table is an attach-time
// refusal, never a silent no-list.
var listReaders = map[string]func(*roleconfig.Config) []string{
	roleconfig.SettingPricingEditors: func(c *roleconfig.Config) []string { return c.PricingEditors() },
	// The pricing log names the cost and margin list (D10): the log carries
	// margin figures, so a role off the list (HR, whose Dealer Admin cell is
	// admin) is refused beside the level, exactly as the price writes name
	// their editors.
	roleconfig.SettingCostVisibility: func(c *roleconfig.Config) []string { return c.CostVisibility() },
}

// valid checks the annotation against roleconfig's vocabulary. An empty
// surface list, an unknown surface, an unknown level, none as the level (a
// wall that cannot be switched off, D49: none would admit every role), or
// an unknown list name is a wiring defect: the gate cannot know what it
// would be guarding.
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
	case roleconfig.LevelNone:
		return errors.New("authz: route access level none refuses every role; a route level cannot be none (D49)")
	case roleconfig.LevelRead, roleconfig.LevelWork, roleconfig.LevelManage, roleconfig.LevelAdmin:
	default:
		return errors.New("authz: route access names unknown level " + string(a.Level))
	}
	if a.List != "" {
		if _, ok := listReaders[a.List]; !ok {
			return errors.New("authz: route access names list " + a.List + ", which no reader knows")
		}
	}
	return nil
}

// RoleGate is the dealer-configuration gate over the staff surface. It
// holds the middleware core, the caller's role source and the loaded
// configuration, all injected; it never loads, never caches, and never
// falls back.
type RoleGate struct {
	gate  *Gate
	roles StaffRolesSource
	cfg   *roleconfig.Config
}

// NewRoleGate validates the collaborators and returns the gate. A nil
// middleware core, role source or configuration is a construction error,
// never a runtime surprise: a gate that cannot ask its question must
// refuse to exist rather than admit for lack of an answer.
func NewRoleGate(gate *Gate, roles StaffRolesSource, cfg *roleconfig.Config) (*RoleGate, error) {
	if gate == nil {
		return nil, errors.New("authz: role gate requires the middleware core")
	}
	if roles == nil {
		return nil, errors.New("authz: role gate requires a staff roles source")
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
	listRole := map[string]bool{}
	if access.List != "" {
		for _, r := range listReaders[access.List](g.cfg) {
			listRole[r] = true
		}
	}
	listNamed := access.List != ""
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			scope, ok := g.gate.staffScopeFor(w, r)
			if !ok {
				// The staff leg already wrote its refusal.
				return
			}
			roles, err := g.roles.ResolveRoles(r.Context(), scope.PlatformUserID, scope.OrgID)
			if err != nil {
				if errors.Is(err, identity.ErrStaffUserInactive) {
					// An account-lifetime fact, the 401 family, exactly as
					// the tier guards answer it.
					RefuseUnauthorized(w)
					return
				}
				// Resolution failure answers the 503 family: an inability
				// to ask, never an admission.
				RefuseUnavailable(w)
				return
			}
			if len(roles) == 0 {
				// No active grant: the RBAC-403, the same answer the tier
				// guard gives the same fact.
				RefuseForbidden(w)
				return
			}
			// The admission rule (X10's whole row): at least one held role
			// whose configured level reaches the route's level on at least
			// one served surface, with the platform-role pass above the
			// level check; an unknown role name is a grant row outside the
			// configuration's vocabulary, a data defect answered by the
			// 503 family, never read as level none.
			levelOK, err := g.levelAdmits(roles, surfaces, level)
			if err != nil {
				RefuseUnavailable(w)
				return
			}
			// The named list is the rule's own wall beside the level: a
			// caller no held role puts on the list is refused whatever its
			// cells say (X6: only a pricing editor edits prices). The
			// platform-role pass covers the level check alone, never the
			// list.
			if listNamed {
				onList := false
				for _, role := range roles {
					if listRole[role] {
						onList = true
						break
					}
				}
				if !onList {
					RefuseForbidden(w)
					return
				}
			}
			if !levelOK {
				RefuseForbidden(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

// levelAdmits is the gate's level walk, one function for the request path and for Holds so the two
// can never disagree: true when at least one held role is a platform role or has a configured level
// that reaches the route's level on at least one served surface. An unknown role name is a grant
// row outside the configuration's vocabulary, a data defect, and answers the error (the request
// path answers it with the 503 family), never level none.
func (g *RoleGate) levelAdmits(roles, surfaces []string, level roleconfig.Level) (bool, error) {
	for _, role := range roles {
		if isPlatformStaffRole(role) {
			return true, nil
		}
		for _, surface := range surfaces {
			cell, err := g.cfg.Level(role, surface)
			if err != nil {
				return false, err
			}
			if cell.AtLeast(level) {
				return true, nil
			}
		}
	}
	return false, nil
}

// Holds reports whether the platform user holds, in the org, the access a route's annotation names,
// by the very walk the gate runs on a request (levelAdmits above, and the named list when the access
// names one). It is the answer to "would the gate admit me to that route", asked ahead of the call,
// so a client can decline to offer an action the caller would be refused. The error is the 503
// family's cause: the roles could not be read, or the caller holds a role outside the
// configuration's vocabulary. An inactive account is the identity package's error, passed through.
func (g *RoleGate) Holds(ctx context.Context, platformUserID, orgID string, access RouteAccess) (bool, error) {
	if err := access.valid(); err != nil {
		return false, err
	}
	roles, err := g.roles.ResolveRoles(ctx, platformUserID, orgID)
	if err != nil {
		return false, err
	}
	if len(roles) == 0 {
		return false, nil
	}
	levelOK, err := g.levelAdmits(roles, access.Surfaces, access.Level)
	if err != nil || !levelOK {
		return false, err
	}
	if access.List != "" {
		for _, listed := range listReaders[access.List](g.cfg) {
			for _, role := range roles {
				if role == listed {
					return true, nil
				}
			}
		}
		return false, nil
	}
	return true, nil
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
