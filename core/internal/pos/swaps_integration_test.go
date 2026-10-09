//go:build integration

package pos

// swaps_integration_test.go, sub item 10.5: the swap. One move: a credit note across orders and a new
// order in ONE transaction, the credit spent on the new order, the leftover returned by the original
// tender (Q7), the shortfall taken by an ordinary tender, the two documents linked both ways, server
// figures throughout (Q8).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

func (s *trentonStack) lineOf(orderID string) string {
	s.t.Helper()
	var id string
	if err := s.pool.QueryRow(context.Background(), `SELECT id::text FROM order_lines WHERE order_id = $1::uuid ORDER BY line_no LIMIT 1`, orderID).Scan(&id); err != nil {
		s.t.Fatalf("line of %s: %v", orderID, err)
	}
	return id
}

// swapBody builds a swap: return qty of the sale's line, buy newQty SF of the paver.
func (s *trentonStack) swapBody(k, customer string, sale trentonSaleView, retQty, newQty float64, tender string) map[string]any {
	s.t.Helper()
	body := map[string]any{
		"idempotency_key": k,
		"customer_id":     customer,
		"reason":          "swapped for another size",
		"till_session_id": s.trentonTill,
		"orders": []any{map[string]any{"order_id": sale.OrderID, "lines": []any{
			map[string]any{"order_line_id": s.lineOf(sale.OrderID), "qty_base": retQty, "reason_code": "OTHER", "disposition": "RESTOCK"}}}},
		"sale_type": SaleCollectNow,
		"tender":    tender,
		"lines":     []any{map[string]any{"product_id": l2cProductID, "qty_entered": newQty, "uom_entered": "SF"}},
	}
	if tender == TenderCard {
		body["payment_token"] = "tok_till_l2c_demo"
	}
	return body
}

func (s *trentonStack) postSwap(who string, body map[string]any) *httptest.ResponseRecorder {
	s.t.Helper()
	return s.do(http.MethodPost, "/api/v1/pos/swaps", s.bearer(who), "", body)
}

func (s *trentonStack) postSwapPreview(who string, body map[string]any) *httptest.ResponseRecorder {
	s.t.Helper()
	return s.do(http.MethodPost, "/api/v1/pos/swaps/preview", s.bearer(who), "", body)
}

// arNet is the receivable's net movement over the named journals.
func (s *trentonStack) glNet(code string, keys []string) int64 {
	s.t.Helper()
	var n int64
	if err := s.pool.QueryRow(context.Background(), `
SELECT COALESCE(SUM(COALESCE(l.debit_cents,0) - COALESCE(l.credit_cents,0)), 0)
  FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id JOIN gl_accounts a ON a.id = l.account_id
 WHERE j.idempotency_key = ANY($1::text[]) AND a.code = $2`, keys, code).Scan(&n); err != nil {
		s.t.Fatalf("glNet %s: %v", code, err)
	}
	return n
}

// drainGL posts every pending journal through the real consumer.
func (s *trentonStack) drainGL() {
	s.t.Helper()
	glSvc := gl.NewService(gl.NewRepository(s.pool)).WithInvoiceReader(billing.NewRepository(s.pool)).WithPaymentReader(till.NewRepository(s.pool))
	d := notify.NewDispatcher(notify.NewPgOutboxStore(s.pool), nil)
	glSvc.RegisterPostingHandlers(d)
	drainAll(s.t, d)
}

// swapKeys are every journal key a committed swap's documents post under.
func (s *trentonStack) swapKeys(res map[string]any) []string {
	s.t.Helper()
	var keys []string
	note := res["note"].(map[string]any)
	for _, c := range note["returns"].([]any) {
		id := c.(map[string]any)["return_id"].(string)
		keys = append(keys, "pos.return.committed:"+id, "order.returned:"+id, "pos.return.written_off:"+id)
	}
	sale := res["sale"].(map[string]any)
	keys = append(keys, "ar.invoice.finalized:"+sale["invoice_id"].(string))
	if pid, _ := sale["payment_id"].(string); pid != "" {
		keys = append(keys, "payment.received:"+pid)
	}
	return keys
}

