package pos

// return_requests.go, A-06: contractor-portal customer returns as a REQUEST + STAFF-APPROVAL flow
// (commander-ruled 2026-07-13). The portal BFF submits/reads requests through the service seam here
// (tenant-scoped by customer_id, an order outside the caller's account reads as NOT FOUND, never 403);
// staff decide them on the /api/v1/pos/return-requests surface. A PENDING request moves NO money and NO
// stock; approval executes the EXISTING W7 return rail (Return) with RefundMethod=ACCOUNT_CREDIT +
// Restock=true and the pos_policy restocking fee, attributing the staff approver; rejection records only
// the reason. This file deliberately re-uses the rail, no money/stock logic is duplicated here.

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/jackc/pgx/v5"
)

// -- domain vocabulary --------------------------------------------------------

// Return-request reason enum (return_requests.reason_code CHECK). OTHER requires the free-text note.
const (
	ReasonDamaged        = "DAMAGED"
	ReasonDefect         = "DEFECT"
	ReasonWrongItem      = "WRONG_ITEM"
	ReasonNoLongerNeeded = "NO_LONGER_NEEDED"
	ReasonOther          = "OTHER"
)

// Return-request statuses (return_requests.status CHECK).
const (
	RequestPending  = "PENDING"
	RequestApproved = "APPROVED"
	RequestRejected = "REJECTED"
)

// maxRequestNoteLen bounds the free-text note/rejection reason (the portal delivery-instructions cap).
const maxRequestNoteLen = 2000

func validReasonCode(code string) bool {
	switch code {
	case ReasonDamaged, ReasonDefect, ReasonWrongItem, ReasonNoLongerNeeded, ReasonOther:
		return true
	default:
		return false
	}
}

// ReturnRequestLineInput is one requested line (a fraction or all of one original order line).
type ReturnRequestLineInput struct {
	OrderLineID string  `json:"order_line_id"`
	QtyBase     float64 `json:"qty_base"`
}

// CreateReturnRequestInput is the portal-submitted intent. CustomerID/RequestedBy come from the verified
// portal caller, never the body.
type CreateReturnRequestInput struct {
	CustomerID  string                   `json:"-"`
	RequestedBy string                   `json:"-"`
	OrderID     string                   `json:"order_id"`
	ReasonCode  string                   `json:"reason_code"`
	Note        string                   `json:"note"`
	Lines       []ReturnRequestLineInput `json:"lines"`
}

// ReturnRequestLine is one requested line with its display facts joined from the original order line.
type ReturnRequestLine struct {
	OrderLineID string  `json:"order_line_id"`
	ProductID   string  `json:"product_id"`
	Description string  `json:"description"`
	QtyBase     float64 `json:"qty_base"`
}

// ReturnRequest is one request row (portal list + staff queue shape).
type ReturnRequest struct {
	ID             string              `json:"id"`
	RequestNo      string              `json:"request_no"`
	OrderID        string              `json:"order_id"`
	OrderNo        string              `json:"order_no"`
	CustomerID     string              `json:"customer_id"`
	CustomerName   string              `json:"customer_name,omitempty"`
	BranchID       string              `json:"branch_id"`
	Status         string              `json:"status"`
	ReasonCode     string              `json:"reason_code"`
	Note           string              `json:"note"`
	RequestedBy    string              `json:"requested_by,omitempty"`
	DecidedBy      string              `json:"decided_by,omitempty"`
	DecidedAt      string              `json:"decided_at,omitempty"`
	DecisionReason string              `json:"decision_reason,omitempty"`
	FeeCents       int64               `json:"fee_cents"`
	ReturnID       string              `json:"return_id,omitempty"`
	ReturnNo       string              `json:"return_no,omitempty"`
	CreatedAt      string              `json:"created_at"`
	Lines          []ReturnRequestLine `json:"lines"`
}

// returnRequestRow is the insert shape (consumed by Repository + the test fake).
type returnRequestRow struct {
	OrderID     string
	CustomerID  string
	ReasonCode  string
	Note        string
	RequestedBy string
}

// requestDecision is the PENDING → APPROVED/REJECTED update (consumed by Repository + the test fake).
type requestDecision struct {
	ID             string
	ToStatus       string
	DecidedBy      string
	DecisionReason string
	ReturnID       string
	FeeCents       int64
}

