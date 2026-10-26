// activity.go, DIB-492 the one-click order activity stream + DIB-493 CRM-style human notes.
//
//	GET   /api/v1/orders/{id}/activity        → the merged, newest-first activity for one order
//	POST  /api/v1/orders/{id}/notes           → record a human note (channel + free text)
//	PATCH /api/v1/orders/{id}/notes/{noteId}  → correct a note (edited-by/edited-at attribution)
//
// DIB-492 is a PRESENTATION task: every system action on an order is already logged, the
// ORDER_STATUS_CHANGED / ORDER_CHANGED trail (history.go, edit_history.go), the pos.sale completion
// record, the invoice and payment rows the money modules own. What a dealer doing an order audit
// lacks is ONE compact, human-readable list. This file is that read: it reuses the SAME audit seam
// and the SAME authz preamble the status/edit history reads use, renders each row as a short human
// sentence ("changed the requested delivery date from … to …", never an HTTP-verb framing), and
// merges in the order's immediate documents, its invoices and the payments applied to them, plus
// the DIB-493 notes, newest first.
//
// DIB-493 notes are ordinary business rows (migration 0161, order_notes, owned and written ONLY by
// this module), NOT audit rows: a note is something a person said, editable with attribution,
// which the append-only trail must never be. The write is registered through the same route-guard
// wrapper every order write uses (protected by construction), the author is the SESSION caller
// (never client-supplied), and the POST carries a one-time idempotency key so a retried save
// converges on the one note it already wrote (the reorder precedent).
//
// SUMMARY DISCIPLINE. The summary names the CHANGE and never the actor, the actor rides in its own
// attributed columns (actor_id/actor_name), so a client renders "the actor · changed the requested
// delivery date…" without the server guessing at sentence order. Coarse HTTP.* middleware rows are
// deliberately SKIPPED: each one shadows an entity-grain row that already tells the story, and
// "POST /api/v1/orders/…" is exactly the API-framing this surface exists to hide. Consequence
// columns of one edit (qty_base, line/order totals, the reprice) are folded away for the same
// reason, one human action reads as one row.
package order

import (
	"context"
	"errors"
	"fmt"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// maxNoteLen bounds a note body (mirrors the 0161 CHECK, enforced here first so the refusal is a
// clear 400 rather than a constraint error).
const maxNoteLen = 4000

// noteChannels is the fixed vocabulary of HOW the conversation happened (0161 CHECK set).
var noteChannels = map[string]bool{"PHONE": true, "EMAIL": true, "IN_PERSON": true, "OTHER": true}

// - wire shapes -

// ActivityRow is ONE normalized activity entry: when, who (display name, resolved server-side), and
// a short human summary. Kind "note" rows additionally carry the note fields so the client can
// render the badge, the body, and the edit attribution.
type ActivityRow struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"` // "event" | "note"
	OccurredAt time.Time `json:"occurred_at"`
	ActorID    string    `json:"actor_id,omitempty"`
	ActorName  string    `json:"actor_name,omitempty"` // "" for a system/portal actor, the client renders its own fallback
	Summary    string    `json:"summary"`
	// note-only fields
	Channel      string     `json:"channel,omitempty"` // PHONE | EMAIL | IN_PERSON | OTHER
	Body         string     `json:"body,omitempty"`
	EditedByName string     `json:"edited_by_name,omitempty"`
	EditedAt     *time.Time `json:"edited_at,omitempty"`
	// seq is the audit causal key (0078) used only as the same-instant tiebreaker; not on the wire.
	seq int64
}

// OrderActivity is one order's merged activity, NEWEST FIRST (the dealer audit reads backwards).
type OrderActivity struct {
	OrderID string        `json:"order_id"`
	Rows    []ActivityRow `json:"rows"`
}

