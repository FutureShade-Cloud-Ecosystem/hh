-- 0415_pos_card_refund_attempts_resolution.sql
--
-- HH-767 (HH-690b): record who started a card refund attempt and who resolved
-- an open (IN_FLIGHT or UNBOOKED) attempt after checking the processor, so
-- separation of duties (a manager at the sale's branch, never the cashier who
-- started the refund) holds across restarts.
--
-- Owner module: internal/pos.

ALTER TABLE pos_card_refund_attempts
  ADD COLUMN IF NOT EXISTS initiated_by    TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS resolved_by     TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS resolved_at     TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS resolution_note TEXT NOT NULL DEFAULT '';

-- One processor reversal ref rides at most one attempt (PR #501 round 1
-- P1-2): the settle refuses a ref already carried by another row, and this
-- partial index backs the rule at the table itself (the empty placeholder
-- never conflicts).
CREATE UNIQUE INDEX IF NOT EXISTS pos_card_refund_attempts_refund_ref_key
  ON pos_card_refund_attempts (refund_ref)
  WHERE refund_ref <> '';

-- The attempt's own resolution history (PR #501 round 1 P2-2): every settle
-- and every same-key restage after a settle appends one event, so the restage
-- blanking resolved_by, resolved_at and resolution_note never erases who
-- judged the processor outcome and why.
ALTER TABLE pos_card_refund_attempts
  ADD COLUMN IF NOT EXISTS resolution_log JSONB NOT NULL DEFAULT '[]'::jsonb;
