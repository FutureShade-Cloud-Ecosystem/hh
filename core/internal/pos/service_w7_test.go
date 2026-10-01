package pos

// service_w7_test.go, W7 counter-v2 unit tests: the COD policy gate + fulfilment lock, deposit math,
// the return over-return guard + the three refund rails + restock dispatch + tax proportionality,
// parked lifecycle (re-priced on resume by construction, the payload carries NO price), and the
// pricing order's list-vs-offered snapshot. DB-free (the fakeStore pattern of service_test.go).

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

// ctxWithActor stamps the authed staff actor exactly as the RequireStaff guard does.
func ctxWithActor(id string) context.Context {
	return identity.ContextWithStaffUserID(context.Background(), id)
}

// -- COD: policy gate + unpaid-at-ring + fulfilment lock ---------------------

func codInput() SaleInput {
	in := baseInput()
	in.SaleType = SaleCOD
	in.Tender = TenderCash // the SETTLEMENT method collected later, nothing charges at ring
	return in
}

func TestSale_COD_PolicyDisabled_422(t *testing.T) {
	h := newHarness()
	h.store.policies[""] = PosPolicy{CODEnabled: false}
	_, err := h.svc.Sale(context.Background(), codInput())
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 422 {
		t.Fatalf("want the 422 policy refusal, got %v", err)
	}
	if len(h.store.orders) != 0 {
		t.Fatal("a policy-refused COD sale must not open the tx")
	}
}

func TestSale_COD_BranchOverride_WinsOverOrgDefault(t *testing.T) {
	h := newHarness()
	h.store.policies[""] = PosPolicy{CODEnabled: false}
	h.store.policies["branch-trt"] = PosPolicy{BranchID: "branch-trt", CODEnabled: true}
	if _, err := h.svc.Sale(context.Background(), codInput()); err != nil {
		t.Fatalf("branch override must allow COD: %v", err)
	}
	// And the inverse: org on, branch off.
	h2 := newHarness()
	h2.store.policies["branch-trt"] = PosPolicy{BranchID: "branch-trt", CODEnabled: false}
	if _, err := h2.svc.Sale(context.Background(), codInput()); err == nil {
		t.Fatal("branch-off must refuse COD even with the org default on")
	}
}

func TestSale_COD_UnpaidAtRing_InvoiceFinalized_FulfilmentLocked(t *testing.T) {
	h := newHarness()
	out, err := h.svc.Sale(context.Background(), codInput())
	if err != nil {
		t.Fatalf("cod sale: %v", err)
	}
	if h.tender.called {
		t.Fatal("COD is unpaid at ring, TakeTender must NOT be called")
	}
	inv := h.store.invoices[0]
	if inv.Status != "FINALIZED" || inv.BalanceCents != out.TotalCents {
		t.Fatalf("COD invoice must FINALIZE for the full balance: %+v", inv)
	}
	// COD is due on demand, today, never the customer's terms.
	if inv.DueDate != "2026-06-29" {
		t.Fatalf("COD due date must be today, got %s", inv.DueDate)
	}
	// The invoice number is printable immediately (customers pay against it).
	if out.InvoiceNo == "" {
		t.Fatal("COD ring must surface the invoice number")
	}
	// Fulfilment LOCK: no stock decrement and no order.fulfilled event at ring.
	for _, ev := range h.store.events {
		if ev == "order.fulfilled" {
			t.Fatal("COD must NOT fulfil at ring")
		}
	}
	if m, ok := h.store.orders[0].Metadata["cod_posture"]; !ok || m != "AWAITING_PAYMENT" {
		t.Fatalf("COD order must carry the AWAITING_PAYMENT posture: %+v", h.store.orders[0].Metadata)
	}
	// AR event carries the full total (GL posts with the EXISTING rules).
	if _, ok := h.store.lastEvent("ar.invoice.finalized"); !ok {
		t.Fatal("COD ring must emit ar.invoice.finalized")
	}
}

func TestSale_COD_FulfilmentField_MapsAndValidates(t *testing.T) {
	h := newHarness()
	in := codInput()
	in.Fulfilment = "DELIVERY"
	if _, err := h.svc.Sale(context.Background(), in); err != nil {
		t.Fatalf("cod delivery: %v", err)
	}
	if h.store.orders[0].Fulfillment != "DELIVERY" {
		t.Fatalf("COD fulfilment not mapped: %q", h.store.orders[0].Fulfillment)
	}
	// Default is PICKUP, cash on DEMAND, never hard-mapped to delivery.
	h2 := newHarness()
	if _, err := h2.svc.Sale(context.Background(), codInput()); err != nil {
		t.Fatalf("cod default: %v", err)
	}
	if h2.store.orders[0].Fulfillment != "PICKUP" {
		t.Fatalf("COD default fulfilment must be PICKUP: %q", h2.store.orders[0].Fulfillment)
	}
	h3 := newHarness()
	bad := codInput()
	bad.Fulfilment = "TELEPORT"
	if _, err := h3.svc.Sale(context.Background(), bad); !errors.Is(err, errBadFulfilment) {
		t.Fatalf("want errBadFulfilment, got %v", err)
	}
}

