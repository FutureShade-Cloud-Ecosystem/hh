-- 0118: DIB-61 — index support for the inventory browse surface (A-18 UDF facets + trigram text).
--
-- The browse surface answers two questions in one round trip: "which stocked products match this
-- filter" and "how many would match each OTHER value of each facet" (the count badges). The second
-- is the expensive half — it is one aggregate per facet column over the filtered set — so the facet
-- columns need to be indexable and the text predicate needs to not be a sequential ILIKE.
--
-- WHAT ALREADY EXISTS (do not re-create): idx_products_color_family (0018), idx_products_collection
-- / _manufacturer / _color / _finish / _application (0002), and the A-18 catalog search support
-- from 0031. Only the gaps are added here.
--
-- pg_trgm is already installed (0031 enables it for the catalog fuzzy/by-SKU seam). The GIN index
-- below is what makes the browse text layer a real index scan rather than a table scan wearing a
-- similarity() costume: without it, `name ILIKE '%blu%'` on the catalogue degrades linearly and the
-- facet counts degrade with it, because every facet aggregate re-applies the same predicate.

-- Facet columns that had no index. is_featured / is_special_order are PARTIAL (indexing the false
-- majority of a boolean buys nothing and costs writes) — the facet only ever filters on true.
--
-- idx_products_texture IS DELIBERATELY ABSENT, and this is the interesting one. Migration 0021 (the
-- L-8 schema-invariants audit) DROPPED that index on the finding that "texture is promoted and
-- editable but is not a queryable facet", and internal/product's
-- TestIntegration_A18_PromotedColumnsMatchTheDecodeDecisions asserts by NAME that it never comes
-- back — its failure message reads "re-add it only if texture becomes a real facet".
--
-- DIB-61 is precisely that condition coming true: texture IS a queryable facet now. So the index is
-- justified — and re-adding it here would still be wrong. That assertion is a deliberate decision
-- recorded by the owner of internal/product, which is outside this bundle's fence, and the honest
-- move is to satisfy the assertion and ask, not to add the index and let another package's suite go
-- red at the union. Renaming the index to dodge a name-matched check would be worse: "a later
-- migration quietly resurrected a dead index" is the exact failure that assertion exists to catch.
--
-- PARKED FOR THE UNION (see REPORT-W4-B.md): re-add idx_products_texture together with the two-line
-- edit to internal/product/integration_test.go that flips the assertion from "must be absent" to
-- "must be present". Until then the texture facet filters with a sequential predicate, which on a
-- catalogue of this size costs nothing measurable — and the FACET COUNT never used a btree anyway
-- (it is a GROUP BY over the filtered set).
CREATE INDEX IF NOT EXISTS idx_products_product_size  ON products(product_size) WHERE product_size <> '';
CREATE INDEX IF NOT EXISTS idx_products_sold_by       ON products(sold_by)      WHERE sold_by      <> '';
CREATE INDEX IF NOT EXISTS idx_products_featured      ON products(id)           WHERE is_featured;
CREATE INDEX IF NOT EXISTS idx_products_special_order ON products(id)           WHERE is_special_order;

-- The browse text layer: one trigram index over the concatenation the surface actually searches
-- (sku + name + manufacturer + collection), so a single predicate is index-backed instead of four
-- ORed ILIKEs each needing their own index. IMMUTABLE-safe: every input is a plain column and the
-- concatenation uses COALESCE-free NOT NULL columns (sku is CITEXT NOT NULL, the rest default '').
CREATE INDEX IF NOT EXISTS idx_products_browse_trgm ON products
  USING GIN ((sku::text || ' ' || name || ' ' || manufacturer || ' ' || collection) gin_trgm_ops);

COMMENT ON INDEX idx_products_browse_trgm IS
  'DIB-61: trigram index backing the inventory browse text filter (sku/name/manufacturer/'
  'collection in one predicate). Facet count aggregates re-apply the same predicate, so this index '
  'is load-bearing for the count badges, not just the row list.';
