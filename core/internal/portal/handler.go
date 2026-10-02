package portal

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/procurement"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
)

type ctxKey int

const callerKey ctxKey = iota

// maxBodyBytes caps request bodies so an unauthenticated caller can't stream an
// unbounded payload into the JSON decoder (M-4).
const maxBodyBytes = 1 << 20 // 1 MiB

// Handler exposes the contractor portal surface at /api/v1/tenant/* (the
// document's spellings, ADR 0021: the pin's /api/portal/v1/* prefixes
// re-homed onto the contract paths), auth/team plus the BFF feature handlers
// (dashboard, invoices, orders, quotes, payments, settings).
// As a BFF it composes other domain services in-process; every feature read is
// tenant-scoped by the customer_id on the verified JWT, and every route is gated on a
// capability, never a raw role.
type Handler struct {
	svc *Service
	log *slog.Logger

	// Domain seams the BFF composes in-process (P2/P3/P4). Injected via the With*
	// builders so the auth-only constructor stays test-stable. A nil seam means that
	// feature's handlers return 501 until the swarm wires + implements the service.
	customerSvc    *customer.Service
	billingSvc     *billing.Service
	orderSvc       *order.Service
	quoteSvc       *quote.Service
	paymentSvc     *payments.Service
	procurementSvc *procurement.Service
	productSvc     *product.Service
	returnsSvc     ReturnsService   // A-06 portal returns (pos-backed; builder in returns.go)
	projectsSvc    *ProjectsService // CP-03 jobs/spend rollup (A-3; seam in projects.go)
}

func NewHandler(svc *Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, log: log}
}

// WithCustomers injects the customers/contacts read seam (account resolver). Returns the
// Handler for chaining (mirrors the pricing module's builder convention).
func (h *Handler) WithCustomers(s *customer.Service) *Handler { h.customerSvc = s; return h }

// WithBilling injects the invoice/AR read seam.
func (h *Handler) WithBilling(s *billing.Service) *Handler { h.billingSvc = s; return h }

// WithOrders injects the orders read + delivery-instructions seam.
func (h *Handler) WithOrders(s *order.Service) *Handler { h.orderSvc = s; return h }

// WithQuotes injects the quotes read + state-machine seam.
func (h *Handler) WithQuotes(s *quote.Service) *Handler { h.quoteSvc = s; return h }

// WithPayments injects the payment-methods + multi-invoice-pay seam.
func (h *Handler) WithPayments(s *payments.Service) *Handler { h.paymentSvc = s; return h }

// WithProcurement injects the Planning-board seam (projects/lines + RFQ pricing).
func (h *Handler) WithProcurement(s *procurement.Service) *Handler { h.procurementSvc = s; return h }

// WithProducts injects the catalog seam (the line-add fuzzy/by-SKU search picker).
func (h *Handler) WithProducts(s *product.Service) *Handler { h.productSvc = s; return h }

// WithProjects injects the CP-03 jobs/spend rollup seam (A-3). Nil → the rollup routes
// answer 501 until wired.
func (h *Handler) WithProjects(s *ProjectsService) *Handler { h.projectsSvc = s; return h }

// RegisterRoutes registers every route through an explicit guard wrapper, so a
// route is protected by construction, a new route can't silently ship public.
//
// CARRY NOTE (the bearer split, HH-3.2 fold, R6): the ERP portal
// authenticated its own HS256 contractor tokens per route group (the pin's
// no-guard registrar); under the target the bearer legs re-home onto the
// Guards set, RequireTenant for the plain legs and RequireCapability for
// each capability wrapper, with h.tenant resolving the module's own
// PortalContext from the guard-resolved tenant scope. The session-MINTING
// legs (login, accept-invite, the password-reset pair and the sliding
// refresh) are NOT registered: they are the portal's own issuer world,
// superseded by the platform bearer the same way the staff HS256 issuer
// was (the Google-SSO-precedent leave-behind, folded into the D38
// sign-in-methods item traveling to HH-4.2). Their handlers and register
// helpers carry; no served mux registers them.
//
// RE-HOMING NOTE (the Phase C integration): every registration below uses
// the document's /api/v1/tenant/* spelling, method and path parameters
// exactly as the generated census carries them
// (core/internal/platform/apigen/routes.gen.go); the composition point's
// parity test refuses any other spelling. The pin's /api/portal/v1/config
// registration UNREGISTERED with the re-homing: the document's
// tenantGetConfig operation is served by the identity composition point
// (the narrowed TenantPublicConfig leg in core/cmd/core/main.go), and a
// second registration of one documented operation is exactly what the
// ServeMux duplicate panic exists to refuse.
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	if g == nil {
		panic("portal.RegisterRoutes: guards are required (the bearer legs are tenant-gated)")
	}
	// Account-admin only (ManageUsers capability)
	r.Handle("GET /api/v1/tenant/users", g.RequireCapability(identity.CapManageUsers, h.tenant(h.listUsers)))
	r.Handle("GET /api/v1/tenant/invites", g.RequireCapability(identity.CapManageUsers, h.tenant(h.listInvites)))
	r.Handle("POST /api/v1/tenant/invites", g.RequireCapability(identity.CapManageUsers, h.tenant(h.createInvite)))
	r.Handle("PUT /api/v1/tenant/users/{id}/role", g.RequireCapability(identity.CapManageUsers, h.tenant(h.updateRole)))
	r.Handle("PUT /api/v1/tenant/users/{id}/status", g.RequireCapability(identity.CapManageUsers, h.tenant(h.updateStatus)))

	// BFF feature surfaces, each self-registers from its own file so swarm edits stay
	// disjoint. The billing/summary path is now served by the dashboard handler.
	h.registerDashboardRoutes(r, g)
	h.registerInvoiceRoutes(r, g)
	h.registerOrderRoutes(r, g)
	h.registerReorderRoutes(r, g)
	h.registerQuoteRoutes(r, g)
	h.registerPaymentMethodRoutes(r, g)
	h.registerPaymentRoutes(r, g)
	h.registerSettingsRoutes(r, g)
	h.registerProcurementRoutes(r, g)
	h.registerCatalogRoutes(r, g)
	h.registerReturnRoutes(r, g)
	h.registerProjectRoutes(r, g)
	// DIB-479 (HH Pro connection, Stage 1): the account's delivery sites
	// (the sliding refresh itself is an unregistered mint surface, above).
	h.registerAddressRoutes(r, g)
}