// -- deposits ----------------------------------------------------------------

func depositInput() SaleInput {
	in := baseInput()
	in.Tender = TenderAccount
	in.DepositCents = 2000
	in.DepositMethod = TenderCash
	return in
}

func TestSale_Deposit_PartiallyPaid_AllocationAndSubledger(t *testing.T) {
	h := newHarness()
	out, err := h.svc.Sale(context.Background(), depositInput())
	if err != nil {
		t.Fatalf("deposit sale: %v", err)
	}
	// total = 10000 + 1300 = 11300; deposit 2000 → balance 9300.
	inv := h.store.invoices[0]
	if inv.Status != "PARTIALLY_PAID" || inv.BalanceCents != 9300 {
		t.Fatalf("want PARTIALLY_PAID/9300, got %s/%d", inv.Status, inv.BalanceCents)
	}
	if !h.tender.called || h.tender.last.AmountCents != 2000 || h.tender.last.Method != TenderCash {
		t.Fatalf("deposit must capture through TakeTender: %+v", h.tender.last)
	}
	if len(h.store.allocations) != 1 || h.store.allocations[0].amount != 2000 {
		t.Fatalf("deposit allocation wrong: %+v", h.store.allocations)
	}
	// Subledger: INVOICE(total) then PAYMENT(deposit) → running balance total − deposit.
	if len(h.store.custTxns) != 2 || h.store.custTxns[0].TxnType != "INVOICE" || h.store.custTxns[1].TxnType != "PAYMENT" {
		t.Fatalf("want INVOICE+PAYMENT txns, got %+v", h.store.custTxns)
	}
	if h.store.custTxns[1].BalanceAfter != 9300 {
		t.Fatalf("running balance after deposit must be 9300, got %d", h.store.custTxns[1].BalanceAfter)
	}
	// Events: ar.invoice.finalized (FULL total) + payment.received (deposit).
	ar, _ := h.store.lastEvent("ar.invoice.finalized")
	if ar["total_cents"] != int64(11300) {
		t.Fatalf("ar.invoice.finalized must carry the full total: %v", ar["total_cents"])
	}
	pr, ok := h.store.lastEvent("payment.received")
	if !ok || pr["amount_cents"] != int64(2000) {
		t.Fatalf("payment.received must carry the deposit: %v", pr)
	}
	if out.DepositCents != 2000 {
		t.Fatalf("deposit not surfaced on the response: %d", out.DepositCents)
	}
}

func TestSale_Deposit_CreditGate_RunsOnRemainder(t *testing.T) {
	h := newHarness()
	if _, err := h.svc.Sale(context.Background(), depositInput()); err != nil {
		t.Fatalf("deposit sale: %v", err)
	}
	if h.credit.lastAmount != 9300 {
		t.Fatalf("credit gate must run on total − deposit (9300), got %d", h.credit.lastAmount)
	}
}

func TestSale_Deposit_Bounds(t *testing.T) {
	h := newHarness()
	in := depositInput()
	in.DepositCents = 11300 // == total: just ring it paid
	if _, err := h.svc.Sale(context.Background(), in); !errors.Is(err, errDepositTooLarge) {
		t.Fatalf("deposit == total must reject, got %v", err)
	}
	in = depositInput()
	in.Tender = TenderCash // paid at ring, no deposit rides
	if _, err := h.svc.Sale(context.Background(), in); !errors.Is(err, errDepositOnPaid) {
		t.Fatalf("deposit on a paid tender must reject, got %v", err)
	}
	in = depositInput()
	in.DepositMethod = "IOU"
	if _, err := h.svc.Sale(context.Background(), in); !errors.Is(err, errBadDepositMethod) {
		t.Fatalf("bad deposit method must reject, got %v", err)
	}
}

func TestSale_Allocation_StampsAllocatedBy(t *testing.T) {
	h := newHarness()
	ctx := ctxWithActor("u-cashier")
	if _, err := h.svc.Sale(ctx, baseInput()); err != nil {
		t.Fatalf("sale: %v", err)
	}
	if len(h.store.allocations) != 1 || h.store.allocations[0].allocatedBy != "u-cashier" {
		t.Fatalf("allocation must stamp allocated_by (Q-044): %+v", h.store.allocations)
	}
}

