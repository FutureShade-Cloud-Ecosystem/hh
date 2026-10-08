//go:build integration

package pos

// returns_rulings_integration_test.go, the rulings round: the principal's ruling on an order that is
// not fully paid (cash or card only up to what was paid, the rest off the open invoice, the server
// deciding the split and the preview carrying it), the walk in sales under ruling A, and the swap
// with a card shortfall (the credit side's refusal answered in its own words, no card charged).

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/jackc/pgx/v5"
)

// plantOwed plants a converted order of 6 units (1000 + 13 percent each, 6780 in all) whose invoice has
// taken `paid` cents; the rest is its open balance.
func (s *trentonStack) plantOwed(cust string, paid int64) (orderID, invoiceID string) {
	s.t.Helper()
	orderID = plantConvertedOrder(s.t, s, cust, trentonBranchID, fmt.Sprintf("#SO-OW-%d", time.Now().UnixNano()%1_000_000), 6, 1000, 6000, 780)
	status := "FINALIZED"
	if paid > 0 {
		status = "PARTIALLY_PAID"
	}
	invoiceID = s.plantInvoice(orderID, cust, trentonBranchID, status, "ORDER", 6780, 6780-paid)
	return orderID, invoiceID
}

func (s *trentonStack) noteFor(cust, orderID, method string, qty float64) *httptest.ResponseRecorder {
	b := map[string]any{"idempotency_key": key(), "customer_id": cust, "refund_method": method, "reason": "x", "restock": false,
		"till_session_id": s.trentonTill,
		"orders":          []any{map[string]any{"order_id": orderID, "lines": []any{map[string]any{"order_line_id": s.lineOf(orderID), "qty_base": qty}}}}}
	return s.postNote(trentonClerkSub, "", b)
}

func (s *trentonStack) invoiceNow(id string) (status string, balance int64) {
	s.t.Helper()
	if err := s.pool.QueryRow(context.Background(), `SELECT status, balance_cents FROM invoices WHERE id = $1::uuid`, id).Scan(&status, &balance); err != nil {
		s.t.Fatal(err)
	}
	return status, balance
}

