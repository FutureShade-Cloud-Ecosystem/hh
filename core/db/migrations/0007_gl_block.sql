-- 0007: the GL block — BEFORE any financial document table exists (R3).
-- Spec: DATA_MODEL.md §7 (G-LOCK-004) · Traces: R3, R4, R5, BT-A-020 (GP export = OPEN Q-006,
-- absorbed later via posting_rules indirection; no schema change anticipated).
-- Posting doctrine: journal entries are written in the SAME TRANSACTION as their source
-- document — a financial document without its entry must be impossible, not logged.

CREATE TABLE gl_accounts (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code           CITEXT NOT NULL UNIQUE,
  name           TEXT NOT NULL,
  account_type   TEXT NOT NULL CHECK (account_type IN ('ASSET','LIABILITY','EQUITY','REVENUE','EXPENSE')),
  normal_balance TEXT NOT NULL CHECK (normal_balance IN ('DEBIT','CREDIT')),
  parent_id      UUID REFERENCES gl_accounts(id) ON DELETE RESTRICT,
  is_active      BOOLEAN NOT NULL DEFAULT TRUE,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO gl_accounts (code, name, account_type, normal_balance) VALUES
  ('1000','Cash',                          'ASSET','DEBIT'),
  ('1100','Accounts Receivable',           'ASSET','DEBIT'),
  ('1150','Holdback Receivable',           'ASSET','DEBIT'),
  ('1200','Inventory',                     'ASSET','DEBIT'),
  ('1300','Prepaid Expenses',              'ASSET','DEBIT'),
  ('1500','Fixed Assets',                  'ASSET','DEBIT'),
  ('2100','Accounts Payable',              'LIABILITY','CREDIT'),
  ('2150','Received Not Invoiced',         'LIABILITY','CREDIT'),
  ('2200','HST Payable',                   'LIABILITY','CREDIT'),
  ('2300','Customer Deposits',             'LIABILITY','CREDIT'),
  ('2350','Holdback Liability',            'LIABILITY','CREDIT'),
  ('3000','Owner''s Equity',               'EQUITY','CREDIT'),
  ('3900','Retained Earnings',             'EQUITY','CREDIT'),
  ('4000','Scrubname0027 Revenue',                 'REVENUE','CREDIT'),
  ('4100','Delivery Revenue',              'REVENUE','CREDIT'),
  ('4150','Break-Fee Revenue',             'REVENUE','CREDIT'),
  ('5000','Cost of Goods Sold',            'EXPENSE','DEBIT'),
  ('5100','Freight-In',                    'EXPENSE','DEBIT'),
  ('5200','Inventory Adjustments',         'EXPENSE','DEBIT'),
  ('6000','Payroll',                       'EXPENSE','DEBIT'),
  ('6100','Rent',                          'EXPENSE','DEBIT'),
  ('6200','Utilities',                     'EXPENSE','DEBIT'),
  ('6300','Bank Fees',                     'EXPENSE','DEBIT'),
  ('9999','Suspense',                      'EXPENSE','DEBIT');

CREATE TABLE fiscal_periods (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  year       INTEGER NOT NULL,
  month      INTEGER NOT NULL CHECK (month BETWEEN 1 AND 12),
  status     TEXT NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN','CLOSED')),
  closed_at  TIMESTAMPTZ,
  closed_by  UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (year, month)
);

INSERT INTO fiscal_periods (year, month)
SELECT 2026, m FROM generate_series(1,12) AS m;

CREATE TABLE gl_journals (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  fiscal_year   INTEGER NOT NULL,
  entry_no      INTEGER NOT NULL,
  period_id     UUID NOT NULL REFERENCES fiscal_periods(id) ON DELETE RESTRICT,
  source        TEXT NOT NULL CHECK (source IN ('MANUAL','INVOICE','PAYMENT','FULFILLMENT','PO_RECEIPT','ADJUSTMENT','CLOSING')),
  source_ref_id UUID,
  memo          TEXT NOT NULL DEFAULT '',
  status        TEXT NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT','POSTED','VOID')),
  posted_at     TIMESTAMPTZ,
  branch_id     UUID REFERENCES locations(id) ON DELETE RESTRICT,
  created_by    UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (fiscal_year, entry_no)
);

CREATE INDEX idx_gl_journals_period ON gl_journals(period_id);
CREATE INDEX idx_gl_journals_source ON gl_journals(source, source_ref_id);

CREATE TABLE gl_journal_lines (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  journal_id   UUID NOT NULL REFERENCES gl_journals(id) ON DELETE CASCADE,
  line_no      INTEGER NOT NULL,
  account_id   UUID NOT NULL REFERENCES gl_accounts(id) ON DELETE RESTRICT,
  debit_cents  BIGINT,
  credit_cents BIGINT,
  branch_id    UUID REFERENCES locations(id) ON DELETE RESTRICT,
  project_ref  UUID,
  memo         TEXT NOT NULL DEFAULT '',
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (journal_id, line_no),
  CHECK ( (debit_cents IS NOT NULL AND credit_cents IS NULL AND debit_cents > 0)
       OR (credit_cents IS NOT NULL AND debit_cents IS NULL AND credit_cents > 0) )
);

CREATE INDEX idx_gl_lines_account ON gl_journal_lines(account_id);

CREATE TABLE posting_rules (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  event_type   TEXT NOT NULL,
  line_role    TEXT NOT NULL,
  account_code CITEXT NOT NULL REFERENCES gl_accounts(code) ON DELETE RESTRICT,
  branch_id    UUID REFERENCES locations(id) ON DELETE CASCADE,
  is_active    BOOLEAN NOT NULL DEFAULT TRUE,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- NULL branch_id = engagement-wide default; a branch row overrides. (Partial unique
-- indexes because UNIQUE(...) treats NULLs as distinct.)
CREATE UNIQUE INDEX ux_posting_rules_default ON posting_rules(event_type, line_role) WHERE branch_id IS NULL;
CREATE UNIQUE INDEX ux_posting_rules_branch  ON posting_rules(event_type, line_role, branch_id) WHERE branch_id IS NOT NULL;

INSERT INTO posting_rules (event_type, line_role, account_code) VALUES
  ('invoice.finalized','ar',        '1100'),
  ('invoice.finalized','revenue',   '4000'),
  ('invoice.finalized','tax',       '2200'),
  ('invoice.finalized','holdback',  '1150'),
  ('payment.received', 'cash',      '1000'),
  ('payment.received', 'ar',        '1100'),
  ('order.fulfilled',  'cogs',      '5000'),
  ('order.fulfilled',  'inventory', '1200'),
  ('po.received',      'inventory', '1200'),
  ('po.received',      'ap_accrual','2150'),
  ('inventory.adjusted','expense',  '5200'),
  ('inventory.adjusted','inventory','1200');
