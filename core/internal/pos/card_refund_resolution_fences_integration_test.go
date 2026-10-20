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
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	settledRef := "rp_fence_" + returnKey

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
			body["refund_ref"] = settledRef
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
		map[string]any{"outcome": "REFUNDED", "refund_ref": settledRef, "note": "confirmed on the processor console"})
	if rec.Code != http.StatusOK {
		t.Fatalf("the recorded unknown outcome settles the row, got %d: %s", rec.Code, rec.Body.String())
	}
	_, status, refundRef, _, _ = attemptRow(t, pool, "refund:"+returnKey)
	if status != "UNBOOKED" || refundRef != settledRef {
		t.Fatalf("the settled row stands UNBOOKED with the ref: %s %q", status, refundRef)
	}
}

// P1-2 end to end: two attempts and one processor ref. The first settles
// REFUNDED with the shared ref; the second refuses the SAME ref 409 in words
// naming it, and a ref already booked as a payment's gateway_ref refuses the
// same way (the sale's own payment row carries it).
func TestIntegration_TwoTxResolution_DuplicateRefundRefRefuses409(t *testing.T) {
	pool := intPool(t)
	rail := &outcomeRail{err: fmt.Errorf("run_payments: refund: %w: status 504", payment.ErrProcessorOutcomeUnknown)}
	s := newTrentonStackOnPoolWithRail(t, pool, rail)
	cust := s.plantCustomer("HH-501 duplicate ref", 30, 5000000)
	sale := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 4, TenderCard)

	// Attempt A: the real flow, held IN_FLIGHT by the unknown outcome.
	returnKey := key()
	sharedRef := "rp_zz_same_" + key()
	payRef := "rp_zz_pay_" + key()
	ownRef := "rp_zz_own_" + key()
	rec := s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", s.returnBody(returnKey, sale, 0, RefundCard, 4))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("the unknown outcome answers 502, got %d: %s", rec.Code, rec.Body.String())
	}
	idA, _, _, _, _ := attemptRow(t, pool, "refund:"+returnKey)

	// Attempt B: a second row on the same sale, the crash case (attempted
	// long before the live-request window, no recorded outcome), planted in
	// its own transaction as the brief shapes it.
	uniq := key()
	keyB := "refund:zz-b-" + uniq
	if _, err := pool.Exec(context.Background(), `
INSERT INTO pos_card_refund_attempts (refund_key, order_id, branch_id, customer_id, gateway_ref, amount_cents, status, attempted_at, updated_at, initiated_by)
VALUES ($1, $2::uuid, $3::uuid, $4::uuid, $5, 4520, 'IN_FLIGHT', now() - interval '10 minutes', now() - interval '10 minutes', $6)`,
		keyB, sale.OrderID, trentonBranchID, cust, saleGatewayRef(t, pool, sale.OrderID), clerkUserID); err != nil {
		t.Fatalf("the second attempt row: %v", err)
	}
	var idB string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM pos_card_refund_attempts WHERE refund_key = $1`, keyB).Scan(&idB); err != nil {
		t.Fatalf("the second attempt id: %v", err)
	}

	// A settles REFUNDED with the shared ref.
	rec = s.do(http.MethodPost, "/api/v1/pos/card-refund-attempts/"+idA+"/resolve", s.bearer(trentonMgrSub), "",
		map[string]any{"outcome": "REFUNDED", "refund_ref": sharedRef, "note": "first settle"})
	if rec.Code != http.StatusOK {
		t.Fatalf("the first attempt settles with the ref, got %d: %s", rec.Code, rec.Body.String())
	}
	// B with the SAME ref refuses 409 in words naming it.
	rec = s.do(http.MethodPost, "/api/v1/pos/card-refund-attempts/"+idB+"/resolve", s.bearer(trentonMgrSub), "",
		map[string]any{"outcome": "REFUNDED", "refund_ref": sharedRef, "note": "second settle"})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), sharedRef) {
		t.Fatalf("the shared ref refuses the second settle 409 naming the ref, got %d: %s", rec.Code, rec.Body.String())
	}
	// A ref already booked as a payment's gateway_ref refuses the same way:
	// the sale's own payment carries it now.
	if _, err := pool.Exec(context.Background(),
		`UPDATE payments SET gateway_ref = $2 WHERE id = (SELECT payment_id FROM pos_sale WHERE order_id = $1::uuid)`,
		sale.OrderID, payRef); err != nil {
		t.Fatalf("the payment's gateway_ref plant: %v", err)
	}
	rec = s.do(http.MethodPost, "/api/v1/pos/card-refund-attempts/"+idB+"/resolve", s.bearer(trentonMgrSub), "",
		map[string]any{"outcome": "REFUNDED", "refund_ref": payRef, "note": "over a booked payment"})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), payRef) {
		t.Fatalf("a ref booked as a payment refuses the settle 409, got %d: %s", rec.Code, rec.Body.String())
	}
	// B still settles with its own ref.
	rec = s.do(http.MethodPost, "/api/v1/pos/card-refund-attempts/"+idB+"/resolve", s.bearer(trentonMgrSub), "",
		map[string]any{"outcome": "REFUNDED", "refund_ref": ownRef, "note": "its own ref"})
	if rec.Code != http.StatusOK {
		t.Fatalf("the second attempt settles with its own ref, got %d: %s", rec.Code, rec.Body.String())
	}
}

// lateChildRail answers its first call at once (after running onFirst, which
// stands for that call having taken long) and holds its second call open until
// released, then declines it: the shape of a return whose second card child
// starts its call late.
type lateChildRail struct {
	mu      sync.Mutex
	calls   int
	onFirst func()
	entered chan struct{}
	release chan struct{}
}

func (r *lateChildRail) RefundCard(_ context.Context, _ pgx.Tx, _ string, _ int64, _, _ string) (string, error) {
	r.mu.Lock()
	r.calls++
	n := r.calls
	r.mu.Unlock()
	if n == 1 {
		if r.onFirst != nil {
			r.onFirst()
		}
		return "rp_late_first_" + key(), nil
	}
	close(r.entered)
	<-r.release
	return "", &payment.DeclineError{RespCode: "05", RespText: "Do not honour"}
}

// PR #501 round 2 P2-1 end to end: a return with two card children. The first
// call takes long (the second row's staging stamp is backdated while it runs),
// then the second call is held open. The fence measures from the start of each
// child's own call, so the second row refuses both outcomes inside its own
// window, and the decline that follows closes it FAILED: the books never show a
// card refund the processor declined.
func TestIntegration_TwoTxResolution_LateChildCallIsFencedFromItsOwnStart(t *testing.T) {
	pool := intPool(t)
	cust := ""
	rail := &lateChildRail{entered: make(chan struct{}), release: make(chan struct{})}
	s := newTrentonStackOnPoolWithRail(t, pool, rail)
	cust = s.plantCustomer("HH-501 late child", 30, 5000000)
	rail.onFirst = func() {
		if _, err := pool.Exec(context.Background(), `
