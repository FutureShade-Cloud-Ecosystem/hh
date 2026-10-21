package main

// qdsr_customer_test.go, the customer read by id (Quote Desk server reads, read two): over the
// registered surface and real rows, the read carries the credit limit and the current balance with
// the account facts the search carries, behind the same grants as the search; an unknown id is a
// 404, a malformed id a 400, an account homed to a branch the caller does not reach a 404 (the
// lookup's rule: a fence that says "exists but not yours" is the oracle it was built to close), and
// a role with no counter or quote desk grant meets the gate's 403.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestQDSRCustomerReadByIDCarriesLimitAndBalance(t *testing.T) {
	fx := newXRoleFixture(t)
	pool := fx.env.fixturePool()
	t.Cleanup(pool.Close)
	qdsrPlant(t, pool,
		qdsrAccount{id: qdsrClearID, accountNo: "QDSR-LIMIT", name: "Fixture Limit Account", branch: qdsrNorthA,
			limitCents: 500_000, balanceCents: 123_456},
		qdsrAccount{id: qdsrHeldID, accountNo: "QDSR-NOBAL", name: "Fixture No Balance Account", branch: qdsrNorthA},
		qdsrAccount{id: qdsrOtherID, accountNo: "QDSR-ELSE", name: "Fixture Other Branch Account", branch: testkit.BranchNorthBID,
			limitCents: 10_000, balanceCents: 5_000},
	)
	path := func(id string) string { return "/api/v1/customers/" + id }
	sales := testkit.SubInsideSales

	code, body := qdsrGet(t, fx, sales, path(qdsrClearID))
	got := qdsrJSON(t, code, body)
	want := map[string]any{
		"customer_id": qdsrClearID, "account_no": "QDSR-LIMIT", "name": "Fixture Limit Account",
		"customer_type": "CONTRACTOR", "branch_id": qdsrNorthA, "payment_terms_days": float64(30),
		"on_hold": false, "tax_exempt": false, "is_active": true,
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("field %s is %v, want %v (body %s)", k, got[k], v, body)
		}
	}
	// UX-97: the dealer default is MANAGER_UP, so a front-line sales caller meets the account
	// without its position: the keys are absent, not zero.
	for _, k := range []string{"credit_limit_cents", "balance_cents", "available_credit_cents"} {
		if _, has := got[k]; has {
			t.Fatalf("a front-line caller under MANAGER_UP must not receive %s: %s", k, body)
		}
	}

	// A manager under the same policy reads the position. An account with no ledger rows owes
	// nothing, and says 0 rather than leaving the field out.
	code, body = qdsrGet(t, fx, testkit.SubGeneralMgr, path(qdsrHeldID))
	if z := qdsrJSON(t, code, body); z["balance_cents"] != float64(0) || z["credit_limit_cents"] != float64(0) || z["available_credit_cents"] != float64(0) {
		t.Fatalf("an account with no ledger answers zero balance and limit to a manager: %s", body)
	}
	// The core computes the headroom, so no client subtracts it.
	code, body = qdsrGet(t, fx, testkit.SubGeneralMgr, path(qdsrClearID))
	if c := qdsrJSON(t, code, body); c["available_credit_cents"] != float64(500_000-123_456) {
		t.Fatalf("the core's available credit is the limit less the balance: %s", body)
	}

	// Unknown and malformed ids.
	code, body = qdsrGet(t, fx, sales, path(qdsrAbsentID))
	if code != http.StatusNotFound {
		t.Fatalf("an unknown id answers %d %s, want 404", code, body)
	}
	code, body = qdsrGet(t, fx, sales, path("not-a-uuid"))
	if code != http.StatusBadRequest {
		t.Fatalf("a malformed id answers %d %s, want 400", code, body)
	}

	// The branch fence: a branch-scoped caller does not see another branch's account (404), a global
	// caller holding the counter read does.
	code, body = qdsrGet(t, fx, sales, path(qdsrOtherID))
	if code != http.StatusNotFound {
		t.Fatalf("another branch's account answers %d %s to a branch caller, want 404", code, body)
	}
	code, body = qdsrGet(t, fx, testkit.SubGeneralMgr, path(qdsrOtherID))
	if o := qdsrJSON(t, code, body); o["balance_cents"] != float64(5_000) {
		t.Fatalf("a global caller reads any branch's account: %s", body)
	}

	// Reaches the Quote Desk's roles as the search does: outside sales holds the Quote Desk and no
	// Counter Till cell, and reads the account at its branch.
	qdsrPlantOutsideSales(t, fx.env, pool)
	code, body = qdsrGet(t, fx, qdsrSubOutsideSales, path(qdsrClearID))
	if o := qdsrJSON(t, code, body); o["account_no"] != "QDSR-LIMIT" {
		t.Fatalf("the Quote Desk sales role reads the account: %s", body)
	}
	// The same grants as the search: no counter or quote desk cell, no read.
	for _, sub := range []string{testkit.SubDriver, xrSubYardTeam} {
		code, body = qdsrGet(t, fx, sub, path(qdsrClearID))
		rgRefused(t, fmt.Sprintf("%s is held off the customer read", sub), code, body)
	}
}

