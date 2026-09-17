-- 0029: A-19 POS / counter-sales. Idempotency + minimal FN-tax-evidence data model + WALK-IN seed.
-- Owns: pos_sale, purchaser_eligibility_profile, tax_evidence_doc, tax_decision_snapshot, pos_signature.
-- The order/invoice/payment tables (0008-0010) are written by pos/repository.go directly (M2 DEBT).
-- The eligibility/evidence/snapshot tables are the SHARED FN data model — created here because A-19 is the
-- first consumer; the full FN engine is M5. NO ALTER of the shared 0008/0009/0010 tables.

-- 1. Idempotency + POS sale facets (the replay-safe spine of the offline-first Scrubname0026).
CREATE TABLE pos_sale (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  idempotency_key UUID NOT NULL UNIQUE,                 -- client-minted; the replay-safe guard (US-A19-05)
  order_id        UUID NOT NULL REFERENCES orders(id) ON DELETE RESTRICT,
  invoice_id      UUID REFERENCES invoices(id) ON DELETE SET NULL,
  payment_id      UUID REFERENCES payments(id) ON DELETE SET NULL, -- NULL for ACCOUNT
  branch_id       UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  till_session_id UUID REFERENCES till_sessions(id) ON DELETE SET NULL,
  sale_type       TEXT NOT NULL DEFAULT 'COLLECT_NOW' CHECK (sale_type IN ('COLLECT_NOW','COLLECT_LATER','DELIVERED')),
  cash_sale       BOOLEAN NOT NULL DEFAULT FALSE,
  tender          TEXT NOT NULL CHECK (tender IN ('ACCOUNT','CASH','CARD','CHEQUE')),
  tax_reason_code TEXT NOT NULL DEFAULT 'STD_ON_HST_13',
  offline_origin  BOOLEAN NOT NULL DEFAULT FALSE,
  status          TEXT NOT NULL DEFAULT 'COMMITTED' CHECK (status IN ('COMMITTED','VOIDED')),
  created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_pos_sale_order ON pos_sale(order_id);
CREATE INDEX idx_pos_sale_branch_time ON pos_sale(branch_id, created_at DESC);

-- 2. Purchaser eligibility profile (SHARED; pre-populates assess-relief; NEVER auto-exempts).
CREATE TABLE purchaser_eligibility_profile (
  id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id           UUID REFERENCES customers(id) ON DELETE SET NULL, -- nullable (transaction-only purchaser)
  purchaser_type        TEXT NOT NULL CHECK (purchaser_type IN ('INDIVIDUAL','SOLE_PROP','PARTNERSHIP','CORPORATION','BAND','BEE','TRUST_JV','OTHER')),
  eligibility_type      TEXT NOT NULL DEFAULT 'STATUS' CHECK (eligibility_type IN ('STATUS','BAND','BEE','OTHER')),
  proof_type            TEXT CHECK (proof_type IN ('STATUS_CARD','TCRD','BAND_CERT','BEE_CERT','OTHER')),
  ontario_eligibility   TEXT CHECK (ontario_eligibility IN ('ON_RESIDENT','AKWESASNE','NOT_ELIGIBLE','UNKNOWN')),
  proof_identifier_enc  BYTEA,                            -- AES-256-GCM at rest; NEVER returned raw to a cashier
  proof_identifier_mask TEXT NOT NULL DEFAULT '',         -- e.g. "•••• 4821" — the ONLY thing the cashier sees
  tcrd_expiry           DATE,                             -- block expired; retain historical sale evidence
  verified_at           TIMESTAMPTZ,
  verified_by           UUID REFERENCES users(id) ON DELETE SET NULL,
  entity_review_status  TEXT NOT NULL DEFAULT 'NONE' CHECK (entity_review_status IN ('NONE','PENDING','APPROVED','REJECTED')),
  created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_eligibility_customer ON purchaser_eligibility_profile(customer_id);

-- 3. Tax evidence documents (SHARED; encrypted at rest, RBAC + retention, masked/withheld from cashiers).
CREATE TABLE tax_evidence_doc (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  profile_id      UUID REFERENCES purchaser_eligibility_profile(id) ON DELETE SET NULL,
  order_id        UUID REFERENCES orders(id) ON DELETE SET NULL,          -- transaction-scoped evidence
  doc_type        TEXT NOT NULL CHECK (doc_type IN ('STATUS_RECORD','BAND_CERT','BEE_CERT','POD','WAYBILL','DISPATCH','EXCEPTION_MEMO','OTHER')),
  content_enc     BYTEA,                                                   -- encrypted; card-image upload DISABLED in std in-person flow
  content_mask    TEXT NOT NULL DEFAULT '',
  source          TEXT NOT NULL DEFAULT '',
  version         INTEGER NOT NULL DEFAULT 1,
  retention_until DATE,                                                    -- 6-yr tax retention
  uploaded_by     UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_evidence_profile ON tax_evidence_doc(profile_id);
CREATE INDEX idx_evidence_order ON tax_evidence_doc(order_id);

-- 4. Tax decision snapshot (per-sale/per-line immutable record; the auditable "why").
--    W4 records reason+rate+evidence link; the deterministic 0%/5%/13%/FNGST engine is M5.
CREATE TABLE tax_decision_snapshot (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  order_id            UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  order_line_id       UUID REFERENCES order_lines(id) ON DELETE SET NULL,    -- null = header-level decision
  tax_reason_code     TEXT NOT NULL CHECK (tax_reason_code IN (
                        'STD_ON_HST_13','ON_FN_POS_8','FED_S87_GOODS_ON_RES','FED_S87_VENDOR_DELIVERY',
                        'FED_S87_SERVICE_RESERVE','FED_BAND_MANAGEMENT','FNGST_5','FNT_PRODUCT',
                        'TREATY_OR_SELF_GOV_REVIEW','MANUAL_TAX_REVIEW','RELIEF_DECLINED_EVIDENCE')),
  rule_version        TEXT NOT NULL DEFAULT 'W4-flat-13',                    -- bound to the rule-set registry in M5
  rate_bps            INTEGER NOT NULL DEFAULT 1300,                         -- 13% provisional fallback
  full_hst_cents      BIGINT NOT NULL DEFAULT 0,
  on_credit_cents     BIGINT NOT NULL DEFAULT 0,                             -- the 8% Ontario credit when ON_FN_POS_8
  net_collected_cents BIGINT NOT NULL DEFAULT 0,
  automated           BOOLEAN NOT NULL DEFAULT FALSE,                        -- M5 flips this true
  approver_id         UUID REFERENCES users(id) ON DELETE SET NULL,
  evidence_doc_id     UUID REFERENCES tax_evidence_doc(id) ON DELETE SET NULL,
  signature_id        UUID,                                                  -- FK added after pos_signature below
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_tax_snapshot_order ON tax_decision_snapshot(order_id);

-- 5. Captured signatures (A-19-owned; encrypted evidence, transaction-linked, audited via A-21).
CREATE TABLE pos_signature (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  purpose       TEXT NOT NULL CHECK (purpose IN ('TAX_ATTESTATION','PROOF_OF_DELIVERY','CARD_PRESENT','ORDER_SIGNOFF')),
  ref_type      TEXT NOT NULL,                            -- e.g. 'order' | 'tax_decision_snapshot'
  ref_id        UUID NOT NULL,
  signature_enc BYTEA NOT NULL,                           -- AES-256-GCM at rest (shared vault primitive)
  provider      TEXT NOT NULL DEFAULT 'SIMULATED' CHECK (provider IN ('SIMULATED','RUN_TERMINAL','THIRD_PARTY')),
  captured_by   UUID REFERENCES users(id) ON DELETE SET NULL,
  captured_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_pos_signature_ref ON pos_signature(ref_type, ref_id);
ALTER TABLE tax_decision_snapshot
  ADD CONSTRAINT fk_tax_snapshot_signature FOREIGN KEY (signature_id) REFERENCES pos_signature(id) ON DELETE SET NULL;

-- 6. Seed the canonical house WALK-IN customer so orders.customer_id NOT NULL (0008) is satisfiable on a
--    migrate-only env. RETAIL, no credit (-> COD). ON CONFLICT DO NOTHING (D-A19-4).
INSERT INTO customers (id, account_no, name, customer_type, payment_terms_days, credit_limit_cents)
VALUES ('a1900000-0000-4000-9000-000000000001','WALK-IN','Scrubbed Party 0009','RETAIL',0,0)
ON CONFLICT (account_no) DO NOTHING;
