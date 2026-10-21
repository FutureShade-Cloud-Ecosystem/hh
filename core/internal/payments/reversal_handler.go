package payments

// reversal_handler.go, the DIB-170 undo route.
//
// It is its own tiny handler rather than a method on the portal's payment surface, because a
// reversal is a STAFF action against a customer's payment and the portal surface is authenticated as
// the customer. Putting an undo on a contractor-authenticated route would be a way for a payer to
// reverse their own payment, which is not a feature.

import (
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"log/slog"
	"net/http"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// ReversalHandler serves the staff-side payment undo.
type ReversalHandler struct {
	userLink identity.UserIDLinker
	svc      *ReverseService
	log      *slog.Logger
}

// NewReversalHandler wires the service.
func NewReversalHandler(svc *ReverseService, log *slog.Logger) *ReversalHandler {
	if log == nil {
		log = slog.Default()
	}
	return &ReversalHandler{svc: svc, log: log}
}

// RegisterRoutes registers the undo route behind the manager gate. The gate is applied HERE as well
// as inside the service (ReverseInput.IsManager): the route gate stops the request, the service check
// stops an in-process caller that bypassed HTTP, and neither is a substitute for the other.
func (h *ReversalHandler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("POST /api/v1/ar/payments/{id}/reverse", g.RequireManager(h.staff(h.reverse)))
	// SF-2l: the money a reversal holds in 2310, applied to invoices of the same customer.
	r.Handle("POST /api/v1/ar/payments/{id}/reapply", g.RequireManager(h.staff(h.reapply)))
}

func (h *ReversalHandler) reapply(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var req struct {
		Reason         string `json:"reason"`
		IdempotencyKey string `json:"idempotency_key"`
		Allocations    []struct {
			InvoiceID   string `json:"invoice_id"`
			AmountCents int64  `json:"amount_cents"`
		} `json:"allocations"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	allocs := make([]Allocation, 0, len(req.Allocations))
	for _, a := range req.Allocations {
		allocs = append(allocs, Allocation{InvoiceID: a.InvoiceID, AmountCents: a.AmountCents})
	}
	sc, _ := identity.StaffContextFromContext(r.Context())
	res, err := h.svc.Reapply(r.Context(), ReapplyInput{
		PaymentID:       r.PathValue("id"),
		Reason:          req.Reason,
		ActorID:         sc.UserID,
		IdempotencyKey:  req.IdempotencyKey,
		Allocations:     allocs,
		IsManager:       sc.IsManager(),
		CanAccessBranch: sc.CanAccessBranch,
	})
	if err != nil {
		var noInvoice NoSuchInvoiceError
		var replay ReplayConflictError
		var bad httpx.BadRequestError
		switch {
		case errors.Is(err, ErrNotFound):
			httpx.Error(w, http.StatusNotFound, "payment not found")
		case errors.As(err, &noInvoice):
			httpx.Error(w, http.StatusNotFound, "invoice not found: "+noInvoice.InvoiceID)
		case errors.As(err, &replay):
			httpx.Error(w, http.StatusConflict, err.Error())
		case errors.Is(err, ErrNotReapplicable), errors.Is(err, ErrReapplyOverHeld):
			// 409, not 400: the request was well formed and the state of the held money says no.
			httpx.Error(w, http.StatusConflict, err.Error())
		case errors.Is(err, ErrReapplyReasonRequired), errors.As(err, &bad):
			httpx.Error(w, http.StatusBadRequest, err.Error())
		default:
			h.log.Warn("payment re-application failed", "payment_id", r.PathValue("id"), "err", err)
			httpx.Error(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	status := http.StatusCreated
	if res.Replayed {
		status = http.StatusOK
	}
	httpx.WriteJSON(w, status, res)
}

func (h *ReversalHandler) reverse(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var req struct {
		Reason string `json:"reason"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// CARRY NOTE (guards swap, HH-3.2 B5): the pin handler re-read the
	// caller from the session middleware's context; under the carry the
	// h.staff adapter has already stamped the bridged StaffContext from
	// the guard-resolved caller (the same name the body reads at the
	// pin), so the local keeps the carried read and the guards.StaffCaller
	// parameter carries the authority half.
	sc, _ := identity.StaffContextFromContext(r.Context())
	rev, err := h.svc.Reverse(r.Context(), ReverseInput{
		PaymentID: r.PathValue("id"),
		Reason:    req.Reason,
		ActorID:   sc.UserID,
		IsManager: sc.IsManager(),
		// The caller's branch reach (a branch manager reverses its own branch's payments only).
		CanAccessBranch: sc.CanAccessBranch,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			httpx.Error(w, http.StatusNotFound, "payment not found")
		case errors.Is(err, ErrAlreadyReversed):
			// 409, not 400: the request was well formed and the state says no.
			httpx.Error(w, http.StatusConflict, err.Error())
		case errors.Is(err, ErrNotReversible), errors.Is(err, ErrReversalReasonRequired):
			httpx.Error(w, http.StatusBadRequest, err.Error())
		default:
			h.log.Warn("payment reversal failed", "payment_id", r.PathValue("id"), "err", err)
			httpx.Error(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	// label is what the operator sees, VOID for a same-day undo, REVERSAL otherwise. The rows are
	// identical either way; only the word changes.
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"label":               rev.Label(),
		"original_payment_id": rev.OriginalPaymentID,
		"reversal_payment_id": rev.ReversalPaymentID,
		"reversal_no":         rev.ReversalNo,
		"amount_cents":        rev.AmountCents,
		"reason":              rev.Reason,
		"same_day":            rev.SameDay,
		"reopened_invoices":   rev.ReopenedInvoices,
	})
}
