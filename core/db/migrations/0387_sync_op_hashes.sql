-- 0387_sync_op_hashes.sql
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
-- scope and the client key. The first write wins (the engine inserts ON CONFLICT DO NOTHING), so a
-- concurrent duplicate cannot rewrite the body a key is bound to. A key applied before this
-- migration has no row and answers as a plain replay; nothing is backfilled, because no stored
-- body exists to hash.
--
-- Additive: one new table, nothing read or changed by any running writer.

CREATE TABLE sync_op_hashes (
    org_id     UUID        NOT NULL,
    tenant_id  TEXT        NOT NULL DEFAULT '',
    scope      TEXT        NOT NULL,
    client_id  UUID        NOT NULL,
    body_hash  TEXT        NOT NULL CHECK (body_hash ~ '^[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, tenant_id, scope, client_id)
);
