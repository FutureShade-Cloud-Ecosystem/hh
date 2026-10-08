//go:build integration

package pos

// returns_review_integration_test.go, the Opus review of PR 277: each leg fails on the code the
// review read and holds on the fix.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/jackc/pgx/v5"
)

func (s *trentonStack) runningBalance(cust string) int64 {
	s.t.Helper()
	var open int64
	if err := s.pool.QueryRow(context.Background(), `SELECT COALESCE((SELECT balance_after FROM customer_transactions WHERE customer_id = $1::uuid ORDER BY occurred_at DESC, id DESC LIMIT 1), 0)`, cust).Scan(&open); err != nil {
		s.t.Fatalf("balance: %v", err)
	}
	return open
}

// P1-1: a swap whose leftover goes back on the account rail leaves the running balance lowered by
// exactly the leftover, and every row's balance_after chains from the row before.
func TestIntegration_Review_SwapAccountLeftoverSubledgerTies(t *testing.T) {
	s := newTrentonStack(t)
	cust := s.plantCustomer("Review account leftover", 30, 5000000)
	s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 4, TenderAccount)
	before := s.runningBalance(cust)
	conv := plantConvertedOrder(t, s, cust, trentonBranchID, fmt.Sprintf("#SO-RV-%d", time.Now().UnixNano()%1_000_000), 6, 1000, 6000, 780)
	s.plantInvoice(conv, cust, trentonBranchID, "PAID", "ORDER", 6780, 0)
	body := map[string]any{
		"idempotency_key": key(), "customer_id": cust, "reason": "review", "till_session_id": s.trentonTill,
		"orders": []any{map[string]any{"order_id": conv, "lines": []any{
			map[string]any{"order_line_id": s.lineOf(conv), "qty_base": 6, "reason_code": "OTHER", "disposition": "RESTOCK"}}}},
		"sale_type": SaleCollectNow, "tender": TenderCash,
		"lines": []any{map[string]any{"product_id": l2cProductID, "qty_entered": 2, "uom_entered": "SF"}},
	}
	rec := s.postSwap(trentonClerkSub, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("swap: %d %s", rec.Code, rec.Body.String())
	}
	res := s.decodeBody(rec)
	if res["leftover_method"] != RefundAccountCredit {
		t.Fatalf("an order a quote became goes back to the account: %v", res["leftover_method"])
	}
	leftover := int64(num(t, res, "leftover_cents"))
	if after := s.runningBalance(cust); after != before-leftover {
		t.Fatalf("the running balance after the swap is %d, want %d (before %d less the leftover %d)", after, before-leftover, before, leftover)
	}
	// The chain: each row's balance_after is the row before's plus its amount (credit memos carry a negative amount).
	rows, err := s.pool.Query(context.Background(), `SELECT txn_type, amount_cents, balance_after FROM customer_transactions WHERE customer_id = $1::uuid ORDER BY occurred_at, id`, cust)
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	defer rows.Close()
	var prev int64
	first := true
	for rows.Next() {
		var typ string
		var amt, bal int64
		if err := rows.Scan(&typ, &amt, &bal); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if !first {
			want := prev + amt
			if typ == "PAYMENT" {
				want = prev - amt
			}
			if bal != want {
				t.Fatalf("%s row of %d leaves %d, the chain says %d (the subledger does not tie)", typ, amt, bal, want)
			}
		}
		prev, first = bal, false
	}
}

