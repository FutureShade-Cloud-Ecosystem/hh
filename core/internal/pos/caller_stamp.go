package pos

import (
	"context"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// requestOrg is the org fact set the guard resolved for the request: the
// slug the caller selected, the anchor it resolved to, and the platform
// user id behind the verified bearer. The return rules read it to resolve
// the approver's credential in the same org and to tell the clerk's
// credential from the approver's; nothing else in the module reads it.
type requestOrg struct {
	OrgSlug        string
	OrgID          string
	PlatformUserID string
}

// requestOrgKey is pos's own context slot for that fact set. Stamped by the
// handler adapter below from the caller the guard resolved, never from an
// upstream header.
type requestOrgKey struct{}

func withRequestOrg(r *http.Request, caller guards.StaffCaller) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), requestOrgKey{}, requestOrg{
		OrgSlug:        caller.OrgSlug,
		OrgID:          caller.OrgID,
		PlatformUserID: caller.PlatformUserID,
	}))
}

// requestOrgFrom returns the stamped org facts, ok false when the request
// reached the service without the handler adapter (a direct construction,
// the unit tests).
func requestOrgFrom(ctx context.Context) (requestOrg, bool) {
	org, ok := ctx.Value(requestOrgKey{}).(requestOrg)
	return org, ok
}

// CARRY NOTE (guards swap, HH-3.2 fold): the ERP registrar took positional
// middleware from the composition root; under the carry every guarded
// handler registers as g.RequireX(h.staff(...)) and receives the resolved
// caller as its last argument (guards.StaffCaller). staff stamps the
// bridged StaffContext onto the request from that caller before the
// carried body runs: the bridge's request-path adapter (D3), so handlers
// and services reading the caller through the carried context names keep
// reading exactly what they read at the pin, while authority is resolved
// per request by the guard, never trusted from an upstream stamp. The
// optional linker adds the ERP users.id (the B4 linkage ruling:
// users.platform_user_id, supplied by the composition root; nil stamps
// authority only and every id-consuming read fails closed).
func (h *Handler) staff(next guards.StaffHandler) guards.StaffHandler {
	return func(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
		next(w, identity.StampRequest(withRequestOrg(r, caller), caller, h.userLink), caller)
	}
}

// WithUserLinker sets the ERP users.id linker the stamp carries. Nil is
// legal: the stamp is authority-only and id-consuming reads fail closed.
func (h *Handler) WithUserLinker(link identity.UserIDLinker) *Handler {
	h.userLink = link
	return h
}