// The credit covers the new order with money left over: the leftover goes back to the original card,
// ONE processor refund for exactly the leftover; the new order is paid by the credit; both documents
// point at each other; the books balance and the receivable nets to nothing.
func TestIntegration_Swap_TheLeftoverGoesBackToTheOriginalCard(t *testing.T) {
	rail := newKeyedRail()
	s := newTrentonStackOnPoolWithRail(t, intPool(t), rail)
	cust := s.plantCustomer("Swap card leftover", 30, 5000000)
	orig := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 10, TenderCard)
	orders := s.countAll(`SELECT count(*) FROM orders WHERE customer_id = $1::uuid`, cust)

	body := s.swapBody(key(), cust, orig, 10, 4, TenderCash)
	prevRec := s.postSwapPreview(trentonClerkSub, body)
	if prevRec.Code != http.StatusOK {
		t.Fatalf("the preview must answer, got %d: %s", prevRec.Code, prevRec.Body.String())
	}
	preview := s.decodeBody(prevRec)
	if s.countAll(`SELECT count(*) FROM orders WHERE customer_id = $1::uuid`, cust) != orders || len(rail.byKey) != 0 {
		t.Fatal("the preview wrote an order or reached the processor")
	}

	rec := s.postSwap(trentonClerkSub, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("the swap must commit, got %d: %s", rec.Code, rec.Body.String())
	}
	res := s.decodeBody(rec)
	credit, applied, leftover, shortfall := num(t, res, "credit_cents"), num(t, res, "applied_cents"), num(t, res, "leftover_cents"), num(t, res, "shortfall_cents")
	sale := res["sale"].(map[string]any)
	newTotal := num(t, sale, "total_cents")
	if applied != newTotal || leftover != credit-newTotal || shortfall != 0 || leftover <= 0 {
		t.Fatalf("credit %v applied %v leftover %v shortfall %v new order %v", credit, applied, leftover, shortfall, newTotal)
	}
	for k, want := range map[string]float64{"applied_cents": applied, "leftover_cents": leftover, "shortfall_cents": shortfall} {
		if num(t, preview, k) != want {
			t.Fatalf("the preview and the commit differ on %s: %v vs %v", k, preview[k], want)
		}
	}
	if num(t, preview["credit"].(map[string]any), "total_cents") != credit {
		t.Fatalf("the preview's credit and the commit's differ: %v vs %v", preview["credit"], credit)
	}
	if num(t, preview["new_order"].(map[string]any), "total_cents") != newTotal {
		t.Fatalf("the preview prices the new order as the ring does: %v vs %v", preview["new_order"], newTotal)
	}
	if res["leftover_method"] != RefundCard {
		t.Fatalf("the leftover goes back by the original tender: %v", res["leftover_method"])
	}

	// ONE processor refund, exactly the leftover.
	if len(rail.byKey) != 1 || rail.refundedTotal() != int64(leftover) {
		t.Fatalf("the processor holds %d refunds totaling %d, want one of %v", len(rail.byKey), rail.refundedTotal(), leftover)
	}
	// The two documents point at each other.
	var linkedNote, linkedOrder string
	if err := s.pool.QueryRow(context.Background(), `SELECT note_id::text, order_id::text FROM pos_swaps WHERE idempotency_key = $1`, body["idempotency_key"]).Scan(&linkedNote, &linkedOrder); err != nil {
		t.Fatalf("the swap link: %v", err)
	}
	note := res["note"].(map[string]any)
	if linkedNote != note["note_id"] || linkedOrder != sale["order_id"] || res["swap_id"] == "" || note["swap_id"] != res["swap_id"] || sale["swap_id"] != res["swap_id"] {
		t.Fatalf("the credit note and the new order point at each other: %v", res)
	}
	// The new order is paid in full by the credit; the customer's account is back where it was.
	var status string
	var balance int64
	if err := s.pool.QueryRow(context.Background(), `SELECT status, balance_cents FROM invoices WHERE id = $1::uuid`, sale["invoice_id"]).Scan(&status, &balance); err != nil || status != "PAID" || balance != 0 {
		t.Fatalf("the new order's invoice: %s %d (%v)", status, balance, err)
	}
	var open int64
	if err := s.pool.QueryRow(context.Background(), `SELECT COALESCE((SELECT balance_after FROM customer_transactions WHERE customer_id = $1::uuid ORDER BY occurred_at DESC, id DESC LIMIT 1), 0)`, cust).Scan(&open); err != nil || open != 0 {
		t.Fatalf("the customer's account nets to nothing, got %d (%v)", open, err)
	}
	// The books balance and the receivable nets to zero across the swap's journals.
	s.drainGL()
	keys := s.swapKeys(res)
	var dr, cr int64
	if err := s.pool.QueryRow(context.Background(), `
SELECT COALESCE(SUM(COALESCE(l.debit_cents,0)),0), COALESCE(SUM(COALESCE(l.credit_cents,0)),0)
  FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id WHERE j.idempotency_key = ANY($1::text[])`, keys).Scan(&dr, &cr); err != nil || dr != cr || dr == 0 {
		t.Fatalf("the swap's books: debits %d credits %d (%v)", dr, cr, err)
	}
	if n := s.glNet("1100", keys); n != 0 {
		t.Fatalf("the receivable nets %d across the swap, want 0", n)
	}
	if n := s.glNet("1010", keys); n != -int64(leftover) {
		t.Fatalf("card clearing is credited exactly the leftover, got %d", n)
	}
}