// -- returns -----------------------------------------------------------------

// seedReturnable installs one committed cash sale: 10 units @ $10.00, line total $100.00, tax $13.00.
func seedReturnable(h *harness) {
	h.store.retCtx["order-1"] = returnOrderCtx{
		OrderID: "order-1", OrderNo: "#SO-1", BranchID: "branch-trt", CustomerID: "cust-abc",
		InvoiceID: "inv-1", InvoiceStatus: "PAID", SaleTender: TenderCash,
	}
	h.store.retLines["line-1"] = returnableLine{
		OrderLineID: "line-1", ProductID: "prod-blu", QtySold: 10,
		UnitPriceCents: 1000, LineTotalCents: 10000, TaxCents: 1300,
	}
}

func returnInput() ReturnInput {
	return ReturnInput{
		IdempotencyKey: "ret-key-1", OrderID: "order-1", RefundMethod: RefundCash,
		TillSessionID: "till-1", Restock: true,
		Lines: []ReturnLineInput{{OrderLineID: "line-1", QtyBase: 4, ReasonCode: "DAMAGED"}},
	}
}

func TestReturn_OverReturn_Guarded_NothingPersists(t *testing.T) {
	h := newHarness()
	seedReturnable(h)
	in := returnInput()
	in.Lines[0].QtyBase = 11
	_, err := h.svc.Return(context.Background(), in)
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 422 {
		t.Fatalf("want the 422 over-return guard, got %v", err)
	}
	if len(h.store.returnRows) != 0 || len(h.store.returnLineRows) != 0 || h.tender.refundCalled {
		t.Fatal("an over-return must persist nothing and touch no rail")
	}
}

func TestReturn_OverReturn_AcrossVisits_SecondPartialGuarded(t *testing.T) {
	h := newHarness()
	seedReturnable(h)
	if _, err := h.svc.Return(context.Background(), returnInput()); err != nil { // 4 of 10
		t.Fatalf("first return: %v", err)
	}
	in := returnInput()
	in.IdempotencyKey = "ret-key-2"
	in.Lines[0].QtyBase = 7 // only 6 remain
	if _, err := h.svc.Return(context.Background(), in); err == nil {
		t.Fatal("returning 7 with 6 remaining must 422")
	}
}

func TestReturn_Cash_TillRail_PaidOutSeam(t *testing.T) {
	h := newHarness()
	seedReturnable(h)
	out, err := h.svc.Return(context.Background(), returnInput())
	if err != nil {
		t.Fatalf("cash return: %v", err)
	}
	// money = 4 × 1000 = 4000; tax = round(1300 × 4/10) = 520; total 4520.
	if out.SubtotalCents != 4000 || out.TaxCents != 520 || out.TotalCents != 4520 {
		t.Fatalf("return money wrong: %+v", out)
	}
	if !h.tender.refundCalled || h.tender.lastRefund.Method != till.MethodCash ||
		h.tender.lastRefund.AmountCents != 4520 || h.tender.lastRefund.TillSessionID != "till-1" {
		t.Fatalf("cash rail must go through till.RefundTender: %+v", h.tender.lastRefund)
	}
	if out.RefundPaymentID != "refund-pay-1" || h.store.refundPayments[out.ReturnID] != "refund-pay-1" {
		t.Fatalf("REFUNDED payments row not linked: %+v", out)
	}
	ev, ok := h.store.lastEvent("pos.return.committed")
	if !ok || ev["refund_method"] != RefundCash || ev["total_cents"] != int64(4520) {
		t.Fatalf("pos.return.committed wrong: %v", ev)
	}
	if !h.rec.has("RETURN_COMPLETED") {
		t.Fatal("committed return must audit RETURN_COMPLETED")
	}
}

func TestReturn_Cash_NeedsOpenTillSession(t *testing.T) {
	h := newHarness()
	seedReturnable(h)
	in := returnInput()
	in.TillSessionID = ""
	var nt ErrNoOpenTill
	if _, err := h.svc.Return(context.Background(), in); !errors.As(err, &nt) {
		t.Fatalf("want ErrNoOpenTill, got %v", err)
	}
}

