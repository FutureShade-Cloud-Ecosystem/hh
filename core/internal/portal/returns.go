package portal

// returns.go, A-06: the contractor-portal returns surface. A contractor REQUESTS a return on one of
// their orders (selected lines + qty + reason); nothing moves until a staff member approves it on the
// ERP side (internal/pos return-request queue → the existing POS return rail, ACCOUNT_CREDIT + restock).
// Every read/write here is tenant-scoped by the verified caller's CustomerID (the orders.go IDOR rule):
// another account's order, or a non-POS order, answers 404, never 403.

import (
	"context"
	"encoding/json"
	"net/http"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
)

// ReturnsService is the pos-backed seam the BFF composes (satisfied by *pos.Service). Kept as an
// interface so the feature tests fake it like every other domain seam.
type ReturnsService interface {
	ReturnEligibility(ctx context.Context, customerID, orderID string) (pos.ReturnLookup, error)
	CreateReturnRequest(ctx context.Context, in pos.CreateReturnRequestInput) (pos.ReturnRequest, error)
	ListCustomerReturnRequests(ctx context.Context, customerID string) ([]pos.ReturnRequest, error)
}

// WithReturns injects the portal-returns seam (the WithOrders builder convention).
func (h *Handler) WithReturns(s ReturnsService) *Handler { h.returnsSvc = s; return h }

// portalReturnRequest is the TENANT-SAFE projection of pos.ReturnRequest: it deliberately omits
// decided_by (a staff user UUID, internal attribution belongs to the staff queue, never the portal).
// decided_at / decision_reason stay: they are the customer's answer ("when and why was my request
// decided"); requested_by is the tenant's own portal user.
type portalReturnRequest struct {
	ID             string                  `json:"id"`
	RequestNo      string                  `json:"request_no"`
	OrderID        string                  `json:"order_id"`
	OrderNo        string                  `json:"order_no"`
	CustomerID     string                  `json:"customer_id"`
	CustomerName   string                  `json:"customer_name,omitempty"`
	BranchID       string                  `json:"branch_id"`
	Status         string                  `json:"status"`
	ReasonCode     string                  `json:"reason_code"`
	Note           string                  `json:"note"`
	RequestedBy    string                  `json:"requested_by,omitempty"`
	DecidedAt      string                  `json:"decided_at,omitempty"`
	DecisionReason string                  `json:"decision_reason,omitempty"`
	FeeCents       int64                   `json:"fee_cents"`
	ReturnID       string                  `json:"return_id,omitempty"`
	ReturnNo       string                  `json:"return_no,omitempty"`
	CreatedAt      string                  `json:"created_at"`
	Lines          []pos.ReturnRequestLine `json:"lines"`
}

// toPortalReturnRequest strips the staff attribution off one request row.
func toPortalReturnRequest(rr pos.ReturnRequest) portalReturnRequest {
	return portalReturnRequest{
		ID: rr.ID, RequestNo: rr.RequestNo, OrderID: rr.OrderID, OrderNo: rr.OrderNo,
		CustomerID: rr.CustomerID, CustomerName: rr.CustomerName, BranchID: rr.BranchID,
		Status: rr.Status, ReasonCode: rr.ReasonCode, Note: rr.Note, RequestedBy: rr.RequestedBy,
		DecidedAt: rr.DecidedAt, DecisionReason: rr.DecisionReason, FeeCents: rr.FeeCents,
		ReturnID: rr.ReturnID, ReturnNo: rr.ReturnNo, CreatedAt: rr.CreatedAt, Lines: rr.Lines,
	}
}

// registerReturnRoutes wires the A-06 portal surface: eligibility + "my requests" ride
// ViewOrdersDeliveries (they are order reads); submitting a request rides CreateOrders, it commits the
// account to a money-moving decision once staff approve, which is exactly the Buyer/Owner power.
func (h *Handler) registerReturnRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/tenant/orders/{id}/return-eligibility", g.RequireCapability(identity.CapViewOrdersDeliveries, h.tenant(h.returnEligibility)))
	r.Handle("POST /api/v1/tenant/orders/{id}/return-requests", g.RequireCapability(identity.CapCreateOrders, h.tenant(h.createReturnRequest)))
	r.Handle("GET /api/v1/tenant/return-requests", g.RequireCapability(identity.CapViewOrdersDeliveries, h.tenant(h.listReturnRequests)))
}

// returnEligibility returns the order's returnable lines (qty sold − prior returns, via the POS rail's
// LookupReturn netting), tenant-scoped.
func (h *Handler) returnEligibility(w http.ResponseWriter, r *http.Request) {
	if h.returnsSvc == nil {
		httpx.NotImplemented(w, "returns not implemented")
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)
	lk, err := h.returnsSvc.ReturnEligibility(ctx, caller.CustomerID, r.PathValue("id"))
	if err != nil {
		h.writeReturnsErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, lk)
}

// createReturnRequest records a PENDING return request, intent only; approval is a staff action.
func (h *Handler) createReturnRequest(w http.ResponseWriter, r *http.Request) {
	if h.returnsSvc == nil {
		httpx.NotImplemented(w, "returns not implemented")
		return
	}
	var req struct {
		ReasonCode string                       `json:"reason_code"`
		Note       string                       `json:"note"`
		Lines      []pos.ReturnRequestLineInput `json:"lines"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)
	rr, err := h.returnsSvc.CreateReturnRequest(ctx, pos.CreateReturnRequestInput{
		CustomerID:  caller.CustomerID, // tenant + attribution come from the verified token, never the body
		RequestedBy: caller.UserID,
		OrderID:     r.PathValue("id"),
		ReasonCode:  req.ReasonCode,
		Note:        req.Note,
		Lines:       req.Lines,
	})
	if err != nil {
		h.writeReturnsErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toPortalReturnRequest(rr))
}

// listReturnRequests returns the account's return requests (newest first), the "Returns" status list.
func (h *Handler) listReturnRequests(w http.ResponseWriter, r *http.Request) {
	if h.returnsSvc == nil {
		httpx.NotImplemented(w, "returns not implemented")
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)
	items, err := h.returnsSvc.ListCustomerReturnRequests(ctx, caller.CustomerID)
	if err != nil {
		h.writeReturnsErr(w, r, err)
		return
	}
	out := make([]portalReturnRequest, 0, len(items))
	for _, rr := range items {
		out = append(out, toPortalReturnRequest(rr))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// writeReturnsErr maps the pos module's client-error taxonomy (404 not-found, IDOR-safe, 4xx input
// refusals) through pos.ClientStatus; anything else falls to the shared writeErr (logged 500).
func (h *Handler) writeReturnsErr(w http.ResponseWriter, r *http.Request, err error) {
	if status, ok := pos.ClientStatus(err); ok {
		httpx.Error(w, status, err.Error())
		return
	}
	h.writeErr(w, r, err)
}
