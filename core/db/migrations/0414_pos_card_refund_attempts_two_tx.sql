-- 0414_pos_card_refund_attempts_two_tx.sql
--
-- HH-690: extend pos_card_refund_attempts for the two-transaction card refund
-- flow so one table holds every card refund not yet booked and reconciliation
-- keeps one place to look:
--   IN_FLIGHT  short tx 1 locked the sale, guarded the lines and staged the
--              attempt before calling the processor outside any DB transaction.
--   UNBOOKED   the processor refunded (refund_ref is set), or tx 2 rolled back
--              after the processor answered; the books do not show it yet.
--   BOOKED     short tx 2 booked the return and its refund payment row.
--   FAILED     the processor refused the refund before any money moved.
--
-- Owner module: internal/pos.

ALTER TABLE pos_card_refund_attempts
  ALTER COLUMN refund_ref SET DEFAULT '',
  DROP CONSTRAINT IF EXISTS pos_card_refund_attempts_status_check,
  ADD CONSTRAINT pos_card_refund_attempts_status_check
    CHECK (status IN ('IN_FLIGHT', 'UNBOOKED', 'BOOKED', 'FAILED')),
  ADD COLUMN IF NOT EXISTS attempted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  ADD COLUMN IF NOT EXISTS processor_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS booked_at    TIMESTAMPTZ;

DROP INDEX IF EXISTS idx_pos_card_refund_attempts_open;
CREATE INDEX idx_pos_card_refund_attempts_open
  ON pos_card_refund_attempts(status)
  WHERE status IN ('IN_FLIGHT', 'UNBOOKED');
