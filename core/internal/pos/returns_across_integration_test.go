//go:build integration

package pos

// returns_across_integration_test.go, sub item 10.2: the credit note across orders. One note, N
// orders of one customer, one branch, ONE transaction: every order's per-sale lock taken in
// ascending order id before any line lock (two overlapping notes cannot deadlock), every line
// guarded by the same per-line lock and netting the single-order return reads, the approval
// threshold judging the NOTE total (the Q3 ruling), one pos_returns row per order keeping its own
// return number, one pos.return.committed journal per child (the GL builder and its key
// untouched), and the note's own idempotency key answering its replay. The rails are the existing
// ones per child; the across-orders rails are sub item 10.4.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

// noteBody builds one returns-across body for whole lines (qty per order line, in line order).
func (s *trentonStack) noteBody(key, customerID, method string, orders []trentonSaleView, qtys []float64) map[string]any {
	s.t.Helper()
	entries := make([]map[string]any, 0, len(orders))
	for i, sale := range orders {
		rows, err := s.pool.Query(context.Background(),
			`SELECT id::text FROM order_lines WHERE order_id = $1::uuid ORDER BY line_no`, sale.OrderID)
		if err != nil {
			s.t.Fatalf("the sale's lines: %v", err)
		}
		defer rows.Close()
		var lineIDs []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				s.t.Fatalf("scan line: %v", err)
			}
			lineIDs = append(lineIDs, id)
		}
		if len(lineIDs) == 0 {
			s.t.Fatalf("sale %s carries no lines", sale.OrderID)
		}
		lines := []map[string]any{{"order_line_id": lineIDs[0], "qty_base": qtys[i]}}
		entries = append(entries, map[string]any{"order_id": sale.OrderID, "lines": lines})
	}
	body := map[string]any{
		"idempotency_key": key,
		"customer_id":     customerID,
		"orders":          entries,
		"refund_method":   method,
		"reason":          "wrong grit ordered twice",
		"restock":         false,
	}
	if method == RefundCash {
		body["till_session_id"] = s.trentonTill
	}
	return body
}

// postNote drives the returns-across route once.
func (s *trentonStack) postNote(who, approver string, body map[string]any) *httptest.ResponseRecorder {
	s.t.Helper()
	rec := s.do(http.MethodPost, "/api/v1/pos/returns-across", s.bearer(who), approver, body)
	return rec
}

