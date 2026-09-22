package till

import (
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"log/slog"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

const maxBodyBytes = 1 << 20 // 1 MiB

// Handler exposes the drawer-lifecycle HTTP surface. The TakeTender/Allocate seams are NOT public HTTP
// , they are composed in-process by A-19. SCAFFOLD: handlers are thin pass-throughs to the service.
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

// RegisterRoutes self-registers the drawer routes. requireStaff gates open/tender/movement/count;
// complete is a RequireStaff route plus the in-service conditional manager gate. requireAdmin is threaded in
// by the W5 scaffold (Tier-1 GLOBAL) for the A-05 NSF/credit-hold admin routes that transfer onto this
// handler; it is unused until A-05 fills them.
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("POST /api/v1/till/sessions", g.RequireStaff(h.staff(h.openSession)))
	// DIB-488 rev 5: the POS terminal rediscovers its OPEN drawer on mount (reload/duplicate).
	// The literal path binds ahead of the {id} wildcard below by ServeMux precedence.
	r.Handle("GET /api/v1/till/sessions/open", g.RequireStaff(h.staff(h.openSessionLookup)))
	r.Handle("GET /api/v1/till/sessions/{id}", g.RequireStaff(h.staff(h.getSession)))
	r.Handle("POST /api/v1/till/sessions/{id}/movements", g.RequireStaff(h.staff(h.recordMovement)))
	r.Handle("POST /api/v1/till/sessions/{id}/count", g.RequireStaff(h.staff(h.countSession)))
	r.Handle("GET /api/v1/till/sessions/{id}/zreport", g.RequireStaff(h.staff(h.zreport)))
	r.Handle("POST /api/v1/till/sessions/{id}/complete", g.RequireStaff(h.staff(h.completeSession)))
	r.Handle("GET /api/v1/till/policy", g.RequireManager(h.staff(h.policy)))

	// - W5 A-05: NSF + credit holds + declined-attempt log -
	// Marking a bounce + editing money policy are manager rights; releasing a credit hold is Tier-1 only
	// (requireAdmin, threaded in by the W5 scaffold). The bounced/credit-hold reads expose customer
	// financial-distress PII, so they are manager-gated (matching the finstmt AR reads), not staff-wide.
	r.Handle("GET /api/v1/ar/bounced", g.RequireManager(h.staff(h.listBounced)))
	r.Handle("POST /api/v1/ar/payments/{id}/nsf", g.RequireManager(h.staff(h.markNSF)))
	r.Handle("GET /api/v1/ar/credit-holds", g.RequireManager(h.staff(h.listCreditHolds)))
	r.Handle("POST /api/v1/ar/credit-holds/{id}/release", g.RequireAdmin(h.staff(h.releaseHold)))
	r.Handle("GET /api/v1/ar/nsf-policy", g.RequireManager(h.staff(h.getNSFPolicy)))
	r.Handle("PUT /api/v1/ar/nsf-policy", g.RequireManager(h.staff(h.updateNSFPolicy)))
	r.Handle("POST /api/v1/ar/payment-attempts", g.RequireStaff(h.staff(h.recordAttempt)))
	h.registerHistoryRoutes(r, g) // Q-044 payment/invoice attribution reads (history.go)
}

// listBounced returns the bounced-payment queue; ?gateway_ref= is an exact-ref search.
func (h *Handler) listBounced(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	items, err := h.svc.ListBouncedPayments(r.Context(), r.URL.Query().Get("gateway_ref"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// markNSF records a bounced payment: reverse, reopen, fee, auto-hold, emit, all in one tx.
func (h *Handler) markNSF(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in NSFInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.PaymentID = r.PathValue("id")
	res, err := h.svc.MarkNSF(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// listCreditHolds returns credit holds; ?status=ACTIVE|RELEASED filters.
func (h *Handler) listCreditHolds(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	items, err := h.svc.ListCreditHolds(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// releaseHold is Tier-1 (requireAdmin) with a mandatory reason + audit.
func (h *Handler) releaseHold(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in ReleaseHoldInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.HoldID = r.PathValue("id")
	if err := h.svc.ReleaseHold(r.Context(), in); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": HoldReleased})
}

// getNSFPolicy returns the effective NSF policy for a branch (?branch_id= optional; "" = org default).
func (h *Handler) getNSFPolicy(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	p, err := h.svc.GetNSFPolicy(r.Context(), r.URL.Query().Get("branch_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

// updateNSFPolicy upserts the org-default NSF policy (manager-gated).
func (h *Handler) updateNSFPolicy(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in NSFPolicy
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := h.svc.UpdateNSFPolicy(r.Context(), in); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// recordAttempt persists one declined/errored gateway attempt (DX-14).
func (h *Handler) recordAttempt(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in PaymentAttempt
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := h.svc.RecordPaymentAttempt(r.Context(), in); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
}

func (h *Handler) openSession(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in OpenTillInput
	if !decodeJSON(w, r, &in) {
		return
	}
	id, err := h.svc.OpenTill(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]string{"id": id, "status": StatusOpen})
}

// openSessionLookup, GET /api/v1/till/sessions/open?branch_id=&station= (DIB-488 rev 5).
// {"open": false} when no drawer is open: an ordinary state answered 200, never a 404.
func (h *Handler) openSessionLookup(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	branchID := r.URL.Query().Get("branch_id")
	if branchID == "" {
		httpx.Error(w, http.StatusBadRequest, "branch_id is required")
		return
	}
	id, openedAt, found, err := h.svc.OpenSessionLookup(r.Context(), branchID, r.URL.Query().Get("station"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if !found {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"open": false})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"open": true, "id": id, "opened_at": openedAt})
}

func (h *Handler) getSession(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	sess, err := h.svc.GetSession(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, sess)
}

func (h *Handler) recordMovement(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in MovementInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.TillSessionID = r.PathValue("id")
	var err error
	switch in.MovementType {
	case MovementPaidIn:
		err = h.svc.PaidIn(r.Context(), in)
	case MovementPaidOut, MovementDrop:
		err = h.svc.PaidOut(r.Context(), in)
	case MovementLodgement:
		err = h.svc.Lodge(r.Context(), in)
	default:
		httpx.Error(w, http.StatusBadRequest, "unknown movement_type")
		return
	}
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
}

func (h *Handler) countSession(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in CountInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.TillSessionID = r.PathValue("id")
	if err := h.svc.CountTill(r.Context(), in); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": StatusCounted})
}

func (h *Handler) zreport(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	z, err := h.svc.ZReportFor(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, z)
}

func (h *Handler) completeSession(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in CompleteInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.TillSessionID = r.PathValue("id")
	if err := h.svc.CompleteTill(r.Context(), in); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": StatusClosed})
}

func (h *Handler) policy(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	p, err := h.svc.Policy(r.Context(), r.URL.Query().Get("branch_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(dst); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	return true
}

// writeErr maps the till error taxonomy to HTTP.
func (h *Handler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var ce clientError
	var br badRequestError
	switch {
	case errors.As(err, &ce):
		httpx.Error(w, ce.status, ce.msg)
	case errors.As(err, &br):
		httpx.Error(w, http.StatusBadRequest, br.msg)
	default:
		h.log.Error("till: internal error", "method", r.Method, "path", r.URL.Path, "err", err)
		httpx.Error(w, http.StatusInternalServerError, "internal error")
	}
}