// Unpaid: no cash or card at all, in the principal's words, and the account rail still works.
// Partly paid: cash up to what was paid, the rest off the open invoice, across successive returns.
// Fully paid: unchanged. The preview carries the same split.
func TestIntegration_Rulings_CashAndCardOnlyUpToWhatWasPaid(t *testing.T) {
	s := newTrentonStackOnPoolWithRail(t, intPool(t), newKeyedRail())
	cust := s.plantCustomer("Ruling owed", 30, 5000000)

	// Unpaid.
	oUnpaid, invUnpaid := s.plantOwed(cust, 0)
	// (Card on an order a quote became has no capture to refund to and says so in its own words.)
	rec := s.noteFor(cust, oUnpaid, RefundCash, 2)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unpaid cash must be refused, got %d: %s", rec.Code, rec.Body.String())
	}
	if msg, _ := s.decodeBody(rec)["error"].(string); !containsStr(msg, "This order is not paid yet") || !containsStr(msg, "comes off the open invoice") {
		t.Fatalf("unpaid cash: the principal's words: %q", msg)
	}
	prev := s.postPreview(trentonClerkSub, map[string]any{"customer_id": cust, "refund_method": RefundCash,
		"orders": []any{map[string]any{"order_id": oUnpaid, "lines": []any{map[string]any{"order_line_id": s.lineOf(oUnpaid), "qty_base": 2}}}}})
	if prev.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", prev.Code, prev.Body.String())
	}
	po := s.decodeBody(prev)["orders"].([]any)[0].(map[string]any)
	if num(t, po, "rail_cents") != 0 || num(t, po, "invoice_offset_cents") != 2260 || !containsStr(po["rail_note"].(string), "not paid yet") {
		t.Fatalf("the preview carries the unpaid split: %v", po)
	}
	if rec := s.noteFor(cust, oUnpaid, RefundAccountCredit, 2); rec.Code != http.StatusCreated {
		t.Fatalf("the account rail still works on an unpaid order, got %d: %s", rec.Code, rec.Body.String())
	}
	if _, bal := s.invoiceNow(invUnpaid); bal != 6780-2260 {
		t.Fatalf("the account credit came off the open invoice: %d", bal)
	}

	// Partly paid: 3780 paid, 3000 owed. Two returns: 2 units (2260) all in cash, then 4 units (4520):
	// 1520 more in cash, 3000 off the invoice.
	oPart, invPart := s.plantOwed(cust, 3780)
	prev = s.postPreview(trentonClerkSub, map[string]any{"customer_id": cust, "refund_method": RefundCash,
		"orders": []any{map[string]any{"order_id": oPart, "lines": []any{map[string]any{"order_line_id": s.lineOf(oPart), "qty_base": 6}}}}})
	pp := s.decodeBody(prev)
	po = pp["orders"].([]any)[0].(map[string]any)
	if num(t, po, "rail_cents") != 3780 || num(t, po, "invoice_offset_cents") != 3000 ||
		!containsStr(po["rail_note"].(string), "up to $37.80 can go back in cash or card") || num(t, pp, "rail_cents") != 3780 {
		t.Fatalf("the preview carries the split in words: %v", po)
	}
	rec = s.noteFor(cust, oPart, RefundCash, 2)
	if rec.Code != http.StatusCreated {
		t.Fatalf("first partial cash return: %d %s", rec.Code, rec.Body.String())
	}
	if st, bal := s.invoiceNow(invPart); st != "PARTIALLY_PAID" || bal != 3000 {
		t.Fatalf("2260 is inside what was paid: the invoice is untouched, %s %d", st, bal)
	}
	rec = s.noteFor(cust, oPart, RefundCash, 4)
	if rec.Code != http.StatusCreated {
		t.Fatalf("second return: %d %s", rec.Code, rec.Body.String())
	}
	child := s.decodeBody(rec)["returns"].([]any)[0].(map[string]any)
	if num(t, child, "total_cents") != 4520 || num(t, child, "invoice_offset_cents") != 3000 || !containsStr(child["payment_note"].(string), "not fully paid") {
		t.Fatalf("the second return splits 1520 cash and 3000 off the invoice: %v", child)
	}
	if st, bal := s.invoiceNow(invPart); st != "PAID" || bal != 0 {
		t.Fatalf("the open invoice is cleared by the offset: %s %d", st, bal)
	}
	var cash int64
	if err := s.pool.QueryRow(context.Background(), `SELECT COALESCE(SUM(p.amount_cents),0) FROM pos_returns r JOIN payments p ON p.id = r.refund_payment_id WHERE r.order_id = $1::uuid`, oPart).Scan(&cash); err != nil || cash != 3780 {
		t.Fatalf("exactly what was paid went back in cash: %d (%v)", cash, err)
	}
	// The books: the split's offset credits the receivable, the rail the cash, and they balance.
	s.drainGL()
	var ids []string
	rows, err := s.pool.Query(context.Background(), `SELECT 'pos.return.committed:' || id::text FROM pos_returns WHERE order_id = $1::uuid`, oPart)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k string
		_ = rows.Scan(&k)
		ids = append(ids, k)
	}
	rows.Close()
	if got := -s.glNet("1100", ids); got != 3000 {
		t.Fatalf("the receivable is credited exactly the offset, got %d", got)
	}
	if got := -s.glNet("1020", ids); got != 3780 {
		t.Fatalf("the cash is credited exactly what went back, got %d", got)
	}

	// Fully paid: unchanged, the whole return in cash and no split in the preview.
	oPaid := plantConvertedOrder(t, s, cust, trentonBranchID, fmt.Sprintf("#SO-OP-%d", time.Now().UnixNano()%1_000_000), 6, 1000, 6000, 780)
	s.plantInvoice(oPaid, cust, trentonBranchID, "PAID", "ORDER", 6780, 0)
	prev = s.postPreview(trentonClerkSub, map[string]any{"customer_id": cust, "refund_method": RefundCash,
		"orders": []any{map[string]any{"order_id": oPaid, "lines": []any{map[string]any{"order_line_id": s.lineOf(oPaid), "qty_base": 6}}}}})
	po = s.decodeBody(prev)["orders"].([]any)[0].(map[string]any)
	if num(t, po, "rail_cents") != 6780 || num(t, po, "invoice_offset_cents") != 0 || po["rail_note"] != nil {
		t.Fatalf("a fully paid order splits nothing: %v", po)
	}
	if rec := s.noteFor(cust, oPaid, RefundCash, 6); rec.Code != http.StatusCreated {
		t.Fatalf("fully paid cash return: %d %s", rec.Code, rec.Body.String())
	}
}

