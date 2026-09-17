-- 0042: W6 M5 — Tax Engine: the configurable Ontario HST table + status-card exemptions that replaces the W4
-- flat-13% stub. Owner: internal/tax. Conventions: rates in bps · money BIGINT *_cents · effective-dated ·
-- masked evidence (never raw serialized) · every rate/threshold is a DB row. Inherits the 0029 FN tables
-- (purchaser_eligibility_profile / tax_evidence_doc / tax_decision_snapshot). Ontario-only at W6; round once at
-- invoice total with a largest-remainder per-line tax_cents; Avalara + postal routing are dormant stubs.

-- 1. Tax rates — configurable, effective-dated ON-HST table (replaces the hard-coded 1300 fallback). A rate is
--    keyed by jurisdiction + reason code; the engine picks the row live on the sale date. ALL PLACEHOLDERS (D-021).
CREATE TABLE tax_rates (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  jurisdiction   TEXT NOT NULL DEFAULT 'ON',
  reason_code    TEXT NOT NULL,          -- mirrors tax_decision_snapshot.tax_reason_code
  rate_bps       INTEGER NOT NULL CHECK (rate_bps BETWEEN 0 AND 10000),
  effective_from DATE NOT NULL DEFAULT DATE '2000-01-01',
  effective_to   DATE,                   -- NULL = open-ended
  notes          TEXT NOT NULL DEFAULT 'PLACEHOLDER — pending tax-table confirmation (D-021)',
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_tax_rates_lookup ON tax_rates(jurisdiction, reason_code, effective_from);
-- Seed the W4-parity table: standard 13% + the FN relief bands. These reproduce the flat-13 behavior exactly
-- (the pos seam stays on FlatHSTAssessor at scaffold; M5 swaps the engine in to READ these rows).
INSERT INTO tax_rates (jurisdiction, reason_code, rate_bps) VALUES
  ('ON','STD_ON_HST_13',           1300),
  ('ON','ON_FN_POS_8',              500),   -- collect 5% net on the Ontario point-of-sale path
  ('ON','FED_S87_VENDOR_DELIVERY',    0),   -- 0% once delivery evidence is attached (hard gate)
  ('ON','FNGST_5',                  500),
  ('ON','MANUAL_TAX_REVIEW',       1300);

-- 2. Status-card profiles — the three admin-editable exemption profiles, each FORCING band-card photo +
--    number + attestation as evidence (D-036..). serialization is MASKED-only (a raw card number never leaves
--    the DB). PLACEHOLDER third profile is PENDING (13%) until ratified.
CREATE TABLE tax_status_card_profiles (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code              CITEXT NOT NULL UNIQUE,
  name              TEXT NOT NULL,
  rate_bps          INTEGER NOT NULL CHECK (rate_bps BETWEEN 0 AND 10000),
  requires_photo    BOOLEAN NOT NULL DEFAULT TRUE,
  requires_number   BOOLEAN NOT NULL DEFAULT TRUE,
  requires_attestation BOOLEAN NOT NULL DEFAULT TRUE,
  status            TEXT NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','PENDING','RETIRED')),
  notes             TEXT NOT NULL DEFAULT '',
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO tax_status_card_profiles (code, name, rate_bps, status, notes) VALUES
  ('ON_RESERVE_DELIVERY','On-reserve Dibbits delivery (0%)',    0,   'ACTIVE',  'Requires POD/waybill evidence (hard gate)'),
  ('FEDERAL_PICKUP',     'Federal status pickup (5% GST only)', 500, 'ACTIVE',  'Ontario 8% credited at POS'),
  ('THIRD_PROFILE',      'Third profile (PENDING ratification)', 1300,'PENDING', 'PLACEHOLDER — pending D-036 ratification')
ON CONFLICT (code) DO NOTHING;

-- 3. Tax evidence — the M5 status-card evidence record (distinct from the 0029 transaction tax_evidence_doc):
--    the band-card photo ref, MASKED number, attestation flag, and card expiry. The raw number is NEVER stored
--    or serialized here — only the mask + an encrypted-elsewhere reference.
CREATE TABLE tax_evidence (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  profile_id     UUID REFERENCES purchaser_eligibility_profile(id) ON DELETE SET NULL,
  card_profile_id UUID REFERENCES tax_status_card_profiles(id) ON DELETE SET NULL,
  order_id       UUID REFERENCES orders(id) ON DELETE SET NULL,
  photo_ref      TEXT NOT NULL DEFAULT '',     -- pointer to the encrypted band-card image (never inline)
  number_mask    TEXT NOT NULL DEFAULT '',     -- e.g. "•••• 4821" — the ONLY identifier serialized
  attestation    BOOLEAN NOT NULL DEFAULT FALSE,
  tcrd_expiry    DATE,
  captured_by    UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_tax_evidence_order ON tax_evidence(order_id);
CREATE INDEX idx_tax_evidence_profile ON tax_evidence(profile_id);

-- 4. Tax config — engine-wide settings (HST registration number, round mode, dormant Avalara/postal toggles).
--    DATA, single org-default row; branch overrides may be added later. PLACEHOLDERS (D-021).
CREATE TABLE tax_config (
  id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  branch_id            UUID UNIQUE REFERENCES locations(id) ON DELETE CASCADE, -- NULL = org default
  hst_registration_no  TEXT NOT NULL DEFAULT '',
  round_mode           TEXT NOT NULL DEFAULT 'INVOICE_TOTAL'
                         CHECK (round_mode IN ('INVOICE_TOTAL','PER_LINE')),  -- round once at invoice total
  avalara_enabled      BOOLEAN NOT NULL DEFAULT FALSE,   -- dormant stub (demoted; D-A19-5)
  postal_routing_enabled BOOLEAN NOT NULL DEFAULT FALSE, -- dormant stub
  notes                TEXT NOT NULL DEFAULT 'PLACEHOLDER — pending HST registration + tax confirmation (D-021)',
  created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO tax_config (branch_id, hst_registration_no, round_mode) VALUES (NULL, '', 'INVOICE_TOTAL');

-- 5. Per-line tax_cents on order_lines + invoice_lines (the largest-remainder split the pos seam writes). The
--    header tax_cents on orders/invoices (0008/0009) is unchanged; these are the per-line breakdown for M5.
ALTER TABLE order_lines   ADD COLUMN IF NOT EXISTS tax_cents BIGINT NOT NULL DEFAULT 0;
ALTER TABLE invoice_lines ADD COLUMN IF NOT EXISTS tax_cents BIGINT NOT NULL DEFAULT 0;

-- 6. Insert-only discipline on tax_decision_snapshot (0029): a snapshot is the immutable auditable "why" of a
--    sale's tax decision — a return/credit writes a NEW reversing snapshot, never an UPDATE (FN spec §11.4).
--    M5's recompute is also INSERT-only. Enforce it so a settled tax decision can never be rewritten under an
--    audit. (DELETE stays allowed for test/GC hygiene, as with the append-only stock ledger.)
CREATE OR REPLACE FUNCTION tax_decision_snapshot_no_update() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'tax_decision_snapshot is insert-only — a tax decision cannot be updated (write a reversing snapshot)';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_tax_snapshot_no_update ON tax_decision_snapshot;
CREATE TRIGGER trg_tax_snapshot_no_update
  BEFORE UPDATE ON tax_decision_snapshot
  FOR EACH ROW EXECUTE FUNCTION tax_decision_snapshot_no_update();

-- 7. FN tax-split accounts — the federal 5% / Ontario 8% portions of collected HST (tracking placeholders;
--    2200 HST Payable + 1165 FN Relief Recoverable already exist from 0007/0032). These give the M5 recon a
--    place to split the collected liability when the engine goes live.
INSERT INTO gl_accounts (code, name, account_type, normal_balance) VALUES
  ('2201','HST Payable — Federal 5% Portion',  'LIABILITY','CREDIT'),
  ('2202','HST Payable — Ontario 8% Portion',  'LIABILITY','CREDIT')
ON CONFLICT (code) DO NOTHING;
