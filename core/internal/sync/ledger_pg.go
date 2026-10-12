package sync

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PgKeyLedger is the Postgres KeyLedger over sync_op_hashes (migration
// 0387). The table is this package's own: the engine is the only reader and
// the only writer.
type PgKeyLedger struct {
	pool *pgxpool.Pool
}

// NewPgKeyLedger returns the ledger over the core's pool.
func NewPgKeyLedger(pool *pgxpool.Pool) *PgKeyLedger { return &PgKeyLedger{pool: pool} }

// tenantSlot is the tenant leg of the primary key: the token's tenant for
// a tenant caller, the empty string for staff, so a staff key and a tenant
// key never share a slot.
func tenantSlot(f ScopeFilter) string {
	if f.Tenant {
		return f.TenantID
	}
	return ""
}

// BodyHash reads the stored hash for one key.
func (l *PgKeyLedger) BodyHash(ctx context.Context, f ScopeFilter, scope, key string) (string, bool, error) {
	var hash string
	rows, err := l.pool.Query(ctx,
		`SELECT body_hash FROM sync_op_hashes
		  WHERE org_id = $1::uuid AND tenant_id = $2 AND scope = $3 AND client_id = $4::uuid`,
		f.OrgID, tenantSlot(f), scope, key)
	if err != nil {
		return "", false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return "", false, rows.Err()
	}
	if err := rows.Scan(&hash); err != nil {
		return "", false, err
	}
	return hash, true, nil
}

// Remember stores the hash; the first write wins and a second changes
// nothing, so a concurrent duplicate cannot rewrite the body a key is
// bound to.
func (l *PgKeyLedger) Remember(ctx context.Context, f ScopeFilter, scope, key, hash string) error {
	_, err := l.pool.Exec(ctx,
		`INSERT INTO sync_op_hashes (org_id, tenant_id, scope, client_id, body_hash)
		 VALUES ($1::uuid, $2, $3, $4::uuid, $5)
		 ON CONFLICT (org_id, tenant_id, scope, client_id) DO NOTHING`,
		f.OrgID, tenantSlot(f), scope, key, hash)
	return err
}
