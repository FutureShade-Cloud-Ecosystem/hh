package dispatch

// handler_loadboard.go, the HTTP surface for the load board (0184, 0185). Thin by policy, like
// handler.go: it decodes, delegates and maps errors. Every authority decision, the money gate, the
// commit capability, the manager check on a reopen, is in service_loadboard.go, one place to read.

import (
	"context"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

func (h *Handler) loadBoard(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	view, err := h.svc.LoadBoard(r.Context(), q.Get("branch_id"), q.Get("date"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	// The pickup card counts OPEN CUSTOMER PICKUPS split by stage (D-110), which are not delivery
	// stops and are therefore not derivable from the board's rows. Counting them here keeps the card
	// honest about its source.
	//
	// A FAILED COUNT LEAVES THE CARD AT ZERO AND THAT IS DELIBERATE, unchanged from the shape this
	// replaces: the board is the answer to a different question and must still render. The service
	// already degrades a failed STAGE SPLIT into "all awaiting pick" rather than into an error, so
	// reaching this branch at all means the population itself could not be read.
	if awaiting, ready, capped, cerr := h.svc.PendingPickupCounts(r.Context(), q.Get("branch_id")); cerr == nil {
		view.Counts.SetPickupCounts(awaiting, ready, capped)
	}
	h.noStore(w, view)
}

func (h *Handler) loadBoardCalendar(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	days, err := h.svc.Calendar(r.Context(), q.Get("branch_id"), q.Get("from"), q.Get("to"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	h.noStore(w, map[string]any{"items": days})
}

func (h *Handler) loadBoardGaps(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	gaps, checked, err := h.svc.BoardGaps(r.Context(), q.Get("branch_id"), q.Get("date"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	h.noStore(w, map[string]any{"items": gaps, "checked": checked})
}

// orderGaps is the yard's half of the gap scan: what one order is short of, asked for at the moment
// a picker opens the ticket rather than swept for a whole day's board.
func (h *Handler) orderGaps(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	gaps, checked, err := h.svc.GapsForOrder(r.Context(), r.URL.Query().Get("branch_id"), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	h.noStore(w, map[string]any{"items": gaps, "checked": checked})
}

type markLoadedBody struct {
	StopIDs []string `json:"stop_ids"`
}

// markLoaded is the bulk gesture. It answers 200 with a PER-STOP outcome list even when some stops
// refused, because a bulk load where one of nine did not take is not a failed request, it is eight
// loaded trucks and one thing to go and look at.
func (h *Handler) markLoaded(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var b markLoadedBody
	if !decodeJSON(w, r, &b) {
		return
	}
	outcomes, err := h.svc.MarkLoaded(r.Context(), b.StopIDs)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": outcomes})
}

func (h *Handler) readDispatchPolicy(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	p, err := h.svc.ReadPolicy(r.Context(), r.URL.Query().Get("branch_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	h.noStore(w, p)
}

// policyHistory is the quiet trail behind the settings screen's disclosure.
func (h *Handler) policyHistory(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	items, err := h.svc.PolicyHistory(r.Context(), r.URL.Query().Get("branch_id"), 0)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	h.noStore(w, map[string]any{"items": items})
}

func (h *Handler) saveDispatchPolicy(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in Policy
	if !decodeJSON(w, r, &in) {
		return
	}
	p, err := h.svc.SavePolicy(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

type reopenBody struct {
	Reason string `json:"reason"`
}

// reopenStop is the ONE-ACTION path the ruling asks for: a manager reopens a committed load from the
// load itself. Nothing about the route is exceptional, because the act is not.
func (h *Handler) reopenStop(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var b reopenBody
	if !decodeJSON(w, r, &b) {
		return
	}
	stop, err := h.svc.ReopenStop(r.Context(), r.PathValue("id"), b.Reason)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, stop)
}

// reopenRun is the bulk form: "unlock and run it again" (the dealer), i.e. reopen the whole truck.
func (h *Handler) reopenRun(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var b reopenBody
	if !decodeJSON(w, r, &b) {
		return
	}
	run, err := h.svc.ReopenRun(r.Context(), r.PathValue("id"), b.Reason)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, run)
}

func (h *Handler) clearRunReopen(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	run, err := h.svc.ClearRunReopen(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, run)
}

type commitBody struct {
	ScheduledDate string `json:"scheduled_date"`
}

func (h *Handler) commitStop(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var b commitBody
	if !decodeJSON(w, r, &b) {
		return
	}
	stop, err := h.svc.CommitStop(r.Context(), r.PathValue("id"), b.ScheduledDate)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, stop)
}

func (h *Handler) setLoadFacts(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in LoadFactsInput
	if !decodeJSON(w, r, &in) {
		return
	}
	stop, err := h.svc.SetLoadFacts(r.Context(), r.PathValue("id"), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, stop)
}

func (h *Handler) splitFreight(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in SplitInput
	if !decodeJSON(w, r, &in) {
		return
	}
	res, err := h.svc.SplitFreight(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// freightRaise answers the card behind a FREIGHT_ABOVE_QUOTE approval: what am I agreeing to?
//
// IT ANSWERS 200 WITH NO MONEY rather than 403, for a caller the dealer's load-value policy refuses.
// Those are two different facts and the approvals queue needs both: an approver who may not see load
// value can still see WHICH order and WHOSE, and can still tell the difference between a request
// that exists and one that has been decided. What they cannot see is the figures, and they cannot
// see them by being absent from the payload, not by a screen declining to draw them.
func (h *Handler) freightRaise(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	view, err := h.svc.FreightRaiseByID(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	h.noStore(w, view)
}

// autoAssignSeam is the PRO/AI MODULE SEAM and nothing more.
//
// The call was explicit that auto-assign / auto-triage is a Pro tier module and that native basic
// dispatch ships in the core product. So the core registers the ROUTE and answers 501 with a
// sentence naming what would answer it, which is the honest thing a scaffolded seam does
// (httpx.NotImplemented's own header says so), rather than shipping a heuristic that looks like
// intelligence, or leaving the surface with a dead button and no explanation.
//
// A Pro build satisfies AutoAssigner and this route starts answering. Nothing else changes.
type AutoAssigner interface {
	ProposeAssignments(ctx context.Context, branchID, date string) (any, error)
}

func (h *Handler) autoAssign(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	httpx.NotImplemented(w, "dispatch: automatic load assignment is part of the Pro module, build the loads here in the meantime")
}

// writeErrLoadboard extends the taxonomy with the two errors this file's service can raise. It is
// folded into writeErr rather than living beside it so there is still ONE mapping.
func loadboardStatus(err error) (int, string, bool) {
	var se *settings.StatusError
	switch {
	case errors.Is(err, ErrReopenNotPermitted), errors.Is(err, ErrReopenDisabled),
		errors.Is(err, ErrCommitNotPermitted):
		// 403, not 409: the WORLD is fine, this person is not allowed. The error's OWN sentence is
		// what goes on the wire, the three refusals are different and a shared string would send a
		// general manager looking for a dispatch manager who cannot help either, or tell a salesperson
		// to wait for a window that is not what stopped them.
		return http.StatusForbidden, err.Error(), true
	case errors.Is(err, ErrLoadCommitted):
		return http.StatusConflict, err.Error(), true
	case errors.As(err, &se):
		return se.Status, se.Msg, true
	}
	return 0, "", false
}

func (h *Handler) noStore(w http.ResponseWriter, v any) {
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, v)
}

// defaultCalendarRange is the week the calendar opens on when a caller names no range.
func defaultCalendarRange(now time.Time) (string, string) {
	start := now.UTC().Truncate(24 * time.Hour)
	return start.Format("2006-01-02"), start.AddDate(0, 0, 6).Format("2006-01-02")
}
