package billing

// write_off_handler.go, W3 item 0.10: the HTTP surface for writing off an invoice's remaining
// balance and reversing a write off. The gate is the credit application doors' own: the route
// carries RequireManager AND the service re-checks IsManager, and the controller's answer (who may
// pass the branch's limit) is read off the caller's staff context role code, never out of the
// request body.

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// RoleCodeFinancialController is the role whose desk passes a branch's write off limit (W3 item
// 0.10). The staff tables are the role's one speller; this constant only names the answer the
// write off door needs from them.
const RoleCodeFinancialController = "FINANCIAL_CONTROLLER"

// WriteOffHandler serves the manager gated write off and reversal routes.
type WriteOffHandler struct {
	userLink identity.UserIDLinker
	svc      *WriteOffService
	log      *slog.Logger
}

// NewWriteOffHandler wires the write off handler.
func NewWriteOffHandler(svc *WriteOffService, log *slog.Logger) *WriteOffHandler {
	if log == nil {
		log = slog.Default()
	}
	return &WriteOffHandler{svc: svc, log: log}
}

// WithUserLinker binds the staff user id linker for request stamping.
func (h *WriteOffHandler) WithUserLinker(link identity.UserIDLinker) *WriteOffHandler {
	h.userLink = link
	return h
}

func (h *WriteOffHandler) staff(next guards.StaffHandler) guards.StaffHandler {
	return func(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
		next(w, identity.StampRequest(r, caller, h.userLink), caller)
	}
}

// RegisterRoutes registers the write off routes behind the manager gate.
func (h *WriteOffHandler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("POST /api/v1/ar/write-offs", g.RequireManager(h.staff(h.writeOff)))
	r.Handle("POST /api/v1/ar/write-offs/reverse", g.RequireManager(h.staff(h.reverse)))
}

func (h *WriteOffHandler) writeOff(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var req struct {
		InvoiceID      string `json:"invoice_id"`
		Reason         string `json:"reason"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRegisterBody)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	sc, _ := identity.StaffContextFromContext(r.Context())
	res, err := h.svc.WriteOff(r.Context(), WriteOffInput{
		InvoiceID:       req.InvoiceID,
		Reason:          req.Reason,
		ActorID:         sc.UserID,
		IdempotencyKey:  req.IdempotencyKey,
		OrgID:           caller.OrgID,
		CanAccessBranch: sc.CanAccessBranch,
		// The controller answer is the caller's own role, resolved by the staff tables: a request
		// body naming itself controller is not an authority.
		IsController: sc.RoleCode == RoleCodeFinancialController,
	}, sc.IsManager())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	status := http.StatusCreated
	if res.Replayed {
		status = http.StatusOK
	}
	httpx.WriteJSON(w, status, res)
}

func (h *WriteOffHandler) reverse(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var req struct {
		InvoiceID  string `json:"invoice_id"`
		WriteOffID string `json:"write_off_id"`
		Reason     string `json:"reason"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRegisterBody)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	sc, _ := identity.StaffContextFromContext(r.Context())
	res, err := h.svc.Reverse(r.Context(), ReverseWriteOffInput{
		InvoiceID:       req.InvoiceID,
		WriteOffID:      req.WriteOffID,
		Reason:          req.Reason,
		ActorID:         sc.UserID,
		OrgID:           caller.OrgID,
		CanAccessBranch: sc.CanAccessBranch,
	}, sc.IsManager())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	status := http.StatusCreated
	if res.Replayed {
		status = http.StatusOK
	}
	httpx.WriteJSON(w, status, res)
}

func (h *WriteOffHandler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var noInv NoSuchInvoiceError
	var over *WriteOffOverLimitError
	var conflict WriteOffConflictError
	var bad httpx.BadRequestError
	switch {
	case errors.As(err, &noInv):
		httpx.Error(w, http.StatusNotFound, "invoice not found: "+noInv.InvoiceID)
	case errors.As(err, &over):
		// 403, not 400: the balance is fine, the caller's desk is what cannot pass it. The words
		// name the branch's limit; the controller's desk reads what it may do about it.
		httpx.Error(w, http.StatusForbidden, err.Error())
	case errors.As(err, &conflict), errors.Is(err, ErrWriteOffNotReversible):
		httpx.Error(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrWriteOffReasonRequired), errors.As(err, &bad):
		httpx.Error(w, http.StatusBadRequest, err.Error())
	default:
		h.log.Warn("ar write off failed", "path", r.URL.Path, "err", err)
		httpx.Error(w, http.StatusBadRequest, err.Error())
	}
}
