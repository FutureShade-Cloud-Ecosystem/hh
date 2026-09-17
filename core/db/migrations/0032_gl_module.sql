-- 0032: W5 A-01 — GL module additions on the EXISTING seeded 0007 block (gl_accounts / gl_journals /
-- gl_journal_lines / fiscal_periods / posting_rules — verified real names). Owner module: internal/gl.
-- Conventions: UUID PKs · money BIGINT *_cents · TEXT + CHECK (no ENUMs) · TIMESTAMPTZ · explicit ON DELETE.
-- Scrubname0001-gap rule: every W5 tunable is a DB POLICY ROW (gl_policy below), never an env var. Every seeded
-- finance number is a documented PLACEHOLDER pending external-accountant confirmation (Scrubname0001 Holt on
-- leave, Q-021).
--
-- TOPIC → POSTING_RULES.EVENT_TYPE MAPPING (frozen contract; mirrored in internal/gl/module.go):
--   'ar.invoice.finalized'  → 'invoice.finalized'   (the 0007 legacy key — its rules live there)
--   every other outbox topic → its LITERAL topic name (rules seeded below under topic names)
-- The two-name scheme is recorded so a literal-topic lookup never silently finds zero rules. Verified:
-- the seeds below do not collide with the 0007 rows under ux_posting_rules_default(event_type, line_role).
--
-- CASH-FLOW DOCTRINE (1020 undeposited funds): 1000 = RBC operating bank. Cash/cheque receipts post
-- DR 1020 Undeposited Funds (NOT 1000); card receipts DR 1010 Card Settlement Clearing. The bank-side
-- DR 1000 / CR 1020 (deposit match) and DR 1000 / CR 1010 (settlement match) journals are posted by
-- W5-A02-bankrec on statement match. GL 1000 lags reality BY DESIGN until bankrec merges (last).

-- 1a. gl_journals gains the outbox idempotency key (one journal per financial event, replay-safe),
--     the maker-checker approver, and the one-click reversing-JE link.
ALTER TABLE gl_journals
  ADD COLUMN idempotency_key     TEXT NOT NULL DEFAULT '',
  ADD COLUMN approved_by         UUID REFERENCES users(id) ON DELETE SET NULL,
  ADD COLUMN reverses_journal_id UUID REFERENCES gl_journals(id) ON DELETE RESTRICT;
CREATE UNIQUE INDEX ux_gl_journals_idem ON gl_journals(idempotency_key) WHERE idempotency_key <> '';

