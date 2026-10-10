//go:build integration

package pos

// returns_rails_integration_test.go, sub item 10.4: the refund rails across orders. D2 (credit never
// lands on the house WALK-IN row), D3 by the Q6 ruling (the account rail is refused in words where
// the credit cannot be spent), D5 (a card refund never exceeds what the original capture holds less
// what earlier refunds took) and D4 (a processor refund whose transaction then rolls back leaves a
// record a manager can see, and the retry books it without refunding twice, so the books and the
// processor agree).

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// keyedRail is a card rail that behaves like a processor honoring idempotency keys: the same key
// answers the same reversal and refunds nothing twice. failKey, when set, refuses the refund whose
// idempotency key contains it, after the earlier keys have been refunded for real.
type keyedRail struct {
	mu      sync.Mutex
	byKey   map[string]string
	amounts map[string]int64
	failKey string
	calls   int
}

func newKeyedRail() *keyedRail {
	return &keyedRail{byKey: map[string]string{}, amounts: map[string]int64{}}
}

func (r *keyedRail) RefundCard(_ context.Context, _ pgx.Tx, _ string, amountCents int64, _ string, idempotencyKey string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if ref, ok := r.byKey[idempotencyKey]; ok {
		return ref, nil
	}
	if r.failKey != "" && containsStr(idempotencyKey, r.failKey) {
		return "", fmt.Errorf("the processor refused the reversal")
	}
	ref := fmt.Sprintf("rf_%d", len(r.byKey)+1)
	r.byKey[idempotencyKey] = ref
	r.amounts[idempotencyKey] = amountCents
	return ref, nil
}

func (r *keyedRail) refundedTotal() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	var n int64
	for _, a := range r.amounts {
		n += a
	}
	return n
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func (s *trentonStack) plantCustomer(name string, termsDays int, creditLimit int64) string {
	s.t.Helper()
	var id string
	if err := s.pool.QueryRow(context.Background(), `
INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
VALUES ($1, $2, 'CONTRACTOR', $3, $4) RETURNING id::text`,
		fmt.Sprintf("RL-%d", time.Now().UnixNano()%1_000_000_000), name, termsDays, creditLimit).Scan(&id); err != nil {
		s.t.Fatalf("plant the customer: %v", err)
	}
	return id
}

func (s *trentonStack) txnCount(customerID string) int64 {
	return s.countAll(`SELECT count(*) FROM customer_transactions WHERE customer_id = $1::uuid`, customerID)
}

// D2: an account credit return on a walk-in sale is refused in words (the credit would land on the
// one shared WALK-IN row any walk-in could in principle spend); the same sale still refunds in cash.
func TestIntegration_Rails_TheWalkInHoldsNoAccountCredit(t *testing.T) {
	s := newTrentonStack(t)
	walkIn := walkInID(t, s.pool)
	sale := s.ringSale(key(), trentonBranchID, s.trentonTill, "trenton", []float64{4}, TenderCash)
	before := s.txnCount(walkIn)
	returnsBefore := s.countAll(`SELECT count(*) FROM pos_returns WHERE order_id = $1::uuid`, sale.OrderID)

	rec := s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", s.returnBody(key(), sale, 0, RefundAccountCredit, 2))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an account credit on a walk-in sale must be refused 422, got %d: %s", rec.Code, rec.Body.String())
	}
	msg, _ := s.decodeBody(rec)["error"].(string)
	if !containsStr(msg, "WALK-IN") || !containsStr(msg, "cash") {
		t.Fatalf("the refusal names the walk-in account and what to do instead: %q", msg)
	}
	if s.txnCount(walkIn) != before || s.countAll(`SELECT count(*) FROM pos_returns WHERE order_id = $1::uuid`, sale.OrderID) != returnsBefore {
		t.Fatal("the refused return wrote a credit or a return")
	}
	// The same sale refunds in cash.
	rec = s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", s.returnBody(key(), sale, 0, RefundCash, 2))
	if rec.Code != http.StatusCreated {
		t.Fatalf("the cash refund of a walk-in sale stays open, got %d: %s", rec.Code, rec.Body.String())
	}
}

