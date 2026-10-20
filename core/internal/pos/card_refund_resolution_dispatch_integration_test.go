//go:build integration

package pos

// card_refund_resolution_dispatch_integration_test.go, PR #501 round 3 on real
// Postgres: a card child still WAITING its turn inside a multi child dispatch.
// Fix 1: every remaining child's stamp is refreshed before each call, so a
// waiting child is never old enough for a manager settle to land. Fix 2 (proven
// with the fence bypassed): a row a manager settled before its call began is
// never sent, says so on the row itself, and the same key retry refuses to book
// the typed ref instead of booking a refund no call ever made.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
)

// hookRail runs onCall (call number from 1, the refund key) then answers a
// fresh processor ref; it counts the calls.
type hookRail struct {
	mu     sync.Mutex
	calls  int
	onCall func(n int, refundKey string)
}

func (r *hookRail) RefundCard(_ context.Context, _ pgx.Tx, _ string, _ int64, _, refundKey string) (string, error) {
	r.mu.Lock()
	r.calls++
	n := r.calls
	r.mu.Unlock()
	if r.onCall != nil {
		r.onCall(n, refundKey)
	}
	return fmt.Sprintf("rp_hook_%d_%s", n, key()), nil
}

func (r *hookRail) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// backdatingStore ages every IN_FLIGHT row of one customer by two minutes just
// before the dispatch's stamp runs, the state of a dispatch that reaches a child
// long after staging (an earlier call, or a wait on the pool).
type backdatingStore struct {
	posStore
	age func()
}

func (b *backdatingStore) StampCardRefundCallStart(ctx context.Context, keys []string) (bool, error) {
	b.age()
	return b.posStore.StampCardRefundCallStart(ctx, keys)
}

// otherChild finds the attempt row of the customer that is not the given key.
func otherChild(t *testing.T, s *trentonStack, cust, exceptKey string) (id, refundKey string) {
	t.Helper()
	if err := s.pool.QueryRow(context.Background(), `
SELECT id::text, refund_key FROM pos_card_refund_attempts WHERE customer_id = $1::uuid AND refund_key <> $2`,
		cust, exceptKey).Scan(&id, &refundKey); err != nil {
		t.Fatalf("the other child's row: %v", err)
	}
	return id, refundKey
}

func twoChildNote(t *testing.T, s *trentonStack, cust string) map[string]any {
	t.Helper()
	sale1 := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 3, TenderCard)
	sale2 := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 2, TenderCard)
	return s.noteBody(key(), cust, RefundCard, []trentonSaleView{sale1, sale2}, []float64{3, 2})
}

// Fix 1: the second child waits while the first call runs, its staging stamp
// aged beyond the window by the time the dispatch reaches the first child. The
// stamp before the first call refreshes it, so the manager's settle during the
// first call is refused 409 for both outcomes.
func TestIntegration_TwoTxResolution_WaitingChildIsRefreshedAndFenced(t *testing.T) {
	pool := intPool(t)
	rail := &hookRail{}
	s := newTrentonStackOnPoolWithRail(t, pool, rail)
	cust := s.plantCustomer("HH-501 waiting child", 30, 5000000)
	s.svc.store = &backdatingStore{posStore: s.svc.store, age: func() {
		if _, err := pool.Exec(context.Background(), `
UPDATE pos_card_refund_attempts SET attempted_at = now() - interval '2 minutes'
 WHERE customer_id = $1::uuid AND status = 'IN_FLIGHT'`, cust); err != nil {
			t.Errorf("age the staging stamps: %v", err)
		}
	}}
	rail.onCall = func(n int, refundKey string) {
		if n != 1 {
			return
		}
		id, _ := otherChild(t, s, cust, refundKey)
		for _, outcome := range []string{"REFUNDED", "NOT_REFUNDED"} {
			body := map[string]any{"outcome": outcome, "note": "while the first child runs"}
			if outcome == "REFUNDED" {
				body["refund_ref"] = "rp_typed_wait_" + key()
			}
			rec := s.do(http.MethodPost, "/api/v1/pos/card-refund-attempts/"+id+"/resolve", s.bearer(trentonMgrSub), "", body)
			if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "may still be running at the processor") {
				t.Errorf("%s on a waiting child refuses 409, got %d: %s", outcome, rec.Code, rec.Body.String())
			}
		}
	}
	rec := s.postNote(trentonClerkSub, "", twoChildNote(t, s, cust))
	if rec.Code != http.StatusCreated || rail.callCount() != 2 {
		t.Fatalf("both children refund and book, got %d after %d calls: %s", rec.Code, rail.callCount(), rec.Body.String())
	}
}

