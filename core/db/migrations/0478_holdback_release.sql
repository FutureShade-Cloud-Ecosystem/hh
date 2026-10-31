-- 0478_holdback_release.sql (wave 3 RT-3): the holdback release, one RELEASE row per invoice.
--
-- WHEN A HOLDBACK FALLS DUE (the Ontario Construction Act). A holdback is retained until the lien
-- preservation period has run out with no lien preserved. The period is the province rule's
-- preserve_days (60 for Ontario, holdback_province_rules) counted from the lien job's
-- substantial_perf_date (the date the certificate or declaration of substantial performance fixes),
-- else its last_supply_date (the completion of the supply, for a job with no such date). The last day
-- on which a lien can still be preserved is that date plus preserve_days; the holdback is releasable
-- from the NEXT house day, never on the last day itself, because a lien preserved late that evening is
-- still in time. A lien that was preserved and not discharged (lien_preserved_on set, lien_discharged_on
-- empty) blocks the release whatever the dates say. A job with neither date is never due.
--
-- WHAT A RELEASE MOVES. For each invoice the lien job holds: one RELEASE row in holdback_ledger carrying
-- the invoice id, the invoice's holdback_cents lowered to zero in the same call on the same transaction,
-- and one journal DR 1100 / CR 1150 for the amount (the customer now owes it). The invoice is NOT
-- re-issued and no new invoice is raised: the invoice already carries the whole amount in balance_cents
-- and the aging reads balance less holdback, so the net the customer owes simply grows by the released
-- amount, on the same invoice, the same due date, the same statement line. A new invoice would count the
-- amount twice in the balance. The journal rides the outbox in the release's own transaction (topic
-- ar.holdback.released), the way a write off's does, so it never lags the release it belongs to; the
-- rules below name its two accounts.
--
-- NOTE ON THE RT-3 NOTE IN 0470 (which is merged and cannot change). It says the today branch of every
-- reader reads the column and the past branch reads invoice_holdback_as_of. That is true of the aging
-- (credit_repository.go) and of the AR balance behind the 1100 tie (gl_seam.go) only. The statement's
-- HOLDBACK line and the finance charge read invoice_holdback_as_of for every date, today included. The
-- instruction in that note is unchanged and is what the release does: one RELEASE row per invoice and the
-- column lowered in the same call, so the readers of the column and the readers of the function agree.

-- RT-3 FIX ROUND, THE CONSERVATIVE DUE DAY (awaiting counsel's confirmation). The Act counts the period from
-- the PUBLICATION of the certificate of substantial performance (csp_published_on), which follows the date of
-- substantial performance, and for a supplier in some cases from the last supply. The period therefore ends
-- at the LATEST of the dates the job carries that can start it (publication, substantial performance, last
-- supply) plus preserve_days, so a release is never early. lien.preservationStart is the one place the rule
-- lives. A job with none of the three dates has no start and is refused.
--
-- 1. A preserved lien blocks the release. Recorded on the lien job (lien owns the table).
ALTER TABLE lien_jobs
  ADD COLUMN csp_published_on    DATE,
  ADD COLUMN lien_preserved_on   DATE,
  ADD COLUMN lien_preserved_note TEXT NOT NULL DEFAULT '',
  ADD COLUMN lien_discharged_on  DATE;
ALTER TABLE lien_jobs
  ADD CONSTRAINT lien_jobs_discharge_needs_preserve
  CHECK (lien_discharged_on IS NULL OR lien_preserved_on IS NOT NULL);

-- 2. A RELEASE row either carries the journal it posted (the job level release, 0038) or rides the
--    outbox: an invoice level release whose journal the GL consumer posts on the release's own key.
ALTER TABLE holdback_ledger
  ADD COLUMN journal_via_outbox BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE holdback_ledger DROP CONSTRAINT holdback_release_has_journal;
ALTER TABLE holdback_ledger ADD CONSTRAINT holdback_release_has_journal
  CHECK (entry_type <> 'RELEASE' OR gl_journal_id IS NOT NULL OR (journal_via_outbox AND invoice_id IS NOT NULL));

-- 3. An invoice is released once. The unique index is the backstop under the service's refusal. A release
--    reversed in error (reversed_at set by the Controller's reversal door) no longer counts, so the holdback
--    can be released again, correctly. The reversal restores the column and books a positive RETENTION row
--    beside the RELEASE row; the RELEASE row stays as history.
ALTER TABLE holdback_ledger ADD COLUMN reversed_at TIMESTAMPTZ;
CREATE UNIQUE INDEX uq_holdback_release_once_per_invoice
  ON holdback_ledger(invoice_id) WHERE entry_type = 'RELEASE' AND invoice_id IS NOT NULL AND reversed_at IS NULL;

-- 4. The release's idempotency key reads by its reason.
CREATE INDEX idx_holdback_ledger_reason_release
  ON holdback_ledger(reason) WHERE entry_type = 'RELEASE';

-- 5. The release's journal: DR the receivable (1100), CR the holdback receivable (1150).
INSERT INTO posting_rules (event_type, line_role, account_code) VALUES
    ('ar.holdback.released', 'ar',       '1100'),
    ('ar.holdback.released', 'holdback', '1150'),
    ('ar.holdback.release_reversed', 'ar',       '1100'),
    ('ar.holdback.release_reversed', 'holdback', '1150')
ON CONFLICT (event_type, line_role) WHERE branch_id IS NULL DO NOTHING;

-- 6. A released part falls due on its release day (RT-3 fix round). A holdback release brings the held part
--    into the invoice's net; that part is due on the RELEASE row's house day (released_at) plus the invoice's
--    own terms, never before the invoice's own due date, not on the invoice's original due date. These two
--    lien-owned functions answer the released part and its release day as of an instant, so the aging, the
--    finance charge and the statement read one dated source and no reader rebuilds it by hand. An invoice is
--    released once (the unique index), so the day is the one RELEASE row's.
CREATE OR REPLACE FUNCTION invoice_released_cents_as_of(p_invoice_id uuid, p_as_of timestamptz) RETURNS bigint
LANGUAGE sql STABLE AS $$
  SELECT COALESCE(SUM(l.amount_cents), 0)::bigint
    FROM holdback_ledger l
   WHERE l.invoice_id = p_invoice_id AND l.entry_type = 'RELEASE' AND l.created_at <= p_as_of
     AND (l.reversed_at IS NULL OR l.reversed_at > p_as_of)
$$;

CREATE OR REPLACE FUNCTION invoice_released_on(p_invoice_id uuid, p_as_of timestamptz) RETURNS date
LANGUAGE sql STABLE AS $$
  SELECT MAX(l.released_at)
    FROM holdback_ledger l
   WHERE l.invoice_id = p_invoice_id AND l.entry_type = 'RELEASE' AND l.created_at <= p_as_of
     AND (l.reversed_at IS NULL OR l.reversed_at > p_as_of)
$$;
