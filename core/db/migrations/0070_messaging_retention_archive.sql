-- 0070: MSG-01 — message retention policy + archive (Feature B). Archive-never-delete.
-- Owner: internal/messaging. Follows 0050 conventions. DECOUPLED from A-21 audit_log — this migration
-- does NOT touch audit_log; audit remains the sole writer of that table (the export endpoint only
-- READS msg_messages, it never writes audit_log).
--
-- Plain-language intent: keep messages "hot" (in msg_messages) for a bounded window — 12 months by
-- default — then ARCHIVE aged content into a summary table rather than deleting it, so an HR/audit
-- export can still account for what was said without the hot table growing forever. The retention
-- WORKER (internal/messaging/retention.go) reads the policy, summarizes aged messages per channel, and
-- writes idempotent archive rows.

-- ── Retention policy (how long a channel stays hot) ─────────────────────────────
-- One optional GLOBAL default row (channel_id NULL) + optional per-channel overrides. hot_retention_
-- months is the hot window; content older than now() - hot_retention_months is eligible to archive.
CREATE TABLE msg_retention_policy (
  id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  channel_id           UUID REFERENCES msg_channels(id) ON DELETE CASCADE,  -- NULL = the global default
  hot_retention_months INT NOT NULL DEFAULT 12 CHECK (hot_retention_months > 0),
  updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- At most ONE global-default row (channel_id IS NULL): the expression is always TRUE for those rows, so
-- the partial unique index caps them at one.
CREATE UNIQUE INDEX uq_msg_retention_global ON msg_retention_policy ((channel_id IS NULL)) WHERE channel_id IS NULL;
-- At most one override per channel.
CREATE UNIQUE INDEX uq_msg_retention_channel ON msg_retention_policy (channel_id) WHERE channel_id IS NOT NULL;

-- Seed the 12-month global default (fixed id + ON CONFLICT, 0052 style; safe to re-run).
INSERT INTO msg_retention_policy (id, channel_id, hot_retention_months) VALUES
  ('c0000000-0000-4000-9000-000000000001', NULL, 12)
ON CONFLICT (id) DO NOTHING;

-- ── Message archive (summarized aged content; NEVER a delete of the source) ──────
-- One row per (channel, archived_through) window. summary is a JSONB rollup (counts, span, authors) the
-- worker builds; message_count is the number of hot rows the window covered. archived_through is the
-- exclusive upper bound the worker has archived up to for the channel — re-running the same window is a
-- no-op via the unique index (idempotent archival).
CREATE TABLE msg_message_archive (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  channel_id       UUID NOT NULL REFERENCES msg_channels(id) ON DELETE CASCADE,
  archived_through TIMESTAMPTZ NOT NULL,
  summary          JSONB NOT NULL DEFAULT '{}',
  message_count    INT NOT NULL DEFAULT 0,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Idempotency: one archive row per channel per window boundary (re-run of the same cutoff conflicts).
CREATE UNIQUE INDEX uq_msg_message_archive_window ON msg_message_archive(channel_id, archived_through);
-- Read path: a channel's archive newest-first.
CREATE INDEX idx_msg_message_archive_channel ON msg_message_archive(channel_id, archived_through DESC);
