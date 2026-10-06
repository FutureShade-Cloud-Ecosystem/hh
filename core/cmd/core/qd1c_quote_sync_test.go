package main

// qd1c_quote_sync_test.go is the quotes sync door's ownership ladder (P2-4,
// round 3): POST /api/v1/sync/quotes driven by request, every guard real.
// The door once fenced by org alone, so a role with no Quote Desk level
// cleared the validity date of a SENT quote and withdrew a DRAFT, where the
// REST edit is a 403. Plan 3.3's ladder holds at both operations: a role
// under Work edits nothing, a role at Work edits its own quotes, a role at
// Manage edits any quote in its branch, and no role reaches another branch
// (plan 3.4 ledger rows 1, 3 and 7).

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// qd1cQuote plants one quote of the north org at a branch, owned by the
// salesperson, and removes it when the test ends.
func qd1cQuote(t *testing.T, q *qd1bFixture, branchID, status, owner string) string {
	t.Helper()
	var id string
	if err := q.pool.QueryRow(context.Background(), `
		INSERT INTO quotes (quote_no, customer_id, branch_id, org_id, status, valid_until,
		                    subtotal_cents, tax_cents, total_cents, salesperson_id, created_by, seq)
		VALUES ('#Q-QD1C-' || nextval('quote_no_seq'), $1::uuid, $2::uuid, $3::uuid, $4, $6::date,
		        2000, 0, 2000, $5::uuid, $5::uuid, 1)
		RETURNING id::text`,
		testkit.TenantAlphaID, branchID, testkit.OrgNorthID, status, owner, qd1cStanding()).Scan(&id); err != nil {
		t.Fatalf("plant quote: %v", err)
	}
	t.Cleanup(func() {
		_, _ = q.pool.Exec(context.Background(), `DELETE FROM quotes WHERE id = $1`, id)
	})
	return id
}

