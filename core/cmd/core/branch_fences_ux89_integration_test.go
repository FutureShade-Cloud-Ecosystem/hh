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
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/finstmt"
	coreidentity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
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

// Round 2: the billing address write holds the customer wall, and only a GLOBAL caller files a company wide
// (null home) account. Refusal words: a missing home is the same 403 as an ungranted one.
func TestUX89R2_BillingAddressWallAndNullHomeCreate(t *testing.T) {
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
	plant := func(acct, branch string) string {
		var id string
		if err := graph.pool.QueryRow(ctx, `
			INSERT INTO customers (account_no, name, customer_type, home_branch_id, org_id)
			VALUES ($1, 'UX89 r2 proof', 'CONTRACTOR', $2::uuid, $3::uuid) RETURNING id::text`,
			acct, branch, testkit.OrgNorthID).Scan(&id); err != nil {
			t.Fatalf("plant customer: %v", err)
		}
		return id
	}
	custB := plant("UX89R2-B-"+suffix, testkit.BranchNorthBID)
	custA := plant("UX89R2-A-"+suffix, testkit.BranchNorthAID)
	t.Cleanup(func() {
		_, _ = graph.pool.Exec(context.Background(), `DELETE FROM customers WHERE account_no LIKE $1`, "UX89R2-%-"+suffix)
	})
	const mgr = testkit.SubSalesManager

	// 1. billing address: the account read's rule, a leg each way.
	addr := `{"line1":"1 Fence St","city":"Belleville","province":"ON","postal":"K8V 5P7"}`
	want(do(mgr, http.MethodPut, "/api/v1/customers/"+custB+"/billing-address", addr), http.StatusNotFound, "billing address of North B's account")
	var billing string
	if err := graph.pool.QueryRow(ctx, `SELECT billing_address::text FROM customers WHERE id = $1`, custB).Scan(&billing); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(billing, "Fence St") {
		t.Errorf("a refused address write landed: %s", billing)
	}
	want(do(mgr, http.MethodPut, "/api/v1/customers/"+custA+"/billing-address", addr), http.StatusOK, "billing address of North A's account")

	// 2. null home create.
	create := func(sub, acct, homeField string) *httptest.ResponseRecorder {
		return do(sub, http.MethodPost, "/api/v1/customers",
			fmt.Sprintf(`{"account_no":%q,"name":"UX89 r2 created","customer_type":"RETAIL"%s}`, acct, homeField))
	}
	want(create(mgr, "UX89R2-N1-"+suffix, ""), http.StatusForbidden, "scoped caller, home missing")
	want(create(mgr, "UX89R2-N2-"+suffix, `,"home_branch_id":""`), http.StatusForbidden, "scoped caller, home empty")
	if n := 0; true {
		_ = graph.pool.QueryRow(ctx, `SELECT count(*) FROM customers WHERE account_no LIKE $1`, "UX89R2-N_-"+suffix).Scan(&n)
		if n != 0 {
			t.Errorf("a refused null home create wrote %d accounts", n)
		}
	}
	want(create(mgr, "UX89R2-G1-"+suffix, `,"home_branch_id":"`+testkit.BranchNorthAID+`"`), http.StatusCreated, "scoped caller, granted home")
	// (the GLOBAL caller's null home create is proven at the handler in the customer package: the fixture's
	// general manager is refused by the role gate on this route before the wall.)
}

