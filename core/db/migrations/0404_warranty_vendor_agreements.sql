-- 0404_warranty_vendor_agreements.sql
--
-- The per vendor warranty credit agreement (walk change 6, the Dealer Admin Warranty page's second
-- card): how each vendor credits warranty. Three kinds, the boards' closed set:
--   claim_by_claim        every approved claim is credited on its own, the default when no row
--   periodic_credit_note  one credit note rolls the period's claims up, on a standard month or
--                         quarter (WARRANTY-DEFAULTS ruling A, rule 5)
--   fixed_allowance       a percent of the vendor's purchases in the period, paid as a standing
--                         damage allowance (WARRANTY-DEFAULTS ruling A, rule 2)
--
-- ONE ROW PER VENDOR (UNIQUE vendor_id, the warranty_vendor_profiles shape, 0053): the read LEFT
-- JOINs the vendor master and answers claim_by_claim for a vendor with no row, so the default lives
-- in the read alone and the table stays honest about the vendors a dealer actually set. No seed: a
-- fresh dealer's vendors all answer the default, which is the truth.
--
-- period is the standard month or quarter the periodic credit note or the fixed allowance runs on;
-- NULL for claim_by_claim, where nothing rolls up. allowance_bp is the fixed allowance as INTEGER
-- BASIS POINTS of the vendor's purchases in the period (1% = 100 bp), the house percent shape (never
-- a float), capped at 10000 bp: a vendor crediting more than the whole of its purchases as damage
-- allowance is a typo, and the CHECK is the backstop behind the service's 422 in words.
--
-- updated_by and updated_at carry the LAST change on the row itself. The history lives on the audit
-- spine (one WARRANTY_VENDOR_AGREEMENT_CHANGED row per moved knob, written by the warranty service's
-- Recorder with the old and new value), not in a second table beside it: the 0396 ruling, kept.

CREATE TABLE warranty_vendor_agreements (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  vendor_id      UUID NOT NULL UNIQUE REFERENCES vendors(id) ON DELETE CASCADE,
  agreement_kind TEXT NOT NULL DEFAULT 'claim_by_claim'
                 CHECK (agreement_kind IN ('claim_by_claim', 'periodic_credit_note', 'fixed_allowance')),
  period         TEXT CHECK (period IS NULL OR period IN ('monthly', 'quarterly')),
  allowance_bp   INTEGER NOT NULL DEFAULT 0 CHECK (allowance_bp BETWEEN 0 AND 10000),
  updated_by     UUID REFERENCES users(id) ON DELETE SET NULL,
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
