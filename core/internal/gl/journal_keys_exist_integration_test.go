//go:build integration

package gl

// journal_keys_exist_integration_test.go, the keyed existence read the billing sweeps ask the ledger
// instead of reading gl_journals themselves (ruling 18 (a)): which of these idempotency keys already
// name a journal.

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestIntegration_JournalKeysExist_AnswersOnlyTheKeysThatNameAJournal(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	ensurePeriods(t, pool, depositReleaseYear)
	var periodID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM fiscal_periods WHERE year = $1 ORDER BY month LIMIT 1`, depositReleaseYear).Scan(&periodID); err != nil {
		t.Fatalf("a period to plant in: %v", err)
	}
	tag := time.Now().UnixNano()
	present := fmt.Sprintf("test.keys_exist:present:%d", tag)
	absent := fmt.Sprintf("test.keys_exist:absent:%d", tag)
	if _, err := pool.Exec(ctx, `
		INSERT INTO gl_journals (fiscal_year, entry_no, period_id, source, status, idempotency_key)
		VALUES ($1, 800000000 + (random()*90000000)::int, $2::uuid, 'INVOICE', 'DRAFT', $3)`,
		depositReleaseYear, periodID, present); err != nil {
		t.Fatalf("plant the journal: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM gl_journals WHERE idempotency_key = $1`, present)
	})

	svc := NewService(NewRepository(pool))
	got, err := svc.JournalKeysExist(ctx, []string{present, absent, ""})
	if err != nil {
		t.Fatalf("JournalKeysExist: %v", err)
	}
	if len(got) != 1 || got[0] != present {
		t.Fatalf("JournalKeysExist = %v, want only %q", got, present)
	}
	if none, err := svc.JournalKeysExist(ctx, nil); err != nil || len(none) != 0 {
		t.Fatalf("no keys must answer nothing, got %v, %v", none, err)
	}
}
