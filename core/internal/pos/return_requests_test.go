package pos

// return_requests_test.go, A-06 unit tests (the service_w7_test.go fakeStore pattern, DB-free):
// a PENDING request moves nothing; the tenant guard answers 404 for another account's order; approval
// executes the EXISTING return rail (ACCOUNT_CREDIT + restock → credit memo subledger rows +
// pos.return.committed/order.returned events) attributed to the staff approver; the restocking-fee
// policy reduces the credit and rides the event's fee_cents; rejection records only the reason; the
// rail's over-return guard still holds when driven through an approval.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// -- fakeStore: the A-06 posStore additions (state field lives in service_test.go) --

func (f *fakeStore) InsertReturnRequest(_ context.Context, rr returnRequestRow, lines []ReturnRequestLineInput) (ReturnRequest, error) {
	f.seq++
	oc := f.retCtx[rr.OrderID]
	out := ReturnRequest{
		ID: fmt.Sprintf("rreq-%d", f.seq), RequestNo: fmt.Sprintf("#RREQ-%d", f.seq),
		OrderID: rr.OrderID, OrderNo: oc.OrderNo, CustomerID: rr.CustomerID, BranchID: oc.BranchID,
		Status: RequestPending, ReasonCode: rr.ReasonCode, Note: rr.Note, RequestedBy: rr.RequestedBy,
		CreatedAt: "2026-07-13T12:00:00Z",
	}
	for _, l := range lines {
		rl := f.retLines[l.OrderLineID]
		out.Lines = append(out.Lines, ReturnRequestLine{OrderLineID: l.OrderLineID, ProductID: rl.ProductID, QtyBase: l.QtyBase})
	}
	f.returnRequests[out.ID] = out
	return out, nil
}

func (f *fakeStore) GetReturnRequest(_ context.Context, id string) (ReturnRequest, bool, error) {
	rr, ok := f.returnRequests[id]
	return rr, ok, nil
}

func (f *fakeStore) ListReturnRequestsByCustomer(_ context.Context, customerID string, _ int) ([]ReturnRequest, error) {
	out := []ReturnRequest{}
	for _, rr := range f.returnRequests {
		if rr.CustomerID == customerID {
			out = append(out, rr)
		}
	}
	return out, nil
}

func (f *fakeStore) ListReturnRequestsByStatus(_ context.Context, status string, _ int) ([]ReturnRequest, error) {
	out := []ReturnRequest{}
	for _, rr := range f.returnRequests {
		if rr.Status == status {
			out = append(out, rr)
		}
	}
	return out, nil
}

func (f *fakeStore) LockReturnRequestStatus(_ context.Context, _ pgx.Tx, id string) (string, bool, error) {
	rr, ok := f.returnRequests[id]
	return rr.Status, ok, nil
}

func (f *fakeStore) DecideReturnRequestTx(_ context.Context, _ pgx.Tx, d requestDecision) error {
	rr, ok := f.returnRequests[d.ID]
	if !ok {
		return errRequestNotFound
	}
	if rr.Status != RequestPending {
		return clientErr(409, "pos: request %s is already %s", rr.RequestNo, rr.Status)
	}
	rr.Status = d.ToStatus
	rr.DecidedBy = d.DecidedBy
	rr.DecidedAt = "2026-07-13T12:00:00Z"
	rr.DecisionReason = d.DecisionReason
	rr.ReturnID = d.ReturnID
	rr.FeeCents = d.FeeCents
	f.returnRequests[d.ID] = rr
	return nil
}

// requestInput is a valid portal request against the seedReturnable sale (4 of 10 units on line-1).
func requestInput() CreateReturnRequestInput {
	return CreateReturnRequestInput{
		CustomerID: "cust-abc", RequestedBy: "pu-1", OrderID: "order-1",
		ReasonCode: ReasonDamaged, Note: "arrived cracked",
		Lines: []ReturnRequestLineInput{{OrderLineID: "line-1", QtyBase: 4}},
	}
}

// -- request creation ---------------------------------------------------------

