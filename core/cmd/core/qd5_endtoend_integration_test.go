//go:build integration

// qd5_endtoend_integration_test.go, QD-5 review round 1 (P1-1): the counter's invoice must not
// move the order. The path is driven the way production drives it: the served routes over the real
// registerRoutes graph, and the outbox dispatcher registered exactly as worker.go registers it (the
// GL posting consumer as owner, the order lifecycle consumer as its observer), then the order walks
// confirm, pick, ready and the hand over through the same adapter dispatch uses.
//
//	Run: HH_CORE_TEST_DATABASE_URL='postgres://.../scratch?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run QD5E2E -count=1 -p 1
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

type qd5e2e struct {
	t     *testing.T
	env   *compEnv
	reg   *recordingRegistrar
	graph *appGraph
	pool  *pgxpool.Pool
	disp  *notify.Dispatcher
}

func newQD5E2E(t *testing.T) *qd5e2e {
	t.Helper()
	env := newCompEnv(t)
	reg, graph := registeredSurface(t, env)
	// Registered exactly as worker.go registers them: the GL posting consumer owns the financial
	// topics, the inventory consumer its own two, and the order lifecycle consumer observes after.
	d := notify.NewDispatcher(graph.money.outboxStore, graph.log)
	graph.money.glSvc.RegisterPostingHandlers(d)
	graph.money.inventorySvc.RegisterPostingHandlers(d)
	order.NewLifecycleConsumer(graph.money.orderSvc, graph.tops.billingRepo, graph.log).RegisterHandlers(d)
	return &qd5e2e{t: t, env: env, reg: reg, graph: graph, pool: graph.pool, disp: d}
}

// drain runs the dispatcher until the outbox holds nothing it can claim.
func (e *qd5e2e) drain() {
	e.t.Helper()
	for i := 0; i < 20; i++ {
		if e.disp.DrainOnce(context.Background()) == 0 {
			return
		}
	}
}

func (e *qd5e2e) do(sub, method, path string, body any) (int, []byte) {
	e.t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Authorization", "Bearer "+e.env.minter.StaffToken(e.t, sub))
	req.Header.Set(authz.HeaderXOrg, xrXOrg)
	req.Header.Set("Content-Type", "application/json")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	rsp := httptest.NewRecorder()
	e.reg.inner.ServeHTTP(rsp, req.WithContext(ctx))
	return rsp.Code, rsp.Body.Bytes()
}

// qd5e2eQuote is one accepted quote with a stocked line (3 units at 1000 each, 13 percent tax).
type qd5e2eQuote struct {
	QuoteID, CustomerID string
	Total               int64
}

