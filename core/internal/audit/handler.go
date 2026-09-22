package audit

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// Handler exposes the two read surfaces. The Recorder seam is NOT an HTTP route, siblings call
// Service.Record in-process.
type Handler struct {
	svc *Service
	log *slog.Logger
}

// NewHandler wires the service.
func NewHandler(svc *Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes self-registers the viewer routes. The ERP registrar took two
// positional middleware guards, requireAdmin first and requireStaff second
// (audit/handler.go at the pin); under the carry it takes the Guards
// constructor set, and the mapping is by route, never by parameter position:
// the deep viewer reads keep the admin gate, the operator transaction-reopen
// read keeps the staff gate. The handlers receive the resolved caller as an
// argument (guards.StaffHandler); this module does not read it today, and the
// parameter is kept so the registrar's shape is the one every carried module
// shares.
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/audit", g.RequireAdmin(h.list))
	r.Handle("GET /api/v1/audit/{id}", g.RequireAdmin(h.get))
	r.Handle("GET /api/v1/audit/transaction/{entityType}/{entityId}", g.RequireStaff(h.reopen))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	// Validate the two filter params that flow into ::uuid / ::timestamptz casts BEFORE the query.
	// A malformed value would blow up the cast in Postgres, surfacing as a generic 503 ("audit
	// lookup failed"), a server error for what is really bad client input. Reject it here as a 400
	// instead. Empty stays empty (no-filter), matching the SQL's `$3 = '' OR …` guard.
	actorID := q.Get("actor_id")
	if actorID != "" && !looksLikeUUID(actorID) {
		httpx.Error(w, http.StatusBadRequest, "actor_id must be a valid UUID")
		return
	}
	since := q.Get("since")
	if since != "" {
		if _, err := time.Parse(time.RFC3339, since); err != nil {
			httpx.Error(w, http.StatusBadRequest, "since must be an RFC3339 timestamp")
			return
		}
	}
	f := Filter{
		EntityType: q.Get("entity_type"),
		Action:     q.Get("action"),
		ActorID:    actorID,
		Since:      since,
		Limit:      limit,
	}
	rows, err := h.svc.List(r.Context(), f)
	if err != nil {
		h.log.Error("audit: list failed", "err", err)
		httpx.Error(w, http.StatusServiceUnavailable, "audit lookup failed")
		return
	}
	if rows == nil {
		rows = []Entry{}
	}
	httpx.WriteJSON(w, http.StatusOK, rows)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	// Validate the {id} path param BEFORE it reaches the `WHERE id = $1::uuid` cast. A non-UUID id
	// would raise SQLSTATE 22P02 and surface as a generic 503 ("audit lookup failed"), a server
	// error for what is really bad client input. Reject it as a 400, mirroring list()'s actor_id.
	id := r.PathValue("id")
	if !looksLikeUUID(id) {
		httpx.Error(w, http.StatusBadRequest, "id must be a valid UUID")
		return
	}
	e, err := h.svc.Get(r.Context(), id)
	if err != nil {
		h.log.Error("audit: get failed", "err", err)
		httpx.Error(w, http.StatusServiceUnavailable, "audit lookup failed")
		return
	}
	if e == nil {
		httpx.Error(w, http.StatusNotFound, "audit row not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, e)
}

// reopen is the operator transaction-reopen read path (any authenticated staff). The masking
// projection runs HERE, server-side: admin-only deep fields (FN identifiers, override reasons, raw
// payloads) are stripped before the response leaves the server (US-B1), so a frontline client never
// receives them.
func (h *Handler) reopen(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	// Validate {entityId} before it reaches the `entity_id = $2::uuid` cast (same 22P02→503 class as
	// get()). entityType is a plain `entity_type = $1` text comparison, so it needs no validation.
	entityID := r.PathValue("entityId")
	if !looksLikeUUID(entityID) {
		httpx.Error(w, http.StatusBadRequest, "entityId must be a valid UUID")
		return
	}
	rows, err := h.svc.ReopenTransaction(r.Context(), r.PathValue("entityType"), entityID)
	if err != nil {
		h.log.Error("audit: reopen failed", "err", err)
		httpx.Error(w, http.StatusServiceUnavailable, "audit lookup failed")
		return
	}
	out := make([]Entry, 0, len(rows))
	for _, e := range rows {
		out = append(out, projectForOperator(e))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