// ClientStatus reports the HTTP status a pos client error carries, so a composing BFF (the portal) can
// map this module's error taxonomy without importing unexported types. ok=false means "not a pos client
// error" (treat as internal).
func ClientStatus(err error) (int, bool) {
	var ce clientError
	if errors.As(err, &ce) {
		return ce.status, true
	}
	return 0, false
}

// -- service (portal-facing, tenant-scoped) ----------------------------------

// ReturnEligibility is the portal pre-request read: LookupReturn's netting (qty sold − prior returns per
// line) scoped to the calling tenant. An order that is not the customer's, or not a committed POS sale -
// is NOT FOUND (IDOR-safe: the two are indistinguishable).
func (s *Service) ReturnEligibility(ctx context.Context, customerID, orderID string) (ReturnLookup, error) {
	if customerID == "" || orderID == "" {
		return ReturnLookup{}, errBadRequest
	}
	lk, err := s.LookupReturn(ctx, orderID, "")
	if err != nil {
		return ReturnLookup{}, err
	}
	if lk.CustomerID != customerID {
		return ReturnLookup{}, errReturnNotFound
	}
	return lk, nil
}

// CreateReturnRequest records a PENDING portal return request, intent only: no money, no stock, no GL.
// The qty guard here is a courtesy pre-check against the netted lines; the approval re-guards everything
// under FOR UPDATE inside the rail's tx (the over-return invariant is never trusted to this read).
func (s *Service) CreateReturnRequest(ctx context.Context, in CreateReturnRequestInput) (ReturnRequest, error) {
	if in.CustomerID == "" || in.OrderID == "" {
		return ReturnRequest{}, errBadRequest
	}
	if !validReasonCode(in.ReasonCode) {
		return ReturnRequest{}, errBadReasonCode
	}
	if in.ReasonCode == ReasonOther && in.Note == "" {
		return ReturnRequest{}, errOtherNeedsNote
	}
	if len(in.Note) > maxRequestNoteLen {
		return ReturnRequest{}, clientErr(http.StatusUnprocessableEntity, "pos: note exceeds %d characters", maxRequestNoteLen)
	}
	if len(in.Lines) == 0 {
		return ReturnRequest{}, errNoReturnLines
	}
	lk, err := s.ReturnEligibility(ctx, in.CustomerID, in.OrderID) // IDOR guard + the netted lines
	if err != nil {
		return ReturnRequest{}, err
	}
	returnable := make(map[string]float64, len(lk.Lines))
	for _, ln := range lk.Lines {
		returnable[ln.OrderLineID] = ln.QtySold - ln.QtyReturned
	}
	seen := make(map[string]bool, len(in.Lines))
	for _, ln := range in.Lines {
		if ln.OrderLineID == "" || ln.QtyBase <= 0 {
			return ReturnRequest{}, clientErr(http.StatusUnprocessableEntity, "pos: every request line needs an order_line_id and a positive qty_base")
		}
		if seen[ln.OrderLineID] {
			return ReturnRequest{}, clientErr(http.StatusUnprocessableEntity, "pos: line %s is requested twice", ln.OrderLineID)
		}
		seen[ln.OrderLineID] = true
		remaining, ok := returnable[ln.OrderLineID]
		if !ok {
			return ReturnRequest{}, clientErr(http.StatusUnprocessableEntity, "pos: line %s does not belong to order %s", ln.OrderLineID, lk.OrderNo)
		}
		if ln.QtyBase > remaining+qtyEpsilon {
			return ReturnRequest{}, clientErr(http.StatusUnprocessableEntity,
				"pos: over-return on line %s, %.4f requested but only %.4f returnable", ln.OrderLineID, ln.QtyBase, remaining)
		}
	}
	return s.store.InsertReturnRequest(ctx, returnRequestRow{
		OrderID: lk.OrderID, CustomerID: in.CustomerID, ReasonCode: in.ReasonCode,
		Note: in.Note, RequestedBy: in.RequestedBy,
	}, in.Lines)
}

// ListCustomerReturnRequests is the portal "my returns" read (newest first), tenant-scoped.
func (s *Service) ListCustomerReturnRequests(ctx context.Context, customerID string) ([]ReturnRequest, error) {
	if customerID == "" {
		return nil, errBadRequest
	}
	return s.store.ListReturnRequestsByCustomer(ctx, customerID, 200)
}

