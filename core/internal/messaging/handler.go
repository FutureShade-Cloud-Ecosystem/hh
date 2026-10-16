package messaging

import (
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// maxBodyBytes caps request bodies (M-4 parity with the other ERP surfaces).
const maxBodyBytes = 1 << 20 // 1 MiB

// Handler exposes the messaging REST surface. Every route is staff-gated (RequireStaff), this is
// an internal staff tool; contractors never reach it. The caller is resolved from the verified
// StaffContext the guard stamps, so channel-scoping authz is always against the trusted server-side
// identity, never a client-supplied id (FR-1, C-1).
type Handler struct {
	userLink identity.UserIDLinker
	svc      *Service
	hub      *SSEHub     // optional; nil => the /stream route answers 501 and clients short-poll
	tickets  *ticketAuth // optional; nil => /stream-ticket answers 501 and clients use the Bearer stream
	log      *slog.Logger
}

func NewHandler(svc *Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, log: log}
}

// WithHub attaches the SSE hub so GET /stream can serve the live event stream (FR-7).
func (h *Handler) WithHub(hub *SSEHub) *Handler { h.hub = hub; return h }

// WithStreamTickets enables the EventSource stream-ticket path (D-P1-1): POST /stream-ticket mints a
// short-lived, single-use, HMAC-signed, user-bound ticket that GET /stream?ticket= accepts in place of the
// Bearer header (which an EventSource cannot send). An empty secret leaves it disabled (the Bearer stream
// still works for non-browser clients; browsers stay on the poll fallback).
func (h *Handler) WithStreamTickets(secret string) *Handler {
	if secret != "" {
		h.tickets = newTicketAuth(secret)
	}
	return h
}

