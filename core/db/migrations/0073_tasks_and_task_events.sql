-- 0073: WMS-Lite — Chat→Order Tasks (PICK walking skeleton) + append-only transition audit.
-- Owner: internal/tasks. Feature: scopeBrief #5 buildableSpikeNow (DIB-191 family).
-- Plain-language intent: the ops floor needs a lightweight task queue so a chat message ("go pick
-- order 1234") becomes a tracked, claimable PICK task with a clean status lifecycle and a photo/DONE
-- capture — without standing up the full WMS. v1 is the PICK slice only; DISPATCH / NEED_WEIGHT /
-- PUSH-timer auto-assignment leave clean seams (the task_type / status / priority CHECKs already admit
-- their values) but no writer drives them yet.
--
-- Conventions (matching 0050 messaging + 0008 orders): UUID v4 PKs (gen_random_uuid()); TIMESTAMPTZ;
-- money/qty as NUMERIC(19,4); identity is the SoT — actor/user columns are plain UUIDs that reference
-- users(id) semantically but are NOT FK-joined here (the module is decoupled: it validates an order via
-- the order module's read seam, never a JOIN, so order_id/branch_id carry no FK). task_events IS the
-- history store for a task's transitions (the audit-recorder precedent, mirroring gl_period_overrides).

-- ── Tasks ───────────────────────────────────────────────────────────────────────
-- One unit of operational work bound to an order (+ optional line). task_type partitions the taxonomy;
-- v1 writes only PICK. status is the lifecycle state machine (enforced in the service, guarded on a
-- FOR UPDATE-locked row in the repository): QUEUED → PUSHED → ACCEPTED → IN_PROGRESS → DONE | CANCELLED.
-- created_by NULL = system-created (a future SYSTEM auto-creation path); a CHAT/MANUAL task carries the
-- staff caller. source records how the task was born (CHAT is the v1 manual-POST path that proves the
-- loop). photo_url / weight_entered / weight_uom are the DONE-capture + NEED_WEIGHT seams (weight is
-- written only when the gated NEED_WEIGHT round-trip lands).
CREATE TABLE tasks (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  task_type          TEXT NOT NULL DEFAULT 'PICK' CHECK (task_type IN ('PICK','DISPATCH','NEED_WEIGHT','OTHER')),
  order_id           UUID NOT NULL,               -- decoupled: validated via the order read seam, never FK-joined
  order_line_no      INT,                          -- NULL = whole-order task
  branch_id          UUID,                         -- owning yard (copied from the order at create time)
  priority           TEXT NOT NULL DEFAULT 'NORMAL' CHECK (priority IN ('LOW','NORMAL','HIGH','URGENT')),
  status             TEXT NOT NULL DEFAULT 'QUEUED' CHECK (status IN ('QUEUED','PUSHED','ACCEPTED','IN_PROGRESS','DONE','CANCELLED')),
  created_by         UUID,                          -- NULL = system
  assigned_to        UUID,                          -- targeted assignee (PUSH seam; unused in v1)
  claimed_by         UUID,                          -- who accepted the task (set on claim)
  claimed_at         TIMESTAMPTZ,
  pushed_at          TIMESTAMPTZ,                    -- PUSH-timer seam (unused in v1)
  accept_deadline_at TIMESTAMPTZ,                    -- PUSH-timer seam (unused in v1)
  done_at            TIMESTAMPTZ,
  photo_url          TEXT,                           -- DONE-capture proof
  weight_entered     NUMERIC(19,4),                  -- NEED_WEIGHT seam (unused in v1)
  weight_uom         TEXT,
  source             TEXT NOT NULL CHECK (source IN ('CHAT','SYSTEM','MANUAL')),
  source_message_id  UUID,                           -- the chat message that spawned a CHAT task (nullable)
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Picking queue read path: open tasks for a branch, highest-priority/oldest first (GET /tasks?status=&branch_id=).
CREATE INDEX idx_tasks_status_branch ON tasks(status, branch_id);
CREATE INDEX idx_tasks_branch ON tasks(branch_id);
-- All tasks for one order (the order-detail "work" panel, and idempotency checks on auto-creation later).
CREATE INDEX idx_tasks_order ON tasks(order_id);

-- ── Task events ─────────────────────────────────────────────────────────────────
-- Append-only transition audit: one row per real status change (and the birth row: from_status NULL →
-- to_status QUEUED). Written INSIDE the same tx as the tasks status write (the order.SetStatus / outbox
-- precedent), so a task can never move without its history edge. from_status NULL marks creation.
CREATE TABLE task_events (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  task_id     UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  from_status TEXT,                                  -- NULL = creation (no prior status)
  to_status   TEXT NOT NULL,
  actor_id    UUID,                                  -- NULL = system transition
  note        TEXT NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Primary read path: one task's transition timeline, oldest-first.
CREATE INDEX idx_task_events_task ON task_events(task_id, created_at);
