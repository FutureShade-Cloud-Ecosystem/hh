package payments

import (
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// CARRY NOTE (guards swap, HH-3.2 B5): the ERP registrar took positional
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
func (h *ReversalHandler) staff(next guards.StaffHandler) guards.StaffHandler {
	return func(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
		next(w, identity.StampRequest(r, caller, h.userLink), caller)
	}
}

// WithUserLinker sets the ERP users.id linker the stamp carries. Nil is
// legal: the stamp is authority-only and id-consuming reads fail closed.
func (h *ReversalHandler) WithUserLinker(link identity.UserIDLinker) *ReversalHandler {
	h.userLink = link
	return h
}
