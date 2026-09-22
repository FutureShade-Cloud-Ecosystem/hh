package billing

// monthend_handler.go, the AR / office month-end HTTP surface (DIB-65, DIB-52, DIB-166).
//
// GATING, and why it is not staff-wide: every read here exposes customer financial distress, who is
// over their limit, who is 90 days late, who is on hold and why. That is exactly the PII the till's
// bounced-payment and credit-hold reads are manager-gated for, and this surface follows the same rule
// rather than inventing a looser one next door to it. The override is stricter still: releasing or
// overriding a hold is a money decision with a named person attached, so it is admin (Tier-1), which
// is the level till's existing release already requires.

import (
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"log/slog"
	"net/http"
	"time"
)

const maxMonthEndBody = 1 << 20 // 1 MiB

// MonthEndHandler serves the AR month-end console.
type MonthEndHandler struct {
	userLink   identity.UserIDLinker
	credit     *CreditService
	statements *StatementService
	log        *slog.Logger
}

// NewMonthEndHandler wires the services.
func NewMonthEndHandler(credit *CreditService, statements *StatementService, log *slog.Logger) *MonthEndHandler {
	if log == nil {
		log = slog.Default()
	}
	return &MonthEndHandler{credit: credit, statements: statements, log: log}
}

// RegisterRoutes self-registers the month-end routes.
//
// The paths deliberately sit under /api/v1/ar/ alongside till's existing credit-hold routes rather
// than under a new prefix: an operator reasoning about "the AR surface" should find one namespace,
// not two that split by which Go package happened to implement them.
func (h *MonthEndHandler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/ar/aging", g.RequireManager(h.staff(h.aging)))
	r.Handle("GET /api/v1/ar/holds", g.RequireManager(h.staff(h.listHolds)))
	r.Handle("POST /api/v1/ar/holds/{id}/override", g.RequireAdmin(h.staff(h.override)))
	r.Handle("POST /api/v1/ar/holds/{id}/ar-release", g.RequireAdmin(h.staff(h.release)))
	r.Handle("POST /api/v1/ar/credit-sweep", g.RequireAdmin(h.staff(h.sweep)))
	// DIB-483 / M3-G7B-NO-HOLD-SWITCH, the master switch for automatic credit holds (autohold.go).
	// 0104 ships it FALSE by design and nothing in the codebase could turn it on. Tech-admin gated,
	// per the DIB-434 caveat (admins own enablement knobs); the THRESHOLDS behind it are read-only
	// here and stay client-set.
	r.Handle("GET /api/v1/ar/auto-hold", g.RequireAdmin(h.staff(h.autoHold)))
	r.Handle("PUT /api/v1/ar/auto-hold", g.RequireAdmin(h.staff(h.setAutoHold)))
	r.Handle("GET /api/v1/ar/dispatch-gate/{orderID}", g.RequireManager(h.staff(h.dispatchGate)))
	r.Handle("POST /api/v1/ar/statement-runs", g.RequireManager(h.staff(h.statementRun)))
	r.Handle("GET /api/v1/ar/statement-runs/{id}", g.RequireManager(h.staff(h.getStatementRun)))
	r.Handle("GET /api/v1/ar/customers/{customerID}/statement", g.RequireManager(h.staff(h.latestStatement)))
}

