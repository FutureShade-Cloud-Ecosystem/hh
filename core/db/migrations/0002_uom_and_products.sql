-- 0002: UoM system (as data, not enum) + product master + hardscape facets.
-- Spec: docs/design/DATA_MODEL.md §1–§2 (locked G-LOCK-004) · Traces: R6, R7, M1, M2, BT-A-005/006.
-- Conventions: UUID PKs · money BIGINT _cents · qty DECIMAL(19,4) · factors DECIMAL(19,8) (D-008)
--              TIMESTAMPTZ · explicit ON DELETE · no ENUMs · flag-don't-delete.

CREATE TABLE uoms (
  code        CITEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  dimension   TEXT NOT NULL CHECK (dimension IN ('count','area','weight','volume','length')),
  is_active   BOOLEAN NOT NULL DEFAULT TRUE,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO uoms (code, name, dimension) VALUES
  ('PC',  'Piece',         'count'),
  ('EA',  'Each',          'count'),
  ('LYR', 'Layer',         'count'),
  ('PLT', 'Pallet',        'count'),
  ('BOX', 'Box',           'count'),
  ('BAG', 'Bag',           'count'),
  ('SF',  'Square Foot',   'area'),
  ('M2',  'Square Metre',  'area'),
  ('LF',  'Linear Foot',   'length'),
  ('KG',  'Kilogram',      'weight'),
  ('T',   'Tonne',         'weight'),
  ('CYD', 'Cubic Yard',    'volume');

CREATE TABLE product_categories (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  parent_id  UUID REFERENCES product_categories(id) ON DELETE SET NULL,
  code       CITEXT NOT NULL UNIQUE,
  name       TEXT NOT NULL,
  is_active  BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE products (
  id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  sku                  CITEXT NOT NULL UNIQUE,
  name                 TEXT NOT NULL,
  description          TEXT NOT NULL DEFAULT '',
  category_id          UUID REFERENCES product_categories(id) ON DELETE SET NULL,
  base_uom             CITEXT NOT NULL REFERENCES uoms(code) ON DELETE RESTRICT,
  price_uom            CITEXT NOT NULL REFERENCES uoms(code) ON DELETE RESTRICT,
  manufacturer         TEXT NOT NULL DEFAULT '',
  collection           TEXT NOT NULL DEFAULT '',
  color                TEXT NOT NULL DEFAULT '',
  finish               TEXT NOT NULL DEFAULT '',
  application          TEXT NOT NULL DEFAULT '',
  dimensions_lwh       TEXT NOT NULL DEFAULT '',
  pallet_count         INTEGER,
  weight_per_unit_kg   DECIMAL(19,4),
  pieces_per_sf        DECIMAL(19,8),
  coverage_sf_per_unit DECIMAL(19,8),
  is_active            BOOLEAN NOT NULL DEFAULT TRUE,
  metadata             JSONB NOT NULL DEFAULT '{}',
  created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_products_category      ON products(category_id);
CREATE INDEX idx_products_manufacturer  ON products(manufacturer);
CREATE INDEX idx_products_collection    ON products(collection);
CREATE INDEX idx_products_color         ON products(color);
CREATE INDEX idx_products_application   ON products(application);
CREATE INDEX idx_products_finish        ON products(finish);
CREATE INDEX idx_products_mfr_coll      ON products(manufacturer, collection);
CREATE INDEX idx_products_active        ON products(is_active) WHERE is_active;

CREATE TABLE product_uoms (
  product_id   UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
  uom_code     CITEXT NOT NULL REFERENCES uoms(code) ON DELETE RESTRICT,
  qty_in_base  DECIMAL(19,8) NOT NULL CHECK (qty_in_base > 0),
  is_base      BOOLEAN NOT NULL DEFAULT FALSE,
  role_hints   TEXT[] NOT NULL DEFAULT '{}',
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (product_id, uom_code)
);

-- exactly one base row per product; base factor must be 1
CREATE UNIQUE INDEX ux_product_uoms_one_base ON product_uoms(product_id) WHERE is_base;
ALTER TABLE product_uoms ADD CONSTRAINT ck_base_factor_is_one
  CHECK (NOT is_base OR qty_in_base = 1);

CREATE TABLE product_rounding_rules (
  product_id             UUID PRIMARY KEY REFERENCES products(id) ON DELETE CASCADE,
  sell_increment_uom     CITEXT NOT NULL REFERENCES uoms(code) ON DELETE RESTRICT,
  break_pallet_allowed   BOOLEAN NOT NULL DEFAULT TRUE,
  break_pallet_fee_cents BIGINT NOT NULL DEFAULT 0,
  waste_factor_pct       DECIMAL(5,2) NOT NULL DEFAULT 0,
  created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);
