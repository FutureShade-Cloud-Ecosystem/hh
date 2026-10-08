//go:build integration

package pos

// returns_converted_integration_test.go, run C: an order a quote became is an order like any other
// (ruling 26, Q1), so the customer lines read's converted orders are returnable: the return context
// resolves them, their own invoice is filled and offset on the account rail, the preview prices them
// as the commit does, and the books balance beside a till ring in one note. An order with no live
// invoice yet says so in words (returnable false) instead of failing at the commit.

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

// plantInvoice plants the order's own invoice the way the conversion's payment path leaves it.
func (s *trentonStack) plantInvoice(orderID, customerID, branchID, status, sourceKind string, total, balance int64) string {
	s.t.Helper()
	var id string
	if err := s.pool.QueryRow(context.Background(), `
INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, status, due_date, subtotal_cents, tax_cents, total_cents, balance_cents,
                      doc_type, source_kind, finalized_at)
VALUES ($1, $2::uuid, $3::uuid, $4::uuid, $5, CURRENT_DATE + 30, $6, 0, $6, $7, 'INVOICE', $8, now()) RETURNING id::text`,
		fmt.Sprintf("#INV-CV-%d", time.Now().UnixNano()%1_000_000_000), customerID, orderID, branchID, status, total, balance, sourceKind).Scan(&id); err != nil {
		s.t.Fatalf("plant the order's invoice: %v", err)
	}
	return id
}

// A till ring and a converted order return together in one note on the account rail: the converted
// order's invoice is filled on its return and offset by the credit, the preview equals the commit,
// the books balance.
func TestIntegration_Converted_ReturnsBesideATillRing(t *testing.T) {
	s := newTrentonStack(t)
	cust := s.plantCustomer("Converted proof", 30, 5000000)
	ring := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 4, TenderAccount)
	conv := plantConvertedOrder(t, s, cust, trentonBranchID, fmt.Sprintf("#SO-CV-%d", time.Now().UnixNano()%1_000_000), 6, 1000, 6000, 780)
	inv := s.plantInvoice(conv, cust, trentonBranchID, "FINALIZED", "ORDER", 6780, 6780)

	// The read: the converted order is returnable and names its invoice; the ring too.
	code, body := s.customerLinesGet(t, trentonClerkSub, cust, "", 0)
	if code != http.StatusOK {
		t.Fatalf("the read: %d", code)
	}
	items := orderItems(t, body)
	co := findOrder(t, items, conv)
	if co["returnable"] != true || co["invoice_id"] != inv {
		t.Fatalf("the converted order with its invoice is returnable and names it: %v", co)
	}
	if findOrder(t, items, ring.OrderID)["returnable"] != true {
		t.Fatal("the till ring is returnable")
	}

	lineOf := func(orderID string) string {
		var id string
		if err := s.pool.QueryRow(context.Background(), `SELECT id::text FROM order_lines WHERE order_id = $1::uuid ORDER BY line_no LIMIT 1`, orderID).Scan(&id); err != nil {
			t.Fatalf("line: %v", err)
		}
		return id
	}
	orders := []any{
		map[string]any{"order_id": ring.OrderID, "lines": []any{map[string]any{"order_line_id": lineOf(ring.OrderID), "qty_base": 4}}},
		map[string]any{"order_id": conv, "lines": []any{map[string]any{"order_line_id": lineOf(conv), "qty_base": 6}}},
	}
	rec := s.postPreview(trentonClerkSub, map[string]any{"customer_id": cust, "orders": orders})
	if rec.Code != http.StatusOK {
		t.Fatalf("the preview must price both, got %d: %s", rec.Code, rec.Body.String())
	}
	preview := s.decodeBody(rec)
	rec = s.postNote(trentonClerkSub, "", map[string]any{
		"idempotency_key": key(), "customer_id": cust, "orders": orders, "refund_method": RefundAccountCredit,
		"reason": "converted proof", "restock": false, "till_session_id": s.trentonTill,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("the note over a ring and a converted order must commit, got %d: %s", rec.Code, rec.Body.String())
	}
	note := s.decodeBody(rec)
	if num(t, preview, "total_cents") != num(t, note, "total_cents") {
		t.Fatalf("preview %v and commit %v differ", preview["total_cents"], note["total_cents"])
	}
	// The converted child carries the order's invoice, and the credit offset it.
	var childInvoice string
	if err := s.pool.QueryRow(context.Background(), `SELECT invoice_id::text FROM pos_returns WHERE order_id = $1::uuid`, conv).Scan(&childInvoice); err != nil || childInvoice != inv {
		t.Fatalf("the converted return carries the order's invoice: %q (%v)", childInvoice, err)
	}
	var balance int64
	var status string
	if err := s.pool.QueryRow(context.Background(), `SELECT balance_cents, status FROM invoices WHERE id = $1::uuid`, inv).Scan(&balance, &status); err != nil {
		t.Fatalf("the invoice: %v", err)
	}
	if balance != 0 || status != "PAID" {
		t.Fatalf("the credit offsets the converted order's open invoice: balance %d status %s", balance, status)
	}

	// The books balance: every journal of the note's children balances, and the receivable is
	// credited by the note total.
	glSvc := gl.NewService(gl.NewRepository(s.pool)).WithInvoiceReader(billing.NewRepository(s.pool))
	d := notify.NewDispatcher(notify.NewPgOutboxStore(s.pool), nil)
	glSvc.RegisterPostingHandlers(d)
	drainAll(t, d)
	var keys []string
	for _, c := range note["returns"].([]any) {
		keys = append(keys, "pos.return.committed:"+c.(map[string]any)["return_id"].(string))
	}
	var dr, cr int64
	if err := s.pool.QueryRow(context.Background(), `
SELECT COALESCE(SUM(COALESCE(l.debit_cents,0)),0), COALESCE(SUM(COALESCE(l.credit_cents,0)),0)
  FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id WHERE j.idempotency_key = ANY($1::text[])`, keys).Scan(&dr, &cr); err != nil || dr != cr || dr != int64(num(t, note, "total_cents")) {
		t.Fatalf("the note's books: debits %d credits %d note %v (%v)", dr, cr, note["total_cents"], err)
	}
}

