// Package guards re-expresses the ERP's staff guards and the portal
// capability guard over the B2 middleware core and the database-resolved
// authority (HH-3.3, batch B3). The ERP's positional middleware
// (RequireStaff, RequireManager, RequireAdmin as func(http.HandlerFunc)
// http.HandlerFunc, identity/handler.go at the pinned commit) cannot carry
// tenant, capability or org scope, so Phase C modules take this package's
// Guards instead: guard constructors that wrap scoped handlers, where a
// handler that cannot fail for lack of scope is unrepresentable (B2
// review-notes item 4). Refusals are the pinned families and bytes: the
// RBAC-403 (authenticated, the gate refused) through authz.RefuseForbidden,
// the ceiling 401 (an authentication-lifetime refusal) through
// authz.RefuseUnauthorized, and every operational inability through
// authz.RefuseUnavailable.
package guards

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

// errNil names a missing required collaborator at construction, so a
// miswired Guards refuses to exist rather than failing at request time.
func errNil(what string) error {
	return fmt.Errorf("guards: %s is required", what)
}

// StaffCaller is the request authority of a verified staff bearer: the
// scope the middleware stamped (org resolved and validated per ADR-0020)
// plus the database-resolved role authority, with the carried predicates
// (IsManager, IsAdmin, CanAccessBranch) promoted from the authority.
type StaffCaller struct {
	authz.StaffScope
	identity.StaffAuthority
}

// StaffHandler is a handler that cannot run without a staff scope and its
// resolved authority: both arrive as arguments, never from context, never
// absent.
type StaffHandler func(w http.ResponseWriter, r *http.Request, caller StaffCaller)

// TenantHandler is a handler that cannot run without a tenant scope.
type TenantHandler func(w http.ResponseWriter, r *http.Request, scope authz.TenantScope)

// SyncCaller carries the one scope a dual-bearer operation resolved:
// exactly one of Staff or Tenant is non-nil on any reached call.
type SyncCaller struct {
	Staff  *StaffCaller
	Tenant *authz.TenantScope
}

// SyncHandler is a handler for the two dual-bearer sync operations
// (ADR-0020: the G4 rule runs bearer-conditioned on that surface).
type SyncHandler func(w http.ResponseWriter, r *http.Request, caller SyncCaller)

// StaffAuthoritySource resolves staff authority per request. The concrete
// reader is identity.StaffAuthorityResolver over the identity tables; the
// seam exists so tests inject doubles and so no guard ever resolves
// authority from the token (D3).
type StaffAuthoritySource interface {
	ResolveStaff(ctx context.Context, platformUserID, orgID string) (identity.StaffAuthority, bool, error)
}

// PortalRoleSource resolves the canonical contractor role per request.
// The concrete reader is identity.PortalRoleResolver over portal_users.
type PortalRoleSource interface {
	PortalRole(ctx context.Context, platformUserID, tenantID string) (string, bool, error)
}

// Guards is the one guard surface Phase C modules take: it composes the
// B2 Gate (verification, org resolution, scoping) with the authority
// resolution this lane adds. Every constructor returns an http.Handler
// built on the scoped wrappers, so a scopeless handler is unrepresentable
// at every registration site. The x-role classes of the contract map onto
// it as: requireStaff, requireManager and requireAdmin onto the three tier
// constructors (and any finer staff predicate onto RequireStaffPredicate,
// which is how the operator class composes when its lane decides what it
// means), tenant and capability:* onto RequireTenant and
// RequireCapability, public onto Public (unguarded), and the sync
// surface's scope-member onto RequireSync.
type Guards struct {
	gate   *authz.Gate
	staff  StaffAuthoritySource
	portal PortalRoleSource
	knob   identity.DriverSessionHours
}