// P1-2: notes slice one sale under the approval amount one after another; the cumulative on the
// sale is judged as the single return judges it, so the slice that crosses the amount is refused
// without an approver.
func TestIntegration_Review_NotesCannotSliceUnderTheApprovalAmount(t *testing.T) {
	s := newTrentonStack(t)
	customer := plantCreditCustomer(t, s, "Review slicing")
	sale := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", customer, "", 50, TenderCash)
	var codes []int
	for _, q := range []float64{20, 20, 10} {
		rec := s.postNote(trentonClerkSub, "", s.noteBody(key(), customer, RefundCash, []trentonSaleView{sale}, []float64{q}))
		codes = append(codes, rec.Code)
	}
	var total int64
	if err := s.pool.QueryRow(context.Background(), `SELECT COALESCE(SUM(total_cents),0) FROM pos_returns WHERE order_id = $1::uuid`, sale.OrderID).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total > 25000 {
		t.Fatalf("a clerk refunded %d cents of one sale without an approver against a 25000 amount (note answers %v)", total, codes)
	}
	if codes[1] != http.StatusConflict {
		t.Fatalf("the slice that crosses the amount must be refused 409 (approval required), got %v", codes)
	}
	// The single return meets the same cumulative.
	rec := s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", s.returnBody(key(), sale, 0, RefundCash, 10))
	if rec.Code != http.StatusConflict {
		t.Fatalf("the single path agrees with the note path, got %d", rec.Code)
	}
}

// P1-3: a card refund the processor made inside a rolled back transaction (UNBOOKED) counts against
// the order's ceiling, so a fresh key cannot refund the same card again; the original key still books it.
func TestIntegration_Review_AnUnbookedCardRefundCountsAgainstTheCapture(t *testing.T) {
	rail := newKeyedRail()
	s := newTrentonStackOnPoolWithRail(t, intPool(t), rail)
	cust := s.plantCustomer("Review unbooked", 30, 5000000)
	sale1 := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 3, TenderCard)
	sale2 := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 2, TenderCard)
	firstBody := s.noteBody(key(), cust, RefundCard, []trentonSaleView{sale1, sale2}, []float64{3, 2})
	rail.failKey = sale2.OrderID
	rec := s.postNote(trentonClerkSub, "", firstBody)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("first note: %d %s", rec.Code, rec.Body.String())
	}
	rail.failKey = ""
	calls := rail.calls
	rec = s.postNote(trentonClerkSub, "", s.noteBody(key(), cust, RefundCard, []trentonSaleView{sale1}, []float64{3}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a fresh key for goods the processor already refunded must be refused, got %d: %s", rec.Code, rec.Body.String())
	}
	if msg, _ := s.decodeBody(rec)["error"].(string); !containsStr(msg, "not booked") {
		t.Fatalf("the refusal names the refund that is not booked yet: %q", msg)
	}
	if rail.calls != calls {
		t.Fatal("the processor must not hear of the second refund")
	}
	// The original key books the refund the processor already made, once.
	rec = s.postNote(trentonClerkSub, "", firstBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("the original key books both orders, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(rail.byKey) != 2 {
		t.Fatalf("one refund per order at the processor, got %d", len(rail.byKey))
	}
}

// P2-2: cash is refused on an order whose invoice has taken nothing (the principal's ruling: no cash
// or card at all on an unpaid order); the account rail stays open there. The partly paid split is in
// returns_rulings_integration_test.go.
func TestIntegration_Review_CashAndCardNeedAPaidInvoice(t *testing.T) {
	s := newTrentonStackOnPoolWithRail(t, intPool(t), newKeyedRail())
	cust := s.plantCustomer("Review open invoice", 30, 5000000)
	conv := plantConvertedOrder(t, s, cust, trentonBranchID, fmt.Sprintf("#SO-OI-%d", time.Now().UnixNano()%1_000_000), 6, 1000, 6000, 780)
	s.plantInvoice(conv, cust, trentonBranchID, "FINALIZED", "ORDER", 6780, 6780)
	line := s.lineOf(conv)
	note := func(method string) *httptest.ResponseRecorder {
		b := map[string]any{"idempotency_key": key(), "customer_id": cust, "refund_method": method, "reason": "x", "restock": false,
			"till_session_id": s.trentonTill,
			"orders":          []any{map[string]any{"order_id": conv, "lines": []any{map[string]any{"order_line_id": line, "qty_base": 1}}}}}
		return s.postNote(trentonClerkSub, "", b)
	}
	rec := note(RefundCash)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("cash on an unpaid invoice must be refused, got %d: %s", rec.Code, rec.Body.String())
	}
	if msg, _ := s.decodeBody(rec)["error"].(string); !containsStr(msg, "not paid yet") {
		t.Fatalf("the refusal is the principal's words: %q", msg)
	}
	if rec := note(RefundAccountCredit); rec.Code != http.StatusCreated {
		t.Fatalf("the account rail stays open there, got %d: %s", rec.Code, rec.Body.String())
	}
	// The single return of a ring on account meets the same words.
	ring := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 4, TenderAccount)
	rec = s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", s.returnBody(key(), ring, 0, RefundCash, 1))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a cash return of an unpaid account ring must be refused, got %d: %s", rec.Code, rec.Body.String())
	}
}

