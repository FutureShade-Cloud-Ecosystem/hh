-- 0426: payment reversals post to the GL (SF-2d).
--
-- payments/reversal.go emits payment.reversed in the reversal's own transaction, and its design comment says the GL
-- books the reversing journal from it. The topic was not in gl.PostedTopics and had no posting rules, so no handler
-- took the row and the dispatcher ended it DEAD; after any reversal the GL kept the original receipt's legs while
-- the AR subledger and the invoices re-opened.
--
-- The journal is Dr AR / Cr the account the original receipt's cash leg sits on, chosen the way payment.bounced
-- chooses its reversal-credit leg (0036): bank 1000 once the original was DEPOSITED, else card clearing 1010 for a
-- card, else undeposited funds 1020. The role names and accounts are payment.bounced's. The default, branch-NULL rows
-- only; a branch override rides the same partial index every other rule does. gl.ValidatePostingRules requires
-- all four roles at boot.
INSERT INTO posting_rules (event_type, line_role, account_code) VALUES
    ('payment.reversed', 'ar',            '1100'),
    ('payment.reversed', 'bank',          '1000'),
    ('payment.reversed', 'undeposited',   '1020'),
    ('payment.reversed', 'card_clearing', '1010')
ON CONFLICT (event_type, line_role) WHERE branch_id IS NULL DO NOTHING;
