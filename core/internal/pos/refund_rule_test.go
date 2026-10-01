package pos

// refund_rule_test.go, the refund rule reading the dealer's configuration
// (X7): the amount without approval and the approvers above it are the
// configuration's numbers, integer cents against integer cents, and a
// return above the amount from a role off the approvers list is refused
// with the amount and the approver roles named, nothing persisted, no rail
// touched, and an audit row naming the clerk, the sale and the amount.
// Changing the configuration's amount changes the answer with no code
// change: the last test rewrites the default document through Validate.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// seedSaleWorth installs one committed cash sale whose whole-return total is
// subtotalCents with the given tax, so a test pins the rule to exact cents.
func seedSaleWorth(h *harness, subtotalCents, taxCents int64) {
	h.store.retCtx["order-1"] = returnOrderCtx{
		OrderID: "order-1", OrderNo: "#SO-1", BranchID: "branch-trt", CustomerID: "cust-abc",
		InvoiceID: "inv-1", InvoiceStatus: "PAID", SaleTender: TenderCash,
	}
	h.store.retLines["line-1"] = returnableLine{
		OrderLineID: "line-1", ProductID: "prod-blu", QtySold: 10,
		UnitPriceCents: subtotalCents / 10, LineTotalCents: subtotalCents, TaxCents: taxCents,
	}
}

func wholeReturnInput() ReturnInput {
	return ReturnInput{
		IdempotencyKey: "ret-key-1", OrderID: "order-1", RefundMethod: RefundCash,
		TillSessionID: "till-1", Restock: false,
		Lines: []ReturnLineInput{{OrderLineID: "line-1", QtyBase: 10, ReasonCode: "DAMAGED"}},
	}
}

// A 300.00 cash return as Sales is refused with the configured 250.00 and
// both approver roles named, nothing persists, no rail is touched, and the
// audit row names the clerk, the sale and the amount.
func TestRefundRule_AboveAmount_RefusedWithTheAmountAndTheApprovers(t *testing.T) {
	h := newHarness()
	seedSaleWorth(h, 30000, 0)
	h.svc = h.svc.WithRoleConfig(defaultConfig(t))
	h.caller = salesCaller()

	_, err := h.svc.Return(ctxWithActor("u-cashier"), wholeReturnInput())
	var refusal ErrRefundApprovalRequired
	if !errors.As(err, &refusal) {
		t.Fatalf("want the approval refusal, got %v", err)
	}
	if refusal.AmountCents != 25000 {
		t.Fatalf("the refusal must name the configured 250.00, got %d", refusal.AmountCents)
	}
	if len(refusal.Approvers) != 2 || refusal.Approvers[0] != "sales_manager" || refusal.Approvers[1] != "branch_manager" {
		t.Fatalf("the refusal must name both approver roles in the configuration's order: %v", refusal.Approvers)
	}
	if len(h.store.returnRows) != 0 || h.tender.refundCalled {
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
	if row.Extra["amount_cents"] != int64(30000) {
		t.Fatalf("the audit row must keep the refused amount: %+v", row.Extra)
	}
	if row.Reason != refundApprovalRequiredError {
		t.Fatalf("the audit row must cite the refusal: %q", row.Reason)
	}
}

// At the amount needs no approver: the boundary is integer-exact.
func TestRefundRule_AtTheAmount_NeedsNoApprover(t *testing.T) {
	h := newHarness()
	seedSaleWorth(h, 25000, 0)
	h.svc = h.svc.WithRoleConfig(defaultConfig(t))
	h.caller = salesCaller()

	out, err := h.svc.Return(context.Background(), wholeReturnInput())
	if err != nil {
		t.Fatalf("a return at the amount must commit: %v", err)
	}
	if out.TotalCents != 25000 {
		t.Fatalf("return money wrong: %+v", out)
	}
}

// A role on the configured approvers list commits the same return with no
// second credential and no refusal row.
func TestRefundRule_ListedRole_CommitsWithNoSecondCredential(t *testing.T) {
	h := newHarness()
	seedSaleWorth(h, 30000, 0)
	h.svc = h.svc.WithRoleConfig(defaultConfig(t))
	h.caller = salesManagerCaller()

	out, err := h.svc.Return(context.Background(), wholeReturnInput())
	if err != nil {
		t.Fatalf("a listed role must commit above the amount: %v", err)
	}
	if out.TotalCents != 30000 || !h.tender.refundCalled {
		t.Fatalf("the return must commit through the rail: %+v", out)
	}
	for _, row := range h.rec.rows {
		if row.Action == auditActionRefundApprovalRefused {
			t.Fatal("no refusal row may exist for a listed role")
		}
	}
}

// Lowering the configured amount to 100.00 through a validated modified
// document flips the answer for the same return, no code change.
func TestRefundRule_ConfiguredAmountChanged_AnswerChanges(t *testing.T) {
	h := newHarness()
	seedSaleWorth(h, 20000, 0) // the 200.00 return the default amount allows
	h.svc = h.svc.WithRoleConfig(modifiedDefault(t, roleconfig.SettingRefundAmountWithoutApproval, 10000))
	h.caller = salesCaller()

	_, err := h.svc.Return(context.Background(), wholeReturnInput())
	var refusal ErrRefundApprovalRequired
	if !errors.As(err, &refusal) {
		t.Fatalf("the same return must refuse under the lowered amount, got %v", err)
	}
	if refusal.AmountCents != 10000 {
		t.Fatalf("the refusal must name the configured 100.00, got %d", refusal.AmountCents)
	}
}

// The 409 mapping: the Error envelope whose error string discriminates the
// approval refusal, the approval body carrying the amount and the approver
// role keys, exactly the contract's PosRefundApprovalRequired shape.
func TestRefundRule_Handler_MapsThe409Shape(t *testing.T) {
	h := newHarness()
	seedSaleWorth(h, 30000, 0)
	h.svc = h.svc.WithRoleConfig(defaultConfig(t))
	h.caller = salesCaller()
	handler := NewHandler(h.svc, slog.Default())

	in := wholeReturnInput()
	body, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	_, retErr := h.svc.Return(ctxWithActor("u-cashier"), in)
	if retErr == nil {
		t.Fatal("want the refusal")
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pos/returns", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.writeErr(rec, req, retErr)

	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Error    string `json:"error"`
		Approval *struct {
			AmountCents int64    `json:"amount_cents"`
			Approvers   []string `json:"approvers"`
		} `json:"approval"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Error != refundApprovalRequiredError {
		t.Fatalf("the error string must discriminate the refusal: %q", got.Error)
	}
	if got.Approval == nil || got.Approval.AmountCents != 25000 ||
		len(got.Approval.Approvers) != 2 ||
		got.Approval.Approvers[0] != "sales_manager" || got.Approval.Approvers[1] != "branch_manager" {
		t.Fatalf("the approval body must name the amount and both roles: %+v", got.Approval)
	}
}
