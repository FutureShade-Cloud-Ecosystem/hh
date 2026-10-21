//go:build integration

package pos

// yard_held_account_integration_test.go (UX-89 round 3, customerFor's ruling): a branch may serve a yard-held
// account (one homed at another branch) only on documents that branch already holds. Here a Trenton sale to an
// account that is then homed at Kingston: the return of that sale is allowed, a swap of it prices its replacement
// order at the account's type exactly as before, and a NEW sale, and the margin glance that prices one, meet the
// customer wall (404 "customer not found", nothing written).

import (
	"context"
	"net/http"
	"testing"
)

func TestIntegration_YardHeldAccount_RulingPerDocument(t *testing.T) {
	s := newTrentonStack(t)
	cust := s.plantCustomer("Yard held ruling", 30, 5000000)
	orig := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 10, TenderCash)

	swapTotal := func(label string) float64 {
		t.Helper()
		rec := s.postSwapPreview(trentonClerkSub, s.swapBody(key(), cust, orig, 4, 2, TenderCash))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: the swap preview of the branch's own sale must answer, got %d: %s", label, rec.Code, rec.Body.String())
		}
		return num(t, s.decodeBody(rec)["new_order"].(map[string]any), "total_cents")
	}
	atHome := swapTotal("account at the branch")

	if _, err := s.pool.Exec(context.Background(), `UPDATE customers SET home_branch_id = $2::uuid WHERE id = $1::uuid`, cust, kingstonBranchID); err != nil {
		t.Fatalf("home the account at Kingston: %v", err)
	}
	orders := s.countAll(`SELECT count(*) FROM orders WHERE customer_id = $1::uuid`, cust)

	// the swap's replacement order prices at the account's type under the same ruling: the same figures.
	if got := swapTotal("account yard held"); got != atHome {
		t.Fatalf("the swap's replacement order must price as before for a yard held account: %v then %v", atHome, got)
	}

	// a RETURN of the branch's own sale is allowed.
	ret := s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", s.returnBody(key(), orig, 0, RefundCash, 1))
	if ret.Code != http.StatusCreated && ret.Code != http.StatusOK {
		t.Fatalf("a return of the branch's own sale to a yard held account must be allowed, got %d: %s", ret.Code, ret.Body.String())
	}

	// a NEW sale to it is refused, and writes nothing.
	sale := s.do(http.MethodPost, "/api/v1/pos/sales", s.bearer(trentonClerkSub), "", map[string]any{
		"idempotency_key": key(), "branch_id": trentonBranchID, "till_session_id": s.trentonTill, "customer_id": cust,
		"sale_type": SaleCollectNow, "tender": TenderCash,
		"lines": []map[string]any{{"product_id": l2cProductID, "qty_entered": 1, "uom_entered": "SF"}},
	})
	if sale.Code != http.StatusNotFound || sale.Body.String() != "{\"error\":\"customer not found\"}\n" {
		t.Fatalf("a new sale to a yard held account must meet the wall, got %d: %s", sale.Code, sale.Body.String())
	}
	if n := s.countAll(`SELECT count(*) FROM orders WHERE customer_id = $1::uuid`, cust); n != orders {
		t.Fatalf("the refused sale wrote an order (%d then %d)", orders, n)
	}

	// the margin glance prices a new document, so it meets the wall too.
	glance := s.do(http.MethodPost, "/api/v1/pos/margin-glance", s.bearer(trentonMgrSub), "", map[string]any{
		"branch_id": trentonBranchID, "customer_id": cust,
		"lines": []map[string]any{{"product_id": l2cProductID, "qty_entered": 1, "uom_entered": "SF"}},
	})
	if glance.Code != http.StatusNotFound {
		t.Fatalf("the margin glance for a yard held account must meet the wall, got %d: %s", glance.Code, glance.Body.String())
	}
}