// Fix 2, with the fence bypassed (the settle is applied straight through the
// repository, as if the fence had admitted it): the dispatch does not send the
// settled child, writes that on its row, keeps its batch reason off the child
// that did refund, and the same key retry refuses to book the manager's typed
// ref. A manager looking again (NOT_REFUNDED) sends it back through the rail.
func TestIntegration_TwoTxResolution_NeverSentRefIsNotBookedByTheRetry(t *testing.T) {
	pool := intPool(t)
	rail := &hookRail{}
	s := newTrentonStackOnPoolWithRail(t, pool, rail)
	cust := s.plantCustomer("HH-501 never sent", 30, 5000000)
	typed := "rp_typed_never_" + key()
	rail.onCall = func(n int, refundKey string) {
		if n != 1 {
			return
		}
		id, _ := otherChild(t, s, cust, refundKey)
		tx, err := pool.Begin(context.Background())
		if err != nil {
			t.Errorf("begin: %v", err)
			return
		}
		if err := NewRepository(pool).ApplyCardRefundResolutionTx(context.Background(), tx, id, "UNBOOKED", typed, managerUserID, "attested"); err != nil {
			t.Errorf("bypass settle: %v", err)
			_ = tx.Rollback(context.Background())
			return
		}
		if err := tx.Commit(context.Background()); err != nil {
			t.Errorf("commit: %v", err)
		}
	}
	body := twoChildNote(t, s, cust)
	rec := s.postNote(trentonClerkSub, "", body)
	if rec.Code != http.StatusBadGateway || rail.callCount() != 1 {
		t.Fatalf("the settled child is not sent and the note answers 502, got %d after %d calls: %s", rec.Code, rail.callCount(), rec.Body.String())
	}
	var firstKey string
	if err := pool.QueryRow(context.Background(), `
SELECT refund_key FROM pos_card_refund_attempts WHERE customer_id = $1::uuid AND refund_ref <> $2`, cust, typed).Scan(&firstKey); err != nil {
		t.Fatalf("the first child's row: %v", err)
	}
	id, secondKey := otherChild(t, s, cust, firstKey)

	var status, refundRef, lastError string
	var log []map[string]any
	if err := pool.QueryRow(context.Background(),
		`SELECT status, refund_ref, last_error, resolution_log FROM pos_card_refund_attempts WHERE refund_key = $1`, secondKey).
		Scan(&status, &refundRef, &lastError, &log); err != nil {
		t.Fatalf("the settled child's row: %v", err)
	}
	if status != "UNBOOKED" || refundRef != typed || !strings.Contains(lastError, "never sent") {
		t.Fatalf("the row itself says the call was never sent: %s %q %q", status, refundRef, lastError)
	}
	if n := len(log); n < 2 || fmt.Sprint(log[n-1]["event"]) != "CALL_NOT_SENT" {
		t.Fatalf("the resolution log records the never sent event last: %v", log)
	}
	var firstErr string
	if err := pool.QueryRow(context.Background(), `SELECT last_error FROM pos_card_refund_attempts WHERE refund_key = $1`, firstKey).Scan(&firstErr); err != nil {
		t.Fatalf("the first child's last_error: %v", err)
	}
	if strings.Contains(firstErr, "manager") || strings.Contains(firstErr, "never sent") {
		t.Fatalf("the batch reason stays off the child that did refund: %q", firstErr)
	}

	// The same key retry refuses to book the typed ref with no call.
	rec = s.postNote(trentonClerkSub, "", body)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "never sent") {
		t.Fatalf("the retry refuses 409 in the never sent words, got %d: %s", rec.Code, rec.Body.String())
	}
	if n := s.countAll(`SELECT count(*) FROM payments WHERE gateway_ref = $1`, typed); n != 0 || rail.callCount() != 1 {
		t.Fatalf("nothing was booked on the typed ref and no call was made: %d payments, %d calls", n, rail.callCount())
	}

	// The flag is sticky against REFUNDED: a repeat, a second manager or a double submit (with the
	// same ref or none) is refused 409 in plain words, and the retry still refuses to book.
	for _, ref := range []string{typed, ""} {
		for _, who := range []string{trentonMgrSub, trentonMgrSub} {
			rec = s.do(http.MethodPost, "/api/v1/pos/card-refund-attempts/"+id+"/resolve", s.bearer(who), "",
				map[string]any{"outcome": "REFUNDED", "refund_ref": ref, "note": "again"})
			if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "never sent to the processor") {
				t.Fatalf("REFUNDED on a never sent row (ref %q) refuses 409 in plain words, got %d: %s", ref, rec.Code, rec.Body.String())
			}
		}
	}
	var stillFlagged bool
	if err := pool.QueryRow(context.Background(), `SELECT call_not_sent FROM pos_card_refund_attempts WHERE refund_key = $1`, secondKey).Scan(&stillFlagged); err != nil || !stillFlagged {
		t.Fatalf("the flag survives the refused settles: %v %v", stillFlagged, err)
	}
	rec = s.postNote(trentonClerkSub, "", body)
	if rec.Code != http.StatusConflict || rail.callCount() != 1 {
		t.Fatalf("the retry after the refused settles still refuses and makes no call, got %d after %d calls: %s", rec.Code, rail.callCount(), rec.Body.String())
	}

	// The manager looks again: the processor holds nothing, so NOT_REFUNDED is allowed on this row
	// and the retry sends it.
	rec = s.do(http.MethodPost, "/api/v1/pos/card-refund-attempts/"+id+"/resolve", s.bearer(trentonMgrSub), "",
		map[string]any{"outcome": "NOT_REFUNDED", "note": "console shows no reversal"})
	if rec.Code != http.StatusOK {
		t.Fatalf("NOT_REFUNDED is allowed on a never sent ref, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = s.postNote(trentonClerkSub, "", body)
	if rec.Code != http.StatusCreated || rail.callCount() != 2 {
		t.Fatalf("the retry sends the second child and books, got %d after %d calls: %s", rec.Code, rail.callCount(), rec.Body.String())
	}
	if n := s.countAll(`SELECT count(*) FROM payments WHERE gateway_ref = $1`, typed); n != 0 {
		t.Fatalf("the typed ref is never booked, got %d payments on it", n)
	}
	var bookedRef, bookedStatus string
	if err := pool.QueryRow(context.Background(), `SELECT status, refund_ref FROM pos_card_refund_attempts WHERE refund_key = $1`, secondKey).Scan(&bookedStatus, &bookedRef); err != nil || bookedStatus != "BOOKED" || bookedRef == typed || bookedRef == "" {
		t.Fatalf("the healed row books the processor's real ref: %s %q %v", bookedStatus, bookedRef, err)
	}
}