// seedQuote plants a customer, on hand stock, and an ACCEPTED quote at the north branch A.
func (e *qd5e2e) seedQuote(creditLimit int64) qd5e2eQuote {
	e.t.Helper()
	ctx := context.Background()
	tag := fmt.Sprintf("QD5E-%d", time.Now().UnixNano())
	var q qd5e2eQuote
	q.Total = 3390
	if err := e.pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, credit_limit_cents) VALUES ($1::text,$1::text,'CONTRACTOR',$2) RETURNING id::text`,
		tag, creditLimit).Scan(&q.CustomerID); err != nil {
		e.t.Fatalf("seed customer: %v", err)
	}
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base, allocated_base)
		VALUES ($1, $2, $2, 1000, 0)
		ON CONFLICT (product_id, location_id) DO UPDATE SET on_hand_base = GREATEST(stock_balances.on_hand_base, 1000)`,
		testkit.ProductID, testkit.BranchNorthAID); err != nil {
		e.t.Fatalf("seed stock: %v", err)
	}
	if err := e.pool.QueryRow(ctx, `
		INSERT INTO quotes (quote_no, customer_id, branch_id, org_id, status, subtotal_cents, tax_cents, total_cents,
		                    delivery_fee_cents, fulfillment)
		VALUES ('#Q-' || $1::text, $2::uuid, $3::uuid, $4::uuid, 'ACCEPTED', 3000, 390, 3390, 0, 'PICKUP') RETURNING id::text`,
		tag, q.CustomerID, testkit.BranchNorthAID, testkit.OrgNorthID).Scan(&q.QuoteID); err != nil {
		e.t.Fatalf("seed quote: %v", err)
	}
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO quote_lines (quote_id, line_no, product_id, description, qty_entered, uom_entered, qty_base,
		                         unit_price_cents, price_uom, line_total_cents)
		VALUES ($1::uuid, 1, $2::uuid, 'fixture line', 3, 'EA', 3, 1000, 'EA', 3000)`,
		q.QuoteID, testkit.ProductID); err != nil {
		e.t.Fatalf("seed quote line: %v", err)
	}
	e.t.Cleanup(func() { e.cleanup(q) })
	return q
}

func (e *qd5e2e) cleanup(q qd5e2eQuote) {
	c := context.Background()
	orders := `SELECT id FROM orders WHERE quote_id = $1`
	invoices := `SELECT id FROM invoices WHERE customer_id = $2`
	for _, s := range []string{
		`DELETE FROM payment_allocations WHERE invoice_id IN (` + invoices + `)`,
		`DELETE FROM payments WHERE customer_id = $2`,
		`DELETE FROM customer_transactions WHERE customer_id = $2`,
		`DELETE FROM invoice_lines WHERE invoice_id IN (` + invoices + `)`,
		`UPDATE shipment_groups SET invoice_id = NULL WHERE order_id IN (` + orders + `)`,
		`DELETE FROM invoices WHERE customer_id = $2`,
		`DELETE FROM stock_reservations WHERE source_ref_id IN (` + orders + `) OR source_ref_id = $1::uuid`,
		`DELETE FROM shipment_group_lines WHERE shipment_group_id IN (SELECT id FROM shipment_groups WHERE order_id IN (` + orders + `))`,
		`DELETE FROM shipment_groups WHERE order_id IN (` + orders + `)`,
		`DELETE FROM audit_log WHERE entity_id IN (` + orders + `)`,
		`DELETE FROM quote_accept_provenance WHERE quote_id = $1`,
		`DELETE FROM order_lines WHERE order_id IN (` + orders + `)`,
		`DELETE FROM orders WHERE quote_id = $1`,
		`DELETE FROM audit_log WHERE entity_id = $1`,
		`DELETE FROM quote_lines WHERE quote_id = $1`,
		`DELETE FROM quotes WHERE id = $1`,
		`DELETE FROM customers WHERE id = $2`,
	} {
		_, _ = e.pool.Exec(c, s, q.QuoteID, q.CustomerID)
	}
}

func (e *qd5e2e) orderOf(q qd5e2eQuote) (id, status string) {
	e.t.Helper()
	if err := e.pool.QueryRow(context.Background(),
		`SELECT id::text, status FROM orders WHERE quote_id = $1`, q.QuoteID).Scan(&id, &status); err != nil {
		e.t.Fatalf("read the order of the quote: %v", err)
	}
	return id, status
}

// holds counts the order's stock holds by status and mode.
func (e *qd5e2e) holds(orderID string) map[string]int {
	e.t.Helper()
	rows, err := e.pool.Query(context.Background(),
		`SELECT status || '/' || hold_mode, count(*)::int FROM stock_reservations WHERE source_ref_id = $1::uuid GROUP BY 1`, orderID)
	if err != nil {
		e.t.Fatalf("read holds: %v", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			e.t.Fatal(err)
		}
		out[k] = n
	}
	return out
}

// closedAt snapshots when each closed hold of the order closed, so a later step that closes a hold
// again (a second consumption) shows as a changed stamp.
func (e *qd5e2e) closedAt(orderID string) map[string]time.Time {
	e.t.Helper()
	rows, err := e.pool.Query(context.Background(),
		`SELECT id::text, closed_at FROM stock_reservations WHERE source_ref_id = $1::uuid AND closed_at IS NOT NULL`, orderID)
	if err != nil {
		e.t.Fatalf("read closed holds: %v", err)
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var id string
		var at time.Time
		if err := rows.Scan(&id, &at); err != nil {
			e.t.Fatal(err)
		}
		out[id] = at
	}
	return out
}

// onlyActive reports whether every hold of the order is ACTIVE and HARD, and how many there are.
func onlyActiveHard(h map[string]int) (int, bool) {
	n := h["ACTIVE/HARD"]
	return n, n > 0 && len(h) == 1
}

func (e *qd5e2e) invoiceCount(q qd5e2eQuote) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM invoices WHERE customer_id = $1`, q.CustomerID).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *qd5e2e) orderStep(sub, orderID, step string, wantCode int) {
	e.t.Helper()
	code, body := e.do(sub, http.MethodPost, "/api/v1/orders/"+orderID+"/"+step, map[string]any{})
	if code != wantCode {
		e.t.Fatalf("order %s: status %d, want %d: %s", step, code, wantCode, body)
	}
}

