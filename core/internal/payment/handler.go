package payment

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// maxBodyBytes caps the config request body.
const maxBodyBytes = 1 << 20 // 1 MiB

// configStore is the write-only config seam the handler depends on, extended
// with the attempt reads and moves the two manager routes serve (the design's
// section 4): the register's ListOpen, the close's guarded Resolve, and the
// by-id read the route uses to split an unknown id from a status the close
// cannot move. *Store satisfies it; tests use a DB-free fake so the RBAC +
// response-shape tests need no database.
type configStore interface {
	Status(ctx context.Context) (configured bool, merchantID, processor, currency string, err error)
	Configure(ctx context.Context, processor, merchantID, currency, baseURL string, mdrBps int64, surchargeEnabled bool, creds map[string]string, actorID string) error
	ListOpen(ctx context.Context, q ListQuery) ([]Attempt, error)
	Resolve(ctx context.Context, id, resolution, note, resolvedBy string) (Attempt, bool, error)
	getByID(ctx context.Context, id string) (Attempt, bool, error)
}

// Handler exposes the processor-config surface and the two manager routes
// over the card charge attempts (the design's section 4: the list a person
// reconciling a Run settlement report reads, and the audited close). There is
// no charge route; charging happens in-process via Service.Capture, called by
// A-03's till.
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

// RegisterRoutes self-registers the config routes and the two manager routes
// over the card charge attempts. readGuard = RequireStaff, adminGuard =
// RequireAdmin, the attempts routes are RequireManager (a tier 1 or tier 2
// role: a person who may close a held-charge item).
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/payments/config", g.RequireStaff(h.staff(h.status)))
	r.Handle("PUT /api/v1/payments/config", g.RequireAdmin(h.staff(h.configure)))
	r.Handle("GET /api/v1/payments/card-attempts", g.RequireManager(h.staff(h.listAttempts)))
	r.Handle("POST /api/v1/payments/card-attempts/{id}/resolve", g.RequireManager(h.staff(h.resolveAttempt)))
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
	// HH-186: refuse the enable. No tender path on this server computes a surcharge, and D18
	// requires a recorded jurisdiction and cap before enabling (none is recorded), so accepting
	// the flag would store a setting nothing enforces. Turning it off stays accepted.
	if req.SurchargeEnabled {
		httpx.Error(w, http.StatusUnprocessableEntity, ErrSurchargeNotEnforced.Error())
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

// CardAttemptSummary is one manager-readable attempt row: the reconciliation
// facts (amount, branch, till session, time, sale key) beside the charge's
// own answers (status, the charge and reversal refs, the bounded last error).
// ref is the id's short form, the name the 409 answers carry; id is the whole
// value, the resolve route's path parameter. The row never carries a leg key,
// a note or a resolver: it is the reconciliation view, not the row's every
// column.
type CardAttemptSummary struct {
	Ref           string    `json:"ref"`
	ID            string    `json:"id"`
	Status        string    `json:"status"`
	AmountCents   int64     `json:"amount_cents"`
	BranchID      string    `json:"branch_id"`
	TillSessionID string    `json:"till_session_id"`
	CreatedAt     time.Time `json:"created_at"`
	SaleKey       string    `json:"sale_key"`
	ChargeRef     string    `json:"charge_ref"`
	ReversalRef   string    `json:"reversal_ref"`
	LastError     string    `json:"last_error"`
}

func attemptSummary(a Attempt) CardAttemptSummary {
	return CardAttemptSummary{
		Ref:           shortAttemptID(a.ID),
		ID:            a.ID,
		Status:        a.Status,
		AmountCents:   a.AmountCents,
		BranchID:      a.BranchID,
		TillSessionID: a.TillSessionID,
		CreatedAt:     a.CreatedAt,
		SaleKey:       a.SaleKey,
		ChargeRef:     a.ProcessorRef,
		ReversalRef:   a.ReversalRef,
		LastError:     a.LastError,
	}
}

// shortAttemptID names an attempt the way a manager reads it: the head of its
// id (pos's shortAttemptRef shape, so the same row reads the same everywhere).
func shortAttemptID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// looksLikeUUID refuses a path id the SQL ::uuid cast would reject before the
// cast turns a client typo into a server error (the audit handler's rule).
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}