// Two orders in one note commit atomically, each child keeping its own return number and its own
// journal; a note whose second order over-returns is refused whole and nothing persists, not even
// the first order's rows; a foreign branch order is refused before any rail; the walls around the
// note's own shape (one branch, one customer, no walk-in) answer in words.
func TestIntegration_ReturnAcross_TwoOrdersOneNote(t *testing.T) {
	s := newTrentonStack(t)
	customer := plantCreditCustomer(t, s, "Across orders proof")
	other := plantCreditCustomer(t, s, "Across orders other")

	// Two Trenton rings of 5 SF each (42.50 + 5.53 tax each, the note 96.06, under the configured
	// 250.00 so no approver is asked) and a Kingston ring for the wall.
	sale1 := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", customer, "", 5, TenderCash)
	sale2 := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", customer, "", 5, TenderCash)
	saleK := s.ringForCustomer(t, key(), kingstonBranchID, s.kingstonTill, "kingston", customer, "", 5, TenderCash)
	saleOther := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", other, "", 5, TenderCash)

	countRows := func(query string, args ...any) int64 {
		t.Helper()
		var n int64
		if err := s.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
			t.Fatalf("count %q: %v", query, err)
		}
		return n
	}
	returnsBefore := countRows(`SELECT count(*) FROM pos_returns WHERE customer_id = $1::uuid`, customer)
	paymentsBefore := countRows(`SELECT count(*) FROM payments`)
	eventsBefore := countRows(`SELECT count(*) FROM events_outbox WHERE topic = 'pos.return.committed'`)

	// The note commits: one note row, two children with their own return numbers and one journal
	// event each, the note total the exact sum.
	rec := s.postNote(trentonClerkSub, "", s.noteBody(key(), customer, RefundCash, []trentonSaleView{sale1, sale2}, []float64{5, 5}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("the two-order note must commit, got %d: %s", rec.Code, rec.Body.String())
	}
	note := s.decodeBody(rec)
	noteNo, _ := note["note_no"].(string)
	if noteNo == "" || note["replayed"] == true {
		t.Fatalf("the note must carry its own number and not read as a replay: %v", note)
	}
	if note["customer_id"] != customer || note["branch_id"] != trentonBranchID {
		t.Fatalf("the note names its customer and its one branch: %v", note)
	}
	if num(t, note, "subtotal_cents") != 8500 || num(t, note, "tax_cents") != 1106 || num(t, note, "total_cents") != 9606 {
		t.Fatalf("the note total is the exact sum of its children: %v", note)
	}
	children, _ := note["returns"].([]any)
	if len(children) != 2 {
		t.Fatalf("the note answers its two children: %v", note)
	}
	returnNos := map[string]bool{}
	childIDs := map[string]bool{}
	for _, c := range children {
		cm, _ := c.(map[string]any)
		returnNos[cm["return_no"].(string)] = true
		childIDs[cm["return_id"].(string)] = true
		if cm["order_id"] != sale1.OrderID && cm["order_id"] != sale2.OrderID {
			t.Fatalf("a child names an order not on the note: %v", cm)
		}
	}
	if len(returnNos) != 2 {
		t.Fatalf("each child keeps its own return number: %v", children)
	}
	var linked int64
	if err := s.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM pos_returns WHERE note_id IS NOT NULL AND customer_id = $1::uuid`, customer).Scan(&linked); err != nil {
		t.Fatalf("the note link read: %v", err)
	}
	if linked != 2 {
		t.Fatalf("both children carry the note link, got %d", linked)
	}
	var noteRow struct {
		no    string
		total int64
	}
	var noteID string
	if err := s.pool.QueryRow(context.Background(),
		`SELECT id::text, note_no, total_cents FROM return_notes WHERE customer_id = $1::uuid`, customer).Scan(&noteID, &noteRow.no, &noteRow.total); err != nil {
		t.Fatalf("the note row: %v", err)
	}
	if noteRow.no != noteNo || noteRow.total != 9606 {
		t.Fatalf("the stored note reads %q %d", noteRow.no, noteRow.total)
	}
	// One journal per child, keyed per child return id, none for the note itself.
	for id := range childIDs {
		var n int64
		if err := s.pool.QueryRow(context.Background(),
			`SELECT count(*) FROM events_outbox WHERE topic = 'pos.return.committed' AND payload->>'return_id' = $1`, id).Scan(&n); err != nil || n != 1 {
			t.Fatalf("child %s carries exactly one committed journal, got %d (%v)", id, n, err)
		}
	}
	if got := countRows(`SELECT count(*) FROM events_outbox WHERE topic = 'pos.return.committed'`) - eventsBefore; got != 2 {
		t.Fatalf("the note books exactly two committed journals, got %d", got)
	}

	// The failure in the SECOND order rolls the whole note back: an over-return on sale2 leaves no
	// row, no payment and no journal behind, sale1's lines stay open.
	rec = s.postNote(trentonClerkSub, "", s.noteBody(key(), customer, RefundCash, []trentonSaleView{sale1, sale2}, []float64{1, 99}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("the over-returning note must be refused, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := countRows(`SELECT count(*) FROM pos_returns WHERE customer_id = $1::uuid`, customer); got != returnsBefore+2 {
		t.Fatalf("the refused note persisted %d return rows beyond its committed children", got-returnsBefore-2)
	}
	if got := countRows(`SELECT count(*) FROM payments`) - paymentsBefore; got != 2 {
		t.Fatalf("the refused note persisted %d drawer payments beyond the committed note's two", got)
	}
	if got := countRows(`SELECT count(*) FROM events_outbox WHERE topic = 'pos.return.committed'`) - eventsBefore; got != 2 {
		t.Fatalf("the refused note persisted %d journals beyond the committed note's two", got)
	}
	var open1 float64
	if err := s.pool.QueryRow(context.Background(), `
SELECT ol.qty_base - COALESCE((SELECT SUM(rl.qty_base) FROM pos_return_lines rl WHERE rl.order_line_id = ol.id), 0)
  FROM order_lines ol WHERE ol.order_id = $1::uuid ORDER BY ol.line_no LIMIT 1`, sale1.OrderID).Scan(&open1); err != nil || open1 != 0 {
		t.Fatalf("sale1's line is fully returned by the committed note (open %.4f)", open1)
	}

	// A foreign branch order is refused before any rail: the counting card rail never hears a call
	// and no drawer row moves.
	cardCalls := s.cards.calls
	rec = s.postNote(trentonClerkSub, "", s.noteBody(key(), customer, RefundCash, []trentonSaleView{sale1, saleK}, []float64{1, 1}))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a note naming a foreign branch order must meet the wall, got %d: %s", rec.Code, rec.Body.String())
	}
	if body := s.decodeBody(rec); body["error"] == nil || !contains(body["error"].(string), kingstonBranchID) {
		t.Fatalf("the wall names the branch: %v", body)
	}
	if s.cards.calls != cardCalls {
		t.Fatalf("the card rail heard %d calls across the wall", s.cards.calls-cardCalls)
	}

	// The note's own shape refusals, in words: two branches (a global caller reaches both, the
	// note still refuses), another customer's order, the walk-in row.
	rec = s.postNote(trentonAdminSub, "", s.noteBody(key(), customer, RefundCash, []trentonSaleView{sale1, saleK}, []float64{1, 1}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a note spanning two branches must be refused, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = s.postNote(trentonClerkSub, "", s.noteBody(key(), customer, RefundCash, []trentonSaleView{sale1, saleOther}, []float64{1, 1}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a note carrying another customer's order must be refused, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = s.postNote(trentonClerkSub, "", s.noteBody(key(), walkInID(t, s.pool), RefundCash, []trentonSaleView{sale1}, []float64{1}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a walk-in note must be refused, got %d: %s", rec.Code, rec.Body.String())
	}
}

// The replay of the same key answers the same note and writes nothing; two concurrent overlapping
// notes (orders A,B and B,A) both finish on a two-connection pool, nobody deadlocks.
func TestIntegration_ReturnAcross_ReplayAndOverlappingNotes(t *testing.T) {
	s := newTrentonStackOnPool(t, smallPool(t, 2))
	customer := plantCreditCustomer(t, s, "Across orders replay proof")
	saleA := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", customer, "", 6, TenderCash)
	saleB := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", customer, "", 6, TenderCash)

	// The first note covers both orders.
	noteKey := key()
	rec := s.postNote(trentonClerkSub, "", s.noteBody(noteKey, customer, RefundAccountCredit, []trentonSaleView{saleA, saleB}, []float64{6, 6}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("the note must commit, got %d: %s", rec.Code, rec.Body.String())
	}
	first := s.decodeBody(rec)
	var returnsAfterFirst, notesAfterFirst int64
	if err := s.pool.QueryRow(context.Background(), `SELECT count(*) FROM pos_returns WHERE customer_id = $1::uuid`, customer).Scan(&returnsAfterFirst); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(context.Background(), `SELECT count(*) FROM return_notes`).Scan(&notesAfterFirst); err != nil {
		t.Fatal(err)
	}

	// The same key again answers the same note, writes nothing, and says replayed.
	rec = s.postNote(trentonClerkSub, "", s.noteBody(noteKey, customer, RefundAccountCredit, []trentonSaleView{saleA, saleB}, []float64{6, 6}))
	if rec.Code != http.StatusOK {
		t.Fatalf("the replay must answer 200, got %d: %s", rec.Code, rec.Body.String())
	}
	again := s.decodeBody(rec)
	if again["note_no"] != first["note_no"] || again["replayed"] != true {
		t.Fatalf("the replay answers the same note: %v", again)
	}
	var n int64
	if err := s.pool.QueryRow(context.Background(), `SELECT count(*) FROM pos_returns WHERE customer_id = $1::uuid`, customer).Scan(&n); err != nil || n != returnsAfterFirst {
		t.Fatalf("the replay wrote return rows: %d want %d (%v)", n, returnsAfterFirst, err)
	}
	if err := s.pool.QueryRow(context.Background(), `SELECT count(*) FROM return_notes`).Scan(&n); err != nil || n != notesAfterFirst {
		t.Fatalf("the replay wrote note rows: %d want %d (%v)", n, notesAfterFirst, err)
	}

	// Two concurrent overlapping notes, opposite order orders, distinct keys, each taking HALF of
	// both orders so together they consume exactly the whole: both finish, nobody deadlocks, and
	// all four halves stand (the point is the lock order, not the over-return guard). Fresh sales
	// so the open quantities are whole.
	saleC := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", customer, "", 6, TenderCash)
	saleD := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", customer, "", 6, TenderCash)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	post := func(body map[string]any) *httptest.ResponseRecorder {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/pos/returns-across", newBytesReader(raw)).WithContext(ctx)
		req.Header.Set("Authorization", s.bearer(trentonClerkSub))
		req.Header.Set("X-Org", demoOrgSlug)
		rr := httptest.NewRecorder()
		s.mux.ServeHTTP(rr, req)
		return rr
	}
	var wg sync.WaitGroup
	out := make([]*httptest.ResponseRecorder, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		out[0] = post(s.noteBody(key(), customer, RefundAccountCredit, []trentonSaleView{saleC, saleD}, []float64{3, 3}))
	}()
	go func() {
		defer wg.Done()
		out[1] = post(s.noteBody(key(), customer, RefundAccountCredit, []trentonSaleView{saleD, saleC}, []float64{3, 3}))
	}()
	wg.Wait()
	if ctx.Err() != nil {
		t.Fatalf("the overlapping notes deadlocked until the deadline: %v", ctx.Err())
	}
	for i, rr := range out {
		if rr.Code != http.StatusCreated {
			t.Fatalf("overlapping note %d must commit, got %d: %s", i, rr.Code, rr.Body.String())
		}
	}
	if err := s.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM pos_returns WHERE order_id IN ($1::uuid, $2::uuid)`, saleC.OrderID, saleD.OrderID).Scan(&n); err != nil || n != 4 {
		t.Fatalf("the two overlapping notes hold %d children, want 4 (%v)", n, err)
	}
}

func newBytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

// The earlier single-order return counts: a note returning the remainder commits and pays exactly
// what is left; a note asking beyond it is refused whole.
func TestIntegration_ReturnAcross_CumulativeWithAnEarlierSingleReturn(t *testing.T) {
	s := newTrentonStack(t)
	customer := plantCreditCustomer(t, s, "Across orders cumulative proof")
	sale := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", customer, "", 10, TenderCash)
	other := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", customer, "", 5, TenderCash)

	// An earlier single return of 2 of 10 on the sale: 17.00 + 2.21 back.
	prior := s.returnBody(key(), sale, 0, RefundCash, 2)
	if rec := s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", prior); rec.Code != http.StatusCreated {
		t.Fatalf("the earlier single return must commit: %d %s", rec.Code, rec.Body.String())
	}

	// A note asking for 9 of the 10 refuses whole (only 8 open), the other order untouched.
	rec := s.postNote(trentonClerkSub, "", s.noteBody(key(), customer, RefundCash, []trentonSaleView{other, sale}, []float64{5, 9}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("the note beyond the open quantity must be refused, got %d: %s", rec.Code, rec.Body.String())
	}
	var otherReturns int64
	if err := s.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM pos_returns WHERE order_id = $1::uuid`, other.OrderID).Scan(&otherReturns); err != nil || otherReturns != 0 {
		t.Fatalf("the refused note left %d rows on its untouched order", otherReturns)
	}

	// The note returning exactly the remainder of both orders commits and pays exactly what is
	// left: 8 of 10 on the sale (68.00 + 8.84) and 5 of 5 on the other (42.50 + 5.53).
	rec = s.postNote(trentonClerkSub, "", s.noteBody(key(), customer, RefundCash, []trentonSaleView{sale, other}, []float64{8, 5}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("the note of the remainder must commit, got %d: %s", rec.Code, rec.Body.String())
	}
	note := s.decodeBody(rec)
	if num(t, note, "subtotal_cents") != 11050 || num(t, note, "tax_cents") != 1437 || num(t, note, "total_cents") != 12487 {
		t.Fatalf("the note pays exactly what is left: %v", note)
	}
}

// The approval threshold judges the NOTE total (the Q3 ruling): two halves each under the
// configured 250.00 commit as one note over it, refused to the clerk with the note's total named,
// committed as the manager's decision with the manager's credential in the approver header.
func TestIntegration_ReturnAcross_ApprovalJudgesTheNoteTotal(t *testing.T) {
	s := newTrentonStack(t)
	customer := plantCreditCustomer(t, s, "Across orders approval proof")
	// 18 SF each: 153.00 + 19.89 = 172.89 per child, 345.78 for the note, each child alone under
	// the configured 250.00.
	sale1 := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", customer, "", 18, TenderCash)
	sale2 := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", customer, "", 18, TenderCash)

	rec := s.postNote(trentonClerkSub, "", s.noteBody(key(), customer, RefundCash, []trentonSaleView{sale1, sale2}, []float64{18, 18}))
	if rec.Code != http.StatusConflict {
		t.Fatalf("the note above the approval amount must be refused, got %d: %s", rec.Code, rec.Body.String())
	}
	body := s.decodeBody(rec)
	if body["error"] != "refund_approval_required" {
		t.Fatalf("the refusal is the approval family: %v", body)
	}
	approval, _ := body["approval"].(map[string]any)
	if num(t, approval, "amount_cents") != 25000 {
		t.Fatalf("the refusal names the configured amount: %v", approval)
	}
	var refused int64
	if err := s.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM pos_returns WHERE customer_id = $1::uuid`, customer).Scan(&refused); err != nil || refused != 0 {
		t.Fatalf("the refused note persisted %d rows", refused)
	}

	// The same note with the Sales manager's credential commits as the manager's decision.
	rec = s.postNote(trentonClerkSub, s.bearer(trentonMgrSub), s.noteBody(key(), customer, RefundCash, []trentonSaleView{sale1, sale2}, []float64{18, 18}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("the manager's decision must commit, got %d: %s", rec.Code, rec.Body.String())
	}
	note := s.decodeBody(rec)
	if num(t, note, "total_cents") != 34578 {
		t.Fatalf("the approved note total: %v", note)
	}
	var decided int64
	if err := s.pool.QueryRow(context.Background(), `
SELECT count(*) FROM events_outbox
 WHERE topic = 'pos.return.committed'
   AND payload->>'order_id' IN ($1, $2)
   AND payload->>'decided_by' <> ''`, sale1.OrderID, sale2.OrderID).Scan(&decided); err != nil || decided != 2 {
		t.Fatalf("both children name the deciding manager, got %d (%v)", decided, err)
	}
}

