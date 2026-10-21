package customer

import (
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

// Handler exposes the customer read surface the staff counter needs. Today that is the
// POS customer picker's search endpoint, staff-gated, like the till/pos counter routes.
// The portal BFF composes the Service in-process and does NOT go through this HTTP surface;
// these routes are the staff-facing (Bearer JWT) seam only.
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

// RegisterRoutes self-registers the staff-gated customer routes. requireStaff is the
// identity gate main.go injects (the same guard the pos/till handlers receive). writeGuard
// (manager) gates the M5 create/update-address write path; a nil writeGuard registers only
// the read route (write path stays unavailable rather than shipping unguarded).
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/customers/search", g.RequireStaff(h.staff(h.search)))
	// One account by id, credit limit and current balance included, behind the search's own grants.
	r.Handle("GET /api/v1/customers/{id}", g.RequireStaff(h.staff(h.get)))
	// DIB-482: reading whether a customer may be texted is a STAFF read, anyone who might send them
	// something needs to know the answer before they promise it.
	r.Handle("GET /api/v1/customers/{id}/sms-consent", g.RequireStaff(h.staff(h.smsConsent)))
	// M5 (A-5): create + update-billing-address, each validating the Canadian
	// address. The write leg is manager-gated (the pin's wiring passed
	// RequireManager as the variadic write guard; the carry names it).
	r.Handle("POST /api/v1/customers", g.RequireManager(h.staff(h.create)))
	r.Handle("PUT /api/v1/customers/{id}/billing-address", g.RequireManager(h.staff(h.updateBillingAddress)))
	// DIB-482: RECORDING consent sits behind the same manager guard as the other customer
	// writes. It is a legal record about a person, not a preference toggle, and the guard is the
	// cheapest available statement of that.
	r.Handle("PUT /api/v1/customers/{id}/sms-consent", g.RequireManager(h.staff(h.setSMSConsent)))
}

// get backs the one-account read. GET /api/v1/customers/{id}. An account the caller's branches do not
// reach is a 404, the same answer as an id nobody has (account_read.go).
func (h *Handler) get(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	out, err := h.svc.GetAccount(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.svc.AccountForWire(r.Context(), out))
}

// smsConsent backs the consent control's read. GET /api/v1/customers/{id}/sms-consent.
//
// A customer with no row answers 200 with consent:false, recorded:false, NOT 404. "Nobody has ever
// asked this customer" is an answer, and the empty-never-an-error rule applies to it exactly as it
// does to an empty list.
func (h *Handler) smsConsent(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	out, err := h.svc.GetSMSConsent(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// setSMSConsent records a consent decision. PUT /api/v1/customers/{id}/sms-consent
//
//	{"consent": true, "phone_e164": "+16135550101"}
//
// PUT rather than POST because the request states the DESIRED STATE of one fact and is idempotent:
// recording consent twice is the same consent, and an operator who double-clicks must not create a
// second contradictory record.
//
// The acting staff member is NOT in the payload and cannot be, it is read from the guard-stamped
// context in the service. A consent trail whose "who" the caller can type records nothing.
func (h *Handler) setSMSConsent(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var in struct {
		Consent   bool   `json:"consent"`
		PhoneE164 string `json:"phone_e164"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	out, err := h.svc.SetSMSConsent(r.Context(), r.PathValue("id"), in.Consent, in.PhoneE164)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// create handles POST /api/v1/customers, a staff/manager create with server-side Canadian
// postal/province validation on billing_address (M5). An invalid address is a 400.
func (h *Handler) create(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var in CreateInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	acct, err := h.svc.CreateCustomer(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, h.svc.AccountForWire(r.Context(), acct))
}

// updateBillingAddress handles PUT /api/v1/customers/{id}/billing-address, validates +
// normalizes the address (k8v5p7 → K8V 5P7; province ISO code) before persisting (M5).
func (h *Handler) updateBillingAddress(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var addr map[string]any
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&addr); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	acct, err := h.svc.UpdateBillingAddress(r.Context(), r.PathValue("id"), addr)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.svc.AccountForWire(r.Context(), acct))
}

// writeErr maps service errors: client-input (incl. postal/province validation) → 400;
// duplicate account_no → 409; not-found → 404; else a generic 500 with the real error logged.
func (h *Handler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var bad httpx.BadRequestError
	switch {
	case errors.As(err, &bad):
		httpx.Error(w, http.StatusBadRequest, bad.Error())
	case errors.Is(err, ErrDuplicateAccountNo):
		httpx.Error(w, http.StatusConflict, "account number already in use")
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not found")
	case errors.Is(err, ErrForbidden):
		httpx.Error(w, http.StatusForbidden, "out of scope")
	case errors.Is(err, ErrUnauthenticated):
		httpx.Error(w, http.StatusUnauthorized, "unauthenticated")
	case errors.Is(err, ErrConsentUnavailable):
		// 501, not 500. The deployment cannot record consent, that is a missing capability, and an
		// operator reading "internal error" would try again forever.
		httpx.Error(w, http.StatusNotImplemented, "sms consent is not available on this deployment")
	default:
		h.log.Error("customer: write failed", "method", r.Method, "path", r.URL.Path, "err", err)
		httpx.Error(w, http.StatusInternalServerError, "internal error")
	}
}

// search backs the POS customer picker. GET /api/v1/customers/search?q=<term>&limit=<n>.
// Returns a JSON array of the account facts the POS strip renders (id, name, account_no,
// customer_type, payment_terms_days, credit_limit_cents, open_balance_cents). The walk-in
// house account is excluded server-side.
func (h *Handler) search(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query().Get("q")
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}
	// U-park E4: the branch the caller is acting for. The POS picker has always sent branch_id; the
	// server now uses it, fenced against the caller's own grants (see search_scope.go).
	branchID := strings.TrimSpace(r.URL.Query().Get("branch_id"))
	out, err := h.svc.SearchAccounts(r.Context(), q, limit, branchID)
	if err != nil {
		var bad httpx.BadRequestError
		switch {
		case errors.Is(err, ErrForbidden):
			httpx.Error(w, http.StatusForbidden, err.Error())
		case errors.Is(err, ErrUnauthenticated):
			httpx.Error(w, http.StatusUnauthorized, err.Error())
		case errors.As(err, &bad):
			httpx.Error(w, http.StatusBadRequest, bad.Error())
		default:
			h.log.Error("customer: search failed", "err", err)
			httpx.Error(w, http.StatusInternalServerError, "internal error")
		}
		return
	}
	// An empty search returns [] not null; the figures are on the wire only under the dealer's policy.
	httpx.WriteJSON(w, http.StatusOK, h.svc.SearchForWire(r.Context(), out, branchID))
}