// -- guards (middleware) ------------------------------------------------------

// require enforces a valid token and (optionally) a capability, then injects the
// caller into the request context for the wrapped handler.
func (h *Handler) require(check func(Capabilities) bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, ok := h.caller(r)
		if !ok {
			httpx.Error(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if check != nil && !check(c.Caps()) {
			httpx.Error(w, http.StatusForbidden, "forbidden")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), callerKey, c)))
	}
}

// authn requires only a valid token (no capability), for self-service /me routes any
// authenticated user may reach.
func (h *Handler) authn(next http.HandlerFunc) http.HandlerFunc { return h.require(nil, next) }

func (h *Handler) admin(next http.HandlerFunc) http.HandlerFunc {
	return h.require(func(c Capabilities) bool { return c.ManageUsers }, next)
}

func (h *Handler) billing(next http.HandlerFunc) http.HandlerFunc {
	return h.require(func(c Capabilities) bool { return c.ViewBilling }, next)
}

func (h *Handler) payInvoices(next http.HandlerFunc) http.HandlerFunc {
	return h.require(func(c Capabilities) bool { return c.PayInvoices }, next)
}

func (h *Handler) managePaymentMethods(next http.HandlerFunc) http.HandlerFunc {
	return h.require(func(c Capabilities) bool { return c.ManagePaymentMethods }, next)
}

func (h *Handler) submitRFQ(next http.HandlerFunc) http.HandlerFunc {
	return h.require(func(c Capabilities) bool { return c.SubmitRFQ }, next)
}

func (h *Handler) createOrders(next http.HandlerFunc) http.HandlerFunc {
	return h.require(func(c Capabilities) bool { return c.CreateOrders }, next)
}

func (h *Handler) viewOrdersDeliveries(next http.HandlerFunc) http.HandlerFunc {
	return h.require(func(c Capabilities) bool { return c.ViewOrdersDeliveries }, next)
}

func (h *Handler) editDeliveryInstructions(next http.HandlerFunc) http.HandlerFunc {
	return h.require(func(c Capabilities) bool { return c.EditDeliveryInstructions }, next)
}

func callerFrom(ctx context.Context) PortalContext {
	c, _ := ctx.Value(callerKey).(PortalContext)
	return c
}

// -- public -------------------------------------------------------------------

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var req struct{ Email, Password string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	resp, err := h.svc.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) acceptInvite(w http.ResponseWriter, r *http.Request) {
	var req struct{ Token, Name, Password string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	u, err := h.svc.AcceptInvite(r.Context(), req.Token, req.Name, req.Password)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, u)
}

func (h *Handler) config(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, defaultConfig())
}

// -- admin-gated --------------------------------------------------------------

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.svc.ListUsers(r.Context(), callerFrom(r.Context()))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if users == nil {
		users = []User{}
	}
	httpx.WriteJSON(w, http.StatusOK, users)
}

func (h *Handler) listInvites(w http.ResponseWriter, r *http.Request) {
	invites, err := h.svc.ListInvites(r.Context(), callerFrom(r.Context()))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if invites == nil {
		invites = []Invite{}
	}
	httpx.WriteJSON(w, http.StatusOK, invites)
}

