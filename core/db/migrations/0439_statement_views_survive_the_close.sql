-- 0439 (FW1-09b, migration named by the principal, FROM-PRINCIPAL 106): the
-- statement views survive the year end close. Round 4 (S3's P2) adds the
-- trial balance's balance accounts reading inception to date, so the balance
-- sheet ties to the trial balance from the second year on.
--
-- 1. THE CLOSED YEAR'S INCOME STATEMENT READS TRUE. The GL-02 blocker: the CLOSING journals'
--    reversing legs sat inside the P&L, so after the close the year's income statement (and its last
--    month's) read zero for every account, and the annual P&L the T2 return needs was gone. The view
--    leaves CLOSING source journals out; the P&L is the trading year, the closing entry is the act
--    that ends it, not a line of it.
-- 2. THE BALANCE SHEET CARRIES FORWARD. The GL-02/GL-03 blocker: the view grouped by fiscal year, so
--    a second year started every asset, liability and equity account at nothing. The view now reads
--    each account and branch at its INCEPTION-TO-DATE amount through the row's year, for every year
--    from the account's first movement on, so the books' history stands behind every later year.
--
-- Column shapes are unchanged from 0034; the finstmt read layer moves with no query change.

-- 3. Income statement, REVENUE + EXPENSE, natural orientation, per period/branch, CLOSING journals out.
CREATE OR REPLACE VIEW v_income_statement AS
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
WHERE j.status = 'POSTED' AND j.source <> 'CLOSING' AND a.account_type IN ('REVENUE','EXPENSE')
GROUP BY a.code, a.name, a.account_type, fp.year, fp.month, COALESCE(l.branch_id, j.branch_id);

-- 4. Balance sheet, ASSET / LIABILITY / EQUITY, natural orientation, inception to date THROUGH the
--    row's year: every fiscal year from the account's first movement on carries the sum of the
--    years behind it, per branch (NULL = unbranched).
CREATE OR REPLACE VIEW v_balance_sheet AS
SELECT
  m.account_code,
  m.account_name,
  m.account_type,
  y.year           AS fiscal_year,
  m.branch_id,
  CASE m.account_type
    WHEN 'ASSET' THEN SUM(m.debit_cents - m.credit_cents)
    ELSE              SUM(m.credit_cents - m.debit_cents)
  END              AS amount_cents
FROM (SELECT DISTINCT year FROM fiscal_periods) y
JOIN (
  SELECT
    a.code           AS account_code,
    a.name           AS account_name,
    a.account_type,
    fp.year          AS fiscal_year,
    COALESCE(l.branch_id, j.branch_id) AS branch_id,
    SUM(COALESCE(l.debit_cents, 0))  AS debit_cents,
    SUM(COALESCE(l.credit_cents, 0)) AS credit_cents
  FROM gl_journals j
  JOIN fiscal_periods fp  ON fp.id = j.period_id
  JOIN gl_journal_lines l ON l.journal_id = j.id
  JOIN gl_accounts a      ON a.id = l.account_id
  WHERE j.status = 'POSTED' AND a.account_type IN ('ASSET','LIABILITY','EQUITY')
  GROUP BY a.code, a.name, a.account_type, fp.year, COALESCE(l.branch_id, j.branch_id)
) m ON m.fiscal_year <= y.year
GROUP BY m.account_code, m.account_name, m.account_type, y.year, m.branch_id;

-- 7. Trial balance, round 4 (S3's P2): BALANCE accounts (ASSET, LIABILITY, EQUITY) read INCEPTION TO
--    DATE through the row's year, so the balance sheet (which carries forward) ties to the trial
--    balance from the second year on; P&L accounts keep the year's own movement, the income
--    statement's basis. Column shapes unchanged from 0034.
CREATE OR REPLACE VIEW v_trial_balance AS
SELECT
  m.account_code,
  m.account_name,
  m.account_type,
  m.normal_balance,
  y.year          AS fiscal_year,
  SUM(m.total_debit_cents)  AS total_debit_cents,
  SUM(m.total_credit_cents) AS total_credit_cents,
  SUM(m.balance_cents)      AS balance_cents
FROM (SELECT DISTINCT year FROM fiscal_periods) y
JOIN (
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
  WHERE a.account_type IN ('ASSET','LIABILITY','EQUITY')
  GROUP BY a.code, a.name, a.account_type, a.normal_balance, fp.year
) m ON m.fiscal_year <= y.year
GROUP BY m.account_code, m.account_name, m.account_type, m.normal_balance, y.year
UNION ALL
SELECT
  account_code, account_name, account_type, normal_balance, fiscal_year,
  total_debit_cents, total_credit_cents, balance_cents
FROM (
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
  WHERE a.account_type IN ('REVENUE','EXPENSE')
  GROUP BY a.code, a.name, a.account_type, a.normal_balance, fp.year
) m
UNION ALL
-- The same computed retained earnings to date the balance sheet report carries
-- (the P&L of every earlier year not yet closed into 3900), as an opening
-- equity line, so the trial balance stays in balance on unclosed history.
SELECT '3901', 'Retained earnings to date (unclosed years)', 'EQUITY', 'CREDIT', y.year,
       CASE WHEN prior.amount < 0 THEN -prior.amount ELSE 0 END,
       CASE WHEN prior.amount > 0 THEN prior.amount ELSE 0 END,
       -prior.amount
FROM (SELECT DISTINCT year FROM fiscal_periods) y
CROSS JOIN LATERAL (
  -- Round 5 (S3's P2): only closings of years BEFORE the reporting year stand behind it, the
  -- carried balance being cumulative, so the latest carried year strictly before y.year is the
  -- right figure (a year's own closing must not read again as negative unclosed on its own
  -- statement). Every row's balance is debit minus credit, so the computed credit figure lands
  -- negated in balance_cents (S3's P1, the sign).
  SELECT COALESCE((SELECT SUM(CASE WHEN account_type = 'REVENUE' THEN amount_cents ELSE -amount_cents END)
                     FROM v_income_statement WHERE fiscal_year < y.year), 0)
       - COALESCE((SELECT SUM(amount_cents) FROM v_balance_sheet
                   WHERE fiscal_year = (SELECT COALESCE(MAX(fiscal_year), 0) FROM v_balance_sheet WHERE fiscal_year < y.year)
                     AND account_code = '3900'), 0) AS amount
) prior
WHERE prior.amount <> 0;
