package branches

import (
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"log/slog"
	"net/http"
)

// Handler exposes the branch read surface. GET /api/v1/branches is staff-gated (the same
// RequireStaff guard the customer/pos/till counter routes use), a branch list is internal
// operational config, not public catalogue data.
type Handler struct {
	userLink identity.UserIDLinker
	svc      *Service
	log      *slog.Logger
}

// NewHandler wires the service.
func NewHandler(svc *Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes self-registers the staff-gated branch route. requireStaff is the identity
// gate main.go injects (the same guard the customer/pos/till handlers receive).
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	if g == nil {
		// Fail closed: a missing guard would expose operational data to anonymous callers.
		panic("branches.RegisterRoutes: guards are required")
	}
	r.Handle("GET /api/v1/branches", g.RequireStaff(h.staff(h.list)))
}

// callerScope turns the guard-resolved caller into the branch authority ListFor gates on.
//
// internal/identity is imported by THIS FILE AND ONLY THIS FILE, which is the seam
// dashboard/handler.go:20-22 states and this package now needs for the same reason it did: the
// handler has to know who is asking, and nothing below it may see an identity type. Everything
// underneath receives CallerScope, which is two strings and a bool.
//
// A MISSING StaffContext IS "NO GRANTS", NOT "EVERY GRANT". The route is registered behind the
// injected requireStaff guard (RegisterRoutes panics without one), so in a running server there is
// always a caller; the only ways to arrive here without one are a wiring mistake and a test that
// skipped the guard. Both of those must fail CLOSED, a fence whose absence means "show everything"
// is the fence being off, and it would be off in exactly the situation nobody is watching.
func (h *Handler) callerScope(r *http.Request) CallerScope {
	caller, _ := identity.StaffContextFromContext(r.Context())
	if caller.Scope == identity.ScopeGlobal {
		return CallerScope{AllBranches: true}
	}
	ids := make([]string, 0, len(caller.Branches))
	for id, granted := range caller.Branches {
		if granted {
			ids = append(ids, id)
		}
	}
	// Sorted, because map iteration is not an order. dashboard/handler.go:82 sorts the identical
	// list for the identical reason, "so a provider's SQL plan and any test assertion are stable" -
	// and here it decides something a person sees: markCallerDefault lands the picker on the first
	// live grant, so an unsorted list would open two logins of the same person on two different yards.
	return CallerScope{BranchIDs: SortedGrants(ids)}
}

// list backs GET /api/v1/branches, the branches THIS CALLER may reach, each with id, code, name,
// address, is_active, the dealer's is_system_default and the caller-relative is_default (US-M7-06,
// then this session's DEFECT 2). Returns [] (never null) when empty.
//
// THE PAYLOAD IS STILL A BARE JSON ARRAY, and that is a deliberate constraint rather than an
// oversight. Three shipping clients read this route and two of them parse the top level as an array
// outright, app/src/dashboard-api.ts:265 (`Array.isArray(rows) ? … : []`) and
// app/src/quote-builder.ts:287 (`rows.filter(…)`, which throws on an object and degrades the branch
// picker to empty). Wrapping the list in an envelope would have been tidier and would have silently
// emptied the quote builder's branch select on the same deploy, so the new fact went into the ROW
// (is_system_default beside is_default) where every existing parser ignores it and the yard console
// can read it. app/src/staff-roles.ts:305 already unwraps a `branches` envelope if one ever appears,
// so the door back to one is open and costs two frontend edits, not three.
func (h *Handler) list(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	out, err := h.svc.ListFor(r.Context(), h.callerScope(r))
	if err != nil {
		h.log.Error("branches: list failed", "err", err)
		httpx.Error(w, http.StatusServiceUnavailable, "branch lookup failed")
		return
	}
	if out == nil {
		out = []Branch{}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
