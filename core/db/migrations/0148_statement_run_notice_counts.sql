-- 0148: statement-run NOTICE accounting — making the HOS_STATEMENT_NOTICES flip an informed act.
--
-- W5-C wired the statement notification seam FLAG-OFF and stated the two independent switches. What
-- it could not answer is the only question an operator actually asks before flipping the first one:
--
--     "if I turn this on tonight, how many text messages go out, and to whom would they NOT go?"
--
-- 0105 gave the run two counters — generated_count and queued_count — and they answer a different
-- question. queued_count is "how many statements were marked QUEUED in ar_statements", i.e. how many
-- accounts were SENDABLE. Whether a NOTICE was actually handed to the outbox depends on the flag,
-- and with the flag off the two numbers are identical while zero notices exist. An operator reading
-- that screen today cannot tell the difference between "the wiring works and is switched off" and
-- "the wiring is broken".
--
-- Three columns, and each one is a distinct fact:
--
--   notices_enabled           was the outbox bound for THIS run? A boolean stamped at run time, not
--                             read from the environment at display time — a run is a historical
--                             record, and re-reading the flag later would rewrite what happened.
--   notice_queued_count       notices actually enqueued (rows handed to the SMS outbox). Flag off ⇒
--                             always 0, and that 0 is now a MEASURED zero rather than an absence.
--   notice_suppressed_count   statements that WERE sendable but produced no notice, because the
--                             outbox was unbound. This is the number the operator is really asking
--                             for: it is exactly how many messages the flip would send.
--
-- The invariant, stated once here and asserted in billing's run accounting:
--
--     notice_queued_count + notice_suppressed_count = queued_count
--
-- ── WHY NOT DERIVE IT FROM sms_outbox ─────────────────────────────────────────────────────────
-- Counting rows on the messaging side would (a) make billing read another module's table, and (b)
-- give the wrong answer the moment the outbox is drained, purged or re-keyed. The run's own
-- accounting must be complete at FinishRun and never depend on a downstream table's retention.

BEGIN;

ALTER TABLE ar_statement_runs
    ADD COLUMN IF NOT EXISTS notices_enabled         BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS notice_queued_count     INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS notice_suppressed_count INT NOT NULL DEFAULT 0;

ALTER TABLE ar_statement_runs
    DROP CONSTRAINT IF EXISTS ck_ar_statement_runs_notice_counts_nonneg;
ALTER TABLE ar_statement_runs
    ADD CONSTRAINT ck_ar_statement_runs_notice_counts_nonneg
        CHECK (notice_queued_count >= 0 AND notice_suppressed_count >= 0);

-- A run with the outbox unbound cannot have queued a notice. The converse is NOT constrained: a run
-- with the outbox bound may still suppress (a statement with no customer id is refused by the
-- producer), so notices_enabled = TRUE with a non-zero suppressed count is a legitimate state.
ALTER TABLE ar_statement_runs
    DROP CONSTRAINT IF EXISTS ck_ar_statement_runs_notices_off_queues_none;
ALTER TABLE ar_statement_runs
    ADD CONSTRAINT ck_ar_statement_runs_notices_off_queues_none
        CHECK (notices_enabled OR notice_queued_count = 0);

COMMENT ON COLUMN ar_statement_runs.notice_suppressed_count IS
    'Scrubname0029 that were sendable but produced no notification because the outbox was unbound. '
    'This is the count an operator reads BEFORE flipping HOS_STATEMENT_NOTICES: it is exactly how '
    'many messages the flip would have sent for this run.';

COMMIT;