// listAttempts answers GET /api/v1/payments/card-attempts. state=open is the
// needs-a-person filter; from and to are RFC3339 and bound the arm of the
// list that carries a processor ref; a missing pair defaults from to the
// seven day window, so the range is bounded whatever the caller sends (the
// store refuses a list with neither the open filter nor a range).
func (h *Handler) listAttempts(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	q := r.URL.Query()
	listQ := ListQuery{OpenOnly: q.Get("state") == "open"}
	if v := q.Get("from"); v != "" {
		from, err := time.Parse(time.RFC3339, v)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "from must be an RFC3339 timestamp")
			return
		}
		listQ.From = from
	}
	if v := q.Get("to"); v != "" {
		to, err := time.Parse(time.RFC3339, v)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "to must be an RFC3339 timestamp")
			return
		}
		listQ.To = to
	}
	if listQ.From.IsZero() && listQ.To.IsZero() {
		listQ.From = time.Now().Add(-7 * 24 * time.Hour)
	}
	attempts, err := h.store.ListOpen(r.Context(), listQ)
	if err != nil {
		h.log.Error("payment: attempt list failed", "err", err)
		httpx.Error(w, http.StatusServiceUnavailable, "attempt list failed")
		return
	}
	out := make([]CardAttemptSummary, 0, len(attempts))
	for _, a := range attempts {
		out = append(out, attemptSummary(a))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

type resolveRequest struct {
	Resolution string `json:"resolution"`
	Note       string `json:"note"`
}

// resolveAttempt answers POST /api/v1/payments/card-attempts/{id}/resolve,
// the manager's close of an attempt that needed a person. It is
// reason-required (a known resolution and a non-empty note, refused before
// anything is stored) and audited (the store's Resolve carries the caller's
// own user id as the resolver). The row is read before the move, so an
// unknown id answers 404 and a bound attempt answers 409 without asking the
// store for a move it would refuse; the move's own guard stays the arbiter
// for the window between the read and the move. The caller must reach the
// attempt's branch (a global role reaches every branch).
func (h *Handler) resolveAttempt(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var req resolveRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if req.Resolution != ResolutionNotCharged && req.Resolution != ResolutionReversedAtProcessor {
		httpx.Error(w, http.StatusBadRequest, "resolution must be NOT_CHARGED or REVERSED_AT_PROCESSOR")
		return
	}
	if strings.TrimSpace(req.Note) == "" {
		httpx.Error(w, http.StatusBadRequest, "note is required")
		return
	}
	id := r.PathValue("id")
	if !looksLikeUUID(id) {
		httpx.Error(w, http.StatusBadRequest, "id must be a valid UUID")
		return
	}
	cur, ok, err := h.store.getByID(r.Context(), id)
	if err != nil {
		h.log.Error("payment: resolve read failed", "err", err)
		httpx.Error(w, http.StatusServiceUnavailable, "resolve failed")
		return
	}
	// An attempt of a branch the caller does not reach answers exactly as an unknown id: 404 with the same
	// words, so the route neither confirms the row exists nor names its branch (the till's convention for
	// another branch's payment records), and it is checked before any status answer can leak the row's state.
	if !ok || !caller.CanAccessBranch(cur.BranchID) {
		httpx.Error(w, http.StatusNotFound, "no card attempt with that id")
		return
	}
	if cur.Status == AttemptBound {
		httpx.Error(w, http.StatusConflict, "the attempt is not open to a resolve")
		return
	}
	if req.Resolution == ResolutionNotCharged && (cur.Status == AttemptVoidUnknown || cur.Status == AttemptVoidDeclined) {
		// The store's pairing rule, said plainly: a row whose capture was
		// confirmed before its reversal went missing or was refused cannot
		// truthfully close as not charged.
		httpx.Error(w, http.StatusBadRequest, "a reversed attempt closes as REVERSED_AT_PROCESSOR")
		return
	}
	actorID := identity.StaffUserIDFromContext(r.Context())
	moved, ok, err := h.store.Resolve(r.Context(), id, req.Resolution, req.Note, actorID)
	if errors.Is(err, ErrResolveInvalid) {
		// The store's own reason and pairing checks are the backstop for a
		// shape the route's checks above cannot see (a VOID_* row cannot
		// close as NOT_CHARGED): the client's error, never a 500.
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		h.log.Error("payment: resolve failed", "err", err)
		httpx.Error(w, http.StatusServiceUnavailable, "resolve failed")
		return
	}
	if !ok {
		// The row moved between the read and the move (the sweep, a bind, or
		// a second close won the guard): the conflict word, never a silent ok.
		httpx.Error(w, http.StatusConflict, "the attempt is not open to a resolve")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, attemptSummary(moved))
}
