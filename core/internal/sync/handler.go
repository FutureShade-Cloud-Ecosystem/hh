package sync

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// maxBodyBytes caps the push request body, the same bound the other write
// surfaces of the core carry.
const maxBodyBytes = 1 << 20 // 1 MiB

// Handler is the sync surface's route half: the two dual-bearer operations
// the contract declares, both behind guards.RequireSync (x-role
// scope-member), so the G4 rule runs bearer-conditioned in the middleware
// before any handler code: X-Org required under StaffBearer, rejected under
// TenantBearer (ADR-0020). Registration follows the D40 registrar form, the
// platform httpx.Registrar as the first parameter.
type Handler struct {
	engine *Engine
	log    *slog.Logger
}

// NewHandler wires the engine. A nil engine is a construction defect and
// refuses loudly: a route surface with nothing behind it is exactly the
// silent fabrication this package refuses elsewhere.
func NewHandler(engine *Engine, log *slog.Logger) (*Handler, error) {
	if engine == nil {
		return nil, errNilEngine
	}
	if log == nil {
		log = slog.Default()
	}
	return &Handler{engine: engine, log: log}, nil
}

// RegisterRoutes self-registers the sync surface: GET syncPull and POST
// syncPush on /api/v1/sync/{scope}, both x-role scope-member behind the
// guards package's RequireSync.
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/sync/{scope}", g.RequireSync(h.pull))
	r.Handle("POST /api/v1/sync/{scope}", g.RequireSync(h.push))
}

// scopeFilter resolves the caller's mandatory scope predicate from the
// stamped scope the guard handed over: org from X-Org under the staff
// bearer, org and tenant from the Team and token under the tenant bearer.
// A caller class the scope's tenancy does not admit is the RBAC-403, the
// shared family body, before any store call, so the refusal leaks nothing
// about the scope's rows. ok false means the refusal was written.
func (h *Handler) scopeFilter(w http.ResponseWriter, caller guards.SyncCaller, info ScopeInfo) (ScopeFilter, bool) {
	if caller.Staff != nil {
		if !info.Admits(false) {
			authz.RefuseForbidden(w)
			return ScopeFilter{}, false
		}
		return ScopeFilter{OrgID: caller.Staff.OrgID, Actor: caller.Staff.PlatformUserID}, true
	}
	if caller.Tenant != nil {
		if !info.Admits(true) {
			authz.RefuseForbidden(w)
			return ScopeFilter{}, false
		}
		return ScopeFilter{OrgID: caller.Tenant.OrgID, TenantID: caller.Tenant.TenantID, Tenant: true, Actor: caller.Tenant.PlatformUserID}, true
	}
	// RequireSync stamps exactly one scope; fail closed regardless.
	authz.RefuseForbidden(w)
	return ScopeFilter{}, false
}

// pull serves syncPull: one page of changes since a seq. since is parsed
// strictly (a malformed or negative value is a client defect this surface
// has no recorded divergence for, and answers 400); limit follows the
// recorded fail-quiet divergence (an unparseable or non-positive value
// reads as the default, an over-maximum clamps, core/api/CONVENTIONS.md).
func (h *Handler) pull(w http.ResponseWriter, r *http.Request, caller guards.SyncCaller) {
	info, ok := Lookup(r.PathValue("scope"))
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "unknown sync scope")
		return
	}
	f, ok := h.scopeFilter(w, caller, info)
	if !ok {
		return
	}
	if _, wired := h.engine.Store(info.Name); !wired {
		httpx.NotImplemented(w, "sync: scope "+info.Name+" is not wired yet")
		return
	}
	since, err := parseSince(r)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	limit := parseLimit(r)
	resp, err := h.engine.Pull(r.Context(), info, f, since, limit)
	if err != nil {
		h.log.Error("sync: pull failed", "scope", info.Name, "err", err)
		httpx.Error(w, http.StatusServiceUnavailable, "sync unavailable")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// push serves syncPush: the outbox replayed in order, one result per
// operation. A pull-only scope's POST is refused in the contract's own
// refusal shape, the 400 Error envelope (the operation does not exist for
// that scope, the same shape class the unknown scope answers with); a
// conflict is never a failed request, it travels per operation in the 200
// body's results.
func (h *Handler) push(w http.ResponseWriter, r *http.Request, caller guards.SyncCaller) {
	info, ok := Lookup(r.PathValue("scope"))
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "unknown sync scope")
		return
	}
	f, ok := h.scopeFilter(w, caller, info)
	if !ok {
		return
	}
	if info.Policy == PolicyPullOnly {
		httpx.Error(w, http.StatusBadRequest, "sync: scope "+info.Name+" is pull-only")
		return
	}
	if _, wired := h.engine.Store(info.Name); !wired {
		httpx.NotImplemented(w, "sync: scope "+info.Name+" is not wired yet")
		return
	}
	var req apigen.SyncPushRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	resp, err := h.engine.Push(r.Context(), info, f, req.Ops)
	if err != nil {
		h.log.Error("sync: push failed", "scope", info.Name, "err", err)
		httpx.Error(w, http.StatusServiceUnavailable, "sync unavailable")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// parseSince reads the since parameter strictly: absent is 0 (the whole
// feed), and a value that is not a non-negative integer is a BadRequest.
// The contract's Since parameter declares minimum 0, and unlike limit this
// parameter carries no recorded fail-quiet divergence.
func parseSince(r *http.Request) (int64, error) {
	v := r.URL.Query().Get("since")
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, httpx.BadRequestError{Msg: "since must be a non-negative integer"}
	}
	return n, nil
}

// parseLimit reads the limit parameter fail-quiet per the recorded
// divergence (core/api/CONVENTIONS.md): unparseable or non-positive reads
// as the default 50, an over-maximum clamps to 200.
func parseLimit(r *http.Request) int {
	limit := httpx.DefaultPageLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > httpx.MaxPageLimit {
		limit = httpx.MaxPageLimit
	}
	return limit
}
