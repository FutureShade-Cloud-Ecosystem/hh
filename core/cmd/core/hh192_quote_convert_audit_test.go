package main

// hh192_quote_convert_audit_test.go pins the contractor door's creation-history wiring at the
// composition root (HH-192). The quote module's own suites prove runConvert seeds the creation
// row when handed a recorder, but wire_tops built the portal's quote service without one, so a
// quote converted through POST /api/v1/tenant/quotes/{id}/convert produced an order whose
// GET /api/v1/orders/{id}/history had no creation transition while the counter door's did. This
// drives both real registered conversion routes over the fixture database and reads each order's
// history through the real registered history route.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestComposition_HH192QuoteConvert_BothDoorsSeedTheCreationRow(t *testing.T) {
	env := newCompEnv(t)
	rec, graph := registeredSurface(t, env)
	ctx := context.Background()

	// The conversion's credit gate reads the customer's limit; the fixture's alpha account has none
	// (COD), so give it room for the run and put the limit back.
	if _, err := graph.pool.Exec(ctx, `UPDATE customers SET credit_limit_cents = 100000000 WHERE id = $1::uuid`, testkit.TenantAlphaID); err != nil {
		t.Fatalf("raise credit limit: %v", err)
	}
	t.Cleanup(func() {
		if _, err := graph.pool.Exec(context.Background(), `UPDATE customers SET credit_limit_cents = 0 WHERE id = $1::uuid`, testkit.TenantAlphaID); err != nil {
			t.Errorf("restore credit limit to the fixture's 0: %v: the shared fixture account is left raised", err)
		}
	})

	// Two ACCEPTED quotes for the alpha contractor, one per door, each with a priced line.
	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
	plant := func(tag string) string {
		t.Helper()
		var id string
		if err := graph.pool.QueryRow(ctx,
			`INSERT INTO quotes (quote_no, customer_id, branch_id, status, org_id)
			 VALUES ($1, $2::uuid, $3::uuid, 'ACCEPTED', $4::uuid) RETURNING id::text`,
			"HH192-"+tag+"-"+suffix, testkit.TenantAlphaID, testkit.BranchNorthAID, testkit.OrgNorthID).Scan(&id); err != nil {
			t.Fatalf("plant quote: %v", err)
		}
		if _, err := graph.pool.Exec(ctx,
			`INSERT INTO quote_lines (quote_id, line_no, product_id, description, qty_entered, uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents, org_id)
			 VALUES ($1::uuid, 1, $2::uuid, 'HH192 line', 10, 'EA', 10, 1000, 'EA', 10000, $3::uuid)`,
			id, testkit.ProductID, testkit.OrgNorthID); err != nil {
			t.Fatalf("plant quote line: %v", err)
		}
		return id
	}
	contractorQuote, staffQuote := plant("C"), plant("S")
	var orderIDs []string
	t.Cleanup(func() {
		bg := context.Background()
		step := func(what, stmt string, args ...any) {
			t.Helper()
			if _, err := graph.pool.Exec(bg, stmt, args...); err != nil {
				t.Errorf("cleanup %s: %v", what, err)
			}
		}
		for _, id := range orderIDs {
			step("order audit rows", `DELETE FROM audit_log WHERE entity_type = 'order' AND entity_id = $1`, id)
		}
		// Each conversion hard-reserves its line and adds the hold to stock_balances.qty_reserved,
		// the number availability reads. Unwind that projection first, then delete the holds:
		// deleting the reservations first would strand qty_reserved (the w5a integration tests'
		// cleanup does the same).
		step("order qty_reserved projection", `
			UPDATE stock_balances sb
			   SET qty_reserved = GREATEST(sb.qty_reserved - h.held, 0)
			  FROM (SELECT product_id, location_id, SUM(qty_base) AS held
			          FROM stock_reservations
			         WHERE source_type = 'order' AND source_ref_id = ANY($1::uuid[])
			           AND status = 'ACTIVE' AND hold_mode = 'HARD'
			         GROUP BY product_id, location_id) h
			 WHERE sb.product_id = h.product_id AND sb.location_id = h.location_id`, orderIDs)
		// Each conversion hard-reserves its line against the order; leave none behind or a reused
		// database loses the fixture product's available stock on every run.
		step("order stock reservations", `DELETE FROM stock_reservations WHERE source_type = 'order' AND source_ref_id = ANY($1::uuid[])`, orderIDs)
		step("order lines", `DELETE FROM order_lines WHERE order_id = ANY($1::uuid[])`, orderIDs)
		step("orders", `DELETE FROM orders WHERE id = ANY($1::uuid[])`, orderIDs)
		quoteIDs := []string{contractorQuote, staffQuote}
		step("quote lines", `DELETE FROM quote_lines WHERE quote_id = ANY($1::uuid[])`, quoteIDs)
		step("quotes", `DELETE FROM quotes WHERE id = ANY($1::uuid[])`, quoteIDs)
	})

	do := func(method, path, token string, staff bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(""))
		req.Header.Set("Authorization", "Bearer "+token)
		if staff {
			req.Header.Set(authz.HeaderXOrg, testkit.OrgNorthSlug)
		}
		rsp := httptest.NewRecorder()
		rec.inner.ServeHTTP(rsp, req)
		return rsp
	}
	convert := func(path, token string, staff bool, want int) string {
		t.Helper()
		rsp := do(http.MethodPost, path, token, staff)
		// Record the order the moment an answer names one, before any check can fail, so a
		// half-failed conversion is still cleaned up.
		var res quote.ConvertResult
		decodeErr := json.Unmarshal(rsp.Body.Bytes(), &res)
		if res.OrderID != "" {
			orderIDs = append(orderIDs, res.OrderID)
		}
		if rsp.Code != want {
			t.Fatalf("POST %s = %d, want %d (body %s)", path, rsp.Code, want, rsp.Body.String())
		}
		if decodeErr != nil || res.OrderID == "" {
			t.Fatalf("decode convert answer: %v (body %s)", decodeErr, rsp.Body.String())
		}
		return res.OrderID
	}
	history := func(orderID string) order.OrderHistory {
		t.Helper()
		rsp := do(http.MethodGet, "/api/v1/orders/"+orderID+"/history", env.minter.StaffToken(t, testkit.SubGeneralMgr), true)
		if rsp.Code != http.StatusOK {
			t.Fatalf("GET history = %d, want 200 (body %s)", rsp.Code, rsp.Body.String())
		}
		var h order.OrderHistory
		if err := json.Unmarshal(rsp.Body.Bytes(), &h); err != nil {
			t.Fatalf("decode history: %v (body %s)", err, rsp.Body.String())
		}
		return h
	}

	staffOrder := convert("/api/v1/quotes/"+staffQuote+"/convert", env.minter.StaffToken(t, testkit.SubInsideSales), true, http.StatusCreated)
	contractorOrder := convert("/api/v1/tenant/quotes/"+contractorQuote+"/convert",
		env.minter.TenantToken(t, testkit.SubPortalAdminAlpha, testkit.TenantAlphaID), false, http.StatusOK)

	// The counter door's row is the expected shape: one creation transition "" to DRAFT for the quote.
	sh := history(staffOrder)
	if len(sh.Changes) != 1 || sh.Changes[0].FromStatus != "" || sh.Changes[0].ToStatus != "DRAFT" {
		t.Fatalf("staff-door history = %+v, want exactly the creation transition from \"\" to DRAFT", sh.Changes)
	}

	// The contractor door must carry the same one row, attributed to the customer.
	ch := history(contractorOrder)
	if len(ch.Changes) != 1 {
		t.Fatalf("contractor-door history has %d transitions (%+v), want the one creation transition: the portal's quote service must be handed the audit recorder", len(ch.Changes), ch.Changes)
	}
	got := ch.Changes[0]
	if got.FromStatus != "" || got.ToStatus != "DRAFT" || got.Reason != sh.Changes[0].Reason {
		t.Errorf("contractor-door creation row = %+v, want from \"\" to DRAFT with the staff door's reason %q", got, sh.Changes[0].Reason)
	}
	if got.ActorID != testkit.TenantAlphaID {
		t.Errorf("contractor-door actor = %q, want the customer %q", got.ActorID, testkit.TenantAlphaID)
	}
}
