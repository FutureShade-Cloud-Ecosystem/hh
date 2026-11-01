//go:build integration

package sync_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// auditMark, row 0.54: the database clock now, taken before a leg writes. A leg's audit cleanup
// deletes only the rows of its entity types written at or after the mark (created_at is the
// transaction's start, never earlier than a clock_timestamp read before the leg began), so an
// older row of the same entity type, another run's or another package's, is left alone.
func auditMark(t *testing.T, pool *pgxpool.Pool) time.Time {
	t.Helper()
	var since time.Time
	if err := pool.QueryRow(context.Background(), `SELECT clock_timestamp()`).Scan(&since); err != nil {
		t.Fatalf("read the database clock: %v", err)
	}
	return since
}

// deleteOwnAudit deletes the audit_log rows of the entity types written since the mark. A refused
// delete fails the leg, naming the table.
func deleteOwnAudit(t *testing.T, pool *pgxpool.Pool, since time.Time, entityTypes ...string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`DELETE FROM audit_log WHERE entity_type = ANY($1::text[]) AND created_at >= $2`,
		entityTypes, since); err != nil {
		t.Errorf("clean the leg's audit_log rows: %v", err)
	}
}