// -- service (staff-facing) ---------------------------------------------------

// ListReturnRequests is the staff queue (oldest PENDING first so the queue drains in order).
func (s *Service) ListReturnRequests(ctx context.Context, status string) ([]ReturnRequest, error) {
	if status == "" {
		status = RequestPending
	}
	switch status {
	case RequestPending, RequestApproved, RequestRejected:
	default:
		return nil, clientErr(http.StatusBadRequest, "pos: unknown request status %q", status)
	}
	return s.store.ListReturnRequestsByStatus(ctx, status, 200)
}

// ApproveReturnRequest executes the request through the EXISTING return rail: ACCOUNT_CREDIT,
// Restock=true, the branch's pos_policy restocking fee, attributed to the staff approver (the rail
// stamps created_by from this ctx and audits RETURN_COMPLETED after the decision commits).
//
// Decisions are SERIALIZED on the request row: the decision tx takes FOR UPDATE on return_requests and
// re-checks PENDING under the lock, so a concurrent reject cannot land while the rail is committing
// money. The rail's transactional body (returnInTx) rides THIS decision tx, so the money and the
// decision commit atomically: the old crash between the committed return and the decision update has
// no window any more. The rail's idempotency key is derived from the request id, so a return already
// committed under the reserved key with the request still PENDING (the old shape's crash row) replays,
// not re-refunds, on re-approve, and reject still refuses it; a decided request is a 409.
// Over-return/other rail refusals surface unchanged and leave the request PENDING (the whole tx rolls
// back, money included).
func (s *Service) ApproveReturnRequest(ctx context.Context, requestID string) (ReturnRequest, error) {
	if requestID == "" {
		return ReturnRequest{}, errBadRequest
	}
	rr, found, err := s.store.GetReturnRequest(ctx, requestID)
	if err != nil {
		return ReturnRequest{}, err
	}
	if !found {
		return ReturnRequest{}, errRequestNotFound
	}
	if rr.Status != RequestPending {
		return ReturnRequest{}, clientErr(http.StatusConflict, "pos: request %s is already %s", rr.RequestNo, rr.Status)
	}
	policy, err := s.store.ReadPosPolicy(ctx, rr.BranchID)
	if err != nil {
		return ReturnRequest{}, err
	}
	lines := make([]ReturnLineInput, len(rr.Lines))
	for i, ln := range rr.Lines {
		lines[i] = ReturnLineInput{OrderLineID: ln.OrderLineID, QtyBase: ln.QtyBase, ReasonCode: rr.ReasonCode}
	}
	// The rail's pre-transaction work (validation, the sale's context, the branch wall, the replay
	// pre-check) runs here while no connection is held. The rail's transactional body below runs on
	// THIS decision tx: the old shape called s.Return, which opened its OWN transaction on the pool
	// while this tx held one, so pool-size concurrent approvals each waited for a second connection
	// the pool could never give and the process stalled until the action timeouts. Carried on the one
	// transaction, the return and the decision commit atomically or not at all.
	plan, err := s.prepareReturn(ctx, ReturnInput{
		IdempotencyKey: reservedRequestKeyPrefix + rr.ID, // replay-safe: re-approving after a crash replays the SAME return
		OrderID:        rr.OrderID,
		Lines:          lines,
		RefundMethod:   RefundAccountCredit,
		Reason:         "portal return " + rr.RequestNo + " (" + rr.ReasonCode + ")",
		Restock:        true,
		FeeBps:         policy.RestockingFeeBps,
		internalRail:   true,
	})
	if err != nil {
		return ReturnRequest{}, err
	}
	var ret Return
	replayed := plan.preReplayed
	err = s.store.RunInTx(ctx, func(tx pgx.Tx) error {
		status, found, err := s.store.LockReturnRequestStatus(ctx, tx, rr.ID)
		if err != nil {
			return err
		}
		if !found {
			return errRequestNotFound
		}
		if status != RequestPending {
			return clientErr(http.StatusConflict, "pos: request %s is already %s", rr.RequestNo, status)
		}
		// The rail rides THIS tx (returnInTx), the request-row lock is what keeps a concurrent reject
		// from recording REJECTED on top of the committing money: nothing outside this tx can see the
		// return until the decision commits with it.
		if plan.preReplayed {
			// The replay pre-check already found the request's committed return (a crash row from the
			// old shape, money under the reserved key with the request still PENDING): converge onto
			// it, the rail body does not run again.
			ret = plan.preReplay
		} else {
			var rerr error
			ret, replayed, _, rerr = s.returnInTx(ctx, tx, plan)
			if rerr != nil {
				return rerr
			}
		}
		// The replayed/committed return MUST be this request's own rail execution. A foreign return
		// squatting on the reserved key must never be stamped as the decision, refuse loudly (500,
		// not a client error) and leave the request PENDING for investigation.
		if ret.OrderID != rr.OrderID || ret.RefundMethod != RefundAccountCredit {
			return fmt.Errorf("pos: idempotency-key collision on %q, return %s (order %s, method %s) does not belong to request %s (order %s); decision NOT stamped, request left PENDING",
				reservedRequestKeyPrefix+rr.ID, ret.ReturnNo, ret.OrderID, ret.RefundMethod, rr.RequestNo, rr.OrderID)
		}
		return s.store.DecideReturnRequestTx(ctx, tx, requestDecision{
			ID: rr.ID, ToStatus: RequestApproved, DecidedBy: identity.StaffUserIDFromContext(ctx),
			ReturnID: ret.ReturnID, FeeCents: ret.FeeCents,
		})
	})
	if err != nil {
		return ReturnRequest{}, err
	}
	// The rail's RETURN_COMPLETED audit rides AFTER the decision tx commits: inside the tx it would
	// ask the pool for a connection while this tx holds one, the very second acquisition this shape
	// removes. A replayed answer writes none, the same shape as the rail's own post-commit hook.
	if !replayed {
		s.record(ctx, audit.SensitiveAccess{
			Action: audit.ActReturnCompleted, EntityType: "pos.return", EntityID: ret.ReturnID,
			ActorID: plan.actor, BranchID: plan.oc.BranchID, Purpose: "POS_RETURN", Reason: RefundAccountCredit,
			Extra: map[string]any{"return_no": ret.ReturnNo, "total_cents": ret.TotalCents},
		})
	}
	out, found, err := s.store.GetReturnRequest(ctx, rr.ID)
	if err != nil {
		return ReturnRequest{}, err
	}
	if !found {
		return ReturnRequest{}, errRequestNotFound
	}
	return out, nil
}

