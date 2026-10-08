package main

// item9_desk_hold_route_test.go, item 9 review P2-5's last leg: the desk's
// order payment lifts the pay before release hold, proven AT THE ROUTE through
// the full served surface (the review noted the lift was only proven through
// the payments settle, never through the quote door a person drives). The
// fixture's alpha contractor is a zero day terms, no credit account, so its
// converted order is held from picking until the desk's payment lands.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestItem9_DeskOrderPaymentLiftsThePickHoldAtTheRoute(t *testing.T) {
	env := newCompEnv(t)
	rec, graph := registeredSurface(t, env)
	ctx := context.Background()

	// The counter's staff login (the fixture's inside sales principal, granted
	// at the north branch) and its request shape.
	token := env.minter.StaffToken(t, testkit.SubInsideSales)
	do := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set(authz.HeaderXOrg, testkit.OrgNorthSlug)
		req.Header.Set("Content-Type", "application/json")
		rsp := httptest.NewRecorder()
		rec.inner.ServeHTTP(rsp, req)
		return rsp
	}

	// One Cash On Demand shaped customer beside the world (zero day terms WITH
	// a credit line, the posture that converts through the plain door and is
	// held from picking until paid), and one ACCEPTED quote for it: a priced
	// line, the quote's totals agreeing with it (the agreed arithmetic the
	// desk door reads).
	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
	var custID string
	if err := graph.pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, home_branch_id, org_id,
		                       payment_terms_days, credit_limit_cents)
		VALUES ($1, 'I9 hold route proof', 'CONTRACTOR', $2::uuid, $3::uuid, 0, 5000000)
		RETURNING id::text`,
		"I9HOLD-C-"+suffix, testkit.BranchNorthAID, testkit.OrgNorthID).Scan(&custID); err != nil {
		t.Fatalf("plant customer: %v", err)
	}
	var quoteID string
	if err := graph.pool.QueryRow(ctx, `
		INSERT INTO quotes (quote_no, customer_id, branch_id, status, org_id,
		                    subtotal_cents, tax_cents, total_cents, fulfillment)
		VALUES ($1, $2::uuid, $3::uuid, 'ACCEPTED', $4::uuid, 10000, 1300, 11300, 'PICKUP')
		RETURNING id::text`,
		"I9HOLD-"+suffix, custID, testkit.BranchNorthAID, testkit.OrgNorthID).Scan(&quoteID); err != nil {
		t.Fatalf("plant quote: %v", err)
	}
	if _, err := graph.pool.Exec(ctx, `
		INSERT INTO quote_lines (quote_id, line_no, product_id, description, qty_entered, uom_entered,
		                         qty_base, unit_price_cents, price_uom, line_total_cents, org_id)
		VALUES ($1::uuid, 1, $2::uuid, 'I9HOLD line', 10, 'EA', 10, 1000, 'EA', 10000, $3::uuid)`,
		quoteID, testkit.ProductID, testkit.OrgNorthID); err != nil {
		t.Fatalf("plant quote line: %v", err)
	}
	var orderID string
	t.Cleanup(func() {
		bg := context.Background()
		if orderID != "" {
			_, _ = graph.pool.Exec(bg, `DELETE FROM invoices WHERE order_id = $1`, orderID)
			_, _ = graph.pool.Exec(bg, `DELETE FROM order_lines WHERE order_id = $1`, orderID)
			_, _ = graph.pool.Exec(bg, `DELETE FROM orders WHERE id = $1`, orderID)
		}
		_, _ = graph.pool.Exec(bg, `DELETE FROM quote_lines WHERE quote_id = $1`, quoteID)
		_, _ = graph.pool.Exec(bg, `DELETE FROM quotes WHERE id = $1`, quoteID)
		_, _ = graph.pool.Exec(bg, `DELETE FROM customers WHERE id = $1`, custID)
	})

	// CONVERT at the route: the order exists, unpaid, and the account's zero
	// day terms hold it from picking.
	conv := do(http.MethodPost, "/api/v1/quotes/"+quoteID+"/convert", "")
	if conv.Code != http.StatusCreated {
		t.Fatalf("convert answered %d: %s", conv.Code, conv.Body.String())
	}
	var converted quote.ConvertResult
	if err := json.Unmarshal(conv.Body.Bytes(), &converted); err != nil {
		t.Fatalf("decode convert: %v", err)
	}
	orderID = converted.OrderID
	pick := do(http.MethodPost, "/api/v1/orders/"+orderID+"/picking", "")
	if pick.Code != http.StatusConflict {
		t.Fatalf("the unpaid converted order's picking door answered %d, want 409 held: %s", pick.Code, pick.Body.String())
	}
	var refusal struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(pick.Body.Bytes(), &refusal); err != nil || refusal.Error != "held_from_picking" {
		t.Fatalf("the picking refusal carries %q (%v), want held_from_picking", refusal.Error, err)
	}

	// THE DESK'S PAYMENT at the route: the whole agreed total, cash.
	pay := do(http.MethodPost, "/api/v1/quotes/"+quoteID+"/order-payment",
		`{"method":"CASH","amount_cents":11300,"idempotency_key":"i9-hold-`+suffix+`"}`)
	if pay.Code != http.StatusOK {
		t.Fatalf("the desk payment answered %d: %s", pay.Code, pay.Body.String())
	}
	// THE LIFT, at the route: the order read's pay_hold block (the same read
	// the till's held card renders) says the hold is gone, and the picking
	// door no longer refuses with the hold's word. A desk paid order may have
	// advanced past picking on its own ladder, so the door's own ladder answer
	// is legal; the hold's word is not.
	read := do(http.MethodGet, "/api/v1/orders/"+orderID, "")
	if read.Code != http.StatusOK {
		t.Fatalf("the order read answered %d: %s", read.Code, read.Body.String())
	}
	var view struct {
		PayHold *struct {
			Held      bool  `json:"held_from_picking"`
			OwedCents int64 `json:"owed_cents"`
		} `json:"pay_hold"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode the order read: %v", err)
	}
	if view.PayHold == nil || view.PayHold.Held || view.PayHold.OwedCents != 0 {
		t.Fatalf("after the desk payment the pay_hold block reads %+v, want not held, owing nothing", view.PayHold)
	}
	again := do(http.MethodPost, "/api/v1/orders/"+orderID+"/picking", "")
	if again.Code != http.StatusOK {
		var againRefusal struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(again.Body.Bytes(), &againRefusal)
		if againRefusal.Error == "held_from_picking" {
			t.Fatalf("the picking door still refuses the desk paid order with the hold's word: %s", again.Body.String())
		}
	}
}