// RegisterRoutes self-registers the messaging routes. Most are staff-read/staff-write (requireStaff);
// the HR/audit export is admin-only (requireAdmin, the SAME gate the audit viewer uses), so the
// wiring passes both guards, matching the audit.RegisterRoutes(mux, requireAdmin, requireStaff)
// convention. There is otherwise no manager/admin split in P1 (channel-admin tooling is deferred).
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/messaging/channels", g.RequireStaff(h.staff(h.listChannels)))
	// The house's own founding and membership routes. Founding is the
	// admins' pen now (change 7, ruling A: the boards' New channel is for
	// admins only); DMs stay every role's, and the membership walls are the
	// module's existing ones: assertMember on every membership write, the
	// owner or matrix admin pen on the seats, the branch wall on a channel
	// that names a branch.
	r.Handle("POST /api/v1/messaging/channels", g.RequireStaff(h.staff(h.createChannel)))
	r.Handle("POST /api/v1/messaging/dms", g.RequireStaff(h.staff(h.createDM)))
	// Slice S1: the New message picker's roster, a read any staffer may make
	// (the picker feeds the DM route beside it), active staff of the caller's
	// org with the labels the picker shows and nothing else.
	r.Handle("GET /api/v1/messaging/people", g.RequireStaff(h.staff(h.listPeople)))
	// The group room (walk change 7, slice S2, rulings 3 and 4): any staffer
	// starts a named group with any set of staffers, and any member renames
	// it; the rename and the fresh seat write their system lines. The add
	// rides the existing member route, whose service wall gives the group its
	// any-member arm. Gated like the siblings: RequireStaff, house-messages
	// work, the caller stamped from the verified staff context.
	r.Handle("POST /api/v1/messaging/groups", g.RequireStaff(h.staff(h.createGroup)))
	r.Handle("PATCH /api/v1/messaging/channels/{id}", g.RequireStaff(h.staff(h.renameChannel)))
	r.Handle("POST /api/v1/messaging/channels/{id}/members", g.RequireStaff(h.staff(h.addChannelMember)))
	r.Handle("DELETE /api/v1/messaging/channels/{id}/members/{userID}", g.RequireStaff(h.staff(h.removeChannelMember)))
	r.Handle("GET /api/v1/messaging/channels/{id}/messages", g.RequireStaff(h.staff(h.listMessages)))
	r.Handle("POST /api/v1/messaging/channels/{id}/messages", g.RequireStaff(h.staff(h.postMessage)))
	r.Handle("GET /api/v1/messaging/channels/{id}/threads/{rootID}", g.RequireStaff(h.staff(h.listThread)))
	// Change 7 S4 (ruling 6): the author's own edit and delete inside the
	// edit window the module's policy row keeps. PATCH rewords the author's
	// own text message, DELETE tombstones it (the body dropped, deleted_at
	// stamped); the walls are the service's: the channel wall, the kind wall
	// (system messages are nobody's), the forced acknowledgement wall (a
	// notice is never editable once sent), the author wall, and the window.
	r.Handle("PATCH /api/v1/messaging/channels/{id}/messages/{msgID}", g.RequireStaff(h.staff(h.editMessage)))
	r.Handle("DELETE /api/v1/messaging/channels/{id}/messages/{msgID}", g.RequireStaff(h.staff(h.deleteMessage)))
	r.Handle("POST /api/v1/messaging/channels/{id}/read", g.RequireStaff(h.staff(h.markRead)))
	// Feature 1 (0071): forced-ack. POST an ack for one message (mirrors markRead: channel-scoped,
	// staff-gated); GET the caller's un-acked forced-ack messages for the kiosk to poll.
	r.Handle("POST /api/v1/messaging/channels/{id}/messages/{msgID}/ack", g.RequireStaff(h.staff(h.ackMessage)))
	r.Handle("GET /api/v1/messaging/unacked", g.RequireStaff(h.staff(h.unacked)))
	// Feature A (0069): per-user notification prefs for a channel. Staff-gated, mirroring markRead.
	r.Handle("GET /api/v1/messaging/channels/{id}/notify-prefs", g.RequireStaff(h.staff(h.getNotifyPrefs)))
	r.Handle("PUT /api/v1/messaging/channels/{id}/notify-prefs", g.RequireStaff(h.staff(h.putNotifyPrefs)))
	r.Handle("GET /api/v1/messaging/unread", g.RequireStaff(h.staff(h.unread)))
	// FR-7 real-time: Server-Sent Events over the existing Go API. Two auth paths on ONE route: an
	// EventSource passes ?ticket= (verified in-handler, it can't send a Bearer), everything else uses
	// the Bearer guard. POST /stream-ticket (Bearer'd) mints the short-lived, user-bound ticket.
	r.Handle("POST /api/v1/messaging/stream-ticket", g.RequireStaff(h.staff(h.mintStreamTicket)))
	// CARRY NOTE (guards swap, HH-3.2 B5): the pin assigned the guarded
	// handler to a variable and called it as a function inside the
	// ticket-or-Bearer switch; under the Guards constructor set the
	// guarded value is an http.Handler, so the switch serves through it.
	streamBearer := g.RequireStaff(h.staff(h.streamBearer))
	r.Handle("GET /api/v1/messaging/stream", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ticket") != "" {
			h.streamTicket(w, r)
			return
		}
		streamBearer.ServeHTTP(w, r)
	}))
	// Feature B (0070): admin-only HR/audit export. Gated by the SAME admin guard as the audit viewer.
	r.Handle("GET /api/v1/messaging/export", g.RequireAdmin(h.staff(h.export)))
	// DIB-449: the event-class config. READ is staff, "why don't I get a message for every payment?"
	// is a question any staffer may ask of the system, and a config they can see is a config they can
	// trust. WRITE is admin, behind the SAME guard as the export, because it is a tech-admin knob.
	r.Handle("GET /api/v1/messaging/event-classes", g.RequireStaff(h.staff(h.listEventClasses)))
	r.Handle("PUT /api/v1/messaging/event-classes/{topic}", g.RequireAdmin(h.staff(h.putEventClass)))
	// DIB-462: the caller's OWN targeted-payment rules. Staff-gated and self-scoped, the user id comes
	// from the verified StaffContext, never from the path or the body, so there is no route through
	// which one staffer can read or change another's notification choices. This is a preference in the
	// same class as the per-channel mode above, not an admin knob like the event classes.
	r.Handle("GET /api/v1/messaging/payment-rules", g.RequireStaff(h.staff(h.getPaymentRules)))
	r.Handle("PUT /api/v1/messaging/payment-rules/{rule}", g.RequireStaff(h.staff(h.putPaymentRule)))
}

