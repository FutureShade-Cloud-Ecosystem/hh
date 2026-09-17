-- 0036: W5 S3 remediation — the reserved integrator migration. Owner modules: internal/bankrec + internal/gl.
-- Conventions unchanged: partial-unique indexes for "at most one live" invariants · posting_rules are DATA
-- (line_role → account_code), NULL branch_id = engagement-wide default. finstmt needed no view fix (0034 is
-- point-in-time correct); this file carries only the confirmed money-integrity fixes.
--
-- FIX B2 — bank double-match / duplicate clearing journal. ux_bank_matches_live_line (0033) only stops TWO
--   live matches on the same statement LINE; nothing stopped one payment/lodgement TARGET backing two live
--   matches (AutoMatch + ManualMatch, or two duplicate statement lines), which posts the DR1000/CR1010(1020)
--   clearing journal twice and overstates the bank. A partial-unique index on the live target closes it. It is
--   scoped to PAYMENT/LODGEMENT targets: a JOURNAL/GROUP target legitimately repeats (a manual grouping header
--   has a NULL ref; a JOURNAL target can back several lines) so those are excluded from the constraint.
CREATE UNIQUE INDEX ux_bank_matches_live_target
  ON bank_matches (target_type, target_ref_id)
  WHERE unmatched_at IS NULL AND target_type IN ('PAYMENT', 'LODGEMENT');

-- FIX (MAJOR) — the payment.bounced GL builder can now pick the reversal-credit leg from method AND status
--   (DEPOSITED→1000 bank, else CARD→1010 card clearing, else cash/cheque→1020 undeposited) and split a taxable
--   NSF fee into income + HST. 0032 seeded payment.bounced/{ar,bank,undeposited,fee_income}; the two missing
--   roles are added here so boot ValidatePostingRules covers every leg buildPaymentBounced can emit.
--     card_clearing → 1010: reverse a still-undeposited CARD receipt out of card settlement clearing (the leg
--                            the original payment.received DEBITED), not 1020.
--     hst           → 2200: the HST Payable portion of a taxable NSF fee (gross fee = fee income + HST).
INSERT INTO posting_rules (event_type, line_role, account_code) VALUES
  ('payment.bounced', 'card_clearing', '1010'),
  ('payment.bounced', 'hst',           '2200')
ON CONFLICT (event_type, line_role) WHERE branch_id IS NULL DO NOTHING;
