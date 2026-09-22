package tasks

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// maxBodyBytes caps request bodies (parity with the other ERP write surfaces).
const maxBodyBytes = 1 << 20 // 1 MiB

// Handler exposes the tasks REST surface. Every route is staff-gated (requireStaff), the picking queue
// is an internal ops tool; the caller (created_by / claimed_by / the audit actor) is resolved from the
// verified StaffContext the guard stamps, never a client-supplied id.
type Handler struct {
	userLink identity.UserIDLinker
	svc      *Service
	log      *slog.Logger
}

func NewHandler(svc *Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes self-registers the tasks routes. All staff-gated: create-from-order, the picking-queue
// list projection, and the three lifecycle transitions (+ cancel, which makes the CANCELLED terminal
// state reachable). The exact state machine enforced downstream is:
//
//	QUEUED/PUSHED → ACCEPTED (claim) → IN_PROGRESS (accept) → DONE (done); any non-terminal → CANCELLED (cancel).
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("POST /api/v1/tasks", g.RequireStaff(h.staff(h.create)))
	r.Handle("GET /api/v1/tasks", g.RequireStaff(h.staff(h.list)))
	r.Handle("POST /api/v1/tasks/{id}/claim", g.RequireStaff(h.staff(h.claim)))
	r.Handle("POST /api/v1/tasks/{id}/accept", g.RequireStaff(h.staff(h.accept)))
	r.Handle("POST /api/v1/tasks/{id}/done", g.RequireStaff(h.staff(h.done)))
	r.Handle("POST /api/v1/tasks/{id}/cancel", g.RequireStaff(h.staff(h.cancel)))
}

type createRequest struct {
	OrderID         string `json:"order_id"`
	Source          string `json:"source"`            // optional; defaults to CHAT
	SourceMessageID string `json:"source_message_id"` // optional; the chat message that spawned it
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var req createRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	t, err := h.svc.CreateFromOrder(r.Context(), CreateInput{
		OrderID:         req.OrderID,
		Source:          req.Source,
		CreatedBy:       identity.StaffUserIDFromContext(r.Context()),
		SourceMessageID: req.SourceMessageID,
	})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, t)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	// Cursor pagination (HH-3.2 B5). Stable-ordering basis: open-first
	// flag, priority rank, created_at, id tie-breaker; the id completes
	// the key the pin's three-column order left ambiguous, and the cursor
	// is that tuple. Fetch limit+1 to learn whether a further page exists,
	// drop the extra row, mint next_cursor from the last kept row.
	p, err := httpx.ParseCursorPage(r)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "cursor is malformed")
		return
	}
	active, rank, created, id, ok := parseTaskCursor(p.Cursor)
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "cursor is malformed")
		return
	}
	items, err := h.svc.ListTasks(r.Context(), TaskFilter{
		Status:          r.URL.Query().Get("status"),
		BranchID:        r.URL.Query().Get("branch_id"),
		Limit:           p.Limit + 1,
		AfterCursor:     p.Cursor != "",
		CursorActive:    active,
		CursorPriority:  rank,
		CursorCreatedAt: created,
		CursorID:        id,
	})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	next := ""
	if len(items) > p.Limit {
		items = items[:p.Limit]
		next = taskCursorKey(items[len(items)-1])
	}
	httpx.WriteCursorPage(w, items, next, p.Limit)
}

// taskOpenFlag and taskPriorityRank re-derive the two computed legs of the
// queue's stable-ordering key from a row the page already returned (the
// same expressions the repository's ORDER BY computes in SQL).
func taskOpenFlag(t Task) int {
	if t.Status == "QUEUED" || t.Status == "PUSHED" {
		return 0
	}
	return 1
}

func taskPriorityRank(t Task) int {
	switch t.Priority {
	case "URGENT":
		return 0
	case "HIGH":
		return 1
	case "NORMAL":
		return 2
	default:
		return 3
	}
}

// taskCursorKey mints the queue's stable-ordering key. The flag leg is
// stored inverted (open first = 0) so every leg of the tuple ascends in the
// same direction the keyset predicate compares.
func taskCursorKey(t Task) string {
	return fmt.Sprintf("%d|%d|%s|%s",
		taskOpenFlag(t), taskPriorityRank(t), t.CreatedAt.UTC().Format(time.RFC3339Nano), t.ID)
}