// Round 2, item 113: every door that takes a customer agrees. The rule is the customer module's own: an account
// homed at a branch is held to callers who reach that branch, an account with no home is shared company wide.
// The ONE answer for an account the caller cannot use, whether it is homed at North B or is no account at all:
// 404 {"error":"customer not found"} on the pos and pricing customer doors, and "not found" where the door
// finds a rule or override by its own id. Every refused write lands nothing.
func TestUX89R2_EveryCustomerDoorAgrees(t *testing.T) {
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
	one := func(q string, args ...any) string {
		t.Helper()
		var v string
		if err := graph.pool.QueryRow(ctx, q, args...).Scan(&v); err != nil {
			t.Fatalf("query: %v", err)
		}
		return v
	}
	plant := func(acct string, home any) string {
		return one(`INSERT INTO customers (account_no, name, customer_type, home_branch_id, org_id)
			VALUES ($1, 'UX89 r2 door proof', 'CONTRACTOR', $2::uuid, $3::uuid) RETURNING id::text`, acct, home, testkit.OrgNorthID)
	}
	custB := plant("UX89D-B-"+suffix, testkit.BranchNorthBID)
	custShared := plant("UX89D-S-"+suffix, nil)
	const unknown = "3ef10000-0000-4000-8000-0000000000ee"
	product := testkit.ProductID
	category := one(`INSERT INTO product_categories (code, name) VALUES ($1, $2) RETURNING id::text`, "UX89D"+suffix, "UX89 door cat "+suffix)
	ruleB := one(`INSERT INTO pricing_rules (scope, customer_id, product_id, calc_type, price_cents, price_uom, org_id)
		VALUES ('CUSTOMER_PRODUCT', $1::uuid, $2::uuid, 'ABSOLUTE', 4242, 'EA', $3::uuid) RETURNING id::text`, custB, product, testkit.OrgNorthID)
	overrideB := one(`INSERT INTO customer_category_overrides (customer_id, category_id, calc_type, discount_bps, price_uom)
		VALUES ($1::uuid, $2::uuid, 'PCT_OFF_LIST', 5000, 'EA') RETURNING id::text`, custB, category)
	tillA := one(`INSERT INTO till_sessions (branch_id, station, status, opened_by) VALUES ($1::uuid, $2, 'OPEN', $3::uuid) RETURNING id::text`,
		testkit.BranchNorthAID, "UX89D-"+suffix, testkit.UserInsideSalesID)
	t.Cleanup(func() {
		bg := context.Background()
		for _, q := range []string{
			`DELETE FROM pos_parked_tickets WHERE customer_id::text = ANY($1)`,
			`DELETE FROM customer_category_overrides WHERE customer_id::text = ANY($1)`,
			`DELETE FROM pricing_rules WHERE customer_id::text = ANY($1)`,
		} {
			_, _ = graph.pool.Exec(bg, q, []string{custB, custShared})
		}
		_, _ = graph.pool.Exec(bg, `DELETE FROM till_sessions WHERE id = $1`, tillA)
		_, _ = graph.pool.Exec(bg, `DELETE FROM product_categories WHERE id = $1`, category)
		_, _ = graph.pool.Exec(bg, `DELETE FROM customers WHERE account_no LIKE $1`, "UX89D-%-"+suffix)
	})
	count := func(q string, args ...any) int {
		var n int
		if err := graph.pool.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}

	const rep, mgr = testkit.SubInsideSales, testkit.SubSalesManager
	// same asserts: an unreachable account and an unknown id give one answer (status and body byte for byte),
	// and the shared account is never answered as not found.
	agree := func(door, who, method string, path func(string) string, bodyFor func(cust string) string, wantStatus int, wantBody string) {
		t.Helper()
		b := do(who, method, path(custB), bodyFor(custB))
		u := do(who, method, path(unknown), bodyFor(unknown))
		if b.Code != wantStatus || u.Code != wantStatus {
			t.Errorf("%s: want %d for North B's account and for an unknown id, got %d and %d: %s | %s", door, wantStatus, b.Code, u.Code, b.Body.String(), u.Body.String())
		}
		if b.Body.String() != u.Body.String() || strings.TrimSpace(b.Body.String()) != wantBody {
			t.Errorf("%s: the two answers differ or are not %s: %q | %q", door, wantBody, b.Body.String(), u.Body.String())
		}
		if s := do(who, method, path(custShared), bodyFor(custShared)); s.Code == http.StatusNotFound && strings.Contains(s.Body.String(), "customer not found") {
			t.Errorf("%s: the shared (no home) account must trade at North A, got %d: %s", door, s.Code, s.Body.String())
		}
	}
	const nf = `{"error":"customer not found"}`
	lines := func(cust string) string {
		return fmt.Sprintf(`{"branch_id":%q,"customer_id":%q,"tender":"CASH","cash_sale":true,"till_session_id":%q,"idempotency_key":"3ef10000-0000-4000-8000-%012d","lines":[{"product_id":%q,"qty_entered":1,"uom_entered":"EA"}]}`,
			testkit.BranchNorthAID, cust, tillA, time.Now().UnixNano()%1_000_000_000_000, product)
	}
	fixedPath := func(p string) func(string) string { return func(string) string { return p } }
	for _, who := range []string{rep, mgr} {
		agree("sale preview", who, http.MethodPost, fixedPath("/api/v1/pos/sale-preview"), lines, 404, nf)
		agree("create sale", who, http.MethodPost, fixedPath("/api/v1/pos/sales"), lines, 404, nf)
		agree("park", who, http.MethodPost, fixedPath("/api/v1/pos/parked"), func(c string) string {
			return fmt.Sprintf(`{"branch_id":%q,"customer_id":%q,"lines":[{"product_id":%q,"qty_entered":1,"uom_entered":"EA"}]}`, testkit.BranchNorthAID, c, product)
		}, 404, nf)
		agree("counter prices", who, http.MethodPost, fixedPath("/api/v1/pos/counter-prices"), func(c string) string {
			return fmt.Sprintf(`{"branch_id":%q,"customer_id":%q,"items":[{"product_id":%q,"qty_entered":1,"uom_entered":"EA"}]}`, testkit.BranchNorthAID, c, product)
		}, 404, nf)
		agree("pricing order create", who, http.MethodPost, fixedPath("/api/v1/pos/pricing-orders"), func(c string) string {
			return fmt.Sprintf(`{"branch_id":%q,"customer_id":%q,"lines":[{"product_id":%q,"qty_entered":1,"uom_entered":"EA"}]}`, testkit.BranchNorthAID, c, product)
		}, 404, nf)
	}
	agree("overrides list", mgr, http.MethodGet, func(c string) string { return "/api/v1/pricing/customers/" + c + "/overrides" }, func(string) string { return "" }, 404, nf)
	agree("overrides create", mgr, http.MethodPost, func(c string) string { return "/api/v1/pricing/customers/" + c + "/overrides" }, func(string) string {
		return fmt.Sprintf(`{"category_id":%q,"calc_type":"PCT_OFF_LIST","discount_bps":5000,"price_uom":"EA"}`, category)
	}, 404, nf)
	agree("floor preview", mgr, http.MethodPost, fixedPath("/api/v1/pricing/floor-preview"), func(c string) string {
		return fmt.Sprintf(`{"kind":"override","customer_id":%q,"override":{"category_id":%q,"calc_type":"PCT_OFF_LIST","discount_bps":5000,"price_uom":"EA"}}`, c, category)
	}, 404, nf)

	// no write landed on North B's account, and the shared account's park worked (it is the one that landed).
	if n := count(`SELECT count(*) FROM orders WHERE customer_id = $1`, custB); n != 0 {
		t.Errorf("a refused sale wrote %d orders on North B's account", n)
	}
	if n := count(`SELECT count(*) FROM pos_parked_tickets WHERE customer_id = $1`, custB); n != 0 {
		t.Errorf("a refused park wrote %d tickets on North B's account", n)
	}
	if n := count(`SELECT count(*) FROM customer_category_overrides WHERE customer_id = $1`, custB); n != 1 {
		t.Errorf("North B's account holds %d overrides, want only the planted one", n)
	}
	if n := count(`SELECT count(*) FROM pos_parked_tickets WHERE customer_id = $1`, custShared); n == 0 {
		t.Errorf("the shared account could not be parked for at North A")
	}

	// the rules list and the by-id rule and override doors: North B's account has no rules and no overrides to
	// this caller, as if it did not exist; the shared account's rule would be listed.
	list := do(mgr, http.MethodGet, "/api/v1/pricing/rules?scope=CUSTOMER_PRODUCT", "")
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), ruleB) || strings.Contains(list.Body.String(), "4242") {
		t.Errorf("rules list: want 200 without North B's contract rule, got %d: %s", list.Code, list.Body.String())
	}
	for _, c := range []struct{ door, method, path, body string }{
		{"rule by id", http.MethodGet, "/api/v1/pricing/rules/" + ruleB, ""},
		{"rule update", http.MethodPut, "/api/v1/pricing/rules/" + ruleB, `{"price_cents":1,"version":1}`},
		{"rule delete", http.MethodDelete, "/api/v1/pricing/rules/" + ruleB + "?version=1", ""},
		{"override update", http.MethodPut, "/api/v1/pricing/overrides/" + overrideB, `{"calc_type":"PCT_OFF_LIST","discount_bps":9000,"price_uom":"EA","version":1}`},
		{"override delete", http.MethodDelete, "/api/v1/pricing/overrides/" + overrideB + "?version=1", ""},
	} {
		if rsp := do(mgr, c.method, c.path, c.body); rsp.Code != http.StatusNotFound {
			t.Errorf("%s on North B's account: want 404, got %d: %s", c.door, rsp.Code, rsp.Body.String())
		}
	}
	if n := count(`SELECT count(*) FROM pricing_rules WHERE id = $1 AND is_active AND deleted_at IS NULL AND price_cents = 4242`, ruleB); n != 1 {
		t.Errorf("a refused rule write changed North B's rule")
	}
	if n := count(`SELECT count(*) FROM customer_category_overrides WHERE id = $1 AND discount_bps = 5000`, overrideB); n != 1 {
		t.Errorf("a refused override write changed North B's override")
	}
}