func TestReturn_Card_ReversesOriginalCapture(t *testing.T) {
	h := newHarness()
	seedReturnable(h)
	oc := h.store.retCtx["order-1"]
	oc.SaleTender = TenderCard
	oc.GatewayRef = "rp_orig_capture"
	h.store.retCtx["order-1"] = oc

	in := returnInput()
	in.RefundMethod = RefundCard
	in.TillSessionID = ""
	out, err := h.svc.Return(context.Background(), in)
	if err != nil {
		t.Fatalf("card return: %v", err)
	}
	if !h.refunder.called || h.refunder.lastRef != "rp_orig_capture" || h.refunder.lastCents != 4520 {
		t.Fatalf("card refund must reverse the ORIGINAL capture: %+v", h.refunder)
	}
	// The till writes the REFUNDED row carrying the REVERSAL's ref.
	if h.tender.lastRefund.Method != till.MethodCard || h.tender.lastRefund.GatewayRef != "rp_rev_1" {
		t.Fatalf("REFUNDED card row wrong: %+v", h.tender.lastRefund)
	}
	if out.RefundPaymentID == "" {
		t.Fatal("card return must link the REFUNDED payments row")
	}
}

func TestReturn_Card_NoOriginalRef_422(t *testing.T) {
	h := newHarness()
	seedReturnable(h) // original was CASH, no gateway ref
	in := returnInput()
	in.RefundMethod = RefundCard
	if _, err := h.svc.Return(context.Background(), in); !errors.Is(err, errCardRefundNoRef) {
		t.Fatalf("want errCardRefundNoRef, got %v", err)
	}
}

func TestReturn_AccountCredit_OffsetsInvoiceThenStoreCredit(t *testing.T) {
	h := newHarness()
	seedReturnable(h)
	oc := h.store.retCtx["order-1"]
	oc.InvoiceStatus = "PARTIALLY_PAID"
	h.store.retCtx["order-1"] = oc
	h.store.invoiceBalance["inv-1"] = 3000 // only 3000 still open on the invoice
	h.store.customers["cust-abc"] = customerRec{ID: "cust-abc", CustomerType: "CONTRACTOR", OpenBalanceCents: 5000}

	in := returnInput()
	in.RefundMethod = RefundAccountCredit
	in.TillSessionID = ""
	out, err := h.svc.Return(context.Background(), in)
	if err != nil {
		t.Fatalf("account-credit return: %v", err)
	}
	if h.tender.refundCalled || h.refunder.called {
		t.Fatal("account credit never touches the till or the card rail")
	}
	// total 4520: 3000 offsets the invoice, 1520 carries as store credit.
	if len(h.store.creditApplies) != 1 || h.store.creditApplies[0].applied != 3000 {
		t.Fatalf("invoice offset wrong: %+v", h.store.creditApplies)
	}
	if h.store.invoiceBalance["inv-1"] != 0 {
		t.Fatalf("invoice balance must reach 0, got %d", h.store.invoiceBalance["inv-1"])
	}
	if len(h.store.custTxns) != 2 {
		t.Fatalf("want 2 CREDIT_MEMO rows (offset + remainder), got %+v", h.store.custTxns)
	}
	if h.store.custTxns[0].TxnType != "CREDIT_MEMO" || h.store.custTxns[0].AmountCents != -3000 ||
		h.store.custTxns[1].AmountCents != -1520 || h.store.custTxns[1].BalanceAfter != 5000-4520 {
		t.Fatalf("credit memos wrong: %+v", h.store.custTxns)
	}
	if out.RefundPaymentID != "" {
		t.Fatal("account credit writes no payments row")
	}
}

func TestReturn_Restock_DispatchesSeamAndEmitsOrderReturned(t *testing.T) {
	h := newHarness()
	seedReturnable(h)
	if _, err := h.svc.Return(context.Background(), returnInput()); err != nil {
		t.Fatalf("return: %v", err)
	}
	if len(h.restock.calls) != 1 {
		t.Fatalf("restock seam not dispatched: %+v", h.restock.calls)
	}
	rc := h.restock.calls[0]
	if rc.BranchID != "branch-trt" || len(rc.Lines) != 1 || rc.Lines[0].ProductID != "prod-blu" || rc.Lines[0].QtyBase != 4 {
		t.Fatalf("restock input wrong: %+v", rc)
	}
	if _, ok := h.store.lastEvent("order.returned"); !ok {
		t.Fatal("restocked return must emit order.returned")
	}
	// No restock → no stock event, no seam call.
	h2 := newHarness()
	seedReturnable(h2)
	in := returnInput()
	in.Restock = false
	if _, err := h2.svc.Return(context.Background(), in); err != nil {
		t.Fatalf("no-restock return: %v", err)
	}
	if len(h2.restock.calls) != 0 {
		t.Fatal("restock=false must not touch the seam")
	}
	if _, ok := h2.store.lastEvent("order.returned"); ok {
		t.Fatal("restock=false must not emit order.returned")
	}
}