// Ruling A: the WALK-IN row is a returns customer through a receipt or order number only, never
// another customer's lines, cash or card only, the approver kept over the limit.
func TestIntegration_Rulings_WalkInSalesOnlyThroughTheNumber(t *testing.T) {
	rail := newKeyedRail()
	s := newTrentonStackOnPoolWithRail(t, intPool(t), rail)
	walk := walkInID(t, s.pool)
	other := s.plantCustomer("Ruling other", 30, 5000000)
	sale := s.ringSale(key(), trentonBranchID, s.trentonTill, "trenton", []float64{4}, TenderCash)
	card := s.ringSale(key(), trentonBranchID, s.trentonTill, "trenton", []float64{3}, TenderCard)
	foreign := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", other, "", 2, TenderCash)

	// No number: never a list.
	code, body := s.customerLinesGetNumber(t, trentonClerkSub, walk, "")
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("the walk-in without a number is refused, got %d %v", code, body)
	}
	if msg, _ := body["error"].(string); !containsStr(msg, "receipt or order number") || !containsStr(msg, "never listed") {
		t.Fatalf("the words: %q", msg)
	}
	// With the order number (with and without the hash) and with the invoice number: that one sale.
	for _, n := range []string{sale.OrderNo, trimHash(sale.OrderNo)} {
		code, body = s.customerLinesGetNumber(t, trentonClerkSub, walk, n)
		items := orderItems(t, body)
		if code != http.StatusOK || len(items) != 1 || items[0]["order_id"] != sale.OrderID {
			t.Fatalf("number %q: %d %v", n, code, body)
		}
	}
	var invNo string
	if err := s.pool.QueryRow(context.Background(), `SELECT invoice_no FROM invoices WHERE order_id = $1::uuid`, sale.OrderID).Scan(&invNo); err != nil {
		t.Fatal(err)
	}
	code, body = s.customerLinesGetNumber(t, trentonClerkSub, walk, invNo)
	if items := orderItems(t, body); code != http.StatusOK || len(items) != 1 || items[0]["order_id"] != sale.OrderID {
		t.Fatalf("the invoice number finds the sale: %d %v", code, body)
	}
	// Another customer's number never reaches another customer's lines through the walk-in.
	code, body = s.customerLinesGetNumber(t, trentonClerkSub, walk, foreign.OrderNo)
	if items := orderItems(t, body); code != http.StatusOK || len(items) != 0 {
		t.Fatalf("another customer's order must not appear under the walk-in: %d %v", code, body)
	}

	// Cash and card go back; account credit does not.
	rec := s.postNote(trentonClerkSub, "", s.noteBody(key(), walk, RefundCash, []trentonSaleView{sale}, []float64{1}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("a walk-in cash note: %d %s", rec.Code, rec.Body.String())
	}
	rec = s.postNote(trentonClerkSub, "", s.noteBody(key(), walk, RefundCard, []trentonSaleView{card}, []float64{1}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("a walk-in card note: %d %s", rec.Code, rec.Body.String())
	}
	rec = s.postNote(trentonClerkSub, "", s.noteBody(key(), walk, RefundAccountCredit, []trentonSaleView{sale}, []float64{1}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("account credit on the walk-in row is refused, got %d: %s", rec.Code, rec.Body.String())
	}
	// Another customer's order cannot ride a walk-in note.
	rec = s.postNote(trentonClerkSub, "", s.noteBody(key(), walk, RefundCash, []trentonSaleView{foreign}, []float64{1}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("another customer's order on a walk-in note is refused, got %d: %s", rec.Code, rec.Body.String())
	}
	// Over the limit the approver is still asked: 40 SF of retail walk-in sale is past 250.00.
	big := s.ringSale(key(), trentonBranchID, s.trentonTill, "trenton", []float64{50}, TenderCash)
	rec = s.postNote(trentonClerkSub, "", s.noteBody(key(), walk, RefundCash, []trentonSaleView{big}, []float64{45}))
	if rec.Code != http.StatusConflict {
		t.Fatalf("a walk-in return over the limit needs its approver, got %d: %s", rec.Code, rec.Body.String())
	}
	// And the swap still takes a customer's account, not the walk-in row.
	rec = s.postSwap(trentonClerkSub, s.swapBody(key(), walk, sale, 1, 1, TenderCash))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a swap on the walk-in row is refused, got %d: %s", rec.Code, rec.Body.String())
	}
}

func trimHash(no string) string {
	if len(no) > 0 && no[0] == '#' {
		return no[1:]
	}
	return no
}

func (s *trentonStack) customerLinesGetNumber(t *testing.T, who, customerID, number string) (int, map[string]any) {
	t.Helper()
	path := "/api/v1/pos/returns/customer-lines?customer_id=" + customerID
	if number != "" {
		path += "&number=" + urlQueryEscape(number)
	}
	rec := s.do(http.MethodGet, path, s.bearer(who), "", nil)
	return rec.Code, s.decodeBody(rec)
}

func urlQueryEscape(v string) string {
	out := ""
	for _, c := range []byte(v) {
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '-' {
			out += string(c)
		} else {
			out += fmt.Sprintf("%%%02X", c)
		}
	}
	return out
}

// captureCountingCharger counts the card the ring charges and the reversals it makes.
type captureCountingCharger struct {
	mu       sync.Mutex
	captures int
	voids    int
}

func (c *captureCountingCharger) Capture(_ context.Context, in till.CardChargeInput) (till.CardCapture, error) {
	if in.ReadOnly {
		return till.CardCapture{}, till.ErrCardLegUnattempted
	}
	c.mu.Lock()
	c.captures++
	c.mu.Unlock()
	return till.CardCapture{Kind: till.CardCaptureCaptured, AttemptID: "att-count-" + in.LegKey, ProcessorRef: "rp_stub_captured"}, nil
}

func (c *captureCountingCharger) BindTx(_ context.Context, _ pgx.Tx, b till.CardBind) error {
	return nil
}

func (c *captureCountingCharger) Compensate(_ context.Context, attemptID string) (till.CardCompensation, error) {
	c.mu.Lock()
	c.voids++
	c.mu.Unlock()
	return till.CardCompensation{Kind: till.CardCompVoided, AttemptID: attemptID}, nil
}

// A swap with a card shortfall whose credit side needs an approver is answered 409 in its own words
// BEFORE any card is charged: no capture and no void.
func TestIntegration_Rulings_ACardShortfallSwapMeetsTheCreditRefusalBeforeTheCard(t *testing.T) {
	charger := &captureCountingCharger{}
	trentonChargerOverride = charger
	defer func() { trentonChargerOverride = nil }()
	s := newTrentonStack(t)
	cust := s.plantCustomer("Ruling card swap", 30, 5000000)
	orig := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 40, TenderCash)
	before := charger.captures
	rec := s.postSwap(trentonClerkSub, s.swapBody(key(), cust, orig, 40, 60, TenderCard))
	if rec.Code != http.StatusConflict {
		t.Fatalf("the credit past the amount needs its approver, got %d: %s", rec.Code, rec.Body.String())
	}
	if body := s.decodeBody(rec); body["error"] == nil || !containsStr(fmt.Sprint(body["error"]), "approv") {
		t.Fatalf("the refusal is the approval family, in its own words: %v", body)
	}
	if charger.captures != before || charger.voids != 0 {
		t.Fatalf("no card was to be charged: %d captures, %d voids", charger.captures-before, charger.voids)
	}
}
