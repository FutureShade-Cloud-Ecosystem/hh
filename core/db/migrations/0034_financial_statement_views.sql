-- 0034: W5 A-04 — financial statement read views. Owner module: internal/finstmt (reads these views ONLY).
-- DOCTRINE: every view filters gl_journals.status = 'POSTED'. DRAFT / PENDING_APPROVAL / VOID journals never
-- appear in a financial statement — the statements are the POSTED books, nothing else. Branch is taken from
-- the line (COALESCE to the journal header) so a per-line branch stamp segments correctly; NULL = unbranched.
-- Money is BIGINT cents throughout. These are pure reads (no policy) — a subtly-wrong view is fixed by the
-- integrator in the reserved 0036 (CREATE OR REPLACE VIEW), never by editing this file post-merge.

-- 1. Per-account, per-period, per-branch balance. GLBalanceCents(account, year, month, branch) reads this.
--    net_debit_cents = Σdebit − Σcredit (positive = debit-heavy); the natural-orientation amount is derived
--    from gl_accounts.normal_balance by the consumer.
CREATE VIEW v_gl_account_balances AS
SELECT
  a.code                                                   AS account_code,
  a.name                                                   AS account_name,
  a.account_type,
  a.normal_balance,
  fp.year                                                  AS fiscal_year,
  fp.month,
  COALESCE(l.branch_id, j.branch_id)                       AS branch_id,
  SUM(COALESCE(l.debit_cents, 0))                          AS debit_cents,
  SUM(COALESCE(l.credit_cents, 0))                         AS credit_cents,
  SUM(COALESCE(l.debit_cents, 0) - COALESCE(l.credit_cents, 0)) AS net_debit_cents
FROM gl_journals j
JOIN fiscal_periods fp   ON fp.id = j.period_id
JOIN gl_journal_lines l  ON l.journal_id = j.id
JOIN gl_accounts a       ON a.id = l.account_id
WHERE j.status = 'POSTED'
GROUP BY a.code, a.name, a.account_type, a.normal_balance, fp.year, fp.month, COALESCE(l.branch_id, j.branch_id);

-- 2. Trial balance — per account per fiscal year, total debits/credits + signed balance. Σ(balance) = 0 for
--    a period whose journals all balance (the double-entry invariant surfaces here).
CREATE VIEW v_trial_balance AS
SELECT
  a.code           AS account_code,
  a.name           AS account_name,
  a.account_type,
  a.normal_balance,
  fp.year          AS fiscal_year,
  SUM(COALESCE(l.debit_cents, 0))  AS total_debit_cents,
  SUM(COALESCE(l.credit_cents, 0)) AS total_credit_cents,
  SUM(COALESCE(l.debit_cents, 0) - COALESCE(l.credit_cents, 0)) AS balance_cents
FROM gl_accounts a
JOIN gl_journal_lines l ON l.account_id = a.id
JOIN gl_journals j      ON j.id = l.journal_id AND j.status = 'POSTED'
JOIN fiscal_periods fp  ON fp.id = j.period_id
GROUP BY a.code, a.name, a.account_type, a.normal_balance, fp.year;

-- 3. Income statement — REVENUE + EXPENSE, natural-orientation amount per period/branch. Revenue is credit-
--    normal (Σcredit − Σdebit); expense is debit-normal (Σdebit − Σcredit). Net income = Σ(revenue) − Σ(expense).
CREATE VIEW v_income_statement AS
SELECT
  a.code           AS account_code,
  a.name           AS account_name,
  a.account_type,
  fp.year          AS fiscal_year,
  fp.month,
  COALESCE(l.branch_id, j.branch_id) AS branch_id,
  CASE a.account_type
    WHEN 'REVENUE' THEN SUM(COALESCE(l.credit_cents, 0) - COALESCE(l.debit_cents, 0))
    ELSE                SUM(COALESCE(l.debit_cents, 0) - COALESCE(l.credit_cents, 0))
  END              AS amount_cents
FROM gl_journals j
JOIN fiscal_periods fp  ON fp.id = j.period_id
JOIN gl_journal_lines l ON l.journal_id = j.id
JOIN gl_accounts a      ON a.id = l.account_id
WHERE j.status = 'POSTED' AND a.account_type IN ('REVENUE','EXPENSE')
GROUP BY a.code, a.name, a.account_type, fp.year, fp.month, COALESCE(l.branch_id, j.branch_id);