// The credit is smaller than the new order: the difference is taken by an ordinary tender at the till.
func TestIntegration_Swap_TheShortfallIsTakenInCash(t *testing.T) {
	s := newTrentonStack(t)
	cust := s.plantCustomer("Swap shortfall", 30, 5000000)
	orig := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 4, TenderCash)
	body := s.swapBody(key(), cust, orig, 4, 10, TenderCash)
	rec := s.postSwap(trentonClerkSub, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("the swap must commit, got %d: %s", rec.Code, rec.Body.String())
	}
	res := s.decodeBody(rec)
	credit, applied, leftover, shortfall := num(t, res, "credit_cents"), num(t, res, "applied_cents"), num(t, res, "leftover_cents"), num(t, res, "shortfall_cents")
	newTotal := num(t, res["sale"].(map[string]any), "total_cents")
	if applied != credit || leftover != 0 || shortfall != newTotal-credit || shortfall <= 0 {
		t.Fatalf("credit %v applied %v leftover %v shortfall %v new %v", credit, applied, leftover, shortfall, newTotal)
	}
	var paid int64
	if err := s.pool.QueryRow(context.Background(), `SELECT COALESCE(SUM(amount_cents),0) FROM payments WHERE customer_id = $1::uuid AND status = 'RECEIVED'
 AND id = $2::uuid`, cust, res["sale"].(map[string]any)["payment_id"]).Scan(&paid); err != nil || paid != int64(shortfall) {
		t.Fatalf("the till took exactly the difference: %d (%v)", paid, err)
	}
	s.drainGL()
	keys := s.swapKeys(res)
	if n := s.glNet("1100", keys); n != 0 {
		t.Fatalf("the receivable nets %d, want 0", n)
	}
	// The shortfall on the account instead: the new invoice stays open for exactly the difference.
	orig2 := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 4, TenderCash)
	rec = s.postSwap(trentonClerkSub, s.swapBody(key(), cust, orig2, 4, 10, TenderAccount))
	if rec.Code != http.StatusCreated {
		t.Fatalf("the on account shortfall must commit, got %d: %s", rec.Code, rec.Body.String())
	}
	res2 := s.decodeBody(rec)
	var status string
	var bal int64
	if err := s.pool.QueryRow(context.Background(), `SELECT status, balance_cents FROM invoices WHERE id = $1::uuid`, res2["sale"].(map[string]any)["invoice_id"]).Scan(&status, &bal); err != nil || status != "PARTIALLY_PAID" || bal != int64(num(t, res2, "shortfall_cents")) {
		t.Fatalf("the open invoice carries the difference: %s %d (%v)", status, bal, err)
	}
}

