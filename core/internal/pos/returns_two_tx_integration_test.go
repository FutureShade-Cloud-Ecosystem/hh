//go:build integration

package pos

// returns_two_tx_integration_test.go, HH-767 (HH-690b) Leg A on real
// Postgres: the manager resolution of a card refund attempt an unknown
// processor outcome left open. End to end through the real composition (the
// real handler behind the real guards, the real staff authority resolver,
// the real audit trail): the processor answers unknown, the attempt stays
// IN_FLIGHT with its initiated_by, the initiating user is refused, a
// different manager at the sale's branch resolves it REFUNDED with the
// processor's ref, the retry of the return under the same key books without
// a second processor call and closes the attempt BOOKED; and the second
// case, where a manager resolves NOT_REFUNDED, the attempt closes FAILED,
// the capture ceiling is released and a fresh return succeeds.
//
// Run (after applying 0415_pos_card_refund_attempts_resolution.sql):
//
//	cd core
//	TEST_DB_URL='postgres://postgres:postgres@127.0.0.1:56790/hh_c2_690?sslmode=disable' \
//	  go test -tags integration ./internal/pos/ -run TestIntegration_TwoTxResolution -p 1 -v

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/jackc/pgx/v5/pgxpool"
)

// resolutionPool is this lane's own gate: the HH-767 legs run against the
// shared integration database named by TEST_DB_URL, and skip when it is not
// set (the sibling lanes' DATABASE_URL rig is a different container).
func resolutionPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DB_URL")
	if url == "" {
		t.Skip("the resolution legs need TEST_DB_URL at the shared integration database")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("integration DB unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("integration DB not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// attemptRow reads one attempt row's resolution facts by refund key.
func attemptRow(t *testing.T, pool *pgxpool.Pool, refundKey string) (id, status, refundRef, initiatedBy, resolvedBy string) {
	t.Helper()
	if err := pool.QueryRow(context.Background(), `
SELECT id::text, status, refund_ref, initiated_by, resolved_by FROM pos_card_refund_attempts WHERE refund_key = $1`,
		refundKey).Scan(&id, &status, &refundRef, &initiatedBy, &resolvedBy); err != nil {
		t.Fatalf("the attempt row %s: %v", refundKey, err)
	}
	return
}

// End to end, case one: the unknown outcome holds the attempt IN_FLIGHT with
// its initiated_by, the list answers it to the sale's branch manager only,
// the initiating user is refused, the manager resolves REFUNDED with the
// processor's ref, and the same-key retry books the return without a second
// processor call and closes the attempt BOOKED, with the resolution's audit
// row on the trail.
func TestIntegration_TwoTxResolution_RefundedThenRetryBooksOnce(t *testing.T) {
	pool := resolutionPool(t)
	rail := &outcomeRail{err: fmt.Errorf("run_payments: refund: %w: status 504", payment.ErrProcessorOutcomeUnknown)}
	s := newTrentonStackOnPoolWithRail(t, pool, rail)
	cust := s.plantCustomer("HH-767 resolved refunded", 30, 5000000)
	sale := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 4, TenderCard)
	returnKey := key()
	refundKey := "refund:" + returnKey

	// The unknown outcome: 502 to the counter, the attempt held IN_FLIGHT.
	rec := s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", s.returnBody(returnKey, sale, 0, RefundCard, 4))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("the unknown outcome answers 502, got %d: %s", rec.Code, rec.Body.String())
	}
	id, status, refundRef, initiatedBy, resolvedBy := attemptRow(t, pool, refundKey)
	if status != "IN_FLIGHT" || initiatedBy != clerkUserID {
		t.Fatalf("the held attempt names its initiator: %s %q %q", status, initiatedBy, refundRef)
	}
	if resolvedBy != "" {
		t.Fatalf("an unresolved row carries no resolver, got %q", resolvedBy)
	}

	// The list: the sale's branch manager sees it; the Kingston manager does not.
	rec = s.do(http.MethodGet, "/api/v1/pos/card-refund-attempts", s.bearer(trentonMgrSub), "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the manager lists open attempts, got %d: %s", rec.Code, rec.Body.String())
	}
	body := s.decodeBody(rec)
	items, _ := body["items"].([]any)
	found := false
	for _, it := range items {
		m, _ := it.(map[string]any)
		if m["id"] == id {
			found = true
			if m["status"] != "IN_FLIGHT" || m["initiated_by"] != clerkUserID || m["order_id"] != sale.OrderID {
				t.Fatalf("the listed row carries its facts: %v", m)
			}
		}
	}
	if !found {
		t.Fatalf("the open attempt is in the manager's list: %v", body)
	}
	rec = s.do(http.MethodGet, "/api/v1/pos/card-refund-attempts", s.bearer(kingstonMgrSub), "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the Kingston manager lists too, got %d: %s", rec.Code, rec.Body.String())
	}
	for _, it := range s.decodeBody(rec)["items"].([]any) {
		if it.(map[string]any)["id"] == id {
			t.Fatal("another branch's attempt must not leak into the Kingston manager's list")
		}
	}

	// The initiating user is refused (and so is any cross-branch manager, as a 404).
	rec = s.do(http.MethodPost, "/api/v1/pos/card-refund-attempts/"+id+"/resolve", s.bearer(trentonClerkSub), "",
		map[string]any{"outcome": "REFUNDED", "refund_ref": "rp_hh767_mgr", "note": "checked the console"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("the initiating user is refused, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = s.do(http.MethodPost, "/api/v1/pos/card-refund-attempts/"+id+"/resolve", s.bearer(kingstonMgrSub), "",
		map[string]any{"outcome": "REFUNDED", "refund_ref": "rp_hh767_mgr", "note": "checked the console"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a cross-branch resolve is a 404, got %d: %s", rec.Code, rec.Body.String())
	}

	// The different manager at the sale's branch resolves REFUNDED.
	rec = s.do(http.MethodPost, "/api/v1/pos/card-refund-attempts/"+id+"/resolve", s.bearer(trentonMgrSub), "",
		map[string]any{"outcome": "REFUNDED", "refund_ref": "rp_hh767_mgr", "note": "confirmed on the processor console"})
	if rec.Code != http.StatusOK {
		t.Fatalf("the manager resolves the attempt, got %d: %s", rec.Code, rec.Body.String())
	}
	out := s.decodeBody(rec)
	if out["status"] != "UNBOOKED" || out["refund_ref"] != "rp_hh767_mgr" || out["resolved_by"] != managerUserID {
		t.Fatalf("the resolved row carries its facts: %v", out)
	}
	_, status, refundRef, initiatedBy, resolvedBy = attemptRow(t, pool, refundKey)
	if status != "UNBOOKED" || refundRef != "rp_hh767_mgr" || initiatedBy != clerkUserID || resolvedBy != managerUserID {
		t.Fatalf("the row stands UNBOOKED with the manager's ref and resolver: %s %q %q %q", status, refundRef, initiatedBy, resolvedBy)
	}
	rows := s.auditRows(audit.ActCardRefundAttemptResolved, id)
	if len(rows) == 0 {
		t.Fatalf("the resolve writes its %s audit row", audit.ActCardRefundAttemptResolved)
	}

	// The retry under the same key books the return, no second processor
	// call, and closes the attempt BOOKED.
	rail.err = nil
	rec = s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", s.returnBody(returnKey, sale, 0, RefundCard, 4))
	if rec.Code != http.StatusCreated {
		t.Fatalf("the retry must book the return, got %d: %s", rec.Code, rec.Body.String())
	}
	if rail.calls != 1 {
		t.Fatalf("the retry reuses the resolved ref, one processor call in total, got %d", rail.calls)
	}
	_, status, refundRef, _, _ = attemptRow(t, pool, refundKey)
	if status != "BOOKED" || refundRef != "rp_hh767_mgr" {
		t.Fatalf("the booked retry closes the attempt at the resolved ref: %s %q", status, refundRef)
	}
	if n := s.countAll(`SELECT count(*) FROM payments pr JOIN pos_returns r ON r.refund_payment_id = pr.id
 WHERE r.order_id = $1::uuid AND pr.gateway_ref = 'rp_hh767_mgr'`, sale.OrderID); n != 1 {
		t.Fatal("the refund payment rides the resolved reversal ref")
	}
}

// End to end, case two: the manager resolves the held attempt NOT_REFUNDED,
// the row closes FAILED, the capture ceiling is released and a fresh return
// on a new key succeeds.
func TestIntegration_TwoTxResolution_NotRefundedReleasesCeiling(t *testing.T) {
	pool := resolutionPool(t)
	rail := &outcomeRail{err: fmt.Errorf("run_payments: refund: %w: status 504", payment.ErrProcessorOutcomeUnknown)}
	s := newTrentonStackOnPoolWithRail(t, pool, rail)
	cust := s.plantCustomer("HH-767 not refunded", 30, 5000000)
	sale := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 4, TenderCard)
	returnKey := key()
	refundKey := "refund:" + returnKey

	rec := s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", s.returnBody(returnKey, sale, 0, RefundCard, 4))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("the unknown outcome answers 502, got %d: %s", rec.Code, rec.Body.String())
	}
	id, status, _, initiatedBy, _ := attemptRow(t, pool, refundKey)
	if status != "IN_FLIGHT" || initiatedBy != clerkUserID {
		t.Fatalf("the held attempt names its initiator: %s %q", status, initiatedBy)
	}

	// While the attempt is open the ceiling is reserved: a fresh-key return refuses.
	freshKey := key()
	rec = s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", s.returnBody(freshKey, sale, 0, RefundCard, 2))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("the held amount stays reserved against the capture, got %d: %s", rec.Code, rec.Body.String())
	}

	// The manager resolves NOT_REFUNDED: the row closes FAILED.
	rec = s.do(http.MethodPost, "/api/v1/pos/card-refund-attempts/"+id+"/resolve", s.bearer(trentonMgrSub), "",
		map[string]any{"outcome": "NOT_REFUNDED", "note": "the processor never received it"})
	if rec.Code != http.StatusOK {
		t.Fatalf("the manager resolves the attempt NOT_REFUNDED, got %d: %s", rec.Code, rec.Body.String())
	}
	_, status, _, _, resolvedBy := attemptRow(t, pool, refundKey)
	if status != "FAILED" || resolvedBy != managerUserID {
		t.Fatalf("the row closes FAILED with its resolver: %s %q", status, resolvedBy)
	}

	// The ceiling is released: the fresh return succeeds and books.
	rail.err = nil
	rec = s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", s.returnBody(freshKey, sale, 0, RefundCard, 2))
	if rec.Code != http.StatusCreated {
		t.Fatalf("the fresh return proceeds once the ceiling is released, got %d: %s", rec.Code, rec.Body.String())
	}
	if rail.calls != 2 {
		t.Fatalf("the held key's one unknown-outcome call and the fresh key's one call, got %d", rail.calls)
	}
}
