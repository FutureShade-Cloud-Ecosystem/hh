-- 0079: BisTrack live-sync persistence (DIB-53 phase 0 scaffold). Owner: internal/bistrack.
--
-- Plain-language intent. M9 keeps BisTrack and HardscapeOS reconciled through the 60-day parallel run
-- (the "rollback is free until November" guarantee): staff work native, a PUSH keeps BisTrack current,
-- and a PULL catches net-new BisTrack rows. This migration lands the three tables that persistence rests
-- on. It creates NO connection and runs NO sync — phase 0 is config-gated and structurally unable to
-- initiate one (the module holds only an unconfigured connector stub). Credentials arrive after the
-- first client call; the worker that fills the connector bodies is a later ticket.
--
-- Three tables:
--   sync_jobs         — one row per sync attempt (entity × direction), with a RESUMABLE composite cursor.
--   sync_discrepancies— what reconciliation could not line up (the point of the exercise, not an error log).
--   bistrack_id_map   — the BisTrack id <-> native uuid crosswalk; the identity spine that makes sync
--                       idempotent (pull) and addressable (push).
--
-- CURSOR IS COMPOSITE, NOT A BARE TIMESTAMP (cursor_ts + cursor_last_id). A watermark of "max updated_at
-- seen" silently SKIPS every row that shares that timestamp tick with the last row consumed — data loss,
-- not just mis-ordering (this program already paid for the bare-timestamp class once: DIB-191 audit H1,
-- two outbox rows sharing one tx timestamp with no tiebreaker). The next pull fetches strictly greater on
-- the (updated_at, bistrack_id) LEXICOGRAPHIC pair, so a tie is broken by id and never dropped.
--
-- bistrack_id_map IS UNIQUE IN BOTH DIRECTIONS. UNIQUE(entity_type, bistrack_id) makes pull idempotent
-- (one native per BisTrack id). UNIQUE(entity_type, native_id) is the push-side guard: push resolves a
-- native entity to its BisTrack id, so if a native entity ever maps to two BisTrack ids (e.g. merged
-- customers) the second upsert CONFLICTS instead of silently lying — the caller records that conflict as
-- a MAPPING_ERROR discrepancy, which is exactly what the discrepancy table is for.
--
-- Rollback: purely additive (three new tables, no cross-module DDL, no data). Dropping them is safe; the
-- module nil-seams when the pool is absent, so an unwired deploy is unaffected.

CREATE TABLE sync_jobs (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  entity_type    TEXT NOT NULL CHECK (entity_type IN ('customer','product','inventory','order')),
  direction      TEXT NOT NULL CHECK (direction IN ('pull','push')),
  status         TEXT NOT NULL DEFAULT 'PENDING'
                   CHECK (status IN ('PENDING','RUNNING','SUCCEEDED','FAILED','SKIPPED')),
  -- Resumable composite watermark (see header). Both NULL/'' on a job that has not advanced yet.
  cursor_ts      TIMESTAMPTZ,
  cursor_last_id TEXT NOT NULL DEFAULT '',
  rows_read      BIGINT NOT NULL DEFAULT 0,
  rows_written   BIGINT NOT NULL DEFAULT 0,
  discrepancies  BIGINT NOT NULL DEFAULT 0,
  error          TEXT NOT NULL DEFAULT '',
  scheduled_at   TIMESTAMPTZ,
  started_at     TIMESTAMPTZ,
  finished_at    TIMESTAMPTZ,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_sync_jobs_entity_created ON sync_jobs (entity_type, created_at DESC);

CREATE TABLE sync_discrepancies (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  sync_job_id  UUID REFERENCES sync_jobs(id) ON DELETE SET NULL,
  entity_type  TEXT NOT NULL CHECK (entity_type IN ('customer','product','inventory','order')),
  bistrack_id  TEXT NOT NULL DEFAULT '',
  native_id    UUID,
  kind         TEXT NOT NULL
                 CHECK (kind IN ('MISSING_IN_NATIVE','MISSING_IN_BISTRACK','FIELD_MISMATCH','UNMAPPED','MAPPING_ERROR')),
  detail       JSONB NOT NULL DEFAULT '{}',
  detected_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  resolved_at  TIMESTAMPTZ
);
-- Open (unresolved) discrepancies per entity, newest first — the observability read.
CREATE INDEX idx_sync_discrepancies_open ON sync_discrepancies (entity_type, detected_at DESC)
  WHERE resolved_at IS NULL;

CREATE TABLE bistrack_id_map (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  entity_type TEXT NOT NULL CHECK (entity_type IN ('customer','product','inventory','order')),
  bistrack_id TEXT NOT NULL,
  native_id   UUID NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (entity_type, bistrack_id), -- idempotent pull: one native per BisTrack id
  UNIQUE (entity_type, native_id)    -- push guard (C3): one BisTrack id per native; a many-to-one conflicts
);
