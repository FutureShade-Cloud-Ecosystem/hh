package sync

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PgKeyLedger is the Postgres KeyLedger over sync_op_hashes (migration
// 0387). The table is this package's own: the engine is the only reader and
// the only writer.
type PgKeyLedger struct {
	pool *pgxpool.Pool
	// locks bounds the connections held by Lock to half the pool, so the
	// stores always find a connection to apply with while others wait on a
	// slot (a lock holder that needed a second connection from a pool full
	// of lock holders would wait forever).
	locks chan struct{}
}

// NewPgKeyLedger returns the ledger over the core's pool.
func NewPgKeyLedger(pool *pgxpool.Pool) *PgKeyLedger {
	n := int(pool.Config().MaxConns) / 2
	if n < 1 {
		n = 1
	}
	return &PgKeyLedger{pool: pool, locks: make(chan struct{}, n)}
}

// tenantSlot is the tenant leg of the primary key: the token's tenant for
// a tenant caller, the empty string for staff, so a staff key and a tenant
// key never share a slot.
func tenantSlot(f ScopeFilter) string {
	if f.Tenant {
		return f.TenantID
	}
	return ""
}

// Lock takes a transaction scoped advisory lock keyed on a hash of the
// slot, held on one connection until release: Postgres drops it with the
// transaction, so a crashed holder frees it. Two pushes of one slot, in this
// process or another, run find, apply and remember one after the other.
func (l *PgKeyLedger) Lock(ctx context.Context, f ScopeFilter, scope, key string) (func(), error) {
	select {
	case l.locks <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	tx, err := l.pool.Begin(ctx)
	if err != nil {
		<-l.locks
		return nil, err
	}
	slot := fmt.Sprintf("sync_op_hashes|%s|%s|%s|%s", f.OrgID, tenantSlot(f), scope, key)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, slot); err != nil {
		_ = tx.Rollback(context.Background())
		<-l.locks
		return nil, err
	}
	return func() {
		_ = tx.Rollback(context.Background())
		<-l.locks
	}, nil
}

// Lookup reads the stored entry for one key.
func (l *PgKeyLedger) Lookup(ctx context.Context, f ScopeFilter, scope, key string) (LedgerEntry, bool, error) {
	var e LedgerEntry
	rows, err := l.pool.Query(ctx,
		`SELECT body_hash, entity_id FROM sync_op_hashes
		  WHERE org_id = $1::uuid AND tenant_id = $2 AND scope = $3 AND client_id = $4::uuid`,
		f.OrgID, tenantSlot(f), scope, key)
	if err != nil {
		return LedgerEntry{}, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return LedgerEntry{}, false, rows.Err()
	}
	if err := rows.Scan(&e.Hash, &e.EntityID); err != nil {
		return LedgerEntry{}, false, err
	}
	return e, true, nil
}

// Remember stores the entry; the first write wins and a second changes
// nothing. The engine calls it under Lock, so there is no second writer to
// race; the conflict clause only keeps a direct caller honest.
func (l *PgKeyLedger) Remember(ctx context.Context, f ScopeFilter, scope, key string, e LedgerEntry) error {
	_, err := l.pool.Exec(ctx,
		`INSERT INTO sync_op_hashes (org_id, tenant_id, scope, client_id, body_hash, entity_id)
		 VALUES ($1::uuid, $2, $3, $4::uuid, $5, $6)
		 ON CONFLICT (org_id, tenant_id, scope, client_id) DO NOTHING`,
		f.OrgID, tenantSlot(f), scope, key, e.Hash, e.EntityID)
	return err
}
