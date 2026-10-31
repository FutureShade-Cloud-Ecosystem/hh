-- 0476: the AP revaluation's run record and its unrealised FX account (wave 3, plan row FX-2b).
-- Owner: internal/purchasing (the table), internal/gl (the account and rule rows, seeded here the way
-- 0462 and 0473 seed theirs). Additive, non-breaking, forward only.
--
-- FX-2 (the month end revaluation of open USD AP) kept its run state on the run's GL journal, so a
-- run whose difference was zero left no trace and a second press at another rate posted a real
-- journal for a month that had already been run. This migration gives every run a record, and the
-- guards that were probing journal keys read the record instead.
--
--   1. THE RUN RECORD: one row per branch and house month, whether the run posted a journal or
--      not. A zero run is a complete run: its row has no journal, and the unique key refuses a
--      second press at another rate as already run. The figures the run was judged on, the rate,
--      who pressed it and when, and, once the reversal posts, the reversal's journal and who and
--      when. The journal ids carry no foreign key: the GL owns its journals and the module reads
--      them only through the GL seam.
--
--   2. THE UNREALISED FX ACCOUNT: 6315 beside 6310 Realised Foreign Exchange Gain or Loss, and the
--      default vendor.bill.posted / fx_unrealised rule pointing at it, so the revaluation leg no
--      longer shares the realised account. A dealer redirects it with a rule row, no code change.
--
--   3. THE REVERSAL SCHEDULE: the worker job that reverses a month's run on the next house day.
--      It never presses a run (the rate is a Controller's input); it only takes back the entry a
--      Controller pressed, so it is seeded on.

-- 1. The run record.
CREATE TABLE IF NOT EXISTS ap_revaluation_runs (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- A revaluation is one branch's books.
    branch_id            UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,

    -- The house month revalued, YYYY-MM.
    month                TEXT NOT NULL,

    -- The month end rate (CAD per USD) the Controller entered.
    fx_rate              NUMERIC(12, 6) NOT NULL CHECK (fx_rate > 0),

    -- What the run measured, all integer cents: the USD bills counted, their open document
    -- amount, the CAD they carry in 2100, the CAD they come to at the rate, and the difference.
    bills                INTEGER NOT NULL CHECK (bills > 0),
    doc_open_cents       BIGINT NOT NULL,
    carried_cad_cents    BIGINT NOT NULL,
    revalued_cad_cents   BIGINT NOT NULL,
    difference_cents     BIGINT NOT NULL,

    -- The run's journal. NULL exactly when the difference is zero: a zero run is recorded with no
    -- journal and needs no reversal.
    journal_id           UUID,

    -- Who pressed the run (the caller's identity as the service saw it) and when.
    created_by           TEXT NOT NULL DEFAULT '',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- The reversal: its journal, who (a person, or the worker's system identity) and when.
    reversal_journal_id  UUID,
    reversed_by          TEXT NOT NULL DEFAULT '',
    reversed_at          TIMESTAMPTZ,

    CONSTRAINT ap_revaluation_runs_month_shape CHECK (month ~ '^[0-9]{4}-[0-9]{2}$'),
    CONSTRAINT ap_revaluation_runs_zero_has_no_journal
        CHECK ((difference_cents = 0) = (journal_id IS NULL)),
    CONSTRAINT ap_revaluation_runs_reversal_needs_run
        CHECK (reversal_journal_id IS NULL OR journal_id IS NOT NULL),
    CONSTRAINT ap_revaluation_runs_reversal_stamped
        CHECK ((reversal_journal_id IS NULL) = (reversed_at IS NULL)),

    -- THE KEY: one run per branch and house month, whatever its difference and whatever rate a
    -- later press brings.
    CONSTRAINT ap_revaluation_runs_once UNIQUE (branch_id, month)
);

-- The guards ask "any earlier unreversed run" and "any later posted run" of one branch.
CREATE INDEX IF NOT EXISTS idx_ap_revaluation_runs_open
    ON ap_revaluation_runs (branch_id, month)
    WHERE journal_id IS NOT NULL AND reversal_journal_id IS NULL;

-- 2. The unrealised FX account and its default rule.
INSERT INTO gl_accounts (code, name, account_type, normal_balance)
VALUES ('6315', 'Unrealised Foreign Exchange Gain or Loss', 'EXPENSE', 'DEBIT')
ON CONFLICT (code) DO NOTHING;

INSERT INTO posting_rules (event_type, line_role, account_code)
VALUES ('vendor.bill.posted', 'fx_unrealised', '6315')
ON CONFLICT (event_type, line_role) WHERE branch_id IS NULL DO NOTHING;

DO $$
DECLARE
    v_bad TEXT;
BEGIN
    SELECT string_agg(pr.event_type || '/' || pr.line_role || ' -> ' || pr.account_code, ', ')
      INTO v_bad
      FROM posting_rules pr
      LEFT JOIN gl_accounts a ON a.code = pr.account_code AND a.is_active
     WHERE pr.event_type = 'vendor.bill.posted'
       AND pr.line_role = 'fx_unrealised'
       AND pr.branch_id IS NULL
       AND a.id IS NULL;
    IF v_bad IS NOT NULL THEN
        RAISE EXCEPTION '0476: the fx_unrealised rule does not resolve to an active account: %', v_bad;
    END IF;
END $$;

-- 3. The reversal job's schedule: 09:00 UTC, after every North American house midnight has passed.
INSERT INTO job_schedules (name, kind, nightly_minute, enabled)
VALUES ('purchasing.ap_revaluation_reversal', 'NIGHTLY', 540, TRUE)
ON CONFLICT (name) DO NOTHING;