UPDATE pos_card_refund_attempts SET attempted_at = now() - interval '2 minutes'
 WHERE customer_id = $1::uuid AND status = 'IN_FLIGHT'`, cust); err != nil {
			t.Errorf("backdate the later child's staging stamp: %v", err)
		}
	}
	sale1 := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 3, TenderCard)
	sale2 := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 2, TenderCard)
	body := s.noteBody(key(), cust, RefundCard, []trentonSaleView{sale1, sale2}, []float64{3, 2})

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- s.postNote(trentonClerkSub, "", body) }()
	<-rail.entered

	var id, refundKey string
	if err := pool.QueryRow(context.Background(), `
SELECT id::text, refund_key FROM pos_card_refund_attempts
 WHERE customer_id = $1::uuid AND status = 'IN_FLIGHT'`, cust).Scan(&id, &refundKey); err != nil {
		t.Fatalf("the second child's row is the one still IN_FLIGHT: %v", err)
	}
	for _, outcome := range []string{"REFUNDED", "NOT_REFUNDED"} {
		reqBody := map[string]any{"outcome": outcome, "note": "checked while it ran"}
		if outcome == "REFUNDED" {
			reqBody["refund_ref"] = "rp_typed_late_" + key()
		}
		rec := s.do(http.MethodPost, "/api/v1/pos/card-refund-attempts/"+id+"/resolve", s.bearer(trentonMgrSub), "", reqBody)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "may still be running at the processor") {
			t.Fatalf("%s inside the late child's own window refuses 409, got %d: %s", outcome, rec.Code, rec.Body.String())
		}
	}

	close(rail.release)
	if rec := <-done; rec.Code != http.StatusBadGateway {
		t.Fatalf("the declined second child answers 502, got %d: %s", rec.Code, rec.Body.String())
	}
	_, status, refundRef, _, _ := attemptRow(t, pool, refundKey)
	if status != "FAILED" || refundRef != "" {
		t.Fatalf("the declined child closes FAILED with no ref: %s %q", status, refundRef)
	}
}

// Defence in depth: the dispatch-side closes meet a row a manager already
// resolved and say so (a typed signal, plus a loud log) instead of matching
// nothing silently; a row nobody resolved, or no row, stays quiet.
func TestIntegration_TwoTxResolution_DispatchWritesFlagAManagerResolvedRow(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	var logged bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	k := "refund:zz-resolved-" + key()
	plant := func(k string, resolvedBy string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
INSERT INTO pos_card_refund_attempts (refund_key, order_id, branch_id, customer_id, gateway_ref, amount_cents, status, initiated_by, resolved_by, resolved_at)
VALUES ($1, (SELECT id FROM orders LIMIT 1), $2::uuid, (SELECT id FROM customers LIMIT 1), 'rp_x', 100, 'FAILED', $3, $4, CASE WHEN $4 = '' THEN NULL ELSE now() END)`,
			k, trentonBranchID, clerkUserID, resolvedBy); err != nil {
			t.Fatalf("plant: %v", err)
		}
	}
	plant(k, managerUserID)
	if err := repo.FailCardRefundAttempt(ctx, k, "declined"); !errors.Is(err, errCardRefundAlreadyResolved) {
		t.Fatalf("Fail on a manager resolved row signals it, got %v", err)
	}
	if err := repo.HoldCardRefundAttemptInFlight(ctx, k, "unknown"); !errors.Is(err, errCardRefundAlreadyResolved) {
		t.Fatalf("Hold on a manager resolved row signals it, got %v", err)
	}
	if out := logged.String(); !strings.Contains(out, "level=ERROR") || !strings.Contains(out, k) || !strings.Contains(out, managerUserID) {
		t.Fatalf("the contradiction is logged loudly with the key and the resolver, got %q", out)
	}
	logged.Reset()
	quiet := "refund:zz-unresolved-" + key()
	plant(quiet, "")
	if err := repo.FailCardRefundAttempt(ctx, quiet, "declined"); err != nil {
		t.Fatalf("a row nobody resolved stays quiet, got %v", err)
	}
	if err := repo.HoldCardRefundAttemptInFlight(ctx, "refund:zz-none-"+key(), "unknown"); err != nil {
		t.Fatalf("no row stays quiet, got %v", err)
	}
	if logged.Len() != 0 {
		t.Fatalf("a row nobody resolved logs nothing loud, got %q", logged.String())
	}
}