// OrderNote is one persisted 0161 row, with display names resolved.
type OrderNote struct {
	ID           string     `json:"id"`
	OrderID      string     `json:"order_id"`
	AuthorID     string     `json:"author_id,omitempty"`
	AuthorName   string     `json:"author_name,omitempty"`
	Channel      string     `json:"channel"`
	Body         string     `json:"body"`
	EditedBy     string     `json:"edited_by,omitempty"`
	EditedByName string     `json:"edited_by_name,omitempty"`
	EditedAt     *time.Time `json:"edited_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	// Replayed marks an idempotent replay: the note already existed under this key, nothing new
	// was written.
	Replayed bool `json:"replayed,omitempty"`
}

// NoteDraft is the insert input the repository consumes. AuthorID is the resolved session caller -
// it never arrives from the client.
type NoteDraft struct {
	OrderID        string
	AuthorID       string
	Channel        string
	Body           string
	IdempotencyKey string
}

// DocEvent is one immediate-document fact (an invoice raised/voided, a payment applied) already
// shaped as a display row by the repository read. ActorName comes from the SQL's users join, so no
// second resolution pass is needed.
type DocEvent struct {
	ID         string
	Summary    string
	ActorID    string
	ActorName  string
	OccurredAt time.Time
}

// - store capabilities (asserted rather than assumed, the invoiceSourceStore precedent) -

// activityNoteStore is the 0161 persistence capability. The real *Repository implements it; a store
// that cannot (the unit fake) makes the notes surface report itself unconfigured instead of panicking.
type activityNoteStore interface {
	InsertOrderNote(ctx context.Context, d NoteDraft) (OrderNote, bool, error) // bool = idempotent replay
	UpdateOrderNoteBody(ctx context.Context, orderID, noteID, body, channel, editorID string) (OrderNote, error)
	ListOrderNotes(ctx context.Context, orderID string) ([]OrderNote, error)
}

// activityDocStore is the immediate-documents read capability (a read-only presentation projection -
// the till/history.go Q-044 precedent; never a money decision). Optional: a store without it simply
// yields an activity stream with no document rows, not an error.
type activityDocStore interface {
	OrderDocumentEvents(ctx context.Context, orderID string, withPayments bool) ([]DocEvent, error)
}

// ErrNotesNotConfigured reports a store with no 0161 capability behind a notes write.
var ErrNotesNotConfigured = errors.New("order: notes store not configured")

// - the activity read (DIB-492) -

// OrderActivityFor returns the merged activity for one order, newest first.
//
// withPayments says whether the stream carries the payment trail (ruling 87): the "applied a payment"
// rows with their amount, method and applier, and the tender named on a counter sale. A caller who
// does not hold the payment trail reads gets the stream without them, absent rather than blanked; the
// route's handler asks the payment trail reader (WithPaymentTrail) and passes its answer.
//
// AUTHZ is identical to the status/edit history reads (history.go): staff-gating at the route proves
// the caller is staff but NOT that they may reach THIS order, so the trusted caller is resolved, the
// order's owning branch loaded, and the read gated on CanAccessBranch. A MISSING order is ErrNotFound
// (never an existence leak); an existing order in an unreachable branch is ErrForbidden.
func (s *Service) OrderActivityFor(ctx context.Context, orderID string, withPayments bool) (OrderActivity, error) {
	orderID, err := s.scopedOrderID(ctx, orderID)
	if err != nil {
		return OrderActivity{}, err
	}
	out := OrderActivity{OrderID: orderID, Rows: []ActivityRow{}}

	// 1. The audit spine: the order's own trail plus the pos.sale completion records, which the POS
	// ring keys by the ORDER's id (pos/service.go SALE_COMPLETED). A nil seam degrades to "no system
	// events", the notes below still answer, mirroring the history reads' unwired-seam posture.
	var actorIDs []string
	if s.auditTrail != nil {
		for _, entity := range []string{"order", "pos.sale"} {
			entries, terr := s.auditTrail.ReopenTransaction(ctx, entity, orderID)
			if terr != nil {
				return OrderActivity{}, terr
			}
			for _, e := range entries {
				summary, ok := summarizeAuditEntry(e, withPayments)
				if !ok {
					continue
				}
				row := ActivityRow{
					ID: e.ID, Kind: "event", ActorID: e.ActorID, Summary: summary, seq: e.Seq,
				}
				if at, perr := time.Parse(time.RFC3339Nano, e.CreatedAt); perr == nil {
					row.OccurredAt = at
				}
				out.Rows = append(out.Rows, row)
				if e.ActorID != "" {
					actorIDs = append(actorIDs, e.ActorID)
				}
			}
		}
	}
	// Resolve staff display names for the audit rows (doc events and notes carry theirs already).
	// A resolution failure degrades to ids, it never fails the read (the history reads' contract).
	if s.names != nil && len(actorIDs) > 0 {
		if m, nerr := s.names.ActorNames(ctx, actorIDs); nerr == nil {
			for i := range out.Rows {
				if n := m[out.Rows[i].ActorID]; n != "" {
					out.Rows[i].ActorName = n
				}
			}
		}
	}

	// 2. Immediate documents: invoices raised against the order and payments applied to them.
	if ds, ok := s.store.(activityDocStore); ok {
		evs, derr := ds.OrderDocumentEvents(ctx, orderID, withPayments)
		if derr != nil {
			return OrderActivity{}, derr
		}
		for _, ev := range evs {
			out.Rows = append(out.Rows, ActivityRow{
				ID: ev.ID, Kind: "event", OccurredAt: ev.OccurredAt,
				ActorID: ev.ActorID, ActorName: ev.ActorName, Summary: ev.Summary,
			})
		}
	}

	// 2b. Money a reversal held and a manager applied to the order's invoices (SF-2r round 2): re-application writes no
	// allocation, so the payment rows above omit it. Part of the payment trail, absent for a caller who does not hold it.
	if withPayments && s.reapplied != nil {
		slices, rerr := s.reapplied.ReappliedToOrder(ctx, orderID)
		if rerr != nil {
			return OrderActivity{}, rerr
		}
		for _, sl := range slices {
			out.Rows = append(out.Rows, ActivityRow{
				ID: "reapplied:" + sl.ID, Kind: "event", OccurredAt: sl.AppliedAt, ActorID: sl.ActorID, ActorName: sl.ActorName,
				Summary: fmt.Sprintf("applied a %s payment (%s) to invoice %s, re applied from a reversed payment %s",
					moneyStr(sl.AmountCents), humanCode(sl.Method), sl.InvoiceNo, sl.PaymentNo),
			})
		}
	}

	// 3. Human notes (DIB-493) interleave on the same clock.
	if ns, ok := s.store.(activityNoteStore); ok {
		notes, nerr := ns.ListOrderNotes(ctx, orderID)
		if nerr != nil {
			return OrderActivity{}, nerr
		}
		for _, n := range notes {
			out.Rows = append(out.Rows, ActivityRow{
				ID: n.ID, Kind: "note", OccurredAt: n.CreatedAt,
				ActorID: n.AuthorID, ActorName: n.AuthorName,
				Summary: noteSummary(n.Channel),
				Channel: n.Channel, Body: n.Body,
				EditedByName: n.EditedByName, EditedAt: n.EditedAt,
			})
		}
	}

	// Newest first. Within one instant (rows written by one transaction share created_at) the audit
	// seq is the causal key, so the later write of a same-instant pair stays on top; stable keeps
	// deterministic order for rows with neither distinction.
	sort.SliceStable(out.Rows, func(i, j int) bool {
		if !out.Rows[i].OccurredAt.Equal(out.Rows[j].OccurredAt) {
			return out.Rows[i].OccurredAt.After(out.Rows[j].OccurredAt)
		}
		return out.Rows[i].seq > out.Rows[j].seq
	})
	return out, nil
}

// scopedOrderID is the shared authz preamble of every activity/notes entry point: resolve the trusted
// caller, accept the visible order_no as an alternate key WITHIN the caller's branch scope (B2-4),
// load the owning branch, and gate on CanAccessBranch. Mirrors OrderHistory/OrderEditHistoryFor.
func (s *Service) scopedOrderID(ctx context.Context, orderID string) (string, error) {
	if orderID == "" {
		return "", httpx.BadRequestf("order id is required")
	}
	caller, err := s.caller(ctx)
	if err != nil {
		return "", err
	}
	rGlobal, rBranches := callerBranchScope(caller)
	orderID, err = s.store.ResolveOrderID(ctx, orderID, rGlobal, rBranches)
	if err != nil {
		return "", err
	}
	branchID, err := s.store.OrderBranch(ctx, orderID)
	if err != nil {
		return "", err // ErrNotFound for a missing order, no existence leak
	}
	if !caller.CanAccessBranch(branchID) {
		return "", ErrForbidden
	}
	return orderID, nil
}

// - the notes writes (DIB-493) -

// AddOrderNote records one human note against the order. The author is the resolved SESSION caller -
// the input carries no author field at all, so it cannot be spoofed. idempotencyKey is the one-time
// client key: a retried POST returns the note the first attempt wrote, flagged Replayed.
func (s *Service) AddOrderNote(ctx context.Context, orderID, channel, body, idempotencyKey string) (OrderNote, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return OrderNote{}, err
	}
	channel = normalizeChannel(channel)
	if !noteChannels[channel] {
		return OrderNote{}, httpx.BadRequestf(
			"channel must be one of phone, email, in_person or other, pick how the conversation happened and try again")
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return OrderNote{}, httpx.BadRequestf("the note text is empty, write what was said, then save")
	}
	if len(body) > maxNoteLen {
		return OrderNote{}, httpx.BadRequestf("the note is over %d characters, trim it and save again", maxNoteLen)
	}
	if idempotencyKey == "" {
		return OrderNote{}, httpx.BadRequestf("idempotency_key is required")
	}
	if len(idempotencyKey) > 128 {
		return OrderNote{}, httpx.BadRequestf("idempotency_key exceeds 128 characters")
	}
	orderID, err = s.scopedOrderID(ctx, orderID)
	if err != nil {
		return OrderNote{}, err
	}
	ns, ok := s.store.(activityNoteStore)
	if !ok {
		return OrderNote{}, ErrNotesNotConfigured
	}
	n, replayed, err := ns.InsertOrderNote(ctx, NoteDraft{
		OrderID: orderID, AuthorID: caller.UserID,
		Channel: channel, Body: body, IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return OrderNote{}, err
	}
	n.Replayed = replayed
	return n, nil
}

// EditOrderNote corrects a note's text (and optionally its channel), stamping edited-by from the
// SESSION caller and edited-at server-side. Any active in-branch staffer may correct a note, the
// counter reality DIB-493 records, and the attribution is what keeps the correction honest.
func (s *Service) EditOrderNote(ctx context.Context, orderID, noteID, body, channel string) (OrderNote, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return OrderNote{}, err
	}
	if noteID == "" || !looksLikeUUID(noteID) {
		// A malformed note id must answer as missing, not blow up the ::uuid cast (the OrderBranch
		// L8 precedent).
		return OrderNote{}, ErrNotFound
	}
	if channel != "" {
		channel = normalizeChannel(channel)
		if !noteChannels[channel] {
			return OrderNote{}, httpx.BadRequestf(
				"channel must be one of phone, email, in_person or other, pick how the conversation happened and try again")
		}
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return OrderNote{}, httpx.BadRequestf("the corrected note text is empty, write what it should say, then save")
	}
	if len(body) > maxNoteLen {
		return OrderNote{}, httpx.BadRequestf("the note is over %d characters, trim it and save again", maxNoteLen)
	}
	orderID, err = s.scopedOrderID(ctx, orderID)
	if err != nil {
		return OrderNote{}, err
	}
	ns, ok := s.store.(activityNoteStore)
	if !ok {
		return OrderNote{}, ErrNotesNotConfigured
	}
	return ns.UpdateOrderNoteBody(ctx, orderID, noteID, body, channel, caller.UserID)
}

// normalizeChannel accepts the reasonable spellings a client might send ("phone", "in-person",
// "In_Person") and folds them onto the 0161 vocabulary.
func normalizeChannel(c string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(c), "-", "_"))
}

// noteSummary renders the fixed note headline for a channel, "note taken via phone call", matching
// the client wording ("taken by the actor · via phone call") without repeating the actor.
func noteSummary(channel string) string {
	switch channel {
	case "PHONE":
		return "note taken via phone call"
	case "EMAIL":
		return "note taken via email"
	case "IN_PERSON":
		return "note taken in person"
	default:
		return "note taken"
	}
}

// - audit-row humanizer (pure; unit-tested table-style) -

// summarizeAuditEntry renders one audit row as a short human sentence, or reports (_, false) for a
// row this surface deliberately does not show: coarse HTTP.* middleware records (API framing), the
// consequence columns of a single edit, and any action this file does not understand (unknown rows
// are OMITTED, never rendered raw, a new backend action must arrive here with words).
func summarizeAuditEntry(e audit.Entry, withPayments bool) (string, bool) {
	switch e.Action {
	case audit.ActOrderStatusChanged:
		oldS := auditStr(e.Changes["old_status"])
		newS := auditStr(e.Changes["new_status"])
		reason := auditStr(e.Changes["reason"])
		if oldS == "" {
			switch reason {
			case "quote_converted":
				return "created the order from an accepted quote", true
			case "reorder_draft_created":
				return "started this order as a reorder draft", true
			}
			return fmt.Sprintf("created the order (%s)", humanStatus(newS)), true
		}
		if newS == "CANCELLED" && reason != "" {
			// The cancel reason is required human text, the one reason worth surfacing verbatim.
			return fmt.Sprintf("cancelled the order, %s", reason), true
		}
		return fmt.Sprintf("moved the order from %s to %s", humanStatus(oldS), humanStatus(newS)), true

	case audit.ActOrderChanged:
		field := auditStr(e.Changes["field"])
		lineNo := auditInt(e.Changes["line_no"])
		before := auditScalar(e.Changes["before"])
		after := auditScalar(e.Changes["after"])
		switch field {
		case "requested_date":
			switch {
			case after == "":
				return "cleared the requested delivery date", true
			case before == "":
				return fmt.Sprintf("set the requested delivery date to %s", after), true
			default:
				return fmt.Sprintf("changed the requested delivery date from %s to %s", before, after), true
			}
		case "delivery_instructions":
			return "updated the delivery instructions", true
		case "qty_entered":
			if lineNo > 0 {
				return fmt.Sprintf("changed the quantity on line %d from %s to %s", lineNo, before, after), true
			}
			return fmt.Sprintf("changed a quantity from %s to %s", before, after), true
		case "line_added":
			if lineNo > 0 {
				return fmt.Sprintf("added line %d to the order", lineNo), true
			}
			return "added a line to the order", true
		case "line_removed":
			if lineNo > 0 {
				return fmt.Sprintf("removed line %d from the order", lineNo), true
			}
			return "removed a line from the order", true
		case "":
			// Field-less ORDER_CHANGED rows are purpose-shaped.
			switch auditStr(e.Changes["purpose"]) {
			case "ORDER_CHANGE_ORDER":
				if no := auditStr(e.Changes["change_no"]); no != "" {
					return fmt.Sprintf("recorded change order %s", no), true
				}
				return "recorded a change order", true
			}
			// PORTAL_REORDER provenance is folded away: its paired creation row (above) already
			// says "started this order as a reorder draft".
			return "", false
		}
		// Consequence columns of one edit (qty_base, unit_price_cents, line/order totals): the
		// headline row above already tells the story, one human action, one row.
		return "", false

	case audit.ActSaleCompleted:
		if tender := auditStr(e.Changes["reason"]); tender != "" && withPayments {
			return fmt.Sprintf("completed a counter sale (%s)", humanCode(tender)), true
		}
		return "completed a counter sale", true
	}
	return "", false
}

// humanStatus renders a status code readably ("PARTIALLY_FULFILLED" → "Partially fulfilled") -
// the same shaping the status-timeline UI applies, done server-side here so the summary is one
// finished sentence.
func humanStatus(s string) string {
	if s == "" {
		return "-"
	}
	words := strings.Split(strings.ToLower(s), "_")
	words[0] = strings.ToUpper(words[0][:1]) + words[0][1:]
	return strings.Join(words, " ")
}

// humanCode lowercases a short vocabulary code for mid-sentence use ("ON_ACCOUNT" → "on account").
func humanCode(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), "_", " ")
}

// moneyStr renders integer cents for a summary sentence ("$1,234" stays plain: "$12.34" shape,
// negative-safe). Mirrors billing/credit_gate.go's rendering.
func moneyStr(cents int64) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	return fmt.Sprintf("%s$%d.%02d", sign, cents/100, cents%100)
}

// - HTTP (hangs off the SAME *Handler as every other order route) -

func (h *Handler) orderActivity(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	withPayments := false
	if h.trail != nil {
		held, err := h.trail.HoldsPaymentTrail(r.Context(), caller.PlatformUserID, caller.OrgID)
		if err != nil {
			h.writeActivityErr(w, err)
			return
		}
		withPayments = held
	}
	out, err := h.svc.OrderActivityFor(r.Context(), r.PathValue("id"), withPayments)
	if err != nil {
		h.writeActivityErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) addOrderNote(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var body struct {
		Channel        string `json:"channel"`
		Body           string `json:"body"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := decodeEditBody(w, r, &body); err != nil {
		h.writeActivityErr(w, err)
		return
	}
	n, err := h.svc.AddOrderNote(r.Context(), r.PathValue("id"), body.Channel, body.Body, body.IdempotencyKey)
	if err != nil {
		h.writeActivityErr(w, err)
		return
	}
	status := http.StatusCreated
	if n.Replayed {
		status = http.StatusOK // the note already existed under this key, nothing new was written
	}
	httpx.WriteJSON(w, status, n)
}

