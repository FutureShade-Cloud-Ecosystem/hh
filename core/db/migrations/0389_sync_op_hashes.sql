-- 0389_sync_op_hashes.sql
-- Owner module: internal/sync (the offline sync engine's idempotency body ledger).
--
-- The engine's fourth guarantee (plan B.3): the same idempotency key with a different body is
-- refused. Each owning module stamps the key on its own row (the D30 idempotency_key columns),
-- and those rows hold the key and nothing of the body, so the engine keeps the one thing the
-- modules cannot: per applied key, the SHA-256 of the body that first applied under it
-- (core/internal/sync/keyledger.go documents the exact canonical form).
--
-- One row per applied key, scoped as the engine scopes every call: the org, the tenant for a
-- tenant caller (empty for staff, so a staff key and a tenant key never share a slot), the sync
-- scope and the client key. entity_id is the row the key applied to (the created row's id for a
-- create), so a key the owning module no longer dedups can still be answered from that row. The
-- first write wins (the engine inserts ON CONFLICT DO NOTHING) and the engine serializes find,
-- apply and remember per slot with a transaction scoped advisory lock, so a concurrent push of
-- the same key with another body waits and then finds this row. A key applied before this
-- migration has no row: while its module still dedups it, it answers as a plain replay, and once
-- the row has moved on it applies again, as it did before this table. Nothing is backfilled,
-- because no stored body exists to hash.
--
-- Additive: one new table, nothing read or changed by any running writer.

CREATE TABLE sync_op_hashes (
    org_id     UUID        NOT NULL,
    tenant_id  TEXT        NOT NULL DEFAULT '',
    scope      TEXT        NOT NULL,
    client_id  UUID        NOT NULL,
    body_hash  TEXT        NOT NULL CHECK (body_hash ~ '^[0-9a-f]{64}$'),
    entity_id  TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, tenant_id, scope, client_id)
);
