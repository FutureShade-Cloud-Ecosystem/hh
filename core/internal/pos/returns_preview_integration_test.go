//go:build integration

package pos

// returns_preview_integration_test.go, the till screens' contract requests for item 10: the credit
// preview (one pricing function shared with the commit, so the preview and the commit can never
// differ) and the branch name on every order of the customer lines read.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// previewBody turns a returns-across body into the preview's: the same customer and the same order
// entries, nothing else (the preview takes no key, no rail and no drawer).
func previewBody(note map[string]any) map[string]any {
	return map[string]any{"customer_id": note["customer_id"], "orders": note["orders"]}
}

// postPreview drives the preview route once.
func (s *trentonStack) postPreview(who string, body map[string]any) *httptest.ResponseRecorder {
	s.t.Helper()
	return s.do(http.MethodPost, "/api/v1/pos/returns/preview", s.bearer(who), "", body)
}

func (s *trentonStack) countAll(query string, args ...any) int64 {
	s.t.Helper()
	var n int64
	if err := s.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		s.t.Fatalf("count %q: %v", query, err)
	}
	return n
}

// The preview answers exactly what the commit then books: the note total, every order's total and
// every line's money and tax, on partial lines (proportional rounding), whole lines (the exact
// remainder) and a line with an earlier return netted; it writes nothing; an over-return is
// refused in the commit's own words.
func TestIntegration_ReturnPreview_PricesExactlyAsTheCommit(t *testing.T) {
	s := newTrentonStack(t)
	customer := plantCreditCustomer(t, s, "Preview proof")

	// 7 SF and 3 SF rings: returning 3 of 7 prorates the line money and tax (a half-cent rounds), 3
	// of 3 pays the exact remainder.
	sale1 := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", customer, "", 7, TenderCash)
	sale2 := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", customer, "", 3, TenderCash)

	nb := s.noteBody(key(), customer, RefundCash, []trentonSaleView{sale1, sale2}, []float64{3, 3})
	pb := previewBody(nb)

	tables := []string{`pos_returns`, `return_notes`, `pos_return_lines`, `payments`, `events_outbox`}
	before := map[string]int64{}
	for _, tb := range tables {
		before[tb] = s.countAll(`SELECT count(*) FROM ` + tb)
	}

	rec := s.postPreview(trentonClerkSub, pb)
	if rec.Code != http.StatusOK {
		t.Fatalf("the preview must answer 200, got %d: %s", rec.Code, rec.Body.String())
	}
	preview := s.decodeBody(rec)
	for _, tb := range tables {
		if got := s.countAll(`SELECT count(*) FROM ` + tb); got != before[tb] {
			t.Fatalf("the preview wrote %d rows into %s", got-before[tb], tb)
		}
	}
	if preview["customer_id"] != customer || preview["branch_id"] != trentonBranchID {
		t.Fatalf("the preview names the customer and the one branch: %v", preview)
	}
	pOrders, _ := preview["orders"].([]any)
	if len(pOrders) != 2 {
		t.Fatalf("the preview answers its two orders: %v", preview)
	}

	// The commit books the same figures.
	rec = s.postNote(trentonClerkSub, "", nb)
	if rec.Code != http.StatusCreated {
		t.Fatalf("the note must commit, got %d: %s", rec.Code, rec.Body.String())
	}
	note := s.decodeBody(rec)
	for _, k := range []string{"subtotal_cents", "tax_cents", "total_cents"} {
		if num(t, preview, k) != num(t, note, k) {
			t.Fatalf("the preview and the commit differ on %s: preview %v, commit %v", k, preview[k], note[k])
		}
	}
	children, _ := note["returns"].([]any)
	byOrder := map[string]map[string]any{}
	for _, c := range children {
		cm := c.(map[string]any)
		byOrder[cm["order_id"].(string)] = cm
	}
	for _, po := range pOrders {
		pm := po.(map[string]any)
		cm, ok := byOrder[pm["order_id"].(string)]
		if !ok {
			t.Fatalf("the preview names an order the commit did not return: %v", pm)
		}
		for _, k := range []string{"subtotal_cents", "tax_cents", "total_cents"} {
			if num(t, pm, k) != num(t, cm, k) {
				t.Fatalf("order %v: preview and commit differ on %s: %v vs %v", pm["order_no"], k, pm[k], cm[k])
			}
		}
		if pm["order_no"] == "" {
			t.Fatalf("the preview names each order by its number: %v", pm)
		}
		lines, _ := pm["lines"].([]any)
		if len(lines) != 1 {
			t.Fatalf("the preview answers each requested line: %v", pm)
		}
		pl := lines[0].(map[string]any)
		var money, tax int64
		if err := s.pool.QueryRow(context.Background(), `
SELECT line_total_cents, tax_cents FROM pos_return_lines WHERE return_id = $1::uuid AND order_line_id = $2::uuid`,
			cm["return_id"], pl["order_line_id"]).Scan(&money, &tax); err != nil {
			t.Fatalf("the committed line: %v", err)
		}
		if int64(num(t, pl, "subtotal_cents")) != money || int64(num(t, pl, "tax_cents")) != tax ||
			int64(num(t, pl, "total_cents")) != money+tax {
			t.Fatalf("the preview line %v differs from the committed line (%d + %d)", pl, money, tax)
		}
	}

	// With an earlier return netted: the remainder of sale1 (4 of 7) previews the exact remainder
	// and the commit pays it.
	nb2 := s.noteBody(key(), customer, RefundCash, []trentonSaleView{sale1}, []float64{4})
	rec = s.postPreview(trentonClerkSub, previewBody(nb2))
	if rec.Code != http.StatusOK {
		t.Fatalf("the remainder preview must answer, got %d: %s", rec.Code, rec.Body.String())
	}
	rest := s.decodeBody(rec)
	rec = s.postNote(trentonClerkSub, "", nb2)
	if rec.Code != http.StatusCreated {
		t.Fatalf("the remainder note must commit, got %d: %s", rec.Code, rec.Body.String())
	}
	restNote := s.decodeBody(rec)
	if num(t, rest, "total_cents") != num(t, restNote, "total_cents") {
		t.Fatalf("the remainder differs: preview %v, commit %v", rest["total_cents"], restNote["total_cents"])
	}

	// An over-return is refused in the commit's own words, with nothing written.
	over := s.noteBody(key(), customer, RefundCash, []trentonSaleView{sale2}, []float64{1})
	recP := s.postPreview(trentonClerkSub, previewBody(over))
	recC := s.postNote(trentonClerkSub, "", over)
	if recP.Code != http.StatusUnprocessableEntity || recC.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an over-return must be refused by both, preview %d commit %d", recP.Code, recC.Code)
	}
	if s.decodeBody(recP)["error"] != s.decodeBody(recC)["error"] {
		t.Fatalf("the preview must refuse in the commit's words: %v vs %v", s.decodeBody(recP)["error"], s.decodeBody(recC)["error"])
	}
}

