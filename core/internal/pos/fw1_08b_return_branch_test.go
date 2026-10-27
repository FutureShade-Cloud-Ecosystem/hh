package pos

import (
	"context"
	"errors"
	"testing"
)

// TestFW1_08b_ReturnPostsToBranchWhereRung proves Ruling 137 (FW1-08b):
// a POS return posts to the branch where the return is rung (pos_returns.branch_id),
// because the stock comes back there and the refund leaves that drawer.
func TestFW1_08b_ReturnPostsToBranchWhereRung(t *testing.T) {
	t.Run("explicit_branch_on_return", func(t *testing.T) {
		h := newHarness()
		h.store.retCtx["order-trt-1"] = returnOrderCtx{
			OrderID:       "order-trt-1",
			OrderNo:       "#SO-TRT-1",
			BranchID:      "branch-trt",
			CustomerID:    "cust-abc",
			InvoiceID:     "inv-trt-1",
			InvoiceStatus: "PAID",
			SaleTender:    TenderCash,
		}
		h.store.retLines["line-restock"] = returnableLine{
			OrderLineID:    "line-restock",
			ProductID:      "prod-blu",
			QtySold:        5,
			UnitPriceCents: 1000,
			LineTotalCents: 5000,
			TaxCents:       650,
		}
		h.store.retLines["line-damaged"] = returnableLine{
			OrderLineID:    "line-damaged",
			ProductID:      "prod-grn",
			QtySold:        5,
			UnitPriceCents: 1000,
			LineTotalCents: 5000,
			TaxCents:       650,
		}
		h.tender.sessionBranch = "branch-kgn"

		in := ReturnInput{
			IdempotencyKey: "ret-fw1-08b-explicit",
			OrderID:        "order-trt-1",
			BranchID:       "branch-kgn",
			RefundMethod:   RefundCash,
			TillSessionID:  "till-kgn-1",
			Restock:        true,
			Lines: []ReturnLineInput{
				{OrderLineID: "line-restock", QtyBase: 2, Disposition: DispositionRestock, ReasonCode: "CHANGED_MIND"},
				{OrderLineID: "line-damaged", QtyBase: 1, Disposition: DispositionDamaged, ReasonCode: "DAMAGED"},
			},
		}
		if _, err := h.svc.Return(context.Background(), in); err != nil {
			t.Fatalf("Return: %v", err)
		}

		if len(h.store.returnRows) != 1 {
			t.Fatalf("expected 1 return row, got %d", len(h.store.returnRows))
		}
		if got := h.store.returnRows[0].BranchID; got != "branch-kgn" {
			t.Errorf("pos_returns.branch_id = %q, want %q (branch where return was rung)", got, "branch-kgn")
		}
		if len(h.restock.calls) != 1 || h.restock.calls[0].BranchID != "branch-kgn" {
			t.Errorf("RestockForReturnTx branch = %+v, want branch-kgn", h.restock.calls)
		}
		if h.tender.lastRefund.BranchID != "branch-kgn" {
			t.Errorf("RefundTender branch = %q, want %q", h.tender.lastRefund.BranchID, "branch-kgn")
		}
		for _, topic := range []string{"pos.return.committed", "order.returned", "pos.return.written_off"} {
			ev, ok := h.store.lastEvent(topic)
			if !ok {
				t.Fatalf("missing outbox event %s", topic)
			}
			if got, _ := ev["branch_id"].(string); got != "branch-kgn" {
				t.Errorf("%s payload branch_id = %q, want %q", topic, got, "branch-kgn")
			}
		}
	})

	t.Run("till_session_branch_when_return_branch_omitted", func(t *testing.T) {
		h := newHarness()
		seedReturnable(h) // order-1 sold at branch-trt
		h.tender.sessionBranch = "branch-kgn"

		in := returnInput()
		in.IdempotencyKey = "ret-fw1-08b-session"
		in.Lines = []ReturnLineInput{
			{OrderLineID: "line-1", QtyBase: 2, Disposition: DispositionRestock, ReasonCode: "CHANGED_MIND"},
		}
		if _, err := h.svc.Return(context.Background(), in); err != nil {
			t.Fatalf("Return: %v", err)
		}

		if len(h.store.returnRows) != 1 {
			t.Fatalf("expected 1 return row, got %d", len(h.store.returnRows))
		}
		if got := h.store.returnRows[0].BranchID; got != "branch-kgn" {
			t.Errorf("pos_returns.branch_id = %q, want %q (open till session branch)", got, "branch-kgn")
		}
		if len(h.restock.calls) != 1 || h.restock.calls[0].BranchID != "branch-kgn" {
			t.Errorf("RestockForReturnTx branch = %+v, want branch-kgn", h.restock.calls)
		}
		if h.tender.lastRefund.BranchID != "branch-kgn" {
			t.Errorf("RefundTender branch = %q, want %q", h.tender.lastRefund.BranchID, "branch-kgn")
		}
		for _, topic := range []string{"pos.return.committed", "order.returned"} {
			ev, ok := h.store.lastEvent(topic)
			if !ok {
				t.Fatalf("missing outbox event %s", topic)
			}
			if got, _ := ev["branch_id"].(string); got != "branch-kgn" {
				t.Errorf("%s payload branch_id = %q, want %q", topic, got, "branch-kgn")
			}
		}
	})

	t.Run("return_across_posts_to_branch_where_rung", func(t *testing.T) {
		h := newHarness()
		h.store.retCtx["order-a"] = returnOrderCtx{
			OrderID: "order-a", OrderNo: "#SO-A", BranchID: "branch-trt", CustomerID: "cust-abc",
			InvoiceID: "inv-a", InvoiceStatus: "PAID", SaleTender: TenderCash,
		}
		h.store.retCtx["order-b"] = returnOrderCtx{
			OrderID: "order-b", OrderNo: "#SO-B", BranchID: "branch-trt", CustomerID: "cust-abc",
			InvoiceID: "inv-b", InvoiceStatus: "PAID", SaleTender: TenderCash,
		}
		h.store.retLines["line-a"] = returnableLine{
			OrderLineID: "line-a", ProductID: "prod-blu", QtySold: 4,
			UnitPriceCents: 1000, LineTotalCents: 4000, TaxCents: 520,
		}
		h.store.retLines["line-b"] = returnableLine{
			OrderLineID: "line-b", ProductID: "prod-grn", QtySold: 4,
			UnitPriceCents: 1000, LineTotalCents: 4000, TaxCents: 520,
		}
		h.tender.sessionBranch = "branch-kgn"

		in := ReturnAcrossInput{
			IdempotencyKey: "ret-across-fw1-08b",
			CustomerID:     "cust-abc",
			BranchID:       "branch-kgn",
			RefundMethod:   RefundCash,
			TillSessionID:  "till-kgn-1",
			Restock:        true,
			Orders: []ReturnAcrossOrderInput{
				{OrderID: "order-a", Lines: []ReturnLineInput{{OrderLineID: "line-a", QtyBase: 1, Disposition: DispositionRestock, ReasonCode: "CHANGED_MIND"}}},
				{OrderID: "order-b", Lines: []ReturnLineInput{{OrderLineID: "line-b", QtyBase: 1, Disposition: DispositionDamaged, ReasonCode: "DAMAGED"}}},
			},
		}
		if _, err := h.svc.ReturnAcross(context.Background(), in); err != nil {
			t.Fatalf("ReturnAcross: %v", err)
		}
		if len(h.store.returnRows) != 2 {
			t.Fatalf("expected 2 return rows, got %d", len(h.store.returnRows))
		}
		for i, rr := range h.store.returnRows {
			if rr.BranchID != "branch-kgn" {
				t.Errorf("returnRows[%d].BranchID = %q, want %q", i, rr.BranchID, "branch-kgn")
			}
		}
		for _, topic := range []string{"pos.return.committed", "order.returned", "pos.return.written_off"} {
			ev, ok := h.store.lastEvent(topic)
			if !ok {
				t.Fatalf("missing outbox event %s", topic)
			}
			if got, _ := ev["branch_id"].(string); got != "branch-kgn" {
				t.Errorf("%s payload branch_id = %q, want %q", topic, got, "branch-kgn")
			}
		}
	})

	t.Run("rung_branch_outside_caller_reach_refused_403", func(t *testing.T) {
		h := newHarness()
		seedReturnable(h) // order-1 sold at branch-trt
		h.caller = identityBranchScoped("u-trt-only", "INSIDE_SALES", "branch-trt")
		in := returnInput()
		in.BranchID = "branch-kgn"
		_, err := h.svc.Return(context.Background(), in)
		var ce clientError
		if !errors.As(err, &ce) || ce.status != 403 {
			t.Fatalf("want 403 clientError for foreign rung branch, got %v", err)
		}
	})
}
