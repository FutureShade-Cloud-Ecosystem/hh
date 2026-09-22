package staffcontext

// tx_fake_test.go: a no-op pgx.Tx for the DB-free unit tests. The atomic decide (0075) threads a
// pgx.Tx through SaveDecisionTx + JEDecider.ApproveJournalTx; the fakes ignore the tx (they record
// state in memory), so this stub only needs to satisfy the interface and let Commit/Rollback succeed.
// The REAL transactional semantics (rollback-on-GL-failure, decider serialization) are covered by
// the integration suite against live Postgres, not here.

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeTx struct {
	committed  bool
	rolledBack bool
}

func (t *fakeTx) Commit(context.Context) error   { t.committed = true; return nil }
func (t *fakeTx) Rollback(context.Context) error { t.rolledBack = true; return nil }

func (t *fakeTx) Begin(context.Context) (pgx.Tx, error) { return t, nil }
func (t *fakeTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (t *fakeTx) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, nil }
func (t *fakeTx) QueryRow(context.Context, string, ...any) pgx.Row        { return nil }
func (t *fakeTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (t *fakeTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (t *fakeTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }
func (t *fakeTx) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (t *fakeTx) Conn() *pgx.Conn                                        { return nil }
