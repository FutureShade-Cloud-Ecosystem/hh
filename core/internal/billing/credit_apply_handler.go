package billing

// credit_apply_handler.go, FW1-12b: the HTTP surface for applying an open credit memo or an
// unapplied account credit to a later invoice of the same customer, and reversing an application.

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// CreditApplyHandler serves the manager-gated credit application and reversal routes.
type CreditApplyHandler struct {
	userLink identity.UserIDLinker
	svc      *CreditApplyService
	log      *slog.Logger
}

// NewCreditApplyHandler wires the credit application handler.
func NewCreditApplyHandler(svc *CreditApplyService, log *slog.Logger) *CreditApplyHandler {
	if log == nil {
		log = slog.Default()
	}
	return &CreditApplyHandler{svc: svc, log: log}
}

// WithUserLinker binds the staff user-id linker for request stamping.
func (h *CreditApplyHandler) WithUserLinker(link identity.UserIDLinker) *CreditApplyHandler {
	h.userLink = link
	return h
}

func (h *CreditApplyHandler) staff(next guards.StaffHandler) guards.StaffHandler {
	return func(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
		next(w, identity.StampRequest(r, caller, h.userLink), caller)
	}
}

// RegisterRoutes registers the credit apply and reversal routes behind the manager gate.
func (h *CreditApplyHandler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("POST /api/v1/ar/credits/apply", g.RequireManager(h.staff(h.apply)))
	r.Handle("POST /api/v1/ar/credits/applications/reverse", g.RequireManager(h.staff(h.reverse)))
}

func (h *CreditApplyHandler) apply(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var req struct {
		CustomerID     string `json:"customer_id"`
		CreditKind     string `json:"credit_kind"`
		CreditID       string `json:"credit_id"`
		InvoiceID      string `json:"invoice_id"`
		AmountCents    int64  `json:"amount_cents"`
		Reason         string `json:"reason"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRegisterBody)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	sc, _ := identity.StaffContextFromContext(r.Context())
	res, err := h.svc.Apply(r.Context(), ApplyCreditInput{
		CustomerID:      req.CustomerID,
		CreditKind:      req.CreditKind,
		CreditID:        req.CreditID,
		InvoiceID:       req.InvoiceID,
		AmountCents:     req.AmountCents,
		Reason:          req.Reason,
		ActorID:         sc.UserID,
		IdempotencyKey:  req.IdempotencyKey,
		CanAccessBranch: sc.CanAccessBranch,
		OrgID:           caller.OrgID,
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

func (h *CreditApplyHandler) reverse(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var req struct {
		InvoiceID     string `json:"invoice_id"`
		ApplicationID string `json:"application_id"`
		Reason        string `json:"reason"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRegisterBody)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	sc, _ := identity.StaffContextFromContext(r.Context())
	res, err := h.svc.Reverse(r.Context(), ReverseCreditApplicationInput{
		InvoiceID:       req.InvoiceID,
		ApplicationID:   req.ApplicationID,
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

func (h *CreditApplyHandler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var noInv NoSuchInvoiceError
	var noCred NoSuchCreditError
	var conflict CreditApplicationConflictError
	var bad httpx.BadRequestError
	switch {
	case errors.As(err, &noInv):
		httpx.Error(w, http.StatusNotFound, "invoice not found: "+noInv.InvoiceID)
	case errors.As(err, &noCred):
		httpx.Error(w, http.StatusNotFound, "credit not found: "+noCred.CreditID)
	case errors.As(err, &conflict), errors.Is(err, CreditApplicationNotOpen), errors.Is(err, ApplicationNotReversible):
		httpx.Error(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrCreditApplicationReasonRequired), errors.As(err, &bad):
		httpx.Error(w, http.StatusBadRequest, err.Error())
	default:
		h.log.Warn("ar credit apply failed", "path", r.URL.Path, "err", err)
		httpx.Error(w, http.StatusBadRequest, err.Error())
	}
}
