package warranty

import (
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"
	"strconv"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// handler_w5b.go, the Wave 5 Swarm B HTTP surface: money-path reads, the WTY-RECOVERY board (DIB-221), the
// PUBLIC intake doorway (DIB-222) and the SLA surface (DIB-223).
//
// GUARDS. Reads are staff. Logging a recovery action is staff (a rep chasing a vendor is doing their job).
// The SLA sweep and alert configuration reads are staff. There is exactly ONE unguarded route and it is
// registered from its own method with its own name, see RegisterPublicRoutes.

// RegisterW5Routes wires the STAFF-side Wave-5 surface. Additive: no route registered here existed before, and
// nothing in RegisterRoutes changes.
func (h *Handler) RegisterW5Routes(r httpx.Registrar, g *guards.Guards) {
	// CARRY NOTE (guards swap, HH-3.2 B5): the pin failed fast when the
	// composition root passed nil middleware; the Guards constructor set
	// carries the same fail-fast (NewGuards refuses to build without its
	// collaborators, and a nil Guards panics on the first registration).
	if g == nil {
		panic("warranty.RegisterW5Routes: guards are required")
	}
	// ERP:warranty-p2, what the money actually did.
	r.Handle("GET /api/v1/warranty/claims/{id}/postings", g.RequireStaff(h.staff(h.claimPostings)))

	// DIB-221, WTY-RECOVERY.
	r.Handle("GET /api/v1/warranty/recovery", g.RequireStaff(h.staff(h.recoveryBoard)))
	r.Handle("GET /api/v1/warranty/claims/{id}/recovery", g.RequireStaff(h.staff(h.recoveryActions)))
	r.Handle("POST /api/v1/warranty/claims/{id}/recovery", g.RequireStaff(h.staff(h.logRecoveryAction)))

	// DIB-222, the STAFF side of intake evidence (the public side is RegisterPublicRoutes).
	r.Handle("GET /api/v1/warranty/claims/{id}/photos", g.RequireStaff(h.staff(h.intakePhotos)))
	r.Handle("GET /api/v1/warranty/claims/{id}/photos/{photoId}", g.RequireStaff(h.staff(h.intakePhotoBytes)))

	// DIB-223, SLA.
	r.Handle("GET /api/v1/warranty/sla", g.RequireStaff(h.staff(h.slaBoard)))
	r.Handle("GET /api/v1/warranty/sla/targets", g.RequireStaff(h.staff(h.slaTargets)))
	// The sweep RAISES alerts and emits notifications, a write, and manager-gated for the same reason the
	// settlement route is: it is the button that makes the system tell people things.
	r.Handle("POST /api/v1/warranty/sla/sweep", g.RequireManager(h.staff(h.slaSweep)))
	r.Handle("POST /api/v1/warranty/claims/{id}/sla/{alertId}/ack", g.RequireStaff(h.staff(h.ackSLAAlert)))
}

// RegisterPublicRoutes registers the UNAUTHENTICATED warranty intake doorway (DIB-222).
//
// Kept a separate method with a separate name so that "this route has no guard" is a decision someone makes
// explicitly in main.go, not something that happens because a path started with the right prefix. Mirrors
// dispatch.Handler.RegisterPublicRoutes and quote.StaffHandler.RegisterPublicRoutes.
//
// ONE ROUTE, ONE VERB, ONE WRITE. There is no public GET here and there must not be one: the reference number
// the POST returns is a receipt, not a lookup key. The service method this calls takes no staff context and
// cannot reach a table outside warranty.
func (h *Handler) RegisterPublicRoutes(r httpx.Registrar) {
	r.Handle("POST /api/public/v1/warranty/intake", http.HandlerFunc(h.publicIntake))
}

// - ERP:warranty-p2 -

func (h *Handler) claimPostings(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	items, err := h.svc.SettlementPostings(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// - DIB-221 WTY-RECOVERY -

func (h *Handler) recoveryBoard(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	// open_only defaults TRUE: this is a WORKLIST. A reconciled claim belongs in history, not on the board a
	// rep works down, and making them opt IN to the noise is the right default.
	openOnly := q.Get("open_only") != "false"
	board, err := h.svc.RecoveryBoardView(r.Context(), q.Get("branch_id"), q.Get("vendor_id"), openOnly)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, board)
}

func (h *Handler) recoveryActions(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	items, err := h.svc.RecoveryActions(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) logRecoveryAction(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in RecoveryActionInput
	if !h.decode(w, r, &in) {
		return
	}
	a, err := h.svc.LogRecoveryAction(r.Context(), r.PathValue("id"), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, a)
}

// - DIB-222 intake -

// publicIntake is THE unguarded route.
//
// THREE REFUSALS HAPPEN HERE, BEFORE THE SERVICE SEES ANYTHING:
//   - a wrong Content-Type is a 415. The body is JSON; accepting whatever arrives and hoping the decoder
//     copes is how a form becomes a fuzzing target.
//   - an oversized body is a 413 via MaxBytesReader, so the process never allocates the whole of a hostile
//     upload. The cap is read from the 0131 config row, not a constant, so an operator can tighten it live.
//   - malformed JSON is a 400.
//
// The rate limit is enforced in the SERVICE (it needs the clock and the store), not here, so it applies to
// every caller of the domain method rather than to one HTTP shape.
func (h *Handler) publicIntake(w http.ResponseWriter, r *http.Request) {
	if ct := normalizeIntakeType(r.Header.Get("Content-Type")); ct != "application/json" {
		httpx.Error(w, http.StatusUnsupportedMediaType, "warranty: the intake form must be submitted as application/json")
		return
	}
	// The body cap comes from configuration. Reading it costs one indexed row and buys an operator the ability
	// to shrink the door during an incident without a deploy.
	maxBody := int64(8 << 20)
	if cfg, err := h.svc.PublicIntakeLimits(r.Context()); err == nil && cfg.MaxBodyBytes > 0 {
		maxBody = int64(cfg.MaxBodyBytes)
	}
	var in PublicIntakeInput
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	if err := dec.Decode(&in); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			httpx.Error(w, http.StatusRequestEntityTooLarge,
				"warranty: the submission is too large, please attach fewer or smaller photos")
			return
		}
		httpx.Error(w, http.StatusBadRequest, "warranty: the submission could not be read")
		return
	}
	res, err := h.svc.SubmitPublicIntake(r.Context(), in, r.RemoteAddr)
	if err != nil {
		// The public error surface is the ordinary clientError taxonomy, which already refuses to leak: every
		// message on this path names a FIELD or a limit, never a table, an id or an internal rule.
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) intakePhotos(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	items, err := h.svc.IntakePhotos(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// intakePhotoBytes serves one photo. The stored content type is echoed back, it is one of three raster types
// checked against the magic bytes at the door, so echoing it cannot serve script. nosniff and a private
// Cache-Control ride along because this is evidence on a customer claim, not a public asset.
func (h *Handler) intakePhotoBytes(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	p, err := h.svc.IntakePhotoBytes(r.Context(), r.PathValue("id"), r.PathValue("photoId"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	w.Header().Set("Content-Type", p.ContentType)
	w.Header().Set("Content-Length", strconv.Itoa(p.ByteSize))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=300")
	if p.Checksum != "" {
		w.Header().Set("ETag", `"`+p.Checksum+`"`)
		if match := r.Header.Get("If-None-Match"); match == `"`+p.Checksum+`"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(p.Bytes)
}

// - DIB-223 SLA -

func (h *Handler) slaBoard(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	view, err := h.svc.SLABoard(r.Context(), q.Get("branch_id"), q.Get("include_acknowledged") == "true")
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, view)
}

func (h *Handler) slaTargets(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	items, err := h.svc.SLATargetList(r.Context())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) slaSweep(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	raised, err := h.svc.SweepSLA(r.Context(), r.URL.Query().Get("branch_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"raised": raised, "count": len(raised)})
}

func (h *Handler) ackSLAAlert(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if err := h.svc.AcknowledgeSLAAlert(r.Context(), r.PathValue("id"), r.PathValue("alertId")); err != nil {
		h.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
