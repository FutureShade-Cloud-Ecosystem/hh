-- 0040: W6 A-15 — Inventory Counts: cycle counts with reason-coded GL adjustments + monthly recon. Owner:
-- internal/invcount. Conventions: quantities DECIMAL(19,4) · money BIGINT *_cents · maker-checker columns ·
-- reason→GL account mapping is DATA. Posts via the seeded inventory.adjusted rule (0007): the reason leg from
-- the reason-code mapping, the 1200 inventory leg resolved through gl.Service.ResolveRule.

-- 1. Reason codes — per-reason GL account for the variance adjustment (DAMAGE/SHRINK/FOUND/MISCOUNT/...).
--    account_code is the expense/contra leg; the inventory 1200 leg is the ResolveRule counterpart.
CREATE TABLE count_reason_codes (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code         CITEXT NOT NULL UNIQUE,
  description  TEXT NOT NULL DEFAULT '',
  account_code CITEXT NOT NULL REFERENCES gl_accounts(code) ON DELETE RESTRICT,
  is_active    BOOLEAN NOT NULL DEFAULT TRUE,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Seed the canonical reasons against the existing 5200 inventory-adjustment expense (0007 inventory.adjusted).
INSERT INTO count_reason_codes (code, description, account_code) VALUES
  ('DAMAGE',   'Damaged / broken stock write-down',      '5200'),
  ('SHRINK',   'Shrinkage / theft / unaccounted loss',   '5200'),
  ('FOUND',    'Found stock (positive adjustment)',      '5200'),
  ('MISCOUNT', 'Prior miscount correction',              '5200'),
  ('EXPIRY',   'Expired / obsolete write-off',           '5200')
ON CONFLICT (code) DO NOTHING;

-- 2. Count sheet — a snapshot-frozen cycle count for a branch (optionally scoped to a category). freeze_at
--    captures the on-hand the variance is measured against; maker-checker approval columns gate the post.
CREATE TABLE count_sheets (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  sheet_no     CITEXT NOT NULL UNIQUE,
  branch_id    UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  scope        TEXT NOT NULL DEFAULT 'FULL',   -- 'FULL' | 'CATEGORY:<id>' | 'AD_HOC'
  status       TEXT NOT NULL DEFAULT 'OPEN'
                 CHECK (status IN ('OPEN','FROZEN','COUNTED','POSTED','CANCELLED')),
  freeze_at    TIMESTAMPTZ,                    -- the snapshot instant (frozen on-hand basis)
  counted_by   UUID REFERENCES users(id) ON DELETE SET NULL,   -- maker
  approved_by  UUID REFERENCES users(id) ON DELETE SET NULL,   -- checker (must differ from maker)
  approved_at  TIMESTAMPTZ,
  posted_at    TIMESTAMPTZ,
  notes        TEXT NOT NULL DEFAULT '',
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_count_sheets_branch_status ON count_sheets(branch_id, status);

-- 3. Count line — snapshot on-hand vs counted qty, the variance, its reason, and the weighted-avg valuation.
CREATE TABLE count_lines (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  sheet_id         UUID NOT NULL REFERENCES count_sheets(id) ON DELETE CASCADE,
  product_id       UUID NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
  location_id      UUID REFERENCES locations(id) ON DELETE RESTRICT,
  snapshot_qty_base DECIMAL(19,4) NOT NULL DEFAULT 0,   -- frozen on-hand at freeze_at
  counted_qty_base  DECIMAL(19,4),                      -- NULL until counted
  variance_base     DECIMAL(19,4) NOT NULL DEFAULT 0,   -- counted − snapshot (signed)
  unit_cost_cents   BIGINT NOT NULL DEFAULT 0,          -- weighted-avg at post time
  variance_value_cents BIGINT NOT NULL DEFAULT 0,       -- variance_base × unit_cost (signed)
  reason_code      CITEXT REFERENCES count_reason_codes(code) ON DELETE RESTRICT,
  move_id          UUID REFERENCES stock_moves(id) ON DELETE SET NULL, -- the COUNT adjustment move it posted
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (sheet_id, product_id, location_id)
);
CREATE INDEX idx_count_lines_sheet ON count_lines(sheet_id);

-- 4. Monthly inventory→GL recon run (lazy current-month run-once). Ties the 1200 inventory subledger
--    (Σ weighted-avg on-hand) to the GL 1200 balance; delta surfaces on the recon surface.
CREATE TABLE inventory_recon_runs (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  branch_id          UUID REFERENCES locations(id) ON DELETE CASCADE, -- NULL = consolidated
  fiscal_year        INTEGER NOT NULL,
  month              INTEGER NOT NULL CHECK (month BETWEEN 1 AND 12),
  subledger_cents    BIGINT NOT NULL DEFAULT 0,
  gl_1200_cents      BIGINT NOT NULL DEFAULT 0,
  delta_cents        BIGINT NOT NULL DEFAULT 0,
  status             TEXT NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN','TIED','OUT_OF_TIE')),
  run_by             UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- One run per branch (or consolidated) per period — lazy run-once.
CREATE UNIQUE INDEX ux_inv_recon_default ON inventory_recon_runs(fiscal_year, month) WHERE branch_id IS NULL;
CREATE UNIQUE INDEX ux_inv_recon_branch  ON inventory_recon_runs(fiscal_year, month, branch_id) WHERE branch_id IS NOT NULL;