// D3 by the Q6 ruling: there is no store credit wallet, so the account rail is refused in words for
// an account that cannot spend a credit (no terms or no credit line), on the single return and on
// the note alike, and works for an account that can.
func TestIntegration_Rails_TheAccountRailIsRefusedWhereItCannotBeSpent(t *testing.T) {
	s := newTrentonStack(t)
	noLine := s.plantCustomer("Rails no credit line", 30, 0)
	noTerms := s.plantCustomer("Rails no terms", 0, 5000000)
	good := s.plantCustomer("Rails credit account", 30, 5000000)

	for name, cust := range map[string]string{"no credit line": noLine, "no terms": noTerms} {
		sale := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 4, TenderCash)
		before := s.txnCount(cust)
		rec := s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", s.returnBody(key(), sale, 0, RefundAccountCredit, 2))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: the single return must be refused 422, got %d: %s", name, rec.Code, rec.Body.String())
		}
		if msg, _ := s.decodeBody(rec)["error"].(string); !containsStr(msg, "cannot spend") {
			t.Fatalf("%s: the refusal says why in words: %q", name, msg)
		}
		rec = s.postNote(trentonClerkSub, "", s.noteBody(key(), cust, RefundAccountCredit, []trentonSaleView{sale}, []float64{2}))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: the note must be refused 422, got %d: %s", name, rec.Code, rec.Body.String())
		}
		if s.txnCount(cust) != before {
			t.Fatalf("%s: the refused rails wrote a credit", name)
		}
		// The cash rail stays open for the same sale.
		rec = s.postNote(trentonClerkSub, "", s.noteBody(key(), cust, RefundCash, []trentonSaleView{sale}, []float64{2}))
		if rec.Code != http.StatusCreated {
			t.Fatalf("%s: cash stays open, got %d: %s", name, rec.Code, rec.Body.String())
		}
	}

	// An account that can spend takes the credit: a note over two orders chains the running balance
	// and drops the account's balance by exactly the note total.
	sale1 := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", good, "", 4, TenderAccount)
	sale2 := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", good, "", 4, TenderAccount)
	var balanceBefore int64
	if err := s.pool.QueryRow(context.Background(), `SELECT COALESCE((SELECT balance_after FROM customer_transactions WHERE customer_id = $1::uuid ORDER BY occurred_at DESC, id DESC LIMIT 1), 0)`, good).Scan(&balanceBefore); err != nil {
		t.Fatalf("balance before: %v", err)
	}
	rec := s.postNote(trentonClerkSub, "", s.noteBody(key(), good, RefundAccountCredit, []trentonSaleView{sale1, sale2}, []float64{4, 4}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("the credit account's note must commit, got %d: %s", rec.Code, rec.Body.String())
	}
	note := s.decodeBody(rec)
	var balanceAfter int64
	if err := s.pool.QueryRow(context.Background(), `SELECT balance_after FROM customer_transactions WHERE customer_id = $1::uuid ORDER BY occurred_at DESC, id DESC LIMIT 1`, good).Scan(&balanceAfter); err != nil {
		t.Fatalf("balance after: %v", err)
	}
	if balanceBefore-balanceAfter != int64(num(t, note, "total_cents")) {
		t.Fatalf("the account's balance drops by exactly the note total: %d to %d, note %v", balanceBefore, balanceAfter, note["total_cents"])
	}
}

// D5: a card refund never exceeds what the original capture holds less what earlier refunds took.
// The capture is cut to a part of the sale's total (as a split tender leaves it); returns inside the
// cap refund, the one that would cross it is refused in words before the processor hears of it, and
// the rest of the sale still refunds in cash.
func TestIntegration_Rails_ACardRefundNeverExceedsTheCapture(t *testing.T) {
	rail := newKeyedRail()
	s := newTrentonStackOnPoolWithRail(t, intPool(t), rail)
	cust := s.plantCustomer("Rails card cap", 30, 5000000)
	sale := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 10, TenderCard)
	if _, err := s.pool.Exec(context.Background(), `
UPDATE payments SET amount_cents = 6000 WHERE id = (SELECT payment_id FROM pos_sale WHERE order_id = $1::uuid)`, sale.OrderID); err != nil {
		t.Fatalf("cut the capture: %v", err)
	}

	rec := s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", s.returnBody(key(), sale, 0, RefundCard, 4))
	if rec.Code != http.StatusCreated {
		t.Fatalf("4 SF is inside the 60.00 the card holds (45.20), got %d: %s", rec.Code, rec.Body.String())
	}
	first := rail.refundedTotal()
	callsBefore := rail.calls
	rec = s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", s.returnBody(key(), sale, 0, RefundCard, 3))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("45.20 and 33.90 cross the 60.00 capture and must be refused 422, got %d: %s", rec.Code, rec.Body.String())
	}
	if msg, _ := s.decodeBody(rec)["error"].(string); !containsStr(msg, "capture") {
		t.Fatalf("the refusal names the capture in words: %q", msg)
	}
	if rail.calls != callsBefore || rail.refundedTotal() != first {
		t.Fatal("the processor must not hear of a refund the capture cannot cover")
	}
	// The same return in cash goes through, and a note over the card cap is refused whole.
	rec = s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", s.returnBody(key(), sale, 0, RefundCash, 3))
	if rec.Code != http.StatusCreated {
		t.Fatalf("the cash refund stays open, got %d: %s", rec.Code, rec.Body.String())
	}
	sale2 := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 10, TenderCard)
	if _, err := s.pool.Exec(context.Background(), `
UPDATE payments SET amount_cents = 6000 WHERE id = (SELECT payment_id FROM pos_sale WHERE order_id = $1::uuid)`, sale2.OrderID); err != nil {
		t.Fatalf("cut the second capture: %v", err)
	}
	sale3 := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 2, TenderCard)
	callsBefore = rail.calls
	rec = s.postNote(trentonClerkSub, "", s.noteBody(key(), cust, RefundCard, []trentonSaleView{sale3, sale2}, []float64{2, 10}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a note whose second order crosses its capture is refused whole, got %d: %s", rec.Code, rec.Body.String())
	}
	if rail.calls != callsBefore {
		t.Fatal("no order of a refused note reaches the processor: the caps are judged before any rail")
	}
}