// Round 2, item 121.3: the AR doors that take a customer hold the same wall with the same one answer
// (404 {"error":"customer not found"}, identical for an account homed at North B and for an id that is no
// account), and the shared (no home) account works at North A: the statement preview, the AP contact routes
// (read, write, delete), the account payment policy (read, write), and the manual hold and paylink beside the
// preview. No refused write lands.
func TestUX89R2_ARDoorsAgree(t *testing.T) {
	// The AR routes sit behind role configuration levels no kit role below the global one passes, and a global
	// caller reaches every branch, so the doors are mounted here on the kit's StaffGuards double (the real
	// guards over a settable authority) with the real handlers, pool and customer seam, and the caller is a
	// branch scoped manager granted North A only.
	pool := wiringPool(t)
	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(coreidentity.StaffAuthority{RoleID: "sales_manager", Tier: coreidentity.TierManager, Scope: coreidentity.ScopeBranch, Branches: []string{testkit.BranchNorthAID}})
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reach := customerModuleReach{svc: customer.NewService(customer.NewRepository(pool))}
	mux := http.NewServeMux()
	arRepo := billing.NewARLandingRepository(pool)
	billing.NewARLandingHandler(billing.NewARLandingService(arRepo),
		billing.NewARReminderService(arRepo, billing.NewARLandingService(arRepo), billing.UnwiredSender{}, "").WithCustomerReach(reach), log).
		WithCustomerReach(reach).RegisterRoutes(mux, sg.Guards)
	billing.NewC8S3S4Handler(billing.NewRepository(pool), log).WithCustomerReach(reach).RegisterRoutes(mux, sg.Guards)
	finstmt.NewC8WalkHandler(pool, log).WithCustomerReach(reach).RegisterC8WalkRoutes(mux, sg.Guards)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
	token := sg.Bearer(t)
	do := func(_ string, method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set(authz.HeaderXOrg, testkit.OrgNorthSlug)
		req.Header.Set("Content-Type", "application/json")
		rsp := httptest.NewRecorder()
		mux.ServeHTTP(rsp, req)
		return rsp
	}
	graph := struct{ pool *pgxpool.Pool }{pool}
	one := func(q string, args ...any) string {
		t.Helper()
		var v string
		if err := graph.pool.QueryRow(ctx, q, args...).Scan(&v); err != nil {
			t.Fatalf("query: %v", err)
		}
		return v
	}
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := graph.pool.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}
	plant := func(acct string, home any) string {
		return one(`INSERT INTO customers (account_no, name, customer_type, home_branch_id, org_id)
			VALUES ($1, 'UX89 AR door proof', 'CONTRACTOR', $2::uuid, $3::uuid) RETURNING id::text`, acct, home, testkit.OrgNorthID)
	}
	custB := plant("UX89AR-B-"+suffix, testkit.BranchNorthBID)
	custShared := plant("UX89AR-S-"+suffix, nil)
	const unknown = "3ef10000-0000-4000-8000-0000000000ef"
	run := one(`INSERT INTO ar_statement_runs (mode, as_of_date, status) VALUES ('INTERIM', current_date, 'COMPLETED') RETURNING id::text`)
	stmt := map[string]string{}
	for _, c := range []string{custB, custShared} {
		stmt[c] = one(`INSERT INTO ar_statements (run_id, customer_id, as_of_date) VALUES ($1::uuid, $2::uuid, current_date) RETURNING id::text`, run, c)
		if _, err := graph.pool.Exec(ctx, `INSERT INTO ar_ap_contacts (customer_id, contact_name, email) VALUES ($1::uuid, 'Original AP', 'ap@example.test')`, c); err != nil {
			t.Fatalf("plant ap contact: %v", err)
		}
		if _, err := graph.pool.Exec(ctx, `INSERT INTO ar_account_payment_policy (customer_id, ach_first, surcharge_rate_bps, min_ach_cents) VALUES ($1::uuid, false, 100, 5000)`, c); err != nil {
			t.Fatalf("plant payment policy: %v", err)
		}
	}
	t.Cleanup(func() {
		bg := context.Background()
		ids := []string{custB, custShared}
		_, _ = graph.pool.Exec(bg, `DELETE FROM ar_statements WHERE run_id = $1`, run)
		_, _ = graph.pool.Exec(bg, `DELETE FROM ar_statement_runs WHERE id = $1`, run)
		_, _ = graph.pool.Exec(bg, `DELETE FROM ar_ap_contacts WHERE customer_id::text = ANY($1)`, ids)
		_, _ = graph.pool.Exec(bg, `DELETE FROM ar_account_payment_policy WHERE customer_id::text = ANY($1)`, ids)
		_, _ = graph.pool.Exec(bg, `DELETE FROM credit_holds WHERE customer_id::text = ANY($1)`, ids)
		_, _ = graph.pool.Exec(bg, `DELETE FROM customers WHERE account_no LIKE $1`, "UX89AR-%-"+suffix)
	})

	const rep, mgr = "rep", "mgr"
	const nf = `{"error":"customer not found"}`
	agree := func(door, who, method string, path func(string) string, body string) {
		t.Helper()
		b := do(who, method, path(custB), body)
		u := do(who, method, path(unknown), body)
		if b.Code != 404 || u.Code != 404 || b.Body.String() != u.Body.String() || strings.TrimSpace(b.Body.String()) != nf {
			t.Errorf("%s: want one 404 %s for North B's account and an unknown id, got %d %q | %d %q", door, nf, b.Code, b.Body.String(), u.Code, u.Body.String())
		}
	}
	shared := func(door, who, method, path, body string, want int) {
		t.Helper()
		if s := do(who, method, path, body); s.Code != want {
			t.Errorf("%s: the shared account must work at North A: want %d, got %d: %s", door, want, s.Code, s.Body.String())
		}
	}
	prev := func(c string) string {
		sid := stmt[c]
		if sid == "" {
			sid = "3ef10000-0000-4000-8000-0000000000f0"
		}
		return "/api/v1/billing/ar/customers/" + c + "/statements/" + sid + "/preview"
	}
	ap := func(c string) string { return "/api/v1/ar/customers/" + c + "/ap-contact" }
	pol := func(c string) string { return "/api/v1/ar/customers/" + c + "/payment-policy" }

	agree("statement preview", rep, http.MethodGet, prev, "")
	shared("statement preview", rep, http.MethodGet, prev(custShared), "", http.StatusOK)
	agree("ap contact read", mgr, http.MethodGet, ap, "")
	shared("ap contact read", mgr, http.MethodGet, ap(custShared), "", http.StatusOK)
	agree("ap contact write", mgr, http.MethodPut, ap, `{"contact_name":"Intruder","email":"x@example.test"}`)
	shared("ap contact write", mgr, http.MethodPut, ap(custShared), `{"contact_name":"Shared AP","email":"s@example.test"}`, http.StatusOK)
	agree("ap contact delete", mgr, http.MethodDelete, ap, "")
	agree("payment policy read", rep, http.MethodGet, pol, "")
	shared("payment policy read", rep, http.MethodGet, pol(custShared), "", http.StatusOK)
	agree("payment policy write", mgr, http.MethodPut, pol, `{"ach_first":true,"surcharge_rate_bps":999,"min_ach_cents":1}`)
	shared("payment policy write", mgr, http.MethodPut, pol(custShared), `{"ach_first":true,"surcharge_rate_bps":200,"min_ach_cents":100}`, http.StatusOK)

	// the hold and paylink carry the customer in the body: one leg each.
	for _, c := range []struct{ door, path, body string }{
		{"manual hold", "/api/v1/ar/holds", `{"customer_id":"%s","reason":"ux89 proof"}`},
		{"paylink", "/api/v1/ar/payments/paylink", `{"customer_id":"%s","channel":"EMAIL"}`},
	} {
		b := do(mgr, http.MethodPost, c.path, fmt.Sprintf(c.body, custB))
		u := do(mgr, http.MethodPost, c.path, fmt.Sprintf(c.body, unknown))
		if b.Code != 404 || u.Code != 404 || b.Body.String() != u.Body.String() || strings.TrimSpace(b.Body.String()) != nf {
			t.Errorf("%s: want one 404 %s, got %d %q | %d %q", c.door, nf, b.Code, b.Body.String(), u.Code, u.Body.String())
		}
	}

	// no refused write landed on North B's account.
	if n := count(`SELECT count(*) FROM ar_ap_contacts WHERE customer_id = $1 AND contact_name = 'Original AP'`, custB); n != 1 {
		t.Errorf("North B's AP contact was changed or deleted")
	}
	if n := count(`SELECT count(*) FROM ar_account_payment_policy WHERE customer_id = $1 AND surcharge_rate_bps = 100 AND min_ach_cents = 5000 AND NOT ach_first`, custB); n != 1 {
		t.Errorf("North B's payment policy was changed")
	}
	if n := count(`SELECT count(*) FROM credit_holds WHERE customer_id = $1`, custB); n != 0 {
		t.Errorf("a refused manual hold landed on North B's account")
	}
}
