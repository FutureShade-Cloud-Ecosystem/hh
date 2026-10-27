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

// ReversalHandler serves the staff-side payment undo: the reversal's held money and, since
// FW1-06b, the true void of a mis-keyed desk receipt. The two acts stay apart in their services
// and their journals (void.go states the rules); they share this one mounting because they are
// the two undos one payment row can carry.
type ReversalHandler struct {
	userLink identity.UserIDLinker
	svc      *ReverseService
	void     *VoidService
	log      *slog.Logger
}

// NewReversalHandler wires the service.
func NewReversalHandler(svc *ReverseService, log *slog.Logger) *ReversalHandler {
	if log == nil {
		log = slog.Default()
	}
	return &ReversalHandler{svc: svc, log: log}
}

// WithVoidService attaches the true void's service. Without it the void route answers 503 and
// names the fault: an unwired seam is a deploy fault, not a policy one.
func (h *ReversalHandler) WithVoidService(v *VoidService) *ReversalHandler { h.void = v; return h }

// RegisterRoutes registers the undo routes behind the manager gate. The gate is applied HERE as
// well as inside the services (ReverseInput.IsManager / VoidInput.IsManager): the route gate stops
// the request, the service check stops an in-process caller that bypassed HTTP, and neither is a
// substitute for the other.
func (h *ReversalHandler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("POST /api/v1/ar/payments/{id}/reverse", g.RequireManager(h.staff(h.reverse)))
	// FW1-06b: a mis-keyed desk receipt, undone as never taken on the dealer day it was taken.
	// No 2310 hold: the void's journal is the receipt's own legs mirrored.
	r.Handle("POST /api/v1/ar/payments/{id}/void", g.RequireManager(h.staff(h.voidPayment)))
	// SF-2l: the money a reversal holds in 2310, applied to invoices of the same customer.
	r.Handle("POST /api/v1/ar/payments/{id}/reapply", g.RequireManager(h.staff(h.reapply)))
	r.Handle("GET /api/v1/ar/payments/{id}/held", g.RequireManager(h.staff(h.held)))
}

func (h *ReversalHandler) voidPayment(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if h.void == nil {
		httpx.Error(w, http.StatusServiceUnavailable, ErrVoidNotWired.Error())
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	sc, _ := identity.StaffContextFromContext(r.Context())
	v, err := h.void.Void(r.Context(), VoidInput{
		PaymentID: r.PathValue("id"),
		Reason:    req.Reason,
		ActorID:   sc.UserID,
		IsManager: sc.IsManager(),
		// The caller's branch reach (a branch manager voids its own branch's receipts only).
		CanAccessBranch: sc.CanAccessBranch,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			httpx.Error(w, http.StatusNotFound, "payment not found")
		case errors.Is(err, ErrVoidNotWired):
			httpx.Error(w, http.StatusServiceUnavailable, err.Error())
		case errors.Is(err, ErrNotVoidable), errors.Is(err, ErrVoidReasonRequired):
			httpx.Error(w, http.StatusBadRequest, err.Error())
		default:
			h.log.Warn("payment void failed", "payment_id", r.PathValue("id"), "err", err)
			httpx.Error(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"label":             "VOID",
		"payment_id":        v.PaymentID,
		"payment_no":        v.PaymentNo,
		"amount_cents":      v.AmountCents,
		"reason":            v.Reason,
		"voided_at":         v.VoidedAt,
		"reopened_invoices": v.ReopenedInvoices,
		"held_cents":        0,
	})
}

func (h *ReversalHandler) held(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	sc, _ := identity.StaffContextFromContext(r.Context())
	got, err := h.svc.Held(r.Context(), r.PathValue("id"), sc.IsManager(), sc.CanAccessBranch)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			httpx.Error(w, http.StatusNotFound, "payment not found")
			return
		}
		h.log.Warn("payment held read failed", "payment_id", r.PathValue("id"), "err", err)
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, got)
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
