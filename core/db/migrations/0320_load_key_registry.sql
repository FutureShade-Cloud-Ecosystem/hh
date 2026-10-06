-- 0320_load_key_registry.sql
-- Owner module: internal/load (HH-LOAD-1 item LD-1, the key registry and
-- the load frame). First migration of this cycle's block, 0320 to 0359,
-- assigned by the principal.
--
-- WHAT THE LOAD DOORS STAND ON. Every record that enters HH through a
-- load door is named by four parts (the plan's section 2 rule): the org,
-- the source system, the kind, and the legacy id. Two tables:
--
--   load_key_registry  the name to row crosswalk. A name maps to exactly
--                      one HH row and an HH row carries at most one name
--                      per source system and kind: unique in BOTH
--                      directions, the same double uniqueness
--                      bistrack_id_map carries (0079), generalised to
--                      every source system and kind. The registry insert
--                      is the load's lock: two loads racing on one name
--                      contend on the name's unique index and one row
--                      survives. The legacy id is opaque bounded text in
--                      the canonical form the caller's ERP pack defines;
--                      the content hash is the hex sha256 of the
--                      normalised record, the replay decision's input.
--                      No foreign key on hh_id: the row lives in the
--                      owning module's table and one registry serves
--                      every kind, the 0079 crosswalk posture.
--
--   load_batch         one row per submitted batch: the caller, the
--                      kind, the counts by outcome, the state, and the
--                      per item outcomes. A replayed batch id returns
--                      what it first recorded, so the outcomes ride in
--                      the row.
--
-- WHY NOT bistrack_id_map. That table holds one source system, four
-- kinds and no org column (0079), and its module stays the fail-closed
-- stub D43 keeps closed. This registry serves every ERP pack.
--
-- ADDITIVE, BOTH TABLES NEW. No existing table is touched, no carried
-- row changes shape. The org id carries no foreign key for the same
-- reason the 0213 columns carry none at rest: the anchor table's
-- constraint story belongs to the tenancy pass, and the registry's
-- reads are always org-scoped predicates.
--
-- kind and source_system are bounded text, deliberately not enumerated:
-- the vocabulary is the doors' (units, categories, customers, prices,
-- and the packs after this one), and a check here would make every new
-- door a migration. The state check carries the batch lifecycle's two
-- recorded values; a door that needs an in-flight state adds it in its
-- own migration.

CREATE TABLE load_key_registry (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        UUID NOT NULL,
    source_system TEXT NOT NULL CHECK (char_length(source_system) BETWEEN 1 AND 64),
    kind          TEXT NOT NULL CHECK (char_length(kind) BETWEEN 1 AND 64),
    legacy_id     TEXT NOT NULL CHECK (char_length(legacy_id) BETWEEN 1 AND 512),
    hh_id         UUID NOT NULL,
    content_hash  TEXT NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    last_batch_id TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, source_system, kind, legacy_id),
    UNIQUE (org_id, source_system, kind, hh_id)
);

COMMENT ON TABLE load_key_registry IS
  'The load doors'' name to row crosswalk: one HH row per (org, source system, kind, legacy id), unique both directions. The insert is the load''s lock.';

CREATE TABLE load_batch (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        UUID NOT NULL,
    source_system TEXT NOT NULL CHECK (char_length(source_system) BETWEEN 1 AND 64),
    batch_id      TEXT NOT NULL CHECK (char_length(batch_id) BETWEEN 1 AND 128),
    caller        TEXT NOT NULL DEFAULT '',
    kind          TEXT NOT NULL CHECK (char_length(kind) BETWEEN 1 AND 64),
    state         TEXT NOT NULL DEFAULT 'RECORDED' CHECK (state IN ('RECORDED')),
    counts        JSONB NOT NULL DEFAULT '{}',
    items         JSONB NOT NULL DEFAULT '[]',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, source_system, batch_id)
);

-- The batch read resolves by the caller's own batch id inside the org,
-- without naming the source system.
CREATE INDEX idx_load_batch_org_batch ON load_batch (org_id, batch_id);

COMMENT ON TABLE load_batch IS
  'One row per submitted load batch: the caller, the kind, the counts by outcome, the per item outcomes, and the state. A replayed batch id returns what it first recorded.';
