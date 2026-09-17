-- 0023: pricing engine tables (A-07 / W3). Owner module: internal/pricing.
-- Conventions: UUID PKs · money BIGINT *_cents · qty/thresholds DECIMAL(19,4) · UOM CITEXT->uoms(code)
--              discount as integer basis points (bps) · TIMESTAMPTZ · explicit ON DELETE · flag-don't-delete.
-- Trace: A-07 plan command/state/plans/A-07-plan-2026-06-29.md §5 step 1; ARCH_DELTA §1.
-- ZERO-TRUST: pricing OWNS only pricing_*/quantity_break_*/customer_category_overrides tables. It does NOT
--             ALTER catalog-owned product_categories/products — margin policy lives here as DATA (a row),
--             and category_id/product_id FKs are read-only constraints, not cross-module writes.

-- 1a. Margin policy as DATA in a PRICING-OWNED table (NOT an ALTER of catalog product_categories, and NOT
--     the rbac.go MarginFloorBps const). The floor lives as a ROW so a corrected value is an UPDATE, not an
--     edit to immutable SQL. Real policy (06-23 call): target 30% / soft basement 25% / hard ~20% enforced
--     floor; category adjuster on BOTH target and floor. Exact bps still OPEN — seed (0024) is PROVISIONAL.
CREATE TABLE pricing_category_policy (
  category_id                 UUID PRIMARY KEY REFERENCES product_categories(id) ON DELETE CASCADE,
  margin_target_bps           INTEGER NOT NULL CHECK (margin_target_bps BETWEEN 0 AND 10000),  -- 3000 = 30% target
  margin_floor_bps            INTEGER NOT NULL CHECK (margin_floor_bps  BETWEEN 0 AND 10000),  -- 2000 = ~20% HARD floor
  margin_enforcement_disabled BOOLEAN NOT NULL DEFAULT FALSE,  -- bulk sand/aggregate overhead: skip gate, STILL audit
  created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (margin_floor_bps <= margin_target_bps)
);
-- The MarginGate computes the EFFECTIVE floor (base ± category adjuster) from margin_floor_bps and passes it
-- to identity.CheckMarginAt(...,floorBps). Per-branch floor values DEFER (deferred-not-dropped) — this schema
-- does not preclude a later branch dimension.