func TestReturn_TaxProportional_SumNeverExceedsOriginal(t *testing.T) {
	h := newHarness()
	seedReturnable(h)
	// 3 + 3 + 4 (full close-out), Σ tax must land EXACTLY on the original 1300, never above.
	var taxSum int64
	for i, qty := range []float64{3, 3, 4} {
		in := returnInput()
		in.IdempotencyKey = "ret-key-" + itoa(i+1)
		in.Lines[0].QtyBase = qty
		out, err := h.svc.Return(context.Background(), in)
		if err != nil {
			t.Fatalf("return %d: %v", i+1, err)
		}
		taxSum += out.TaxCents
	}
	if taxSum != 1300 {
		t.Fatalf("Σ refunded tax must equal the original 1300, got %d", taxSum)
	}
	// And the money zeroes out exactly.
	var moneySum int64
	for _, rl := range h.store.returnLineRows {
		moneySum += rl.LineTotalCents
	}
	if moneySum != 10000 {
		t.Fatalf("Σ refunded money must equal the original 10000, got %d", moneySum)
	}
}

// -- B2: partial-qty refunds must be PROPORTIONAL (unit_price_cents is per price-UOM, qty_base is
// base-UOM, multiplying them mixes units whenever the two UOMs differ) --

// seedMixedUOMReturnable installs a committed sale whose price UOM ≠ base UOM: 10 SF entered / 41 PC
// base / 300¢ per SF → line total 3000¢, tax 390¢ (the seeded-catalog shape, scaled down).
func seedMixedUOMReturnable(h *harness) {
	h.store.retCtx["order-1"] = returnOrderCtx{
		OrderID: "order-1", OrderNo: "#SO-1", BranchID: "branch-trt", CustomerID: "cust-abc",
		InvoiceID: "inv-1", InvoiceStatus: "PAID", SaleTender: TenderCash,
	}
	h.store.retLines["line-1"] = returnableLine{
		OrderLineID: "line-1", ProductID: "prod-blu", QtySold: 41, // base qty (PC)
		UnitPriceCents: 300, // per PRICE UOM (SF), NOT per PC
		LineTotalCents: 3000, TaxCents: 390,
	}
}

func TestReturn_Partial_MixedUOM_RefundsProportionally(t *testing.T) {
	h := newHarness()
	seedMixedUOMReturnable(h)
	in := returnInput()
	in.Lines[0].QtyBase = 20 // 20 of 41 PC
	out, err := h.svc.Return(context.Background(), in)
	if err != nil {
		t.Fatalf("partial mixed-UOM return: %v", err)
	}
	// round(3000 × 20/41) = 1463, NOT qty × unit price (20 × 300 = 6000, which the old code capped
	// to the full 3000 and over-refunded a half-line return by 2×).
	if out.SubtotalCents != 1463 {
		t.Fatalf("partial refund must be proportional: want 1463, got %d", out.SubtotalCents)
	}
	if tax := int64(190); out.TaxCents != tax { // round(390 × 20/41)
		t.Fatalf("tax proration: want %d, got %d", tax, out.TaxCents)
	}
}

func TestReturn_Partial_MixedUOM_FeeComputesOnCorrectedSubtotal(t *testing.T) {
	h := newHarness()
	seedMixedUOMReturnable(h)
	h.store.customers["cust-abc"] = customerRec{ID: "cust-abc", CustomerType: "CONTRACTOR"}
	in := returnInput()
	in.Lines[0].QtyBase = 20
	in.RefundMethod = RefundAccountCredit
	in.TillSessionID = ""
	in.FeeBps = 1000 // 10%
	out, err := h.svc.Return(context.Background(), in)
	if err != nil {
		t.Fatalf("fee return: %v", err)
	}
	// The fee derives from the COMMITTED subtotal: round-half-up(1463 × 10%) = 146, not 300/600.
	if out.SubtotalCents != 1463 || out.FeeCents != 146 {
		t.Fatalf("fee must follow the corrected subtotal: subtotal=%d fee=%d", out.SubtotalCents, out.FeeCents)
	}
}

func TestReturn_Partial_MixedUOM_CumulativePartialsSumExactly(t *testing.T) {
	h := newHarness()
	seedMixedUOMReturnable(h)
	// 20 + 10 + 11 (the last closes the line via the exact-remainder branch): Σ must land EXACTLY on
	// the original 3000/390, never over, never under.
	var moneySum, taxSum int64
	for i, qty := range []float64{20, 10, 11} {
		in := returnInput()
		in.IdempotencyKey = "ret-mixed-" + itoa(i+1)
		in.Lines[0].QtyBase = qty
		out, err := h.svc.Return(context.Background(), in)
		if err != nil {
			t.Fatalf("partial %d: %v", i+1, err)
		}
		moneySum += out.SubtotalCents
		taxSum += out.TaxCents
	}
	if moneySum != 3000 {
		t.Fatalf("Σ refunded money must equal the original 3000, got %d", moneySum)
	}
	if taxSum != 390 {
		t.Fatalf("Σ refunded tax must equal the original 390, got %d", taxSum)
	}
	// The line is closed: one more unit must hit the over-return guard.
	in := returnInput()
	in.IdempotencyKey = "ret-mixed-over"
	in.Lines[0].QtyBase = 1
	if _, err := h.svc.Return(context.Background(), in); err == nil {
		t.Fatal("a closed line must refuse further returns")
	}
}

