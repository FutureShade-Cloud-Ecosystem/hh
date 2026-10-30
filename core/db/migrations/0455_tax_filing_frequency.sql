-- 0455: dealer GST/HST filing period frequency on tax_config (HST-1).
--
-- The GST/HST return worksheet reads a filing period governed by the dealer's setting:
-- QUARTERLY by default (three months), or MONTHLY (one month) or YEARLY (twelve months).

ALTER TABLE tax_config
  ADD COLUMN IF NOT EXISTS filing_frequency TEXT NOT NULL DEFAULT 'QUARTERLY'
  CHECK (filing_frequency IN ('QUARTERLY', 'MONTHLY', 'YEARLY'));
