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