-- 1b. Period close/reopen override log. Reopen is Scrubname0007-only + mandatory reason (2FA is not
--     available in identity — this row + the audit record is the recorded compensating control).
CREATE TABLE gl_period_overrides (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  period_id  UUID NOT NULL REFERENCES fiscal_periods(id) ON DELETE RESTRICT,
  action     TEXT NOT NULL CHECK (action IN ('CLOSE','REOPEN')),
  reason     TEXT NOT NULL,
  actor_id   UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_gl_period_overrides_period ON gl_period_overrides(period_id);

-- 1c. GL policy — DATA, not code consts (till_policy 0028 precedent). branch_id NULL = org-wide default
--     row; a branch row overrides. ALL SEEDED VALUES ARE PLACEHOLDERS (Q-021).
CREATE TABLE gl_policy (
  id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  branch_id                   UUID UNIQUE REFERENCES locations(id) ON DELETE CASCADE,  -- NULL = org default
  je_approval_threshold_cents BIGINT NOT NULL DEFAULT 500000,  -- $5,000 maker-checker threshold (PLACEHOLDER)
  fiscal_year_start_month     INTEGER NOT NULL DEFAULT 1 CHECK (fiscal_year_start_month BETWEEN 1 AND 12),
  cost_basis_strategy         TEXT NOT NULL DEFAULT 'WEIGHTED_AVG'
                                CHECK (cost_basis_strategy IN ('WEIGHTED_AVG','STANDARD_BUY')),
  exclude_special_buys        BOOLEAN NOT NULL DEFAULT TRUE,
  notes                       TEXT NOT NULL DEFAULT '',
  created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO gl_policy (branch_id, je_approval_threshold_cents, fiscal_year_start_month, cost_basis_strategy, exclude_special_buys, notes)
VALUES (NULL, 500000, 1, 'WEIGHTED_AVG', TRUE,
        'PLACEHOLDER — pending external-accountant confirmation (Scrubname0001 Holt on leave, Q-021)');

-- 1d. Recurring JE templates: /run materializes a DRAFT journal from the JSONB lines (A-01 T9).
CREATE TABLE gl_recurring_templates (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name       TEXT NOT NULL,
  memo       TEXT NOT NULL DEFAULT '',
  lines      JSONB NOT NULL,          -- [{account_code, debit_cents|credit_cents, memo}]
  schedule   TEXT NOT NULL DEFAULT 'MONTHLY',
  next_run   DATE,
  is_active  BOOLEAN NOT NULL DEFAULT TRUE,
  created_by UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 1e. GP-parity export batches (A-01 T10; drop-at-cutover path — Q-006).
CREATE TABLE gl_export_batches (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  fiscal_year INTEGER NOT NULL,
  month       INTEGER NOT NULL CHECK (month BETWEEN 1 AND 12),
  format      TEXT NOT NULL DEFAULT 'GP_PARITY',
  row_count   INTEGER NOT NULL DEFAULT 0,
  exported_by UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 1f. Tie-out discrepancy state — persists FIRST-SEEN age per subledger so the tie-out report carries
--     out_of_tie_since + age_days across checks (A-01 T8, req 8). UNIQUE treats NULLs as distinct, so
--     branch-scoped rows use the partial-index pair (0007 posting_rules precedent).
CREATE TABLE gl_tieout_state (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  subledger           TEXT NOT NULL,        -- e.g. 'AR', 'BANK:<bank_account_id>'
  account_code        CITEXT NOT NULL,
  branch_id           UUID REFERENCES locations(id) ON DELETE CASCADE,  -- NULL = consolidated
  first_out_of_tie_at TIMESTAMPTZ,          -- NULL = in tie
  last_checked_at     TIMESTAMPTZ,
  last_delta_cents    BIGINT NOT NULL DEFAULT 0,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX ux_gl_tieout_default ON gl_tieout_state(subledger, account_code) WHERE branch_id IS NULL;
CREATE UNIQUE INDEX ux_gl_tieout_branch  ON gl_tieout_state(subledger, account_code, branch_id) WHERE branch_id IS NOT NULL;

-- 2. New GL accounts. 2110–2117 are FK targets for the 0033 Visa bank_accounts and are TRACKING-ONLY
--    placeholders at W5 (nothing books Visa spend yet). 2500/2510/2520 are the loan/lease/LOC
--    placeholder liabilities (A-04 req 6, ASPE 3065) — structure AND current-vs-long-term
--    classification are PLACEHOLDERS pending the external accountant (Q-021).
INSERT INTO gl_accounts (code, name, account_type, normal_balance) VALUES
  ('1010','Card Settlement Clearing',                      'ASSET','DEBIT'),
  ('1020','Undeposited Funds — Cash In Transit',           'ASSET','DEBIT'),
  ('1165','FN Point-of-Scrubname0026 Relief Recoverable (ON 8%)',   'ASSET','DEBIT'),
  ('2110','Corporate Visa Payable — Card 1',               'LIABILITY','CREDIT'),
  ('2111','Corporate Visa Payable — Card 2',               'LIABILITY','CREDIT'),
  ('2112','Corporate Visa Payable — Card 3',               'LIABILITY','CREDIT'),
  ('2113','Corporate Visa Payable — Card 4',               'LIABILITY','CREDIT'),
  ('2114','Corporate Visa Payable — Card 5',               'LIABILITY','CREDIT'),
  ('2115','Corporate Visa Payable — Card 6',               'LIABILITY','CREDIT'),
  ('2116','Corporate Visa Payable — Card 7',               'LIABILITY','CREDIT'),
  ('2117','Corporate Visa Payable — Card 8',               'LIABILITY','CREDIT'),
  ('2500','RBC Line of Credit',                            'LIABILITY','CREDIT'),
  ('2510','Equipment Loans — Paccar/CEL',                  'LIABILITY','CREDIT'),
  ('2520','Lease Obligations (ASPE 3065)',                 'LIABILITY','CREDIT'),
  ('4200','NSF Fee Income',                                'REVENUE','CREDIT'),
  ('6350','Cash Over/Short',                               'EXPENSE','DEBIT')
ON CONFLICT (code) DO NOTHING;

-- 3a. Undeposited-funds doctrine: the existing 0007 'payment.received'/'cash' rule moves 1000 → 1020.
--     An UPDATE (not a new row) avoids colliding with ux_posting_rules_default(event_type, line_role).
--     The consumer picks 1020 (cash/cheque) vs 1010 (card) via the PaymentReader method facts.
UPDATE posting_rules
SET account_code = '1020', updated_at = now()
WHERE event_type = 'payment.received' AND line_role = 'cash' AND branch_id IS NULL;

-- 3b. New rules, keyed by LITERAL topic name per the frozen two-name mapping (header).
INSERT INTO posting_rules (event_type, line_role, account_code) VALUES
  ('payment.received',       'card_clearing', '1010'),
  ('payment.bounced',        'ar',            '1100'),
  ('payment.bounced',        'bank',          '1000'),
  ('payment.bounced',        'undeposited',   '1020'),
  ('payment.bounced',        'fee_income',    '4200'),
  ('tax.relief.assessed',    'hst_full',      '2200'),
  ('tax.relief.assessed',    'fn_relief',     '1165'),
  ('till.session.completed', 'over_short',    '6350'),
  ('till.session.completed', 'undeposited',   '1020')
ON CONFLICT (event_type, line_role) WHERE branch_id IS NULL DO NOTHING;