// P2-3 (Q5): a credit only return of goods that never left the shelf reduces the order's open
// quantity through the order module, so the stock side shows less pending and the yard cannot pick
// the credited goods; and P3: goods that ship after a credit only return are still restockable.
func TestIntegration_Review_ACreditOnlyReturnReducesTheOpenLines(t *testing.T) {
	f := newDispFixture(t)
	o, l := f.ring(SaleCollectLater, 4)
	note, err := f.note(ReturnAcrossOrderInput{OrderID: o, Lines: []ReturnLineInput{{OrderLineID: l, QtyBase: 3, ReasonCode: "OTHER", Disposition: DispositionRestock}}})
	if err != nil {
		t.Fatalf("the note: %v", err)
	}
	if f.stock.qtyFor(note.Returns[0].ReturnID) != 0 {
		t.Fatal("credit only: no stock moves")
	}
	var open float64
	if err := f.pool.QueryRow(f.ctx, `SELECT (qty_base - fulfilled_base - released_base)::float8 FROM order_lines WHERE id = $1::uuid`, l).Scan(&open); err != nil || open != 1 {
		t.Fatalf("the order's open quantity after the credit is %.4f (%v), want 1", open, err)
	}
	pending, err := inventory.NewRepository(f.pool).PendingFulfilments(f.ctx, f.branchID)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	var seen float64
	for _, p := range pending {
		if p.OrderID == o {
			seen += p.QtyBase
		}
	}
	if seen != 1 {
		t.Fatalf("the stock side shows %.4f pending for the order, want 1", seen)
	}
	// The yard cannot pick the credited goods.
	orders := order.NewService(order.NewRepository(f.pool))
	if _, err := orders.ApplyYardPicks(f.ctx, order.YardPickInput{OrderID: o, Lines: []order.YardPickLineInput{{LineNo: 1, QtyPicked: "4"}}}); err == nil {
		t.Fatal("a pick of 4 against 1 still open must be refused")
	}
	if _, err := orders.ApplyYardPicks(f.ctx, order.YardPickInput{OrderID: o, Lines: []order.YardPickLineInput{{LineNo: 1, QtyPicked: "1"}}}); err != nil {
		t.Fatalf("a pick of the 1 still open is a full pick, no reason asked: %v", err)
	}
	// P3: the one open unit ships; a later RESTOCK of it goes back on the shelf.
	if _, err := f.pool.Exec(f.ctx, `UPDATE order_lines SET fulfilled_base = 1 WHERE id = $1::uuid`, l); err != nil {
		t.Fatal(err)
	}
	note2, err := f.note(ReturnAcrossOrderInput{OrderID: o, Lines: []ReturnLineInput{{OrderLineID: l, QtyBase: 1, ReasonCode: "OTHER", Disposition: DispositionRestock}}})
	if err != nil {
		t.Fatalf("the second note: %v", err)
	}
	if got := f.stock.qtyFor(note2.Returns[0].ReturnID); got != 1 {
		t.Fatalf("goods that shipped after a credit only return restock in full, the seam heard %.4f", got)
	}
}

