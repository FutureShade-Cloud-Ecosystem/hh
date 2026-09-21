// Package staffcontext is the identity bridge of HH-3.2 batch B2: the ERP
// staff authority vocabulary (rbac.go) and the approvals rail
// (approvals_rail.go, approvals.go) carried inside the target identity
// module under their own names, with resolution bridged onto the Phase B
// world instead of re-pointed at it. The ERP identity package at the pinned
// commit splits for this carry: the HS256 session world (auth.go, the
// sign-in and password routes of handler.go, password_reset.go,
// google_oauth.go, the login-path projections of model.go, and the
// session-issuing halves of service.go and repository.go) is superseded by
// the Phase B verifier and stays behind; the StaffContext/rbac vocabulary
// and the permission_approval_requests rail bridge through this package.
// The ported consumers (gl, dispatch, messaging) arrive in later batches and
// consume this bridge; until then nothing outside the identity module
// imports it. The bridge is the ruled mechanism (Tech Lead review of unit
// A1, ruling a): a wholesale re-point of the hundreds of module-side
// references would convert a mechanical carry into a simultaneous semantic
// rewrite and maximize the predicate-drift class the batch plan names, so
// the semantic decision is confined here, behind tests that prove the tier
// lattice and the scope lattice map exactly, in both directions.
package staffcontext

