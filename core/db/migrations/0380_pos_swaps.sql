-- 0380_pos_swaps.sql
--
-- Sub item 10.5, the swap: a credit note across orders and a new order in ONE transaction. The
-- credit is spent on the new order (applied), what the new order does not take goes back by the
-- original tender (the leftover, a real refund on a rail), what it takes beyond the credit is paid
-- by an ordinary tender (the shortfall).
--
-- pos_returns.applied_cents is the part of a return's total that was applied to the swap's new
-- order instead of going out on the rail: the rail moves total less applied. Zero on every
-- return that is not a swap child. The card refund ceiling and the journal read it.
--
-- pos_swaps is the link between the two documents, one row per swap, both directions at once:
-- the credit note's id and the new order's id, each unique, with the swap's own figures and the
-- idempotency key a replay answers from.
--
-- Owner module: internal/pos.

ALTER TABLE pos_returns
  ADD COLUMN applied_cents BIGINT NOT NULL DEFAULT 0 CHECK (applied_cents >= 0 AND applied_cents <= total_cents);

CREATE TABLE pos_swaps (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  idempotency_key  TEXT NOT NULL UNIQUE,
  note_id          UUID NOT NULL UNIQUE REFERENCES return_notes(id) ON DELETE RESTRICT,
  order_id         UUID NOT NULL UNIQUE REFERENCES orders(id) ON DELETE RESTRICT,
  customer_id      UUID NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
  branch_id        UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  credit_cents     BIGINT NOT NULL CHECK (credit_cents > 0),
  applied_cents    BIGINT NOT NULL CHECK (applied_cents >= 0),
  new_total_cents  BIGINT NOT NULL CHECK (new_total_cents >= 0),
  leftover_cents   BIGINT NOT NULL CHECK (leftover_cents >= 0),
  shortfall_cents  BIGINT NOT NULL CHECK (shortfall_cents >= 0),
  leftover_method  TEXT NOT NULL CHECK (leftover_method IN ('CASH','CARD','ACCOUNT_CREDIT')),
  created_by       UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (applied_cents + leftover_cents = credit_cents),
  CHECK (applied_cents + shortfall_cents = new_total_cents)
);
CREATE INDEX idx_pos_swaps_customer ON pos_swaps(customer_id);
