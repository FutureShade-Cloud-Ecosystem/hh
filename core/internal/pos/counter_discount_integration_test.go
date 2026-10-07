//go:build integration

package pos

// The counter discount over the real composition (the Trenton stack: the real guards, the real
// role configuration, the real tables). A manager's verbal yes is recorded, not queued: the
// ring names the approver on every discounted line and on the ticket, the approver must be a
// login the dealer's own-branch clearing list admits at the sale's branch, the discount lands in
// the sale's lines, the audit log and the pricing log, the preview answers the total the ring
// charges, and a return of a discounted line refunds what was paid.
//
// Run as the Trenton legs do (a throwaway Postgres migrated and seeded, then):
//
//	CI=true DATABASE_URL=... go test -tags integration ./internal/pos/ -run TestIntegration_CounterDiscount -p 1 -v

import (
	"context"
	"net/http"
	"testing"
)

// ring posts one sale body as the clerk and answers the recorder.
func (s *trentonStack) ringBody(who string, body map[string]any) (int, map[string]any) {
	s.t.Helper()
	rec := s.do(http.MethodPost, "/api/v1/pos/sales", s.bearer(who), "", body)
	return rec.Code, s.decodeBody(rec)
}

func (s *trentonStack) discountedBody(k string, lines []map[string]any, ticket map[string]any) map[string]any {
	b := map[string]any{
		"idempotency_key": k, "branch_id": trentonBranchID, "till_session_id": s.trentonTill,
		"sale_type": SaleCollectNow, "tender": TenderCash, "lines": lines,
	}
	if ticket != nil {
		b["discount"] = ticket
	}
	return b
}

