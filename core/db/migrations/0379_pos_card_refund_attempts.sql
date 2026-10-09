-- 0379_pos_card_refund_attempts.sql
--
-- Sub item 10.4, D4: a card refund is made at the processor INSIDE the return's database transaction.
-- If that transaction then rolls back (a later order of the same note fails, a unique violation, a
-- failing emit), the processor has refunded money the books never recorded, and nothing said so.
--
-- One row per processor refund whose booking did not commit, written on the pool AFTER the failed
-- transaction ended (never while it held a connection):
--   UNBOOKED  the processor refunded, the books hold nothing: a manager can see it.
--   BOOKED    the same refund key was retried and the retry committed: the books caught up. The
--             processor honors the refund's idempotency key, so the retry refunds nothing twice.
-- refund_key is the processor's own idempotency key ('refund:' plus the return's key), one row per
-- refund however many times it is retried.
--
-- Owner module: internal/pos.

CREATE TABLE pos_card_refund_attempts (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  refund_key   TEXT NOT NULL UNIQUE,
  order_id     UUID NOT NULL REFERENCES orders(id) ON DELETE RESTRICT,
  branch_id    UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  customer_id  UUID NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
  gateway_ref  TEXT NOT NULL,
  refund_ref   TEXT NOT NULL,
  amount_cents BIGINT NOT NULL CHECK (amount_cents > 0),
  status       TEXT NOT NULL CHECK (status IN ('UNBOOKED','BOOKED')),
  last_error   TEXT NOT NULL DEFAULT '',
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_pos_card_refund_attempts_open ON pos_card_refund_attempts(status) WHERE status = 'UNBOOKED';
