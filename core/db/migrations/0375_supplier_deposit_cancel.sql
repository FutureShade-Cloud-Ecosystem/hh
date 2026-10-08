-- 0375_supplier_deposit_cancel.sql
--
-- A cancelled special order's supplier deposit (decision log row 1193, the
-- supervisor's ruling): a dealer setting defaulting to credit the deposit back
-- to the customer's account, a manager able to keep it on a given order with a
-- reason, and the two bookings that clear 2300 Customer Deposits either way.
--
-- Three facts:
--
--   1. pos_policy.supplier_deposit_cancel, the dealer default a cancel applies
--      when the order's only money is a supplier deposit document. CREDIT_BACK
--      (the default): DR 2300 / CR the customer's receivable, the deposit
--      credited back on the account. KEPT: DR 2300 / CR other income, the
--      dealer keeps the deposit. A manager's per-order override to keep it
--      rides the cancel itself, not this column.
--
--   2. gl_accounts 4950 Other Income, the credit leg of the kept booking. The
--      chart carried no other-income account: 4200 and 4210 are named fees and
--      4900 is warranty recovery, none of them a kept deposit.
--
--   3. The posting rules for the deposit.cancelled topic, emitted once per
--      live deposit document a cancel settles:
--
--   deposit.cancelled / customer_deposit -> 2300 (DR, the liability cleared)
--   deposit.cancelled / ar               -> 1100 (CR, credit back on the account,
--                                           or the unpaid rest of a kept deposit)
--   deposit.cancelled / other_income     -> 4950 (CR, the kept deposit)
--
-- The journal key is deposit.cancelled:<invoice id>, the release's own
-- deposit.released:<invoice id> pattern, so a replayed event posts nothing.
--
-- Owner module: internal/pos (the setting), internal/billing (the document
-- settlement and the emission), internal/gl (the posting).

-- 1. The dealer setting.
ALTER TABLE pos_policy
    ADD COLUMN supplier_deposit_cancel TEXT NOT NULL DEFAULT 'CREDIT_BACK'
        CHECK (supplier_deposit_cancel IN ('CREDIT_BACK','KEPT'));

-- 2. The other income account.
INSERT INTO gl_accounts (code, name, account_type, normal_balance)
VALUES ('4950', 'Other Income', 'REVENUE', 'CREDIT')
ON CONFLICT (code) DO NOTHING;

-- 3. The posting pair (the default, branch-NULL rows; a branch override rides
--    the same partial index pair every other rule does).
INSERT INTO posting_rules (event_type, line_role, account_code)
VALUES
    ('deposit.cancelled', 'customer_deposit', '2300'),
    ('deposit.cancelled', 'ar', '1100'),
    ('deposit.cancelled', 'other_income', '4950')
ON CONFLICT (event_type, line_role) WHERE branch_id IS NULL DO NOTHING;
