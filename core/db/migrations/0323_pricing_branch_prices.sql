-- 0323: branch prices (HH-LOAD-1 LD-7, plan CP0-5 answer A). Owner module: internal/pricing.
-- A price per branch beside the dealer-wide anchor: at a branch the resolver
-- starts from that branch's price where one is set and from the dealer-wide
-- anchor where not, and every percent rule, quantity break and promotion
-- works from that start as it does today.
-- Conventions mirror 0023 and the module's own anchor table: UUID keys,
-- money BIGINT *_cents, price_uom CITEXT -> uoms(code), TIMESTAMPTZ,
-- explicit ON DELETE, additive only. The 0213 sync stamps (org_id, seq,
-- deleted_at, idempotency_key) are the same four columns pricing_list_prices
-- carries: the load doors stamp org and draw a seq per write, a delete is a
-- tombstone, and FB Move's derived key lands in the idempotency key column.
-- The key column stays nullable and non-unique on purpose (0213's own note):
-- uniqueness belongs to the load key registry, never to this column.
CREATE TABLE pricing_branch_prices (
  product_id       UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
  branch_id        UUID NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
  list_price_cents BIGINT NOT NULL CHECK (list_price_cents >= 0),
  price_uom        CITEXT NOT NULL REFERENCES uoms(code) ON DELETE RESTRICT,
  org_id           UUID,
  seq              BIGINT,
  deleted_at       TIMESTAMPTZ,
  idempotency_key  UUID,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (product_id, branch_id)
);
-- The list read filters by branch (the pricing editors' branch view); the
-- resolver's read is the primary key itself.
CREATE INDEX idx_pricing_branch_prices_branch ON pricing_branch_prices(branch_id);

-- A branch price change is traced like an anchor change: the trail row names
-- the branch, and carries the unit the price is per (a branch price may stand
-- in a larger unit, and 12000 per PLT is a different price from 12000 per EA,
-- so the trail says which one it records). Both nullable, so every anchor
-- trail row already written stands. The table is append-only (0039's
-- trigger), which an ADD COLUMN does not disturb.
ALTER TABLE price_change_log ADD COLUMN branch_id UUID REFERENCES locations(id) ON DELETE SET NULL;
ALTER TABLE price_change_log ADD COLUMN price_uom CITEXT REFERENCES uoms(code) ON DELETE RESTRICT;