// NewGuards validates the collaborators and returns the Guards. The knob
// source is the one optional collaborator: nil is legal and refuses the
// driver ceiling leg (identity.SessionCeiling fails closed), because the
// concrete knob reader is dispatch-owned and lands with the Phase C
// dispatch module; wiring that needs driver sessions before then must
// inject a reader, never widen the guard.
func NewGuards(gate *authz.Gate, staff StaffAuthoritySource, portal PortalRoleSource, knob identity.DriverSessionHours) (*Guards, error) {
	if gate == nil {
		return nil, errNil("gate")
	}
	if staff == nil {
		return nil, errNil("staff authority source")
	}
	if portal == nil {
		return nil, errNil("portal role source")
	}
	return &Guards{gate: gate, staff: staff, portal: portal, knob: knob}, nil
}

// Public leaves the handler unguarded (the contract's security: []
// operations): no verification, no org resolution, no scope. The wrapper
// exists so every x-role class has one spelling at the registration site;
// it changes nothing about the handler (ADR-0020 leg 2: X-Org on a public
// request is an undeclared header with zero effect).
func (g *Guards) Public(next http.Handler) http.Handler {
	return next
}

// RequireStaff re-expresses the ERP's RequireStaff (identity/handler.go
// authn at the pinned commit): any authenticated staff member with an
// active grant in the request org. No grant is the RBAC-403.
func (g *Guards) RequireStaff(next StaffHandler) http.Handler {
	return g.gate.StaffScoped(func(w http.ResponseWriter, r *http.Request, scope authz.StaffScope) {
		caller, ok := g.resolveCaller(w, r, scope)
		if !ok {
			return
		}
		next(w, r, caller)
	})
}

// RequireManager re-expresses the ERP's RequireManager over the catalog
// tiers: a tier 1 or tier 2 role passes (IsManager), everything else is
// the RBAC-403.
func (g *Guards) RequireManager(next StaffHandler) http.Handler {
	return g.RequireStaffPredicate(func(a identity.StaffAuthority) bool { return a.IsManager() }, next)
}

// RequireAdmin re-expresses the ERP's RequireAdmin: a tier 1 global role
// (IsAdmin), everything else is the RBAC-403.
func (g *Guards) RequireAdmin(next StaffHandler) http.Handler {
	return g.RequireStaffPredicate(func(a identity.StaffAuthority) bool { return a.IsAdmin() }, next)
}

// RequireStaffPredicate guards a staff operation by an arbitrary authority
// predicate over the resolved authority. It exists so the contract's
// remaining staff-side x-role classes (the operator class) compose on this
// one signature when the lane that carries them records what the class
// means; the three tier constructors above are the recorded ones and
// modules use them directly.
func (g *Guards) RequireStaffPredicate(pred func(identity.StaffAuthority) bool, next StaffHandler) http.Handler {
	return g.gate.StaffScoped(func(w http.ResponseWriter, r *http.Request, scope authz.StaffScope) {
		if pred == nil {
			// A missing predicate is a wiring defect, and the guard cannot
			// run its check: refuse closed as an operational inability,
			// never admit for lack of a question.
			authz.RefuseUnavailable(w)
			return
		}
		caller, ok := g.resolveCaller(w, r, scope)
		if !ok {
			return
		}
		if !pred(caller.StaffAuthority) {
			authz.RefuseForbidden(w)
			return
		}
		next(w, r, caller)
	})
}

// RequireTenant guards a tenant operation: the tenant leg of the Gate
// (X-Org rejected, org from the Team, tenant from the token) with the
// scope handed to the handler. It is the contract's x-role tenant.
func (g *Guards) RequireTenant(next TenantHandler) http.Handler {
	return g.gate.TenantScoped(func(w http.ResponseWriter, r *http.Request, scope authz.TenantScope) {
		next(w, r, scope)
	})
}

