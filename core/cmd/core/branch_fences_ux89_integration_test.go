//go:build integration

package main

// branch_fences_ux89_integration_test.go (UX-89): the branch and customer fences the first sweep missed, driven
// AT THE ROUTE through the full served surface as the fixture's callers granted North A. Each door is refused for
// North B (or for an account homed at North B) in its own words, writes nothing there, and still answers the
// caller's own branch. Door by door:
//
//	pricing resolve, low margin report   branch: 403 "outside your grants"; customer: 404 (the account read's answer)
//	counter prices, pricing order create customer: 404 (branch refusal unchanged)
//	staff approvals, reservations, customer create   write at an ungranted branch: 403 "outside your grants"
//	till policy                          branch configuration: 403 with the till's words
//	till session read, z report          not found (no existence oracle on a session id)

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestUX89_BranchAndCustomerFencesAtTheRoute(t *testing.T) {
	env := newCompEnv(t)
	rec, graph := registeredSurface(t, env)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)

	do := func(sub, method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+env.minter.StaffToken(t, sub))
		req.Header.Set(authz.HeaderXOrg, testkit.OrgNorthSlug)
		req.Header.Set("Content-Type", "application/json")
		rsp := httptest.NewRecorder()
		rec.inner.ServeHTTP(rsp, req)
		return rsp
	}
	want := func(rsp *httptest.ResponseRecorder, code int, door string) {
		t.Helper()
		if rsp.Code != code {
			t.Errorf("%s: want %d, got %d: %s", door, code, rsp.Code, rsp.Body.String())
		}
	}
	// No refusal may carry the other yard's branch id or the account's facts.
	quiet := func(rsp *httptest.ResponseRecorder, door string) {
		t.Helper()
		if strings.Contains(rsp.Body.String(), testkit.BranchNorthBID) {
			t.Errorf("%s: the refusal names the other branch: %s", door, rsp.Body.String())
		}
	}

	// Fixtures: a contractor homed at North B, one at North A, and a drawer open at North B.
	plant := func(acct, branch string) string {
		var id string
		if err := graph.pool.QueryRow(ctx, `
			INSERT INTO customers (account_no, name, customer_type, home_branch_id, org_id)
			VALUES ($1, 'UX89 fence proof', 'CONTRACTOR', $2::uuid, $3::uuid) RETURNING id::text`,
			acct, branch, testkit.OrgNorthID).Scan(&id); err != nil {
			t.Fatalf("plant customer: %v", err)
		}
		return id
	}
	custB := plant("UX89-B-"+suffix, testkit.BranchNorthBID)
	custA := plant("UX89-A-"+suffix, testkit.BranchNorthAID)
	var sessionB string
	if err := graph.pool.QueryRow(ctx, `
		INSERT INTO till_sessions (branch_id, station, status, opened_by)
		VALUES ($1::uuid, $2, 'OPEN', $3::uuid) RETURNING id::text`,
		testkit.BranchNorthBID, "UX89-"+suffix, testkit.UserSalesMgrID).Scan(&sessionB); err != nil {
		t.Fatalf("plant drawer at B: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = graph.pool.Exec(bg, `DELETE FROM till_sessions WHERE id = $1`, sessionB)
		_, _ = graph.pool.Exec(bg, `DELETE FROM stock_reservations WHERE reason = $1`, "ux89-"+suffix)
		_, _ = graph.pool.Exec(bg, `DELETE FROM permission_approval_requests WHERE detail = $1`, "ux89-"+suffix)
		_, _ = graph.pool.Exec(bg, `DELETE FROM customers WHERE account_no LIKE $1`, "UX89-%-"+suffix)
	})
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := graph.pool.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}

	const mgr, rep = testkit.SubSalesManager, testkit.SubInsideSales
	product := testkit.ProductID

	// 1. pricing resolve: the branch, then the customer.
	resolve := func(branch, cust string) *httptest.ResponseRecorder {
		return do(rep, http.MethodPost, "/api/v1/pricing/resolve",
			fmt.Sprintf(`{"product_id":%q,"branch_id":%q,"customer_id":%q,"customer_type":"RETAIL","qty_entered":1,"uom_entered":"EA"}`, product, branch, cust))
	}
	r := resolve(testkit.BranchNorthBID, "")
	want(r, http.StatusForbidden, "resolve at North B")
	quiet(r, "resolve at North B")
	want(resolve("", custB), http.StatusNotFound, "resolve, no branch, North B's account")
	want(resolve(testkit.BranchNorthAID, custB), http.StatusNotFound, "resolve at A, North B's account")
	want(resolve(testkit.BranchNorthBID, custB), http.StatusForbidden, "resolve at B, North B's account")
	if rsp := resolve(testkit.BranchNorthAID, custA); rsp.Code == http.StatusForbidden || rsp.Code == http.StatusNotFound {
		t.Errorf("resolve at A for A's account must pass the wall, got %d: %s", rsp.Code, rsp.Body.String())
	}

	// 6. the low margin report: any line behind the wall refuses the batch.
	lm := func(lines string) *httptest.ResponseRecorder {
		return do(rep, http.MethodPost, "/api/v1/pricing/reports/low-margin", `{"lines":[`+lines+`]}`)
	}
	line := func(branch, cust string) string {
		return fmt.Sprintf(`{"product_id":%q,"branch_id":%q,"customer_id":%q,"customer_type":"RETAIL","qty_entered":1,"uom_entered":"EA","cost_cents":100}`, product, branch, cust)
	}
	want(lm(line(testkit.BranchNorthBID, "")), http.StatusForbidden, "low margin line at North B")
	want(lm(line("", custB)), http.StatusNotFound, "low margin line for North B's account")

	// 2. counter prices and pricing order create: the customer.
	cp := func(branch, cust string) *httptest.ResponseRecorder {
		return do(rep, http.MethodPost, "/api/v1/pos/counter-prices",
			fmt.Sprintf(`{"branch_id":%q,"customer_id":%q,"items":[{"product_id":%q,"qty_entered":1,"uom_entered":"EA"}]}`, branch, cust, product))
	}
	r = cp("", custB)
	want(r, http.StatusNotFound, "counter prices, no branch, North B's account")
	if strings.Contains(r.Body.String(), "CONTRACTOR") {
		t.Errorf("counter prices answered the account's type: %s", r.Body.String())
	}
	want(cp(testkit.BranchNorthAID, custB), http.StatusNotFound, "counter prices at A, North B's account")
	want(cp(testkit.BranchNorthBID, ""), http.StatusForbidden, "counter prices at North B")
	want(cp(testkit.BranchNorthAID, custA), http.StatusOK, "counter prices at A, A's account")
	po := func(cust, uom string) *httptest.ResponseRecorder {
		return do(rep, http.MethodPost, "/api/v1/pos/pricing-orders",
			fmt.Sprintf(`{"branch_id":%q,"customer_id":%q,"lines":[{"product_id":%q,"qty_entered":1,"uom_entered":%q}]}`, testkit.BranchNorthAID, cust, product, uom))
	}
	want(po(custB, "EA"), http.StatusNotFound, "pricing order for North B's account")
	// P3: a unit the product has no conversion for is a 422 in words, never a bare 500.
	if rsp := po("", "NOSUCHUOM"); rsp.Code >= 500 {
		t.Errorf("pricing order, no unit conversion: want a 4xx, got %d: %s", rsp.Code, rsp.Body.String())
	}

	// 3. staff approvals: a card is filed only into a queue the caller is granted.
	filed := func() int {
		return count(`SELECT count(*) FROM permission_approval_requests WHERE detail = $1`, "ux89-"+suffix)
	}
	card := func(branch string) *httptest.ResponseRecorder {
		return do(rep, http.MethodPost, "/api/v1/staff/approvals",
			fmt.Sprintf(`{"branch_id":%q,"policy_type":"CREDIT_LIMIT_EXCEEDED","detail":%q}`, branch, "ux89-"+suffix))
	}
	r = card(testkit.BranchNorthBID)
	want(r, http.StatusForbidden, "approval card at North B")
	quiet(r, "approval card at North B")
	if n := filed(); n != 0 {
		t.Errorf("a refused card was filed: %d rows", n)
	}
	want(card(testkit.BranchNorthAID), http.StatusCreated, "approval card at North A")

	// 4. reservations: a hold is placed only at a granted branch.
	held := func() int {
		return count(`SELECT count(*) FROM stock_reservations WHERE reason = $1`, "ux89-"+suffix)
	}
	reserve := func(branch string) *httptest.ResponseRecorder {
		return do(mgr, http.MethodPost, "/api/v1/inventory/reservations",
			fmt.Sprintf(`{"product_id":%q,"branch_id":%q,"res_type":"ORDER","qty_base":1,"reason":%q}`, product, branch, "ux89-"+suffix))
	}
	r = reserve(testkit.BranchNorthBID)
	want(r, http.StatusForbidden, "reservation at North B")
	quiet(r, "reservation at North B")
	if n := held(); n != 0 {
		t.Errorf("a refused hold was written: %d rows", n)
	}
	if rsp := reserve(testkit.BranchNorthAID); rsp.Code == http.StatusForbidden {
		t.Errorf("a hold at the caller's own branch must pass the wall: %s", rsp.Body.String())
	}

	// 5. customer create: an account is homed only at a granted branch.
	create := func(acct, branch string) *httptest.ResponseRecorder {
		return do(mgr, http.MethodPost, "/api/v1/customers",
			fmt.Sprintf(`{"account_no":%q,"name":"UX89 created","customer_type":"RETAIL","home_branch_id":%q}`, acct, branch))
	}
	r = create("UX89-N-"+suffix, testkit.BranchNorthBID)
	want(r, http.StatusForbidden, "customer create homed at North B")
	quiet(r, "customer create homed at North B")
	if n := count(`SELECT count(*) FROM customers WHERE account_no = $1`, "UX89-N-"+suffix); n != 0 {
		t.Errorf("a refused create wrote the account")
	}
	want(create("UX89-M-"+suffix, testkit.BranchNorthAID), http.StatusCreated, "customer create homed at North A")

	// P3: the till. Policy is branch configuration (403, the till's words); a drawer read is not found.
	r = do(mgr, http.MethodGet, "/api/v1/till/policy?branch_id="+testkit.BranchNorthBID, "")
	want(r, http.StatusForbidden, "till policy at North B")
	want(do(mgr, http.MethodGet, "/api/v1/till/policy?branch_id="+testkit.BranchNorthAID, ""), http.StatusOK, "till policy at North A")
	r = do(mgr, http.MethodGet, "/api/v1/till/sessions/"+sessionB, "")
	want(r, http.StatusNotFound, "till session read at North B")
	quiet(r, "till session read at North B")
	r = do(mgr, http.MethodGet, "/api/v1/till/sessions/"+sessionB+"/zreport", "")
	want(r, http.StatusNotFound, "z report at North B")
	quiet(r, "z report at North B")
}
