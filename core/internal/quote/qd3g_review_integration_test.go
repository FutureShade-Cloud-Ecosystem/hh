//go:build integration

// qd3g_review_integration_test.go, QD-3d review round 2: the keyed retry through a dead link reads
// under the quote lock, never replays under another typed name, checks the affirmative like a live
// link, and answers a lookup failure as an error rather than as a 410.
// Fixtures use role words and times relative to the clock, never a person or a calendar date.
package quote

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// P2-A: an in flight accept that is past the quote row lock, and the link dies under it. An outside
// transaction holds the acceptance table against writes (reads still pass), so attempt 1 takes the
// quote lock, finds the link live, and stalls at its acceptance insert; the link is revoked, the keyed
// retry is sent and queues behind the quote lock, the table is released: the retry answers 200 with the first outcome (never 410) and
// exactly one acceptance row exists. (QD-3d review 3: an attempt still waiting FOR the quote lock when
// the link dies is refused with the 410 itself, see the QD3h tests.)
func TestIntegration_QD3g_RetryThroughDeadLinkWaitsForInFlightAccept(t *testing.T) {
	pool := intPool(t)
	now := qd3fNow()
	for _, multi := range []bool{true, false} {
		name := "multi_off"
		if multi {
			name = "multi_on"
		}
		t.Run(name, func(t *testing.T) {
			setMultiAccept(t, pool, multi)
			svc, quoteID, token, linkID := qd3fMinted(t, pool, now)
			ctx := context.Background()
			holder, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin holder: %v", err)
			}
			defer func() { _ = holder.Rollback(ctx) }()
			if _, err := holder.Exec(ctx, `LOCK TABLE quote_acceptances IN SHARE MODE`); err != nil {
				t.Fatalf("hold the acceptance table: %v", err)
			}
			first := make(chan answerOrErr, 1)
			go func() {
				r, err := svc.AcceptByTokenKeyed(ctx, token, qd3fCustomer, true, true, qd3dKeyA)
				first <- answerOrErr{res: r, err: err}
			}()
			qd3fWaitForLockWaiters(t, pool, 1)
			if err := svc.RevokeShareLink(qCtxAs(intStaff(intTrenton)), quoteID, linkID); err != nil {
				t.Fatalf("revoke: %v", err)
			}
			retry := make(chan *httptest.ResponseRecorder, 1)
			go func() { retry <- qd3dPost(t, svc, ctx, token, qd3dKeyA, qd3fCustomer) }()
			// The retry must queue behind the in flight accept. If it answers while the lock is still
			// held, it answered without knowing the truth.
			deadline := time.Now().Add(15 * time.Second)
			for queued := false; !queued; {
				select {
				case w := <-retry:
					t.Fatalf("the retry answered %d (%s) while the accept was still in flight", w.Code, w.Body.String())
				default:
				}
				var n int
				if err := pool.QueryRow(ctx, `
					SELECT COUNT(*) FROM pg_stat_activity
					 WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&n); err != nil {
					t.Fatalf("read lock waiters: %v", err)
				}
				queued = n >= 2
				if !queued {
					if time.Now().After(deadline) {
						t.Fatalf("the retry never queued behind the quote row lock")
					}
					time.Sleep(20 * time.Millisecond)
				}
			}
			if err := holder.Commit(ctx); err != nil {
				t.Fatalf("release the lock: %v", err)
			}
			a := <-first
			if a.err != nil {
				t.Fatalf("attempt 1: %v", a.err)
			}
			w := <-retry
			if w.Code != http.StatusOK {
				t.Fatalf("retry through a revoked link = %d (%s), want 200 with the first outcome", w.Code, w.Body.String())
			}
			if strings.TrimSpace(w.Body.String()) != qd3dBody(t, a.res) {
				t.Errorf("retry body %s is not the first outcome %s", w.Body.String(), qd3dBody(t, a.res))
			}
			if n := acceptanceRows(t, pool, quoteID); n != 1 {
				t.Errorf("acceptance rows = %d, want 1", n)
			}
		})
	}
}

type answerOrErr struct {
	res AcceptanceResult
	err error
}

// qd3gAccepted mints, accepts once under key A and kills the link; it returns the service that sees
// the dead link and the first outcome.
func qd3gAccepted(t *testing.T, pool *pgxpool.Pool, now time.Time) (svc *StaffService, quoteID, token string, first AcceptanceResult) {
	t.Helper()
	svc, quoteID, token, linkID := qd3fMinted(t, pool, now)
	first, err := svc.AcceptByTokenKeyed(context.Background(), token, qd3fCustomer, true, true, qd3dKeyA)
	if err != nil {
		t.Fatalf("attempt 1: %v", err)
	}
	if err := svc.RevokeShareLink(qCtxAs(intStaff(intTrenton)), quoteID, linkID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	return svc, quoteID, token, first
}

// P2-B: on a dead link the same key with another typed name is refused with the generic 410 and the
// first name never appears in that answer. A mutant that skips the name check on this path replays the
// first acceptance, name included, to anyone holding the old token and the key.
func TestIntegration_QD3g_DeadLinkOtherNameNeverReplayed(t *testing.T) {
	pool := intPool(t)
	now := qd3fNow()
	for _, multi := range []bool{true, false} {
		setMultiAccept(t, pool, multi)
		svc, quoteID, token, first := qd3gAccepted(t, pool, now)
		w := qd3dPost(t, svc, context.Background(), token, qd3dKeyA, "Customer Two")
		if w.Code != http.StatusGone {
			t.Fatalf("multi=%v: dead link, same key, other name = %d (%s), want 410", multi, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), qd3fCustomer) || strings.Contains(w.Body.String(), first.QuoteNo) {
			t.Errorf("the refusal must carry nothing of the first acceptance: %s", w.Body.String())
		}
		if n := acceptanceRows(t, pool, quoteID); n != 1 {
			t.Errorf("acceptance rows = %d, want 1", n)
		}
	}
}

// qd3gPostBody is qd3dPost with the raw JSON body in the caller's hands.
func qd3gPostBody(t *testing.T, svc *StaffService, token, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/public/v1/quotes/"+token+"/accept",
		strings.NewReader(body)).WithContext(context.Background())
	req.SetPathValue("token", token)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	NewStaffHandler(svc).publicAccept(w, req)
	return w
}

// P3-a: the dead link door checks the affirmative like the live link door: accepted false, or no
// accepted field at all, is the same 400.
func TestIntegration_QD3g_DeadLinkNeedsTheAffirmative(t *testing.T) {
	pool := intPool(t)
	now := qd3fNow()
	setMultiAccept(t, pool, true)
	svc, quoteID, token, _ := qd3gAccepted(t, pool, now)
	for name, body := range map[string]string{
		"accepted_false":  `{"typed_name":"` + qd3fCustomer + `","accepted":false}`,
		"accepted_absent": `{"typed_name":"` + qd3fCustomer + `"}`,
	} {
		w := qd3gPostBody(t, svc, token, qd3dKeyA, body)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "you must confirm") {
			t.Errorf("%s through a dead link = %d (%s), want the 400 you must confirm", name, w.Code, w.Body.String())
		}
	}
	if w := qd3gPostBody(t, svc, token, qd3dKeyA, `{"typed_name":"`+qd3fCustomer+`","accepted":true,"quantities_confirmed":true}`); w.Code != http.StatusOK {
		t.Errorf("the affirmed retry = %d (%s), want 200", w.Code, w.Body.String())
	}
	if n := acceptanceRows(t, pool, quoteID); n != 1 {
		t.Errorf("acceptance rows = %d, want 1", n)
	}
}

// failingReplay is a shareStore whose dead link lookup fails.
type failingReplay struct {
	shareStore
}

func (failingReplay) ReplayKeyedAcceptance(context.Context, acceptWrite) (AcceptanceResult, bool, error) {
	return AcceptanceResult{}, false, errors.New("injected lookup failure")
}

// P3-b: a lookup error on the dead link path is an error (a 500, the outcome unknown), never the 410
// that means "no acceptance exists under this key".
func TestIntegration_QD3g_DeadLinkLookupErrorIsNotA410(t *testing.T) {
	pool := intPool(t)
	now := qd3fNow()
	setMultiAccept(t, pool, true)
	svc, _, token, _ := qd3gAccepted(t, pool, now)
	broken := *svc
	broken.shares = failingReplay{svc.shares}
	w := qd3dPost(t, &broken, context.Background(), token, qd3dKeyA, qd3fCustomer)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("lookup failure on a dead link = %d (%s), want 500", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "injected") {
		t.Errorf("the 500 must not echo the internal error: %s", w.Body.String())
	}
}
