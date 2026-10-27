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
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

const qdsrPastDueID = "5d5a0000-0000-4000-8000-000000000006"

func TestQDSRAccountStandingAnswersEveryCaseDistinctly(t *testing.T) {
	fx := newXRoleFixture(t)
	pool := fx.env.fixturePool()
	t.Cleanup(pool.Close)
	qdsrPlant(t, pool,
		qdsrAccount{id: qdsrClearID, accountNo: "QDSR-CLEAR", name: "Fixture Clear Account", branch: qdsrNorthA},
		qdsrAccount{id: qdsrHeldID, accountNo: "QDSR-HELD", name: "Fixture Held Account", branch: qdsrNorthA, held: true},
		qdsrAccount{id: qdsrPastDueID, accountNo: "QDSR-PASTDUE", name: "Fixture Past Due Account", branch: qdsrNorthA},
		qdsrAccount{id: qdsrOtherID, accountNo: "QDSR-ELSE", name: "Fixture Other Branch Held", branch: testkit.BranchNorthBID, held: true},
		qdsrAccount{id: qdsrOther2ID, accountNo: "QDSR-ELSE2", name: "Fixture Other Branch Clear", branch: testkit.BranchNorthBID},
		qdsrAccount{id: qdsrUnhomedID, accountNo: "QDSR-UNHOMED", name: "Fixture Unhomed Held", held: true},
	)
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at)
		 VALUES ('INV-QDSR-PASTDUE', $1::uuid, $2::uuid, 'FINALIZED',
		         CURRENT_DATE - 75, CURRENT_DATE - 45, 25000, 0, 25000, 25000, now())`,
		qdsrPastDueID, qdsrNorthA); err != nil {
		t.Fatalf("plant overdue invoice: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM invoices WHERE customer_id = $1::uuid`, qdsrPastDueID); err != nil {
			t.Errorf("cleanup overdue invoice: %v", err)
		}
	})

	base := "/api/v1/pos/account-standing?branch_id=" + qdsrNorthA
	sales := testkit.SubInsideSales
	mgr := testkit.SubSalesManager

	// A walk-in names no customer and is its own answer.
	code, body := qdsrGet(t, fx, sales, base)
	walkIn := qdsrJSON(t, code, body)
	if walkIn["standing"] != "walk_in" {
		t.Fatalf("a walk-in answers %s, want standing walk_in", body)
	}

	// On a default box (ar_invoice_policy.account_position_visibility = MANAGER_UP as migrated,
	// nothing set by hand), a manager reads the clear position and the past-due position with its
	// figures, while a frontline clerk gets {"standing":"withheld"} on both with no money keys or
	// past_due word.
	code, body = qdsrGet(t, fx, mgr, base+"&customer_id="+qdsrClearID)
	clear := qdsrJSON(t, code, body)
	if clear["standing"] != "clear" {
		t.Fatalf("a manager on a clear account answers %s, want standing clear", body)
	}
	if _, has := clear["on_hold"]; has {
		t.Fatalf("a clear account carries on_hold: %s", body)
	}

	code, body = qdsrGet(t, fx, sales, base+"&customer_id="+qdsrClearID)
	withheldClear := qdsrJSON(t, code, body)
	if withheldClear["standing"] != "withheld" || strings.TrimSpace(body) != `{"standing":"withheld"}` {
		t.Errorf("a clerk on a clear account under the default policy answers %s, want {\"standing\":\"withheld\"}", body)
	}

	code, body = qdsrGet(t, fx, mgr, base+"&customer_id="+qdsrPastDueID)
	pastDueMgr := qdsrJSON(t, code, body)
	if pastDueMgr["standing"] != "past_due" || pastDueMgr["past_due_cents"] != float64(25000) || pastDueMgr["oldest_bucket"] != "60" {
		t.Fatalf("a manager on a past-due account answers %s, want past_due 25000 bucket 60", body)
	}

	code, body = qdsrGet(t, fx, sales, base+"&customer_id="+qdsrPastDueID)
	withheldPastDue := qdsrJSON(t, code, body)
	if withheldPastDue["standing"] != "withheld" || strings.TrimSpace(body) != `{"standing":"withheld"}` {
		t.Errorf("a clerk on a past-due account under the default policy answers %s, want {\"standing\":\"withheld\"}", body)
	}
	for _, forbidden := range []string{"past_due_cents", "oldest_bucket", "past_due"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("clerk past-due standing body %s leaked %q under the default policy", body, forbidden)
		}
	}

	// A held account says so, in the new word and in the old field, for both clerk and manager.
	code, body = qdsrGet(t, fx, sales, base+"&customer_id="+qdsrHeldID)
	held := qdsrJSON(t, code, body)
	if held["standing"] != "on_hold" || held["on_hold"] != true || strings.TrimSpace(body) != `{"standing":"on_hold","on_hold":true}` {
		t.Fatalf("a held account answers %s, want {\"standing\":\"on_hold\",\"on_hold\":true}", body)
	}

	// An account that does not exist is not "clear".
	code, body = qdsrGet(t, fx, sales, base+"&customer_id="+qdsrAbsentID)
	if code != http.StatusNotFound {
		t.Fatalf("an unknown account answers %d %s, want 404", code, body)
	}
	// The account's home branch is a wall too. An account homed to a branch the caller does not reach
	// (held or clear) answers exactly as an id nobody has: the same status and the same body, so the
	// read discloses neither the account's hold nor that it exists. An unhomed account is company
	// wide and visible, as in the customer read.
	code, unknownBody := qdsrGet(t, fx, sales, base+"&customer_id="+qdsrAbsentID)
	for _, id := range []string{qdsrOtherID, qdsrOther2ID} {
		code, body = qdsrGet(t, fx, sales, base+"&customer_id="+id)
		if code != http.StatusNotFound || body != unknownBody {
			t.Fatalf("another branch's account %s answers %d %s, want the unknown id's 404 %s", id, code, body, unknownBody)
		}
	}
	code, body = qdsrGet(t, fx, sales, base+"&customer_id="+qdsrUnhomedID)
	if u := qdsrJSON(t, code, body); u["standing"] != "on_hold" {
		t.Fatalf("an unhomed account is company wide and answers %s", body)
	}
	// A global caller reaches every branch's account.
	code, body = qdsrGet(t, fx, testkit.SubGeneralMgr, "/api/v1/pos/account-standing?branch_id="+qdsrNorthA+"&customer_id="+qdsrOtherID)
	if g := qdsrJSON(t, code, body); g["standing"] != "on_hold" {
		t.Fatalf("a global caller reads any branch's account: %s", body)
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
	// The Quote Desk surface: outside sales holds Quote Desk and no Counter Till cell, and gets the
	// answer (Convert to Order asks it before it confirms). Sales roles with a Counter Till cell too.
	qdsrPlantOutsideSales(t, fx.env, pool)
	code, body = qdsrGet(t, fx, qdsrSubOutsideSales, base+"&customer_id="+qdsrHeldID)
	if o := qdsrJSON(t, code, body); o["standing"] != "on_hold" {
		t.Fatalf("the Quote Desk sales role answers %s, want on_hold", body)
	}
	// A role holding neither the Counter Till nor the Quote Desk meets the gate's refusal.
	for _, sub := range []string{testkit.SubDriver, xrSubYardTeam, xrSubHR} {
		code, body = qdsrGet(t, fx, sub, base+"&customer_id="+qdsrClearID)
		rgRefused(t, sub+" is held off the standing read", code, body)
	}

	// The dealer's account position policy (ar_invoice_policy.account_position_visibility,
	// MANAGER_UP by default) gates the standing figures and the clear/past_due words; when widened
	// to ALL_STAFF, the clerk reads them too. The POS balance_visibility HIDDEN value remains the
	// 403 surface switch that turns the endpoint off for everyone.
	setPositionPolicy := func(v string) {
		if _, err := pool.Exec(context.Background(),
			`UPDATE ar_invoice_policy SET account_position_visibility = $1, updated_at = now() WHERE branch_id IS NULL`, v); err != nil {
			t.Fatalf("set account_position_visibility %s: %v", v, err)
		}
	}
	setBalanceSurface := func(v string) {
		if _, err := pool.Exec(context.Background(),
			`UPDATE pos_policy SET balance_visibility = $1 WHERE branch_id IS NULL`, v); err != nil {
			t.Fatalf("set balance_visibility %s: %v", v, err)
		}
	}
	t.Cleanup(func() {
		setPositionPolicy("MANAGER_UP")
		setBalanceSurface("ALL_STAFF")
	})
	setPositionPolicy("ALL_STAFF")
	code, body = qdsrGet(t, fx, sales, base+"&customer_id="+qdsrPastDueID)
	if p := qdsrJSON(t, code, body); p["standing"] != "past_due" || p["past_due_cents"] != float64(25000) {
		t.Fatalf("a clerk under ALL_STAFF account_position_visibility answers %s, want past_due 25000", body)
	}
	setPositionPolicy("MANAGER_UP")
	setBalanceSurface("HIDDEN")
	code, body = qdsrGet(t, fx, mgr, base+"&customer_id="+qdsrHeldID)
	if code != http.StatusForbidden {
		t.Fatalf("a manager under HIDDEN answers %d %s, want 403", code, body)
	}
}
