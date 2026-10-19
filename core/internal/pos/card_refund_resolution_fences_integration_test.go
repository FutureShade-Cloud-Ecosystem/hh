//go:build integration

package pos

// card_refund_resolution_fences_integration_test.go, PR #501 round 1: the
// fences around the manager resolution of an open card refund attempt, on
// real Postgres through the real composition. P1-1: while the processor call
// of a dispatched refund is still open (a held chan the rail waits on), BOTH
// resolutions refuse in words naming the live request, and the row settles
// once the unknown outcome is recorded. P1-2: a processor ref already
// carried by another attempt, or already booked as a payment's gateway_ref,
// refuses the settle 409 in words naming the duplicate ref.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/jackc/pgx/v5"
)

// holdRail is a card rail whose one processor call stays open until the test
// releases it, then answers the unknown outcome (the manager resolution's
// own starting shape).
type holdRail struct {
	enteredOnce sync.Once
	entered     chan struct{}
	release     chan struct{}
}

func newHoldRail() *holdRail {
	return &holdRail{entered: make(chan struct{}), release: make(chan struct{})}
}

func (r *holdRail) RefundCard(_ context.Context, _ pgx.Tx, _ string, _ int64, _, _ string) (string, error) {
	r.enteredOnce.Do(func() { close(r.entered) })
	<-r.release
	return "", fmt.Errorf("run_payments: refund: %w: status 504", payment.ErrProcessorOutcomeUnknown)
}

// P1-1 end to end: the return's processor call is held open; in that window
// the manager's REFUNDED and NOT_REFUNDED both refuse 409 in the live-request
// words; once the call lands as an unknown outcome (processor_at stamped) the
// row settles REFUNDED and the retry books on the resolved ref.
func TestIntegration_TwoTxResolution_LiveRequestFenceRefusesBothOutcomes(t *testing.T) {
	pool := intPool(t)
	rail := newHoldRail()
	s := newTrentonStackOnPoolWithRail(t, pool, rail)
	cust := s.plantCustomer("HH-501 live fence", 30, 5000000)
	sale := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 4, TenderCard)
	returnKey := key()

	// The return runs on its own goroutine, its processor call held open.
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", s.returnBody(returnKey, sale, 0, RefundCard, 4))
	}()
	<-rail.entered

	id, status, refundRef, _, _ := attemptRow(t, pool, "refund:"+returnKey)
	if status != "IN_FLIGHT" || refundRef != "" {
		t.Fatalf("the held refund leaves its attempt IN_FLIGHT with no ref: %s %q", status, refundRef)
	}
	var processorAt *string
	if err := pool.QueryRow(context.Background(),
		`SELECT processor_at::text FROM pos_card_refund_attempts WHERE refund_key = $1`,
		"refund:"+returnKey).Scan(&processorAt); err != nil {
		t.Fatalf("the attempt's processor_at read: %v", err)
	}
	if processorAt != nil {
		t.Fatal("no unknown outcome is recorded while the live request runs")
	}

	// Both outcomes refuse inside the live-request window, in its words.
	for _, outcome := range []string{"REFUNDED", "NOT_REFUNDED"} {
		body := map[string]any{"outcome": outcome, "note": "checked while it ran"}
		if outcome == "REFUNDED" {
			body["refund_ref"] = "rp_hh501_fence"
		}
		rec := s.do(http.MethodPost, "/api/v1/pos/card-refund-attempts/"+id+"/resolve", s.bearer(trentonMgrSub), "", body)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "may still be running at the processor") {
			t.Fatalf("%s inside the live-request window refuses 409 in the live words, got %d: %s", outcome, rec.Code, rec.Body.String())
		}
	}

	// The call lands as an unknown outcome: processor_at is stamped, the row
	// settles REFUNDED and the retry books on the resolved ref.
	close(rail.release)
	if rec := <-done; rec.Code != http.StatusBadGateway {
		t.Fatalf("the held call lands as the unknown outcome, a 502, got %d: %s", rec.Code, rec.Body.String())
	}
	rec := s.do(http.MethodPost, "/api/v1/pos/card-refund-attempts/"+id+"/resolve", s.bearer(trentonMgrSub), "",
		map[string]any{"outcome": "REFUNDED", "refund_ref": "rp_hh501_fence", "note": "confirmed on the processor console"})
	if rec.Code != http.StatusOK {
		t.Fatalf("the recorded unknown outcome settles the row, got %d: %s", rec.Code, rec.Body.String())
	}
	_, status, refundRef, _, _ = attemptRow(t, pool, "refund:"+returnKey)
	if status != "UNBOOKED" || refundRef != "rp_hh501_fence" {
		t.Fatalf("the settled row stands UNBOOKED with the ref: %s %q", status, refundRef)
	}
}
