-- 0129: ERP:warranty-p2 — PER-OUTCOME SETTLEMENT MONEY PATHS. Owner: internal/warranty (Wave 5, Swarm B).
--
-- WHAT P1 LEFT OPEN. WTY-01 P1 recorded a settlement (outcome + expected/realized cents) and posted ONE
-- generic journal for the two cash outcomes. Every other money consequence — a vendor credit that has to be
-- CHASED until it lands, a customer credit that is a billing document, a replacement that is a stock cost
-- movement — was a comment in service.go. That is what this migration closes: each outcome now names its OWN
-- money path, and the path a settlement took is a ROW, not an inference from the outcome column.
--
-- WHY A CONFIG TABLE AND NOT A GO SWITCH. The house rule is "every tunable is a DB row", and the account
-- mapping here is the exact thing the accountant has not ratified yet (PRD §9 Q5). warranty_outcome_paths maps
-- outcome → money path → seam → account pair, so ratifying the mapping is an UPDATE, and adding a fifth
-- outcome later is an INSERT plus a CHECK edit — not a redeploy of posting logic. The Go code reads this table
-- and refuses to invent a path for an outcome that has no row.
--
-- WHY A POSTINGS TABLE AND NOT A gl_journal_id COLUMN. warranty_claims.gl_journal_id (0053) can hold exactly
-- one journal and says nothing about the two paths that produce no journal at all (a billing credit memo is
-- billing's document; an inventory cost move is inventory's transaction). warranty_settlement_postings records
-- WHICH path ran, WHAT it produced (a journal id, a foreign document ref, or nothing), and WHY it produced
-- nothing (seam unwired / no cash effect / failed). Without it "did the replacement actually cost anything"
-- has no answer in the database, and the recovery board (0130) has no accrual to reconcile against.

-- ─────────────────────────────────────────────────────────────────────────────
-- 1. The outcome → money-path map (configuration, one row per outcome).
--
--    money_path is the NAMED path, stable across account re-mappings:
--      VENDOR_RECOVERY        — the vendor owes us. Posts the AR-side recovery journal AND accrues an
--                               outstanding balance on the recovery board (0130). This is the ~$40k reality.
--      CUSTOMER_CREDIT        — the customer is made whole with a credit memo. Billing owns the document and
--                               its journal; warranty records the ref. FALLBACK_GL when the seam is unwired
--                               (the expense still belongs on the books — see status below).
--      INVENTORY_REPLACEMENT  — stock leaves the yard to replace the failed product. Inventory owns the cost
--                               movement; warranty records the ref. No cash journal here, ever.
--      EXPENSE_WRITE_OFF      — denied / eaten. Straight warranty-expense journal.
--
--    seam names WHO executes the path, so an unwired seam is a legible operational fact rather than a silence.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE warranty_outcome_paths (
  outcome        TEXT PRIMARY KEY
                   CHECK (outcome IN ('VENDOR_CREDIT','REPLACEMENT_STOCK','CUSTOMER_ACCOMMODATION','WRITE_OFF')),
  money_path     TEXT NOT NULL
                   CHECK (money_path IN ('VENDOR_RECOVERY','CUSTOMER_CREDIT','INVENTORY_REPLACEMENT','EXPENSE_WRITE_OFF')),
  seam           TEXT NOT NULL
                   CHECK (seam IN ('GL','BILLING','INVENTORY')),
  -- The account pair for the paths warranty posts ITSELF (seam='GL'), and the FALLBACK pair for a seam path
  -- whose seam is unwired. NULL for a path that must never post from here.
  debit_account_code  CITEXT REFERENCES gl_accounts(code) ON DELETE RESTRICT,
  credit_account_code CITEXT REFERENCES gl_accounts(code) ON DELETE RESTRICT,
  -- accrues_recovery marks the path whose amount becomes an OUTSTANDING receivable on the recovery board.
  -- Only the vendor path does; a customer credit is money going OUT, not money owed to us.
  accrues_recovery BOOLEAN NOT NULL DEFAULT FALSE,
  is_active      BOOLEAN NOT NULL DEFAULT TRUE,
  notes          TEXT NOT NULL DEFAULT '',
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- A GL-seam path must carry both accounts: posting half a journal is not a thing.
  CONSTRAINT warranty_outcome_paths_gl_accounts CHECK (
    seam <> 'GL' OR (debit_account_code IS NOT NULL AND credit_account_code IS NOT NULL)
  )
);

-- The account codes are the ones 0053 already registered (4900 recovery, 5900 expense) plus 1100 clearing,
-- which is what P1's postSettlementJournal already used. DOCUMENTED PLACEHOLDERS pending PRD §9 Q5 — the point
-- of the table is that ratifying them is an UPDATE.
INSERT INTO warranty_outcome_paths
  (outcome, money_path, seam, debit_account_code, credit_account_code, accrues_recovery, notes) VALUES
  ('VENDOR_CREDIT',          'VENDOR_RECOVERY',       'GL',        '1100', '4900', TRUE,
   'DR clearing / CR warranty recovery. Accrues an outstanding vendor balance on the WTY-RECOVERY board until received.'),
  ('CUSTOMER_ACCOMMODATION', 'CUSTOMER_CREDIT',       'BILLING',   '5900', '1100', FALSE,
   'Billing issues the customer credit memo and owns its journal. The account pair is the FALLBACK used only when the billing seam is unwired, so the accommodation expense still lands.'),
  ('REPLACEMENT_STOCK',      'INVENTORY_REPLACEMENT', 'INVENTORY', NULL,   NULL,   FALSE,
   'Inventory owns the replacement cost movement. Warranty never posts a cash journal for a replacement — no fallback, by design.'),
  ('WRITE_OFF',              'EXPENSE_WRITE_OFF',     'GL',        '5900', '1100', FALSE,
   'DR warranty expense / CR clearing. The denied/eaten claim.')
