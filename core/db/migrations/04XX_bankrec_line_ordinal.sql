-- 04XX_bankrec_line_ordinal.sql
-- FW1-05: Bank matching integrity.
-- 1. Keep identical statement lines in a single import file apart by adding line_ordinal
--    to bank_statement_lines and scoping uniqueness to (bank_account_id, dedupe_hash, line_ordinal).
-- 2. Bound the seeded run-settlement-fuzzy batch card settlement rule with an explicit fee tolerance
--    (FROM-PRINCIPAL 84) so a whole payment shortfall is never booked to 6300 as a processor fee.

ALTER TABLE bank_statement_lines
  ADD COLUMN IF NOT EXISTS line_ordinal INTEGER NOT NULL DEFAULT 1 CHECK (line_ordinal >= 1);

ALTER TABLE bank_statement_lines
  DROP CONSTRAINT IF EXISTS bank_statement_lines_bank_account_id_dedupe_hash_key;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
     WHERE conname = 'bank_statement_lines_account_hash_ordinal_key'
  ) THEN
    ALTER TABLE bank_statement_lines
      ADD CONSTRAINT bank_statement_lines_account_hash_ordinal_key
      UNIQUE (bank_account_id, dedupe_hash, line_ordinal);
  END IF;
END $$;

UPDATE bank_match_rules
   SET spec = jsonb_set(spec, '{tolerance_cents}', '5000'::jsonb)
 WHERE code = 'run-settlement-fuzzy'
   AND NOT (spec ? 'tolerance_cents');
