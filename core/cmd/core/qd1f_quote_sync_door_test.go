package main

// qd1f_quote_sync_door_test.go is the rest of the quotes sync door (P2-C,
// round 4): POST and GET /api/v1/sync/quotes by request, every guard real.
// Update and delete held the REST ladder after round 3, but create and pull
// were fenced by org alone: HR, Yard Team, the Financial Controller and the
// General Manager at Read created DRAFT quotes through sync (the REST create
// needs Work in the quote's branch), and HR, Yard Team and the Financial
// Controller pulled Inside Sales' SENT quote with its totals, where the REST
// read is a 403 (plan 3.4 ledger row 1, exit line 4). The door now answers
// what REST answers: create needs Work and the branch in reach, pull returns
// the quotes the caller could list through REST (own at Work, the branch at
// Manage or Read, nothing without a Quote Desk level), and a stale update's
// conflict envelope carries no projection of a quote the caller cannot read.

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// qd1fQuoteNo reads one planted quote's number back (its figures are 2000).
func qd1fQuoteNo(t *testing.T, q *qd1bFixture, id string) string {
	t.Helper()
	var no string
	if err := q.pool.QueryRow(context.Background(), `SELECT quote_no FROM quotes WHERE id = $1`, id).Scan(&no); err != nil {
		t.Fatalf("read the quote number: %v", err)
	}
	return no
}

// qd1fPull is one principal's pull body of the quotes feed.
func qd1fPull(t *testing.T, q *qd1bFixture, sub string) string {
	t.Helper()
	code, body := qd1bCall(t, q, http.MethodGet, "/api/v1/sync/quotes?since=0&limit=500", sub, nil)
	if code != http.StatusOK {
		t.Fatalf("pull quotes as %s: %d %s", sub, code, body)
	}
	return body
}

func TestQD1F_QuotesSyncCreateNeedsWorkAndTheBranchInReach(t *testing.T) {
	q := newQD1BFixture(t)
	create := func(sub, branch string) (qd1bPushResult, string) {
		id := uuid.NewString()
		res := qd1cQuotePush(t, q, sub, uuid.NewString(), "create", map[string]any{
			"id": id, "customer_id": testkit.TenantAlphaID, "branch_id": branch})
		t.Cleanup(func() { _, _ = q.pool.Exec(context.Background(), `DELETE FROM quotes WHERE id = $1`, id) })
		return res, id
	}
	exists := func(id string) bool {
		var n int
		_ = q.pool.QueryRow(context.Background(), `SELECT count(*) FROM quotes WHERE id = $1`, id).Scan(&n)
		return n == 1
	}
	// The REST create sits at Work: roles with no Quote Desk level, and the
	// General Manager at Read, create nothing from a device either.
	for _, sub := range []string{xrSubHR, xrSubYardTeam, xrSubFinancialController, testkit.SubGeneralMgr} {
		res, id := create(sub, testkit.BranchNorthAID)
		qd1bRejected(t, sub+" creates a quote through sync", res, "")
		if exists(id) {
			t.Errorf("%s created a quote through the sync door", sub)
		}
	}
	// A Work role creates in its own branch and not in another.
	res, id := create(testkit.SubInsideSales, testkit.BranchNorthAID)
	qd1bApplied(t, "Inside Sales creates in its own branch", res)
	if !exists(id) {
		t.Errorf("the applied create left no row")
	}
	var owner, creator string
	if err := q.pool.QueryRow(context.Background(),
		`SELECT COALESCE(salesperson_id::text,''), COALESCE(created_by::text,'') FROM quotes WHERE id = $1`, id).Scan(&owner, &creator); err != nil {
		t.Fatalf("read the created quote: %v", err)
	}
	if clerk := qd1bUserID(t, q.pool, testkit.SubInsideSales); owner != clerk || creator != clerk {
		t.Errorf("the created quote is owned by %q and created by %q, want the caller %q for both", owner, creator, clerk)
	}
	res, id = create(testkit.SubInsideSales, testkit.BranchNorthBID)
	qd1bRejected(t, "Inside Sales creates in another branch", res, "")
	if exists(id) {
		t.Errorf("Inside Sales created a quote in a branch it does not reach")
	}
	res, id = create(testkit.SubSalesManager, testkit.BranchNorthAID)
	qd1bApplied(t, "the Sales manager creates in its own branch", res)
	res, id = create(testkit.SubSalesManager, testkit.BranchNorthBID)
	qd1bRejected(t, "the Sales manager creates in another branch", res, "")
	if exists(id) {
		t.Errorf("the Sales manager created a quote in a branch it does not reach")
	}
}