// A replay of the swap's key answers the first result: no second refund, no second order, no second note.
func TestIntegration_Swap_AReplayAnswersTheFirstResult(t *testing.T) {
	rail := newKeyedRail()
	s := newTrentonStackOnPoolWithRail(t, intPool(t), rail)
	cust := s.plantCustomer("Swap replay", 30, 5000000)
	orig := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 10, TenderCard)
	body := s.swapBody(key(), cust, orig, 10, 4, TenderCash)
	rec := s.postSwap(trentonClerkSub, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("first: %d %s", rec.Code, rec.Body.String())
	}
	first := s.decodeBody(rec)
	orders := s.countAll(`SELECT count(*) FROM orders WHERE customer_id = $1::uuid`, cust)
	calls := rail.calls
	rec = s.postSwap(trentonClerkSub, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("the replay answers 200, got %d: %s", rec.Code, rec.Body.String())
	}
	again := s.decodeBody(rec)
	if again["replayed"] != true || again["swap_id"] != first["swap_id"] ||
		again["sale"].(map[string]any)["order_id"] != first["sale"].(map[string]any)["order_id"] ||
		again["note"].(map[string]any)["note_id"] != first["note"].(map[string]any)["note_id"] {
		t.Fatalf("the replay answers the first result: %v", again)
	}
	if s.countAll(`SELECT count(*) FROM orders WHERE customer_id = $1::uuid`, cust) != orders || rail.calls != calls || len(rail.byKey) != 1 {
		t.Fatal("the replay wrote an order or reached the processor again")
	}
}

// A processor failure on the leftover rolls back the new order and the credit note, leaves the
// processor's refunds on record when it made any, and the retry books everything once.
func TestIntegration_Swap_AProcessorFailureRollsBackBothDocuments(t *testing.T) {
	rail := newKeyedRail()
	s := newTrentonStackOnPoolWithRail(t, intPool(t), rail)
	cust := s.plantCustomer("Swap rollback", 30, 5000000)
	orig := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 10, TenderCard)
	orders := s.countAll(`SELECT count(*) FROM orders WHERE customer_id = $1::uuid`, cust)
	body := s.swapBody(key(), cust, orig, 10, 4, TenderCash)
	rail.failKey = orig.OrderID
	rec := s.postSwap(trentonClerkSub, body)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("the leftover refund fails 502, got %d: %s", rec.Code, rec.Body.String())
	}
	if s.countAll(`SELECT count(*) FROM orders WHERE customer_id = $1::uuid`, cust) != orders ||
		s.countAll(`SELECT count(*) FROM return_notes WHERE customer_id = $1::uuid`, cust) != 0 ||
		s.countAll(`SELECT count(*) FROM pos_swaps WHERE customer_id = $1::uuid`, cust) != 0 {
		t.Fatal("a failed swap left an order, a note or a swap behind")
	}
	rail.failKey = ""
	rec = s.postSwap(trentonClerkSub, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("the retry must commit, got %d: %s", rec.Code, rec.Body.String())
	}
	res := s.decodeBody(rec)
	if len(rail.byKey) != 1 || rail.refundedTotal() != int64(num(t, res, "leftover_cents")) {
		t.Fatalf("the processor holds one refund for exactly the leftover: %v", rail.amounts)
	}
}