func TestReturn_Partial_SameUOM_Unchanged(t *testing.T) {
	// price_uom == base_uom (the seedReturnable shape: 10 sold @ 1000¢, total 10000): proportional
	// and qty×unit agree, 4/10 must still refund exactly 4000 (the pre-fix behavior for this case).
	h := newHarness()
	seedReturnable(h)
	out, err := h.svc.Return(context.Background(), returnInput())
	if err != nil {
		t.Fatalf("same-UOM return: %v", err)
	}
	if out.SubtotalCents != 4000 || out.TaxCents != 520 {
		t.Fatalf("same-UOM partial changed: %+v", out)
	}
}

func TestReturn_IdempotentReplay(t *testing.T) {
	h := newHarness()
	seedReturnable(h)
	h.store.cachedReturns["ret-key-1"] = Return{ReturnID: "ret-orig", ReturnNo: "#RET-9", Status: "COMMITTED"}
	out, err := h.svc.Return(context.Background(), returnInput())
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !out.Replayed || out.ReturnID != "ret-orig" {
		t.Fatalf("want the cached replay, got %+v", out)
	}
	if len(h.store.returnRows) != 0 || h.tender.refundCalled {
		t.Fatal("a replay must not re-run the rails")
	}
}

func TestReturn_Pickup_RequestedAndQueued(t *testing.T) {
	h := newHarness()
	seedReturnable(h)
	in := returnInput()
	in.Pickup = &PickupInput{Required: true, Site: "23 Quarry Rd", RequestedDate: "2026-07-02"}
	out, err := h.svc.Return(context.Background(), in)
	if err != nil {
		t.Fatalf("pickup return: %v", err)
	}
	if out.PickupStatus != PickupRequested {
		t.Fatalf("want REQUESTED, got %s", out.PickupStatus)
	}
	rr := h.store.returnRows[0]
	if !rr.PickupRequired || rr.PickupSite != "23 Quarry Rd" || rr.PickupDate != "2026-07-02" {
		t.Fatalf("pickup fields not persisted: %+v", rr)
	}
}

func TestLookupReturn_NetsPriorReturns_FlagsCardRefundable(t *testing.T) {
	h := newHarness()
	seedReturnable(h)
	if _, err := h.svc.Return(context.Background(), returnInput()); err != nil { // 4 of 10 already returned
		t.Fatalf("seed return: %v", err)
	}
	lk, err := h.svc.LookupReturn(context.Background(), "", "#SO-1")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if lk.OrderID != "order-1" || len(lk.Lines) != 1 {
		t.Fatalf("lookup wrong: %+v", lk)
	}
	if lk.Lines[0].QtySold != 10 || lk.Lines[0].QtyReturned != 4 {
		t.Fatalf("prior returns not netted: %+v", lk.Lines[0])
	}
	if lk.CardRefundable {
		t.Fatal("a cash sale is not card-refundable")
	}
	oc := h.store.retCtx["order-1"]
	oc.SaleTender = TenderCard
	oc.GatewayRef = "rp_1"
	h.store.retCtx["order-1"] = oc
	lk2, _ := h.svc.LookupReturn(context.Background(), "order-1", "")
	if !lk2.CardRefundable {
		t.Fatal("a card capture with a gateway ref must be card-refundable")
	}
}

func TestAdvancePickup_WalksForwardOnly(t *testing.T) {
	h := newHarness()
	h.store.pickups["ret-1"] = ReturnPickup{ReturnID: "ret-1", BranchID: "branch-trt", PickupStatus: PickupRequested}
	out, err := h.svc.AdvancePickup(context.Background(), "ret-1", PickupScheduled)
	if err != nil || out.PickupStatus != PickupScheduled {
		t.Fatalf("REQUESTED→SCHEDULED: %v %+v", err, out)
	}
	if _, err := h.svc.AdvancePickup(context.Background(), "ret-1", PickupRequested); err == nil {
		t.Fatal("walking backwards must 409")
	}
	if _, err := h.svc.AdvancePickup(context.Background(), "ret-1", PickupDone); err != nil {
		t.Fatalf("SCHEDULED→DONE: %v", err)
	}
}

