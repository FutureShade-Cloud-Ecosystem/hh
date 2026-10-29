-- 0462_fx_realised_gain_loss.sql
--
-- W3-FX1: realised FX gain or loss at supplier bill payment (decision 1.9, FROM-PRINCIPAL 153).
--
-- A USD vendor bill is booked to 2100 Accounts Payable in CAD at its own rate (bill.fx_rate).
-- When it is paid at a payment rate, the CAD leaving 1000 Operating Bank differs from the CAD
-- the bill carries in 2100. 2100 clears the bill's CAD share for the paid USD (pro rata rounded
-- half to even in cents on part payments, with the exact remainder cleared on the last payment),
-- 1000 credits the CAD at the payment rate, and the difference posts to 6310 Realised Foreign
-- Exchange Gain or Loss (DEBIT on a loss when the payment rate exceeds the bill rate, CREDIT on
-- a gain when the payment rate is below the bill rate).
--
-- Owner modules: internal/gl (gl_accounts, posting_rules), internal/purchasing (purchasing_bills,
-- purchasing_bill_instalments).

-- 1. The realised FX gain or loss account in the 6300 financial variance block (beside 6300 Bank
--    Fees and 6350 Cash Over/Short).
INSERT INTO gl_accounts (code, name, account_type, normal_balance)
VALUES ('6310', 'Realised Foreign Exchange Gain or Loss', 'EXPENSE', 'DEBIT')
ON CONFLICT (code) DO NOTHING;

-- 2. Default posting rule so internal/purchasing resolves the FX gain/loss leg through the rule
--    seam alongside the bill's 2100 AP leg.
INSERT INTO posting_rules (event_type, line_role, account_code)
VALUES ('vendor.bill.posted', 'fx_gain_loss', '6310')
ON CONFLICT (event_type, line_role) WHERE branch_id IS NULL DO NOTHING;

DO $$
DECLARE
  v_bad TEXT;
BEGIN
  SELECT string_agg(pr.line_role || '->' || pr.account_code, ', ')
    INTO v_bad
    FROM posting_rules pr
    LEFT JOIN gl_accounts a ON a.code = pr.account_code AND a.is_active
   WHERE pr.event_type = 'vendor.bill.posted'
     AND pr.line_role = 'fx_gain_loss'
     AND pr.branch_id IS NULL
     AND a.id IS NULL;
  IF v_bad IS NOT NULL THEN
    RAISE EXCEPTION '0454: vendor.bill.posted fx_gain_loss rule points at missing/inactive gl_accounts: %', v_bad;
  END IF;
END $$;

-- 3. Document-currency total on purchasing_bills and document-currency paid amount + payment rate
--    on purchasing_bill_instalments so part payments on a USD bill track both the paid USD and the
--    CAD share cleared from 2100.
ALTER TABLE purchasing_bills
  ADD COLUMN IF NOT EXISTS doc_total_cents BIGINT NOT NULL DEFAULT 0 CHECK (doc_total_cents >= 0);

UPDATE purchasing_bills
   SET doc_total_cents = CASE
     WHEN currency = 'USD' AND fx_rate > 0 THEN ROUND(total_cents / fx_rate)::bigint
     ELSE total_cents
   END
 WHERE doc_total_cents = 0;

ALTER TABLE purchasing_bill_instalments
  ADD COLUMN IF NOT EXISTS doc_amount_cents BIGINT NOT NULL DEFAULT 0 CHECK (doc_amount_cents >= 0),
  ADD COLUMN IF NOT EXISTS fx_rate          NUMERIC(12, 6) CHECK (fx_rate IS NULL OR fx_rate > 0);

UPDATE purchasing_bill_instalments
   SET doc_amount_cents = amount_cents,
       fx_rate = 1.0
 WHERE doc_amount_cents = 0;
