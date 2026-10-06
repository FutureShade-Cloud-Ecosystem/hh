//go:build integration

// qd3f_review_integration_test.go, QD-3d review round 1: the retry through a dead link, the rollback
// and anchor proofs, the replay writing nothing, and the broader concurrency and in flight coverage.
// Fixtures use role words and times relative to the clock, never a person or a calendar date.
package quote

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const qd3fCustomer = "Customer One"

func qd3fNow() time.Time { return time.Now().UTC().Truncate(time.Second) }

// qd3fMinted is mintedFor that also hands back the link id (the revoke needs it).
func qd3fMinted(t *testing.T, pool *pgxpool.Pool, now time.Time) (svc *StaffService, quoteID, token, linkID string) {
	t.Helper()
	_, quoteID, _, _ = seedQuote(t, pool, "DRAFT", nil)
	cleanupShares(t, pool, quoteID)
	svc = shareSvc(t, pool, now)
	m, err := svc.MintShareLink(qCtxAs(intStaff(intTrenton)), quoteID, 0)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	return svc, quoteID, m.Token, m.ID
}

func qd3fEvents(t *testing.T, pool *pgxpool.Pool, quoteID, event string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM quote_share_link_events e
		  JOIN quote_share_links l ON l.id = e.share_link_id
		 WHERE l.quote_id = $1 AND e.event = $2`, quoteID, event).Scan(&n); err != nil {
		t.Fatalf("count events: %v", err)
	}
	return n
}

func qd3fKeyedAuditRows(t *testing.T, pool *pgxpool.Pool, quoteID, key string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM audit_log
		 WHERE entity_type='quote' AND entity_id=$1 AND action='QUOTE_ACCEPTED' AND changes->>'client_id' = $2`,
		quoteID, key).Scan(&n); err != nil {
		t.Fatalf("count keyed audit rows: %v", err)
	}
	return n
}

// P2-1: a retry of a COMMITTED acceptance through a link that has since died answers the first
// outcome with a 200 and writes nothing; a keyed attempt with no matching acceptance stays 410.
func TestIntegration_QD3f_RetryThroughDeadLink(t *testing.T) {
	pool := intPool(t)
	now := qd3fNow()
	for _, multi := range []bool{true, false} {
		setMultiAccept(t, pool, multi)
		kill := map[string]func(t *testing.T, svc *StaffService, quoteID, linkID string) *StaffService{
			"revoked": func(t *testing.T, svc *StaffService, quoteID, linkID string) *StaffService {
				if err := svc.RevokeShareLink(qCtxAs(intStaff(intTrenton)), quoteID, linkID); err != nil {
					t.Fatalf("revoke: %v", err)
				}
				return svc
			},
			"expired": func(t *testing.T, svc *StaffService, quoteID, linkID string) *StaffService {
				return shareSvc(t, pool, now.AddDate(10, 0, 0))
			},
		}
		for name, die := range kill {
			t.Run(name, func(t *testing.T) {
				svc, quoteID, token, linkID := qd3fMinted(t, pool, now)
				first, err := svc.AcceptByTokenKeyed(context.Background(), token, qd3fCustomer, true, qd3dKeyA)
				if err != nil {
					t.Fatalf("attempt 1: %v", err)
				}
				late := die(t, svc, quoteID, linkID)
				w := qd3dPost(t, late, context.Background(), token, qd3dKeyA, qd3fCustomer)
				if w.Code != http.StatusOK {
					t.Fatalf("multi=%v retry through a %s link = %d (%s), want 200 with the first outcome",
						multi, name, w.Code, w.Body.String())
				}
				if strings.TrimSpace(w.Body.String()) != qd3dBody(t, first) {
					t.Errorf("retry body %s is not the first outcome %s", w.Body.String(), qd3dBody(t, first))
				}
				if n := acceptanceRows(t, pool, quoteID); n != 1 {
					t.Errorf("acceptance rows = %d, want 1", n)
				}
				if n := qd3dAuditRows(t, pool, quoteID); n != 1 {
					t.Errorf("audit rows = %d, want 1", n)
				}
				// A dead link still refuses a keyed attempt it never accepted, and an unkeyed one.
				if w := qd3dPost(t, late, context.Background(), token, qd3dKeyB, qd3fCustomer); w.Code != http.StatusGone {
					t.Errorf("other key on a %s link = %d, want 410", name, w.Code)
				}
				if w := qd3dPost(t, late, context.Background(), token, "", qd3fCustomer); w.Code != http.StatusGone {
					t.Errorf("no key on a %s link = %d, want 410", name, w.Code)
				}
			})
		}
	}
}

