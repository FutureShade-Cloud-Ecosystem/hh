//go:build integration

// qd3g_review_integration_test.go, QD-3d review round 2: the keyed retry through a dead link reads
// under the quote lock, never replays under another typed name, checks the affirmative like a live
// link, and answers a lookup failure as an error rather than as a 410.
// Fixtures use role words and times relative to the clock, never a person or a calendar date.
package quote

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// P2-A: the exact probe of the review. An outside transaction holds the quote row lock, attempt 1
// queues behind it, the link is revoked, the keyed retry is sent, the lock is released: the retry
// answers 200 with the first outcome (never 410) and exactly one acceptance row exists.
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
			if _, err := holder.Exec(ctx, `SELECT 1 FROM quotes WHERE id = $1 FOR UPDATE`, quoteID); err != nil {
				t.Fatalf("hold the quote row lock: %v", err)
			}
			first := make(chan answerOrErr, 1)
			go func() {
				r, err := svc.AcceptByTokenKeyed(ctx, token, qd3fCustomer, true, qd3dKeyA)
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