func TestReturnRequest_Create_PendingMovesNothing(t *testing.T) {
	h := newHarness()
	seedReturnable(h)
	rr, err := h.svc.CreateReturnRequest(context.Background(), requestInput())
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	if rr.Status != RequestPending || rr.RequestNo == "" || rr.OrderID != "order-1" || rr.RequestedBy != "pu-1" {
		t.Fatalf("request row wrong: %+v", rr)
	}
	if len(rr.Lines) != 1 || rr.Lines[0].QtyBase != 4 {
		t.Fatalf("request lines wrong: %+v", rr.Lines)
	}
	// A PENDING request is intent only: NO return, NO subledger row, NO stock, NO event.
	if len(h.store.returnRows) != 0 || len(h.store.custTxns) != 0 || len(h.store.events) != 0 || len(h.restock.calls) != 0 {
		t.Fatal("a PENDING request must move no money, no stock and emit nothing")
	}
	// And it shows on the tenant list + the staff PENDING queue.
	mine, _ := h.svc.ListCustomerReturnRequests(context.Background(), "cust-abc")
	if len(mine) != 1 {
		t.Fatalf("tenant list must carry the request: %+v", mine)
	}
	queue, _ := h.svc.ListReturnRequests(context.Background(), "")
	if len(queue) != 1 || queue[0].Status != RequestPending {
		t.Fatalf("staff queue must default to PENDING: %+v", queue)
	}
}

func TestReturnRequest_IDOR_OtherTenant_NotFound(t *testing.T) {
	h := newHarness()
	seedReturnable(h) // order-1 belongs to cust-abc
	in := requestInput()
	in.CustomerID = "cust-evil"
	_, err := h.svc.CreateReturnRequest(context.Background(), in)
	if !errors.Is(err, errReturnNotFound) {
		t.Fatalf("another tenant's order must read NOT FOUND, got %v", err)
	}
	// The BFF maps this through ClientStatus, a tenant miss must surface as 404, never 403.
	if st, ok := ClientStatus(err); !ok || st != 404 {
		t.Fatalf("ClientStatus must expose the 404, got %d %v", st, ok)
	}
	if _, err := h.svc.ReturnEligibility(context.Background(), "cust-evil", "order-1"); !errors.Is(err, errReturnNotFound) {
		t.Fatalf("eligibility must be tenant-scoped, got %v", err)
	}
	// The owner still sees the netted lines.
	lk, err := h.svc.ReturnEligibility(context.Background(), "cust-abc", "order-1")
	if err != nil || len(lk.Lines) != 1 {
		t.Fatalf("owner eligibility: %v %+v", err, lk)
	}
}

func TestReturnRequest_Create_Validation(t *testing.T) {
	h := newHarness()
	seedReturnable(h)
	cases := []struct {
		name string
		mut  func(*CreateReturnRequestInput)
		want error
	}{
		{"bad reason", func(in *CreateReturnRequestInput) { in.ReasonCode = "MELTED" }, errBadReasonCode},
		{"OTHER needs note", func(in *CreateReturnRequestInput) { in.ReasonCode = ReasonOther; in.Note = "" }, errOtherNeedsNote},
		{"no lines", func(in *CreateReturnRequestInput) { in.Lines = nil }, errNoReturnLines},
	}
	for _, tc := range cases {
		in := requestInput()
		tc.mut(&in)
		if _, err := h.svc.CreateReturnRequest(context.Background(), in); !errors.Is(err, tc.want) {
			t.Fatalf("%s: want %v, got %v", tc.name, tc.want, err)
		}
	}
	// Over-request (11 of 10) and a foreign line id are 422s.
	var ce clientError
	in := requestInput()
	in.Lines[0].QtyBase = 11
	if _, err := h.svc.CreateReturnRequest(context.Background(), in); !errors.As(err, &ce) || ce.status != 422 {
		t.Fatalf("over-request must 422, got %v", err)
	}
	in = requestInput()
	in.Lines[0].OrderLineID = "line-nope"
	if _, err := h.svc.CreateReturnRequest(context.Background(), in); !errors.As(err, &ce) || ce.status != 422 {
		t.Fatalf("foreign line must 422, got %v", err)
	}
	if len(h.store.returnRequests) != 0 {
		t.Fatal("refused requests must persist nothing")
	}
}

// -- approval → the EXISTING rail ---------------------------------------------

