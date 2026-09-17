-- 0018: A-18 — promote in-use BisTrack Product UDFs to first-class product columns.
-- Source of truth for the column set: seed/udf_promote_list.csv (decoded by
-- cmd/decode_udf from bistrack_snapshot/extract). Decisions: D-023/D-024/D-027.
--
-- Finding (D-027): dbo.OrderLineUDF is empty of value columns (OrderLineID only);
-- the in-use UDFs are PRODUCT-level (dbo.Product.udf*). 10 net-new attrs promote
-- here; the long tail (udfSizing/udfLighting/udfRecommendedFor/udfFamilyValue) stays
-- in products.metadata JSONB; non-product UDFs drop.
--
-- Conventions: TEXT (CHECK-constrained, no ENUMs) · DECIMAL(19,4) quantities ·
-- DEFAULT '' / sane defaults so the migration is non-breaking on seeded rows.

ALTER TABLE products
  ADD COLUMN color_family                      TEXT    NOT NULL DEFAULT '',  -- udfProductColour (facet)
  ADD COLUMN texture                           TEXT    NOT NULL DEFAULT '',  -- udfTexture
  ADD COLUMN product_size                      TEXT    NOT NULL DEFAULT '',  -- udfProductSize (display label)
  ADD COLUMN sold_by                           TEXT    NOT NULL DEFAULT '',  -- udfSoldBy
  ADD COLUMN is_featured                       BOOLEAN NOT NULL DEFAULT FALSE, -- udfDibbitsFavourite
  ADD COLUMN is_special_order                  BOOLEAN NOT NULL DEFAULT FALSE, -- udfSpecialOrder
  ADD COLUMN contractor_pricing_event_discount BOOLEAN NOT NULL DEFAULT FALSE, -- udfContractorPricingEventDiscount (feeds W3)
  ADD COLUMN full_skid_quantity                INTEGER,                      -- udfFullSkidQuantity (coerce in M4 ETL)
  ADD COLUMN layer_quantity                    INTEGER,                      -- udfLayerQuantity (coerce in M4 ETL)
  ADD COLUMN minimum_order_qty                 DECIMAL(19,4);                -- udfMinimumQuantity

-- Facet indexes for the catalogue filter (finish/color/application already indexed in 0002).
CREATE INDEX idx_products_color_family ON products (color_family);
CREATE INDEX idx_products_texture      ON products (texture);

-- Order-line attribute (D-024): columns ship now; edit UI defers to the order module (W4).
ALTER TABLE order_lines
  ADD COLUMN lien_holdback_status TEXT NOT NULL DEFAULT 'NONE'
    CHECK (lien_holdback_status IN ('NONE','REQUIRED','EXEMPT'));
