//go:build integration

package pos

// returns_customer_lines_integration_test.go, sub item 10.1: the customer lines read behind the
// returns-across-orders flow. The source is every sales order the customer holds in HH (the widened
// Q1 ruling): till rings AND orders converted from quotes, both of them orders like any other. The
// read answers the orders newest first, each with its lines' returnable quantities computed by the
// same SQL the commit guard reads, so the screen and the guard cannot disagree. The branch wall is
// the counter's own: a scoped caller sees only the orders their resolved authority reaches, and
// naming a branch they do not reach is the wall's 403 naming the branch back. The house WALK-IN row
// is refused in words: it is not one customer's history, it is everybody's.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// plantCreditCustomer plants one account customer (the walk-in exclusion's counterweight: this read
// is the account customer's own history).
func plantCreditCustomer(t *testing.T, s *trentonStack, name string) string {
	t.Helper()
	var id string
	if err := s.pool.QueryRow(context.Background(), `
INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
VALUES ($1, $2, 'CONTRACTOR', 30, 5000000) RETURNING id::text`,
		fmt.Sprintf("CL-%d", time.Now().UnixNano()%1_000_000_000), name).Scan(&id); err != nil {
		t.Fatalf("plant the customer: %v", err)
	}
	return id
}

// ringForCustomer rings one sale for a named customer through the real composition, with the
// optional job label the read must answer.
func (s *trentonStack) ringForCustomer(t *testing.T, key, branch, drawer, who, customerID, jobAddress string, qty float64, tender string) trentonSaleView {
	t.Helper()
	body := map[string]any{
		"idempotency_key": key,
		"branch_id":       branch,
		"till_session_id": drawer,
		"customer_id":     customerID,
		"sale_type":       SaleCollectNow,
		"tender":          tender,
		"job_address":     jobAddress,
		"lines":           []map[string]any{{"product_id": l2cProductID, "qty_entered": qty, "uom_entered": "SF"}},
	}
	if tender == TenderCard {
		body["payment_token"] = "tok_till_l2c_demo"
	}
	who2 := trentonClerkSub
	if who == "kingston" {
		who2 = kingstonClerkSub
	}
	if who == "admin" {
		who2 = trentonAdminSub
	}
	rec := s.do(http.MethodPost, "/api/v1/pos/sales", s.bearer(who2), "", body)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("ring the sale: %d %s", rec.Code, rec.Body.String())
	}
	var out trentonSaleView
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode the sale: %v", err)
	}
	return out
}