// aging returns the AR aging worklist. ?branch_id= scopes it; ?as_of=YYYY-MM-DD backdates it.
func (h *MonthEndHandler) aging(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	asOf, err := parseAsOf(r.URL.Query().Get("as_of"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	rows, err := h.credit.Aging(r.Context(), asOf, r.URL.Query().Get("branch_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	// A nil slice would serialize as null and make every consumer write a null check.
	if rows == nil {
		rows = []AgingRow{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": rows, "as_of": asOf.Format("2006-01-02")})
}

// listHolds returns credit holds; ?status=ACTIVE|RELEASED filters.
func (h *MonthEndHandler) listHolds(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	items, err := h.credit.ListHolds(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if items == nil {
		items = []CreditHold{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

type overrideRequest struct {
	Reason string `json:"reason"`
	// Hours is how long the override lasts. There is no "indefinite" option, and that is the design:
	// an override with no end is a release wearing a disguise, and it destroys the record that the
	// account was ever over its limit.
	Hours int `json:"hours"`
}

func (h *MonthEndHandler) override(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var req overrideRequest
	if err := decodeJSON(w, r, &req); err != nil {
		h.writeErr(w, r, err)
		return
	}
	if req.Hours <= 0 {
		req.Hours = 24
	}
	if req.Hours > 24*30 {
		h.writeErr(w, r, httpx.BadRequestf("an override may not exceed 30 days, a longer one is a release, "+
			"and a release is a different decision with a different record"))
		return
	}
	caller, _ := identity.StaffContextFromContext(r.Context())
	until := time.Now().Add(time.Duration(req.Hours) * time.Hour)
	if err := h.credit.Override(r.Context(), r.PathValue("id"), req.Reason, until, caller.UserID, callerBranch(caller)); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok": true, "override_until": until.UTC().Format(time.RFC3339), "reason": req.Reason,
	})
}

type reasonRequest struct {
	Reason string `json:"reason"`
}

func (h *MonthEndHandler) release(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var req reasonRequest
	if err := decodeJSON(w, r, &req); err != nil {
		h.writeErr(w, r, err)
		return
	}
	caller, _ := identity.StaffContextFromContext(r.Context())
	if err := h.credit.Release(r.Context(), r.PathValue("id"), req.Reason, caller.UserID, callerBranch(caller)); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// sweep runs the credit evaluation on demand. It is the same code the nightly job runs, so an
// operator can see the dry-run blast radius before flipping auto_hold_enabled rather than finding out
// at the counter the next morning.
func (h *MonthEndHandler) sweep(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	res, err := h.credit.Evaluate(r.Context(), r.URL.Query().Get("branch_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// -- DIB-483: the auto-hold master switch ---------------------------------------------------------

// autoHold reads the effective setting: the switch, plus the thresholds it would apply, read-only.
//
// It also reports whether the switch is WRITABLE on this deployment (`switch_wired`), so the client
// can render the true state read-only instead of offering a control that would fail, the same
// seam_wired posture the quote hold-expiry banner takes.
func (h *MonthEndHandler) autoHold(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	setting, err := h.credit.AutoHold(r.Context(), r.URL.Query().Get("branch_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, autoHoldResponse{
		AutoHoldSetting: setting,
		SwitchWired:     h.credit.AutoHoldSwitchWired(),
	})
}

// autoHoldResponse is the setting plus the one fact about the SERVER the client needs.
type autoHoldResponse struct {
	AutoHoldSetting
	SwitchWired bool `json:"switch_wired"`
}

type autoHoldRequest struct {
	Enabled  *bool  `json:"enabled"` // a POINTER: an absent field is a bad request, not "false"
	BranchID string `json:"branch_id"`
	Reason   string `json:"reason"`
}

// setAutoHold flips the switch.
//
// `enabled` is a *bool so that a body which omits it is REFUSED rather than silently read as false.
// This is the one field on the request and it decides whether a machine may stop customers
// transacting; a malformed body that turns the safety flag off by accident is the worst available
// failure mode, and it is the default one for a plain bool.
func (h *MonthEndHandler) setAutoHold(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var req autoHoldRequest
	if err := decodeJSON(w, r, &req); err != nil {
		h.writeErr(w, r, err)
		return
	}
	if req.Enabled == nil {
		h.writeErr(w, r, httpx.BadRequestf("enabled must be true or false, say which"))
		return
	}
	caller, _ := identity.StaffContextFromContext(r.Context())
	setting, err := h.credit.SetAutoHold(r.Context(), req.BranchID, *req.Enabled, caller.UserID, req.Reason)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, autoHoldResponse{
		AutoHoldSetting: setting,
		SwitchWired:     h.credit.AutoHoldSwitchWired(),
	})
}

// dispatchGate exposes the DIB-52 predicate as a read, so the office can answer "why is this order
// not going out?" without reading logs. Swarm C consumes the SEAM in-process, not this route.
func (h *MonthEndHandler) dispatchGate(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	allowed, reason := h.credit.CanDispatch(r.Context(), r.PathValue("orderID"))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"allowed": allowed, "reason": reason})
}

type statementRunRequest struct {
	Mode       string `json:"mode"`        // MASS | INTERIM
	BranchID   string `json:"branch_id"`   // "" = all branches
	CustomerID string `json:"customer_id"` // required for INTERIM
	AsOf       string `json:"as_of"`       // YYYY-MM-DD; empty = today
}

func (h *MonthEndHandler) statementRun(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var req statementRunRequest
	if err := decodeJSON(w, r, &req); err != nil {
		h.writeErr(w, r, err)
		return
	}
	asOf, err := parseAsOf(req.AsOf)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	caller, _ := identity.StaffContextFromContext(r.Context())
	var only []string
	if req.CustomerID != "" {
		only = []string{req.CustomerID}
	}
	mode := req.Mode
	if mode == "" {
		mode = RunModeMass
	}
	run, statements, err := h.statements.GenerateRun(r.Context(), mode, req.BranchID, asOf, caller.UserID, only...)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if statements == nil {
		statements = []Statement{}
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"run": run, "statements": statements})
}

func (h *MonthEndHandler) getStatementRun(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	run, statements, err := h.statements.GetRun(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if statements == nil {
		statements = []Statement{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"run": run, "statements": statements})
}

// latestStatement serves one account's most recent statement. ?format=html returns the stored
// artefact itself, which is what the print path opens, the SAME bytes that were generated, never a
// re-render, because a re-render is a different document.
func (h *MonthEndHandler) latestStatement(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	st, ok, err := h.statements.LatestStatement(r.Context(), r.PathValue("customerID"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if !ok {
		h.writeErr(w, r, ErrNotFound)
		return
	}
	if r.URL.Query().Get("format") == "html" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// The stored HTML is served with a restrictive CSP because it is generated content persisted
		// and later re-served. The renderer escapes everything customer-supplied; this is the second
		// layer, and two layers is the right number for stored markup.
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(st.HTML))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, st)
}

// callerBranch resolves a single branch for the audit row. identity.StaffContext carries a SET of
// granted branches (a manager can hold several) and GLOBAL scope carries none, so there is no single
// "the caller's branch" to read. An audit row wants one, and the honest answer for a multi-branch or
// global caller is to leave it empty rather than pick one arbitrarily and make the trail look more
// precise than it is, the actor id is what identifies the person, and that is always recorded.
func callerBranch(c identity.StaffContext) string {
	if len(c.Branches) != 1 {
		return ""
	}
	for id := range c.Branches {
		return id
	}
	return ""
}

// parseAsOf reads a YYYY-MM-DD date, defaulting to now.
func parseAsOf(s string) (time.Time, error) {
	if s == "" {
		return time.Now(), nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, httpx.BadRequestf("as_of must be YYYY-MM-DD")
	}
	return t, nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxMonthEndBody)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		return httpx.BadRequestf("invalid JSON body")
	}
	return nil
}

// writeErr maps domain errors onto status codes. A not-found is 404 and never 403: the AR console must
// not become a way to discover which customer ids exist.
func (h *MonthEndHandler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var bad httpx.BadRequestError
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not found")
	case errors.Is(err, ErrAutoHoldNotConfigured):
		// DIB-483: an unwired seam is a DEPLOY fault, not a policy one. 503 and name it, so an
		// operator reports the right problem to the right person instead of arguing with AR.
		httpx.Error(w, http.StatusServiceUnavailable,
			"the auto-hold switch is not configured on this server, the AR policy write seam is not wired")
	case errors.As(err, &bad):
		httpx.Error(w, http.StatusBadRequest, bad.Error())
	default:
		h.log.Warn("ar month-end request failed", "path", r.URL.Path, "err", err)
		// The message is echoed because every error surfaced here is a policy refusal a human needs to
		// read ("an override reason is required"), not an internal detail. Nothing in this package
		// wraps a credential or a query into an error message.
		httpx.Error(w, http.StatusBadRequest, err.Error())
	}
}