// caller builds the messaging Caller from the identity StaffContext stamped by RequireStaff. ok is
// false only if the guard somehow admitted a request with no caller (defensive, should not happen).
func (h *Handler) caller(r *http.Request) (Caller, bool) {
	sc, ok := identity.StaffContextFromContext(r.Context())
	if !ok || sc.UserID == "" {
		return Caller{}, false
	}
	branches := make(map[string]bool, len(sc.Branches))
	for b := range sc.Branches {
		branches[b] = true
	}
	return Caller{
		UserID:   sc.UserID,
		RoleCode: sc.RoleCode,
		Scope:    string(sc.Scope),
		Branches: branches,
	}, true
}

// pathUUID reads a {name} path param and validates it is a well-formed UUID. A malformed value is a
// CLIENT fault that must NEVER reach the repository: every messaging read/write binds these params
// straight into a Postgres `uuid` column (IsMember/channelExists on channel_id, ListThread/
// InsertMessage on the thread root), so a non-UUID string fails the implicit `text -> uuid` cast and
// the error wraps to the catch-all HTTP 500 (e.g. GET /channels/not-a-uuid/messages was 500). This
// mirrors the decodeCursor/$4::uuid guard the team already added for the cursor id-half: validate at
// the boundary and stop the bad input before it hits the DB. A parse failure maps to 404 (not-found)
// , a malformed id names no channel/message, and 404 leaks no existence (a valid-but-absent id is
// also 404 via channelExists, so the two are indistinguishable to the caller). ok is false when the
// response has already been written.
func (h *Handler) pathUUID(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	v := r.PathValue(name)
	if _, err := uuid.Parse(v); err != nil {
		httpx.Error(w, http.StatusNotFound, "not found")
		return "", false
	}
	return v, true
}

func (h *Handler) listChannels(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	chans, err := h.svc.ListMyChannels(r.Context(), c)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"channels": chans})
}

// createChannelRequest is the channel-create body. Slug is optional (derived
// from the name when empty, so the same founding twice converges); branch_id
// optionally scopes a TEAM channel to one yard.
type createChannelRequest struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Topic    string `json:"topic"`
	Slug     string `json:"slug"`
	BranchID string `json:"branch_id"`
}

// createChannel founds a TEAM or TOPIC channel, 201 the first time and 200 on
// the idempotent replay (the found channel), so a retried founding is not a
// duplicate room.
func (h *Handler) createChannel(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req createChannelRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.BranchID != "" {
		if _, err := uuid.Parse(req.BranchID); err != nil {
			httpx.Error(w, http.StatusBadRequest, "branch_id must be a valid UUID")
			return
		}
	}
	ch, created, err := h.svc.CreateChannel(r.Context(), c, CreateChannelInput{
		Kind: req.Kind, Name: req.Name, Topic: req.Topic, Slug: req.Slug, BranchID: req.BranchID,
	})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if created {
		httpx.WriteJSON(w, http.StatusCreated, ch)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ch)
}

