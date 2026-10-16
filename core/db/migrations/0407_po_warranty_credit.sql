-- 0407_po_warranty_credit.sql
--
-- Buying and Stock Change 5 (S5 + S6): vendor warranty credit on purchase orders.
-- Ruling R3: unit prices stay the real prices; credit reduces net total due to vendor.
--
-- Adds warranty credit tracking to purchase orders:
--   warranty_credit_applied_cents: credit amount applied against this order
--   warranty_claim_id: the warranty claim providing the credit (references warranty_claims)
--   net_total_cents: order subtotal less applied warranty credit (total due to vendor)

ALTER TABLE purchasing_pos
  ADD COLUMN IF NOT EXISTS warranty_credit_applied_cents BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS warranty_claim_id UUID REFERENCES warranty_claims(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS net_total_cents BIGINT NOT NULL DEFAULT 0;

ALTER TABLE purchase_orders
  ADD COLUMN IF NOT EXISTS warranty_credit_applied_cents BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS warranty_claim_id UUID REFERENCES warranty_claims(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS net_total_cents BIGINT NOT NULL DEFAULT 0;