// P2-1: a multi order account credit note takes its invoices before the shared invoice sequence
// row, as the till's reversal does; a payment holding a later order's invoice and then asking for the
// sequence row no longer closes a cycle with it.
func TestIntegration_Review_TheNoteLocksItsInvoicesBeforeTheSequenceRow(t *testing.T) {
	s := newTrentonStack(t)
	cust := s.plantCustomer("Review lock order", 30, 5000000)
	a := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 4, TenderAccount)
	b := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 4, TenderAccount)
	var invA, invB string
	for _, q := range []struct {
		order string
		dst   *string
	}{{a.OrderID, &invA}, {b.OrderID, &invB}} {
		if err := s.pool.QueryRow(context.Background(), `SELECT id::text FROM invoices WHERE order_id = $1::uuid`, q.order).Scan(q.dst); err != nil {
			t.Fatal(err)
		}
	}
	// The later invoice in lock order is the one the payment holds.
	held := invB
	if a.OrderID > b.OrderID {
		held = invA
	}
	ctx := context.Background()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM invoices WHERE id = $1::uuid FOR NO KEY UPDATE`, held); err != nil {
		t.Fatalf("hold the invoice: %v", err)
	}
	var wg sync.WaitGroup
	var noteCode int
	wg.Add(1)
	go func() {
		defer wg.Done()
		rec := s.postNote(trentonClerkSub, "", s.noteBody(key(), cust, RefundAccountCredit, []trentonSaleView{a, b}, []float64{4, 4}))
		noteCode = rec.Code
	}()
	time.Sleep(700 * time.Millisecond) // the note is now waiting on the held invoice
	// The payment side asks for the shared sequence row after its invoice, as the payments path does.
	_, seqErr := tx.Exec(ctx, `INSERT INTO scope_sequences (scope) VALUES ('invoices') ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1`)
	if seqErr == nil {
		seqErr = tx.Commit(ctx)
	} else {
		_ = tx.Rollback(ctx)
	}
	wg.Wait()
	if seqErr != nil {
		t.Fatalf("the payment side was chosen as the deadlock victim: %v", seqErr)
	}
	if noteCode != http.StatusCreated {
		t.Fatalf("the note must commit once the payment side is done, got %d", noteCode)
	}
}

type recordingStockLocker struct {
	mu    sync.Mutex
	calls [][]string
}

func (r *recordingStockLocker) LockStockTx(_ context.Context, _ pgx.Tx, _ string, ids []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, append([]string(nil), ids...))
	return nil
}

// P2-1 (stock): a swap takes the balance rows of every product either half moves, sorted and unique,
// once, ahead of both halves.
func TestIntegration_Review_ASwapPreLocksItsStockInOneOrder(t *testing.T) {
	s := newTrentonStack(t)
	locker := &recordingStockLocker{}
	s.svc.WithStockLocks(locker)
	cust := s.plantCustomer("Review stock locks", 30, 5000000)
	orig := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 4, TenderCash)
	rec := s.postSwap(trentonClerkSub, s.swapBody(key(), cust, orig, 4, 2, TenderCash))
	if rec.Code != http.StatusCreated {
		t.Fatalf("swap: %d %s", rec.Code, rec.Body.String())
	}
	if len(locker.calls) != 1 || len(locker.calls[0]) != 1 || locker.calls[0][0] != l2cProductID {
		t.Fatalf("one pre-lock over the sorted union of the products, got %v", locker.calls)
	}
}

// P3: the swap's key namespace is reserved against a public note.
func TestIntegration_Review_APublicNoteCannotSquatASwapKey(t *testing.T) {
	s := newTrentonStack(t)
	cust := s.plantCustomer("Review swap namespace", 30, 5000000)
	sale := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 4, TenderCash)
	rec := s.postNote(trentonClerkSub, "", s.noteBody("swap:"+key(), cust, RefundCash, []trentonSaleView{sale}, []float64{1}))
	if rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("a note keyed into the swap namespace is refused, got %d: %s", rec.Code, rec.Body.String())
	}
}