func (e *qd5e2e) pay(sub, quoteID, method string, cents int64, key string) (int, map[string]any) {
	e.t.Helper()
	code, body := e.do(sub, http.MethodPost, "/api/v1/quotes/"+quoteID+"/order-payment", map[string]any{
		"method": method, "amount_cents": cents, "reference": "till 1", "idempotency_key": key,
	})
	var out map[string]any
	_ = json.Unmarshal(body, &out)
	if out == nil {
		out = map[string]any{"raw": string(body)}
	}
	return code, out
}

// handOver is the order's hand over: the adapter dispatch calls for a delivered stop or a collected
// will-call, the one sanctioned way the yard tells the order it left.
func (e *qd5e2e) handOver(orderID string) {
	e.t.Helper()
	if err := (dispatchFulfilAdapter{svc: e.graph.money.orderSvc}).MarkFulfilled(
		context.Background(), orderID, "will-call:qd5-e2e"); err != nil {
		e.t.Fatalf("hand over: %v", err)
	}
}

// TestQD5E2E_CashCustomerPaysAtTheCounterThenTheOrderWalksTheYardAndLeaves is the money path end to
// end: one action at the counter (convert, invoice, payment), the worker's dispatcher, then confirm,
// pick, ready and hand over. One invoice, the stock consumed exactly once, the order's final state.
func TestQD5E2E_CashCustomerPaysAtTheCounterThenTheOrderWalksTheYardAndLeaves(t *testing.T) {
	e := newQD5E2E(t)
	q := e.seedQuote(0)

	code, out := e.pay(testkit.SubInsideSales, q.QuoteID, "CASH", q.Total, "e2e-cash")
	if code != http.StatusOK {
		t.Fatalf("the counter payment: %d %v", code, out)
	}
	invoiceNo, _ := out["invoice_no"].(string)
	orderID, status := e.orderOf(q)
	if status != "DRAFT" {
		t.Fatalf("a fresh order is DRAFT, got %s", status)
	}

	// The worker's pass over the queued events.
	e.drain()
	if _, status = e.orderOf(q); status != "DRAFT" {
		t.Fatalf("the counter invoice must not advance the order: %s after the dispatcher, want DRAFT", status)
	}
	if _, ok := onlyActiveHard(e.holds(orderID)); !ok {
		t.Fatalf("the counter invoice must not consume the holds: %v", e.holds(orderID))
	}

	// The yard moves the order: confirm, pick, ready.
	e.orderStep(testkit.SubInsideSales, orderID, "confirm", http.StatusOK)
	e.orderStep(testkit.SubInsideSales, orderID, "picking", http.StatusOK)
	e.orderStep(testkit.SubInsideSales, orderID, "ready", http.StatusOK)
	if _, ok := onlyActiveHard(e.holds(orderID)); !ok {
		t.Fatalf("the holds stay ACTIVE through the yard: %v", e.holds(orderID))
	}

	// The hand over: the order leaves, its holds are consumed once, and the counter invoice that
	// is already there completes the order's move.
	e.handOver(orderID)
	e.drain()
	if _, status = e.orderOf(q); status != "INVOICED" {
		t.Fatalf("a paid, handed over order ends INVOICED, got %s", status)
	}
	h := e.holds(orderID)
	if h["CONSUMED/HARD"] == 0 || len(h) != 1 {
		t.Fatalf("the holds are consumed at the hand over: %v", h)
	}
	consumedAt := e.closedAt(orderID)

	// Billing the order when it leaves bills nothing: the writer answers the counter's document.
	tail := billing.NewInvoiceWriter(e.pool).WithOrderSource(w6cOrderInvoiceSource{svc: e.graph.money.orderSvc})
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE order_lines SET fulfilled_base = qty_base WHERE order_id = $1`, orderID); err != nil {
		t.Fatal(err)
	}
	doc, err := tail.InvoiceOrder(context.Background(), orderID)
	if err != nil {
		t.Fatalf("billing at fulfilment: %v", err)
	}
	if !doc.Reused || doc.InvoiceNo != invoiceNo {
		t.Fatalf("fulfilment must answer the counter's invoice %s, got %+v", invoiceNo, doc)
	}
	e.drain()
	if n := e.invoiceCount(q); n != 1 {
		t.Fatalf("invoices %d, want exactly one", n)
	}
	if _, status = e.orderOf(q); status != "INVOICED" {
		t.Fatalf("final state %s, want INVOICED", status)
	}
	after := e.closedAt(orderID)
	if len(after) != len(consumedAt) {
		t.Fatalf("holds closed %d times after fulfilment billing, %d at the hand over", len(after), len(consumedAt))
	}
	for id, at := range consumedAt {
		if !after[id].Equal(at) {
			t.Fatalf("hold %s was closed a second time", id)
		}
	}
}

// TestQD5E2E_DepositOnACreditCustomersConvertedQuoteLeavesTheOrderConfirmable is the deposit leg:
// the order stays DRAFT and confirmable with its holds ACTIVE and HARD after the dispatcher runs.
func TestQD5E2E_DepositOnACreditCustomersConvertedQuoteLeavesTheOrderConfirmable(t *testing.T) {
	e := newQD5E2E(t)
	q := e.seedQuote(1_000_000)

	if code, body := e.do(testkit.SubInsideSales, http.MethodPost, "/api/v1/quotes/"+q.QuoteID+"/convert", map[string]any{}); code != http.StatusCreated {
		t.Fatalf("convert: %d %s", code, body)
	}
	orderID, _ := e.orderOf(q)
	if code, out := e.pay(testkit.SubInsideSales, q.QuoteID, "CHEQUE", 1000, "e2e-deposit"); code != http.StatusOK {
		t.Fatalf("deposit: %d %v", code, out)
	}
	e.drain()
	if _, status := e.orderOf(q); status != "DRAFT" {
		t.Fatalf("a deposit leaves the order DRAFT, got %s", status)
	}
	if _, ok := onlyActiveHard(e.holds(orderID)); !ok {
		t.Fatalf("a deposit must not consume the holds: %v", e.holds(orderID))
	}
	e.orderStep(testkit.SubInsideSales, orderID, "confirm", http.StatusOK)
	if _, status := e.orderOf(q); status != "CONFIRMED" {
		t.Fatalf("the deposit order confirms, got %s", status)
	}
}

// convertAndConfirm turns the credit customer's accepted quote into a CONFIRMED order over the
// served routes and returns the order id.
func (e *qd5e2e) convertAndConfirm(q qd5e2eQuote) string {
	e.t.Helper()
	if code, body := e.do(testkit.SubInsideSales, http.MethodPost, "/api/v1/quotes/"+q.QuoteID+"/convert", map[string]any{}); code != http.StatusCreated {
		e.t.Fatalf("convert: %d %s", code, body)
	}
	orderID, _ := e.orderOf(q)
	e.orderStep(testkit.SubInsideSales, orderID, "confirm", http.StatusOK)
	return orderID
}

// TestQD5E2E_EveryOtherInvoiceSourceStillMovesTheOrderAsBefore pins, one subtest per source, that the
// consumer's skip is the counter's alone: the fulfilment invoice (ORDER), a shipment group invoice
// (MULTISHIP) and the POS ring's event (no source) each still move a CONFIRMED order to INVOICED and
// consume its holds, exactly as they did before QD-5.
func TestQD5E2E_EveryOtherInvoiceSourceStillMovesTheOrderAsBefore(t *testing.T) {
	for _, source := range []string{"ORDER", "MULTISHIP", "POS"} {
		t.Run(source, func(t *testing.T) {
			e := newQD5E2E(t)
			q := e.seedQuote(1_000_000)
			orderID := e.convertAndConfirm(q)
			ctx := context.Background()
			tail := billing.NewInvoiceWriter(e.pool).WithOrderSource(w6cOrderInvoiceSource{svc: e.graph.money.orderSvc})
			switch source {
			case "ORDER":
				if _, err := e.pool.Exec(ctx, `UPDATE order_lines SET fulfilled_base = qty_base WHERE order_id = $1`, orderID); err != nil {
					t.Fatal(err)
				}
				if _, err := tail.InvoiceOrder(ctx, orderID); err != nil {
					t.Fatalf("fulfilment invoice: %v", err)
				}
			case "MULTISHIP":
				var groupID string
				if err := e.pool.QueryRow(ctx, `
					WITH g AS (
					  INSERT INTO shipment_groups (order_id, group_no, status, completed_at)
					  VALUES ($1, 1, 'COMPLETE', now()) RETURNING id),
					l AS (
					  INSERT INTO shipment_group_lines (shipment_group_id, order_line_id, qty_base)
					  SELECT g.id, ol.id, ol.qty_base FROM g, order_lines ol WHERE ol.order_id = $1 RETURNING 1)
					SELECT g.id::text FROM g, (SELECT count(*) FROM l) n`, orderID).Scan(&groupID); err != nil {
					t.Fatalf("seed shipment group: %v", err)
				}
				if _, err := tail.InvoiceOrderGroups(ctx, orderID, []string{groupID}, "ON_ALL_COMPLETE"); err != nil {
					t.Fatalf("group invoice: %v", err)
				}
			case "POS":
				// The ring's own shape: a POS invoice row and the event it emits, which carries no source.
				var invID string
				if err := e.pool.QueryRow(ctx, `
					INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, status, due_date, subtotal_cents,
					                      tax_cents, total_cents, balance_cents, finalized_at, doc_type, source_kind)
					VALUES ('#INV-QD5E-' || nextval('invoice_no_seq'), $1, $2, $3, 'FINALIZED', CURRENT_DATE, 3000, 390, 3390, 0, now(), 'INVOICE', 'POS')
					RETURNING id::text`, q.CustomerID, orderID, testkit.BranchNorthAID).Scan(&invID); err != nil {
					t.Fatalf("seed POS invoice: %v", err)
				}
				if _, err := e.pool.Exec(ctx, `INSERT INTO events_outbox (topic, payload) VALUES ('ar.invoice.finalized', $1)`,
					map[string]any{"invoice_id": invID, "customer_id": q.CustomerID, "total_cents": 3390, "due_date": "2030-01-01"}); err != nil {
					t.Fatal(err)
				}
			}
			e.drain()
			if _, status := e.orderOf(q); status != "INVOICED" {
				t.Fatalf("a %s invoice moves the order to INVOICED as before, got %s", source, status)
			}
			if h := e.holds(orderID); h["CONSUMED/HARD"] == 0 || h["ACTIVE/HARD"] != 0 {
				t.Fatalf("a %s invoice consumes the holds as before: %v", source, h)
			}
		})
	}
}

// TestQD5E2E_ACounterInvoiceForAnOrderAlreadyFulfilledCompletesTheMove is the other direction: the goods
// have already left, so the counter's invoice event moves FULFILLED to INVOICED and nothing consumes a
// second time.
func TestQD5E2E_ACounterInvoiceForAnOrderAlreadyFulfilledCompletesTheMove(t *testing.T) {
	e := newQD5E2E(t)
	q := e.seedQuote(1_000_000)
	orderID := e.convertAndConfirm(q)
	e.handOver(orderID)
	if _, status := e.orderOf(q); status != "FULFILLED" {
		t.Fatalf("hand over: %s, want FULFILLED", status)
	}
	consumed := e.closedAt(orderID)
	if code, out := e.pay(testkit.SubInsideSales, q.QuoteID, "CASH", 1000, "e2e-late-deposit"); code != http.StatusOK {
		t.Fatalf("deposit after hand over: %d %v", code, out)
	}
	e.drain()
	if _, status := e.orderOf(q); status != "INVOICED" {
		t.Fatalf("a counter invoice for goods that already left moves the order on, got %s", status)
	}
	after := e.closedAt(orderID)
	if len(after) != len(consumed) {
		t.Fatalf("the holds were closed again: %d vs %d", len(after), len(consumed))
	}
}