// D4: the processor refunds the first order of a card note, the second order's refund fails, the
// transaction rolls back: the books hold nothing, the processor holds a refund. A record of that
// refund stands where a manager can see it, and the same note retried books it WITHOUT a second
// refund at the processor: afterwards the books and the processor agree to the cent.
func TestIntegration_Rails_ARolledBackCardRefundIsRecordedAndTheRetryBooksItOnce(t *testing.T) {
	rail := newKeyedRail()
	s := newTrentonStackOnPoolWithRail(t, intPool(t), rail)
	cust := s.plantCustomer("Rails card rollback", 30, 5000000)
	sale1 := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 3, TenderCard)
	sale2 := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 2, TenderCard)
	noteKey := key()
	rail.failKey = sale2.OrderID
	body := s.noteBody(noteKey, cust, RefundCard, []trentonSaleView{sale1, sale2}, []float64{3, 2})

	rec := s.postNote(trentonClerkSub, "", body)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("the second order's processor failure is a 502, got %d: %s", rec.Code, rec.Body.String())
	}
	// The processor refunded the first order; the books hold nothing.
	if len(rail.byKey) != 1 || rail.refundedTotal() <= 0 {
		t.Fatalf("the processor holds the first order's refund: %v", rail.amounts)
	}
	if n := s.countAll(`SELECT count(*) FROM pos_returns WHERE customer_id = $1::uuid`, cust); n != 0 {
		t.Fatalf("the rolled back note left %d returns on the books", n)
	}
	if n := s.countAll(`SELECT count(*) FROM payments WHERE customer_id = $1::uuid AND status = 'REFUNDED'`, cust); n != 0 {
		t.Fatalf("the rolled back note left %d refund payments on the books", n)
	}
	if n := s.countAll(`SELECT count(*) FROM events_outbox WHERE topic = 'pos.return.committed' AND payload->>'order_id' IN ($1, $2)`, sale1.OrderID, sale2.OrderID); n != 0 {
		t.Fatalf("the rolled back note left %d return journals pending", n)
	}
	// The record a manager can see.
	var status, refundRef string
	var amount int64
	if err := s.pool.QueryRow(context.Background(), `
SELECT status, refund_ref, amount_cents FROM pos_card_refund_attempts WHERE customer_id = $1::uuid AND order_id = $2::uuid`,
		cust, sale1.OrderID).Scan(&status, &refundRef, &amount); err != nil {
		t.Fatalf("the refund the processor made must stand on record: %v", err)
	}
	if status != "UNBOOKED" || refundRef == "" || amount != rail.refundedTotal() {
		t.Fatalf("the record names the unbooked refund: %s %q %d (processor holds %d)", status, refundRef, amount, rail.refundedTotal())
	}

	// The retry of the same note books everything and refunds the first order only once.
	rail.failKey = ""
	rec = s.postNote(trentonClerkSub, "", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("the retried note must commit, got %d: %s", rec.Code, rec.Body.String())
	}
	note := s.decodeBody(rec)
	if len(rail.byKey) != 2 {
		t.Fatalf("the processor holds exactly one refund per order, got %d", len(rail.byKey))
	}
	if rail.refundedTotal() != int64(num(t, note, "total_cents")) {
		t.Fatalf("the processor refunded %d, the books say %v", rail.refundedTotal(), note["total_cents"])
	}
	if n := s.countAll(`SELECT count(*) FROM payments WHERE customer_id = $1::uuid AND status = 'REFUNDED'`, cust); n != 2 {
		t.Fatalf("the retried note books one refund payment per order, got %d", n)
	}
	var booked int64
	if err := s.pool.QueryRow(context.Background(), `SELECT COALESCE(SUM(total_cents),0) FROM pos_returns WHERE customer_id = $1::uuid`, cust).Scan(&booked); err != nil || booked != rail.refundedTotal() {
		t.Fatalf("the books hold %d (%v), the processor %d", booked, err, rail.refundedTotal())
	}
	if err := s.pool.QueryRow(context.Background(), `
SELECT status FROM pos_card_refund_attempts WHERE order_id = $1::uuid`, sale1.OrderID).Scan(&status); err != nil || status != "BOOKED" {
		t.Fatalf("the booked retry closes the record: %q (%v)", status, err)
	}
}