-- 1a-bis. Per-PRODUCT enforcement disable (06-23 literal grain: "per product"; bulk-aggregate case).
--     Pricing-OWNED, keyed by product_id — never an ALTER of catalog-owned products.
CREATE TABLE pricing_product_policy (
  product_id                  UUID PRIMARY KEY REFERENCES products(id) ON DELETE CASCADE,
  margin_enforcement_disabled BOOLEAN NOT NULL DEFAULT FALSE,
  created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 1b. Retail-anchor (list price) source. Greenfield has no list price column; the resolver needs a base.
--     Per D-A07-2 this row IS the retail anchor (manufacturer GTA trade price + FOB freight when given,
--     else cost × 1.85). Catalog owns products, so pricing keeps its own anchor table (zero-trust).
CREATE TABLE pricing_list_prices (
  product_id       UUID PRIMARY KEY REFERENCES products(id) ON DELETE CASCADE,
  list_price_cents BIGINT NOT NULL CHECK (list_price_cents >= 0),
  price_uom        CITEXT NOT NULL REFERENCES uoms(code) ON DELETE RESTRICT,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 1c. The rule engine (BisTrack SellPriceRule retargeted).
CREATE TABLE pricing_rules (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  scope          TEXT NOT NULL CHECK (scope IN
                   ('CUSTOMER_PRODUCT','CUSTOMER_CATEGORY','TIER_CATEGORY','TIER_DEFAULT')),
  -- RESERVED-UNBUILT: CUSTOMER_SUPPLIER / SUPPLIER_CATEGORY are deliberately NOT in this CHECK and there is
  -- NO supplier_id column at gate — `products` has no supplier/vendor column (verified absent in 0002/0018),
  -- so the resolver cannot resolve a product's supplier. Building the supplier dimension is a post-LOCK
  -- prerequisite (catalog products.primary_supplier_id OR a purchasing product→vendor seam, Q#4).
  -- scope dimensions (all nullable; which are set depends on scope)
  customer_id    UUID REFERENCES customers(id) ON DELETE CASCADE,
  customer_type  TEXT CHECK (customer_type IN ('CONTRACTOR','RETAIL','BUILDER','GOVERNMENT','INTERNAL')),
  category_id    UUID REFERENCES product_categories(id) ON DELETE CASCADE,
  product_id     UUID REFERENCES products(id) ON DELETE CASCADE,
  branch_id      UUID REFERENCES locations(id) ON DELETE CASCADE,   -- NULL = all branches
  -- calculation
  calc_type      TEXT NOT NULL CHECK (calc_type IN ('ABSOLUTE','PCT_OFF_LIST','MARKUP_ON_COST')),
  price_cents    BIGINT,            -- set when calc_type = ABSOLUTE
  discount_bps   INTEGER,           -- set when PCT_OFF_LIST (e.g. 1800 = 18% off)
  markup_bps     INTEGER,           -- set when MARKUP_ON_COST (e.g. 3000 = cost +30%)
  price_uom      CITEXT NOT NULL REFERENCES uoms(code) ON DELETE RESTRICT,  -- BisTrack PricePerID
  -- selection metadata
  priority       INTEGER NOT NULL DEFAULT 0,    -- BisTrack RulePriority; higher wins within a scope
  valid_from     TIMESTAMPTZ,                   -- NULL = always
  valid_to       TIMESTAMPTZ,
  detail_text    TEXT NOT NULL DEFAULT '',      -- BisTrack ScreenText; human breakdown string
  is_active      BOOLEAN NOT NULL DEFAULT TRUE,
  version        INTEGER NOT NULL DEFAULT 1,    -- optimistic locking
  created_by     UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- exactly one calc value matches the calc_type
  CHECK ( (calc_type='ABSOLUTE'      AND price_cents IS NOT NULL AND discount_bps IS NULL AND markup_bps IS NULL)
       OR (calc_type='PCT_OFF_LIST'  AND discount_bps IS NOT NULL AND price_cents IS NULL AND markup_bps IS NULL)
       OR (calc_type='MARKUP_ON_COST'AND markup_bps   IS NOT NULL AND price_cents IS NULL AND discount_bps IS NULL) ),
  -- DISCOUNT-%-ONLY constraint (D-A07-2, 06-23: "never $/sq-ft"). An ABSOLUTE $/UOM price is allowed ONLY on
  -- CUSTOMER_PRODUCT (a negotiated fixed contract price) and on the RESERVED supplier/manufacturer absolute
  -- scopes; tier/customer-category/category scopes must be PCT_OFF_LIST.
  CHECK ( scope = 'CUSTOMER_PRODUCT' OR calc_type <> 'ABSOLUTE' )
);
CREATE INDEX idx_pricing_rules_customer  ON pricing_rules(customer_id)    WHERE is_active;
CREATE INDEX idx_pricing_rules_type      ON pricing_rules(customer_type)  WHERE is_active;
CREATE INDEX idx_pricing_rules_category  ON pricing_rules(category_id)    WHERE is_active;
CREATE INDEX idx_pricing_rules_product   ON pricing_rules(product_id)     WHERE is_active;
-- ANTI-COLLISION (determinism guard): at most one ACTIVE rule per identical selection key + priority +
-- valid_from, so the total-order tie-break (priority DESC, valid_from DESC, id ASC) never has to reach for
-- `id` between two genuinely-equivalent seeded rows. NULL scope-dims normalize to sentinels (Postgres treats
-- NULLs as distinct, so a bare UNIQUE would not block "two TIER_DEFAULTs same priority").
CREATE UNIQUE INDEX uq_pricing_rules_active ON pricing_rules (
  scope,
  COALESCE(customer_id,   '00000000-0000-0000-0000-000000000000'::uuid),
  COALESCE(customer_type, ''),
  COALESCE(category_id,   '00000000-0000-0000-0000-000000000000'::uuid),
  COALESCE(product_id,    '00000000-0000-0000-0000-000000000000'::uuid),
  COALESCE(branch_id,     '00000000-0000-0000-0000-000000000000'::uuid),
  priority,
  COALESCE(valid_from,    'epoch'::timestamptz)
) WHERE is_active;

-- 1d. Quantity breaks (BisTrack QuantityBreakHeader/Item).
CREATE TABLE quantity_break_headers (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code        CITEXT NOT NULL UNIQUE,
  name        TEXT NOT NULL,
  product_id  UUID REFERENCES products(id) ON DELETE CASCADE,  -- ProductSpecific
  category_id UUID REFERENCES product_categories(id) ON DELETE CASCADE,
  threshold_uom CITEXT NOT NULL DEFAULT 'EA' REFERENCES uoms(code) ON DELETE RESTRICT,
  -- ^ UOM that from_qty/to_qty are expressed in. A WEIGHT UOM (KG/T) supports the 06-23 partial-skid surcharge
  --   (natural stone < 4500 lb → charge MORE); the resolver buckets via the catalog ConvertUOM seam (NOT a
  --   direct product_uoms SELECT). Schema capability is at gate; the specific 4500-lb values DEFER.
  is_active   BOOLEAN NOT NULL DEFAULT TRUE,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (product_id IS NOT NULL OR category_id IS NOT NULL)
);
CREATE TABLE quantity_break_items (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  header_id   UUID NOT NULL REFERENCES quantity_break_headers(id) ON DELETE CASCADE,
  from_qty    DECIMAL(19,4) NOT NULL CHECK (from_qty >= 0),   -- thresholds in the header's threshold_uom
  to_qty      DECIMAL(19,4),                                  -- NULL = open-ended top tier
  -- ADDITIVE enum (06-23 partial-skid): SURCHARGE_* are POSITIVE adjustments (charge MORE under threshold).
  -- A different ruling on the exact surcharge needs no corrective migration — the enum just grows.
  value_type  TEXT NOT NULL CHECK (value_type IN
                ('ABSOLUTE','PCT_OFF_LIST','SURCHARGE_PCT','SURCHARGE_ABS')),  -- PriceValueType
  price_cents     BIGINT,    -- when ABSOLUTE (replacement price)
  discount_bps    INTEGER,   -- when PCT_OFF_LIST (off-list discount)
  surcharge_bps   INTEGER,   -- when SURCHARGE_PCT (positive % uplift)
  surcharge_cents BIGINT,    -- when SURCHARGE_ABS (positive flat uplift)
  price_uom   CITEXT NOT NULL REFERENCES uoms(code) ON DELETE RESTRICT,  -- PricePerID
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK ( (value_type='ABSOLUTE'      AND price_cents     IS NOT NULL AND discount_bps IS NULL AND surcharge_bps IS NULL AND surcharge_cents IS NULL)
       OR (value_type='PCT_OFF_LIST'  AND discount_bps    IS NOT NULL AND price_cents  IS NULL AND surcharge_bps IS NULL AND surcharge_cents IS NULL)
       OR (value_type='SURCHARGE_PCT' AND surcharge_bps   IS NOT NULL AND price_cents  IS NULL AND discount_bps  IS NULL AND surcharge_cents IS NULL)
       OR (value_type='SURCHARGE_ABS' AND surcharge_cents IS NOT NULL AND price_cents  IS NULL AND discount_bps  IS NULL AND surcharge_bps   IS NULL) )
);
CREATE INDEX idx_qbreak_items_header ON quantity_break_items(header_id);

-- 1e. Per-customer per-category override (Phase-0 account_pricing_overrides).
CREATE TABLE customer_category_overrides (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id  UUID NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
  category_id  UUID NOT NULL REFERENCES product_categories(id) ON DELETE CASCADE,
  calc_type    TEXT NOT NULL CHECK (calc_type IN ('ABSOLUTE','PCT_OFF_LIST','MARKUP_ON_COST')),
  price_cents  BIGINT,
  discount_bps INTEGER,
  markup_bps   INTEGER,
  price_uom    CITEXT NOT NULL REFERENCES uoms(code) ON DELETE RESTRICT,
  notes        TEXT NOT NULL DEFAULT '',
  version      INTEGER NOT NULL DEFAULT 1,
  created_by   UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (customer_id, category_id),
  CHECK ( (calc_type='ABSOLUTE'       AND price_cents IS NOT NULL)
       OR (calc_type='PCT_OFF_LIST'   AND discount_bps IS NOT NULL)
       OR (calc_type='MARKUP_ON_COST' AND markup_bps   IS NOT NULL) ),
  -- DISCOUNT-%-ONLY: this IS the customer+category (CUSTOMER_OVERRIDE) scope, so ABSOLUTE $/UOM is forbidden
  -- here too (06-23 "never $/sq-ft"); negotiated fixed prices belong on CUSTOMER_PRODUCT (pricing_rules).
  CHECK ( calc_type <> 'ABSOLUTE' )
);
CREATE INDEX idx_cust_cat_override_customer ON customer_category_overrides(customer_id);

-- 1f. Pricing audit log (Phase-0 pricing_audit_log).
CREATE TABLE pricing_audit_log (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  entity_type TEXT NOT NULL,     -- 'pricing_rule','quantity_break','customer_override','category_policy','margin_enforcement'
  entity_id   UUID NOT NULL,
  -- BYPASS = the margin gate was skipped at resolve time because margin_enforcement_disabled was set on the
  -- category (pricing_category_policy) or product (pricing_product_policy). NO SILENT BYPASS: a disabled item
  -- still writes a row here (entity_type='margin_enforcement', new_value=resulting margin).
  action      TEXT NOT NULL CHECK (action IN ('CREATE','UPDATE','DELETE','BYPASS')),
  field_name  TEXT,
  old_value   TEXT,
  new_value   TEXT,
  user_id     UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_pricing_audit_entity  ON pricing_audit_log(entity_type, entity_id);
CREATE INDEX idx_pricing_audit_created ON pricing_audit_log(created_at);
