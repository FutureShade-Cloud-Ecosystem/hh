package sync

import (
	"context"
	"errors"
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
	// tooSmall marks a pool of fewer than two connections: Lock refuses.
	tooSmall bool
}

// ErrLedgerPoolTooSmall is the refusal for a pool of fewer than two
// connections: a lock holder keeps one connection for the lock and needs a
// second for the lookup, the store and the remember, so with one connection
// every keyed push would wait for itself forever.
var ErrLedgerPoolTooSmall = errors.New("sync: the key ledger needs a pool of at least 2 connections (pool_max_conns)")

// RequireLedgerPool is the boot check for the process that serves pushes:
// it returns ErrLedgerPoolTooSmall for a pool the ledger cannot work over.
func RequireLedgerPool(pool *pgxpool.Pool) error {
	if conns := pool.Config().MaxConns; conns < 2 {
		return fmt.Errorf("%w, got %d", ErrLedgerPoolTooSmall, conns)
	}
	return nil
}

// NewPgKeyLedger returns the ledger over the core's pool. It does not panic
// on a pool of one connection (the composition tests build the production
// wiring over one on purpose, for flows that never push): Lock refuses with
// ErrLedgerPoolTooSmall at once, so a keyed push fails whole and loudly
// instead of hanging, and serve refuses such a pool at boot.
func NewPgKeyLedger(pool *pgxpool.Pool) *PgKeyLedger {
	n := 1
	tooSmall := false
	if pool != nil { // the composition tests wire with no pool
		if conns := int(pool.Config().MaxConns); conns < 2 {
			tooSmall = true
		} else {
			n = conns / 2
		}
	}
	return &PgKeyLedger{pool: pool, locks: make(chan struct{}, n), tooSmall: tooSmall}
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
	if l.tooSmall {
		return nil, ErrLedgerPoolTooSmall
	}
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
