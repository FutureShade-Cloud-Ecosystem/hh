package pos

// refund_per_sale_test.go, the refund approval amount counting every return
// against the same sale together: the sale's earlier committed returns ride
// the comparison, so returns each under the configured amount can no longer
// walk a sale out of the drawer one approved-sized slice at a time. The
// refusal carries the configured amount, the configured approvers and the
// NEW return's own total (the money asked for at the counter, never the
// cumulative), and a manager credential in the header is consulted when the
// CUMULATIVE would cross, not only when the new return alone would.

import (
	"context"
	"errors"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

// seedSaleThenReturn seeds the 442.96 sale of the defect's report and one
// earlier committed return against it, the slice the comprehensive reviewer
// proved: four cash returns of one unit each walked 442.96 out of the drawer
// with no manager because the gate judged every return alone.
func seedSaleThenReturn(h *harness) {
	seedSaleWorth(h, 44296, 0)
	h.store.priorSums["order-1"] = 11074 // one earlier committed return of 110.74
}

// fiveUnitReturn is a cash return of five units of the seeded sale: its own
// total is 221.48, under the configured 250.00.
func fiveUnitReturn() ReturnInput {
	in := wholeReturnInput()
	in.Lines = []ReturnLineInput{{OrderLineID: "line-1", QtyBase: 5, ReasonCode: "DAMAGED"}}
	return in
}

// With one EARLIER committed return on the sale, the new return's own total
// sits under the configured 250.00 but the cumulative 332.22 crosses it: the
// return is refused with the amount and the approvers named, nothing
// persists, no rail is touched, and the audit row names the clerk, the sale
// and the asked-for 221.48, never the cumulative.
func TestRefundSumsPerSale_EarlierReturn_CumulativeCross_Refused(t *testing.T) {
	h := newHarness()
	seedSaleThenReturn(h)
	h.svc = h.svc.WithRoleConfig(defaultConfig(t))
	h.caller = salesCaller()

	_, err := h.svc.Return(ctxWithActor("u-cashier"), fiveUnitReturn())
	var refusal ErrRefundApprovalRequired
	if !errors.As(err, &refusal) {
		t.Fatalf("the cumulative 332.22 crosses the 250.00, want the approval refusal, got %v", err)
	}
	if refusal.AmountCents != 25000 {
		t.Fatalf("the refusal must name the configured 250.00, got %d", refusal.AmountCents)
	}
	if len(refusal.Approvers) != 2 || refusal.Approvers[0] != "sales_manager" || refusal.Approvers[1] != "branch_manager" {
		t.Fatalf("the refusal must name both approver roles in the configuration's order: %v", refusal.Approvers)
	}
	if refusal.RefundCents != 22148 {
		t.Fatalf("the refusal must keep the NEW return's own 221.48, never the cumulative: %d", refusal.RefundCents)
	}
	if len(h.store.returnRows) != 0 || len(h.store.returnLineRows) != 0 || h.tender.refundCalled {
		t.Fatal("a refused return must persist nothing and touch no rail")
	}
	if len(h.rec.rows) != 1 {
		t.Fatalf("want exactly the refusal's audit row, got %v", h.rec.rows)
	}
	row := h.rec.rows[0]
	if row.Action != auditActionRefundApprovalRefused || row.ActorID != "u-cashier" ||
		row.EntityType != "pos.return" || row.EntityID != "order-1" {
		t.Fatalf("the audit row must name the clerk, the sale and the action: %+v", row)
	}
	if row.Extra["amount_cents"] != int64(22148) {
		t.Fatalf("the audit row must keep the asked-for 221.48, not the cumulative: %+v", row.Extra)
	}
	if row.Reason != refundApprovalRequiredError {
		t.Fatalf("the audit row must cite the refusal: %q", row.Reason)
	}
}

// The same new return against a sale with NO earlier return commits: the
// per-return rule still holds, the sale's running total is what moves the
// answer, not the mere existence of a prior slice.
func TestRefundSumsPerSale_NoEarlierReturn_Commits(t *testing.T) {
	h := newHarness()
	seedSaleWorth(h, 44296, 0)
	h.svc = h.svc.WithRoleConfig(defaultConfig(t))
	h.caller = salesCaller()

	out, err := h.svc.Return(ctxWithActor("u-cashier"), fiveUnitReturn())
	if err != nil {
		t.Fatalf("with no earlier return the 221.48 return must commit: %v", err)
	}
	if out.TotalCents != 22148 || len(h.store.returnRows) != 1 || !h.tender.refundCalled {
		t.Fatalf("the return must commit through the rail: %+v", out)
	}
}

// A manager credential in the header commits when the CUMULATIVE crosses,
// though the new return alone sits under the amount: the approval prospect
// reads the prior committed total, so the header is consulted (the resolver
// ran) and the commit is the manager's decision, the record naming the
// approver who decided and the clerk who rang it.
func TestRefundSumsPerSale_CumulativeCross_ManagerHeaderCommits(t *testing.T) {
	h := newHarness()
	seedSaleThenReturn(h)
	h.svc = h.svc.WithRoleConfig(defaultConfig(t))
	src := &fakeApproverSource{}
	src.resolved = qualifyingApprover()
	h.svc = h.svc.WithApproverSource(src)
	h.svc = h.svc.WithUserLinker(func(context.Context, string) (string, bool) {
		return "staff-manager-uuid", true
	})
	h.caller = salesCaller()

	in := fiveUnitReturn()
	in.ApproverCredential = "Bearer approver-credential-value"
	out, err := h.svc.Return(clerkRequestCtx(), in)
	if err != nil {
		t.Fatalf("the manager's credential must commit the crossing cumulative: %v", err)
	}
	if out.TotalCents != 22148 {
		t.Fatalf("return money wrong: %+v", out)
	}
	if src.calls != 1 {
		t.Fatalf("the header must be consulted when the cumulative would cross, resolver calls=%d", src.calls)
	}
	ev, ok := h.store.lastEvent("pos.return.committed")
	if !ok {
		t.Fatal("the committed record must exist")
	}
	if ev["decided_by"] != "staff-manager-uuid" || ev["rang_by"] != "u-harness" {
		t.Fatalf("the commit must be the manager's decision beside the clerk: %v", ev)
	}
	if len(h.rec.rows) != 1 || h.rec.rows[0].Extra["approver_id"] != "staff-manager-uuid" {
		t.Fatalf("the completion row must name the manager as approver: %+v", h.rec.rows)
	}
}

// The credential arm must compare the CUMULATIVE, not the new return's own
// total: a caller whose role alone would approve (the Sales manager),
// carrying a credential that FAILS TO JUDGE (the verifier errors), on a
// return whose own total sits under the amount while the cumulative crosses
// it, must answer the credential's own refusal family. A presented
// credential above the amount must qualify, whoever carried it; the wrong
// comparison skips the arm and commits the return on an unjudged
// credential.
func TestRefundSumsPerSale_CredentialThatFailsToJudge_RefusesOnItsOwnFamily(t *testing.T) {
	h := newHarness()
	seedSaleThenReturn(h)
	h.svc = h.svc.WithRoleConfig(defaultConfig(t))
	src := &fakeApproverSource{}
	src.err = authz.ErrSecondCredentialRefused
	h.svc = h.svc.WithApproverSource(src)
	h.caller = salesManagerCaller() // the role alone would approve

	in := fiveUnitReturn()
	in.ApproverCredential = "Bearer approver-credential-value"
	_, err := h.svc.Return(clerkRequestCtx(), in)
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 401 {
		t.Fatalf("an unjudgeable credential above the amount must refuse on the credential family, got %v", err)
	}
	var approval ErrRefundApprovalRequired
	if errors.As(err, &approval) {
		t.Fatalf("the credential's own family, never the approval 409: %v", err)
	}
	if len(h.store.returnRows) != 0 || h.tender.refundCalled {
		t.Fatal("a refused return must persist nothing and touch no rail")
	}
}

// The credential arm's other half, same comparison: a credential that IS
// judged and does not qualify (a role off the approvers list) leaves the
// standing approval refusal with the configured amount and approvers, even
// for a caller whose own role would approve the crossing cumulative.
func TestRefundSumsPerSale_PresentedCredentialOffTheApproversList_StandsRefused(t *testing.T) {
	h := newHarness()
	seedSaleThenReturn(h)
	h.svc = h.svc.WithRoleConfig(defaultConfig(t))
	src := &fakeApproverSource{}
	unlisted := qualifyingApprover()
	unlisted.RoleID = "inside_sales" // judged fine, off the approvers list
	src.resolved = unlisted
	h.svc = h.svc.WithApproverSource(src)
	h.caller = salesManagerCaller() // the role alone would approve

	in := fiveUnitReturn()
	in.ApproverCredential = "Bearer approver-credential-value"
	_, err := h.svc.Return(clerkRequestCtx(), in)
	var refusal ErrRefundApprovalRequired
	if !errors.As(err, &refusal) {
		t.Fatalf("a presented credential that does not qualify must leave the standing refusal, got %v", err)
	}
	if refusal.AmountCents != 25000 {
		t.Fatalf("the refusal must name the configured 250.00, got %d", refusal.AmountCents)
	}
	if len(refusal.Approvers) != 2 || refusal.Approvers[0] != "sales_manager" || refusal.Approvers[1] != "branch_manager" {
		t.Fatalf("the refusal must name both approver roles: %v", refusal.Approvers)
	}
	if refusal.RefundCents != 22148 {
		t.Fatalf("the refusal must keep the NEW return's own 221.48: %d", refusal.RefundCents)
	}
	if len(h.store.returnRows) != 0 || h.tender.refundCalled {
		t.Fatal("a refused return must persist nothing and touch no rail")
	}
}

// seedTwoUnderAmountLines seeds a sale of two lines each individually under
// the configured amount: the shape where the requested line's ORIGINAL total
// alone sits under it and only the sale's committed prior pushes the
// cumulative over, so the prospect is true through the prior or not at all.
func seedTwoUnderAmountLines(h *harness) {
	h.store.retCtx["order-1"] = returnOrderCtx{
		OrderID: "order-1", OrderNo: "#SO-1", BranchID: "branch-trt", CustomerID: "cust-abc",
		InvoiceID: "inv-1", InvoiceStatus: "PAID", SaleTender: TenderCash,
	}
	h.store.retLines["line-1"] = returnableLine{
		OrderLineID: "line-1", ProductID: "prod-blu", QtySold: 10, UnitPriceCents: 1500, LineTotalCents: 15000,
	}
	h.store.retLines["line-2"] = returnableLine{
		OrderLineID: "line-2", ProductID: "prod-red", QtySold: 10, UnitPriceCents: 1500, LineTotalCents: 15000,
	}
}

// The prospect must read the prior committed total: two lines each
// individually under the amount, the first line's return commits with no
// approval, and the second line's return with the Sales manager's credential
// in the header must consult the resolver (only the prior pushes the
// cumulative over) and commit as the manager's decision. With the prospect
// ignoring the prior the header is inert and the return wrongly refuses.
func TestRefundSumsPerSale_SecondLineUnderAmount_HeaderConsultedThroughThePrior(t *testing.T) {
	h := newHarness()
	seedTwoUnderAmountLines(h)
	h.svc = h.svc.WithRoleConfig(defaultConfig(t))
	src := &fakeApproverSource{}
	src.resolved = qualifyingApprover()
	h.svc = h.svc.WithApproverSource(src)
	h.svc = h.svc.WithUserLinker(func(context.Context, string) (string, bool) {
		return "staff-manager-uuid", true
	})
	h.caller = salesCaller()

	in1 := ReturnInput{
		IdempotencyKey: "ret-key-1", OrderID: "order-1", RefundMethod: RefundCash,
		TillSessionID: "till-1", Restock: false,
		Lines: []ReturnLineInput{{OrderLineID: "line-1", QtyBase: 10, ReasonCode: "DAMAGED"}},
	}
	out1, err := h.svc.Return(ctxWithActor("u-cashier"), in1)
	if err != nil {
		t.Fatalf("the first 150.00 return commits under the amount: %v", err)
	}
	if out1.TotalCents != 15000 {
		t.Fatalf("first return money wrong: %+v", out1)
	}
	// The fake's prior map is not fed by its own inserts, so the leg stages
	// the first return's committed total, the fact the prospect must read.
	h.store.priorSums["order-1"] = 15000

	in2 := ReturnInput{
		IdempotencyKey: "ret-key-2", OrderID: "order-1", RefundMethod: RefundCash,
		TillSessionID: "till-1", Restock: false,
		Lines: []ReturnLineInput{{OrderLineID: "line-2", QtyBase: 10, ReasonCode: "DAMAGED"}},
	}
	in2.ApproverCredential = "Bearer approver-credential-value"
	out2, err := h.svc.Return(clerkRequestCtx(), in2)
	if err != nil {
		t.Fatalf("the manager's credential must commit the crossing cumulative: %v", err)
	}
	if out2.TotalCents != 15000 {
		t.Fatalf("second return money wrong: %+v", out2)
	}
	if src.calls != 1 {
		t.Fatalf("the header must be consulted when only the prior pushes the cumulative over, resolver calls=%d", src.calls)
	}
	ev, ok := h.store.lastEvent("pos.return.committed")
	if !ok || ev["decided_by"] != "staff-manager-uuid" || ev["rang_by"] != "u-harness" {
		t.Fatalf("the commit must be the manager's decision beside the clerk: %v", ev)
	}
	if len(h.rec.rows) != 2 || h.rec.rows[1].Extra["approver_id"] != "staff-manager-uuid" {
		t.Fatalf("the second return's completion row must name the manager as approver: %+v", h.rec.rows)
	}
}
