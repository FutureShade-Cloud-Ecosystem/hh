-- 0095: Yard pick → STAGE → shortage, on the shipped WMS-Lite tasks substrate (0073).
-- Owner: internal/tasks (Wave 2, Swarm B). Heritage: DIB-342 / DIB-343.
-- Plain-language intent: a yard picker pulls an order's material, then STAGES it (parks it in a
-- named spot with a stage reference the counter can call out), and — when the yard cannot fill a
-- line — flags a SHORTAGE that must reach the person at the counter who is standing in front of the
-- customer. Today the pick task can only go IN_PROGRESS → DONE, which collapses "picked" and
-- "handed over" into one event and gives the counter nowhere to read a shortage.
--
-- Two changes, both additive:
--   1. STAGED becomes a real state between IN_PROGRESS and DONE (the status CHECK is widened; the
--      old lane IN_PROGRESS → DONE is DELIBERATELY kept so a will-call pick that never gets parked
--      still closes, and so every task written before this migration remains legal).
--   2. task_shortages is the append-only shortage record. It is a TABLE, not a boolean on tasks,
--      because a pick can be short on more than one line, each with its own quantity, note and
--      resolution; and because "who reported it, when, and who cleared it" is exactly the kind of
--      fact that must not be overwritten by the next report.
--
-- Conventions (matching 0073): UUID v4 PKs; TIMESTAMPTZ; qty as NUMERIC(19,4); actor columns are
-- plain UUIDs that reference users(id) SEMANTICALLY but are not FK-joined (the tasks module is
-- decoupled and never JOINs identity). task_shortages DOES carry a real FK to tasks(id) because it
-- is owned by, and meaningless without, its task — the same call 0073 made for task_events.

-- ── 1 · STAGED status + the stage stamp ─────────────────────────────────────────
-- The lifecycle after this migration:
--   QUEUED ─▶ PUSHED ─▶ ACCEPTED ─▶ IN_PROGRESS ─┬─▶ STAGED ─▶ DONE
--                                                └─▶ DONE            (unchanged legacy lane)
--   any non-terminal ─▶ CANCELLED
ALTER TABLE tasks DROP CONSTRAINT tasks_status_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_status_check
  CHECK (status IN ('QUEUED','PUSHED','ACCEPTED','IN_PROGRESS','STAGED','DONE','CANCELLED'));

-- staged_at / stage_location / stage_ref are written together on the IN_PROGRESS → STAGED move.
-- stage_location is the physical spot ("Scrubname0002 3", "North fence") the counter reads out loud; stage_ref
-- is the short human handle the counter and the yard say to each other over the radio.
ALTER TABLE tasks ADD COLUMN staged_at      TIMESTAMPTZ;
ALTER TABLE tasks ADD COLUMN stage_location TEXT;
ALTER TABLE tasks ADD COLUMN stage_ref      TEXT;

-- Yard board read path: everything currently parked in a branch, oldest first (a staged pick that
-- has sat all afternoon is the one the counter needs chased).
CREATE INDEX idx_tasks_staged ON tasks(branch_id, staged_at) WHERE status = 'STAGED';

-- ── 2 · Shortages ───────────────────────────────────────────────────────────────
-- One row per reported shortage on a pick. Append-only in spirit: the only column a later write
-- touches is the resolution stamp (resolved_at / resolved_by / resolution), so the original report —
-- what was short, by how much, who said so — is never edited.
--
-- product_id is nullable and NOT FK-joined: the yard reports what it cannot find, and it may be an
-- item that is on the ticket by description only. qty_short is the shortfall in the line's own UOM
-- (NOT converted to base) because the number the picker types is the number the counter must repeat
-- to the customer.
CREATE TABLE task_shortages (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  task_id       UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  order_line_no INT,                                   -- NULL = shortage against the whole ticket
  product_id    UUID,                                  -- NULL = described-only item
  sku           TEXT NOT NULL DEFAULT '',
  description   TEXT NOT NULL,
  qty_short     NUMERIC(19,4) NOT NULL CHECK (qty_short > 0),
  uom           TEXT NOT NULL DEFAULT 'EA',
  note          TEXT NOT NULL DEFAULT '',
  reported_by   UUID,                                  -- NULL = system-reported
  reported_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  resolved_at   TIMESTAMPTZ,                           -- NULL = still open (the counter has not cleared it)
  resolved_by   UUID,
  resolution    TEXT NOT NULL DEFAULT ''               -- how the counter settled it (substituted, back-ordered, refunded…)
);
-- Primary read path: one task's shortages, oldest first (the pick detail panel).
CREATE INDEX idx_task_shortages_task ON task_shortages(task_id, reported_at);
-- The counter's open-shortage board: every unresolved shortage, newest first.
CREATE INDEX idx_task_shortages_open ON task_shortages(reported_at DESC) WHERE resolved_at IS NULL;
