-- 0039: W6 A-08 — Price Change Log: batch manufacturer price updates with scheduled effective dates. Owner:
-- internal/pricing (extension). Conventions: money BIGINT *_cents · bps for rates/variance · TEXT+CHECK ·
-- append-only change log · every derivation anchor is a DB row (Scrubname0001-gap D-021). Manufacturer names are
-- INTERNAL-ONLY (never surfaced to the portal — rule 1); the import profiles key to the existing V-* vendors.

-- 1. Import profiles — the CSV column mapping per manufacturer vendor (seeded in dibbits_seed.sql for the 7
--    V-* manufacturers). mapping JSONB carries {sku_col, cost_col, retail_col, effective_col, ...}.
CREATE TABLE price_import_profiles (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  vendor_id     UUID NOT NULL REFERENCES vendors(id) ON DELETE CASCADE,
  name          TEXT NOT NULL,
  mapping       JSONB NOT NULL DEFAULT '{}',
  markup_bps    INTEGER NOT NULL DEFAULT 18500 CHECK (markup_bps >= 0), -- cost ×1.85 default (PLACEHOLDER)
  is_active     BOOLEAN NOT NULL DEFAULT TRUE,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (vendor_id, name)
);
CREATE INDEX idx_price_import_profiles_vendor ON price_import_profiles(vendor_id);

-- 2. Anchor policy — the retail derivation rule set (GTA trade + FOB band, cost×markup, variance alert). DATA,
--    branch_id NULL = org default. ALL VALUES ARE PLACEHOLDERS pending Scrubname0001's confirmation (D-021).
CREATE TABLE pricing_anchor_policy (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  branch_id          UUID UNIQUE REFERENCES locations(id) ON DELETE CASCADE, -- NULL = org default
  markup_bps         INTEGER NOT NULL DEFAULT 18500 CHECK (markup_bps >= 0), -- ×1.85
  fob_low_cents      BIGINT NOT NULL DEFAULT 40,   -- $0.40/sqft FOB band low (PLACEHOLDER)
  fob_high_cents     BIGINT NOT NULL DEFAULT 50,   -- $0.50/sqft FOB band high (PLACEHOLDER)
  variance_alert_bps INTEGER NOT NULL DEFAULT 1500 CHECK (variance_alert_bps >= 0), -- 15% outlier threshold
  notes              TEXT NOT NULL DEFAULT 'PLACEHOLDER — pending pricing-anchor confirmation (D-021)',
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO pricing_anchor_policy (branch_id, markup_bps, fob_low_cents, fob_high_cents, variance_alert_bps)
VALUES (NULL, 18500, 40, 50, 1500);

-- 3. Price batch — a staged manufacturer update with a scheduled effective date + maker-checker approval.
CREATE TABLE price_batches (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  batch_no       CITEXT NOT NULL UNIQUE,
  vendor_id      UUID REFERENCES vendors(id) ON DELETE SET NULL,
  profile_id     UUID REFERENCES price_import_profiles(id) ON DELETE SET NULL,
  status         TEXT NOT NULL DEFAULT 'DRAFT'
                   CHECK (status IN ('DRAFT','STAGED','APPROVED','APPLIED','CANCELLED')),
  effective_date DATE,
  source_ref     TEXT NOT NULL DEFAULT '',   -- import filename / origin
  line_count     INTEGER NOT NULL DEFAULT 0,
  outlier_count  INTEGER NOT NULL DEFAULT 0,
  created_by     UUID REFERENCES users(id) ON DELETE SET NULL,
  approved_by    UUID REFERENCES users(id) ON DELETE SET NULL,   -- separation of duties (maker <> checker)
  approved_at    TIMESTAMPTZ,
  applied_at     TIMESTAMPTZ,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_price_batches_status ON price_batches(status, effective_date);

-- 4. Price change lines — before/after per product with the outlier flag stamped at derivation time.
CREATE TABLE price_change_lines (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  batch_id          UUID NOT NULL REFERENCES price_batches(id) ON DELETE CASCADE,
  product_id        UUID REFERENCES products(id) ON DELETE SET NULL,
  sku               CITEXT NOT NULL DEFAULT '',
  before_cost_cents   BIGINT,
  after_cost_cents    BIGINT,
  before_retail_cents BIGINT,
  after_retail_cents  BIGINT,
  variance_bps      INTEGER,
  is_outlier        BOOLEAN NOT NULL DEFAULT FALSE,  -- stamped when |variance| exceeds the anchor threshold
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_price_change_lines_batch ON price_change_lines(batch_id);

-- 5. Price change log — append-only who/when/before/after audit of every applied price move (a mutation-in-
--    place would destroy the audit; a correction is a new row). Enforced append-only by the trigger below.
CREATE TABLE price_change_log (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  product_id        UUID REFERENCES products(id) ON DELETE SET NULL,
  batch_id          UUID REFERENCES price_batches(id) ON DELETE SET NULL,
  field             TEXT NOT NULL,                   -- 'cost_cents' | 'retail_cents'
  before_cents      BIGINT,
  after_cents       BIGINT,
  effective_date    DATE,
  changed_by        UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_price_change_log_product ON price_change_log(product_id, created_at DESC);

CREATE OR REPLACE FUNCTION price_change_log_no_update() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'price_change_log is append-only — a recorded price change cannot be edited';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_price_change_log_no_update ON price_change_log;
CREATE TRIGGER trg_price_change_log_no_update
  BEFORE UPDATE ON price_change_log
  FOR EACH ROW EXECUTE FUNCTION price_change_log_no_update();