// The preview meets the commit's walls in the commit's words: the branch wall (403 naming the
// branch), the one branch rule, one customer, the house WALK-IN row, one order twice, no orders, a
// non-positive quantity.
func TestIntegration_ReturnPreview_MeetsTheNotesWalls(t *testing.T) {
	s := newTrentonStack(t)
	customer := plantCreditCustomer(t, s, "Preview walls")
	other := plantCreditCustomer(t, s, "Preview walls other")
	sale1 := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", customer, "", 5, TenderCash)
	saleK := s.ringForCustomer(t, key(), kingstonBranchID, s.kingstonTill, "kingston", customer, "", 5, TenderCash)
	saleOther := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", other, "", 5, TenderCash)

	rec := s.postPreview(trentonClerkSub, previewBody(s.noteBody(key(), customer, RefundCash, []trentonSaleView{sale1, saleK}, []float64{1, 1})))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("an unreachable branch's order must meet the wall, got %d: %s", rec.Code, rec.Body.String())
	}
	if body := s.decodeBody(rec); body["error"] == nil || !contains(body["error"].(string), kingstonBranchID) {
		t.Fatalf("the wall names the branch: %v", body)
	}
	rec = s.postPreview(trentonAdminSub, previewBody(s.noteBody(key(), customer, RefundCash, []trentonSaleView{sale1, saleK}, []float64{1, 1})))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a preview over two branches must be refused, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = s.postPreview(trentonClerkSub, previewBody(s.noteBody(key(), customer, RefundCash, []trentonSaleView{sale1, saleOther}, []float64{1, 1})))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("another customer's order must be refused, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = s.postPreview(trentonClerkSub, previewBody(s.noteBody(key(), walkInID(t, s.pool), RefundCash, []trentonSaleView{sale1}, []float64{1})))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("the walk-in row must be refused, got %d: %s", rec.Code, rec.Body.String())
	}
	twice := s.noteBody(key(), customer, RefundCash, []trentonSaleView{sale1, sale1}, []float64{1, 1})
	rec = s.postPreview(trentonClerkSub, previewBody(twice))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("one order twice must be refused, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = s.postPreview(trentonClerkSub, map[string]any{"customer_id": customer, "orders": []any{}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("no orders is a 400, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = s.postPreview(trentonClerkSub, previewBody(s.noteBody(key(), customer, RefundCash, []trentonSaleView{sale1}, []float64{0})))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a zero quantity must be refused, got %d: %s", rec.Code, rec.Body.String())
	}
}

