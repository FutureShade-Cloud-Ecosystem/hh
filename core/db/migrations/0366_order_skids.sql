-- 0366: the skid (pallet) count on an order, changeable by the yard with a reason, and the skid
-- deposit that follows from it. Owner: internal/order.
--
-- WHAT EXISTED. Nothing. "skid" appears in the core only on products (pallet_count,
-- full_skid_quantity, the PLT unit) and on vendor purchase units; no order recorded how many skids
-- it left the yard on, and no skid deposit charge existed anywhere (orders.deposit_required_cents
-- and quotes.deposit_pct, 0008 and 0173, are a payment deposit on the order's price, not a charge
-- for skids). The yard condenses skids and the office learned only when the customer complained.
--
-- WHAT THIS ADDS. order_skids holds the CURRENT count for an order, and the deposit charged per
-- skid (a rate, defaulting to zero: no rate exists anywhere in the core today, so the deposit is
-- zero until one is set, and nothing here invents one). deposit_cents is the count times the rate,
-- kept on the row so the amount a notice quotes is the amount stored. order_skid_changes is the
-- append-only history: who changed the count, from what to what, and why.
--
-- THE DEPOSIT IS RECORDED, NOT BILLED. No invoice or order document carries a skid deposit line
-- type, and adding one would change the invoice arithmetic every billing path is held to. The
-- amount is held here and reported when the count changes (it is what is owed back, or due, on a
-- paid order), and billing it is a later decision for the office.
--
-- Additive: two new tables, no change to an existing table, no backfill. An order with no row has
-- no recorded skid count, which is what it has always meant.

CREATE TABLE IF NOT EXISTS order_skids (
  order_id               UUID PRIMARY KEY REFERENCES orders(id) ON DELETE CASCADE,
  skid_count             INTEGER NOT NULL CHECK (skid_count >= 0),
  deposit_per_skid_cents BIGINT NOT NULL DEFAULT 0 CHECK (deposit_per_skid_cents >= 0),
  deposit_cents          BIGINT NOT NULL DEFAULT 0 CHECK (deposit_cents >= 0),
  updated_by             UUID,
  updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS order_skid_changes (
  id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  order_id              UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  count_before          INTEGER CHECK (count_before IS NULL OR count_before >= 0),
  count_after           INTEGER NOT NULL CHECK (count_after >= 0),
  deposit_before_cents  BIGINT NOT NULL DEFAULT 0 CHECK (deposit_before_cents >= 0),
  deposit_after_cents   BIGINT NOT NULL DEFAULT 0 CHECK (deposit_after_cents >= 0),
  reason                TEXT NOT NULL CHECK (reason IN ('CONDENSED_SKIDS', 'DAMAGED', 'NOT_IN_YARD', 'COUNT_CORRECTION', 'OTHER')),
  note                  TEXT NOT NULL DEFAULT '' CHECK (char_length(note) <= 400),
  changed_by            UUID,
  changed_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_order_skid_changes_order ON order_skid_changes (order_id, changed_at);

COMMENT ON TABLE order_skids IS
  '0366: the current skid count on an order and the skid deposit that follows (count times rate). Recorded, not billed.';
COMMENT ON TABLE order_skid_changes IS
  '0366: append-only history of skid count changes with the reason and who made them.';
