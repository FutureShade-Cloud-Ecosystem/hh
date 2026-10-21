//go:build integration

package bankrec

// integrity_lockorder_integration_test.go (SF-2j round 2, S3's P3-1): every bankrec writer takes the reconciliation
// row FOR SHARE and the shared advisory key of the line's month FIRST, before any payment row, so no writer can sit
// on a payment waiting for the reconciliation while completion sits on the reconciliation waiting for a line. A match
// of a line inside a COMPLETED month is refused, and a match or an import beside a completion in flight waits for it.

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// completionInFlight holds what CompleteReconciliation holds before it locks the lines: the reconciliation row FOR
// UPDATE and the exclusive advisory key. It returns a release that marks the month COMPLETED and commits. The
// completion path draws no counter, so testkit.DrawPark has nothing to park there; a plain transaction stands for it.
func (w *reachWorld) completionInFlight(t *testing.T, year, month int) (release func()) {
	t.Helper()
	ctx := context.Background()
	var recID string
	if err := w.pool.QueryRow(ctx, `INSERT INTO bank_reconciliations (bank_account_id, year, month, status) VALUES ($1::uuid, $2, $3, 'OPEN')
		ON CONFLICT (bank_account_id, year, month) DO UPDATE SET status = 'OPEN' RETURNING id::text`, w.account, year, month).Scan(&recID); err != nil {
		t.Fatalf("open the month: %v", err)
	}
	t.Cleanup(func() {
		_, _ = w.pool.Exec(context.Background(), `DELETE FROM bank_reconciliations WHERE id = $1::uuid`, recID)
	})
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM bank_reconciliations WHERE id = $1::uuid FOR UPDATE`, recID); err != nil {
		t.Fatalf("lock the row: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, reconKey(w.account, year, month)); err != nil {
		t.Fatalf("lock the key: %v", err)
	}
	return func() {
		if _, err := tx.Exec(ctx, `UPDATE bank_reconciliations SET status = 'COMPLETED', completed_at = now() WHERE id = $1::uuid`, recID); err != nil {
			t.Fatalf("complete: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}
}

func (w *reachWorld) monthOf(t *testing.T, line string) (year, month int) {
	t.Helper()
	if err := w.pool.QueryRow(context.Background(), `SELECT EXTRACT(YEAR FROM txn_date)::int, EXTRACT(MONTH FROM txn_date)::int FROM bank_statement_lines WHERE id = $1::uuid`, line).Scan(&year, &month); err != nil {
		t.Fatalf("the line's month: %v", err)
	}
	return year, month
}

// A line inside a COMPLETED month cannot be matched (a line imported after the sign off would post a clearing into a
// signed off month); nothing is written.
func TestIntegration_LockOrder_AMatchInsideACompletedMonthIsRefused(t *testing.T) {
	w := newReachWorld(t)
	ctx := context.Background()
	line := w.line(t, "LO CLOSED MONTH", 5000)
	y, m := w.monthOf(t, line)
	if _, err := w.pool.Exec(ctx, `INSERT INTO bank_reconciliations (bank_account_id, year, month, status, completed_at) VALUES ($1::uuid, $2, $3, 'COMPLETED', now())
		ON CONFLICT (bank_account_id, year, month) DO UPDATE SET status = 'COMPLETED'`, w.account, y, m); err != nil {
		t.Fatalf("complete the month: %v", err)
	}
	t.Cleanup(func() {
		_, _ = w.pool.Exec(context.Background(), `DELETE FROM bank_reconciliations WHERE bank_account_id = $1::uuid AND year = $2 AND month = $3`, w.account, y, m)
	})
	code, body := w.post(w.callerAB, w.manual(line, reachPayA1))
	ls, ps, n := w.written(t, line, reachPayA1)
	t.Logf("a match inside the completed month: %d %s | line %s payment %s matches %d", code, body, ls, ps, n)
	if code == http.StatusCreated || ls != "UNMATCHED" || ps != "RECEIVED" || n != 0 {
		t.Errorf("a line in a COMPLETED month was matched: answered %d, line %s, payment %s", code, ls, ps)
	}
	if !strings.Contains(body, "reconciled and completed") {
		t.Errorf("the refusal %q does not say the month is completed", body)
	}
}

// A match beside a completion in flight waits for it and then finds the month COMPLETED.
func TestIntegration_LockOrder_AMatchWaitsForACompletionInFlight(t *testing.T) {
	w := newReachWorld(t)
	line := w.line(t, "LO WAIT MATCH", 5000)
	y, m := w.monthOf(t, line)
	release := w.completionInFlight(t, y, m)
	done := make(chan [2]any, 1)
	go func() { c, b := w.post(w.callerAB, w.manual(line, reachPayA1)); done <- [2]any{c, b} }()
	select {
	case o := <-done:
		release()
		t.Fatalf("the match did not wait for the completion in flight: %v", o)
	case <-time.After(1500 * time.Millisecond):
	}
	release()
	select {
	case o := <-done:
		ls, ps, _ := w.written(t, line, reachPayA1)
		t.Logf("the match after the completion committed: %v | line %s payment %s", o, ls, ps)
		if o[0].(int) == http.StatusCreated || ls != "UNMATCHED" || ps != "RECEIVED" {
			t.Errorf("a match that waited on a completion landed in the signed off month: %v, line %s, payment %s", o, ls, ps)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the match never finished")
	}
}

// An import beside a completion in flight serialises on the key, as CompleteReconciliation's comment says.
func TestIntegration_LockOrder_AnImportWaitsForACompletionInFlight(t *testing.T) {
	w := newReachWorld(t)
	day := time.Now().UTC().Truncate(24 * time.Hour)
	line := w.line(t, "LO IMPORT ANCHOR", 5000)
	y, m := w.monthOf(t, line)
	release := w.completionInFlight(t, y, m)
	repo := NewRepository(w.pool)
	done := make(chan error, 1)
	go func() {
		_, _, err := repo.ImportLines(context.Background(), w.account, "", "lo.csv", []RawStatementLine{
			{TxnDate: day, Description: "LO IMPORTED", Reference: "LO-IMP", AmountCents: 4242, DedupeHash: DedupeHash(day, 4242, "LO IMPORTED", "LO-IMP")}}, "")
		done <- err
	}()
	select {
	case err := <-done:
		release()
		t.Fatalf("the import did not wait for the completion in flight: %v", err)
	case <-time.After(1500 * time.Millisecond):
	}
	release()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the import never finished")
	}
}