// plantConvertedOrder plants an order that came from a quote: a converted quote row, the order
// pointing at it, and one line. The quantities and money are planted exactly, the read must answer
// them as planted.
func plantConvertedOrder(t *testing.T, s *trentonStack, customerID, branchID, orderNo string, qty float64, unit, lineTotal, tax int64) string {
	t.Helper()
	ctx := context.Background()
	var quoteID, orderID string
	if err := s.pool.QueryRow(ctx, `
INSERT INTO quotes (quote_no, customer_id, branch_id, status, subtotal_cents, tax_cents, total_cents)
VALUES ($1, $2::uuid, $3::uuid, 'CONVERTED', $4, $5, $6) RETURNING id::text`,
		orderNo+"-Q", customerID, branchID, lineTotal, tax, lineTotal+tax).Scan(&quoteID); err != nil {
		t.Fatalf("plant the quote: %v", err)
	}
	if err := s.pool.QueryRow(ctx, `
INSERT INTO orders (order_no, customer_id, branch_id, quote_id, status, fulfillment, subtotal_cents, tax_cents, total_cents)
VALUES ($1, $2::uuid, $3::uuid, $4::uuid, 'CONFIRMED', 'WILL_CALL', $5, $6, $7) RETURNING id::text`,
		orderNo, customerID, branchID, quoteID, lineTotal, tax, lineTotal+tax).Scan(&orderID); err != nil {
		t.Fatalf("plant the converted order: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `
INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered, qty_base,
                         unit_price_cents, price_uom, line_total_cents, tax_cents)
VALUES ($1::uuid, 1, $2::uuid, 'converted line', $3, 'SF', $3, $4, 'SF', $5, $6)`,
		orderID, l2cProductID, qty, unit, lineTotal, tax); err != nil {
		t.Fatalf("plant the converted order's line: %v", err)
	}
	return orderID
}

// customerLinesGet drives the read once.
func (s *trentonStack) customerLinesGet(t *testing.T, who, customerID, branchID string, limit int) (int, map[string]any) {
	t.Helper()
	q := url.Values{}
	q.Set("customer_id", customerID)
	if branchID != "" {
		q.Set("branch_id", branchID)
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	rec := s.do(http.MethodGet, "/api/v1/pos/returns/customer-lines?"+q.Encode(), s.bearer(who), "", nil)
	if rec.Code != http.StatusOK {
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return rec.Code, m
	}
	return rec.Code, s.decodeBody(rec)
}

// orderItems pulls the read's items, failing loudly on a wrong envelope.
func orderItems(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	raw, ok := body["items"].([]any)
	if !ok {
		t.Fatalf("the read must answer an items envelope: %v", body)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		m, ok := r.(map[string]any)
		if !ok {
			t.Fatalf("an item must be an object: %v", r)
		}
		out = append(out, m)
	}
	return out
}

// findOrder returns the read item for one order id.
func findOrder(t *testing.T, items []map[string]any, orderID string) map[string]any {
	t.Helper()
	for _, o := range items {
		if o["order_id"] == orderID {
			return o
		}
	}
	t.Fatalf("order %s absent from the read: %v", orderID, items)
	return nil
}

// num reads one map field as float64.
func num(t *testing.T, m map[string]any, k string) float64 {
	t.Helper()
	v, ok := m[k].(float64)
	if !ok {
		t.Fatalf("field %s is not a number: %v", k, m)
	}
	return v
}

// The three orders over two branches with one prior partial return: the open quantities exact, the
// tax and money already returned netted per line, the job label answered, the branch wall held, the
// converted quote's order sitting beside the till rings, the contract's required keys present.
func TestIntegration_CustomerLines_ThreeOrdersTwoBranchesOnePriorReturn(t *testing.T) {
	s := newTrentonStack(t)
	customer := plantCreditCustomer(t, s, "Customer lines proof")
	other := plantCreditCustomer(t, s, "Another customer")

	// Three orders for the customer: two Trenton rings and one Kingston ring, rung oldest first so
	// the newest first ordering is asserted, plus another customer's order that must never appear.
	saleA := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", customer, "Marina Road patio", 20, TenderCash)
	saleB := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", customer, "", 10, TenderCash)
	saleC := s.ringForCustomer(t, key(), kingstonBranchID, s.kingstonTill, "kingston", customer, "", 10, TenderCash)
	saleOther := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", other, "", 7, TenderCash)

	// One prior partial return on the older Trenton sale: 5 of 20 SF, money 4250 and tax 553 back.
	prior := s.returnBody(key(), saleA, 0, RefundCash, 5)
	if rec := s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", prior); rec.Code != http.StatusCreated {
		t.Fatalf("the prior partial return must commit: %d %s", rec.Code, rec.Body.String())
	}

	// A converted quote's order, the newest thing the customer holds.
	converted := plantConvertedOrder(t, s, customer, trentonBranchID, fmt.Sprintf("#SO-CL-%d", time.Now().UnixNano()%1_000_000), 4, 1000, 4000, 520)

	// The Trenton clerk (scoped to Trenton): the converted order and the two Trenton rings, newest
	// first, never the Kingston ring, never the other customer's order.
	code, body := s.customerLinesGet(t, trentonClerkSub, customer, "", 0)
	if code != http.StatusOK {
		t.Fatalf("the clerk's read must answer, got %d", code)
	}
	if body["customer_id"] != customer {
		t.Fatalf("the read must echo the customer: %v", body["customer_id"])
	}
	items := orderItems(t, body)
	if len(items) != 3 {
		t.Fatalf("the clerk must see the customer's three Trenton orders, got %d: %v", len(items), items)
	}
	if items[0]["order_id"] != converted || items[1]["order_id"] != saleB.OrderID || items[2]["order_id"] != saleA.OrderID {
		t.Fatalf("the orders must arrive newest first (converted, B, A): %v", items)
	}

	// The converted order reads as the quote conversion it is, with its planted line open in full.
	conv := findOrder(t, items, converted)
	if conv["source"] != "QUOTE" || conv["sale_type"] != "" || conv["tender"] != "" {
		t.Fatalf("the converted order must name its source and carry no till facts: %v", conv)
	}
	if conv["job_label"] != "" {
		t.Fatalf("the converted order carries no job label: %v", conv)
	}
	convLines := conv["lines"].([]any)
	if len(convLines) != 1 {
		t.Fatalf("the converted order has one line: %v", conv)
	}
	cl := convLines[0].(map[string]any)
	if num(t, cl, "qty_sold") != 4 || num(t, cl, "qty_returned") != 0 || num(t, cl, "qty_open") != 4 ||
		num(t, cl, "unit_price_cents") != 1000 || num(t, cl, "line_total_cents") != 4000 || num(t, cl, "tax_cents") != 520 {
		t.Fatalf("the converted order's line must read as planted: %v", cl)
	}
	if cl["shelf_state"] != "NOT_FULFILLED" {
		t.Fatalf("a will-call order that never left the shelf must say so: %v", cl["shelf_state"])
	}

	// The older ring answers the job label and the exact open quantity: 15 of 20 open, 5 returned,
	// the 5000 money and 650 tax already back, the line's own totals unchanged.
	a := findOrder(t, items, saleA.OrderID)
	if a["source"] != "TILL" || a["sale_type"] != SaleCollectNow || a["tender"] != TenderCash {
		t.Fatalf("the ring must name its source, sale type and tender: %v", a)
	}
	if a["job_label"] != "Marina Road patio" {
		t.Fatalf("the ring's job label must answer: %v", a["job_label"])
	}
	aLines := a["lines"].([]any)
	if len(aLines) != 1 {
		t.Fatalf("the ring has one line: %v", a)
	}
	al := aLines[0].(map[string]any)
	if num(t, al, "qty_sold") != 20 || num(t, al, "qty_returned") != 5 || num(t, al, "qty_open") != 15 {
		t.Fatalf("the prior return must net exactly: %v", al)
	}
	if num(t, al, "money_returned_cents") != 4250 || num(t, al, "tax_returned_cents") != 553 {
		t.Fatalf("the prior return's money and tax must net exactly: %v", al)
	}
	if num(t, al, "unit_price_cents") != 850 || num(t, al, "line_total_cents") != 17000 || num(t, al, "tax_cents") != 2210 {
		t.Fatalf("the line's own totals must stand: %v", al)
	}
	if al["shelf_state"] != "FULFILLED" {
		t.Fatalf("a collect-now ring's goods left the shelf: %v", al["shelf_state"])
	}
	for _, k := range []string{"order_line_id", "product_id", "sku", "description", "qty_sold", "qty_returned", "qty_open", "unit_price_cents", "line_total_cents", "tax_cents", "money_returned_cents", "tax_returned_cents", "shelf_state"} {
		if _, ok := al[k]; !ok {
			t.Fatalf("the contract requires %s on every line: %v", k, al)
		}
	}

	// The general manager (global) sees all four of the customer's orders, the Kingston one included.
	code, body = s.customerLinesGet(t, trentonAdminSub, customer, "", 0)
	if code != http.StatusOK {
		t.Fatalf("the general manager's read must answer, got %d", code)
	}
	items = orderItems(t, body)
	if len(items) != 4 {
		t.Fatalf("the general manager must see every order of the customer, got %d", len(items))
	}
	for _, o := range items {
		if o["order_id"] == saleOther.OrderID {
			t.Fatal("another customer's order must never appear")
		}
	}
	kingstonSeen := false
	for _, o := range items {
		if o["order_id"] == saleC.OrderID {
			kingstonSeen = true
			if o["branch_id"] != kingstonBranchID {
				t.Fatalf("the Kingston order must carry its branch: %v", o)
			}
		}
	}
	if !kingstonSeen {
		t.Fatal("the general manager must see the Kingston order")
	}

	// The limit bounds the orders answered, the newest kept.
	code, body = s.customerLinesGet(t, trentonClerkSub, customer, "", 2)
	if code != http.StatusOK {
		t.Fatalf("the limited read must answer, got %d", code)
	}
	if items = orderItems(t, body); len(items) != 2 || items[0]["order_id"] != converted || items[1]["order_id"] != saleB.OrderID {
		t.Fatalf("limit 2 must keep the two newest: %v", items)
	}

	// A scoped caller naming a branch they do not reach gets the wall's 403 naming the branch.
	code, _ = s.customerLinesGet(t, trentonClerkSub, customer, kingstonBranchID, 0)
	if code != http.StatusForbidden {
		t.Fatalf("naming an unreachable branch must be the wall's 403, got %d", code)
	}
	// The scoped caller naming their own branch sees only it.
	code, body = s.customerLinesGet(t, trentonClerkSub, customer, trentonBranchID, 0)
	if code != http.StatusOK {
		t.Fatalf("naming the caller's own branch must answer, got %d", code)
	}
	if items = orderItems(t, body); len(items) != 3 {
		t.Fatalf("the Trenton branch read answers the Trenton orders, got %d", len(items))
	}

	// A line returned in full shows zero open.
	full := s.returnBody(key(), saleB, 0, RefundCash, 10)
	if rec := s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", full); rec.Code != http.StatusCreated {
		t.Fatalf("the full return must commit: %d %s", rec.Code, rec.Body.String())
	}
	code, body = s.customerLinesGet(t, trentonClerkSub, customer, trentonBranchID, 0)
	if code != http.StatusOK {
		t.Fatalf("the read after the full return must answer, got %d", code)
	}
	b := findOrder(t, orderItems(t, body), saleB.OrderID)
	bl := b["lines"].([]any)[0].(map[string]any)
	if num(t, bl, "qty_open") != 0 || num(t, bl, "qty_returned") != 10 || num(t, bl, "money_returned_cents") != 8500 || num(t, bl, "tax_returned_cents") != 1105 {
		t.Fatalf("a fully returned line shows zero open with the money and tax netted: %v", bl)
	}
}

// The house WALK-IN row is refused in words: its sales are everybody's, not one customer's history,
// and a walk-in sale is returned by its own number on the single-order path.
func TestIntegration_CustomerLines_TheWalkInIsRefused(t *testing.T) {
	s := newTrentonStack(t)
	walkIn := walkInID(t, s.pool)
	code, body := s.customerLinesGet(t, trentonClerkSub, walkIn, "", 0)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("the WALK-IN read must be refused 422, got %d", code)
	}
	msg, _ := body["error"].(string)
	if msg == "" {
		t.Fatalf("the refusal must say why in words: %v", body)
	}
	// Ruling A: the walk-in's lines are found only by a receipt or order number, never listed.
	if !strings.Contains(msg, "receipt or order number") || !strings.Contains(msg, "never listed") {
		t.Fatalf("the refusal must say how walk-in sales are found: %q", msg)
	}
	// No customer named is the client's own 400.
	rec := s.do(http.MethodGet, "/api/v1/pos/returns/customer-lines", s.bearer(trentonClerkSub), "", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a read with no customer must be a 400, got %d", rec.Code)
	}
}
