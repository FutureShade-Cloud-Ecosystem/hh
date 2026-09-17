-- 0122: the GL account + posting rules a vendor bill posts through.
--
-- THE JOURNAL A BILL WRITES, and why each leg exists:
--
--   DR 2150 Received Not Invoiced   the accrual inventory raised when the goods landed, now relieved. This
--                                   is the leg that makes GRNI self-clearing: receiving credits 2150,
--                                   billing debits it, and a 2150 balance at month-end means exactly
--                                   "goods we have but have not been invoiced for".
--   DR 2200 HST Payable             recoverable input tax, when the bill carries any.
--   DR 5250 Purchase Price Variance the difference between what the vendor charged and what the PO said the
--                                   goods cost. Positive = charged more than agreed. It goes to its own
--                                   account rather than into COGS because a buyer needs to be able to ask
--                                   "which vendors are drifting off contract" and get an answer from the
--                                   ledger. A NEGATIVE variance (billed less than agreed, or fewer units
--                                   than received) posts as a CREDIT to the same account — same question,
--                                   other sign.
--   CR 2100 Accounts Payable        the money owed.
--
-- WHY THE VARIANCE LEG IS NOT OPTIONAL. Without it a bill that disagrees with its PO either cannot be
-- posted at all (and AP stalls) or is silently forced to agree (and the disagreement disappears). Booking it
-- to a named account is what lets a VARIANCE bill be accepted deliberately, by a named manager, and still
-- leave a trail an auditor can follow.
--
-- Rules, not literals: internal/purchasing resolves every leg through gl.ResolveRule(event, role, branch),
-- so a branch that wants its own variance account sets one row here and nothing in Go changes. gl's
-- boot-time ValidatePostingRules checks every ACTIVE rule resolves to an active account, so a typo in this
-- file fails the server at boot rather than at month-end.

INSERT INTO gl_accounts (code, name, account_type, normal_balance)
VALUES ('5250', 'Purchase Price Variance', 'EXPENSE', 'DEBIT')
ON CONFLICT (code) DO NOTHING;

INSERT INTO posting_rules (event_type, line_role, account_code) VALUES
  ('vendor.bill.posted', 'ap',             '2100'),
  ('vendor.bill.posted', 'ap_accrual',     '2150'),
  ('vendor.bill.posted', 'price_variance', '5250'),
  ('vendor.bill.posted', 'tax',            '2200')
ON CONFLICT DO NOTHING;

-- Prove the rules resolve to ACTIVE accounts here, at migrate time, rather than letting the server discover
-- it at boot. Same check gl.ValidatePostingRules runs, scoped to the four rows this file adds.
DO $$
DECLARE
  v_bad TEXT;
BEGIN
  SELECT string_agg(pr.event_type || '/' || pr.line_role || '→' || pr.account_code, ', ')
    INTO v_bad
    FROM posting_rules pr
    LEFT JOIN gl_accounts a ON a.code = pr.account_code AND a.is_active
   WHERE pr.event_type = 'vendor.bill.posted' AND a.id IS NULL;
  IF v_bad IS NOT NULL THEN
    RAISE EXCEPTION '0122: vendor.bill.posted rule(s) point at a missing/inactive account: %', v_bad;
  END IF;
END;
$$;
