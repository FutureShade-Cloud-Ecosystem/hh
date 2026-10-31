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

-- 4. The manager's approval of a discount taken OUTSIDE its window. The walk refuses a late
--    discount unless the caller passed the approval gate (the same wall the AR write off route
--    runs: the money desk's manage level under the manager tier), and a manager's approval is
--    RECORDED: who approved, when, the invoice, the amount taken and the manager's reason, one row
--    per invoice whose discount rode the approval, written in the application's own transaction so
--    a discount taken on an approval never stands without its record. A retry under the same key
--    replays the first posting and writes no second row.
CREATE TABLE IF NOT EXISTS invoice_discount_approvals (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id     UUID NOT NULL REFERENCES invoices(id),
    payment_id     UUID NOT NULL REFERENCES payments(id),
    approved_by    UUID NOT NULL,
    approved_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    discount_cents BIGINT NOT NULL CHECK (discount_cents > 0),
    reason         TEXT NOT NULL CHECK (length(btrim(reason)) > 0),
    branch_id      UUID NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_invoice_discount_approvals_invoice ON invoice_discount_approvals (invoice_id);
CREATE INDEX IF NOT EXISTS idx_invoice_discount_approvals_payment ON invoice_discount_approvals (payment_id);

COMMENT ON TABLE invoice_discount_approvals IS
'TM-2: one manager approval of an early payment discount taken outside its window, recorded in the application''s own transaction beside the discount it allowed.';