// qdsrSetPolicy sets the dealer default account position policy, and one branch row when branch is
// not empty, restoring both when the test ends.
func qdsrSetPolicy(t *testing.T, pool *pgxpool.Pool, dealerDefault, branch, branchValue string) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE ar_invoice_policy SET account_position_visibility = $1 WHERE branch_id IS NULL`, dealerDefault); err != nil {
		t.Fatalf("set default policy: %v", err)
	}
	if branch != "" {
		if _, err := pool.Exec(ctx, `INSERT INTO ar_invoice_policy (branch_id, account_position_visibility) VALUES ($1::uuid, $2)
			ON CONFLICT (branch_id) DO UPDATE SET account_position_visibility = EXCLUDED.account_position_visibility`, branch, branchValue); err != nil {
			t.Fatalf("set branch policy: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `UPDATE ar_invoice_policy SET account_position_visibility = 'MANAGER_UP' WHERE branch_id IS NULL`)
		if branch != "" {
			_, _ = pool.Exec(ctx, `DELETE FROM ar_invoice_policy WHERE branch_id = $1::uuid`, branch)
		}
	})
}

// UX-97: the search and the by-id read apply ar_invoice_policy.account_position_visibility the way
// the receivables register does. A role leg each way (the front-line clerk and the manager) under
// each policy value, and a HIDDEN branch row that overrides a permissive dealer default.
func TestUX97CustomerReadsFollowTheAccountPositionPolicy(t *testing.T) {
	fx := newXRoleFixture(t)
	pool := fx.env.fixturePool()
	t.Cleanup(pool.Close)
	qdsrPlant(t, pool, qdsrAccount{id: qdsrClearID, accountNo: "UX97-ACCT", name: "Fixture Position Account",
		branch: qdsrNorthA, limitCents: 500_000, balanceCents: 123_456})
	byID := "/api/v1/customers/" + qdsrClearID
	search := "/api/v1/customers/search?q=UX97-ACCT&branch_id=" + qdsrNorthA
	clerk, manager := testkit.SubInsideSales, testkit.SubGeneralMgr

	figures := func(sub string) (getHas, searchHas bool) {
		code, body := qdsrGet(t, fx, sub, byID)
		g := qdsrJSON(t, code, body)
		_, l := g["credit_limit_cents"]
		_, b := g["balance_cents"]
		if l != b {
			t.Fatalf("the limit and the balance travel together: %s", body)
		}
		code, body = qdsrGet(t, fx, sub, search)
		if code != http.StatusOK {
			t.Fatalf("search answered %d %s", code, body)
		}
		var rows []map[string]any
		if err := json.Unmarshal([]byte(body), &rows); err != nil || len(rows) != 1 {
			t.Fatalf("search body %s err %v", body, err)
		}
		_, sl := rows[0]["credit_limit_cents"]
		_, sb := rows[0]["open_balance_cents"]
		if sl != sb {
			t.Fatalf("the search limit and balance travel together: %s", body)
		}
		return l, sl
	}
	expect := func(label, sub string, want bool) {
		t.Helper()
		if g, s := figures(sub); g != want || s != want {
			t.Fatalf("%s: by-id has figures %v, search has figures %v, want %v", label, g, s, want)
		}
	}

	qdsrSetPolicy(t, pool, "MANAGER_UP", "", "")
	expect("MANAGER_UP clerk", clerk, false)
	expect("MANAGER_UP manager", manager, true)

	qdsrSetPolicy(t, pool, "ALL_STAFF", "", "")
	expect("ALL_STAFF clerk", clerk, true)
	expect("ALL_STAFF manager", manager, true)

	qdsrSetPolicy(t, pool, "HIDDEN", "", "")
	expect("HIDDEN clerk", clerk, false)
	expect("HIDDEN manager", manager, false)

	// A branch row overrides the dealer default for that branch's accounts and searches.
	qdsrSetPolicy(t, pool, "ALL_STAFF", qdsrNorthA, "HIDDEN")
	expect("HIDDEN branch under an ALL_STAFF default, clerk", clerk, false)
	expect("HIDDEN branch under an ALL_STAFF default, manager", manager, false)

	// A refused caller still finds the account: only the position is withheld.
	code, body := qdsrGet(t, fx, clerk, search)
	if code != http.StatusOK || !strings.Contains(body, "UX97-ACCT") {
		t.Fatalf("a refused caller still finds the account: %d %s", code, body)
	}
}

// UX-97 round 2: the cash on demand posture is stated by the core as its own field under every policy
// value, to the front-line clerk the policy refuses the figures, and to the manager alike.
func TestUX97CashOnDemandIsStatedWhateverThePolicy(t *testing.T) {
	fx := newXRoleFixture(t)
	pool := fx.env.fixturePool()
	t.Cleanup(pool.Close)
	qdsrPlant(t, pool,
		qdsrAccount{id: qdsrClearID, accountNo: "UX97-TERMS", name: "Fixture Terms Account", branch: qdsrNorthA, limitCents: 500_000},
		qdsrAccount{id: qdsrHeldID, accountNo: "UX97-COD", name: "Fixture Cod Account", branch: qdsrNorthA, limitCents: 400_000},
	)
	if _, err := pool.Exec(context.Background(), `UPDATE customers SET payment_terms_days = 0 WHERE id = $1`, qdsrHeldID); err != nil {
		t.Fatal(err)
	}
	for _, policy := range []string{"MANAGER_UP", "ALL_STAFF", "HIDDEN"} {
		qdsrSetPolicy(t, pool, policy, "", "")
		for _, sub := range []string{testkit.SubInsideSales, testkit.SubGeneralMgr} {
			code, body := qdsrGet(t, fx, sub, "/api/v1/customers/"+qdsrHeldID)
			if got := qdsrJSON(t, code, body); got["cash_on_demand"] != true {
				t.Fatalf("%s %s: the zero day account with a line says cash_on_demand true: %s", policy, sub, body)
			}
			code, body = qdsrGet(t, fx, sub, "/api/v1/customers/"+qdsrClearID)
			if got := qdsrJSON(t, code, body); got["cash_on_demand"] != false {
				t.Fatalf("%s %s: an account on terms says cash_on_demand false: %s", policy, sub, body)
			}
			code, body = qdsrGet(t, fx, sub, "/api/v1/customers/search?q=UX97-COD&branch_id="+qdsrNorthA)
			var rows []map[string]any
			if code != http.StatusOK || json.Unmarshal([]byte(body), &rows) != nil || len(rows) != 1 || rows[0]["cash_on_demand"] != true {
				t.Fatalf("%s %s: the search carries cash_on_demand true: %d %s", policy, sub, code, body)
			}
		}
		// The refused clerk gets the posture and still no figure.
		if policy != "ALL_STAFF" {
			code, body := qdsrGet(t, fx, testkit.SubInsideSales, "/api/v1/customers/"+qdsrHeldID)
			got := qdsrJSON(t, code, body)
			for _, k := range []string{"credit_limit_cents", "balance_cents", "available_credit_cents"} {
				if _, has := got[k]; has {
					t.Fatalf("%s: the clerk must not receive %s: %s", policy, k, body)
				}
			}
		}
	}
}