// P2-1: a keyed attempt on a dead link with no acceptance at all is 410, and nothing is written.
func TestIntegration_QD3f_KeyedAttemptOnDeadLinkNothingAccepted(t *testing.T) {
	pool := intPool(t)
	now := qd3fNow()
	setMultiAccept(t, pool, true)
	svc, quoteID, token, linkID := qd3fMinted(t, pool, now)
	if err := svc.RevokeShareLink(qCtxAs(intStaff(intTrenton)), quoteID, linkID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if w := qd3dPost(t, svc, context.Background(), token, qd3dKeyA, qd3fCustomer); w.Code != http.StatusGone {
		t.Fatalf("keyed attempt on a revoked link, nothing accepted = %d, want 410", w.Code)
	}
	if n := acceptanceRows(t, pool, quoteID); n != 0 {
		t.Errorf("acceptance rows = %d, want 0", n)
	}
}

// P3-2: a replay writes nothing, which includes the ACCEPT row in quote_share_link_events.
func TestIntegration_QD3f_ReplayWritesNoShareEvent(t *testing.T) {
	pool := intPool(t)
	now := qd3fNow()
	setMultiAccept(t, pool, true)
	svc, quoteID, token, linkID := qd3fMinted(t, pool, now)
	ctx := context.Background()
	if _, err := svc.AcceptByTokenKeyed(ctx, token, qd3fCustomer, true, qd3dKeyA); err != nil {
		t.Fatalf("attempt 1: %v", err)
	}
	if n := qd3fEvents(t, pool, quoteID, "ACCEPT"); n != 1 {
		t.Fatalf("ACCEPT events after the first acceptance = %d, want 1", n)
	}
	if _, err := svc.AcceptByTokenKeyed(ctx, token, qd3fCustomer, true, qd3dKeyA); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if n := qd3fEvents(t, pool, quoteID, "ACCEPT"); n != 1 {
		t.Errorf("ACCEPT events after a replay = %d, want still 1", n)
	}
	// A new key is a new acceptance and does log one.
	if _, err := svc.AcceptByTokenKeyed(ctx, token, qd3fCustomer, true, qd3dKeyB); err != nil {
		t.Fatalf("second acceptance: %v", err)
	}
	if n := qd3fEvents(t, pool, quoteID, "ACCEPT"); n != 2 {
		t.Errorf("ACCEPT events after a second acceptance = %d, want 2", n)
	}
	// And the replay through a dead link writes none either.
	if err := svc.RevokeShareLink(qCtxAs(intStaff(intTrenton)), quoteID, linkID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := svc.AcceptByTokenKeyed(ctx, token, qd3fCustomer, true, qd3dKeyA); err != nil {
		t.Fatalf("replay through the dead link: %v", err)
	}
	if n := qd3fEvents(t, pool, quoteID, "ACCEPT"); n != 2 {
		t.Errorf("ACCEPT events after a dead link replay = %d, want still 2", n)
	}
}

// P3-1: the anchor is the audit row carrying THIS key. A trigger swallows the second acceptance's
// audit insert (unreachable today, the hardening is for the day something does); the keyed accept must
// then be refused and write nothing, never anchor to the earlier acceptance's row.
func TestIntegration_QD3f_AnchorIsTheRowCarryingThisKey(t *testing.T) {
	pool := intPool(t)
	now := qd3fNow()
	setMultiAccept(t, pool, true)
	svc, quoteID, token, _ := qd3fMinted(t, pool, now)
	ctx := context.Background()
	if _, err := svc.AcceptByTokenKeyed(ctx, token, qd3fCustomer, true, qd3dKeyA); err != nil {
		t.Fatalf("attempt 1: %v", err)
	}
	for _, ddl := range []string{
		`CREATE OR REPLACE FUNCTION qd3f_swallow() RETURNS trigger LANGUAGE plpgsql AS $$
		 BEGIN
		   IF NEW.changes->>'client_id' = '` + qd3dKeyB + `' THEN RETURN NULL; END IF;
		   RETURN NEW;
		 END $$`,
		`CREATE TRIGGER qd3f_swallow BEFORE INSERT ON audit_log FOR EACH ROW EXECUTE FUNCTION qd3f_swallow()`,
	} {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			t.Fatalf("install trigger: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS qd3f_swallow ON audit_log`)
		_, _ = pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS qd3f_swallow()`)
	})
	if _, err := svc.AcceptByTokenKeyed(ctx, token, qd3fCustomer, true, qd3dKeyB); err == nil {
		t.Errorf("a keyed accept whose own audit row is missing must be refused, not anchored to another row")
	}
	if n := acceptanceRows(t, pool, quoteID); n != 1 {
		t.Errorf("acceptance rows = %d, want 1 (the refused accept wrote nothing)", n)
	}
	if n := qd3fKeyedAuditRows(t, pool, quoteID, qd3dKeyB); n != 0 {
		t.Errorf("keyed audit rows for the refused key = %d, want 0", n)
	}
}

// P2-3 (condition 1 of the approval): a rolled back accept leaves no key behind. The forced failure
// comes AFTER the audit insert on the same transaction, so the keyed audit row existed inside it.
func TestIntegration_QD3f_RolledBackAcceptLeavesNoKey(t *testing.T) {
	pool := intPool(t)
	now := qd3fNow()
	for _, multi := range []bool{true, false} {
		setMultiAccept(t, pool, multi)
		svc, quoteID, token, linkID := qd3fMinted(t, pool, now)
		ctx := context.Background()
		boom := errors.New("forced failure after the audit insert")
		_, err := svc.shares.AcceptQuoteGuarded(ctx, acceptWrite{
			QuoteID: quoteID, ShareLinkID: linkID, TypedName: qd3fCustomer, Now: now, IdempotencyKey: qd3dKeyA,
		}, acceptHooks{
			record: func(ctx context.Context, tx pgx.Tx, r AcceptanceResult) (int64, error) {
				seq, rerr := svc.recordAcceptance(ctx, tx, quoteID, linkID, r)
				if rerr != nil {
					return 0, rerr
				}
				var inside int
				if qerr := tx.QueryRow(ctx, `SELECT COUNT(*) FROM audit_log
					WHERE entity_type='quote' AND entity_id=$1 AND changes->>'client_id'=$2`,
					quoteID, qd3dKeyA).Scan(&inside); qerr != nil || inside != 1 || seq == 0 {
					t.Errorf("the keyed audit row must exist inside the transaction: n=%d seq=%d err=%v", inside, seq, qerr)
				}
				return seq, boom
			},
		})
		if !errors.Is(err, boom) {
			t.Fatalf("multi=%v: accept error = %v, want the forced failure", multi, err)
		}
		if n := qd3fKeyedAuditRows(t, pool, quoteID, qd3dKeyA); n != 0 {
			t.Errorf("multi=%v: keyed audit rows after the rollback = %d, want 0", multi, n)
		}
		if n := acceptanceRows(t, pool, quoteID); n != 0 {
			t.Errorf("multi=%v: acceptance rows after the rollback = %d, want 0", multi, n)
		}
		if got := qd3dStatus(t, pool, quoteID); got != "SENT" {
			t.Errorf("multi=%v: status after the rollback = %s, want SENT", multi, got)
		}
		// The retry under the same key records fresh: it is the first acceptance, not a replay.
		res, err := svc.AcceptByTokenKeyed(ctx, token, qd3fCustomer, true, qd3dKeyA)
		if err != nil {
			t.Fatalf("multi=%v: retry: %v", multi, err)
		}
		if res.Repeat {
			t.Errorf("multi=%v: the retry after a rollback is a first acceptance, got a repeat", multi)
		}
		if n := acceptanceRows(t, pool, quoteID); n != 1 {
			t.Errorf("multi=%v: acceptance rows = %d, want 1", multi, n)
		}
		if n := qd3fKeyedAuditRows(t, pool, quoteID, qd3dKeyA); n != 1 {
			t.Errorf("multi=%v: keyed audit rows = %d, want 1", multi, n)
		}
	}
}

// silentRecorder is an audit recorder whose RecordTx writes nothing: a trail that lost the row.
type silentRecorder struct{}

func (silentRecorder) RecordTx(context.Context, audit.Execer, audit.SensitiveAccess) error {
	return nil
}

// P2-3: the anchor-missing refusal. The keyed audit row never landed, so a keyed accept is refused and
// writes nothing; the same loss on an unkeyed accept degrades to a zero anchor, as it always did.
func TestIntegration_QD3f_AnchorMissingRefusesKeyedAccept(t *testing.T) {
	pool := intPool(t)
	now := qd3fNow()
	setMultiAccept(t, pool, true)
	svc, quoteID, token, _ := qd3fMinted(t, pool, now)
	svc.auditTx = silentRecorder{}
	ctx := context.Background()
	_, err := svc.AcceptByTokenKeyed(ctx, token, qd3fCustomer, true, qd3dKeyA)
	if err == nil || !strings.Contains(err.Error(), "audit anchor") {
		t.Fatalf("a keyed accept with no anchor must be refused naming the anchor, got %v", err)
	}
	if n := acceptanceRows(t, pool, quoteID); n != 0 {
		t.Errorf("acceptance rows = %d, want 0", n)
	}
	if got := qd3dStatus(t, pool, quoteID); got != "SENT" {
		t.Errorf("status = %s, want SENT", got)
	}
	if _, err := svc.AcceptByToken(ctx, token, qd3fCustomer, true); err != nil {
		t.Errorf("an unkeyed accept with the same lost row still degrades, got %v", err)
	}
}

// P2-3: the guard for a missing audit trail (share.go recordAcceptance, auditTx nil): a keyed accept
// has nowhere to carry its key, so it is refused and writes nothing; an unkeyed one is unchanged.
func TestIntegration_QD3f_MissingAuditTrailRefusesKeyedAccept(t *testing.T) {
	pool := intPool(t)
	now := qd3fNow()
	setMultiAccept(t, pool, true)
	svc, quoteID, token, _ := qd3fMinted(t, pool, now)
	svc.auditTx = nil
	ctx := context.Background()
	_, err := svc.AcceptByTokenKeyed(ctx, token, qd3fCustomer, true, qd3dKeyA)
	if err == nil || !strings.Contains(err.Error(), "needs the audit trail") {
		t.Fatalf("a keyed accept with no audit trail must be refused, got %v", err)
	}
	if n := acceptanceRows(t, pool, quoteID); n != 0 {
		t.Errorf("acceptance rows = %d, want 0", n)
	}
	if got := qd3dStatus(t, pool, quoteID); got != "SENT" {
		t.Errorf("status = %s, want SENT", got)
	}
	if _, err := svc.AcceptByToken(ctx, token, qd3fCustomer, true); err != nil {
		t.Errorf("an unkeyed accept with no audit trail is unchanged, got %v", err)
	}
}

// P3-5: twelve parallel accepts under one key, multi-accept on AND off: twelve identical answers, one
// acceptance row, one audit row.
func TestIntegration_QD3f_ConcurrentSameKeyBothModes(t *testing.T) {
	pool := intPool(t)
	now := qd3fNow()
	for _, multi := range []bool{true, false} {
		setMultiAccept(t, pool, multi)
		svc, quoteID, token, _ := qd3fMinted(t, pool, now)
		const n = 12
		bodies := make([]string, n)
		errs := make([]error, n)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				r, err := svc.AcceptByTokenKeyed(context.Background(), token, qd3fCustomer, true, qd3dKeyA)
				errs[i] = err
				if err == nil {
					bodies[i] = qd3dBody(t, r)
				}
			}(i)
		}
		close(start)
		wg.Wait()
		for i := 0; i < n; i++ {
			if errs[i] != nil {
				t.Fatalf("multi=%v accept %d: %v", multi, i, errs[i])
			}
			if bodies[i] != bodies[0] {
				t.Errorf("multi=%v answer %d differs from the first outcome:\n%s\n%s", multi, i, bodies[i], bodies[0])
			}
		}
		if got := acceptanceRows(t, pool, quoteID); got != 1 {
			t.Errorf("multi=%v acceptance rows = %d, want 1", multi, got)
		}
		if got := qd3dAuditRows(t, pool, quoteID); got != 1 {
			t.Errorf("multi=%v audit rows = %d, want 1", multi, got)
		}
	}
}

// qd3fWaitForLockWaiters polls until at least want sessions of this database wait on a lock.
func qd3fWaitForLockWaiters(t *testing.T, pool *pgxpool.Pool, want int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := pool.QueryRow(context.Background(), `
			SELECT COUNT(*) FROM pg_stat_activity
			 WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&n); err != nil {
			t.Fatalf("read lock waiters: %v", err)
		}
		if n >= want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("fewer than %d sessions queued on the quote row lock", want)
}

// P3-5: a genuinely in flight commit. An outside transaction holds the quote row lock while attempt 1
// and then the retry queue behind it; releasing the lock lets attempt 1 commit and the retry, which
// was waiting, must answer the byte identical first outcome. Multi-accept on and off.
func TestIntegration_QD3f_InFlightCommitThenRetry(t *testing.T) {
	pool := intPool(t)
	now := qd3fNow()
	for _, multi := range []bool{true, false} {
		name := "multi_off"
		if multi {
			name = "multi_on"
		}
		t.Run(name, func(t *testing.T) {
			setMultiAccept(t, pool, multi)
			svc, quoteID, token, _ := qd3fMinted(t, pool, now)
			ctx := context.Background()
			holder, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin holder: %v", err)
			}
			defer func() { _ = holder.Rollback(ctx) }()
			if _, err := holder.Exec(ctx, `SELECT 1 FROM quotes WHERE id = $1 FOR UPDATE`, quoteID); err != nil {
				t.Fatalf("hold the quote row lock: %v", err)
			}
			type answer struct {
				body string
				err  error
			}
			out := make(chan answer, 2)
			attempt := func() {
				r, err := svc.AcceptByTokenKeyed(ctx, token, qd3fCustomer, true, qd3dKeyA)
				a := answer{err: err}
				if err == nil {
					a.body = qd3dBody(t, r)
				}
				out <- a
			}
			go attempt()
			qd3fWaitForLockWaiters(t, pool, 1)
			go attempt()
			qd3fWaitForLockWaiters(t, pool, 2)
			if n := acceptanceRows(t, pool, quoteID); n != 0 {
				t.Fatalf("nothing may commit while the lock is held, %d rows", n)
			}
			if err := holder.Commit(ctx); err != nil {
				t.Fatalf("release the lock: %v", err)
			}
			a, b := <-out, <-out
			if a.err != nil || b.err != nil {
				t.Fatalf("both attempts must answer: %v / %v", a.err, b.err)
			}
			if a.body != b.body {
				t.Errorf("the queued retry must answer the first outcome:\n%s\n%s", a.body, b.body)
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

// Condition 3: the key is bounded (a UUID, 36 characters of hex and hyphens), anything else is a 400,
// and the key is never echoed into an error body.
func TestIntegration_QD3f_KeyBoundedAndNeverEchoed(t *testing.T) {
	pool := intPool(t)
	now := qd3fNow()
	setMultiAccept(t, pool, true)
	svc, quoteID, token, _ := qd3fMinted(t, pool, now)
	bad := []string{
		strings.Repeat("a", 37), strings.Repeat("a", 4096), qd3dKeyA[:35], "{" + qd3dKeyA + "}",
		strings.ReplaceAll(qd3dKeyA, "-", "_"), "secretlooking-not-a-uuid-at-all-0000000",
	}
	for _, k := range bad {
		w := qd3dPost(t, svc, context.Background(), token, k, qd3fCustomer)
		if w.Code != http.StatusBadRequest {
			t.Errorf("key of length %d = %d, want 400", len(k), w.Code)
		}
		if strings.Contains(w.Body.String(), k) || strings.Contains(w.Body.String(), "secretlooking") {
			t.Errorf("the 400 body echoes the key: %s", w.Body.String())
		}
	}
	// The 409 for a reused key must not echo it either.
	if _, err := svc.AcceptByTokenKeyed(context.Background(), token, qd3fCustomer, true, qd3dKeyA); err != nil {
		t.Fatalf("accept: %v", err)
	}
	w := qd3dPost(t, svc, context.Background(), token, qd3dKeyA, "Another Customer")
	if w.Code != http.StatusConflict || strings.Contains(w.Body.String(), qd3dKeyA) {
		t.Errorf("reused key = %d (%s): want a 409 that does not echo the key", w.Code, w.Body.String())
	}
	if n := acceptanceRows(t, pool, quoteID); n != 1 {
		t.Errorf("acceptance rows = %d, want 1", n)
	}
}