func (h *Handler) editOrderNote(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var body struct {
		Channel string `json:"channel"`
		Body    string `json:"body"`
	}
	if err := decodeEditBody(w, r, &body); err != nil {
		h.writeActivityErr(w, err)
		return
	}
	n, err := h.svc.EditOrderNote(r.Context(), r.PathValue("id"), r.PathValue("noteId"), body.Body, body.Channel)
	if err != nil {
		h.writeActivityErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, n)
}

// writeActivityErr maps the activity/notes errors, then falls back to the shared writeErr so the
// authority and not-found answers are byte-identical to the other order surfaces'.
func (h *Handler) writeActivityErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNoteNotFound):
		httpx.Error(w, http.StatusNotFound,
			"that note is not on this order, it may have been removed, or the link is stale; reopen the activity panel and try again")
	case errors.Is(err, ErrNotesNotConfigured):
		// A server wiring fault, deliberately NOT a generic 500, name the unwired seam.
		httpx.Error(w, http.StatusServiceUnavailable,
			"order notes are not configured on this server (the notes store is not wired), the note was not saved")
	default:
		h.writeErr(w, err)
	}
}

// ErrNoteNotFound reports a note id that is not on the given order (missing, or on another order -
// the two are deliberately indistinguishable).
var ErrNoteNotFound = errors.New("order: note not found")