import (
	"context"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

// StatusActive is the one users.status value that may hold authority,
// carried as the vocabulary name other modules assert when they verify a
// resolved actor. It is the same value the Phase B resolver refuses against
// (identity.StaffStatusActive), aliased so there is one spelling of the
// constant, not two that can drift.
const StatusActive = identity.StaffStatusActive

// RoleMapping is one row of the complete ERP role-name mapping table (the
// F-5 requirement): the canonical staff_role row of migration
// core/db/migrations/0215_identity_audit.sql (id, tier, scope) and the ERP
// role code that row's origin_code carries, which is the name the carried
// predicates key on. The table below is ordered exactly as the 0215 INSERT
// for line review, and bridge_test.go pins every row against the frozen
// catalog facts and against the carried Roles()/AllRoles() definitions.
type RoleMapping struct {
	// Canonical is staff_role.id, the role id the Phase B resolver returns
	// (identity.StaffAuthority.RoleID).
	Canonical string
	// ERPCode is the ERP role code (staff_role.origin_code): the constant
	// space of Roles(), RoleSuperAdmin, RoleDrivers and every allowlist in
	// rbac.go.
	ERPCode string
	// Tier and Scope are the vocabulary values the canonical role maps to.
	Tier  Tier
	Scope Scope
}

// roleMappings is the complete table: the thirteen D3 business roles plus
// the fourteenth platform-owner role the source documents (rbac.go at the
// pin, migration 0043, staff_role is_platform_role). The driver row is the
// knob role (RoleDrivers): the one role whose session length is a dealer
// knob, carried in the catalog as origin_code DRIVERS (0215 records that
// the catalog's second origin code for driver is not carried). The two
// canonical roles with no ERP-side name in the carried vocabulary
// (dealer_admin, fb_operator) are deliberately absent: they have no ERP
// role code to map, and FromAuthority leaves RoleCode empty for them so
// every role-code allowlist fails closed rather than guessing a match.
var roleMappings = []RoleMapping{
	{"general_manager", "GENERAL_MANAGER", TierLeadership, ScopeGlobal},
	{"financial_controller", "FINANCIAL_CONTROLLER", TierLeadership, ScopeGlobal},
	{"hr", "HR", TierLeadership, ScopeGlobal},
	{"branch_manager", "BRANCH_MANAGER", TierManager, ScopeBranch},
	{"sales_manager", "SALES_MANAGER", TierManager, ScopeBranch},
	{"yard_manager", "YARD_MANAGER", TierManager, ScopeBranch},
	{"logistics_manager", "LOGISTICS_MANAGER", TierManager, ScopeBranch},
	{"procurement_manager", "PROCUREMENT_MANAGER", TierManager, ScopeBranch},
	{"payables_receivables", "PAYABLES_RECEIVABLES", TierManager, ScopeGlobal},
	{"inside_sales", "INSIDE_SALES", TierFrontline, ScopeBranch},
	{"outside_sales", "OUTSIDE_SALES", TierFrontline, ScopeBranch},
	{"yard_team", "YARD_TEAM", TierFrontline, ScopeBranch},
	{"driver", RoleDrivers, TierFrontline, ScopeBranch},
	{"super_admin", RoleSuperAdmin, TierLeadership, ScopeGlobal},
}

// erpRoleCodeByCanonical and canonicalRoleIDByERPCode are the two lookup
// halves of the mapping table, derived once from the rows above so the
// table is the single editable artifact.
var (
	erpRoleCodeByCanonical   = buildERPCodeByCanonical()
	canonicalRoleIDByERPCode = buildCanonicalByERPCode()
)

func buildERPCodeByCanonical() map[string]string {
	m := make(map[string]string, len(roleMappings))
	for _, r := range roleMappings {
		m[r.Canonical] = r.ERPCode
	}
	return m
}

func buildCanonicalByERPCode() map[string]string {
	m := make(map[string]string, len(roleMappings))
	for _, r := range roleMappings {
		m[r.ERPCode] = r.Canonical
	}
	return m
}

// ERPCodeFor maps a canonical staff_role id to the ERP role code the
// carried vocabulary uses. ok is false for a canonical role with no ERP
// name (the two non-ERP internal roles) or an unknown id.
func ERPCodeFor(canonicalRoleID string) (code string, ok bool) {
	code, ok = erpRoleCodeByCanonical[canonicalRoleID]
	return code, ok
}

// CanonicalForERPCode maps an ERP role code back to the canonical
// staff_role id. ok is false for a code outside the carried vocabulary.
func CanonicalForERPCode(erpCode string) (canonicalRoleID string, ok bool) {
	id, ok := canonicalRoleIDByERPCode[erpCode]
	return id, ok
}

// ScopeFromCanonical maps the canonical scope vocabulary (staff_role.scope,
// "global"/"branch") onto the carried Scope. An unknown value maps to the
// empty Scope, which fails every scope equality test in the vocabulary
// (fail closed); the Phase B resolver's grant-shape validation means a
// resolved authority only ever carries one of the two named values.
func ScopeFromCanonical(canonicalScope string) Scope {
	switch canonicalScope {
	case identity.ScopeGlobal:
		return ScopeGlobal
	case identity.ScopeBranch:
		return ScopeBranch
	}
	return ""
}

// CanonicalScope maps the carried Scope back onto the canonical vocabulary.
// The empty Scope maps to the empty string, never to a guess.
func CanonicalScope(s Scope) string {
	switch s {
	case ScopeGlobal:
		return identity.ScopeGlobal
	case ScopeBranch:
		return identity.ScopeBranch
	}
	return ""
}

// FromAuthority maps one Phase B resolved authority onto the carried
// StaffContext vocabulary: tier and scope translated, branch grants
// re-keyed onto the vocabulary's map, and RoleCode resolved through the
// mapping table so the carried predicates evaluate unchanged. This is the
// bridge's pure half; CallerFrom is the context-reading half.
//
// UserID is left EMPTY by the bridge on purpose, and the reason is the id
// spaces: the vocabulary's UserID is an ERP users.id (the rail's
// requested_by and decided_by are UUID foreign keys into users), while the
// request path holds the platform user id, a different space entirely. The
// users-row linkage (users.platform_user_id) is the B4 population ruling's
// territory; until it lands, every rail entry point that needs a user id
// takes it as an explicit argument, exactly as the ERP signatures already
// do (FileJEApproval takes requestedBy; DecideApproval takes callerID), and
// the wire functions supply it. No predicate in the vocabulary reads
// UserID except ResolveApprovers, whose input is the candidate pool the
// rail's store reads (real users.id values from SQL), not bridged
// authorities.
//
// Status is stamped ACTIVE because a resolved authority implies it: the
// Phase B resolver refuses a linked users row that is not ACTIVE
// (identity.ErrStaffUserInactive upstream), so an authority that exists at
// all passed the account-lifetime gate. The Status checks in
// CanSellOnCredit and CanCommitDelivery therefore see the honest value for
// linked callers; a caller with no linked users row is admitted by the
// guard chain today (until the B4 linkage ruling) and bridges the same
// way, which is recorded as a finding for that ruling, not silently
// decided here.
//
// A canonical role with no ERP code (the two non-ERP internal roles)
// bridges with an empty RoleCode: tier and scope still map (IsManager,
// IsAdmin, CanAccessBranch answer by tier and scope as the guards do), and
// every role-code allowlist (the rail approvers, the sellers, the
// dispatch committers, IsSuperAdmin) fails closed rather than guessing a
// match.
func FromAuthority(a identity.StaffAuthority) StaffContext {
	code, _ := ERPCodeFor(a.RoleID)
	branches := make(map[string]bool, len(a.Branches))
	for _, b := range a.Branches {
		branches[b] = true
	}
	return StaffContext{
		RoleCode: code,
		Tier:     Tier(a.Tier),
		Scope:    ScopeFromCanonical(a.Scope),
		Status:   StatusActive,
		Branches: branches,
	}
}

// AuthoritySource is the consumer-declared seam over the Phase B staff
// authority resolver (identity.StaffAuthorityResolver satisfies it
// structurally). It exists so the bridge reads the resolved authority
// through the narrowest interface it needs, tests inject doubles, and no
// bridged caller ever resolves authority from a token (D3).
type AuthoritySource interface {
	ResolveStaff(ctx context.Context, platformUserID, orgID string) (identity.StaffAuthority, bool, error)
}

// CallerFrom returns the bridged default the wire functions inject as the
// carried services' callerFrom seam (the ERP services take a
// func(context.Context) (StaffContext, bool) at construction; the order
// module's service.go at the pin is the pattern's evidence). The returned
// function reads the authz staff scope the middleware stamped plus the
// authority the source resolves, and maps both through the bridge: this is
// the ruled mechanism, that StaffContextFromContext's world reads
// authz.StaffScopeFrom plus the resolved authority.
//
// A context stamped by ContextWithStaff wins over resolution, so the
// carried test pattern (stamp a caller, call the service) flows through
// the same injected seam unchanged. Fail closed on every leg: no scope, an
// unreadable authority, or no authority answers not-ok, never a half
// context.
func CallerFrom(source AuthoritySource) func(context.Context) (StaffContext, bool) {
	return func(ctx context.Context) (StaffContext, bool) {
		if c, ok := StaffContextFromContext(ctx); ok {
			return c, true
		}
		scope, ok := authz.StaffScopeFrom(ctx)
		if !ok {
			return StaffContext{}, false
		}
		authority, has, err := source.ResolveStaff(ctx, scope.PlatformUserID, scope.OrgID)
		if err != nil || !has {
			return StaffContext{}, false
		}
		return FromAuthority(authority), true
	}
}

// staffCallerKey is the bridge's context slot. The ERP stamped the caller
// under identity's unexported key through its guards; the bridge owns the
// stamping seam now (the F-4 integration strategy: the carried context
// construction goes through these functions), and the authz scope remains
// the middleware's to stamp, exactly as the authz contract requires (only
// middleware stamps scope; a stamped scope arriving from upstream is never
// trusted).
type staffCallerKey struct{}

// ContextWithStaff stamps a FULL StaffContext onto a context exactly as
// the ERP guards did: the tier/scope-aware stamp for callers (and
// cross-package tests) that must satisfy a carried module's branch-scope
// or manager checks. Request-path adapters that thread a bridged caller
// into a service which reads the context directly stamp
// ContextWithStaff(ctx, FromAuthority(...)) themselves after the guard
// hands them the caller.
func ContextWithStaff(ctx context.Context, sc StaffContext) context.Context {
	return context.WithValue(ctx, staffCallerKey{}, sc)
}

// StaffContextFromContext returns the caller stamped by ContextWithStaff,
// plus ok=false when the context carries no stamp. It is the carried
// accessor shape: sibling modules that read the caller from the context
// (rather than through an injected callerFrom) keep compiling against it.
func StaffContextFromContext(ctx context.Context) (StaffContext, bool) {
	c, ok := ctx.Value(staffCallerKey{}).(StaffContext)
	return c, ok
}

// StaffUserIDFromContext returns the stamped caller's user id (empty when
// the context carries no caller): the trusted, server-resolved caller id
// the carried modules read for audit attribution.
func StaffUserIDFromContext(ctx context.Context) string {
	c, _ := ctx.Value(staffCallerKey{}).(StaffContext)
	return c.UserID
}

// ContextWithStaffUserID stamps a caller id onto a context exactly as the
// guards do. Exposed so a caller that routes manually (and the tests that
// exercise actor threading) can populate the audit actor without the
// unexported context key.
func ContextWithStaffUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, staffCallerKey{}, StaffContext{UserID: userID})
}
