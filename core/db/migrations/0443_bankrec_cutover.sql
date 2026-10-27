-- 0443: bank_accounts stores the reconciliation starting point (cut-over, FW1-04 round 2).
--
-- The Financial Controller records a cut-over date and the opening statement balance at that date, accepted as
-- reconciled to the book, with an optional confirmed list of the ledger entries on or before that date that are
-- still outstanding at cut-over. Entries on or before the cut-over date that are not on that list are never
-- outstanding items.
ALTER TABLE bank_accounts
    ADD COLUMN IF NOT EXISTS cutover_date DATE,
    ADD COLUMN IF NOT EXISTS cutover_statement_balance_cents BIGINT,
    ADD COLUMN IF NOT EXISTS cutover_outstanding_journal_ids UUID[] NOT NULL DEFAULT '{}'::uuid[],
    ADD COLUMN IF NOT EXISTS cutover_recorded_by UUID REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS cutover_recorded_at TIMESTAMPTZ;