// parseTaskCursor decodes a minted key; ok is false for an empty cursor
// (first page) or one that does not carry the tuple shape (already refused
// by ParseCursorPage's token check, refused again here rather than read as
// page one).
func parseTaskCursor(key string) (active, rank int, created time.Time, id string, ok bool) {
	if key == "" {
		return 0, 0, time.Time{}, "", true
	}
	parts := strings.SplitN(key, "|", 4)
	if len(parts) != 4 {
		return 0, 0, time.Time{}, "", false
	}
	var err1, err2, err3 error
	active, err1 = strconv.Atoi(parts[0])
	rank, err2 = strconv.Atoi(parts[1])
	created, err3 = time.Parse(time.RFC3339Nano, parts[2])
	if err1 != nil || err2 != nil || err3 != nil || !looksLikeUUID(parts[3]) {
		return 0, 0, time.Time{}, "", false
	}
	return active, rank, created, parts[3], true
}

func (h *Handler) claim(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	h.doTransition(w, r, func(id, actor string) (Task, error) { return h.svc.Claim(r.Context(), id, actor) })
}

func (h *Handler) accept(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	h.doTransition(w, r, func(id, actor string) (Task, error) { return h.svc.Accept(r.Context(), id, actor) })
}

type doneRequest struct {
	PhotoURL string `json:"photo_url"` // optional DONE-capture proof
}

func (h *Handler) done(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	// TODO(GATED, DIB-191 SCOPE): completing a task does NOT advance the underlying order's status in v1.
	// Whether/how a DONE PICK task should push order.status (and via which order.SetStatus transition) is
	// an open product ruling, not an engineering call. Tasks stay purely operational; the hook would land
	// in Service.Done (see its GATED note) once the decision is made.
	var req doneRequest
	if !decodeJSONOptional(w, r, &req) {
		return
	}
	h.doTransition(w, r, func(id, actor string) (Task, error) { return h.svc.Done(r.Context(), id, actor, req.PhotoURL) })
}

type cancelRequest struct {
	Note string `json:"note"` // optional cancel reason (recorded on the task_events row)
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var req cancelRequest
	if !decodeJSONOptional(w, r, &req) {
		return
	}
	h.doTransition(w, r, func(id, actor string) (Task, error) { return h.svc.Cancel(r.Context(), id, actor, req.Note) })
}

// doTransition is the shared transition-handler body: resolve the staff caller, call the move, map the
// result. The task id comes straight from the path, the repository UUID-guards it (malformed → 404).
func (h *Handler) doTransition(w http.ResponseWriter, r *http.Request, move func(id, actor string) (Task, error)) {
	actor := identity.StaffUserIDFromContext(r.Context())
	t, err := move(r.PathValue("id"), actor)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, t)
}

// decodeJSON strictly decodes a required JSON body (empty/malformed → 400).
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(dst); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	return true
}

// decodeJSONOptional tolerates an absent body (EOF is fine, the fields are all optional); only
// malformed JSON is a 400. Mirrors the messaging ack decode.
func decodeJSONOptional(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(dst); err != nil && !errors.Is(err, io.EOF) {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	return true
}

// writeErr maps service errors to HTTP without leaking internals. Bad input → 400; unknown order on
// create → 422; missing task → 404; an illegal or duplicate transition → 409; DB down → 503.
func (h *Handler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var br httpx.BadRequestError
	switch {
	case errors.As(err, &br):
		httpx.Error(w, http.StatusBadRequest, br.Msg)
	case errors.Is(err, ErrUnknownOrder):
		httpx.Error(w, http.StatusUnprocessableEntity, "order not found")
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "task not found")
	case errors.Is(err, ErrIllegalTransition):
		httpx.Error(w, http.StatusConflict, "illegal task transition")
	case errors.Is(err, ErrNoTransition):
		httpx.Error(w, http.StatusConflict, "task already in that status")
	case errors.Is(err, errNoDB):
		httpx.Error(w, http.StatusServiceUnavailable, "database unavailable")
	default:
		h.log.Error("tasks: internal error", "method", r.Method, "path", r.URL.Path, "err", err)
		httpx.Error(w, http.StatusInternalServerError, "internal error")
	}
}