// The same order line twice in one request must not be able to return more than is open: each
// request line is guarded against the earlier request lines of the same order (the preview and the
// commit both refuse in words).
func TestIntegration_ReturnAcross_TheSameLineTwiceCannotOverReturn(t *testing.T) {
	s := newTrentonStack(t)
	customer := plantCreditCustomer(t, s, "Same line twice")
	sale := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", customer, "", 4, TenderCash)
	var lineID string
	if err := s.pool.QueryRow(context.Background(), `SELECT id::text FROM order_lines WHERE order_id = $1::uuid ORDER BY line_no LIMIT 1`, sale.OrderID).Scan(&lineID); err != nil {
		t.Fatalf("the line: %v", err)
	}
	twice := map[string]any{
		"idempotency_key": key(), "customer_id": customer, "refund_method": RefundCash, "till_session_id": s.trentonTill,
		"orders": []any{map[string]any{"order_id": sale.OrderID, "lines": []any{
			map[string]any{"order_line_id": lineID, "qty_base": 3},
			map[string]any{"order_line_id": lineID, "qty_base": 3},
		}}},
	}
	returnsBefore := s.countAll(`SELECT count(*) FROM pos_returns WHERE order_id = $1::uuid`, sale.OrderID)
	rec := s.postNote(trentonClerkSub, "", twice)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("3 + 3 of 4 open must be refused, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := s.countAll(`SELECT count(*) FROM pos_returns WHERE order_id = $1::uuid`, sale.OrderID); got != returnsBefore {
		t.Fatalf("the refused note wrote %d returns", got-returnsBefore)
	}
	rec = s.postPreview(trentonClerkSub, previewBody(twice))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("the preview must refuse 3 + 3 of 4 open too, got %d: %s", rec.Code, rec.Body.String())
	}
}

// Every order of the customer lines read names its branch, so the one branch refusal can name the
// branches in words.
func TestIntegration_CustomerLines_EveryOrderNamesItsBranch(t *testing.T) {
	s := newTrentonStack(t)
	customer := plantCreditCustomer(t, s, "Branch names")
	s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", customer, "", 2, TenderCash)
	s.ringForCustomer(t, key(), kingstonBranchID, s.kingstonTill, "kingston", customer, "", 2, TenderCash)
	names := map[string]string{}
	for _, id := range []string{trentonBranchID, kingstonBranchID} {
		var name string
		if err := s.pool.QueryRow(context.Background(), `SELECT name FROM locations WHERE id = $1::uuid`, id).Scan(&name); err != nil || name == "" {
			t.Fatalf("branch %s has no name (%v)", id, err)
		}
		names[id] = name
	}
	code, body := s.customerLinesGet(t, trentonAdminSub, customer, "", 0)
	if code != http.StatusOK {
		t.Fatalf("the read must answer, got %d", code)
	}
	seen := 0
	for _, o := range orderItems(t, body) {
		want := names[o["branch_id"].(string)]
		if o["branch_name"] != want {
			t.Fatalf("order %v must name its branch %q, got %v", o["order_no"], want, o["branch_name"])
		}
		seen++
	}
	if seen != 2 {
		t.Fatalf("expected the customer's two orders, saw %d", seen)
	}
}