// The words: a source order whose invoice is still open, the walk-in, a leftover rail that is not the
// original tender, a leftover on an account that cannot spend it, and no new lines.
func TestIntegration_Swap_RefusalsInWords(t *testing.T) {
	s := newTrentonStackOnPoolWithRail(t, intPool(t), newKeyedRail())
	cust := s.plantCustomer("Swap refusals", 30, 5000000)
	open := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 4, TenderAccount)
	rec := s.postSwap(trentonClerkSub, s.swapBody(key(), cust, open, 4, 2, TenderCash))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an order with an open invoice cannot be swapped, got %d: %s", rec.Code, rec.Body.String())
	}
	if msg, _ := s.decodeBody(rec)["error"].(string); !containsStr(msg, "invoice") {
		t.Fatalf("the refusal names the open invoice: %q", msg)
	}

	paid := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 10, TenderCash)
	bad := s.swapBody(key(), cust, paid, 10, 2, TenderCash)
	bad["leftover_method"] = RefundCard
	rec = s.postSwap(trentonClerkSub, bad)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a leftover to a card the order was not paid with is refused, got %d: %s", rec.Code, rec.Body.String())
	}
	if msg, _ := s.decodeBody(rec)["error"].(string); !containsStr(msg, "original tender") {
		t.Fatalf("the refusal names the original tender: %q", msg)
	}

	walk := walkInID(t, s.pool)
	rec = s.postSwap(trentonClerkSub, s.swapBody(key(), walk, paid, 2, 2, TenderCash))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("the walk-in cannot swap, got %d: %s", rec.Code, rec.Body.String())
	}
	noLines := s.swapBody(key(), cust, paid, 2, 2, TenderCash)
	noLines["lines"] = []any{}
	rec = s.postSwap(trentonClerkSub, noLines)
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a swap with nothing bought is refused, got %d", rec.Code)
	}
	// Nothing was written by any refusal.
	if s.countAll(`SELECT count(*) FROM pos_swaps WHERE customer_id = $1::uuid`, cust) != 0 {
		t.Fatal("a refusal wrote a swap")
	}
}

// The dispositions run in the swap's own transaction (a damaged line is written off, a restocked
// line reverses COGS), and a card shortfall is captured by the ring's own card path; the books of
// the whole move balance.
func TestIntegration_Swap_DispositionsAndACardShortfall(t *testing.T) {
	s := newTrentonStack(t)
	cust := s.plantCustomer("Swap dispositions", 30, 5000000)
	orig := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 4, TenderCash)
	body := s.swapBody(key(), cust, orig, 4, 10, TenderCard)
	body["orders"].([]any)[0].(map[string]any)["lines"].([]any)[0].(map[string]any)["disposition"] = "DAMAGED"
	rec := s.postSwap(trentonClerkSub, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("the swap must commit, got %d: %s", rec.Code, rec.Body.String())
	}
	res := s.decodeBody(rec)
	child := res["note"].(map[string]any)["returns"].([]any)[0].(map[string]any)["return_id"].(string)
	if s.countAll(`SELECT count(*) FROM events_outbox WHERE topic = 'pos.return.written_off' AND payload->>'return_id' = $1`, child) != 1 ||
		s.countAll(`SELECT count(*) FROM events_outbox WHERE topic = 'order.returned' AND payload->>'return_id' = $1`, child) != 0 {
		t.Fatal("the damaged line is written off in the swap's transaction and reverses no COGS to inventory")
	}
	sale := res["sale"].(map[string]any)
	if sale["tender"] != TenderCard || sale["gateway_ref"] == "" {
		t.Fatalf("the shortfall rode the card path: %v", sale)
	}
	var paid int64
	if err := s.pool.QueryRow(context.Background(), `SELECT amount_cents FROM payments WHERE id = $1::uuid`, sale["payment_id"]).Scan(&paid); err != nil || paid != int64(num(t, res, "shortfall_cents")) {
		t.Fatalf("the card was charged exactly the shortfall: %d (%v)", paid, err)
	}
	s.drainGL()
	keys := s.swapKeys(res)
	var dr, cr int64
	if err := s.pool.QueryRow(context.Background(), `
SELECT COALESCE(SUM(COALESCE(l.debit_cents,0)),0), COALESCE(SUM(COALESCE(l.credit_cents,0)),0)
  FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id WHERE j.idempotency_key = ANY($1::text[])`, keys).Scan(&dr, &cr); err != nil || dr != cr || dr == 0 {
		t.Fatalf("the books: debits %d credits %d (%v)", dr, cr, err)
	}
	if n := s.glNet("1100", keys); n != 0 {
		t.Fatalf("the receivable nets %d, want 0", n)
	}
}
