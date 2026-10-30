package billing

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// CreditChangeHandler serves the manager-gated credit change request and approval routes.
type CreditChangeHandler struct {
	userLink identity.UserIDLinker
	svc      *CreditChangeService
	log      *slog.Logger
}

// NewCreditChangeHandler constructs the credit change HTTP handler.
func NewCreditChangeHandler(svc *CreditChangeService, log *slog.Logger) *CreditChangeHandler {
	if log == nil {
		log = slog.Default()
	}
	return &CreditChangeHandler{svc: svc, log: log}
}

// WithUserLinker binds the staff user-id linker for request stamping.
func (h *CreditChangeHandler) WithUserLinker(link identity.UserIDLinker) *CreditChangeHandler {
	h.userLink = link
	return h
}

func (h *CreditChangeHandler) staff(next guards.StaffHandler) guards.StaffHandler {
	return func(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
		next(w, identity.StampRequest(r, caller, h.userLink), caller)
	}
}

// RegisterRoutes registers the credit change request, read, list, approve, and reject routes.
func (h *CreditChangeHandler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("POST /api/v1/ar/credit-changes", g.RequireManager(h.staff(h.request)))
	r.Handle("GET /api/v1/ar/credit-changes", g.RequireManager(h.staff(h.list)))
	r.Handle("GET /api/v1/ar/credit-changes/{id}", g.RequireManager(h.staff(h.get)))
	r.Handle("POST /api/v1/ar/credit-changes/{id}/approve", g.RequireManager(h.staff(h.approve)))
	r.Handle("POST /api/v1/ar/credit-changes/{id}/reject", g.RequireManager(h.staff(h.reject)))
}

func (h *CreditChangeHandler) request(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var in CreateCreditChangeInput
	r.Body = http.MaxBytesReader(w, r.Body, maxRegisterBody)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	res, err := h.svc.Request(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *CreditChangeHandler) list(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	p, err := httpx.ParseCursorPage(r)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	q := r.URL.Query()
	items, err := h.svc.List(r.Context(), CreditChangeFilter{
		CustomerID: q.Get("customer_id"),
		BranchID:   q.Get("branch_id"),
		Status:     q.Get("status"),
		Limit:      p.Limit,
	})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteCursorPage(w, items, "", p.Limit)
}

func (h *CreditChangeHandler) get(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	res, err := h.svc.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *CreditChangeHandler) approve(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var in DecideCreditChangeInput
	if r.Body != nil && r.ContentLength != 0 {
		r.Body = http.MaxBytesReader(w, r.Body, maxRegisterBody)
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}
	res, err := h.svc.Approve(r.Context(), r.PathValue("id"), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *CreditChangeHandler) reject(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var in DecideCreditChangeInput
	r.Body = http.MaxBytesReader(w, r.Body, maxRegisterBody)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	res, err := h.svc.Reject(r.Context(), r.PathValue("id"), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *CreditChangeHandler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var bad httpx.BadRequestError
	switch {
	case errors.Is(err, ErrCreditChangeCustomerNotFound):
		httpx.Error(w, http.StatusNotFound, "customer not found")
	case errors.Is(err, ErrCreditChangeNotFound):
		httpx.Error(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrCreditChangeUnauthorized):
		httpx.Error(w, http.StatusForbidden, err.Error())
	case errors.Is(err, ErrCreditChangeForbidden),
		errors.Is(err, ErrCreditChangeRoleForbidden),
		errors.Is(err, ErrCreditChangeSelfApproval),
		errors.Is(err, ErrCreditChangeDuplicateApprover):
		httpx.Error(w, http.StatusForbidden, err.Error())
	case errors.Is(err, ErrCreditChangeNotPending):
		httpx.Error(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrCreditChangeReasonRequired),
		errors.Is(err, ErrCreditChangeInvalidTarget),
		errors.As(err, &bad):
		httpx.Error(w, http.StatusBadRequest, err.Error())
	default:
		h.log.Warn("credit change request failed", "path", r.URL.Path, "err", err)
		httpx.Error(w, http.StatusInternalServerError, "credit change service unavailable")
	}
}
