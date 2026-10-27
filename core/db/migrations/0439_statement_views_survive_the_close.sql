-- 0439 (FW1-09b, migration named by the principal, FROM-PRINCIPAL 106): the
-- statement views survive the year end close.
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
