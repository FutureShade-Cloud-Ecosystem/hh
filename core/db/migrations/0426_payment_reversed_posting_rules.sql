-- 0426: payment reversals post to the GL, and every one credits 2310 (SF-2d).
--
-- payments/reversal.go emits payment.reversed in the reversal's own transaction. The topic was not in
-- gl.PostedTopics and had no posting rules, so no handler took the row and the dispatcher ended it DEAD; after any
-- reversal the GL kept the original receipt's legs while the AR subledger and the invoices re-opened.
--
-- THE RULE (ruled, FROM-PRINCIPAL 52): every payment.reversed is Dr AR / Cr 2310 "Customer Refunds and Unapplied
-- Cash", whatever the tender, session, register or deposit status. A reversal leaves the original payment RECEIVED
-- (payments/reversal.go), and the till's expected tenders count every RECEIVED payment of a session, so the money is
-- either still in the drawer, where it is lodged and the bank match clears 1020, or gone unrecorded, where the count
-- shows the shortage and the close posts it against 1020. Crediting 1020 at the reversal would credit it twice in
-- either case. 2310 holds the money for the customer until the act that moves it clears it: re-application to an
-- invoice (Dr 2310 / Cr AR), a pay out (Dr 2310 / Cr where the money leaves), or a write off (Dr 2310 / Cr 6350).
-- payment.bounced keeps its own rule (0035, 0036): that money never arrived.
--
-- 1. The liability account (the chart's shape: 2300 Customer Deposits, 2350 Holdback Liability are its neighbours;
--    2300 is the supplier deposit documents' liability and does not fit).
INSERT INTO gl_accounts (code, name, account_type, normal_balance)
VALUES ('2310', 'Customer Refunds and Unapplied Cash', 'LIABILITY', 'CREDIT')
ON CONFLICT (code) DO NOTHING;

-- 2. The posting pair, the default branch-NULL rows; a branch override rides the same partial index every other rule
--    does. gl.ValidatePostingRules requires both roles at boot.
INSERT INTO posting_rules (event_type, line_role, account_code) VALUES
    ('payment.reversed', 'ar',             '1100'),
    ('payment.reversed', 'refund_payable', '2310')
ON CONFLICT (event_type, line_role) WHERE branch_id IS NULL DO NOTHING;