// RequireCapability re-expresses the ERP's portal capability guard
// (portal/handler.go require over CapabilitiesFor at the pinned commit):
// the tenant leg of the Gate, then the caller's canonical contractor role
// resolved from portal_users keyed by platform user id within the token's
// tenant, then the capability checked against the role's set. No linked
// portal user, or a role without the capability, is the RBAC-403; a
// resolved row that is not active is the 401 family
// (identity.ErrPortalUserInactive: an account-lifetime fact, the ERP's H-4
// recheck and the contract's plain-401 refresh rule); an unreadable role is
// the 503 family. Only the eight capabilities the ERP wraps at the pinned
// commit are guardable; anything else refuses because Capabilities.Has
// knows no other name.
func (g *Guards) RequireCapability(cap identity.Capability, next TenantHandler) http.Handler {
	return g.gate.TenantScoped(func(w http.ResponseWriter, r *http.Request, scope authz.TenantScope) {
		role, ok, err := g.portal.PortalRole(r.Context(), scope.PlatformUserID, scope.TenantID)
		if err != nil {
			if errors.Is(err, identity.ErrPortalUserInactive) {
				authz.RefuseUnauthorized(w)
				return
			}
			authz.RefuseUnavailable(w)
			return
		}
		if !ok || !identity.CapabilitiesFor(role).Has(cap) {
			authz.RefuseForbidden(w)
			return
		}
		next(w, r, scope)
	})
}

// RequireSync guards a dual-bearer sync operation (the contract's two
// /api/v1/sync/{scope} operations, x-role scope-member): the G4 rule runs
// bearer-conditioned in the Gate's Sync, and whichever leg stamped its
// scope decides what the handler receives. The staff leg resolves
// authority and the ceiling exactly like any staff guard; the tenant leg
// carries the stamped scope. The route half of the sync surface is
// HH-3.5's acceptance; this constructor is the middleware half this lane
// proves (QA finding F7).
func (g *Guards) RequireSync(next SyncHandler) http.Handler {
	return g.gate.Sync(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if staffScope, ok := authz.StaffScopeFrom(r.Context()); ok {
			caller, ok := g.resolveCaller(w, r, staffScope)
			if !ok {
				return
			}
			next(w, r, SyncCaller{Staff: &caller})
			return
		}
		if tenantScope, ok := authz.TenantScopeFrom(r.Context()); ok {
			next(w, r, SyncCaller{Tenant: &tenantScope})
			return
		}
		// Unreachable: Sync stamps one scope before the handler runs; fail
		// closed regardless, never proceed scopeless.
		authz.RefuseForbidden(w)
	}))
}

// resolveCaller runs the staff authority chain for one stamped staff
// scope, in a fixed order: authority resolution from the app-role tables
// (operational failures and grant-shape data defects answer the 503
// family, never an admission; a linked users row that is not ACTIVE is
// identity.ErrStaffUserInactive and answers the 401 family, an
// account-lifetime fact in the ERP's errInvalidToken mapping), no active
// grant answering the RBAC-403 (authenticated, the x-role gate refused),
// then the carried driver session ceiling (an uncomputable ceiling answers
// the 503 family; an over-ceiling window answers 401, because it is a
// credential-lifetime fact rather than a gate decision, per the Tech
// Lead's recorded recommendation), and only then any tier predicate the
// caller chose. The credential-lifetime refusals run ahead of the tier
// predicates on purpose: a credential defect is upstream of every gate,
// so the refusal names the credential, not the route.
func (g *Guards) resolveCaller(w http.ResponseWriter, r *http.Request, scope authz.StaffScope) (StaffCaller, bool) {
	authority, has, err := g.staff.ResolveStaff(r.Context(), scope.PlatformUserID, scope.OrgID)
	if err != nil {
		if errors.Is(err, identity.ErrStaffUserInactive) {
			authz.RefuseUnauthorized(w)
			return StaffCaller{}, false
		}
		authz.RefuseUnavailable(w)
		return StaffCaller{}, false
	}
	if !has {
		authz.RefuseForbidden(w)
		return StaffCaller{}, false
	}
	ceiling, err := identity.SessionCeiling(r.Context(), authority.RoleID, scope.OrgID, g.knob)
	if err != nil {
		authz.RefuseUnavailable(w)
		return StaffCaller{}, false
	}
	if !identity.SessionWithinCeiling(scope.IssuedAt, scope.ExpiresAt, ceiling) {
		authz.RefuseUnauthorized(w)
		return StaffCaller{}, false
	}
	return StaffCaller{StaffScope: scope, StaffAuthority: authority}, true
}