// A converted order that carried a supplier deposit releases the deposit exactly once on a return
// (rulings 16 and 17) and the credit reaches the deposit document the order's own invoice cannot absorb.
func TestIntegration_Converted_ASupplierDepositReleasesOnce(t *testing.T) {
	s := newTrentonStack(t)
	cust := s.plantCustomer("Converted deposit", 30, 5000000)
	conv := plantConvertedOrder(t, s, cust, trentonBranchID, fmt.Sprintf("#SO-CD-%d", time.Now().UnixNano()%1_000_000), 6, 1000, 6000, 780)
	s.plantInvoice(conv, cust, trentonBranchID, "PAID", "ORDER", 2780, 0)
	dep := s.plantInvoice(conv, cust, trentonBranchID, "FINALIZED", "SUPPLIER_DEPOSIT", 4000, 4000)
	var line string
	if err := s.pool.QueryRow(context.Background(), `SELECT id::text FROM order_lines WHERE order_id = $1::uuid`, conv).Scan(&line); err != nil {
		t.Fatalf("line: %v", err)
	}
	rec := s.postNote(trentonClerkSub, "", map[string]any{
		"idempotency_key": key(), "customer_id": cust, "refund_method": RefundAccountCredit, "reason": "x", "restock": false,
		"till_session_id": s.trentonTill,
		"orders":          []any{map[string]any{"order_id": conv, "lines": []any{map[string]any{"order_line_id": line, "qty_base": 2}}}},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("the converted deposit order must return, got %d: %s", rec.Code, rec.Body.String())
	}
	if n := s.countAll(`SELECT count(*) FROM events_outbox WHERE topic = 'deposit.released' AND payload->>'invoice_id' = $1`, dep); n != 1 {
		t.Fatalf("the deposit is released exactly once, got %d", n)
	}
}

// An order with no live invoice cannot be refunded to anything yet: the read says so in words and
// the commit refuses in the same words.
func TestIntegration_Converted_WithoutAnInvoiceSaysSo(t *testing.T) {
	s := newTrentonStack(t)
	cust := s.plantCustomer("Converted bare", 30, 5000000)
	bare := plantConvertedOrder(t, s, cust, trentonBranchID, fmt.Sprintf("#SO-CB-%d", time.Now().UnixNano()%1_000_000), 3, 1000, 3000, 390)
	draft := plantConvertedOrder(t, s, cust, trentonBranchID, fmt.Sprintf("#SO-CE-%d", time.Now().UnixNano()%1_000_000), 3, 1000, 3000, 390)
	s.plantInvoice(draft, cust, trentonBranchID, "DRAFT", "ORDER", 3390, 3390)

	code, body := s.customerLinesGet(t, trentonClerkSub, cust, "", 0)
	if code != http.StatusOK {
		t.Fatalf("read: %d", code)
	}
	for _, id := range []string{bare, draft} {
		o := findOrder(t, orderItems(t, body), id)
		if o["returnable"] != false {
			t.Fatalf("an order with no live invoice is not returnable: %v", o)
		}
		if msg, _ := o["not_returnable_reason"].(string); msg == "" {
			t.Fatalf("the read says why in words: %v", o)
		}
	}
	var line string
	_ = s.pool.QueryRow(context.Background(), `SELECT id::text FROM order_lines WHERE order_id = $1::uuid`, bare).Scan(&line)
	rec := s.postNote(trentonClerkSub, "", map[string]any{
		"idempotency_key": key(), "customer_id": cust, "refund_method": RefundCash, "reason": "x", "restock": false,
		"till_session_id": s.trentonTill,
		"orders":          []any{map[string]any{"order_id": bare, "lines": []any{map[string]any{"order_line_id": line, "qty_base": 1}}}},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("the commit refuses in words, got %d: %s", rec.Code, rec.Body.String())
	}
}
