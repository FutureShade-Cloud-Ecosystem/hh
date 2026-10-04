//go:build integration

// qd3f_review_integration_test.go, QD-3d review round 1: the retry through a dead link, the rollback
// and anchor proofs, the replay writing nothing, and the broader concurrency and in flight coverage.
// Fixtures use role words and times relative to the clock, never a person or a calendar date.
package quote

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

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
