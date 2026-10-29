-- 0454: W2-1d item 3, a landed cost names its payee.
--
-- A landed cost is a charge the dealer owes a freight carrier or a customs
-- broker, but the header carried no payee: the AP aging could only label the
-- payable with the PO reference it rode, so the books showed
-- "Freight Carrier (lc-s1-0003)" where a real dealer's books name the carrier.
--
-- The header now names its payee vendor (the same vendors table the purchase
-- orders name, so the world's carriers and brokers are vendors like any
-- other). The column is nullable: the create route takes it, the aging labels
-- the payable with that vendor's name, and the PO reference stays the label's
-- fallback for rows written before this migration.

ALTER TABLE landed_costs
  ADD COLUMN IF NOT EXISTS payee_vendor_id UUID REFERENCES vendors(id) ON DELETE RESTRICT;

COMMENT ON COLUMN landed_costs.payee_vendor_id IS
  'the freight carrier or customs broker this charge is owed to; NULL on rows written before 0454, whose label falls back to the PO reference';

CREATE INDEX IF NOT EXISTS idx_landed_costs_payee ON landed_costs(payee_vendor_id);
