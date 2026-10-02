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
