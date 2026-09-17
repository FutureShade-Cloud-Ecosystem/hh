-- 0096: the job scheduler this ERP does not have. Owner: internal/jobs (Wave 2, Swarm B).
--
-- Plain-language intent: several things in HardscapeOS need to happen on a clock rather than in
-- response to a click — sweeping pick tasks that have sat in the yard queue overnight, and (later)
-- nightly reconciliations, retention passes and the BisTrack pull. Today each of those would have to
-- grow its own goroutine + ticker in main.go (the notify dispatcher, the messaging retention worker
-- and the approvals reconciler each already did exactly that). This migration lands the two tables a
-- single, shared, minimal cron-style runner needs, so the next scheduled thing is a registration
-- rather than another bespoke ticker.
--
-- ─────────────────────────────────────────────────────────────────────────────────────────────────
-- SHARED SHAPE, SEPARATE IMPLEMENTATION.
-- job_runs is deliberately SHAPE-COMPATIBLE with sync_jobs (migration 0079, owner internal/bistrack):
-- the same status vocabulary (PENDING/RUNNING/SUCCEEDED/FAILED/SKIPPED), the same rows_read /
-- rows_written / error / scheduled_at / started_at / finished_at run-accounting columns, and the same
-- "one row per attempt, never updated after it finishes" discipline. That is intentional so the
-- migration track's future BisTrack sync worker can be scheduled BY this runner and record its
-- attempts in the same shape it already records them in.
--
-- The shape is shared; the IMPLEMENTATION is not, and these tables are not sync_jobs. internal/jobs
-- neither reads nor writes sync_jobs, and internal/bistrack neither reads nor writes these tables.
-- Nothing here creates, drives or presumes a BisTrack connection. When the sync worker lands, wiring
-- it is a JobFunc registration in the composition root — no schema change on either side.
-- ─────────────────────────────────────────────────────────────────────────────────────────────────
--
-- ─────────────────────────────────────────────────────────────────────────────────────────────────
-- LIMITATION, STATED UP FRONT: SINGLE INSTANCE. NO LEADER ELECTION.
-- The runner claims a due schedule with one atomic conditional UPDATE (next_run_at <= now() → new
-- next_run_at, RETURNING), so two processes racing the same due window cannot both win the row. That
-- is single-FLIGHT, not leader election, and it is NOT the same as being safe to run N replicas:
--   * There is no lease and no fencing token. A run that is still executing when its next window
--     arrives will simply be STARTED AGAIN. Every registered job must therefore be idempotent and
--     short.
--   * A process that dies mid-run leaves its job_runs row in RUNNING forever. Nothing reaps it; it is
--     a tombstone you read, not a lock that blocks the next run.
--   * Schedules are evaluated in UTC. nightly_minute is minutes past midnight UTC, not local time,
--     so a nightly job does not follow Ontario's DST shift. Anything that must land at a wall-clock
--     local hour needs a timezone column first — deliberately not built.
-- Run ONE instance of the server with the runner enabled. Making this multi-instance safe means
-- advisory locks or a real leader election, and that is a separate, explicit piece of work.
-- ─────────────────────────────────────────────────────────────────────────────────────────────────
--
-- Conventions (matching 0073/0079): UUID v4 PKs; TIMESTAMPTZ everywhere; TEXT + CHECK for enums.
-- Rollback: purely additive (two new tables, no cross-module DDL). Dropping them is safe — the
-- runner nil-seams on an absent pool and simply never fires.

-- ── Schedules ───────────────────────────────────────────────────────────────────
-- One row per scheduled job. name is the registration key the Go side registers a JobFunc under; a
-- schedule whose name has no registered JobFunc is reported at boot and skipped (never silently
-- dropped), because a schedule with no code behind it is a wiring bug.
--
-- Two kinds only, deliberately — this is not a cron parser:
--   NIGHTLY  — fires once per day at nightly_minute (minutes past midnight UTC).
--   INTERVAL — fires every interval_seconds.
-- Anything needing "every second Tuesday" needs a real cron expression parser; that is out of scope
-- and the CHECK below makes the omission loud rather than silently accepting a cron string.
CREATE TABLE job_schedules (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name            TEXT NOT NULL UNIQUE,             -- registration key, e.g. 'yard.stale_pick_sweep'
  kind            TEXT NOT NULL CHECK (kind IN ('NIGHTLY','INTERVAL')),
  nightly_minute  INT CHECK (nightly_minute BETWEEN 0 AND 1439), -- minutes past midnight UTC (NIGHTLY)
  interval_secs   INT CHECK (interval_secs >= 60),               -- fire period (INTERVAL); floor 60s
  enabled         BOOLEAN NOT NULL DEFAULT TRUE,
  next_run_at     TIMESTAMPTZ,                      -- NULL = compute on first tick
  last_run_at     TIMESTAMPTZ,
  last_status     TEXT NOT NULL DEFAULT ''
                    CHECK (last_status IN ('','PENDING','RUNNING','SUCCEEDED','FAILED','SKIPPED')),
  last_error      TEXT NOT NULL DEFAULT '',
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- Each kind must carry exactly the parameter it needs, and not the other one. A NIGHTLY row with
  -- an interval (or an INTERVAL row with no period) is a misconfiguration that would otherwise fire
  -- at a time nobody chose.
  CONSTRAINT job_schedules_kind_params CHECK (
    (kind = 'NIGHTLY'  AND nightly_minute IS NOT NULL AND interval_secs IS NULL) OR
    (kind = 'INTERVAL' AND interval_secs  IS NOT NULL AND nightly_minute IS NULL)
  )
);
-- The runner's only hot read: which enabled schedules are due.
CREATE INDEX idx_job_schedules_due ON job_schedules(next_run_at) WHERE enabled;

-- ── Runs ────────────────────────────────────────────────────────────────────────
-- One row per attempt — the sync_jobs-shaped history described in the header. Written RUNNING when
-- the job starts and stamped exactly once when it ends; a finished row is never edited again.
CREATE TABLE job_runs (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  job_name     TEXT NOT NULL,
  schedule_id  UUID REFERENCES job_schedules(id) ON DELETE SET NULL,
  trigger      TEXT NOT NULL DEFAULT 'SCHEDULE' CHECK (trigger IN ('SCHEDULE','MANUAL')),
  status       TEXT NOT NULL DEFAULT 'PENDING'
                 CHECK (status IN ('PENDING','RUNNING','SUCCEEDED','FAILED','SKIPPED')),
  rows_read    BIGINT NOT NULL DEFAULT 0,
  rows_written BIGINT NOT NULL DEFAULT 0,
  error        TEXT NOT NULL DEFAULT '',
  detail       JSONB NOT NULL DEFAULT '{}',
  scheduled_at TIMESTAMPTZ,
  started_at   TIMESTAMPTZ,
  finished_at  TIMESTAMPTZ,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Observability read: one job's recent attempts, newest first.
CREATE INDEX idx_job_runs_name_created ON job_runs(job_name, created_at DESC);

-- ── The one real job wired at Wave 2 ────────────────────────────────────────────
-- The nightly stale-pick sweep (internal/tasks): pick tasks still QUEUED after the staleness window
-- get a chat EVENT nudge into their branch's yard channel so the morning shift sees yesterday's
-- leftovers instead of discovering them at the counter. It changes no task status — it only speaks.
-- 09:00 UTC ≈ 05:00 Eastern, i.e. before the yard opens.
INSERT INTO job_schedules (name, kind, nightly_minute, enabled)
VALUES ('yard.stale_pick_sweep', 'NIGHTLY', 540, TRUE);