// A note covering an order with a supplier deposit releases that deposit exactly once (ruling 17):
// one deposit.released journal for the deposit document however many children the note holds, the
// release the return path itself emits, on the real books through the real consumer.
func TestIntegration_ReturnAcross_SupplierDepositReleasedOnce(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	productID, branchID := aPaver(t, pool)
	suffix := fmt.Sprintf("rna-%d", time.Now().UnixNano()%1_000_000_000)
	var custID string
	if err := pool.QueryRow(ctx, `
INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
VALUES ('RNA-'||$1, 'Across orders deposit proof', 'CONTRACTOR', 30, 5000000) RETURNING id::text`,
		suffix).Scan(&custID); err != nil {
		t.Fatalf("plant the customer: %v", err)
	}
	svc := realPosSvc(t, pool, stubCharger{}).WithRoleConfig(defaultConfig(t))
	depositSale, err := svc.Sale(ctx, SaleInput{
		IdempotencyKey: key(), BranchID: branchID, CustomerID: custID,
		SaleType: SaleCollectLater, Tender: TenderAccount,
		SupplierDepositCents: 4000,
		Lines:                []LineInput{{ProductID: productID, QtyEntered: 5, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("the deposit sale: %v", err)
	}
	plainSale, err := svc.Sale(ctx, SaleInput{
		IdempotencyKey: key(), BranchID: branchID, CustomerID: custID,
		SaleType: SaleCollectNow, Tender: TenderAccount,
		Lines: []LineInput{{ProductID: productID, QtyEntered: 3, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("the plain sale: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		for _, oid := range []string{depositSale.OrderID, plainSale.OrderID} {
			_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE payload->>'order_id' = $1`, oid)
			_, _ = pool.Exec(c, `DELETE FROM pos_return_lines WHERE return_id IN (SELECT id FROM pos_returns WHERE order_id = $1::uuid)`, oid)
			_, _ = pool.Exec(c, `DELETE FROM pos_returns WHERE order_id = $1::uuid`, oid)
			_, _ = pool.Exec(c, `DELETE FROM pos_sale WHERE order_id = $1::uuid`, oid)
			_, _ = pool.Exec(c, `DELETE FROM order_lines WHERE order_id = $1::uuid`, oid)
			_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1::uuid`, oid)
		}
		_, _ = pool.Exec(c, `DELETE FROM return_notes WHERE customer_id = $1::uuid`, custID)
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, custID)
		_, _ = pool.Exec(c, `DELETE FROM invoice_lines WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`, custID)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, custID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, custID)
	})

	var depLineID, plainLineID string
	var depQty, plainQty float64
	if err := pool.QueryRow(ctx, `SELECT id::text, qty_base FROM order_lines WHERE order_id = $1::uuid ORDER BY line_no LIMIT 1`, depositSale.OrderID).Scan(&depLineID, &depQty); err != nil {
		t.Fatalf("the deposit sale's line: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text, qty_base FROM order_lines WHERE order_id = $1::uuid ORDER BY line_no LIMIT 1`, plainSale.OrderID).Scan(&plainLineID, &plainQty); err != nil {
		t.Fatalf("the plain sale's line: %v", err)
	}

	note, err := svc.ReturnAcross(ctx, ReturnAcrossInput{
		IdempotencyKey: key(), CustomerID: custID,
		Orders: []ReturnAcrossOrderInput{
			{OrderID: depositSale.OrderID, Lines: []ReturnLineInput{{OrderLineID: depLineID, QtyBase: depQty, ReasonCode: "OTHER"}}},
			{OrderID: plainSale.OrderID, Lines: []ReturnLineInput{{OrderLineID: plainLineID, QtyBase: plainQty, ReasonCode: "OTHER"}}},
		},
		RefundMethod: RefundAccountCredit, Reason: "special order cancelled",
	})
	if err != nil {
		t.Fatalf("the note across the deposit order: %v", err)
	}
	dep := depositSale.SupplierDepositInvoiceID

	billingRepo := billing.NewRepository(pool)
	glSvc := gl.NewService(gl.NewRepository(pool)).WithInvoiceReader(billingRepo)
	dispatcher := notify.NewDispatcher(notify.NewPgOutboxStore(pool), nil)
	glSvc.RegisterPostingHandlers(dispatcher)
	drainAll(t, dispatcher)

	var journals int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gl_journals WHERE idempotency_key = $1`, "deposit.released:"+dep).Scan(&journals); err != nil || journals != 1 {
		t.Fatalf("the deposit release stands at %d journals (%v), want exactly the one", journals, err)
	}
	var events int64
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM events_outbox WHERE topic = 'deposit.released' AND payload->>'invoice_id' = $1`, dep).Scan(&events); err != nil || events != 1 {
		t.Fatalf("the deposit release was emitted %d times (%v), want exactly once", events, err)
	}
	if len(note.Returns) != 2 || note.TotalCents <= 0 {
		t.Fatalf("the note answers its two children with their totals: %v", note)
	}
}
