package billing

// caller_stamp_workspacehandler.go, the caller-stamp middleware for the workspace
// handler. The pattern is the same one the register, month-end and AR landing
// handlers use: the ERP registrar took positional middleware from the composition
// root; under the carry every guarded handler registers as g.RequireX(h.staff(...))
// and receives the resolved caller as its last argument. The stamp plumbs the bridged
// StaffContext onto the request from the caller before the carried body runs, so
// handlers and services reading the caller through the carried context names keep
// reading exactly what they read at the pin, while authority is resolved per request
// by the guard, never trusted from an upstream stamp.
//
// An optional user linker adds the ERP users.id (the B4 linkage ruling:
// users.platform_user_id). Nil stamps authority only; every id-consuming read fails
// closed.

import (
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// staff wraps a StaffHandler with the caller-stamp middleware. The wrapped handler is
// what RequireStaff calls; the stamp runs before the route's body so context reads
// for the caller see exactly what the carry pinned.
func (h *WorkspaceHandler) staff(next guards.StaffHandler) guards.StaffHandler {
	return func(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
		next(w, identity.StampRequest(r, caller, h.userLink), caller)
	}
}

// WithUserLinker sets the ERP users.id linker the stamp carries. Nil is legal: the
// stamp is authority-only and id-consuming reads fail closed.
func (h *WorkspaceHandler) WithUserLinker(link identity.UserIDLinker) *WorkspaceHandler {
	h.userLink = link
	return h
}