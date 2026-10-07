package main

// qdsr_customer_test.go, the customer read by id (Quote Desk server reads, read two): over the
// registered surface and real rows, the read carries the credit limit and the current balance with
// the account facts the search carries, behind the same grants as the search; an unknown id is a
// 404, a malformed id a 400, an account homed to a branch the caller does not reach a 404 (the
// lookup's rule: a fence that says "exists but not yours" is the oracle it was built to close), and
// a role with no counter or quote desk grant meets the gate's 403.

import (
	"fmt"
	"net/http"
	"testing"

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
		"credit_limit_cents": float64(500_000), "balance_cents": float64(123_456),
		"on_hold": false, "tax_exempt": false, "is_active": true,
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("field %s is %v, want %v (body %s)", k, got[k], v, body)
		}
	}

	// An account with no ledger rows owes nothing, and says 0 rather than leaving the field out.
	code, body = qdsrGet(t, fx, sales, path(qdsrHeldID))
	if z := qdsrJSON(t, code, body); z["balance_cents"] != float64(0) || z["credit_limit_cents"] != float64(0) {
		t.Fatalf("an account with no ledger answers zero balance and limit: %s", body)
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
	if o := qdsrJSON(t, code, body); o["credit_limit_cents"] != float64(500_000) {
		t.Fatalf("the Quote Desk sales role reads the account: %s", body)
	}
	// The same grants as the search: no counter or quote desk cell, no read.
	for _, sub := range []string{testkit.SubDriver, xrSubYardTeam} {
		code, body = qdsrGet(t, fx, sub, path(qdsrClearID))
		rgRefused(t, fmt.Sprintf("%s is held off the customer read", sub), code, body)
	}
}
