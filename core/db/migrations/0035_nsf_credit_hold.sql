-- 0035: W5 A-05 — NSF handling + credit holds + declined-attempt log. Owner module: internal/till (NSF
-- transaction), internal/payment (Refund), internal/payments (event emission). Frontend: ar-console.
-- Conventions: UUID PKs · money BIGINT *_cents · TEXT + CHECK · TIMESTAMPTZ · explicit ON DELETE · partial
-- unique index for the "one ACTIVE per customer" invariant · every finance number is a DB POLICY ROW.
--
-- NSF WRITE DOCTRINE (documented, bends module rule 3 deliberately — carried in the mission-plan risks):
--   On a bounce, internal/till writes payment_allocations + customer_transactions + invoices ATOMICALLY in
--   ONE tx (the pos M2-DEBT precedent): reverse every allocation, reopen the affected invoices, and write a
--   reversal customer_transactions row + a fee ADJUSTMENT row. The fee journal facts + payment.bounced are
--   emitted to events_outbox in the SAME tx (transactional outbox → gl posts). We do NOT write a reversing
--   tax_decision_snapshot on an NSF: the invoice reopens and the tax liability is unchanged (the sale still
--   happened; only the cash failed).

-- 1. NSF record — one per bounced payment. fee_cents / fee_taxable are captured from nsf_policy AT bounce
--    time (a later policy change never rewrites history). payment_id UNIQUE = at most one NSF per payment.
CREATE TABLE payment_nsf (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  payment_id      UUID NOT NULL UNIQUE REFERENCES payments(id) ON DELETE RESTRICT,
  customer_id     UUID REFERENCES customers(id) ON DELETE RESTRICT,
  reason          TEXT NOT NULL DEFAULT '',
  fee_cents       BIGINT NOT NULL DEFAULT 0,
  fee_taxable     BOOLEAN NOT NULL DEFAULT FALSE,
  fee_account_code CITEXT REFERENCES gl_accounts(code) ON DELETE RESTRICT,
  recorded_by     UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_payment_nsf_customer ON payment_nsf(customer_id);

-- 2. Credit holds — at most ONE ACTIVE per customer (partial unique index). A hold blocks ACCOUNT tenders
--    only (fail closed); cash/card are always allowed. Release is Tier-1-only with a mandatory reason (A-05).
CREATE TABLE credit_holds (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id       UUID NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
  source_payment_id UUID REFERENCES payments(id) ON DELETE SET NULL,
  status            TEXT NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','RELEASED')),
  reason            TEXT NOT NULL DEFAULT '',
  release_reason    TEXT NOT NULL DEFAULT '',
  placed_by         UUID REFERENCES users(id) ON DELETE SET NULL,
  released_by       UUID REFERENCES users(id) ON DELETE SET NULL,
  released_at       TIMESTAMPTZ,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- The keystone invariant: one ACTIVE hold per customer (a second ACTIVE row is impossible).
CREATE UNIQUE INDEX ux_credit_holds_one_active ON credit_holds(customer_id) WHERE status = 'ACTIVE';
CREATE INDEX idx_credit_holds_customer ON credit_holds(customer_id, status);

-- 3. NSF policy — DATA, not code. branch_id NULL = org default; a branch row overrides. fee_cents 4500 =
--    $45 non-taxable PLACEHOLDER pending the external accountant (Scrubname0001 on leave, Q-021). auto_hold TRUE =
--    a bounce auto-places a credit hold.
CREATE TABLE nsf_policy (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  branch_id        UUID UNIQUE REFERENCES locations(id) ON DELETE CASCADE,  -- NULL = org default
  fee_cents        BIGINT NOT NULL DEFAULT 4500,        -- $45 PLACEHOLDER (Q-021)
  fee_taxable      BOOLEAN NOT NULL DEFAULT FALSE,
  fee_account_code CITEXT NOT NULL DEFAULT '4200' REFERENCES gl_accounts(code) ON DELETE RESTRICT,
  auto_hold        BOOLEAN NOT NULL DEFAULT TRUE,
  notes            TEXT NOT NULL DEFAULT '',
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO nsf_policy (branch_id, fee_cents, fee_taxable, fee_account_code, auto_hold, notes)
VALUES (NULL, 4500, FALSE, '4200', TRUE, 'PLACEHOLDER $45 — pending external-accountant confirmation (Scrubname0001 Holt on leave, Q-021)');

-- 4. Payment attempts (DX-14 fold-in) — the declined/errored gateway attempt log. idempotency_key makes a
--    retried attempt replay-safe; a successful capture is the payments row (0010), not an attempt here.
CREATE TABLE payment_attempts (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  idempotency_key TEXT NOT NULL DEFAULT '',
  customer_id     UUID REFERENCES customers(id) ON DELETE SET NULL,
  amount_cents    BIGINT NOT NULL CHECK (amount_cents > 0),
  method          TEXT NOT NULL CHECK (method IN ('CASH','CARD','CHEQUE','EFT','OTHER')),
  processor       TEXT NOT NULL DEFAULT '',
  result          TEXT NOT NULL CHECK (result IN ('DECLINED','ERROR')),
  detail          TEXT NOT NULL DEFAULT '',
  branch_id       UUID REFERENCES locations(id) ON DELETE SET NULL,
  attempted_by    UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX ux_payment_attempts_key ON payment_attempts(idempotency_key) WHERE idempotency_key <> '';
CREATE INDEX idx_payment_attempts_customer ON payment_attempts(customer_id, created_at DESC);