func TestReturnRequest_Approve_ExecutesAccountCreditRail(t *testing.T) {
	h := newHarness()
	seedReturnable(h)
	h.store.customers["cust-abc"] = customerRec{ID: "cust-abc", CustomerType: "CONTRACTOR", OpenBalanceCents: 5000}
	rr, err := h.svc.CreateReturnRequest(context.Background(), requestInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	out, err := h.svc.ApproveReturnRequest(ctxWithActor("u-staff"), rr.ID)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	// The decision row links the rail-committed return and stamps the approver.
	if out.Status != RequestApproved || out.ReturnID == "" || out.DecidedBy != "u-staff" {
		t.Fatalf("approved request wrong: %+v", out)
	}
	// The rail ran ACCOUNT_CREDIT + restock, attributed to the approver: no till/card touch, a CREDIT_MEMO
	// subledger drop for the full 4520 (original PAID invoice → all store credit), restock dispatched.
	if len(h.store.returnRows) != 1 {
		t.Fatalf("want 1 committed return, got %d", len(h.store.returnRows))
	}
	ret := h.store.returnRows[0]
	if ret.RefundMethod != RefundAccountCredit || !ret.Restock || ret.CreatedBy != "u-staff" || ret.FeeCents != 0 {
		t.Fatalf("return row wrong: %+v", ret)
	}
	if h.tender.refundCalled || h.refunder.called {
		t.Fatal("account credit never touches the till or the card rail")
	}
	if len(h.store.custTxns) != 1 || h.store.custTxns[0].TxnType != "CREDIT_MEMO" || h.store.custTxns[0].AmountCents != -4520 {
		t.Fatalf("credit memo wrong: %+v", h.store.custTxns)
	}
	if len(h.restock.calls) != 1 || h.restock.calls[0].Lines[0].QtyBase != 4 {
		t.Fatalf("restock not dispatched: %+v", h.restock.calls)
	}
	// The two GL events emit through the rail unchanged.
	ev, ok := h.store.lastEvent("pos.return.committed")
	if !ok || ev["refund_method"] != RefundAccountCredit || ev["total_cents"] != int64(4520) || ev["fee_cents"] != int64(0) {
		t.Fatalf("pos.return.committed wrong: %v", ev)
	}
	if _, ok := h.store.lastEvent("order.returned"); !ok {
		t.Fatal("restocked approval must emit order.returned")
	}
	if !h.rec.has("RETURN_COMPLETED") {
		t.Fatal("the rail's RETURN_COMPLETED audit must fire on approval")
	}
	// A second approve (or a reject) of the decided request is a 409.
	var ce clientError
	if _, err := h.svc.ApproveReturnRequest(ctxWithActor("u-staff"), rr.ID); !errors.As(err, &ce) || ce.status != 409 {
		t.Fatalf("double-approve must 409, got %v", err)
	}
	if _, err := h.svc.RejectReturnRequest(ctxWithActor("u-staff"), rr.ID, "changed my mind"); !errors.As(err, &ce) || ce.status != 409 {
		t.Fatalf("reject-after-approve must 409, got %v", err)
	}
}

func TestReturnRequest_Approve_RestockingFee_ReducesCreditAndRidesEvent(t *testing.T) {
	h := newHarness()
	seedReturnable(h)
	h.store.customers["cust-abc"] = customerRec{ID: "cust-abc", CustomerType: "CONTRACTOR", OpenBalanceCents: 0}
	// 10% restocking fee on the sale's branch. subtotal 4000 → fee 400; credit 4520 − 400 = 4120.
	h.store.policies["branch-trt"] = PosPolicy{BranchID: "branch-trt", CODEnabled: true, RestockingFeeBps: 1000}
	rr, err := h.svc.CreateReturnRequest(context.Background(), requestInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	out, err := h.svc.ApproveReturnRequest(ctxWithActor("u-staff"), rr.ID)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if out.FeeCents != 400 {
		t.Fatalf("request must snapshot the fee, got %d", out.FeeCents)
	}
	if h.store.returnRows[0].FeeCents != 400 {
		t.Fatalf("return row must carry the fee: %+v", h.store.returnRows[0])
	}
	// The customer's account is credited total − fee, in one CREDIT_MEMO (no open invoice balance).
	if len(h.store.custTxns) != 1 || h.store.custTxns[0].AmountCents != -4120 || h.store.custTxns[0].BalanceAfter != -4120 {
		t.Fatalf("fee must reduce the credit: %+v", h.store.custTxns)
	}
	// The GL event carries the full split + the fee (DR 4000/520 = CR 4120 + CR 400 downstream).
	ev, _ := h.store.lastEvent("pos.return.committed")
	if ev["subtotal_cents"] != int64(4000) || ev["tax_cents"] != int64(520) ||
		ev["total_cents"] != int64(4520) || ev["fee_cents"] != int64(400) {
		t.Fatalf("event split wrong: %v", ev)
	}
}

func TestReturn_Fee_GuardRails(t *testing.T) {
	h := newHarness()
	seedReturnable(h)
	// A fee on a CASH refund is refused, the fee exists only on the ACCOUNT_CREDIT rail.
	in := returnInput()
	in.FeeBps = 500
	if _, err := h.svc.Return(context.Background(), in); !errors.Is(err, errFeeNeedsCredit) {
		t.Fatalf("want errFeeNeedsCredit, got %v", err)
	}
	// Out-of-range bps is refused before anything opens.
	in = returnInput()
	in.RefundMethod = RefundAccountCredit
	in.TillSessionID = ""
	in.FeeBps = 10001
	if _, err := h.svc.Return(context.Background(), in); !errors.Is(err, errBadFeeBps) {
		t.Fatalf("want errBadFeeBps, got %v", err)
	}
	if len(h.store.returnRows) != 0 {
		t.Fatal("refused fee inputs must persist nothing")
	}
}

// -- rejection ----------------------------------------------------------------

func TestReturnRequest_Reject_RecordsReasonOnly(t *testing.T) {
	h := newHarness()
	seedReturnable(h)
	rr, err := h.svc.CreateReturnRequest(context.Background(), requestInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Rejecting without a reason is refused.
	if _, err := h.svc.RejectReturnRequest(ctxWithActor("u-staff"), rr.ID, ""); !errors.Is(err, errRejectNeedsWhy) {
		t.Fatalf("want errRejectNeedsWhy, got %v", err)
	}
	out, err := h.svc.RejectReturnRequest(ctxWithActor("u-staff"), rr.ID, "outside the return window")
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if out.Status != RequestRejected || out.DecisionReason != "outside the return window" || out.DecidedBy != "u-staff" || out.ReturnID != "" {
		t.Fatalf("rejected request wrong: %+v", out)
	}
	// Nothing moved.
	if len(h.store.returnRows) != 0 || len(h.store.custTxns) != 0 || len(h.store.events) != 0 {
		t.Fatal("a rejection must move nothing")
	}
	// A decided request cannot be approved.
	var ce clientError
	if _, err := h.svc.ApproveReturnRequest(ctxWithActor("u-staff"), rr.ID); !errors.As(err, &ce) || ce.status != 409 {
		t.Fatalf("approve-after-reject must 409, got %v", err)
	}
}

// -- the rail's guards still hold through an approval -------------------------

func TestReturnRequest_Approve_OverReturn_Blocked_StaysPending(t *testing.T) {
	h := newHarness()
	seedReturnable(h)
	rr, err := h.svc.CreateReturnRequest(context.Background(), requestInput()) // asks for 4
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Meanwhile the counter returns 8 of the 10 directly, only 2 remain.
	direct := returnInput()
	direct.IdempotencyKey = "ret-direct"
	direct.Lines[0].QtyBase = 8
	if _, err := h.svc.Return(context.Background(), direct); err != nil {
		t.Fatalf("direct return: %v", err)
	}
	txns := len(h.store.custTxns)
	var ce clientError
	if _, err := h.svc.ApproveReturnRequest(ctxWithActor("u-staff"), rr.ID); !errors.As(err, &ce) || ce.status != 422 {
		t.Fatalf("approval past the over-return guard must 422, got %v", err)
	}
	got, _, _ := h.store.GetReturnRequest(context.Background(), rr.ID)
	if got.Status != RequestPending {
		t.Fatalf("a refused approval must leave the request PENDING, got %s", got.Status)
	}
	if len(h.store.returnRows) != 1 || len(h.store.custTxns) != txns {
		t.Fatal("the refused approval must persist nothing beyond the earlier direct return")
	}
	if _, err := h.svc.ApproveReturnRequest(ctxWithActor("u-staff"), "rreq-missing"); !errors.Is(err, errRequestNotFound) {
		t.Fatalf("unknown request must 404, got %v", err)
	}
}

func TestReturnRequest_ListStatusValidation(t *testing.T) {
	h := newHarness()
	var ce clientError
	if _, err := h.svc.ListReturnRequests(context.Background(), "WEIRD"); !errors.As(err, &ce) || ce.status != 400 {
		t.Fatalf("unknown status filter must 400, got %v", err)
	}
}

// -- B1: decision race + crash divergence -------------------------------------

// railInputFor mirrors exactly what ApproveReturnRequest sends the rail, used to simulate the crash
// window (money committed under "rreq:"+id, decision row still PENDING).
func railInputFor(rr ReturnRequest) ReturnInput {
	lines := make([]ReturnLineInput, len(rr.Lines))
	for i, ln := range rr.Lines {
		lines[i] = ReturnLineInput{OrderLineID: ln.OrderLineID, QtyBase: ln.QtyBase, ReasonCode: rr.ReasonCode}
	}
	return ReturnInput{
		IdempotencyKey: "rreq:" + rr.ID, OrderID: rr.OrderID, Lines: lines,
		RefundMethod: RefundAccountCredit, Restock: true, internalRail: true,
	}
}

func TestReturnRequest_RejectAfterCrashWindow_409_ApproveConverges(t *testing.T) {
	h := newHarness()
	seedReturnable(h)
	h.store.customers["cust-abc"] = customerRec{ID: "cust-abc", CustomerType: "CONTRACTOR"}
	rr, err := h.svc.CreateReturnRequest(context.Background(), requestInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Simulate the crash between rail-commit and decide: the return commits under the request's
	// reserved key, but the request row stays PENDING.
	ret, err := h.svc.Return(context.Background(), railInputFor(rr))
	if err != nil {
		t.Fatalf("simulated crash-window rail commit: %v", err)
	}
	h.store.cachedReturns["rreq:"+rr.ID] = ret // the real repo would find it via ux_pos_returns_idem

	// Rejecting now would record REJECTED on top of committed money, must 409 and tell the operator
	// to replay approve.
	var ce clientError
	_, rerr := h.svc.RejectReturnRequest(ctxWithActor("u-staff"), rr.ID, "changed my mind")
	if !errors.As(rerr, &ce) || ce.status != 409 {
		t.Fatalf("reject after the crash window must 409, got %v", rerr)
	}
	if !strings.Contains(rerr.Error(), "approve") {
		t.Fatalf("the 409 must direct the operator to replay approve: %v", rerr)
	}
	got, _, _ := h.store.GetReturnRequest(context.Background(), rr.ID)
	if got.Status != RequestPending {
		t.Fatalf("a refused rejection must leave the request PENDING, got %s", got.Status)
	}

	// Replaying approve converges: SAME return id, APPROVED, no second money movement.
	txns := len(h.store.custTxns)
	returns := len(h.store.returnRows)
	out, err := h.svc.ApproveReturnRequest(ctxWithActor("u-staff"), rr.ID)
	if err != nil {
		t.Fatalf("approve replay must converge: %v", err)
	}
	if out.Status != RequestApproved || out.ReturnID != ret.ReturnID {
		t.Fatalf("approve must stamp the ALREADY-COMMITTED return %s, got %+v", ret.ReturnID, out)
	}
	if len(h.store.custTxns) != txns || len(h.store.returnRows) != returns {
		t.Fatal("the converging approve must replay, never re-refund")
	}
}

// -- M1: "rreq:" key-namespace hijack -----------------------------------------

func TestReturn_ReservedRequestKeyPrefix_Rejected422(t *testing.T) {
	h := newHarness()
	seedReturnable(h)
	in := returnInput()
	in.IdempotencyKey = "rreq:" + "some-request-id" // a public caller squatting on the reserved namespace
	var ce clientError
	if _, err := h.svc.Return(context.Background(), in); !errors.As(err, &ce) || ce.status != 422 {
		t.Fatalf("a public return with an rreq: key must 422, got %v", err)
	}
	if len(h.store.returnRows) != 0 {
		t.Fatal("the refused key must persist nothing")
	}
}

func TestReturnRequest_Approve_ForeignReturnUnderKey_RefusesToStamp(t *testing.T) {
	h := newHarness()
	seedReturnable(h)
	rr, err := h.svc.CreateReturnRequest(context.Background(), requestInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// A foreign return squats on the request's reserved key (simulating a pre-fix hijack or a repo-level
	// insert): different order, different rail.
	h.store.cachedReturns["rreq:"+rr.ID] = Return{
		ReturnID: "ret-foreign", ReturnNo: "#RET-666", OrderID: "order-OTHER",
		RefundMethod: RefundCash, TotalCents: 99999, Status: "COMMITTED",
	}
	_, err = h.svc.ApproveReturnRequest(ctxWithActor("u-staff"), rr.ID)
	if err == nil {
		t.Fatal("approve must refuse to stamp a foreign return")
	}
	if _, ok := ClientStatus(err); ok {
		t.Fatalf("the refusal must be a loud INTERNAL error (not a client status), got %v", err)
	}
	if !strings.Contains(err.Error(), "idempotency-key collision") {
		t.Fatalf("the error must name the idempotency-key collision: %v", err)
	}
	got, _, _ := h.store.GetReturnRequest(context.Background(), rr.ID)
	if got.Status != RequestPending || got.ReturnID != "" {
		t.Fatalf("the request must stay PENDING and unlinked for investigation: %+v", got)
	}
}