ON CONFLICT (outcome) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────────────
-- 2. The posting record: one row per money path executed for a claim's settlement.
--
--    status is the honest report of what happened:
--      POSTED        — the path completed and produced a ref (journal id or external document ref).
--      NO_EFFECT     — the path ran and correctly produced nothing (a zero-amount settlement).
--      SEAM_UNWIRED  — the seam that owns this path is not bound in this deployment. The REQUEST that would
--                      have been sent is preserved in `request` so the union (or an operator) can replay it.
--      FALLBACK_GL   — the seam was unwired AND the path has a fallback account pair, so warranty posted the
--                      journal itself. Deliberately distinct from POSTED: the books are right, the document
--                      the customer expects does not exist yet.
--      FAILED        — the seam was wired and refused. The settlement is still recorded (P1 rule: a GL failure
--                      never un-books a settlement), and this row carries the error.
--      MANUAL        — P1's manual settlement, exercised as an OVERRIDE: a manager recorded this settlement
--                      with the money already booked by hand (the bookkeeper raised the credit note last week),
--                      so the automatic path was deliberately NOT run. It is a status rather than an absent row
--                      because "nobody ran a path here, on purpose, and here is who said so" is information;
--                      a missing row is indistinguishable from a bug.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE warranty_settlement_postings (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  claim_id       UUID NOT NULL REFERENCES warranty_claims(id) ON DELETE CASCADE,
  outcome        TEXT NOT NULL
                   CHECK (outcome IN ('VENDOR_CREDIT','REPLACEMENT_STOCK','CUSTOMER_ACCOMMODATION','WRITE_OFF')),
  money_path     TEXT NOT NULL
                   CHECK (money_path IN ('VENDOR_RECOVERY','CUSTOMER_CREDIT','INVENTORY_REPLACEMENT','EXPENSE_WRITE_OFF')),
  seam           TEXT NOT NULL CHECK (seam IN ('GL','BILLING','INVENTORY')),
  status         TEXT NOT NULL
                   CHECK (status IN ('POSTED','NO_EFFECT','SEAM_UNWIRED','FALLBACK_GL','FAILED','MANUAL')),
  amount_cents   BIGINT NOT NULL DEFAULT 0 CHECK (amount_cents >= 0),
  -- accrues_recovery is COPIED from the config at posting time: re-pointing the config later must not
  -- retroactively change what an already-settled claim accrued (the recovery board's arithmetic is history).
  accrues_recovery BOOLEAN NOT NULL DEFAULT FALSE,
  gl_journal_id  UUID REFERENCES gl_journals(id) ON DELETE SET NULL,
  external_ref   TEXT NOT NULL DEFAULT '',   -- billing credit-memo no / inventory movement ref
  request        JSONB NOT NULL DEFAULT '{}',-- the exact seam request (replayable when SEAM_UNWIRED)
  error_text     TEXT NOT NULL DEFAULT '',
  note           TEXT NOT NULL DEFAULT '',
  posted_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- One posting per (claim, path). P1 forbids re-settlement, so in practice this is one row per claim; the
  -- composite key is what makes the write idempotent if a retry ever re-runs the same path.
  UNIQUE (claim_id, money_path),
  -- A POSTED row must show its work: either a journal or a foreign ref. Otherwise "posted" is a claim with no
  -- evidence, which is exactly the ambiguity this table exists to remove.
  CONSTRAINT warranty_settlement_postings_posted_has_ref CHECK (
    status <> 'POSTED' OR gl_journal_id IS NOT NULL OR external_ref <> ''
  ),
  -- A FALLBACK_GL row is by definition a journal warranty posted itself.
  CONSTRAINT warranty_settlement_postings_fallback_has_journal CHECK (
    status <> 'FALLBACK_GL' OR gl_journal_id IS NOT NULL
  )
);
CREATE INDEX idx_warranty_settlement_postings_claim ON warranty_settlement_postings(claim_id);
CREATE INDEX idx_warranty_settlement_postings_path ON warranty_settlement_postings(money_path, status);
-- The recovery board's hot read: outstanding vendor accruals.
CREATE INDEX idx_warranty_settlement_postings_accrual ON warranty_settlement_postings(claim_id)
  WHERE accrues_recovery;

-- Posting-rule roles for the two paths warranty posts itself, so the 0122 posting_rules registry knows these
-- event types exist even though warranty resolves its accounts through warranty_outcome_paths (the config the
-- accountant will edit). Registering them keeps the GL rule inventory complete.
INSERT INTO posting_rules (event_type, line_role, account_code) VALUES
  ('warranty.recovery',  'warranty_clearing',  '1100'),
  ('warranty.recovery',  'warranty_recovery',  '4900'),
  ('warranty.writeoff',  'warranty_expense',   '5900'),
  ('warranty.writeoff',  'warranty_clearing',  '1100')
ON CONFLICT (event_type, line_role) WHERE branch_id IS NULL DO NOTHING;