// RejectReturnRequest records the decision + reason. Nothing else moves. It serializes on the request
// row (FOR UPDATE) like ApproveReturnRequest, and additionally probes the rail's reserved idempotency
// key: if the approve rail already committed money (a crash between rail-commit and decide, or a
// concurrent approve that won the lock), the rejection is refused with a 409 telling the operator to
// replay approve so the request converges with the money that actually moved.
func (s *Service) RejectReturnRequest(ctx context.Context, requestID, reason string) (ReturnRequest, error) {
	if requestID == "" {
		return ReturnRequest{}, errBadRequest
	}
	if reason == "" {
		return ReturnRequest{}, errRejectNeedsWhy
	}
	if len(reason) > maxRequestNoteLen {
		return ReturnRequest{}, clientErr(http.StatusUnprocessableEntity, "pos: rejection reason exceeds %d characters", maxRequestNoteLen)
	}
	rr, found, err := s.store.GetReturnRequest(ctx, requestID)
	if err != nil {
		return ReturnRequest{}, err
	}
	if !found {
		return ReturnRequest{}, errRequestNotFound
	}
	if rr.Status != RequestPending {
		return ReturnRequest{}, clientErr(http.StatusConflict, "pos: request %s is already %s", rr.RequestNo, rr.Status)
	}
	err = s.store.RunInTx(ctx, func(tx pgx.Tx) error {
		status, found, err := s.store.LockReturnRequestStatus(ctx, tx, rr.ID)
		if err != nil {
			return err
		}
		if !found {
			return errRequestNotFound
		}
		if status != RequestPending {
			return clientErr(http.StatusConflict, "pos: request %s is already %s", rr.RequestNo, status)
		}
		// Crash-divergence probe: money already committed under this request's rail key means the
		// request must converge APPROVED, never REJECTED. The probe reads ON this decision tx
		// (FindReturnByKeyTx): through the pool it would ask for a second connection while this tx
		// holds one, and pool-size concurrent rejects each waited for a connection the pool could
		// never give, the same starvation the approval rail carried.
		if _, found, err := s.store.FindReturnByKeyTx(ctx, tx, reservedRequestKeyPrefix+rr.ID); err != nil {
			return err
		} else if found {
			return clientErr(http.StatusConflict,
				"pos: request %s already executed its return (money committed), replay approve to converge; it cannot be rejected", rr.RequestNo)
		}
		return s.store.DecideReturnRequestTx(ctx, tx, requestDecision{
			ID: rr.ID, ToStatus: RequestRejected, DecidedBy: identity.StaffUserIDFromContext(ctx), DecisionReason: reason,
		})
	})
	if err != nil {
		return ReturnRequest{}, err
	}
	out, found, err := s.store.GetReturnRequest(ctx, rr.ID)
	if err != nil {
		return ReturnRequest{}, err
	}
	if !found {
		return ReturnRequest{}, errRequestNotFound
	}
	return out, nil
}