// -- parked tickets ----------------------------------------------------------

func TestParked_Lifecycle_ParkListResumeDiscard(t *testing.T) {
	h := newHarness()
	t1, err := h.svc.Park(context.Background(), ParkInput{
		BranchID: "branch-trt", CustomerID: "cust-abc", Notes: "back after lunch",
		Lines: []ParkedLineInput{{ProductID: "prod-blu", QtyEntered: 12, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("park: %v", err)
	}
	if t1.TicketNo == "" || t1.Status != "PARKED" || t1.SaleType != SaleCollectNow {
		t.Fatalf("parked ticket wrong: %+v", t1)
	}
	list, err := h.svc.ListParked(context.Background(), "branch-trt")
	if err != nil || len(list) != 1 {
		t.Fatalf("list parked: %v %d", err, len(list))
	}
	res, err := h.svc.ResumeParked(context.Background(), t1.ID)
	if err != nil || res.Status != "RESUMED" {
		t.Fatalf("resume: %v %+v", err, res)
	}
	// The resumed payload round-trips the LINES ONLY, re-priced through the normal path.
	if len(res.Lines) != 1 || res.Lines[0].ProductID != "prod-blu" || res.Lines[0].QtyEntered != 12 {
		t.Fatalf("resumed lines wrong: %+v", res.Lines)
	}
	// Resuming twice is a 409; discarding a resumed ticket is a 409.
	if _, err := h.svc.ResumeParked(context.Background(), t1.ID); err == nil {
		t.Fatal("double resume must conflict")
	}
	if err := h.svc.DiscardParked(context.Background(), t1.ID); err == nil {
		t.Fatal("discarding a RESUMED ticket must conflict")
	}
}

func TestParked_LinesCarryNoPrice_RepricedOnResume(t *testing.T) {
	// Structural guarantee: a parked line has NO price field, a resume can only ring at the
	// CURRENT engine price (the design's "never rings at a stale price").
	rt := reflect.TypeOf(ParkedLineInput{})
	for i := 0; i < rt.NumField(); i++ {
		n := strings.ToLower(rt.Field(i).Name)
		if strings.Contains(n, "price") || strings.Contains(n, "cents") {
			t.Fatalf("ParkedLineInput must not carry a price: %s", rt.Field(i).Name)
		}
	}
}

// The parked fence (D49 row 500): a branch is a hard wall on list, resume and discard; a login
// from another branch can neither see nor touch a foreign branch's ticket.
func TestParked_ForeignBranch_CannotListResumeOrDiscard(t *testing.T) {
	h := newHarness()
	t1, err := h.svc.Park(context.Background(), ParkInput{
		BranchID: "branch-trt",
		Lines:    []ParkedLineInput{{ProductID: "prod-blu", QtyEntered: 12, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("park: %v", err)
	}
	// A Kingston clerk, granted at the other branch only.
	h.caller = identity.StaffContext{UserID: "u-kingston", RoleCode: "INSIDE_SALES",
		Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
		Branches: map[string]bool{"branch-kin": true}}
	if _, err := h.svc.ListParked(context.Background(), "branch-trt"); !isForbidden(err) {
		t.Fatalf("a Kingston login cannot list Trenton's tickets, got %v", err)
	}
	if _, err := h.svc.ResumeParked(context.Background(), t1.ID); !isForbidden(err) {
		t.Fatalf("a Kingston login cannot resume a Trenton ticket, got %v", err)
	}
	if err := h.svc.DiscardParked(context.Background(), t1.ID); !isForbidden(err) {
		t.Fatalf("a Kingston login cannot discard a Trenton ticket, got %v", err)
	}
	// And the refused touches moved nothing: the ticket is still PARKED where it was.
	h.caller = identity.StaffContext{UserID: "u-trenton-2", RoleCode: "INSIDE_SALES",
		Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
		Branches: map[string]bool{"branch-trt": true}}
	list, err := h.svc.ListParked(context.Background(), "branch-trt")
	if err != nil || len(list) != 1 || list[0].Status != "PARKED" {
		t.Fatalf("the fenced ticket is intact at its branch: %v %+v", err, list)
	}
}

// The ruled pick-up: anyone at the counter picks up any parked sale at THEIR branch, no owner
// check; a second clerk at the same branch resumes a ticket the first parked, and who resumed
// stays recorded.
func TestParked_SameBranch_SecondClerkResumes_NoOwnerCheck(t *testing.T) {
	h := newHarness()
	h.caller = identity.StaffContext{UserID: "u-trenton-1", RoleCode: "INSIDE_SALES",
		Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
		Branches: map[string]bool{"branch-trt": true}}
	t1, err := h.svc.Park(ctxWithActor("u-trenton-1"), ParkInput{
		BranchID: "branch-trt",
		Lines:    []ParkedLineInput{{ProductID: "prod-blu", QtyEntered: 12, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("park: %v", err)
	}
	// A different person at the same branch, the sales manager walking up to the same till.
	h.caller = identity.StaffContext{UserID: "u-trenton-2", RoleCode: "SALES_MANAGER",
		Tier: identity.TierManager, Scope: identity.ScopeBranch,
		Branches: map[string]bool{"branch-trt": true}}
	res, err := h.svc.ResumeParked(ctxWithActor("u-trenton-2"), t1.ID)
	if err != nil || res.Status != "RESUMED" {
		t.Fatalf("anyone at the branch picks up any parked sale: %v %+v", err, res)
	}
	if h.store.lastResumeActor != "u-trenton-2" {
		t.Fatalf("who resumed stays recorded, got %q", h.store.lastResumeActor)
	}
}

// isForbidden answers whether err is the 403 the branch wall answers with.
func isForbidden(err error) bool {
	var ce clientError
	return errors.As(err, &ce) && ce.status == http.StatusForbidden
}

// -- pricing orders ----------------------------------------------------------

func TestPricingOrder_SnapshotsListVsOffered_TotalsAndValidity(t *testing.T) {
	h := newHarness()
	h.resolver.price.Breakdown.OriginalCents = 45000 // the retail/list anchor
	h.store.products["prod-blu"] = [2]string{"BLU-60", "Blu 60mm Slab"}
	po, err := h.svc.CreatePricingOrder(context.Background(), PricingOrderInput{
		BranchID: "branch-trt", CustomerName: "Weekend Walk-in",
		Lines: []PricingOrderLineInput{{ProductID: "prod-blu", QtyEntered: 100, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("pricing order: %v", err)
	}
	if po.DocNo == "" {
		t.Fatal("pricing order must mint a doc number")
	}
	ln := po.Lines[0]
	if ln.UnitPriceCents != 39934 || ln.ListPriceCents != 45000 || ln.SKU != "BLU-60" || ln.Name != "Blu 60mm Slab" {
		t.Fatalf("list-vs-offered snapshot wrong: %+v", ln)
	}
	if po.SubtotalCents != 10000 || po.TaxCents != 1300 || po.TotalCents != 11300 {
		t.Fatalf("pricing totals wrong: %+v", po)
	}
	// Default validity: clock 2026-06-29 + 14d.
	if po.ValidUntil != "2026-07-13" {
		t.Fatalf("valid_until default must be today+14d, got %s", po.ValidUntil)
	}
	// It is NOT an order and NOT an invoice, nothing else persists.
	if len(h.store.orders) != 0 || len(h.store.invoices) != 0 || len(h.store.custTxns) != 0 {
		t.Fatal("a pricing order must never touch orders/invoices/AR")
	}
}

// The till's pricing doc prices through the counter resolve too (D48 row 481, D49): a line the
// gated resolve would refuse below the floor is priced and the document is created.
func TestPricingOrder_BelowFloorLine_IsCreated_NoBlock(t *testing.T) {
	h := newHarness()
	// The GATED resolve's verdict on the same line; the pricing order never asks it.
	h.resolver.blocked = true
	h.resolver.policy = "MARGIN_FLOOR"
	po, err := h.svc.CreatePricingOrder(context.Background(), PricingOrderInput{
		BranchID: "branch-trt",
		Lines:    []PricingOrderLineInput{{ProductID: "prod-blu", QtyEntered: 1, UOMEntered: "EA"}},
	})
	if err != nil {
		t.Fatalf("the till's pricing order is created below the floor, got %v", err)
	}
	if po.DocNo == "" || len(po.Lines) != 1 || po.Lines[0].UnitPriceCents != 39934 {
		t.Fatalf("the doc carries the resolver's rule price: %+v", po)
	}
}

// -- policy endpoints --------------------------------------------------------

func TestPolicy_ReadAndUpdate(t *testing.T) {
	h := newHarness()
	p, err := h.svc.Policy(context.Background(), "branch-trt")
	if err != nil || !p.CODEnabled {
		t.Fatalf("default policy must be COD-on: %v %+v", err, p)
	}
	upd, err := h.svc.UpdatePolicy(context.Background(), PosPolicy{BranchID: "branch-trt", CODEnabled: false, Notes: "no COD at Trenton"})
	if err != nil || upd.CODEnabled {
		t.Fatalf("update must flip the branch row: %v %+v", err, upd)
	}
	// The org default is untouched.
	org, _ := h.svc.Policy(context.Background(), "")
	if !org.CODEnabled {
		t.Fatal("org default must stay COD-on")
	}
}
