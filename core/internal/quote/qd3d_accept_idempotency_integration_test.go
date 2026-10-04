//go:build integration

// qd3d_accept_idempotency_integration_test.go, QD-3d: the public share link accept carries an
// optional Idempotency-Key, and a repeat under the same key answers the FIRST outcome, never a second
// acceptance row, multi-accept on or off (decision row 744: a re-read showing SENT after a failed
// accept does not prove the acceptance was not recorded).
//
// Every test drives the real service and repository against Postgres; the handler tests drive the
// real StaffHandler.publicAccept so the header, the 400 and the 409 mapping are the shipped ones.
package quote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	qd3dKeyA = "7b1c2d3e-4f50-4a61-8b72-93a4b5c6d7e8"
	qd3dKeyB = "0a1b2c3d-4e5f-4061-9273-84a5b6c7d8e9"
)

func qd3dAuditRows(t *testing.T, pool *pgxpool.Pool, quoteID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM audit_log
		 WHERE entity_type='quote' AND entity_id=$1 AND action='QUOTE_ACCEPTED'`, quoteID).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	return n
}

func qd3dStatus(t *testing.T, pool *pgxpool.Pool, quoteID string) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM quotes WHERE id=$1`, quoteID).Scan(&s); err != nil {
		t.Fatalf("read status: %v", err)
	}
	return s
}

func qd3dBody(t *testing.T, r AcceptanceResult) string {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// qd3dPost drives the shipped handler: header, body, path token.
func qd3dPost(t *testing.T, svc *StaffService, ctx context.Context, token, key, name string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/public/v1/quotes/"+token+"/accept",
		strings.NewReader(`{"typed_name":"`+name+`","accepted":true}`)).WithContext(ctx)
	req.SetPathValue("token", token)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	NewStaffHandler(svc).publicAccept(w, req)
	return w
}

// (a) eight concurrent accepts, one key, multi-accept ON: one row, one audit row, eight identical 200s.
func TestIntegration_QD3d_ConcurrentSameKey_OneRow(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	setMultiAccept(t, pool, true)
	svc, quoteID, token := mintedFor(t, pool, now, nil)

	const n = 8
	bodies := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			r, err := svc.AcceptByTokenKeyed(context.Background(), token, "Dana Reyes", true, qd3dKeyA)
			errs[i] = err
			if err == nil {
				b, _ := json.Marshal(r)
				bodies[i] = string(b)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("accept %d: %v", i, errs[i])
		}
		if bodies[i] != bodies[0] {
			t.Errorf("answer %d differs from the first outcome:\n%s\n%s", i, bodies[i], bodies[0])
		}
	}
	if got := acceptanceRows(t, pool, quoteID); got != 1 {
		t.Errorf("acceptance rows = %d, want exactly 1", got)
	}
	if got := qd3dAuditRows(t, pool, quoteID); got != 1 {
		t.Errorf("audit rows = %d, want exactly 1", got)
	}
	if strings.Contains(bodies[0], "repeat_acceptance") {
		t.Errorf("the first outcome was not a repeat, no answer may claim it was: %s", bodies[0])
	}
}

// (b) the slow commit: attempt 1 commits, its response never reaches the client (the request context
// is cancelled), the retry under the same key answers the first outcome. Multi-accept ON and OFF.
func TestIntegration_QD3d_SlowCommitThenRetry_FirstOutcome(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	for _, multi := range []bool{true, false} {
		name := "multi_off"
		if multi {
			name = "multi_on"
		}
		t.Run(name, func(t *testing.T) {
			setMultiAccept(t, pool, multi)
			svc, quoteID, token := mintedFor(t, pool, now, nil)

			// Attempt 1 goes through the handler; the server commits, then the client cancels before it
			// can read the answer (the recorder is thrown away). The first outcome is read back from the
			// database, the only place it is certain to exist.
			c1, cancel := context.WithCancel(context.Background())
			qd3dPost(t, svc, c1, token, qd3dKeyA, "Dana Reyes")
			cancel()
			var first AcceptanceResult
			if err := pool.QueryRow(context.Background(), `
				SELECT quote_no, typed_name, accepted_subtotal_cents, accepted_tax_cents, accepted_total_cents, accepted_at
				  FROM quote_acceptances WHERE quote_id=$1`, quoteID).
				Scan(&first.QuoteNo, &first.TypedName, &first.SubtotalCents, &first.TaxCents, &first.TotalCents, &first.AcceptedAt); err != nil {
				t.Fatalf("attempt 1 must have committed one acceptance: %v", err)
			}

			// Attempt 2, the retry with the same key, through the handler on a live context.
			w := qd3dPost(t, svc, context.Background(), token, qd3dKeyA, "Dana Reyes")
			if w.Code != http.StatusOK {
				t.Fatalf("retry = %d (%s), want 200 with the first outcome", w.Code, w.Body.String())
			}
			var got AcceptanceResult
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("body: %v", err)
			}
			if got.QuoteNo != first.QuoteNo || got.TotalCents != first.TotalCents ||
				got.TypedName != first.TypedName || !got.AcceptedAt.Equal(first.AcceptedAt) || got.Repeat {
				t.Errorf("retry body %+v is not the first outcome %+v", got, first)
			}
			if n := acceptanceRows(t, pool, quoteID); n != 1 {
				t.Errorf("acceptance rows = %d, want 1", n)
			}
			if n := qd3dAuditRows(t, pool, quoteID); n != 1 {
				t.Errorf("audit rows = %d, want 1", n)
			}
		})
	}
}