// createDM opens (or finds) the direct message channel between the caller and
// the named staffer: 201 the first time, 200 when the pair's room already
// exists (whoever opened it).
func (h *Handler) createDM(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req struct {
		UserID string `json:"user_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.UserID != "" {
		if _, err := uuid.Parse(req.UserID); err != nil {
			httpx.Error(w, http.StatusBadRequest, "user_id must be a valid UUID")
			return
		}
	}
	ch, created, err := h.svc.CreateDM(r.Context(), c, req.UserID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if created {
		httpx.WriteJSON(w, http.StatusCreated, ch)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ch)
}

// listPeople answers the New message picker's roster page. The org comes
// from the guard's resolved scope (never from the query string), the window
// and the limit from the shared cursor family, q from the query string; a
// malformed cursor is the client fault the shared family already maps to 400.
func (h *Handler) listPeople(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if caller.OrgID == "" {
		// The guards resolve the org before any handler runs; an empty one
		// means the request arrived unblessed, fail closed.
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	p, perr := httpx.ParseCursorPage(r)
	if perr != nil {
		httpx.Error(w, http.StatusBadRequest, "cursor is malformed")
		return
	}
	items, next, err := h.svc.GetPeople(r.Context(), c, caller.OrgID, r.URL.Query().Get("q"), p.Cursor, p.Limit)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteCursorPage(w, items, next, p.Limit)
}

// createGroup starts a group room (walk change 7, slice S2, rulings 3 and 4):
// the caller's own name and set are optional, the caller is seated as the
// group's owner, and every named staffer as an explicit member.
func (h *Handler) createGroup(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req struct {
		Name      string   `json:"name"`
		MemberIDs []string `json:"member_ids"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	for _, id := range req.MemberIDs {
		if _, err := uuid.Parse(id); err != nil {
			httpx.Error(w, http.StatusBadRequest, "member_ids must be valid UUIDs")
			return
		}
	}
	ch, err := h.svc.CreateGroup(r.Context(), c, GroupCreateInput{Name: req.Name, MemberIDs: req.MemberIDs})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, ch)
}

// renameChannel renames a group room: any member may, and the rename writes
// its system line. A room of any other kind refuses with 409.
func (h *Handler) renameChannel(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	id, ok := h.pathUUID(w, r, "id")
	if !ok {
		return
	}
	ch, err := h.svc.RenameGroup(r.Context(), c, id, req.Name)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ch)
}