func TestIntegration_CounterDiscount_EndToEnd(t *testing.T) {
	s := newTrentonStack(t)
	ctx := context.Background()
	line := func(qty float64, d map[string]any) map[string]any {
		l := map[string]any{"product_id": l2cProductID, "qty_entered": qty, "uom_entered": "SF"}
		if d != nil {
			// The yes is for this quantity and this line total (10.00 a SF here).
			d["approved_qty"], d["approved_base_cents"] = qty, int64(qty*1000)
			l["discount"] = d
		}
		return l
	}

	// 20 SF at 10.00 is 200.00; the Sales manager's yes on 10 percent takes 20.00 off the line.
	k := key()
	code, sale := s.ringBody(trentonClerkSub, s.discountedBody(k,
		[]map[string]any{line(20, map[string]any{"percent_bps": 1000, "approved_by": managerUserID})}, nil))
	if code != http.StatusCreated {
		t.Fatalf("the discounted ring must commit, got %d: %v", code, sale)
	}
	if sale["subtotal_cents"].(float64) != 18000 || sale["total_cents"].(float64) != 20340 || sale["discount_cents"].(float64) != 2000 {
		t.Fatalf("want 18000 / 20340 with 2000 off, got %v", sale)
	}
	ds := sale["discounts"].([]any)
	if len(ds) != 1 || ds[0].(map[string]any)["approved_by_label"] != "Sales Manager" || ds[0].(map[string]any)["approved_by"] != managerUserID {
		t.Fatalf("the sale must report the approver's role label: %v", ds)
	}
	orderID := sale["order_id"].(string)

	// The stored line is net, with the discount and the approver beside it.
	var lineTotal, disc int64
	var bps int
	var by, role string
	if err := s.pool.QueryRow(ctx, `SELECT line_total_cents, discount_cents, discount_percent_bps,
		discount_approved_by::text, discount_approved_role FROM order_lines WHERE order_id = $1::uuid`, orderID).
		Scan(&lineTotal, &disc, &bps, &by, &role); err != nil {
		t.Fatalf("read the line: %v", err)
	}
	if lineTotal != 18000 || disc != 2000 || bps != 1000 || by != managerUserID || role != "sales_manager" {
		t.Fatalf("stored line %d/%d/%d/%s/%s", lineTotal, disc, bps, by, role)
	}
	var invSub int64
	if err := s.pool.QueryRow(ctx, `SELECT subtotal_cents FROM invoices WHERE order_id = $1::uuid`, orderID).Scan(&invSub); err != nil || invSub != 18000 {
		t.Fatalf("the invoice carries the net subtotal: %d %v", invSub, err)
	}

	// The audit log and the pricing log both name the manager.
	if rows := s.auditRows("POS_DISCOUNT_APPROVED", orderID); len(rows) != 1 || rows[0]["approver_id"] != managerUserID || rows[0]["amount_cents"] != float64(2000) {
		t.Fatalf("audit rows: %v", rows)
	}
	var logUser, reason, field string
	if err := s.pool.QueryRow(ctx, `SELECT user_id::text, bypass_reason, field_name FROM pricing_audit_log
		WHERE ref_entity_type = 'order' AND ref_entity_id = $1::uuid`, orderID).Scan(&logUser, &reason, &field); err != nil {
		t.Fatalf("read the pricing log: %v", err)
	}
	if logUser != managerUserID || reason != "MANAGER_SCOPE_OVERRIDE" || field != "line_1" {
		t.Fatalf("pricing log row: %s %s %s", logUser, reason, field)
	}

	// A replay answers the same sale, discounts included; a reopen shows them too.
	code, replay := s.ringBody(trentonClerkSub, s.discountedBody(k,
		[]map[string]any{line(20, map[string]any{"percent_bps": 1000, "approved_by": managerUserID})}, nil))
	if code != http.StatusOK || replay["replayed"] != true || replay["discount_cents"].(float64) != 2000 ||
		replay["discounts"].([]any)[0].(map[string]any)["approved_by_label"] != "Sales Manager" {
		t.Fatalf("the replay must carry the discounts verbatim: %d %v", code, replay)
	}
	rec := s.do(http.MethodGet, "/api/v1/pos/sales/"+orderID, s.bearer(trentonClerkSub), "", nil)
	view := s.decodeBody(rec)
	if rec.Code != http.StatusOK || view["discount_cents"].(float64) != 2000 || view["lines"].([]any)[0].(map[string]any)["discount_cents"].(float64) != 2000 {
		t.Fatalf("the reopen must show the discount: %d %v", rec.Code, view)
	}

	// The preview answers the total the ring charges (line and ticket discounts together).
	both := s.discountedBody(key(),
		[]map[string]any{line(20, map[string]any{"percent_bps": 1000, "approved_by": managerUserID}), line(10, nil)},
		map[string]any{"amount_cents": 1500, "approved_by": managerUserID, "approved_base_cents": 28000})
	prevBody := map[string]any{"branch_id": trentonBranchID, "lines": both["lines"], "discount": both["discount"]}
	rec = s.do(http.MethodPost, "/api/v1/pos/sale-preview", s.bearer(trentonClerkSub), "", prevBody)
	prev := s.decodeBody(rec)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview: %d %v", rec.Code, prev)
	}
	code, ring := s.ringBody(trentonClerkSub, both)
	if code != http.StatusCreated {
		t.Fatalf("the two discount ring: %d %v", code, ring)
	}
	for _, f := range []string{"subtotal_cents", "tax_cents", "total_cents", "discount_cents"} {
		if prev[f] != ring[f] {
			t.Fatalf("the preview's %s %v is not the ring's %v", f, prev[f], ring[f])
		}
	}
	if ring["subtotal_cents"].(float64) != 18000+10000-1500 || ring["discount_cents"].(float64) != 3500 {
		t.Fatalf("want 26500 subtotal and 3500 off: %v", ring)
	}
	var shares int64
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(SUM(sale_discount_cents),0) FROM order_lines WHERE order_id = $1::uuid`,
		ring["order_id"].(string)).Scan(&shares); err != nil || shares != 1500 {
		t.Fatalf("the ticket discount is shared over the lines to the cent: %d %v", shares, err)
	}

	// A yes is for the quantity it was given: the same discount at 30 SF is refused, nothing written.
	k2 := key()
	stale := s.discountedBody(k2, []map[string]any{{
		"product_id": l2cProductID, "qty_entered": 30.0, "uom_entered": "SF",
		"discount": map[string]any{"percent_bps": 1000, "approved_by": managerUserID, "approved_qty": 20.0, "approved_base_cents": 20000},
	}}, nil)
	if code, body := s.ringBody(trentonClerkSub, stale); code != http.StatusUnprocessableEntity {
		t.Fatalf("a stale approval must be refused: %d %v", code, body)
	}

	// Refusals in words, and nothing written: no approver, a clerk, a manager of the other
	// branch, the global General Manager (not on the dealer's clearing list).
	for name, d := range map[string]map[string]any{
		"no approver":        {"percent_bps": 500},
		"a clerk":            {"percent_bps": 500, "approved_by": clerkUserID},
		"other branch mgr":   {"percent_bps": 500, "approved_by": "5d1b0000-0000-4000-9000-0000000000c4"},
		"general manager":    {"percent_bps": 500, "approved_by": adminUserID},
		"not a staff id":     {"percent_bps": 500, "approved_by": "nobody"},
		"percent and amount": {"percent_bps": 500, "amount_cents": 100, "approved_by": managerUserID},
	} {
		k := key()
		code, body := s.ringBody(trentonClerkSub, s.discountedBody(k, []map[string]any{line(5, d)}, nil))
		if code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: want 422, got %d %v", name, code, body)
		}
		var n int
		_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM pos_sale WHERE idempotency_key = $1`, k).Scan(&n)
		if n != 0 {
			t.Fatalf("%s: a refused discount wrote a sale", name)
		}
	}

	// The approvers read: the Trenton Sales manager only (a clerk, the global General Manager and
	// Kingston's manager are not named); a Kingston-only caller is walled off Trenton's list.
	rec = s.do(http.MethodGet, "/api/v1/pos/discount-approvers?branch_id="+trentonBranchID, s.bearer(trentonClerkSub), "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("approvers: %d %s", rec.Code, rec.Body.String())
	}
	items := s.decodeBody(rec)["items"].([]any)
	found := false
	for _, it := range items {
		m := it.(map[string]any)
		switch m["id"] {
		case managerUserID:
			found = true
			if m["role_label"] != "Sales Manager" {
				t.Fatalf("the manager's role label: %v", m)
			}
		case clerkUserID, adminUserID, "5d1b0000-0000-4000-9000-0000000000c4":
			t.Fatalf("%v must not be named as an approver at Trenton", m)
		}
	}
	if !found {
		t.Fatalf("the Trenton Sales manager must be named: %v", items)
	}
	rec = s.do(http.MethodGet, "/api/v1/pos/discount-approvers?branch_id="+trentonBranchID, s.bearer(kingstonClerkSub), "", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a Kingston login must not read Trenton's approvers, got %d", rec.Code)
	}
}

