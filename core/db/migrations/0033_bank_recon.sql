-- 0033: W5 A-02 — bank reconciliation. Owner module: internal/bankrec.
-- Conventions: UUID PKs · money BIGINT *_cents · TEXT + CHECK (no ENUMs) · TIMESTAMPTZ · explicit ON DELETE ·
-- flag-don't-delete · CITEXT for human codes · partial-unique indexes where a NULL branch means "org default".
-- Scrubname0001-gap rule: every W5 tunable is a DB POLICY / PROFILE / RULE row (bank_import_profiles,
-- bank_match_rules below), never an env var. Seeded profile/rule specs are PLACEHOLDERS pending the
-- real RBC/Visa statement formats (Q-021).
--
-- 1020 doctrine (see 0032 header): GL 1000 (RBC operating) lags cash reality until the bank-side match
-- journals post here. bank_accounts.gl_account_code binds each real account to its GL code; the Corporate
-- Visa accounts (2110-2117) are TRACKING-ONLY at W5 (no Visa spend is booked yet).

-- 1. The real bank + card accounts, each bound to a GL account code (0007/0032 codes).
CREATE TABLE bank_accounts (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name            TEXT NOT NULL,
  account_type    TEXT NOT NULL CHECK (account_type IN ('BANK','CREDIT_CARD')),
  gl_account_code CITEXT NOT NULL REFERENCES gl_accounts(code) ON DELETE RESTRICT,
  institution     TEXT NOT NULL DEFAULT '',
  last4           TEXT NOT NULL DEFAULT '',
  is_active       BOOLEAN NOT NULL DEFAULT TRUE,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (gl_account_code)
);

-- SEED 9: RBC Operating → 1000 (the operating bank), Corporate Visa 1..8 → 2110..2117 (tracking-only).
INSERT INTO bank_accounts (name, account_type, gl_account_code, institution) VALUES
  ('RBC Operating',        'BANK',        '1000', 'RBC'),
  ('Corporate Visa — Card 1','CREDIT_CARD','2110', 'RBC'),
  ('Corporate Visa — Card 2','CREDIT_CARD','2111', 'RBC'),
  ('Corporate Visa — Card 3','CREDIT_CARD','2112', 'RBC'),
  ('Corporate Visa — Card 4','CREDIT_CARD','2113', 'RBC'),
  ('Corporate Visa — Card 5','CREDIT_CARD','2114', 'RBC'),
  ('Corporate Visa — Card 6','CREDIT_CARD','2115', 'RBC'),
  ('Corporate Visa — Card 7','CREDIT_CARD','2116', 'RBC'),
  ('Corporate Visa — Card 8','CREDIT_CARD','2117', 'RBC')
ON CONFLICT (gl_account_code) DO NOTHING;

