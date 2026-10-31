-- 0472: the early payment discount at cash application (TM-2, plan row after TM-1).
--
-- A payment applied to an invoice on or before its discount date, house day, takes the invoice's
-- own terms percent of the money applied, and the act posts DR discounts allowed / CR 1100 beside
-- the receipt. Three things land here:
--
-- 1. The discounts allowed account. The chart carries no such account today: the revenue block
--    (4000, 4100, 4150, 4200) holds incomes and the expense block's 54xx holds the receivable's
--    own costs (5400, the bad debt). 5450 sits beside 5400 as the other thing that quietly shrinks
--    a receivable's collectible value, and it is the DR side of the posting, an expense with a
--    debit balance.
--
-- 2. The posting pair for the discount's own event. The window and the percent are decided in the
--    application's transaction (payments reads the invoice's stored terms code through the customer
--    module's master and emits payment.discount.taken per discounted invoice there), and the
--    consumer posts the journal on the deterministic key payment.discount.taken:<payment>:<invoice>,
--    so a redelivered outbox row posts once.
--
-- 3. The allocation's own record of the discount it took. payment_allocations.discount_cents is
--    the durable per application figure the reversal reads to give the discount back with the
--    money, and the account's statement row (customer_transactions, txn_type ADJUSTMENT,
--    source_type 'invoice') carries the subledger side, so the aging reconstruction at a past date
--    reads the discount from the statement and never counts it twice.
--
-- Forward only, rerunnable: every statement is additive or ON CONFLICT DO NOTHING.

INSERT INTO gl_accounts (code, name, account_type, normal_balance)
VALUES ('5450', 'Sales Discounts Allowed', 'EXPENSE', 'DEBIT')
ON CONFLICT (code) DO NOTHING;

INSERT INTO posting_rules (event_type, line_role, account_code) VALUES
    ('payment.discount.taken', 'discount_allowed', '5450'),
    ('payment.discount.taken', 'ar', '1100')
ON CONFLICT DO NOTHING;

ALTER TABLE payment_allocations ADD COLUMN IF NOT EXISTS discount_cents BIGINT NOT NULL DEFAULT 0;

COMMENT ON COLUMN payment_allocations.discount_cents IS
'in integer cents, the early payment discount this application took on the invoice (TM-2): the invoice''s own terms percent of the money applied, rounded half to even, taken inside the invoice''s stored discount window or under a recorded manager approval outside it. A reversal''s contra row carries the discount it gives back, positive like its amount.';