// A return of a discounted line refunds what was paid, the discount already out of the line.
func TestIntegration_CounterDiscount_ReturnRefundsWhatWasPaid(t *testing.T) {
	s := newTrentonStack(t)
	code, sale := s.ringBody(trentonClerkSub, s.discountedBody(key(), []map[string]any{{
		"product_id": l2cProductID, "qty_entered": 20.0, "uom_entered": "SF",
		"discount": map[string]any{"percent_bps": 1000, "approved_by": managerUserID, "approved_qty": 20.0, "approved_base_cents": 20000},
	}}, map[string]any{"amount_cents": 500, "approved_by": managerUserID, "approved_base_cents": 18000}))
	if code != http.StatusCreated {
		t.Fatalf("ring: %d %v", code, sale)
	}
	paid := sale["total_cents"].(float64) // 18000 - 500 = 17500, plus 13 percent
	if paid != 19775 {
		t.Fatalf("want 19775 paid, got %v", paid)
	}
	view := trentonSaleView{OrderID: sale["order_id"].(string), OrderNo: sale["order_no"].(string), Status: "COMMITTED"}
	rec := s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "",
		s.returnBody(key(), view, 0, RefundCash, 20))
	if rec.Code != http.StatusCreated {
		t.Fatalf("the full return must commit, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := s.decodeBody(rec)["total_cents"].(float64); got != paid {
		t.Fatalf("a return of the discounted line refunds %v, the customer paid %v", got, paid)
	}
}