func TestQD1F_QuotesSyncPullReturnsWhatTheCallerCouldListThroughRest(t *testing.T) {
	q := newQD1BFixture(t)
	clerk := qd1bUserID(t, q.pool, testkit.SubInsideSales)
	manager := qd1bUserID(t, q.pool, testkit.SubSalesManager)
	clerksSent := qd1cQuote(t, q, testkit.BranchNorthAID, "SENT", clerk)
	managersDraft := qd1cQuote(t, q, testkit.BranchNorthAID, "DRAFT", manager)
	elsewhere := qd1cQuote(t, q, testkit.BranchNorthBID, "SENT", clerk)

	// No Quote Desk level: the REST read is a 403, the pull carries nothing.
	for _, sub := range []string{xrSubHR, xrSubYardTeam, xrSubFinancialController, xrSubProcurementManager} {
		body := qd1fPull(t, q, sub)
		for _, id := range []string{clerksSent, managersDraft, elsewhere} {
			if strings.Contains(body, id) || strings.Contains(body, qd1fQuoteNo(t, q, id)) {
				t.Errorf("%s pulled a quote through the sync door that REST refuses it: %s", sub, body)
			}
		}
	}
	want := func(sub string, visible, hidden []string) {
		body := qd1fPull(t, q, sub)
		for _, id := range visible {
			if !strings.Contains(body, id) {
				t.Errorf("%s must pull quote %s", sub, id)
			}
		}
		for _, id := range hidden {
			if strings.Contains(body, id) {
				t.Errorf("%s must not pull quote %s", sub, id)
			}
		}
	}
	// Work lists its own; Manage lists its branch and only its branch; Read
	// lists what it reaches (the General Manager reaches every branch).
	want(testkit.SubInsideSales, []string{clerksSent}, []string{managersDraft, elsewhere})
	want(testkit.SubSalesManager, []string{clerksSent, managersDraft}, []string{elsewhere})
	want(testkit.SubGeneralMgr, []string{clerksSent, managersDraft, elsewhere}, nil)
}

func TestQD1F_QuotesSyncStaleUpdateConflictCarriesNoQuoteTheCallerCannotRead(t *testing.T) {
	q := newQD1BFixture(t)
	clerk := qd1bUserID(t, q.pool, testkit.SubInsideSales)
	manager := qd1bUserID(t, q.pool, testkit.SubSalesManager)
	sent := qd1cQuote(t, q, testkit.BranchNorthAID, "SENT", clerk)
	managersDraft := qd1cQuote(t, q, testkit.BranchNorthAID, "DRAFT", manager)
	no, mgrNo := qd1fQuoteNo(t, q, sent), qd1fQuoteNo(t, q, managersDraft)
	stale := 0
	push := func(sub, id string, payload map[string]any) string {
		payload["id"] = id
		code, body := qd1bCall(t, q, http.MethodPost, "/api/v1/sync/quotes", sub, map[string]any{
			"ops": []map[string]any{{"client_id": uuid.NewString(), "op": "update", "payload": payload, "base_seq": stale}}})
		if code != http.StatusOK {
			t.Fatalf("push as %s: %d %s", sub, code, body)
		}
		return body
	}
	// A stale update names a base older than the row: the engine answers a
	// conflict envelope. Every payload shape, for every caller that cannot
	// read the quote, must leave the envelope without it.
	payloads := []map[string]any{{"valid_until": qd1cMoved()}, {}, {"valid_until": nil}}
	for _, sub := range []string{xrSubHR, xrSubYardTeam, xrSubFinancialController} {
		for _, p := range payloads {
			body := push(sub, sent, p)
			if strings.Contains(body, no) || strings.Contains(body, "total_cents") {
				t.Errorf("%s: a stale update's answer carries the quote it cannot read: %s", sub, body)
			}
		}
	}
	// Inside Sales (Work) lists its own quotes only: another seller's draft
	// stays out of its envelope.
	for _, p := range payloads {
		body := push(testkit.SubInsideSales, managersDraft, p)
		if strings.Contains(body, mgrNo) || strings.Contains(body, "total_cents") {
			t.Errorf("Inside Sales: a stale update's answer carries another seller's quote: %s", body)
		}
	}
}
