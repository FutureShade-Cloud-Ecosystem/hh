//go:build integration

// quote_floor_tx_integration_test.go, the transactional half of the quote
// floor seam's record (plan 4.2): AppendAuditTx is the same INSERT as
// AppendAudit executed on a caller-supplied transaction, so the pricing log
// row a quote write owes commits with that write and rolls back with it.
package pricing

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// qd2TxEntity is the row's entity_id: pricing_audit_log.entity_id is a NOT
// NULL UUID, and this test is about the transaction, not the reference, so
// a fixed UUID-shaped literal no real product carries does the naming.
const qd2TxEntity = "12345678-1234-4123-8123-123456789abc"

func TestAppendAuditTx_RidesTheTransaction(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := NewService(NewRepository(pool))
	rec := FloorRecord{
		QuoteID:       "11111111-2222-4222-8222-333333333333",
		ProductID:     qd2TxEntity,
		RuleUnitCents: 1000, NetUnitCents: 900, DiscPct: 10,
		MarginBps: 4444, FloorBps: 1200, Action: "UPDATE",
	}
	cleanup := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM pricing_audit_log WHERE entity_id = $1`, qd2TxEntity)
	}
	cleanup()
	t.Cleanup(cleanup)

	// A rolled back transaction leaves no row.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := svc.RecordFloorDecisionTx(ctx, tx, rec); err != nil {
		t.Fatalf("the append must ride the transaction: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if n := qd2TxRowCount(t, pool); n != 0 {
		t.Fatalf("a rolled back append must leave no pricing log row, got %d", n)
	}

	// A committed transaction keeps it.
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := svc.RecordFloorDecisionTx(ctx, tx, rec); err != nil {
		t.Fatalf("the append must ride the transaction: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if n := qd2TxRowCount(t, pool); n != 1 {
		t.Fatalf("a committed append must leave exactly one row, got %d", n)
	}
}

// qd2TxRowCount counts this test's rows.
func qd2TxRowCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM pricing_audit_log WHERE entity_id = $1`, qd2TxEntity).Scan(&n); err != nil {
		t.Fatalf("count pricing log rows: %v", err)
	}
	return n
}
