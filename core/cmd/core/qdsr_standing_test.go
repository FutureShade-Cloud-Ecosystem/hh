package main

// qdsr_standing_test.go, the account-standing read made answerable (Quote Desk server reads, read
// one): over the registered surface and real rows, a walk-in, a clear account and a held account
// answer distinctly in the body; an unknown account is a 404, a malformed id a 400, a caller the
// dealer's balance policy refuses a 403, a branch the caller does not reach a 403, and a caller
// without the counter grant the gate's own 403. The old fields stay: a held account still answers
// on_hold true.

import (
	"context"
	"net/http"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestQDSRAccountStandingAnswersEveryCaseDistinctly(t *testing.T) {
	fx := newXRoleFixture(t)
	pool := fx.env.fixturePool()
	t.Cleanup(pool.Close)
	qdsrPlant(t, pool,
		qdsrAccount{id: qdsrClearID, accountNo: "QDSR-CLEAR", name: "Fixture Clear Account", branch: qdsrNorthA},
		qdsrAccount{id: qdsrHeldID, accountNo: "QDSR-HELD", name: "Fixture Held Account", branch: qdsrNorthA, held: true},
	)
	base := "/api/v1/pos/account-standing?branch_id=" + qdsrNorthA
	sales := testkit.SubInsideSales

	// A walk-in names no customer and is its own answer.
	code, body := qdsrGet(t, fx, sales, base)
	walkIn := qdsrJSON(t, code, body)
	if walkIn["standing"] != "walk_in" {
		t.Fatalf("a walk-in answers %s, want standing walk_in", body)
	}

	// A clear account is distinct from the walk-in and from the empty object of old.
	code, body = qdsrGet(t, fx, sales, base+"&customer_id="+qdsrClearID)
	clear := qdsrJSON(t, code, body)
	if clear["standing"] != "clear" {
		t.Fatalf("a clear account answers %s, want standing clear", body)
	}
	if _, has := clear["on_hold"]; has {
		t.Fatalf("a clear account carries on_hold: %s", body)
	}

	// A held account says so, in the new word and in the old field, though it owes nothing past due.
	code, body = qdsrGet(t, fx, sales, base+"&customer_id="+qdsrHeldID)
	held := qdsrJSON(t, code, body)
	if held["standing"] != "on_hold" || held["on_hold"] != true {
		t.Fatalf("a held account answers %s, want standing on_hold and on_hold true", body)
	}

	// An account that does not exist is not "clear".
	code, body = qdsrGet(t, fx, sales, base+"&customer_id="+qdsrAbsentID)
	if code != http.StatusNotFound {
		t.Fatalf("an unknown account answers %d %s, want 404", code, body)
	}
	// A malformed id is the caller's mistake, never a server fault.
	code, body = qdsrGet(t, fx, sales, base+"&customer_id=not-a-uuid")
	if code != http.StatusBadRequest {
		t.Fatalf("a malformed id answers %d %s, want 400", code, body)
	}
	// A branch the caller does not reach is refused, never answered.
	code, body = qdsrGet(t, fx, sales, "/api/v1/pos/account-standing?branch_id="+testkit.BranchNorthBID+"&customer_id="+qdsrClearID)
	if code != http.StatusForbidden {
		t.Fatalf("another branch answers %d %s, want 403", code, body)
	}
	// A role holding no counter or quote desk grant meets the gate's refusal.
	code, body = qdsrGet(t, fx, testkit.SubDriver, base+"&customer_id="+qdsrClearID)
	rgRefused(t, "a driver is held off the standing read", code, body)

	// The dealer's balance policy refuses the clerk and still admits the manager.
	setPolicy := func(v string) {
		if _, err := pool.Exec(context.Background(),
			`UPDATE pos_policy SET balance_visibility = $1 WHERE branch_id IS NULL`, v); err != nil {
			t.Fatalf("set balance_visibility %s: %v", v, err)
		}
	}
	setPolicy("MANAGER_UP")
	t.Cleanup(func() { setPolicy("ALL_STAFF") })
	code, body = qdsrGet(t, fx, sales, base+"&customer_id="+qdsrHeldID)
	if code != http.StatusForbidden {
		t.Fatalf("a clerk under MANAGER_UP answers %d %s, want 403 and no empty 200", code, body)
	}
	code, body = qdsrGet(t, fx, testkit.SubSalesManager, base+"&customer_id="+qdsrHeldID)
	if m := qdsrJSON(t, code, body); m["standing"] != "on_hold" {
		t.Fatalf("a manager under MANAGER_UP answers %s, want on_hold", body)
	}
	setPolicy("HIDDEN")
	code, body = qdsrGet(t, fx, testkit.SubSalesManager, base+"&customer_id="+qdsrHeldID)
	if code != http.StatusForbidden {
		t.Fatalf("a manager under HIDDEN answers %d %s, want 403", code, body)
	}
}
