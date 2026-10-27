//go:build integration

package pos

// card_refund_booked_window_integration_test.go pins UX-102: a card refund's attempt row closes
// BOOKED in the same transaction that writes its pos_returns row, so the capture cap never counts
// one refund in both sums. The post-commit pool mark is made a no-op here, which is exactly the
// formerly racy window: on a core that marked only after commit the attempt would still read
// UNBOOKED beside its committed return and the guard would see the refund twice.
//
// Run (own throwaway container, DATABASE_URL at it):
//
//	CI=true DATABASE_URL=... go test -tags integration ./internal/pos/ -run TestIntegration_Trenton_CardRefund_BookedWindow -p 1 -v

import (
	"context"
	"net/http"
	"testing"
)

// noPostCommitMark is the store with the pool's after-commit mark removed: the window made permanent.
type noPostCommitMark struct{ posStore }

func (noPostCommitMark) MarkCardRefundsBooked(context.Context, []string) error { return nil }

func TestIntegration_Trenton_CardRefund_BookedWindow(t *testing.T) {
	s := newTrentonStack(t)
	s.svc.store = noPostCommitMark{s.svc.store}
	sale := s.ringSale(key(), trentonBranchID, s.trentonTill, "trenton", []float64{30}, TenderCard)

	rec := s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", s.returnBody(key(), sale, 0, RefundCard, 5))
	if rec.Code != http.StatusCreated {
		t.Fatalf("the card return must commit, got %d: %s", rec.Code, rec.Body.String())
	}

	ctx := context.Background()
	var open int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM pos_card_refund_attempts WHERE order_id = $1::uuid AND status IN ('IN_FLIGHT', 'UNBOOKED')`,
		sale.OrderID).Scan(&open); err != nil {
		t.Fatalf("attempts read: %v", err)
	}
	if open != 0 {
		t.Fatalf("a committed card return must leave no open attempt, got %d", open)
	}

	// The guard's two sums on the committed state: the booked return is counted once.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	refunded, err := s.svc.store.SumCardRefundedTx(ctx, tx, sale.OrderID, "")
	if err != nil {
		t.Fatalf("booked sum: %v", err)
	}
	unbooked, err := s.svc.store.SumUnbookedCardRefundsTx(ctx, tx, sale.OrderID, "")
	if err != nil {
		t.Fatalf("unbooked sum: %v", err)
	}
	if refunded != 5650 || unbooked != 0 {
		t.Fatalf("the refund must count once: booked %d (want 5650), unbooked %d (want 0)", refunded, unbooked)
	}
}

// UX-102b (the 623 review's P3 1): the after-commit unbooked path can run after a refund already
// booked, when a commit reports an error after it took effect. The record it files must not turn a
// BOOKED row back to UNBOOKED: the cap would then count the refund twice (it fails safe), and the
// manager's list would show a booked refund as open.
func TestIntegration_Trenton_CardRefund_BookedSurvivesTheUnbookedPath(t *testing.T) {
	s := newTrentonStack(t)
	sale := s.ringSale(key(), trentonBranchID, s.trentonTill, "trenton", []float64{30}, TenderCard)
	rk := key()
	rec := s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", s.returnBody(rk, sale, 0, RefundCard, 5))
	if rec.Code != http.StatusCreated {
		t.Fatalf("the card return must commit, got %d: %s", rec.Code, rec.Body.String())
	}
	ctx := context.Background()
	// The attempt row the booking transaction just closed, as the failed commit's own sweeper would
	// have seen it: the same key, a fresh reversal ref, the row's own order, branch, customer and
	// initiator.
	var orderID, branchID, customerID, initiatedBy string
	var amount int64
	if err := s.pool.QueryRow(ctx,
		`SELECT order_id::text, branch_id::text, customer_id::text, amount_cents, initiated_by::text
		   FROM pos_card_refund_attempts WHERE refund_key = $1`, "refund:"+rk,
	).Scan(&orderID, &branchID, &customerID, &amount, &initiatedBy); err != nil {
		t.Fatalf("the booked attempt read: %v", err)
	}
	facts := []cardRefundFact{{
		RefundKey: "refund:" + rk, OrderID: orderID, BranchID: branchID, CustomerID: customerID,
		GatewayRef: "gw-the-first-try", RefundRef: "rf-the-retry", AmountCents: amount,
		InitiatedBy: initiatedBy,
	}}
	if err := s.svc.store.RecordUnbookedCardRefunds(ctx, facts, "the commit reported an error after it took effect"); err != nil {
		t.Fatalf("the unbooked record: %v", err)
	}
	var status string
	if err := s.pool.QueryRow(ctx,
		`SELECT status FROM pos_card_refund_attempts WHERE order_id = $1::uuid AND refund_key = $2`,
		sale.OrderID, "refund:"+rk).Scan(&status); err != nil {
		t.Fatalf("the attempt read: %v", err)
	}
	if status != "BOOKED" {
		t.Fatalf("a booked refund stays booked: the unbooked path moved it to %s", status)
	}
}

// UX-102b (the 623 review's P3 2): the batch half of the window, pinned as the leg above pins the
// single half. A card note across two card sales, the after-commit pool mark removed: every child's
// attempt must read BOOKED, because each child's booking transaction flips its own key.
func TestIntegration_Trenton_CardRefund_BookedWindowBatch(t *testing.T) {
	s := newTrentonStack(t)
	s.svc.store = noPostCommitMark{s.svc.store}
	customer := plantCreditCustomer(t, s, "Booked window batch")
	sale1 := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", customer, "", 5, TenderCard)
	sale2 := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", customer, "", 5, TenderCard)
	rec := s.postNote(trentonClerkSub, "", s.noteBody(key(), customer, RefundCard, []trentonSaleView{sale1, sale2}, []float64{5, 5}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("the two-order card note must commit, got %d: %s", rec.Code, rec.Body.String())
	}
	ctx := context.Background()
	var open, booked int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE status IN ('IN_FLIGHT','UNBOOKED')), count(*) FILTER (WHERE status = 'BOOKED')
		   FROM pos_card_refund_attempts WHERE order_id IN ($1::uuid, $2::uuid)`,
		sale1.OrderID, sale2.OrderID).Scan(&open, &booked); err != nil {
		t.Fatalf("the attempts read: %v", err)
	}
	if open != 0 || booked != 2 {
		t.Fatalf("every child of the note must close booked with no open attempt: open %d, booked %d", open, booked)
	}
}