// addChannelMember seats one staffer in a channel the caller belongs to. The
// body names the member; the row is EXPLICIT and idempotent.
func (h *Handler) addChannelMember(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	channelID, ok := h.pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		UserID string `json:"user_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if _, err := uuid.Parse(req.UserID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "user_id must be a valid UUID")
		return
	}
	m, err := h.svc.AddMember(r.Context(), c, channelID, req.UserID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, m)
}

// removeChannelMember drops one EXPLICIT member row; a DERIVED row is a 409
// conflict with the role and branch model that owns it.
func (h *Handler) removeChannelMember(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	channelID, ok := h.pathUUID(w, r, "id")
	if !ok {
		return
	}
	userID, ok := h.pathUUID(w, r, "userID")
	if !ok {
		return
	}
	if err := h.svc.RemoveMember(r.Context(), c, channelID, userID); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) listMessages(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	channelID, ok := h.pathUUID(w, r, "id")
	if !ok {
		return
	}
	cursor := r.URL.Query().Get("cursor")
	limit := parseLimit(r.URL.Query().Get("limit"))
	page, err := h.svc.GetMessages(r.Context(), c, channelID, cursor, limit)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

type postMessageRequest struct {
	Body         string `json:"body"`
	ThreadRootID string `json:"thread_root_id"` // optional, set to reply in a thread
}

func (h *Handler) postMessage(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	channelID, ok := h.pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req postMessageRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	// A malformed (non-UUID) thread_root_id must never reach the InsertMessage uuid cast, mirror
	// pathUUID's boundary rationale and map it to 404, indistinguishable from an absent/wrong-channel
	// root (which repository.go also maps to ErrNotFound → 404), so no existence leaks.
	if req.ThreadRootID != "" {
		if _, err := uuid.Parse(req.ThreadRootID); err != nil {
			httpx.Error(w, http.StatusNotFound, "not found")
			return
		}
	}
	m, err := h.svc.PostMessage(r.Context(), c, channelID, req.Body, req.ThreadRootID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, m)
}

func (h *Handler) listThread(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	channelID, ok := h.pathUUID(w, r, "id")
	if !ok {
		return
	}
	rootID, ok := h.pathUUID(w, r, "rootID")
	if !ok {
		return
	}
	replies, err := h.svc.GetThread(r.Context(), c, channelID, rootID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"messages": replies})
}

// editMessageRequest is the PATCH body: the message's new wording.
type editMessageRequest struct {
	Body string `json:"body"`
}

// editMessage applies the caller's new wording to their own message inside
// the edit window (change 7 S4). Mirrors postMessage's shape: caller, the
// path UUIDs validated at the boundary, the body decoded and bounded, and
// the service's walls answer in their own words.
func (h *Handler) editMessage(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	channelID, ok := h.pathUUID(w, r, "id")
	if !ok {
		return
	}
	messageID, ok := h.pathUUID(w, r, "msgID")
	if !ok {
		return
	}
	var req editMessageRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	m, err := h.svc.EditMessage(r.Context(), c, channelID, messageID, req.Body)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, m)
}

// deleteMessage tombstones the caller's own message inside the edit window
// (change 7 S4): the body is dropped in the row, deleted_at is stamped, and
// the answer is the tombstone the client renders in place.
func (h *Handler) deleteMessage(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	channelID, ok := h.pathUUID(w, r, "id")
	if !ok {
		return
	}
	messageID, ok := h.pathUUID(w, r, "msgID")
	if !ok {
		return
	}
	m, err := h.svc.DeleteMessage(r.Context(), c, channelID, messageID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, m)
}

func (h *Handler) markRead(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	channelID, ok := h.pathUUID(w, r, "id")
	if !ok {
		return
	}
	if err := h.svc.MarkRead(r.Context(), c, channelID); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ackRequest is the ack body: an OPTIONAL method (default TAP). An empty/absent body is valid, the
// kiosk "I got it" button posts {"method":"TAP"}, but a body-less POST still acks as TAP.
type ackRequest struct {
	Method string `json:"method"`
}

// ackMessage records the caller's acknowledgement of a forced-ack message. It mirrors markRead's
// shape, caller → pathUUID({id},{msgID}) → membership-gated service, plus a tolerant body decode so
// an empty body defaults the method to TAP. The service validates the method (ErrBadAckMethod → 400)
// and the repository binds the write to (msgID, channel) so a foreign channel's message can't be acked.
func (h *Handler) ackMessage(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	channelID, ok := h.pathUUID(w, r, "id")
	if !ok {
		return
	}
	messageID, ok := h.pathUUID(w, r, "msgID")
	if !ok {
		return
	}
	var req ackRequest
	// Tolerant decode: an empty body (EOF) is fine and means "default method"; only malformed JSON is a
	// 400. Bounded by the same MaxBytesReader cap as the other write routes.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	ack, err := h.svc.AckMessage(r.Context(), c, channelID, messageID, req.Method)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ack)
}

// unacked returns the caller's un-acknowledged forced-ack messages (newest-first) for the kiosk poll.
// Membership is enforced inside the service read (only the caller's channels are considered).
func (h *Handler) unacked(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	msgs, err := h.svc.UnackedRequiresAck(r.Context(), c)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"messages": msgs})
}

// getNotifyPrefs returns the caller's notification prefs for a channel (defaults if unset). Mirrors
// markRead: caller → pathUUID → service (membership-gated) → JSON.
func (h *Handler) getNotifyPrefs(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	channelID, ok := h.pathUUID(w, r, "id")
	if !ok {
		return
	}
	prefs, err := h.svc.GetNotifyPrefs(r.Context(), c, channelID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, prefs)
}

// putNotifyPrefs upserts the caller's notification prefs for a channel. The body is a PrefsPatch, each
// field optional, an omitted field keeps the current value (merge-then-upsert in the service).
func (h *Handler) putNotifyPrefs(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	channelID, ok := h.pathUUID(w, r, "id")
	if !ok {
		return
	}
	var patch PrefsPatch
	if !decodeJSON(w, r, &patch) {
		return
	}
	prefs, err := h.svc.SetNotifyPrefs(r.Context(), c, channelID, patch)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, prefs)
}

// getPaymentRules returns the caller's targeted-payment rules, ALWAYS all three, with the ones they
// have never touched present and false. A partial map would force the surface to invent the missing
// half, and "never decided" and "off" mean the same thing to the system, so they must look the same to
// the reader.
func (h *Handler) getPaymentRules(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	prefs, err := h.svc.GetPaymentRules(r.Context(), c)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, prefs)
}

// putPaymentRule sets ONE of the caller's own toggles. The rule id is a path segment validated against
// the closed set in the service (400 on anything else); the body carries only the boolean.
func (h *Handler) putPaymentRule(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	prefs, err := h.svc.SetPaymentRule(r.Context(), c, r.PathValue("rule"), body.Enabled)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, prefs)
}

func (h *Handler) unread(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	counts, err := h.svc.UnreadSummary(r.Context(), c)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, counts)
}

// mintStreamTicket issues a short-lived, single-use, user-bound stream ticket for the Bearer'd caller so a
// browser EventSource (which cannot send a Bearer) can open GET /stream?ticket=. 501 when tickets are not
// enabled (the client stays on the Bearer stream / poll).
func (h *Handler) mintStreamTicket(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if h.tickets == nil {
		httpx.NotImplemented(w, "stream tickets not enabled; use the Bearer stream")
		return
	}
	tok, err := h.tickets.Mint(c.UserID)
	if err != nil {
		h.log.Error("messaging: mint stream ticket", "err", err)
		httpx.Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ticket": tok, "expires_in_seconds": int(streamTicketTTL.Seconds())})
}

// streamBearer serves the SSE feed for the Bearer-authenticated caller (the guard stamped the context).
// When no hub is wired it answers 501 so the client falls back to short-poll of /unread.
func (h *Handler) streamBearer(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if h.hub == nil {
		httpx.NotImplemented(w, "sse stream not enabled; poll /api/v1/messaging/unread")
		return
	}
	h.hub.Serve(w, r, c.UserID, h.svc)
}

// streamTicket serves the SSE feed for an EventSource that presented ?ticket=. The ticket is verified
// (HMAC + expiry + single-use) into the user id it is BOUND to, and the stream serves exactly that user's
// membership view, a ticket minted by user A never yields user B's channels. A bad/expired/reused/forged
// ticket is 401. 501 when the hub or ticket auth is not wired.
func (h *Handler) streamTicket(w http.ResponseWriter, r *http.Request) {
	if h.hub == nil {
		httpx.NotImplemented(w, "sse stream not enabled; poll /api/v1/messaging/unread")
		return
	}
	if h.tickets == nil {
		httpx.Error(w, http.StatusUnauthorized, "stream tickets not enabled")
		return
	}
	userID, err := h.tickets.Verify(r.URL.Query().Get("ticket"))
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "invalid or expired stream ticket")
		return
	}
	h.hub.Serve(w, r, userID, h.svc)
}

// export is the admin-only HR/audit export (Feature B). It mirrors the audit admin viewer: the ADMIN
// gate is applied at the route; the filter params that flow into ::uuid / ::timestamptz casts are
// validated at the boundary (a malformed value would blow the cast into a generic 500 for what is
// really bad client input), then the service returns admin-masked rows (projectForExport).
func (h *Handler) export(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	channelID := q.Get("channel_id")
	if channelID != "" {
		if _, err := uuid.Parse(channelID); err != nil {
			httpx.Error(w, http.StatusBadRequest, "channel_id must be a valid UUID")
			return
		}
	}
	userID := q.Get("user_id")
	if userID != "" {
		if _, err := uuid.Parse(userID); err != nil {
			httpx.Error(w, http.StatusBadRequest, "user_id must be a valid UUID")
			return
		}
	}
	since := q.Get("since")
	if since != "" {
		if _, err := time.Parse(time.RFC3339, since); err != nil {
			httpx.Error(w, http.StatusBadRequest, "since must be an RFC3339 timestamp")
			return
		}
	}
	until := q.Get("until")
	if until != "" {
		if _, err := time.Parse(time.RFC3339, until); err != nil {
			httpx.Error(w, http.StatusBadRequest, "until must be an RFC3339 timestamp")
			return
		}
	}
	f := ExportFilter{
		ChannelID: channelID,
		UserID:    userID,
		Since:     since,
		Until:     until,
		Limit:     parseLimit(q.Get("limit")),
	}
	rows, err := h.svc.ExportMessages(r.Context(), f)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if rows == nil {
		rows = []ExportMessage{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"messages": rows})
}

// - DIB-449: the event-class config surface -

// listEventClasses returns the classification config with each row's EFFECTIVE class already applied,
// so the surface shows what the system will actually do rather than what the table happens to hold.
func (h *Handler) listEventClasses(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	rows, err := h.svc.ListEventClasses(r.Context())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if rows == nil {
		rows = []EventClass{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"classes": rows})
}

// putEventClass reclassifies one topic (admin-gated at the route). The service refuses a LOCKED row
// with a 409 that names the rule, mirroring DIB-269's un-mutable refusal, an admin who believes they
// turned a critical alert into a rollup, and did not, is the failure this endpoint exists to prevent.
func (h *Handler) putEventClass(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "no caller")
		return
	}
	topic := r.PathValue("topic")
	if topic == "" {
		httpx.Error(w, http.StatusNotFound, "not found")
		return
	}
	var req struct {
		Class string `json:"class"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	out, err := h.svc.SetEventClass(r.Context(), c.UserID, topic, req.Class)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// - helpers -

func parseLimit(v string) int {
	if v == "" {
		return 0 // service applies default
	}
	n := 0
	for _, ch := range v {
		if ch < '0' || ch > '9' {
			return 0
		}
		n = n*10 + int(ch-'0')
		if n > 1000 {
			break
		}
	}
	return n
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(dst); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	return true
}

// writeErr maps service errors to HTTP without leaking internals.
func (h *Handler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not found")
	case errors.Is(err, ErrNotMember):
		httpx.Error(w, http.StatusForbidden, "not a channel member")
	case errors.Is(err, ErrEmptyBody):
		httpx.Error(w, http.StatusBadRequest, "message body is empty")
	case errors.Is(err, ErrBadCursor):
		httpx.Error(w, http.StatusBadRequest, "malformed pagination cursor")
	case errors.Is(err, ErrBadAckMethod):
		httpx.Error(w, http.StatusBadRequest, "ack method must be one of TAP, QUICK_REPLY, VOICE")
	// DIB-269: refusing to mute an escalation channel is a CONFLICT with the channel's class, not a
	// permission failure, the caller may edit their prefs, just not this one on this channel. 409 also
	// lets the UI show the honest message ("this channel cannot be muted") instead of "forbidden".
	case errors.Is(err, ErrChannelUnmutable):
		httpx.Error(w, http.StatusConflict, "escalation channels cannot be muted")
	// DIB-449: same shape, same reason. Reclassifying a CRITICAL event is a conflict with what the
	// event IS, not a permission failure, the admin owns this config, just not this row of it.
	case errors.Is(err, ErrEventClassLocked):
		httpx.Error(w, http.StatusConflict, "critical alerts cannot be reclassified")
	case errors.Is(err, ErrBadEventClass):
		httpx.Error(w, http.StatusBadRequest, "class must be one of ROUTINE, NOTABLE, CRITICAL")
	// DIB-462: an unknown rule id is a client fault caught at the boundary, so it never reaches
	// Postgres as a CHECK violation → 500.
	case errors.Is(err, ErrBadPaymentRule):
		httpx.Error(w, http.StatusBadRequest, "rule must be one of ONLINE, CROSS_BRANCH, OWN_ORDER")
	case errors.Is(err, ErrBadMode):
		httpx.Error(w, http.StatusBadRequest, "mode must be one of ALL, MENTIONS, MUTED")
	// The founding and membership refusals: the branch wall is the caller's
	// reach (403), a bad kind/name/target is a client fault (400), an inactive
	// staffer can be named but not seated (403), and a DERIVED member row is
	// the role and branch model's, a conflict (409) rather than a permission
	// failure.
	case errors.Is(err, ErrBadChannelKind):
		httpx.Error(w, http.StatusBadRequest, "kind must be one of TEAM, TOPIC")
	case errors.Is(err, ErrChannelNameRequired):
		httpx.Error(w, http.StatusBadRequest, "channel name is required")
	case errors.Is(err, ErrReservedSlug):
		httpx.Error(w, http.StatusBadRequest, "that slug prefix is reserved for system channels")
	case errors.Is(err, ErrChannelSlugTaken):
		httpx.Error(w, http.StatusConflict, "a channel with that name or slug already exists")
	case errors.Is(err, ErrMembershipFixed):
		httpx.Error(w, http.StatusConflict, "this channel's membership is fixed (a direct message's two parties, an approvals channel's owner)")
	case errors.Is(err, ErrNotChannelOwner):
		httpx.Error(w, http.StatusForbidden, "only the channel's owner or a house messages admin may manage members")
	case errors.Is(err, ErrTargetNotInBranch):
		httpx.Error(w, http.StatusForbidden, "that staff member does not reach this branch")
	case errors.Is(err, ErrTargetNotInRoom):
		httpx.Error(w, http.StatusForbidden, "that staff member's role may not be seated in this room")
	case errors.Is(err, ErrLastOwner):
		httpx.Error(w, http.StatusConflict, "a channel must keep its owner")
	// The founding gate (change 7, ruling A): founding is the admins' pen,
	// a permission answer, never a validation one.
	case errors.Is(err, ErrNotChannelAdmin):
		httpx.Error(w, http.StatusForbidden, "only a house messages admin may found a channel")
	// Change 7 S4: the edit and delete refusals, each in its own words so
	// the surface can say what happened: the pen is the author's own (403),
	// a system message or a forced ack notice is what it is (409), a
	// tombstone is not a revival (409), and the window has closed (409).
	case errors.Is(err, ErrNotMessageAuthor):
		httpx.Error(w, http.StatusForbidden, "only the message's author may edit or delete it")
	case errors.Is(err, ErrMessageImmutable):
		httpx.Error(w, http.StatusConflict, "system messages cannot be edited or deleted")
	case errors.Is(err, ErrMessageDeleted):
		httpx.Error(w, http.StatusConflict, "a deleted message cannot be edited")
	case errors.Is(err, ErrEditWindowClosed):
		httpx.Error(w, http.StatusConflict, "the edit window has closed")
	case errors.Is(err, ErrNotGroupRoom):
		httpx.Error(w, http.StatusConflict, "only a group room renames through this route")
	case errors.Is(err, ErrNotInBranch):
		httpx.Error(w, http.StatusForbidden, "caller does not reach this branch")
	case errors.Is(err, ErrBadDMTarget):
		httpx.Error(w, http.StatusBadRequest, "a direct message needs another staff member")
	case errors.Is(err, ErrUserNotActive):
		httpx.Error(w, http.StatusForbidden, "that staff member is not active")
	case errors.Is(err, ErrDerivedMember):
		httpx.Error(w, http.StatusConflict, "derived membership is managed by the role and branch model")
	case errors.Is(err, ErrAlreadyClaimed):
		httpx.Error(w, http.StatusConflict, "conversation already claimed")
	case errors.Is(err, ErrNoCustomer):
		httpx.Error(w, http.StatusBadRequest, "customer_id is required")
	case errors.Is(err, ErrBadAssignSource):
		httpx.Error(w, http.StatusBadRequest, "source must be one of MANUAL, CLAIM, IMPORT")
	case errors.Is(err, errNoDB):
		httpx.Error(w, http.StatusServiceUnavailable, "database unavailable")
	default:
		h.log.Error("messaging: internal error", "method", r.Method, "path", r.URL.Path, "err", err)
		httpx.Error(w, http.StatusInternalServerError, "internal error")
	}
}
