-- 0041: W6 A-16 — Landed Cost: freight/duty/handling capitalized into product cost, incl. USD POs. Owner:
-- internal/landedcost. Conventions: money BIGINT *_cents · quantities DECIMAL(19,4) · TEXT+CHECK.
--
-- DX-19 SCOPED EXCEPTION: a PO / landed-cost DOCUMENT may be denominated in USD with a MANUAL fx_rate; the
-- amounts are converted to CAD before they ever touch the GL. GL + payments stay CAD-only (the currency-scan
-- gate-suite invariant ALLOWLISTS exactly purchase_orders + landed_costs + payment_config — nothing else may
-- carry a currency column). fx_rate is a DECIMAL multiplier (USD_cents × fx_rate = CAD_cents).

-- 1. purchase_orders gains the DX-19 currency + manual FX (default CAD / 1.0, so every existing row is CAD).
ALTER TABLE purchase_orders
  ADD COLUMN IF NOT EXISTS currency TEXT NOT NULL DEFAULT 'CAD'
    CHECK (currency IN ('CAD','USD')),
  ADD COLUMN IF NOT EXISTS fx_rate DECIMAL(12,6) NOT NULL DEFAULT 1.0 CHECK (fx_rate > 0);

-- 2. Landed cost — freight/duty/handling attached to a PO receipt, allocated across the received lines and
--    capitalized into the weighted-average cost. USD amounts convert to CAD via the manual fx_rate; the *_cad
--    columns are what the inventory valuation + GL ever see.
CREATE TABLE landed_costs (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  po_id            UUID REFERENCES purchase_orders(id) ON DELETE RESTRICT,
  po_ref           CITEXT NOT NULL DEFAULT '',    -- denormalized po_no for display / late-arriving docs
  branch_id        UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  currency         TEXT NOT NULL DEFAULT 'CAD' CHECK (currency IN ('CAD','USD')),
  fx_rate          DECIMAL(12,6) NOT NULL DEFAULT 1.0 CHECK (fx_rate > 0),
  freight_cents    BIGINT NOT NULL DEFAULT 0,     -- in `currency`
  duty_cents       BIGINT NOT NULL DEFAULT 0,
  handling_cents   BIGINT NOT NULL DEFAULT 0,
  freight_cad_cents  BIGINT NOT NULL DEFAULT 0,   -- converted to CAD (what the GL/valuation reads)
  duty_cad_cents     BIGINT NOT NULL DEFAULT 0,
  handling_cad_cents BIGINT NOT NULL DEFAULT 0,
  allocation_basis TEXT NOT NULL DEFAULT 'VALUE'
                     CHECK (allocation_basis IN ('WEIGHT','VALUE','QUANTITY')),
  exclude_special_buys BOOLEAN NOT NULL DEFAULT TRUE,   -- special buys don't absorb landed cost (gl_policy)
  status           TEXT NOT NULL DEFAULT 'DRAFT'
                     CHECK (status IN ('DRAFT','ALLOCATED','POSTED','VOIDED','RETRO_ADJUSTED')),
  retro_of_id      UUID REFERENCES landed_costs(id) ON DELETE SET NULL,  -- late-freight retro-adjustment link
  gl_journal_id    UUID REFERENCES gl_journals(id) ON DELETE RESTRICT,
  created_by       UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_landed_costs_po ON landed_costs(po_id);
CREATE INDEX idx_landed_costs_branch_status ON landed_costs(branch_id, status);

-- 3. Landed cost allocation — the per-product-line share of the landed cost, capitalized into that line's
--    inventory value (largest-remainder split by allocation_basis; always CAD).
CREATE TABLE landed_cost_allocations (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  landed_cost_id   UUID NOT NULL REFERENCES landed_costs(id) ON DELETE CASCADE,
  po_line_id       UUID REFERENCES po_lines(id) ON DELETE SET NULL,
  product_id       UUID REFERENCES products(id) ON DELETE SET NULL,
  basis_weight     DECIMAL(19,4) NOT NULL DEFAULT 0,   -- the weight used for the split (value/qty/kg)
  allocated_cad_cents BIGINT NOT NULL DEFAULT 0,
  sold_through_cad_cents BIGINT NOT NULL DEFAULT 0,    -- retro: the portion that hit COGS already
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_landed_cost_alloc_lc ON landed_cost_allocations(landed_cost_id);
