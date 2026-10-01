package payment

import (
	"context"
	"encoding/json"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"log/slog"
	"net/http"
)

// maxBodyBytes caps the config request body.
const maxBodyBytes = 1 << 20 // 1 MiB

// configStore is the write-only config seam the handler depends on. *Store satisfies it; tests use
// a DB-free fake so the RBAC + response-shape tests need no database.
type configStore interface {
	Status(ctx context.Context) (configured bool, merchantID, processor, currency string, err error)
	Configure(ctx context.Context, processor, merchantID, currency, baseURL string, mdrBps int64, surchargeEnabled bool, creds map[string]string, actorID string) error
}

// Handler exposes the processor-config surface, the ONLY HTTP RUN-PAY owns. There is no charge route;
// charging happens in-process via Service.Charge, called by A-03's till.
type Handler struct {
	userLink  identity.UserIDLinker
	store     configStore
	log       *slog.Logger
	simulated bool
}

// NewHandler wires the config store.
func NewHandler(store *Store, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{store: store, log: log}
}

// WithSimulated marks the card rail as TEST MODE (deterministic stub, no live gateway) on the status
// response, so the POS UI can badge the card tender honestly. Set from Service.Simulated() at the
// composition root, so the answer always describes the rail that was actually wired.
func (h *Handler) WithSimulated(sim bool) *Handler {
	h.simulated = sim
	return h
}

// RegisterRoutes self-registers the config routes. readGuard = RequireStaff, adminGuard = RequireAdmin.
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/payments/config", g.RequireStaff(h.staff(h.status)))
	r.Handle("PUT /api/v1/payments/config", g.RequireAdmin(h.staff(h.configure)))
}

// status → {configured, merchant_id, processor, currency}, NEVER creds.
func (h *Handler) status(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	configured, mid, proc, currency, err := h.store.Status(r.Context())
	if err != nil {
		h.log.Error("payment: status failed", "err", err)
		httpx.Error(w, http.StatusServiceUnavailable, "config lookup failed")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"configured":  configured,
		"merchant_id": mid,
		"processor":   proc,
		"currency":    currency,
		"simulated":   h.simulated,
	})
}

type configureRequest struct {
	Processor        string            `json:"processor"`
	MerchantID       string            `json:"merchant_id"`
	Currency         string            `json:"currency"`
	BaseURL          string            `json:"base_url"`
	MDRBps           int64             `json:"mdr_bps"`
	SurchargeEnabled bool              `json:"surcharge_enabled"`
	Credentials      map[string]string `json:"credentials"`
}

// configure stores write-only creds. NEVER echoes creds back.
func (h *Handler) configure(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var req configureRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if req.MerchantID == "" {
		httpx.Error(w, http.StatusBadRequest, "merchant_id is required")
		return
	}
	actorID := identity.StaffUserIDFromContext(r.Context())
	if err := h.store.Configure(r.Context(), req.Processor, req.MerchantID, req.Currency, req.BaseURL, req.MDRBps, req.SurchargeEnabled, req.Credentials, actorID); err != nil {
		h.log.Error("payment: configure failed", "err", err)
		httpx.Error(w, http.StatusServiceUnavailable, "configure failed")
		return
	}
	proc := req.Processor
	if proc == "" {
		proc = "run_payments"
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"configured":  true,
		"merchant_id": req.MerchantID,
		"processor":   proc,
	})
}