// createInvite answers 501 until HH-4.2 lands (HH-196). An invite created here can
// never be delivered (no live notifier is wired) or accepted (the accept route is
// unregistered, and AcceptInvite does not write the platform link the resolvers key
// on), so creating the row would only leave a pending invite nobody can redeem. It
// creates no row, emits no outbox event and mints no token. Listing stays served.
func (h *Handler) createInvite(w http.ResponseWriter, _ *http.Request) {
	httpx.Error(w, http.StatusNotImplemented, "inviting crew members is not available yet")
}

func (h *Handler) updateRole(w http.ResponseWriter, r *http.Request) {
	var req struct{ Role string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if err := h.svc.UpdateRole(r.Context(), callerFrom(r.Context()), r.PathValue("id"), req.Role); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) updateStatus(w http.ResponseWriter, r *http.Request) {
	var req struct{ Status string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if err := h.svc.UpdateStatus(r.Context(), callerFrom(r.Context()), r.PathValue("id"), req.Status); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// -- helpers ------------------------------------------------------------------

// bearerToken pulls the raw credential out of the Authorization header. One place parses that
// header, so "Bearer " handling cannot drift between the guard and the refresh route.
func bearerToken(r *http.Request) (string, bool) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	return token, token != ""
}

// caller verifies the Bearer token into a PortalContext.
func (h *Handler) caller(r *http.Request) (PortalContext, bool) {
	token, ok := bearerToken(r)
	if !ok {
		return PortalContext{}, false
	}
	c, err := h.svc.Authenticate(r.Context(), token)
	if err != nil {
		return PortalContext{}, false
	}
	return c, true
}

// writeErr maps service errors to HTTP without ever leaking internal detail:
// known sentinels → their status; client-input errors → 400; everything else →
// a generic 500 with the real error logged server-side.
func (h *Handler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var bre httpx.BadRequestError
	var creditBlocked quote.ErrCreditBlocked
	switch {
	case errors.Is(err, errInvalidCredentials):
		httpx.Error(w, http.StatusUnauthorized, "invalid credentials")
	case errors.Is(err, errInvalidToken):
		httpx.Error(w, http.StatusBadRequest, "invalid or expired invite")
	case errors.Is(err, errLastAdmin):
		httpx.Error(w, http.StatusConflict, errLastAdmin.Error())
	case errors.Is(err, errEmailTaken):
		httpx.Error(w, http.StatusConflict, errEmailTaken.Error())
	case errors.Is(err, errNotFound):
		httpx.Error(w, http.StatusNotFound, "not found")
	// -- domain (BFF-composed) sentinels -------------------------------------
	// A tenant-scoped miss in any composed module is a 404 (never 403): the row
	// either does not exist or belongs to another account, and we must not leak
	// which (IDOR-safe). Each module exports its own ErrNotFound value.
	case errors.Is(err, customer.ErrNotFound),
		errors.Is(err, billing.ErrNotFound),
		errors.Is(err, order.ErrNotFound),
		errors.Is(err, quote.ErrNotFound),
		errors.Is(err, payments.ErrNotFound),
		errors.Is(err, procurement.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not found")
	// -- DIB-483 / M3-G7B: the credit refusal keeps its REASON ------------------------------------
	// THIS CASE MUST STAY ABOVE THE GENERIC ErrInvalidTransition CASE BELOW. quote.ErrCreditBlocked
	// deliberately Unwraps to quote.ErrInvalidTransition (that is how a blocked conversion earns a 409
	// rather than a 500, see quote/credit.go). The consequence is that the generic errors.Is case
	// MATCHES a credit block, and if it is reached first the answer is the literal string "invalid
	// state transition", the policy and the detail the error type is carrying are dropped on the
	// floor. A contractor over their limit by $62,200 was being told their quote was in the wrong
	// state. Same 409, same wire shape, real words.
	case errors.As(err, &creditBlocked):
		httpx.Error(w, http.StatusConflict, quote.CreditRefusalMessage(creditBlocked))
	case errors.Is(err, quote.ErrInvalidTransition),
		errors.Is(err, procurement.ErrInvalidTransition):
		httpx.Error(w, http.StatusConflict, "invalid state transition")
	case errors.Is(err, procurement.ErrNotImplemented):
		// A scaffolded Planning-board seam with no behavior yet answers honestly (501).
		httpx.Error(w, http.StatusNotImplemented, "not implemented")
	case errors.Is(err, payments.ErrDeclined):
		httpx.Error(w, http.StatusPaymentRequired, "payment declined")
	case errors.Is(err, payments.ErrChargePending):
		// A transient gateway RETRY verdict: nothing was persisted and the caller may safely
		// retry the same request. 409 (distinct from the 402 hard decline above).
		httpx.Error(w, http.StatusConflict, "payment is pending, please retry")
	case errors.As(err, &bre):
		httpx.Error(w, http.StatusBadRequest, bre.Error())
	default:
		h.log.Error("portal: internal error", "method", r.Method, "path", r.URL.Path, "err", err)
		httpx.Error(w, http.StatusInternalServerError, "internal error")
	}
}