-- 2. Import profiles — column-mapping is DATA (the StatementSource CSV parser reads mapping JSONB). Seeded
--    PLACEHOLDER mappings for the RBC + Visa exports pending the real column headers (Q-021).
CREATE TABLE bank_import_profiles (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code        CITEXT NOT NULL UNIQUE,
  name        TEXT NOT NULL,
  format      TEXT NOT NULL DEFAULT 'CSV' CHECK (format IN ('CSV','OFX','PLAID')),
  mapping     JSONB NOT NULL DEFAULT '{}',   -- {date_col, description_col, amount_col, reference_col, date_format, ...}
  is_active   BOOLEAN NOT NULL DEFAULT TRUE,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO bank_import_profiles (code, name, format, mapping) VALUES
  ('rbc-default',  'RBC Operating CSV (default)', 'CSV',
     '{"date_col":"Date","description_col":"Description","amount_col":"Amount","reference_col":"Reference","date_format":"2006-01-02"}'),
  ('visa-default', 'Corporate Visa CSV (default)', 'CSV',
     '{"date_col":"Transaction Date","description_col":"Description","amount_col":"Amount","reference_col":"Reference","date_format":"2006-01-02"}')
ON CONFLICT (code) DO NOTHING;

-- 3. One import run over a profile (the audit envelope for a batch of parsed lines).
CREATE TABLE bank_statement_imports (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  bank_account_id UUID NOT NULL REFERENCES bank_accounts(id) ON DELETE RESTRICT,
  profile_id      UUID REFERENCES bank_import_profiles(id) ON DELETE SET NULL,
  filename        TEXT NOT NULL DEFAULT '',
  status          TEXT NOT NULL DEFAULT 'IMPORTED' CHECK (status IN ('IMPORTED','FAILED')),
  row_count       INTEGER NOT NULL DEFAULT 0,
  error_detail    TEXT NOT NULL DEFAULT '',
  imported_by     UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_bank_imports_account ON bank_statement_imports(bank_account_id, created_at DESC);

-- 4. Parsed statement lines. dedupe_hash makes re-import idempotent (UNIQUE per account). amount_cents is
--    signed (credits positive, debits negative) — the parser normalizes sign from the profile mapping.
CREATE TABLE bank_statement_lines (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  bank_account_id UUID NOT NULL REFERENCES bank_accounts(id) ON DELETE RESTRICT,
  import_id       UUID REFERENCES bank_statement_imports(id) ON DELETE SET NULL,
  txn_date        DATE NOT NULL,
  description     TEXT NOT NULL DEFAULT '',
  reference       TEXT NOT NULL DEFAULT '',
  amount_cents    BIGINT NOT NULL,              -- signed: + credit / − debit
  dedupe_hash     TEXT NOT NULL,                -- deterministic over (date, amount, description, reference)
  status          TEXT NOT NULL DEFAULT 'UNMATCHED' CHECK (status IN ('UNMATCHED','MATCHED','EXCLUDED')),
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (bank_account_id, dedupe_hash)
);
CREATE INDEX idx_bank_lines_account_status ON bank_statement_lines(bank_account_id, status);
CREATE INDEX idx_bank_lines_date ON bank_statement_lines(txn_date);

-- 5. Matches — a statement line to a domain target (a payment, an open lodgement, a GL journal, or a fuzzy
--    GROUP). unmatched_at NULL = live; a manual unmatch stamps it (history is kept, never deleted).
CREATE TABLE bank_matches (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  statement_line_id UUID NOT NULL REFERENCES bank_statement_lines(id) ON DELETE RESTRICT,
  target_type       TEXT NOT NULL CHECK (target_type IN ('PAYMENT','LODGEMENT','JOURNAL','GROUP')),
  target_ref_id     UUID,                          -- payment_id / journal_id / lodgement movement id (NULL for a GROUP header)
  method            TEXT NOT NULL CHECK (method IN ('AUTO','MANUAL')),
  rule_id           UUID,                          -- the bank_match_rules row that fired (AUTO); NULL for MANUAL
  journal_id        UUID REFERENCES gl_journals(id) ON DELETE SET NULL, -- the clearing journal posted for this match
  amount_cents      BIGINT NOT NULL DEFAULT 0,
  matched_by        UUID REFERENCES users(id) ON DELETE SET NULL,
  matched_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  unmatched_at      TIMESTAMPTZ,                    -- NULL = live match; stamped on a manual unmatch (kept for history)
  unmatched_by      UUID REFERENCES users(id) ON DELETE SET NULL
);
CREATE INDEX idx_bank_matches_line ON bank_matches(statement_line_id);
CREATE INDEX idx_bank_matches_target ON bank_matches(target_type, target_ref_id);
-- At most ONE live match per statement line (a re-match must unmatch first).
CREATE UNIQUE INDEX ux_bank_matches_live_line ON bank_matches(statement_line_id) WHERE unmatched_at IS NULL;

-- 6. Match rules — the auto-matcher spec is DATA (JSONB), versioned via a supersedes chain. status DRAFT →
--    APPROVED → RETIRED. SEED 3 APPROVED v1 rules (PLACEHOLDER specs, Q-021).
CREATE TABLE bank_match_rules (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code             CITEXT NOT NULL,
  name             TEXT NOT NULL,
  spec             JSONB NOT NULL,               -- matcher predicate (descriptor_ilike, date_window_days, allow_fee_split, ...)
  priority         INTEGER NOT NULL DEFAULT 100,
  version          INTEGER NOT NULL DEFAULT 1,
  status           TEXT NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT','APPROVED','RETIRED')),
  supersedes_rule_id UUID REFERENCES bank_match_rules(id) ON DELETE SET NULL,
  approved_by      UUID REFERENCES users(id) ON DELETE SET NULL,
  approved_at      TIMESTAMPTZ,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- At most one APPROVED version per rule code (the live rule; older versions are RETIRED).
CREATE UNIQUE INDEX ux_bank_match_rules_approved ON bank_match_rules(code) WHERE status = 'APPROVED';
INSERT INTO bank_match_rules (code, name, spec, priority, version, status, approved_at) VALUES
  ('gateway-ref-exact',    'Card settlement — gateway reference exact',
     '{"kind":"gateway_ref_exact","target":"PAYMENT","tolerance_cents":0}', 10, 1, 'APPROVED', now()),
  ('lodgement-ref-exact',  'Cash/cheque lodgement — reference exact (zero tolerance)',
     '{"kind":"lodgement_ref_exact","target":"LODGEMENT","tolerance_cents":0}', 20, 1, 'APPROVED', now()),
  ('run-settlement-fuzzy', 'RUN settlement — descriptor + date window (fee split)',
     '{"kind":"descriptor_fuzzy","target":"PAYMENT","descriptor_ilike":"%RUN%","date_window_days":3,"allow_fee_split":true,"fee_account_code":"6300"}', 30, 1, 'APPROVED', now())
ON CONFLICT DO NOTHING;

-- 7. Monthly reconciliation per bank account. variance = statement_balance − gl_balance; COMPLETED requires
--    zero unmatched + zero variance (enforced in the service, A-02). UNIQUE (account, year, month).
CREATE TABLE bank_reconciliations (
  id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  bank_account_id        UUID NOT NULL REFERENCES bank_accounts(id) ON DELETE RESTRICT,
  year                   INTEGER NOT NULL,
  month                  INTEGER NOT NULL CHECK (month BETWEEN 1 AND 12),
  statement_balance_cents BIGINT NOT NULL DEFAULT 0,
  gl_balance_cents       BIGINT NOT NULL DEFAULT 0,
  variance_cents         BIGINT NOT NULL DEFAULT 0,
  status                 TEXT NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN','COMPLETED')),
  completed_by           UUID REFERENCES users(id) ON DELETE SET NULL,
  completed_at           TIMESTAMPTZ,
  created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (bank_account_id, year, month)
);
CREATE INDEX idx_bank_recon_account ON bank_reconciliations(bank_account_id, year DESC, month DESC);
