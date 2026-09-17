-- 0068: DIB-264 — cash over/short tolerance set to ±$3 (client answer, W8 UAT feedback).
-- till_policy.variance_threshold_cents was seeded PROVISIONAL 0 (any non-zero variance escalates to a
-- manager) in 0028_till_drawer_ops.sql, pending the float/reconciliation SOP (D-A03-1/2). On the
-- 2026-07-21 sync the client answered ±$3, so a blind-close variance within 300 cents no longer needs
-- manager sign-off. The enforcement mechanism already exists and reads this column — this migration is
-- data only.
--
-- Idempotent + fresh-migrate safe: only touches rows still at the provisional 0 (never clobbers a value
-- someone has since tuned via the admin console), and is a no-op if till_policy has no rows yet.
UPDATE till_policy
   SET variance_threshold_cents = 300, updated_at = now()
 WHERE variance_threshold_cents = 0;
