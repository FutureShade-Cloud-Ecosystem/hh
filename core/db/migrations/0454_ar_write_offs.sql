-- 0454: AR write offs.
--
-- A manager writes off an invoice's remaining balance, with a reason, under a small balance limit.
-- The write off posts DR a bad debt expense account and CR 1100, so the aging and GL 1100 move by
-- the same cents, and it is reversible once (W3 item 0.10).
--
-- Three things are seeded here:
--
-- 1. The expense account. The chart (0007) carries no uncollectible accounts expense: the nearest
--    accounts are Inventory Adjustments (5200, goods) and Bank Fees (6300, bank), and neither is
--    where an accountant books a receivable that will never be collected. 5400 sits in the expense
--    block's numbering beside them and carries the name an accountant recognises for the act, small
--    balance or uncollectible alike.
--
-- 2. The posting pairs, the default branch-NULL rows; a branch override rides the same partial index
--    every other rule does. gl.ValidatePostingRules requires both roles at boot, for the reversal as
--    well: an undo that cannot resolve its accounts would strand the money it is undoing.
--
-- 3. The branch setting. The limit is a branch's own number with the org default as the fallback
--    (the ar_credit_policy shape: branch row overrides, branch_id NULL row is the org default, and
--    0104 seeds that row), so the column default IS the org default: $25.00.

INSERT INTO gl_accounts (code, name, account_type, normal_balance)
VALUES ('5400', 'Bad Debt Expense', 'EXPENSE', 'DEBIT')
ON CONFLICT (code) DO NOTHING;

INSERT INTO posting_rules (event_type, line_role, account_code) VALUES
    ('ar.invoice.write_off', 'write_off_expense', '5400'),
    ('ar.invoice.write_off', 'ar', '1100'),
    ('ar.invoice.write_off_reversed', 'write_off_expense', '5400'),
    ('ar.invoice.write_off_reversed', 'ar', '1100')
ON CONFLICT (event_type, line_role) WHERE branch_id IS NULL DO NOTHING;

ALTER TABLE ar_credit_policy
  ADD COLUMN write_off_limit_cents BIGINT NOT NULL DEFAULT 2500 CHECK (write_off_limit_cents >= 0);