// A retry whose own context is already cancelled must not record anything either: the request dies
// before or at the lock and nothing lands (the other half of "the slow client").
func TestIntegration_QD3d_CancelledRetryWritesNothing(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	setMultiAccept(t, pool, true)
	svc, quoteID, token := mintedFor(t, pool, now, nil)
	if _, err := svc.AcceptByTokenKeyed(context.Background(), token, "Dana Reyes", true, qd3dKeyA); err != nil {
		t.Fatalf("attempt 1: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = svc.AcceptByTokenKeyed(ctx, token, "Dana Reyes", true, qd3dKeyA)
	if n := acceptanceRows(t, pool, quoteID); n != 1 {
		t.Errorf("acceptance rows = %d, want 1", n)
	}
}

// (c) the quote moved on: CONVERTED, ACCEPTED or even back to SENT, the same key still answers the
// first outcome and writes nothing.
func TestIntegration_QD3d_RetryAfterQuoteMovedOn(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	for _, multi := range []bool{true, false} {
		for _, moved := range []string{"CONVERTED", "SENT", "EXPIRED"} {
			t.Run(moved, func(t *testing.T) {
				setMultiAccept(t, pool, multi)
				svc, quoteID, token := mintedFor(t, pool, now, nil)
				first, err := svc.AcceptByTokenKeyed(context.Background(), token, "Dana Reyes", true, qd3dKeyA)
				if err != nil {
					t.Fatalf("attempt 1: %v", err)
				}
				if _, err := pool.Exec(context.Background(),
					`UPDATE quotes SET status=$2 WHERE id=$1`, quoteID, moved); err != nil {
					t.Fatalf("move quote on: %v", err)
				}
				again, err := svc.AcceptByTokenKeyed(context.Background(), token, "Dana Reyes", true, qd3dKeyA)
				if err != nil {
					t.Fatalf("retry after %s: %v", moved, err)
				}
				if qd3dBody(t, again) != qd3dBody(t, first) {
					t.Errorf("retry answered %s, want the first outcome %s", qd3dBody(t, again), qd3dBody(t, first))
				}
				if n := acceptanceRows(t, pool, quoteID); n != 1 {
					t.Errorf("acceptance rows = %d, want 1", n)
				}
				if n := qd3dAuditRows(t, pool, quoteID); n != 1 {
					t.Errorf("audit rows = %d, want 1", n)
				}
				if got := qd3dStatus(t, pool, quoteID); got != moved {
					t.Errorf("a replay must not touch the status: %s became %s", moved, got)
				}
			})
		}
	}
}

// The first outcome of a REPEAT acceptance keeps its repeat flag on replay (only if the first had it).
func TestIntegration_QD3d_ReplayOfARepeatKeepsTheRepeatFlag(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	setMultiAccept(t, pool, true)
	svc, quoteID, token := mintedFor(t, pool, now, nil)
	ctx := context.Background()
	if _, err := svc.AcceptByTokenKeyed(ctx, token, "Dana Reyes", true, qd3dKeyA); err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := svc.AcceptByTokenKeyed(ctx, token, "Dana Reyes", true, qd3dKeyB)
	if err != nil || !second.Repeat {
		t.Fatalf("second under a new key must be a recorded repeat: %+v %v", second, err)
	}
	replay, err := svc.AcceptByTokenKeyed(ctx, token, "Dana Reyes", true, qd3dKeyB)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if qd3dBody(t, replay) != qd3dBody(t, second) || !replay.Repeat {
		t.Errorf("replay %s must equal the second outcome %s", qd3dBody(t, replay), qd3dBody(t, second))
	}
	if n := acceptanceRows(t, pool, quoteID); n != 2 {
		t.Errorf("acceptance rows = %d, want 2", n)
	}
}

// (d) a different key on a multi-accept quote records a second row, today's behaviour.
func TestIntegration_QD3d_DifferentKeyMultiAccept_SecondRow(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	setMultiAccept(t, pool, true)
	svc, quoteID, token := mintedFor(t, pool, now, nil)
	ctx := context.Background()
	if _, err := svc.AcceptByTokenKeyed(ctx, token, "Dana Reyes", true, qd3dKeyA); err != nil {
		t.Fatalf("first: %v", err)
	}
	r, err := svc.AcceptByTokenKeyed(ctx, token, "Dana Reyes", true, qd3dKeyB)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !r.Repeat {
		t.Error("the second acceptance is a repeat and says so")
	}
	if n := acceptanceRows(t, pool, quoteID); n != 2 {
		t.Errorf("acceptance rows = %d, want 2", n)
	}
	if n := qd3dAuditRows(t, pool, quoteID); n != 2 {
		t.Errorf("audit rows = %d, want 2", n)
	}
}

// (d2) a different key with multi-accept OFF is still today's refusal.
func TestIntegration_QD3d_DifferentKeyMultiOff_Refused(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	setMultiAccept(t, pool, false)
	svc, quoteID, token := mintedFor(t, pool, now, nil)
	ctx := context.Background()
	if _, err := svc.AcceptByTokenKeyed(ctx, token, "Dana Reyes", true, qd3dKeyA); err != nil {
		t.Fatalf("first: %v", err)
	}
	w := qd3dPost(t, svc, ctx, token, qd3dKeyB, "Dana Reyes")
	if w.Code != http.StatusConflict {
		t.Errorf("a new key on an accepted quote with multi-accept off = %d, want 409", w.Code)
	}
	if n := acceptanceRows(t, pool, quoteID); n != 1 {
		t.Errorf("acceptance rows = %d, want 1", n)
	}
}

// (e) no key: unchanged. Two accepts with multi-accept ON are two rows, and nothing is deduplicated.
func TestIntegration_QD3d_NoKey_Unchanged(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	setMultiAccept(t, pool, true)
	svc, quoteID, token := mintedFor(t, pool, now, nil)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		w := qd3dPost(t, svc, ctx, token, "", "Dana Reyes")
		if w.Code != http.StatusOK {
			t.Fatalf("no-key accept %d = %d (%s)", i, w.Code, w.Body.String())
		}
	}
	if n := acceptanceRows(t, pool, quoteID); n != 2 {
		t.Errorf("acceptance rows = %d, want 2", n)
	}
	// And a no-key retry never matches a keyed acceptance.
	if _, err := svc.AcceptByTokenKeyed(ctx, token, "Dana Reyes", true, qd3dKeyA); err != nil {
		t.Fatalf("keyed: %v", err)
	}
	if _, err := svc.AcceptByToken(ctx, token, "Dana Reyes", true); err != nil {
		t.Fatalf("no key after keyed: %v", err)
	}
	if n := acceptanceRows(t, pool, quoteID); n != 4 {
		t.Errorf("acceptance rows = %d, want 4", n)
	}
}

// (f) the key is scoped to the quote AND the share link: the same key elsewhere never matches.
func TestIntegration_QD3d_KeyScopedToQuoteAndLink(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	setMultiAccept(t, pool, true)
	ctx := context.Background()

	t.Run("another quote", func(t *testing.T) {
		svcA, quoteA, tokenA := mintedFor(t, pool, now, nil)
		svcB, quoteB, tokenB := mintedFor(t, pool, now, nil)
		if _, err := svcA.AcceptByTokenKeyed(ctx, tokenA, "Dana Reyes", true, qd3dKeyA); err != nil {
			t.Fatalf("A: %v", err)
		}
		rb, err := svcB.AcceptByTokenKeyed(ctx, tokenB, "Lee Park", true, qd3dKeyA)
		if err != nil {
			t.Fatalf("B with A's key must be its own acceptance, not a 409 or a replay: %v", err)
		}
		if rb.TypedName != "Lee Park" {
			t.Errorf("B got someone else's outcome: %+v", rb)
		}
		if acceptanceRows(t, pool, quoteA) != 1 || acceptanceRows(t, pool, quoteB) != 1 {
			t.Errorf("each quote must hold its own single acceptance")
		}
	})

	t.Run("another link on the same quote", func(t *testing.T) {
		svc, quoteID, token1 := mintedFor(t, pool, now, nil)
		m2, err := svc.MintShareLink(qCtxAs(intStaff(intTrenton)), quoteID, 0)
		if err != nil {
			t.Fatalf("mint second link: %v", err)
		}
		if _, err := svc.AcceptByTokenKeyed(ctx, token1, "Dana Reyes", true, qd3dKeyA); err != nil {
			t.Fatalf("link 1: %v", err)
		}
		r2, err := svc.AcceptByTokenKeyed(ctx, m2.Token, "Lee Park", true, qd3dKeyA)
		if err != nil {
			t.Fatalf("link 2 with link 1's key: %v", err)
		}
		if r2.TypedName != "Lee Park" || !r2.Repeat {
			t.Errorf("link 2 must record its own (repeat) acceptance, got %+v", r2)
		}
		if n := acceptanceRows(t, pool, quoteID); n != 2 {
			t.Errorf("acceptance rows = %d, want 2", n)
		}
	})
}

// (g) the same key with another typed name is a 409 that names the key reuse, never the first
// outcome of someone else's name.
func TestIntegration_QD3d_SameKeyOtherName_409(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	for _, multi := range []bool{true, false} {
		setMultiAccept(t, pool, multi)
		svc, quoteID, token := mintedFor(t, pool, now, nil)
		if _, err := svc.AcceptByTokenKeyed(context.Background(), token, "Dana Reyes", true, qd3dKeyA); err != nil {
			t.Fatalf("first: %v", err)
		}
		w := qd3dPost(t, svc, context.Background(), token, qd3dKeyA, "Lee Park")
		if w.Code != http.StatusConflict {
			t.Fatalf("multi=%v: same key, other name = %d (%s), want 409", multi, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "different acceptance") {
			t.Errorf("the 409 must say the key was used for a different acceptance: %s", w.Body.String())
		}
		if strings.Contains(w.Body.String(), "Dana") {
			t.Errorf("the 409 must not leak the first name: %s", w.Body.String())
		}
		if n := acceptanceRows(t, pool, quoteID); n != 1 {
			t.Errorf("acceptance rows = %d, want 1", n)
		}
	}
}

// (h) a malformed key is a 400 and writes nothing; a well formed key in any case matches.
func TestIntegration_QD3d_MalformedKey_400(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	setMultiAccept(t, pool, true)
	svc, quoteID, token := mintedFor(t, pool, now, nil)
	for _, bad := range []string{"not-a-uuid", "7b1c2d3e4f504a618b7293a4b5c6d7e8", qd3dKeyA + "x", " " + qd3dKeyA, "zzzzzzzz-4f50-4a61-8b72-93a4b5c6d7e8"} {
		w := qd3dPost(t, svc, context.Background(), token, bad, "Dana Reyes")
		if w.Code != http.StatusBadRequest {
			t.Errorf("key %q = %d, want 400", bad, w.Code)
		}
	}
	if n := acceptanceRows(t, pool, quoteID); n != 0 {
		t.Errorf("a refused key must write nothing, %d rows", n)
	}
	// Upper case is the same key as lower case.
	if w := qd3dPost(t, svc, context.Background(), token, strings.ToUpper(qd3dKeyA), "Dana Reyes"); w.Code != http.StatusOK {
		t.Fatalf("upper case key = %d (%s)", w.Code, w.Body.String())
	}
	if w := qd3dPost(t, svc, context.Background(), token, qd3dKeyA, "Dana Reyes"); w.Code != http.StatusOK {
		t.Fatalf("lower case retry = %d", w.Code)
	}
	if n := acceptanceRows(t, pool, quoteID); n != 1 {
		t.Errorf("acceptance rows = %d, want 1", n)
	}
}