-- 4. Balance sheet — ASSET / LIABILITY / EQUITY, natural-orientation amount cumulative through a fiscal year.
--    ASSET is debit-normal; LIABILITY + EQUITY are credit-normal. The BS invariant (assets = liabilities +
--    equity + net income) is asserted by A-04, including the 2500/2510/2520 loan/lease liabilities.
CREATE VIEW v_balance_sheet AS
SELECT
  a.code           AS account_code,
  a.name           AS account_name,
  a.account_type,
  fp.year          AS fiscal_year,
  COALESCE(l.branch_id, j.branch_id) AS branch_id,
  CASE a.account_type
    WHEN 'ASSET' THEN SUM(COALESCE(l.debit_cents, 0) - COALESCE(l.credit_cents, 0))
    ELSE              SUM(COALESCE(l.credit_cents, 0) - COALESCE(l.debit_cents, 0))
  END              AS amount_cents
FROM gl_journals j
JOIN fiscal_periods fp  ON fp.id = j.period_id
JOIN gl_journal_lines l ON l.journal_id = j.id
JOIN gl_accounts a      ON a.id = l.account_id
WHERE j.status = 'POSTED' AND a.account_type IN ('ASSET','LIABILITY','EQUITY')
GROUP BY a.code, a.name, a.account_type, fp.year, COALESCE(l.branch_id, j.branch_id);

-- 5. GL detail — the drill-down target: one row per POSTED journal line, with its account, journal, date,
--    branch, and memo. A statement figure ties EXACTLY to the sum of the detail rows behind it.
CREATE VIEW v_gl_detail AS
SELECT
  l.id             AS line_id,
  j.id             AS journal_id,
  j.entry_no,
  j.fiscal_year,
  fp.year          AS period_year,
  fp.month         AS period_month,
  j.source,
  j.posted_at,
  a.code           AS account_code,
  a.name           AS account_name,
  a.account_type,
  COALESCE(l.branch_id, j.branch_id) AS branch_id,
  l.debit_cents,
  l.credit_cents,
  COALESCE(NULLIF(l.memo, ''), j.memo) AS memo
FROM gl_journals j
JOIN fiscal_periods fp  ON fp.id = j.period_id
JOIN gl_journal_lines l ON l.journal_id = j.id
JOIN gl_accounts a      ON a.id = l.account_id
WHERE j.status = 'POSTED';

-- 6. AR aging — UNION of open invoices (keyed by due_date) + non-invoice AR customer_transactions (NSF fees,
--    adjustments, credit memos, write-offs — keyed by created_at). The grand total ties to billing's
--    ARBalanceCents BY CONSTRUCTION (same two sources) — A-04 asserts the subledger tie-out. bucket is the
--    age band relative to CURRENT_DATE.
CREATE VIEW v_ar_aging AS
SELECT
  src.customer_id,
  src.source_type,
  src.source_ref_id,
  src.branch_id,
  src.amount_cents,
  src.age_date,
  GREATEST(CURRENT_DATE - src.age_date, 0) AS age_days,
  CASE
    WHEN CURRENT_DATE - src.age_date <= 0  THEN 'CURRENT'
    WHEN CURRENT_DATE - src.age_date <= 30 THEN '1_30'
    WHEN CURRENT_DATE - src.age_date <= 60 THEN '31_60'
    WHEN CURRENT_DATE - src.age_date <= 90 THEN '61_90'
    ELSE '90_PLUS'
  END AS bucket
FROM (
  -- open AR carried on invoices
  SELECT i.customer_id, 'invoice'::text AS source_type, i.id AS source_ref_id, i.branch_id,
         i.balance_cents AS amount_cents, i.due_date AS age_date
  FROM invoices i
  WHERE i.status IN ('FINALIZED','PARTIALLY_PAID') AND i.balance_cents <> 0
  UNION ALL
  -- non-invoice AR movements (kept out of the invoices table): NSF fee, adjustment, credit memo, write-off
  SELECT ct.customer_id, 'customer_txn'::text AS source_type, ct.id AS source_ref_id, ct.branch_id,
         ct.amount_cents, ct.created_at::date AS age_date
  FROM customer_transactions ct
  WHERE ct.txn_type IN ('NSF','ADJUSTMENT','CREDIT_MEMO','WRITE_OFF') AND ct.source_type <> 'invoice'
) src;

-- 7. Scheduled-report registry (registry only at W5 — no delivery yet; A-04 owns CRUD + validation).
CREATE TABLE finstmt_schedules (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  report      TEXT NOT NULL CHECK (report IN ('INCOME_STATEMENT','BALANCE_SHEET','TRIAL_BALANCE','GL_DETAIL','AR_AGING')),
  recipients  TEXT NOT NULL DEFAULT '',                   -- comma-separated emails; validated by A-04
  cadence     TEXT NOT NULL DEFAULT 'MONTHLY' CHECK (cadence IN ('DAILY','WEEKLY','MONTHLY')),
  branch_id   UUID REFERENCES locations(id) ON DELETE CASCADE,  -- NULL = consolidated
  is_active   BOOLEAN NOT NULL DEFAULT TRUE,
  created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_finstmt_schedules_report ON finstmt_schedules(report) WHERE is_active;