// -- staff HTTP surface -------------------------------------------------------

// registerReturnRequestRoutes self-registers the staff approval queue (the RegisterRoutes one-line rule).
// Staff-gated like the sibling POST /api/v1/pos/returns, the decision is a counter-money action.
func (h *Handler) registerReturnRequestRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/pos/return-requests", g.RequireStaff(h.staff(h.listReturnRequests)))
	r.Handle("POST /api/v1/pos/return-requests/{id}/approve", g.RequireStaff(h.staff(h.approveReturnRequest)))
	r.Handle("POST /api/v1/pos/return-requests/{id}/reject", g.RequireStaff(h.staff(h.rejectReturnRequest)))
}

func (h *Handler) listReturnRequests(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	out, err := h.svc.ListReturnRequests(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) approveReturnRequest(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	out, err := h.svc.ApproveReturnRequest(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) rejectReturnRequest(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var in struct {
		Reason string `json:"reason"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	out, err := h.svc.RejectReturnRequest(r.Context(), r.PathValue("id"), in.Reason)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// -- persistence (0045 tables; *Repository satisfies the posStore additions) --

func (r *Repository) InsertReturnRequest(ctx context.Context, rr returnRequestRow, lines []ReturnRequestLineInput) (ReturnRequest, error) {
	if r.pool == nil {
		return ReturnRequest{}, errNoPool
	}
	var id string
	err := r.RunInTx(ctx, func(tx pgx.Tx) error {
		const q = `
INSERT INTO return_requests (request_no, order_id, customer_id, reason_code, note, requested_by)
VALUES ('#RREQ-' || nextval('return_request_no_seq'), $1, $2, $3, $4, $5)
RETURNING id::text`
		if err := tx.QueryRow(ctx, q, rr.OrderID, rr.CustomerID, rr.ReasonCode, rr.Note,
			nullStr(rr.RequestedBy)).Scan(&id); err != nil {
			return err
		}
		for _, l := range lines {
			if _, err := tx.Exec(ctx,
				`INSERT INTO return_request_lines (request_id, order_line_id, qty_base) VALUES ($1, $2, $3)`,
				id, l.OrderLineID, l.QtyBase); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return ReturnRequest{}, err
	}
	out, _, err := r.GetReturnRequest(ctx, id)
	return out, err
}

// selectReturnRequest is the shared row shape: the request + its order/customer display facts + the
// linked return's number (approval).
const selectReturnRequest = `
SELECT rq.id::text, rq.request_no, rq.order_id::text, o.order_no, rq.customer_id::text, c.name,
       o.branch_id::text, rq.status, rq.reason_code, rq.note,
       COALESCE(rq.requested_by::text,''), COALESCE(rq.decided_by::text,''), COALESCE(rq.decided_at::text,''),
       rq.decision_reason, rq.fee_cents, COALESCE(rq.return_id::text,''), COALESCE(pr.return_no,''),
       rq.created_at::text
  FROM return_requests rq
  JOIN orders o ON o.id = rq.order_id
  JOIN customers c ON c.id = rq.customer_id
  LEFT JOIN pos_returns pr ON pr.id = rq.return_id`

func scanReturnRequest(row pgx.Row) (ReturnRequest, error) {
	var rr ReturnRequest
	err := row.Scan(&rr.ID, &rr.RequestNo, &rr.OrderID, &rr.OrderNo, &rr.CustomerID, &rr.CustomerName,
		&rr.BranchID, &rr.Status, &rr.ReasonCode, &rr.Note,
		&rr.RequestedBy, &rr.DecidedBy, &rr.DecidedAt,
		&rr.DecisionReason, &rr.FeeCents, &rr.ReturnID, &rr.ReturnNo, &rr.CreatedAt)
	return rr, err
}

func (r *Repository) loadRequestLines(ctx context.Context, requests []ReturnRequest) error {
	for i := range requests {
		rows, err := r.pool.Query(ctx, `
SELECT rl.order_line_id::text, COALESCE(ol.product_id::text,''), ol.description, rl.qty_base::float8
  FROM return_request_lines rl
  JOIN order_lines ol ON ol.id = rl.order_line_id
 WHERE rl.request_id::text = $1
 ORDER BY ol.line_no`, requests[i].ID)
		if err != nil {
			return err
		}
		lines := []ReturnRequestLine{}
		for rows.Next() {
			var l ReturnRequestLine
			if err := rows.Scan(&l.OrderLineID, &l.ProductID, &l.Description, &l.QtyBase); err != nil {
				rows.Close()
				return err
			}
			lines = append(lines, l)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		requests[i].Lines = lines
	}
	return nil
}

func (r *Repository) GetReturnRequest(ctx context.Context, id string) (ReturnRequest, bool, error) {
	if r.pool == nil {
		return ReturnRequest{}, false, errNoPool
	}
	rr, err := scanReturnRequest(r.pool.QueryRow(ctx, selectReturnRequest+` WHERE rq.id::text = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ReturnRequest{}, false, nil
	}
	if err != nil {
		return ReturnRequest{}, false, err
	}
	out := []ReturnRequest{rr}
	if err := r.loadRequestLines(ctx, out); err != nil {
		return ReturnRequest{}, false, err
	}
	return out[0], true, nil
}

func (r *Repository) listReturnRequests(ctx context.Context, where string, arg any, order string, limit int) ([]ReturnRequest, error) {
	if r.pool == nil {
		return nil, errNoPool
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := r.pool.Query(ctx, selectReturnRequest+where+order+` LIMIT $2`, arg, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReturnRequest{}
	for rows.Next() {
		rr, err := scanReturnRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rr)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := r.loadRequestLines(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *Repository) ListReturnRequestsByCustomer(ctx context.Context, customerID string, limit int) ([]ReturnRequest, error) {
	return r.listReturnRequests(ctx, ` WHERE rq.customer_id::text = $1`, customerID, ` ORDER BY rq.created_at DESC`, limit)
}

func (r *Repository) ListReturnRequestsByStatus(ctx context.Context, status string, limit int) ([]ReturnRequest, error) {
	// Oldest first, the approval queue drains in arrival order.
	return r.listReturnRequests(ctx, ` WHERE rq.status = $1`, status, ` ORDER BY rq.created_at`, limit)
}

// LockReturnRequestStatus takes FOR UPDATE on one return_requests row inside the caller's tx and
// returns its current status, the decision serializer: whoever holds this lock is the only decider
// until commit/rollback, so approve-vs-reject can never interleave around the rail's money tx.
func (r *Repository) LockReturnRequestStatus(ctx context.Context, tx pgx.Tx, id string) (string, bool, error) {
	var status string
	err := tx.QueryRow(ctx, `SELECT status FROM return_requests WHERE id::text = $1 FOR UPDATE`, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return status, true, nil
}

// DecideReturnRequestTx walks a request PENDING → APPROVED/REJECTED inside the caller's locking tx.
// The status guard is belt-and-braces, the caller already re-checked PENDING under FOR UPDATE.
func (r *Repository) DecideReturnRequestTx(ctx context.Context, tx pgx.Tx, d requestDecision) error {
	tag, err := tx.Exec(ctx, `
UPDATE return_requests
   SET status = $2, decided_by = $3, decided_at = now(), decision_reason = $4,
       return_id = NULLIF($5,'')::uuid, fee_cents = $6
 WHERE id::text = $1 AND status = 'PENDING'`,
		d.ID, d.ToStatus, nullStr(d.DecidedBy), d.DecisionReason, d.ReturnID, d.FeeCents)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return clientErr(http.StatusConflict, "pos: request %s is already decided", d.ID)
	}
	return nil
}
