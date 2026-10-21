package messaging

import (
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/customerwall"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// - DIB-273/DIB-333 · DIB-271 · DIB-275 REST surface -
//
// RegisterConversationRoutes is a SECOND self-registration entry point rather than an edit to
// RegisterRoutes, so the shipped route table stays byte-identical and this wave's surface is
// reviewable as one block. main.go calls both.
//
// The admin/staff split:
//
//	rep assignment WRITES, requireAdmin. Who owns which customer is a book-of-business decision
//	  (commission follows it); a rep must not be able to hand themselves an account.
//	rep assignment READS , requireStaff. A rep needs to see their own book, and the routing rule is
//	  not a secret.
//	house queue + claim  , requireStaff, additionally gated on CHANNEL MEMBERSHIP in the service.
//	  Claiming is the one write a rep makes for themselves, and it is legitimate: taking ownership of
//	  an unclaimed conversation is the queue working as designed.
func (h *Handler) RegisterConversationRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/messaging/rep-assignments", g.RequireStaff(h.staff(h.listRepAssignments)))
	r.Handle("POST /api/v1/messaging/rep-assignments", g.RequireAdmin(h.staff(h.assignRep)))
	r.Handle("DELETE /api/v1/messaging/rep-assignments", g.RequireAdmin(h.staff(h.unassignRep)))
	// The routing preview: "where would a message from this customer go?", read-only, no write, so the
	// admin UI can show the consequence of an assignment before anyone sends anything.
	r.Handle("GET /api/v1/messaging/rep-route", g.RequireStaff(h.staff(h.previewRoute)))
	// The inbound ingress: one customer message in, one routed post out. Staff-gated because the
	// bridges that call it (portal / SMS relays) run server-side with a service identity.
	r.Handle("POST /api/v1/messaging/inbound/customer", g.RequireStaff(h.staff(h.inboundCustomer)))
	// DIB-271: the house queue for one channel, and the claim.
	r.Handle("GET /api/v1/messaging/channels/{id}/house-queue", g.RequireStaff(h.staff(h.houseQueue)))
	r.Handle("POST /api/v1/messaging/house-conversations/{convID}/claim", g.RequireStaff(h.staff(h.claimConversation)))
	// DIB-275: the ack arithmetic for one forced-ack message.
	r.Handle("GET /api/v1/messaging/channels/{id}/messages/{msgID}/ack-progress", g.RequireStaff(h.staff(h.ackProgress)))
}

// queryUUID reads an OPTIONAL uuid query param, 400-ing a malformed one at the boundary (the same
// doctrine pathUUID applies to path params: never let a junk id reach a Postgres uuid cast). An empty
// value is fine and means "no filter". ok is false when the response has already been written.
func queryUUID(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return "", true
	}
	if _, err := uuid.Parse(v); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid "+name)
		return "", false
	}
	return v, true
}

func (h *Handler) listRepAssignments(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if _, ok := h.caller(r); !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	repID, ok := queryUUID(w, r, "rep_user_id")
	if !ok {
		return
	}
	custID, ok := queryUUID(w, r, "customer_id")
	if !ok {
		return
	}
	if !customerwall.Fence(w, r, h.reach, custID) {
		return
	}
	rows, err := h.svc.ListRepAssignments(r.Context(), repID, custID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"assignments": rows})
}

// assignRequest is the POST body. OrderID empty = the customer-level assignment (the routable
// default); OrderID set = the per-order override (split books).
type assignRequest struct {
	CustomerID string `json:"customer_id"`
	OrderID    string `json:"order_id"`
	RepUserID  string `json:"rep_user_id"`
	Source     string `json:"source"`
	Note       string `json:"note"`
}

func (h *Handler) assignRep(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req assignRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	// Validate the three id-shaped fields at the boundary so a junk id is a 400, never a 500 from a
	// failed text->uuid cast (mirrors pathUUID).
	for _, f := range []struct{ name, val string }{
		{"customer_id", req.CustomerID}, {"rep_user_id", req.RepUserID},
	} {
		if _, err := uuid.Parse(f.val); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid "+f.name)
			return
		}
	}
	if req.OrderID != "" {
		if _, err := uuid.Parse(req.OrderID); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid order_id")
			return
		}
	}
	if !customerwall.Fence(w, r, h.reach, req.CustomerID) {
		return
	}
	a, err := h.svc.AssignRep(r.Context(), c, req.CustomerID, req.OrderID, req.RepUserID, req.Source, req.Note)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, a)
}

// unassignRep removes an assignment. DELETE carries its target in the query string (customer_id +
// optional order_id) rather than a body, matching the HTTP convention the rest of the ERP uses for
// deletes with a composite key.
func (h *Handler) unassignRep(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if _, ok := h.caller(r); !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	custID, ok := queryUUID(w, r, "customer_id")
	if !ok {
		return
	}
	if custID == "" {
		httpx.Error(w, http.StatusBadRequest, "customer_id is required")
		return
	}
	orderID, ok := queryUUID(w, r, "order_id")
	if !ok {
		return
	}
	if !customerwall.Fence(w, r, h.reach, custID) {
		return
	}
	if err := h.svc.UnassignRep(r.Context(), custID, orderID); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"unassigned": true, "customer_id": custID, "order_id": orderID})
}

// previewRoute answers "where would a message from this customer go right now?" without sending
// anything, the admin UI's before/after for an assignment change.
func (h *Handler) previewRoute(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if _, ok := h.caller(r); !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	custID, ok := queryUUID(w, r, "customer_id")
	if !ok {
		return
	}
	if custID == "" {
		httpx.Error(w, http.StatusBadRequest, "customer_id is required")
		return
	}
	orderID, ok := queryUUID(w, r, "order_id")
	if !ok {
		return
	}
	if !customerwall.Fence(w, r, h.reach, custID) {
		return
	}
	a, level, err := h.svc.ResolveRep(r.Context(), custID, orderID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	kind := RouteHouse
	if level != LevelNone {
		kind = RouteRepDM
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"level": level, "kind": kind, "rep_user_id": a.RepUserID, "rep_name": a.RepName,
	})
}

func (h *Handler) inboundCustomer(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if _, ok := h.caller(r); !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var in CustomerInbound
	if !decodeJSON(w, r, &in) {
		return
	}
	if _, err := uuid.Parse(in.CustomerID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid customer_id")
		return
	}
	if in.OrderID != "" {
		if _, err := uuid.Parse(in.OrderID); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid order_id")
			return
		}
	}
	if in.BranchID != "" {
		if _, err := uuid.Parse(in.BranchID); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid branch_id")
			return
		}
	}
	if !customerwall.Fence(w, r, h.reach, in.CustomerID) {
		return
	}
	res, err := h.svc.RouteCustomerMessage(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) houseQueue(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	channelID, ok := h.pathUUID(w, r, "id")
	if !ok {
		return
	}
	openOnly := r.URL.Query().Get("open") == "1"
	rows, err := h.svc.ListHouseQueue(r.Context(), c, channelID, openOnly)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"conversations": rows})
}

func (h *Handler) claimConversation(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	convID, ok := h.pathUUID(w, r, "convID")
	if !ok {
		return
	}
	conv, err := h.svc.ClaimConversation(r.Context(), c, convID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, conv)
}

func (h *Handler) ackProgress(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
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
	p, err := h.svc.AckProgress(r.Context(), c, channelID, messageID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}
