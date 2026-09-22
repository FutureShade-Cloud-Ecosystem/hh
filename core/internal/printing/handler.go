package printing

import (
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"log/slog"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// Handler exposes the printing module's HTTP surface. Every route is RequireStaff-gated at
// registration; the per-document branch authority is the service's (see ReceiptService.Receipt).
type Handler struct {
	userLink identity.UserIDLinker
	receipts *ReceiptService
	queue    *QueueService
	log      *slog.Logger
}

// NewHandler wires the services.
func NewHandler(receipts *ReceiptService, queue *QueueService, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{receipts: receipts, queue: queue, log: log}
}

// RegisterRoutes self-registers the printing routes.
//
// The receipt lives under the POS sale's path (GET /api/v1/pos/sales/{id}/receipt) because that is
// the resource it is a representation OF, the operator holds a sale id, not a "document id". The
// printing MODULE still owns the handler: receipts, labels and delivery docs are one concern, and
// none of them belongs to the counter service.
func (h *Handler) RegisterRoutes(mux *http.ServeMux, g *guards.Guards) {
	mux.Handle("GET /api/v1/pos/sales/{id}/receipt", g.RequireStaff(h.staff(h.receipt)))
	mux.Handle("GET /api/v1/printing/jobs", g.RequireStaff(h.staff(h.listJobs)))
}

// receipt renders the print-CSS receipt document for a committed sale.
func (h *Handler) receipt(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	rec, err := h.receipts.Receipt(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	html, err := RenderReceiptHTML(rec)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// A receipt is a point-in-time document carrying a reprint counter, a cached copy would show a
	// stale marker, which is the one thing on the page that must never be stale.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(html))
}

// listJobs returns one document's print history, "how many times has this been printed, by whom".
func (h *Handler) listJobs(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	jobs, err := h.queue.ListForRef(r.Context(), q.Get("doc_type"), q.Get("ref_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

// writeErr maps the printing error taxonomy to HTTP.
func (h *Handler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var br httpx.BadRequestError
	switch {
	case errors.Is(err, ErrUnauthorized):
		httpx.Error(w, http.StatusUnauthorized, "unauthenticated")
	case errors.Is(err, ErrForbidden):
		httpx.Error(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not found")
	case errors.Is(err, ErrNotQueued):
		httpx.Error(w, http.StatusConflict, err.Error())
	case errors.As(err, &br):
		httpx.Error(w, http.StatusBadRequest, br.Msg)
	default:
		h.log.Error("printing: internal error", "method", r.Method, "path", r.URL.Path, "err", err)
		httpx.Error(w, http.StatusInternalServerError, "internal error")
	}
}