// qd1cQuotePush pushes one operation to the quotes scope as one principal.
func qd1cQuotePush(t *testing.T, q *qd1bFixture, sub, clientID, op string, payload map[string]any) qd1bPushResult {
	t.Helper()
	code, body := qd1bCall(t, q, http.MethodPost, "/api/v1/sync/quotes", sub,
		map[string]any{"ops": []qd1bPushOp{{ClientID: clientID, Op: op, Payload: payload}}})
	if code != http.StatusOK {
		t.Fatalf("push %s %s: %d %s", op, sub, code, body)
	}
	var resp struct {
		Results []qd1bPushResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil || len(resp.Results) != 1 {
		t.Fatalf("decode the push answer: %v (%s)", err, body)
	}
	t.Cleanup(func() {
		_, _ = q.pool.Exec(context.Background(),
			`DELETE FROM audit_log WHERE entity_type = 'sync.quotes' AND extra->>'client_id' = $1`, clientID)
	})
	return resp.Results[0]
}

// qd1cValidity reads a quote's validity date and tombstone back.
func qd1cValidity(t *testing.T, q *qd1bFixture, id string) (validUntil string, deleted bool) {
	t.Helper()
	if err := q.pool.QueryRow(context.Background(),
		`SELECT COALESCE(valid_until::text,''), deleted_at IS NOT NULL FROM quotes WHERE id = $1`, id).
		Scan(&validUntil, &deleted); err != nil {
		t.Fatalf("read the quote back: %v", err)
	}
	return validUntil, deleted
}

// The validity dates are offsets from the clock, never calendar literals:
// the standing date a planted quote carries, and two later ones a push moves
// it to.
func qd1cDate(days int) string { return time.Now().UTC().AddDate(5, 0, days).Format("2006-01-02") }
func qd1cStanding() string     { return qd1cDate(0) }
func qd1cMoved() string        { return qd1cDate(30) }
func qd1cMovedAgain() string   { return qd1cDate(60) }

// ids are one fixed uuid per leg; the engine dedups on them, so each leg
// states its own.
func qd1cOp(n string) string { return "3ef10000-0d1c-4000-8000-0000000000" + n }

func TestQD1C_QuotesSyncDoorRefusesRolesWithNoQuoteDeskLevel(t *testing.T) {
	q := newQD1BFixture(t)
	clerk := qd1bUserID(t, q.pool, testkit.SubInsideSales)
	for i, sub := range []string{xrSubHR, xrSubYardTeam, xrSubFinancialController} {
		sent := qd1cQuote(t, q, testkit.BranchNorthAID, "SENT", clerk)
		res := qd1cQuotePush(t, q, sub, qd1cOp("a"+string(rune('0'+i))), "update",
			map[string]any{"id": sent, "valid_until": qd1cMoved()})
		qd1bRejected(t, sub+" moves a SENT quote's validity", res, "")
		if v, _ := qd1cValidity(t, q, sent); v != qd1cStanding() {
			t.Errorf("%s moved the validity date to %s", sub, v)
		}

		draft := qd1cQuote(t, q, testkit.BranchNorthAID, "DRAFT", clerk)
		res = qd1cQuotePush(t, q, sub, qd1cOp("b"+string(rune('0'+i))), "delete", map[string]any{"id": draft})
		qd1bRejected(t, sub+" withdraws a DRAFT quote", res, "")
		if _, deleted := qd1cValidity(t, q, draft); deleted {
			t.Errorf("%s withdrew the quote", sub)
		}
	}
}

func TestQD1C_QuotesSyncDoorHoldsTheOwnershipLadder(t *testing.T) {
	q := newQD1BFixture(t)
	clerk := qd1bUserID(t, q.pool, testkit.SubInsideSales)
	manager := qd1bUserID(t, q.pool, testkit.SubSalesManager)

	// Inside Sales (Work): its own quote edits and withdraws, another's does not.
	own := qd1cQuote(t, q, testkit.BranchNorthAID, "DRAFT", clerk)
	qd1bApplied(t, "Inside Sales moves its own quote's validity",
		qd1cQuotePush(t, q, testkit.SubInsideSales, qd1cOp("c1"), "update",
			map[string]any{"id": own, "valid_until": qd1cMoved()}))
	if v, _ := qd1cValidity(t, q, own); v != qd1cMoved() {
		t.Errorf("the own quote's validity is %s, want %s", v, qd1cMoved())
	}
	other := qd1cQuote(t, q, testkit.BranchNorthAID, "DRAFT", manager)
	res := qd1cQuotePush(t, q, testkit.SubInsideSales, qd1cOp("c2"), "update",
		map[string]any{"id": other, "valid_until": qd1cMoved()})
	qd1bRejected(t, "Inside Sales moves another seller's quote", res, "")
	res = qd1cQuotePush(t, q, testkit.SubInsideSales, qd1cOp("c3"), "delete", map[string]any{"id": other})
	qd1bRejected(t, "Inside Sales withdraws another seller's quote", res, "")
	if v, deleted := qd1cValidity(t, q, other); v != qd1cStanding() || deleted {
		t.Errorf("another seller's quote was touched: %s deleted %v", v, deleted)
	}
	qd1bApplied(t, "Inside Sales withdraws its own DRAFT",
		qd1cQuotePush(t, q, testkit.SubInsideSales, qd1cOp("c4"), "delete", map[string]any{"id": own}))

	// The Sales manager (Manage) acts on any quote of its branch, never another branch's.
	qd1bApplied(t, "the Sales manager moves a seller's quote in its branch",
		qd1cQuotePush(t, q, testkit.SubSalesManager, qd1cOp("d1"), "update",
			map[string]any{"id": other, "valid_until": qd1cMovedAgain()}))
	elsewhere := qd1cQuote(t, q, testkit.BranchNorthBID, "DRAFT", clerk)
	res = qd1cQuotePush(t, q, testkit.SubSalesManager, qd1cOp("d2"), "update",
		map[string]any{"id": elsewhere, "valid_until": qd1cMovedAgain()})
	qd1bRejected(t, "the Sales manager moves another branch's quote", res, "")
	res = qd1cQuotePush(t, q, testkit.SubSalesManager, qd1cOp("d3"), "delete", map[string]any{"id": elsewhere})
	qd1bRejected(t, "the Sales manager withdraws another branch's quote", res, "")
	qd1bApplied(t, "the Sales manager withdraws a seller's DRAFT in its branch",
		qd1cQuotePush(t, q, testkit.SubSalesManager, qd1cOp("d4"), "delete", map[string]any{"id": other}))

	// The General Manager holds Read on the Quote Desk: it cannot work.
	gmq := qd1cQuote(t, q, testkit.BranchNorthAID, "DRAFT", clerk)
	res = qd1cQuotePush(t, q, testkit.SubGeneralMgr, qd1cOp("e1"), "update",
		map[string]any{"id": gmq, "valid_until": qd1cMovedAgain()})
	qd1bRejected(t, "the General Manager moves a quote's validity", res, "")
}
