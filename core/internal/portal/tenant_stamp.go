package portal

import (
	"context"
	"errors"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// CARRY NOTE (the bearer split, HH-3.2 fold, R6): the ERP portal verified
// its own HS256 contractor token per request (h.caller over
// Service.Authenticate). Under the target the tenant bearer arrives
// verified by the authz gate, and RequireTenant / RequireCapability hand
// every guarded leg the resolved TenantScope; this adapter resolves the
// module's own PortalContext from that scope and stamps it on the request
// under the same callerKey the carried handlers already read, so no
// feature handler changes. The scope's TenantID is the contractor account
// (portal_users.tenant_id equals the row's own customer_id, the 0213
// equality ADR-0020 records), and the row's id and role are re-read per
// request through the module's own table: the H-4 posture the HS256 path
// had (claims stale, database current) carries over unchanged.
func (h *Handler) tenant(next http.HandlerFunc) guards.TenantHandler {
	return func(w http.ResponseWriter, r *http.Request, scope authz.TenantScope) {
		caller, err := h.svc.ResolveTenantCaller(r.Context(), scope.PlatformUserID, scope.TenantID)
		if err != nil {
			httpx.Error(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), callerKey, caller)))
	}
}

// errNoTenantCaller names a tenant-scope caller the module could not
// resolve to a portal_users row: the adapter refuses (401 family), never
// an empty caller, so a feature handler never runs unscoped.
var errNoTenantCaller = errors.New("portal: no portal user for this tenant caller")

// ResolveTenantCaller reads the caller's own portal_users row by platform
// user id within the token's tenant, live and active only. The read is the
// module's own table through its own repository; a missing or inactive row
// is an error the adapter answers with the 401 family (the guard has
// already refused a non-active row on the capability legs; this is the
// plain-leg half of the same posture).
func (s *Service) ResolveTenantCaller(ctx context.Context, platformUserID, tenantID string) (PortalContext, error) {
	if s == nil || platformUserID == "" || tenantID == "" {
		return PortalContext{}, errNoTenantCaller
	}
	return s.store.ResolveTenantUser(ctx, platformUserID, tenantID)
}
