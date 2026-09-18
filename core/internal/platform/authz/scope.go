package authz

import (
	"context"
	"time"
)

// StaffScope is the org-resolved request scope of a verified staff bearer.
// It carries identity and org context only: role, tier and branch grants
// are resolved from the database per request, never from the token (D3).
// OrgID is the org anchor id (migration
// core/db/migrations/0213_org_tenant_seq_deleted_idem.sql), resolved
// through the OrgResolver only after Team membership passed (ADR-0020).
// IssuedAt and ExpiresAt are carried so later stages can enforce session
// ceilings without re-parsing the token.
type StaffScope struct {
	PlatformUserID string
	OrgSlug        string
	OrgID          string
	IssuedAt       time.Time
	ExpiresAt      time.Time
}

// TenantScope is the request scope of a verified tenant bearer. The org
// comes from the Team behind the token and the tenant (the contractor
// account) from the token, with branch beneath both (ADR-0016 C33).
type TenantScope struct {
	PlatformUserID string
	OrgSlug        string
	OrgID          string
	TenantID       string
	IssuedAt       time.Time
	ExpiresAt      time.Time
}

// requestScope is the single context slot for request scope. At most one
// kind is ever set: a request is one bearer class or it is refused.
type requestScope struct {
	staff  *StaffScope
	tenant *TenantScope
}

type scopeCtxKey struct{}

// WithStaffScope stamps the staff scope, replacing any scope already
// present. Only middleware stamps scope; a stamped scope arriving from
// upstream is never trusted (the middleware overwrites it with verified
// values).
func WithStaffScope(ctx context.Context, scope StaffScope) context.Context {
	return context.WithValue(ctx, scopeCtxKey{}, &requestScope{staff: &scope})
}

// WithTenantScope stamps the tenant scope, replacing any scope already
// present.
func WithTenantScope(ctx context.Context, scope TenantScope) context.Context {
	return context.WithValue(ctx, scopeCtxKey{}, &requestScope{tenant: &scope})
}

// StaffScopeFrom reports the staff scope of the request. ok is false
// unless the staff leg of the middleware stamped it, which is the
// fail-closed accessor: code that needs a staff scope must refuse when ok
// is false, so scoped access without scope is impossible by construction
// (the repo invariant: scoping is enforced in middleware, never left to
// individual queries).
func StaffScopeFrom(ctx context.Context) (scope StaffScope, ok bool) {
	rs, _ := ctx.Value(scopeCtxKey{}).(*requestScope)
	if rs == nil || rs.staff == nil {
		return StaffScope{}, false
	}
	return *rs.staff, true
}

// TenantScopeFrom reports the tenant scope of the request, with the same
// fail-closed contract as StaffScopeFrom.
func TenantScopeFrom(ctx context.Context) (scope TenantScope, ok bool) {
	rs, _ := ctx.Value(scopeCtxKey{}).(*requestScope)
	if rs == nil || rs.tenant == nil {
		return TenantScope{}, false
	}
	return *rs.tenant, true
}
